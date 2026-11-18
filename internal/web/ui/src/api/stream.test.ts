import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Snapshot } from "./gen";
import { onUnpaired } from "./request";
import { connectStream, type LinkState, type StreamHandlers } from "./stream";

const fetchMock = vi.fn<typeof fetch>();
const closers: (() => void)[] = [];

// A server-sent-events connection the test drives: fetch resolves with a response whose reader
// yields what push() gives it, ends on end(), and fails when the request's signal aborts.
interface FakeConnection {
  readonly aborted: boolean;
  push(text: string): void;
  end(): void;
}

function nextConnection(): FakeConnection {
  const encoder = new TextEncoder();
  const chunks: ReadableStreamReadResult<Uint8Array>[] = [];
  let waiting: {
    resolve: (r: ReadableStreamReadResult<Uint8Array>) => void;
    reject: (e: unknown) => void;
  } | null = null;
  let aborted = false;

  const deliver = (result: ReadableStreamReadResult<Uint8Array>) => {
    if (waiting) {
      const w = waiting;
      waiting = null;
      w.resolve(result);
    } else {
      chunks.push(result);
    }
  };
  const reader = {
    read: () =>
      new Promise<ReadableStreamReadResult<Uint8Array>>((resolve, reject) => {
        const next = chunks.shift();
        if (next) resolve(next);
        else if (aborted) reject(new DOMException("aborted", "AbortError"));
        else waiting = { resolve, reject };
      }),
  };

  fetchMock.mockImplementationOnce((_input, init) => {
    init?.signal?.addEventListener("abort", () => {
      aborted = true;
      if (waiting) {
        const w = waiting;
        waiting = null;
        w.reject(new DOMException("aborted", "AbortError"));
      }
    });
    return Promise.resolve({
      ok: true,
      status: 200,
      body: { getReader: () => reader },
    } as unknown as Response);
  });

  return {
    get aborted() {
      return aborted;
    },
    push: (text) => {
      deliver({ done: false, value: encoder.encode(text) });
    },
    end: () => {
      deliver({ done: true, value: undefined });
    },
  };
}

function handlers() {
  const events: [string, unknown][] = [];
  const links: LinkState[] = [];
  const h: StreamHandlers = {
    onEvent: (name, data) => {
      events.push([name, data]);
    },
    onLink: (state) => {
      links.push(state);
    },
  };
  return { h, events, links };
}

function start(h: StreamHandlers): () => void {
  const close = connectStream(h);
  closers.push(close);
  return close;
}

// distinct collapses consecutive repeats: the link says "restarting" at the bye and again when
// the stream ends, which is one state.
const distinct = (links: LinkState[]) => links.filter((state, i) => state !== links[i - 1]);

const frame = (event: string, data: unknown) =>
  `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
const snapshot = { build: "b1" } as unknown as Snapshot;

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
}

const settle = () => vi.advanceTimersByTimeAsync(0);

beforeEach(() => {
  vi.useFakeTimers();
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
  setVisibility("visible");
});

afterEach(() => {
  for (const close of closers.splice(0)) close();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("connectStream", () => {
  it("opens the stream with the session cookie and no cache, and says connecting", async () => {
    nextConnection();
    const { h, links } = handlers();
    start(h);
    await settle();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/stream",
      expect.objectContaining({
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "text/event-stream" },
      }),
    );
    expect(links).toEqual(["connecting"]);
  });

  it("delivers each known event, and says live once the snapshot has arrived", async () => {
    const conn = nextConnection();
    const { h, events, links } = handlers();
    start(h);
    await settle();
    conn.push(frame("snapshot", snapshot));
    await settle();
    conn.push(frame("sessions", { workspace: 1, sessions: [], badge: 0 }));
    await settle();
    expect(events).toEqual([
      ["snapshot", snapshot],
      ["sessions", { workspace: 1, sessions: [], badge: 0 }],
    ]);
    expect(links).toEqual(["connecting", "live"]);
  });

  it("reassembles an event split across chunks and skips comments, unknown events and bad JSON", async () => {
    const conn = nextConnection();
    const { h, events } = handlers();
    start(h);
    await settle();
    conn.push(": ping\n\n");
    conn.push("event: mystery\ndata: {}\n\n");
    conn.push("event: device\ndata: {not json\n\n");
    conn.push("event: notice\nda");
    conn.push('ta: {"text":"hi","error":false,"at":"t"}\n\n');
    conn.push('event: quick_replies\r\ndata: {"quick_replies":["yes"]}\r\n\r\n');
    await settle();
    expect(events).toEqual([
      ["notice", { text: "hi", error: false, at: "t" }],
      ["quick_replies", { quick_replies: ["yes"] }],
    ]);
  });

  it("keeps reading after a handler throws", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const conn = nextConnection();
    const seen: string[] = [];
    start({
      onEvent: (name) => {
        seen.push(name);
        if (name === "device") throw new Error("handler bug");
      },
      onLink: () => undefined,
    });
    await settle();
    conn.push(frame("device", {}) + frame("usage", { usage: [] }));
    await settle();
    expect(seen).toEqual(["device", "usage"]);
  });

  it("says restarting after a bye and reconnects a second after the stream ends", async () => {
    const first = nextConnection();
    const second = nextConnection();
    const { h, links } = handlers();
    start(h);
    await settle();
    first.push(frame("snapshot", snapshot) + frame("bye", {}));
    first.end();
    await settle();
    expect(distinct(links)).toEqual(["connecting", "live", "restarting"]);
    await vi.advanceTimersByTimeAsync(999);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    second.push(frame("snapshot", snapshot));
    await settle();
    expect(distinct(links)).toEqual(["connecting", "live", "restarting", "live"]);
  });

  it("says offline when the stream ends with no bye before it", async () => {
    const conn = nextConnection();
    const { h, links } = handlers();
    start(h);
    await settle();
    conn.push(frame("snapshot", snapshot));
    conn.end();
    await settle();
    expect(distinct(links)).toEqual(["connecting", "live", "offline"]);
  });

  it("says restarting for 30 s after a bye, then offline", async () => {
    const first = nextConnection();
    fetchMock.mockRejectedValue(new TypeError("down"));
    const { h, links } = handlers();
    start(h);
    await settle();
    first.push(frame("snapshot", snapshot) + frame("bye", {}));
    first.end();
    await settle();
    await vi.advanceTimersByTimeAsync(15_000);
    expect(links.at(-1)).toBe("restarting");
    await vi.advanceTimersByTimeAsync(16_000);
    expect(links.at(-1)).toBe("offline");
  });

  it("backs off 1, 2, 4, 8, 16 and then 30 seconds between failed attempts", async () => {
    fetchMock.mockRejectedValue(new TypeError("down"));
    const { h } = handlers();
    start(h);
    await settle();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    let calls = 1;
    for (const delay of [1000, 2000, 4000, 8000, 16_000, 30_000]) {
      await vi.advanceTimersByTimeAsync(delay - 1);
      expect(fetchMock).toHaveBeenCalledTimes(calls);
      await vi.advanceTimersByTimeAsync(1);
      calls += 1;
      expect(fetchMock).toHaveBeenCalledTimes(calls);
    }
  });

  it("starts the backoff over once a connection opens", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("down"));
    fetchMock.mockRejectedValueOnce(new TypeError("down"));
    const third = nextConnection();
    fetchMock.mockRejectedValue(new TypeError("down"));
    const { h } = handlers();
    start(h);
    await settle();
    await vi.advanceTimersByTimeAsync(1000);
    await vi.advanceTimersByTimeAsync(2000);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    third.end();
    await settle();
    await vi.advanceTimersByTimeAsync(999);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("drops a stream silent for 45 s, and a ping keeps it alive", async () => {
    const conn = nextConnection();
    const { h, links } = handlers();
    start(h);
    await settle();
    await vi.advanceTimersByTimeAsync(40_000);
    conn.push(": ping\n\n");
    await settle();
    await vi.advanceTimersByTimeAsync(44_999);
    expect(conn.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(conn.aborted).toBe(true);
    expect(links.at(-1)).toBe("offline");
  });

  it("refreshes a live stream when the page becomes visible, without saying offline", async () => {
    const first = nextConnection();
    const second = nextConnection();
    const { h, links } = handlers();
    start(h);
    await settle();
    first.push(frame("snapshot", snapshot));
    await settle();
    document.dispatchEvent(new Event("visibilitychange"));
    await settle();
    expect(first.aborted).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    second.push(frame("snapshot", snapshot));
    await settle();
    expect(links).not.toContain("offline");
  });

  it("leaves a live stream alone while the page is hidden", async () => {
    const conn = nextConnection();
    const { h } = handlers();
    start(h);
    await settle();
    conn.push(frame("snapshot", snapshot));
    await settle();
    setVisibility("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    await settle();
    expect(conn.aborted).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("reconnects at once when the phone comes online during a backoff", async () => {
    fetchMock.mockRejectedValue(new TypeError("down"));
    const { h } = handlers();
    start(h);
    await settle();
    await vi.advanceTimersByTimeAsync(100);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    window.dispatchEvent(new Event("online"));
    await settle();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("reports a 401 as unpaired once and never reconnects", async () => {
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);
    fetchMock.mockResolvedValueOnce({ ok: false, status: 401, body: null } as unknown as Response);
    const { h, links } = handlers();
    start(h);
    await settle();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(unpaired).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(links).toEqual(["connecting"]);
    off();
  });

  it("stops reading, reconnecting and refreshing once closed", async () => {
    const conn = nextConnection();
    const { h } = handlers();
    const close = start(h);
    await settle();
    conn.push(frame("snapshot", snapshot));
    await settle();
    expect(vi.getTimerCount()).toBe(1);
    const removed = vi.spyOn(document, "removeEventListener");
    const removedFromWindow = vi.spyOn(window, "removeEventListener");
    close();
    // Synchronously: the watchdog is cleared by close itself, not by the abort it causes.
    expect(vi.getTimerCount()).toBe(0);
    expect(removed).toHaveBeenCalledWith("visibilitychange", expect.any(Function));
    expect(removedFromWindow).toHaveBeenCalledWith("online", expect.any(Function));
    await settle();
    expect(conn.aborted).toBe(true);
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(100_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not reconnect when closed during a backoff", async () => {
    fetchMock.mockRejectedValue(new TypeError("down"));
    const { h } = handlers();
    const close = start(h);
    await settle();
    expect(vi.getTimerCount()).toBe(1);
    close();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(100_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
