// The request path (spec §20): every generated client function calls http.send, which turns a
// response's status into an Outcome. Nothing here throws on an HTTP status.
import {
  createClient,
  type Client,
  type Method,
  type Notice,
  type Outcome,
  type Transport,
} from "./gen";

const noticeListeners = new Set<(notices: Notice[]) => void>();
const unpairedListeners = new Set<() => void>();
const stoppingListeners = new Set<() => void>();

/** Calls listener with the notices of every response that carries some, and of each stream notice. */
export function onNotices(listener: (notices: Notice[]) => void): () => void {
  noticeListeners.add(listener);
  return () => {
    noticeListeners.delete(listener);
  };
}

/** Calls listener when a request or the stream is answered 401: the device is no longer paired. */
export function onUnpaired(listener: () => void): () => void {
  unpairedListeners.add(listener);
  return () => {
    unpairedListeners.delete(listener);
  };
}

/** Calls listener when a request is answered 503 with the daemon's own stopping text. */
export function onStopping(listener: () => void): () => void {
  stoppingListeners.add(listener);
  return () => {
    stoppingListeners.delete(listener);
  };
}

/** Hands notices to the onNotices listeners; a listener that throws does not stop the rest. */
export function emitNotices(notices: Notice[]): void {
  for (const listener of [...noticeListeners]) {
    try {
      listener(notices);
    } catch (err) {
      console.error("loom: a notices listener failed", err);
    }
  }
}

/** Tells the onUnpaired listeners; a listener that throws does not stop the rest. */
export function emitUnpaired(): void {
  for (const listener of [...unpairedListeners]) {
    try {
      listener();
    } catch (err) {
      console.error("loom: an unpaired listener failed", err);
    }
  }
}

/** Tells the onStopping listeners, as a 503 with the stopping text does; a listener that throws does not stop the rest. */
export function emitStopping(): void {
  for (const listener of [...stoppingListeners]) {
    try {
      listener();
    } catch (err) {
      console.error("loom: a stopping listener failed", err);
    }
  }
}

// What a daemon that is stopping, or that the web layer has lost, answers a request with: the text
// of core.ErrUnavailable (the Go message may carry a reason after it) and of the stream hub's
// "loom is stopping". Every 503 is a `stopping` outcome, but another reason (the push key's "push
// is not available", the web layer being off) is not a restart, and is neither retried nor shown
// as RESTARTING.
const STOPPING_TEXTS = ["the loom daemon is unavailable", "loom is stopping"];

/** Whether a 503's error text is the daemon's own stopping text. */
export function isStoppingText(message: string): boolean {
  return STOPPING_TEXTS.some((text) => message.includes(text));
}

const UNREACHABLE = "Can't reach loom.";
const FALLBACK = "That didn't work.";

// errorText is the answer's `error`, else the classic app's fallback.
async function errorText(res: Response): Promise<string> {
  try {
    const body: unknown = await res.json();
    if (
      typeof body === "object" &&
      body !== null &&
      "error" in body &&
      typeof body.error === "string" &&
      body.error !== ""
    ) {
      return body.error;
    }
  } catch {
    // no JSON body: the fallback below
  }
  return FALLBACK;
}

// noticesOf is the body's `notices` list, else an empty one.
function noticesOf(body: unknown): Notice[] {
  if (
    typeof body === "object" &&
    body !== null &&
    "notices" in body &&
    Array.isArray(body.notices)
  ) {
    return body.notices as Notice[];
  }
  return [];
}

async function send<T>(
  method: Method,
  path: string,
  query: Record<string, string> | undefined,
  body: unknown,
): Promise<Outcome<T>> {
  const params = query === undefined ? "" : new URLSearchParams(query).toString();
  const url = params === "" ? path : `${path}?${params}`;
  const headers: Record<string, string> = {};
  const init: RequestInit = { method, credentials: "same-origin", headers };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }

  let res: Response;
  try {
    res = await fetch(url, init);
  } catch {
    return { kind: "failed", status: 0, message: UNREACHABLE };
  }

  switch (res.status) {
    case 200:
    case 201:
    case 204: {
      let value: unknown;
      let notices: Notice[] = [];
      if (res.status !== 204) {
        try {
          value = await res.json();
        } catch {
          return { kind: "failed", status: res.status, message: "Unreadable answer from loom." };
        }
        notices = noticesOf(value);
      }
      if (notices.length > 0) emitNotices(notices);
      return { kind: "ok", status: res.status, value: value as T, notices };
    }
    case 202:
      return { kind: "pending" };
    case 401:
      emitUnpaired();
      return { kind: "unpaired" };
    case 503: {
      const message = await errorText(res);
      if (isStoppingText(message)) emitStopping();
      return { kind: "stopping", message };
    }
    case 504:
      return { kind: "timeout", message: await errorText(res) };
    case 400:
    case 404:
    case 405:
    case 409:
    case 429:
      return { kind: "refused", status: res.status, message: await errorText(res) };
    default:
      return { kind: "failed", status: res.status, message: await errorText(res) };
  }
}

export const http: Transport = { send };

export const api: Client = createClient(http);
