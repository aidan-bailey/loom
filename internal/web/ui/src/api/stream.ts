// The event stream (spec §25): GET /api/v1/stream with the classic app's rules. It reconnects with
// a backoff from 1 s to 30 s, drops a stream silent for 45 s, opens a fresh one whenever the page
// becomes visible, and reports the link's state for the footer.
import type { StreamEvents } from "./gen";
import { emitUnpaired } from "./request";

export type LinkState = "connecting" | "live" | "restarting" | "offline";

export interface StreamHandlers {
  onEvent: <E extends keyof StreamEvents>(name: E, data: StreamEvents[E]) => void;
  onLink: (state: LinkState) => void;
}

const FIRST_DELAY = 1000;
const MAX_DELAY = 30_000;
// The server pings every 20 s; a stream silent for more than twice that is dead, whatever the
// phone's network layer believes.
const SILENCE = 45_000;
// How long after a bye the link says "restarting": a normal restart reconnects within it, so the
// link never flashes "offline"; a daemon that stays away longer is plainly unreachable.
export const RESTART_GRACE = 30_000;

// Every event the stream carries, one key per StreamEvents key (the compiler holds the two in
// step): a name outside it is ignored, so a newer daemon's event does no harm.
const KNOWN: Record<keyof StreamEvents, true> = {
  snapshot: true,
  sessions: true,
  workspaces: true,
  usage: true,
  quick_replies: true,
  terminal: true,
  device: true,
  notice: true,
  ping: true,
  bye: true,
};

function isKnown(name: string): name is keyof StreamEvents {
  return Object.keys(KNOWN).includes(name);
}

/**
 * Opens the stream and calls handlers.onEvent for each event, and handlers.onLink as the link
 * changes: connecting at the start, live once a snapshot has arrived, restarting after a bye,
 * offline when the stream fails. It reconnects with a backoff from 1 s to 30 s, and at once when
 * the phone comes online. When the page becomes visible again it drops a live stream and opens a
 * fresh one without saying offline: a page iOS resumes can sit on a stream that delivers nothing,
 * and the fresh snapshot replaces the view. On a 401 it tells the onUnpaired listeners and stops.
 * It returns the function that closes it.
 */
export function connectStream(handlers: StreamHandlers): () => void {
  let closed = false;
  let running = false;
  let live = false;
  let refreshing = false;
  let delay = FIRST_DELAY;
  let byeAt = 0;
  let controller: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let watchdog: ReturnType<typeof setTimeout> | undefined;

  function guarded(what: string, call: () => void): void {
    try {
      call();
    } catch (err) {
      console.error(`loom: ${what} failed`, err);
    }
  }

  function link(state: LinkState): void {
    guarded("the link handler", () => {
      handlers.onLink(state);
    });
  }

  function receive(name: keyof StreamEvents, data: unknown): void {
    guarded(`the ${name} handler`, () => {
      handlers.onEvent(name, data as never);
    });
    if (name === "snapshot") {
      byeAt = 0;
      link("live");
    } else if (name === "bye") {
      byeAt = Date.now();
      link("restarting");
    }
  }

  function dispatch(block: string): void {
    let event = "message";
    const data: string[] = [];
    for (const line of block.split("\n")) {
      if (line === "" || line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const field = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") event = value;
      else if (field === "data") data.push(value);
    }
    if (data.length === 0 || !isKnown(event)) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(data.join("\n"));
    } catch {
      return;
    }
    receive(event, parsed);
  }

  async function session(): Promise<void> {
    const mine = new AbortController();
    controller = mine;
    const arm = () => {
      clearTimeout(watchdog);
      watchdog = setTimeout(() => {
        mine.abort();
      }, SILENCE);
    };
    try {
      arm();
      const res = await fetch("/api/v1/stream", {
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "text/event-stream" },
        signal: mine.signal,
      });
      if (res.status === 401) {
        closed = true;
        emitUnpaired();
        return;
      }
      if (!res.ok || !res.body) throw new Error(`stream answered ${res.status}`);
      live = true;
      delay = FIRST_DELAY;
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        arm();
        buf += decoder.decode(value, { stream: true }).replace(/\r/g, "");
        for (let end = buf.indexOf("\n\n"); end >= 0; end = buf.indexOf("\n\n")) {
          dispatch(buf.slice(0, end));
          buf = buf.slice(end + 2);
        }
      }
    } catch {
      // The stream is down; run() reconnects.
    } finally {
      clearTimeout(watchdog);
      live = false;
    }
  }

  async function run(): Promise<void> {
    if (closed || running) return;
    running = true;
    try {
      await session();
    } finally {
      running = false;
    }
    if (closed) return;
    if (refreshing) {
      // onVisible dropped it on purpose: open the next one now, quietly.
      refreshing = false;
      delay = FIRST_DELAY;
      void run();
      return;
    }
    link(byeAt !== 0 && Date.now() - byeAt <= RESTART_GRACE ? "restarting" : "offline");
    clearTimeout(timer);
    timer = setTimeout(() => {
      void run();
    }, delay);
    delay = Math.min(delay * 2, MAX_DELAY);
  }

  function wake(): void {
    if (closed || running || live) return;
    clearTimeout(timer);
    delay = FIRST_DELAY;
    void run();
  }

  // onVisible refreshes a live stream, which may be dead without a sign the watchdog would see for
  // up to SILENCE, and wakes one that is down.
  function onVisible(): void {
    if (document.visibilityState !== "visible") return;
    if (live && controller) {
      refreshing = true;
      controller.abort();
      return;
    }
    wake();
  }

  document.addEventListener("visibilitychange", onVisible);
  window.addEventListener("online", wake);
  link("connecting");
  void run();

  return () => {
    closed = true;
    clearTimeout(timer);
    clearTimeout(watchdog);
    document.removeEventListener("visibilitychange", onVisible);
    window.removeEventListener("online", wake);
    controller?.abort();
  };
}
