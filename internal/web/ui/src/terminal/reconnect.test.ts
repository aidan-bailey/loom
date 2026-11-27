import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  FIRST_DELAY,
  LONG_HIDDEN,
  MAX_DELAY,
  RIS,
  SIZE_SETTLE,
  STABLE_LIVE,
  createLink,
  freshScreen,
  lastCall,
  nextDelay,
  reconnects,
  type Link,
  type LinkState,
  type ScreenSink,
} from "./reconnect";
import type { Handlers, TerminalSocket } from "./socket";

// One connection the link opened: it records what the link does to it and plays the daemon's
// side. close() reports a normal close at once, as the real socket does, so a test sees whether the
// link ignores a replaced connection's late events.
class Conn {
  closes = 0;
  sent: Uint8Array[] = [];
  accepts = true;
  readonly sock: TerminalSocket;

  constructor(private readonly on: Handlers) {
    this.sock = {
      send: (bytes) => {
        if (this.accepts) this.sent.push(bytes);
        return this.accepts;
      },
      resize: () => undefined,
      close: () => {
        this.closes++;
        this.on.state("closed", { code: 1000, reason: "" });
      },
    };
  }

  live(): void {
    this.on.state("live");
  }

  data(...bytes: number[]): void {
    this.on.data(new Uint8Array(bytes));
  }

  end(code: number, reason = ""): void {
    this.on.state("closed", { code, reason });
  }
}

interface Rig {
  link: Link;
  conns: Conn[];
  log: string[];
  states: LinkState[];
}

const links: Link[] = [];

function rig(framed = false): Rig {
  const conns: Conn[] = [];
  const log: string[] = [];
  const states: LinkState[] = [];
  const link = createLink({
    framed,
    open: (on) => {
      const conn = new Conn(on);
      conns.push(conn);
      return conn.sock;
    },
    fresh: () => {
      log.push("fresh");
    },
    data: (bytes) => {
      log.push(`data:${Array.from(bytes).join(",")}`);
    },
    changed: (state) => {
      states.push(state);
    },
  });
  links.push(link);
  return { link, conns, log, states };
}

function last(r: Rig): Conn {
  const conn = r.conns.at(-1);
  if (conn === undefined) throw new Error("the link opened no connection");
  return conn;
}

// The page's visibility, with the event the browser fires.
function visibility(state: "visible" | "hidden"): void {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

function pageshow(persisted: boolean): void {
  const event = new Event("pageshow");
  Object.defineProperty(event, "persisted", { value: persisted });
  window.dispatchEvent(event);
}

function pagehide(): void {
  window.dispatchEvent(new Event("pagehide"));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(1_000_000);
});

afterEach(() => {
  for (const link of links) link.close();
  links.length = 0;
  Reflect.deleteProperty(document, "visibilityState");
  vi.useRealTimers();
});

describe("reconnects", () => {
  it.each([1001, 4005, 1006])("reconnects after %i", (code) => {
    expect(reconnects({ code, reason: "" })).toBe(true);
  });

  it.each([1000, 1005, 1011, 4001, 4002, 4003, 4004, 4006, 4007])(
    "does not reconnect after %i",
    (code) => {
      expect(reconnects({ code, reason: "x" })).toBe(false);
    },
  );

  it("doubles the delay up to 30 s and no further", () => {
    expect(nextDelay(FIRST_DELAY)).toBe(2000);
    expect(nextDelay(16_000)).toBe(MAX_DELAY);
    expect(nextDelay(MAX_DELAY)).toBe(MAX_DELAY);
  });
});

describe("lastCall", () => {
  it("calls once, with the last arguments, after the quiet period", () => {
    const fn = vi.fn();
    const call = lastCall(fn, SIZE_SETTLE);
    call(1, 2);
    vi.advanceTimersByTime(60);
    call(3, 4);
    vi.advanceTimersByTime(SIZE_SETTLE - 1);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(3, 4);
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("calls nothing once cancelled", () => {
    const fn = vi.fn();
    const call = lastCall(fn, SIZE_SETTLE);
    call(1);
    call.cancel();
    vi.advanceTimersByTime(1000);
    expect(fn).not.toHaveBeenCalled();
  });
});

describe("the link", () => {
  it("connects at start when the page is not in a frame", () => {
    const r = rig();
    expect(r.conns).toHaveLength(0);
    r.link.start();
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("holds the first connection until the link is up when it is in a frame", () => {
    const r = rig(true);
    r.link.start();
    expect(r.conns).toHaveLength(0);
    expect(r.link.state()).toEqual({ phase: "connecting" });

    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
    r.link.setLink(true);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
  });

  it("takes what was set before it started when it starts", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.reconnect();
    expect(r.conns).toHaveLength(0);
    r.link.start();
    expect(r.conns).toHaveLength(1);

    const closed = rig();
    closed.link.close();
    closed.link.start();
    expect(closed.conns).toHaveLength(0);
    expect(closed.link.state()).toEqual({ phase: "closed", closed: { code: 1000, reason: "" } });
  });

  it("is live when the daemon says so, and tells each change once", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    last(r).live();
    expect(r.link.state()).toEqual({ phase: "live" });
    last(r).end(1001, "loom is stopping");
    const retryAt = Date.now() + FIRST_DELAY;
    expect(r.link.state()).toEqual({ phase: "waiting", retryAt });
    vi.advanceTimersByTime(FIRST_DELAY);
    expect(r.states).toEqual([
      { phase: "live" },
      { phase: "waiting", retryAt },
      { phase: "connecting" },
    ]);
  });

  it("retries on a backoff of 1 s doubling to 30 s", () => {
    const r = rig();
    r.link.start();
    const waits: number[] = [];
    for (let i = 0; i < 8; i++) {
      last(r).end(1006);
      const state = r.link.state();
      expect(state.phase).toBe("waiting");
      const wait = (state.retryAt ?? 0) - Date.now();
      waits.push(wait);
      vi.advanceTimersByTime(wait - 1);
      expect(r.conns).toHaveLength(i + 1);
      vi.advanceTimersByTime(1);
      expect(r.conns).toHaveLength(i + 2);
    }
    expect(waits).toEqual([1000, 2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000]);
  });

  // Two failures before anything is live: the next wait is 4 s.
  function failTwice(r: Rig): void {
    r.link.start();
    last(r).end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).end(1006);
    vi.advanceTimersByTime(2 * FIRST_DELAY);
  }

  function wait(r: Rig): number {
    return (r.link.state().retryAt ?? 0) - Date.now();
  }

  it.each([1001, 4005, 1006])(
    "keeps the delay doubling when a connection ends after %i before 30 s live",
    (code) => {
      const r = rig();
      failTwice(r);
      last(r).live();
      vi.advanceTimersByTime(STABLE_LIVE - 1);
      last(r).end(code);
      expect(wait(r)).toBe(4000);
      // The timer of the ended connection must not fire later and reset the delay.
      vi.advanceTimersByTime(4000);
      last(r).end(1006);
      expect(wait(r)).toBe(8000);
    },
  );

  it("goes back to 1 s once a connection has stayed live for 30 s", () => {
    const r = rig();
    failTwice(r);
    last(r).live();
    vi.advanceTimersByTime(STABLE_LIVE);
    last(r).end(1006);
    expect(wait(r)).toBe(FIRST_DELAY);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).end(1006);
    expect(wait(r)).toBe(2 * FIRST_DELAY);
  });

  it("does not let a replaced connection's 30 s timer reset the delay", () => {
    const r = rig(true);
    r.link.setLink(true);
    failTwice(r);
    last(r).live();
    vi.advanceTimersByTime(STABLE_LIVE - 1000);
    r.link.setLink(false);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(4);
    vi.advanceTimersByTime(2000);
    last(r).end(1006);
    expect(wait(r)).toBe(4000);
  });

  it("retries a connection that failed before it opened", () => {
    const r = rig();
    r.link.start();
    expect(r.link.state()).toEqual({ phase: "connecting" });
    last(r).end(1006);
    expect(r.link.state().phase).toBe("waiting");
    vi.advanceTimersByTime(FIRST_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("retries a socket that cannot be made", () => {
    let made = 0;
    const link = createLink({
      framed: false,
      open: () => {
        made++;
        throw new SyntaxError("bad url");
      },
      fresh: () => undefined,
      data: () => undefined,
      changed: () => undefined,
    });
    links.push(link);
    link.start();
    expect(link.state().phase).toBe("waiting");
    vi.advanceTimersByTime(FIRST_DELAY);
    expect(made).toBe(2);
  });

  it.each([
    [4001, "the terminal is off on this host"],
    [4002, "this device is no longer paired"],
    [4003, "the session was paused or killed"],
    [4004, "the shell exited"],
    [4006, "a frame loom does not understand"],
    [4007, "loom could not attach to the shell"],
  ])("ends closed after %i and never reconnects on its own", (code, reason) => {
    const r = rig();
    r.link.start();
    last(r).live();
    last(r).end(code, reason);
    expect(r.link.state()).toEqual({ phase: "closed", closed: { code, reason } });
    vi.advanceTimersByTime(10 * MAX_DELAY);
    visibility("hidden");
    visibility("visible");
    expect(r.conns).toHaveLength(1);
  });

  it("reconnects from closed at once when asked", () => {
    const r = rig();
    r.link.start();
    last(r).end(4004, "the shell exited");
    r.link.reconnect();
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    last(r).live();
    expect(r.link.state()).toEqual({ phase: "live" });
  });

  it("holds a reconnect that was asked for while the link is down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).end(4004, "the shell exited");
    r.link.setLink(false);
    r.link.reconnect();
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
  });

  it("reconnects at once when the page becomes visible while waiting", () => {
    const r = rig();
    r.link.start();
    last(r).end(1006);
    visibility("hidden");
    visibility("visible");
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("starts nothing while the link is down, and starts the held reconnect when it is up", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).end(1006);
    r.link.setLink(false);
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state().phase).toBe("waiting");
    visibility("hidden");
    visibility("visible");
    expect(r.conns).toHaveLength(1);

    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("starts a scheduled reconnect at once when the link comes back up", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).end(1006);
    r.link.setLink(false);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("replaces a live connection when the page was hidden for 30 s or longer", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    visibility("hidden");
    vi.advanceTimersByTime(LONG_HIDDEN);
    visibility("visible");
    expect(r.conns).toHaveLength(2);
    expect(r.conns[0].closes).toBe(1);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("leaves a live connection alone when the page was hidden for less than 30 s", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    visibility("hidden");
    vi.advanceTimersByTime(LONG_HIDDEN - 1);
    visibility("visible");
    expect(r.conns).toHaveLength(1);
    expect(r.conns[0].closes).toBe(0);
    expect(r.link.state()).toEqual({ phase: "live" });
  });

  it("ignores the late events of a connection it replaced", () => {
    const r = rig();
    r.link.start();
    const old = last(r);
    old.live();
    visibility("hidden");
    vi.advanceTimersByTime(LONG_HIDDEN);
    visibility("visible");
    expect(r.log).toEqual(["fresh"]);

    old.data(9);
    old.live();
    old.end(1006);
    expect(r.log).toEqual(["fresh"]);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("replaces a live connection when the link comes back up after being down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(false);
    expect(r.conns).toHaveLength(1);
    expect(r.conns[0].closes).toBe(0);
    expect(r.link.state()).toEqual({ phase: "live" });

    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
    expect(r.conns[0].closes).toBe(1);
  });

  it("leaves a live connection alone when the link is set up again without having been down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({ phase: "live" });
  });

  it("leaves a connection that is still being made when the link comes back up", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    r.link.setLink(false);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
    expect(r.conns[0].closes).toBe(0);
  });

  it("ends the connection for good on close", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    r.link.close();
    expect(r.conns[0].closes).toBe(1);
    expect(r.link.state()).toEqual({ phase: "closed", closed: { code: 1000, reason: "" } });

    vi.advanceTimersByTime(10 * MAX_DELAY);
    r.link.reconnect();
    r.link.setLink(false);
    r.link.setLink(true);
    visibility("hidden");
    visibility("visible");
    pageshow(true);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state().phase).toBe("closed");
  });

  it("drops a scheduled reconnect on close", () => {
    const r = rig();
    r.link.start();
    last(r).end(1006);
    r.link.close();
    vi.advanceTimersByTime(10 * MAX_DELAY);
    expect(r.conns).toHaveLength(1);
  });

  it("ends the connection on pagehide and connects again when a cached page is restored", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    pagehide();
    expect(r.conns[0].closes).toBe(1);
    expect(r.link.state()).toEqual({ phase: "waiting" });
    vi.advanceTimersByTime(10 * MAX_DELAY);
    expect(r.conns).toHaveLength(1);

    pageshow(true);
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("connects nothing on the link coming up or the page showing between pagehide and pageshow", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(false);
    pagehide();
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);

    const plain = rig();
    plain.link.start();
    last(plain).live();
    pagehide();
    visibility("hidden");
    visibility("visible");
    expect(plain.conns).toHaveLength(1);
    pageshow(true);
    expect(plain.conns).toHaveLength(2);
  });

  it("changes nothing on pagehide before it starts", () => {
    const r = rig();
    pagehide();
    expect(r.states).toEqual([]);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    pageshow(true);
    expect(r.conns).toHaveLength(0);
    r.link.start();
    expect(r.conns).toHaveLength(1);
  });

  it("counts a page created hidden as hidden since it was created", () => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    const r = rig();
    r.link.start();
    last(r).live();
    vi.advanceTimersByTime(LONG_HIDDEN);
    visibility("visible");
    expect(r.conns).toHaveLength(2);
  });

  it("does not reconnect on a pageshow that is not a restore", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    pagehide();
    pageshow(false);
    expect(r.conns).toHaveLength(1);
    pageshow(true);
    expect(r.conns).toHaveLength(2);
  });

  it("does not reconnect a page that ended in a refusal when it is restored", () => {
    const r = rig();
    r.link.start();
    last(r).end(4004, "the shell exited");
    pagehide();
    pageshow(true);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({
      phase: "closed",
      closed: { code: 4004, reason: "the shell exited" },
    });
  });

  it("waits for the link when a cached page is restored with it down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(false);
    pagehide();
    pageshow(true);
    expect(r.conns).toHaveLength(1);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
  });

  it("resets the terminal once a connection, before its first bytes", () => {
    const r = rig();
    r.link.start();
    expect(r.log).toEqual([]);
    last(r).data(1);
    last(r).data(2);
    last(r).live();
    last(r).data(3);
    expect(r.log).toEqual(["fresh", "data:1", "data:2", "data:3"]);

    last(r).end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).data(4);
    expect(r.log).toEqual(["fresh", "data:1", "data:2", "data:3", "fresh", "data:4"]);
  });

  it("resets the terminal when a connection goes live with no bytes", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    expect(r.log).toEqual(["fresh"]);
    last(r).data(1);
    expect(r.log).toEqual(["fresh", "data:1"]);
  });

  it("leaves the old screen alone when a connection fails before anything arrives", () => {
    const r = rig();
    r.link.start();
    last(r).end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).end(1006);
    expect(r.log).toEqual([]);
  });

  it("sends only while live, and answers whether the bytes went", () => {
    const r = rig();
    r.link.start();
    const bytes = new Uint8Array([97]);
    expect(r.link.send(bytes)).toBe(false);
    expect(r.conns[0].sent).toEqual([]);

    last(r).live();
    expect(r.link.send(bytes)).toBe(true);
    expect(r.conns[0].sent).toEqual([bytes]);

    r.conns[0].accepts = false;
    expect(r.link.send(bytes)).toBe(false);
    r.conns[0].accepts = true;

    last(r).end(1006);
    expect(r.link.send(bytes)).toBe(false);
    expect(r.conns[0].sent).toEqual([bytes]);
  });
});

describe("freshScreen", () => {
  const ris = [0x1b, 0x63];

  // A terminal that records what is written to it, in order, as xterm.js's write queue takes it.
  function screen(): { out: number[][]; sink: ScreenSink } {
    const out: number[][] = [];
    return {
      out,
      sink: {
        write: (data) => {
          out.push(Array.from(data));
        },
      },
    };
  }

  // A link wired as main wires it: the reset is freshScreen's, the bytes go to the same screen.
  function wired(sink: ScreenSink | null): { link: Link; conns: Conn[] } {
    const conns: Conn[] = [];
    const link = createLink({
      framed: false,
      open: (on) => {
        const conn = new Conn(on);
        conns.push(conn);
        return conn.sock;
      },
      fresh: freshScreen(() => sink),
      data: (bytes) => {
        sink?.write(bytes);
      },
      changed: () => undefined,
    });
    links.push(link);
    return { link, conns };
  }

  it("is the bytes of RIS", () => {
    expect(Array.from(RIS)).toEqual(ris);
  });

  it("writes RIS ahead of each connection's first bytes, once per connection", () => {
    const { out, sink } = screen();
    const { link, conns } = wired(sink);
    link.start();
    expect(out).toEqual([]);
    conns[0].data(1);
    conns[0].data(2);
    conns[0].live();
    conns[0].data(3);
    expect(out).toEqual([ris, [1], [2], [3]]);

    // An old connection cut inside an escape sequence: the next one still begins with RIS.
    conns[0].data(0x1b, 0x5b);
    conns[0].end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    conns[1].data(4);
    expect(out).toEqual([ris, [1], [2], [3], [0x1b, 0x5b], ris, [4]]);
  });

  it("writes RIS when a connection goes live with no bytes", () => {
    const { out, sink } = screen();
    const { link, conns } = wired(sink);
    link.start();
    conns[0].live();
    expect(out).toEqual([ris]);
  });

  it("writes nothing for a connection that fails before anything arrives", () => {
    const { out, sink } = screen();
    const { link, conns } = wired(sink);
    link.start();
    conns[0].end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    conns[1].end(1006);
    expect(out).toEqual([]);
  });

  it("writes nothing, and does not throw, while there is no terminal", () => {
    const { link, conns } = wired(null);
    link.start();
    expect(() => {
      conns[0].data(1);
      conns[0].live();
    }).not.toThrow();
  });
});
