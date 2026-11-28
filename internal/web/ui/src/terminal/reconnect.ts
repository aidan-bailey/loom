// The terminal page's connection policy (spec §34, §35, §59, §60): which endings reconnect, the
// backoff, and the link that holds every new connection while the app's snapshot is stale. It owns
// no WebSocket and no xterm.js: `open` makes each connection, so a test drives it with fake sockets
// and fake timers.
import type { FrameState, Phase } from "./frame";
import {
  CLOSE_SLOW,
  CLOSE_STOPPING,
  type Closed,
  type Handlers,
  type TerminalSocket,
} from "./socket";

// The stream's numbers (api/stream.ts keeps its own, unexported).
export const FIRST_DELAY = 1000;
export const MAX_DELAY = 30_000;
/** A connection must stay live this long before the delay goes back to FIRST_DELAY (spec §66). */
export const STABLE_LIVE = 30_000;
/** A page hidden this long may hold a half-open connection: the daemon's 20 s ping and 10 s wait. */
export const LONG_HIDDEN = 30_000;
/** How long the window's size must hold still before the new size is sent. */
export const SIZE_SETTLE = 100;

/** What a browser reports for a close with no close frame, and for a connection that failed before it opened. */
export const NO_CLOSE_FRAME = 1006;

/**
 * Whether an ending reconnects: the daemon stopping (1001), the browser falling behind (4005) and
 * an ending with no close frame. Every refusal (4001–4004, 4006, 4007) and any other code ends the
 * connection until `reconnect()`.
 */
export function reconnects(closed: Closed): boolean {
  return (
    closed.code === CLOSE_STOPPING || closed.code === CLOSE_SLOW || closed.code === NO_CLOSE_FRAME
  );
}

/** The wait after one of `delay`: doubled, never past MAX_DELAY. */
export function nextDelay(delay: number): number {
  return Math.min(delay * 2, MAX_DELAY);
}

/**
 * A function that calls `fn` with its latest arguments once `ms` have passed with no newer call.
 */
export function lastCall<A extends unknown[]>(
  fn: (...args: A) => void,
  ms: number,
): ((...args: A) => void) & { cancel(): void } {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const call = (...args: A): void => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      timer = undefined;
      fn(...args);
    }, ms);
  };
  return Object.assign(call, {
    cancel: () => {
      clearTimeout(timer);
      timer = undefined;
    },
  });
}

/** RIS, "reset to initial state" (`ESC c`): xterm.js leaves any escape sequence it was inside. */
export const RIS = new Uint8Array([0x1b, 0x63]);

/** The part of xterm.js's Terminal that `freshScreen` writes to. */
export interface ScreenSink {
  write(data: Uint8Array): void;
}

/**
 * The `fresh` of a link wired to a terminal (spec §65): it writes RIS to the terminal. The write
 * queue orders it after whatever the old connection left in it and before the new history, and RIS
 * leaves an escape sequence the old connection was cut inside, which xterm.js's `reset()` does not.
 */
export function freshScreen(target: () => ScreenSink | null): () => void {
  return () => {
    target()?.write(RIS);
  };
}

/** What the link tells the frame: FrameState without the renderer. */
export type LinkState = Omit<FrameState, "renderer">;

export interface LinkOptions {
  /** Whether the page is in a frame; a page that is not behaves as if the link were up. */
  framed: boolean;
  /** Opens one connection, giving it the handlers. */
  open(on: Handlers): TerminalSocket;
  /** A new connection's first bytes (or its `live`) are about to reach the terminal: write the reset (RIS) to it. */
  fresh(): void;
  /** The shell's bytes. */
  data(bytes: Uint8Array): void;
  /** The state changed. */
  changed(state: LinkState): void;
}

export interface Link {
  /** The terminal is ready: connect now if the link is up. */
  start(): void;
  setLink(up: boolean): void;
  /** A fresh connection from any state, at once when the link is up. */
  reconnect(): void;
  /** Ends the connection for good and unbinds the page's listeners. */
  close(): void;
  /** True when the bytes went to an open, live connection. */
  send(bytes: Uint8Array<ArrayBuffer>): boolean;
  resize(cols: number, rows: number): void;
  state(): LinkState;
}

/** The connection controller of the terminal page; see the file's comment. */
export function createLink(opts: LinkOptions): Link {
  let up = !opts.framed;
  let started = false;
  // close() has run: nothing connects any more.
  let final = false;
  // pagehide ended the connection, and a restore from the back-forward cache may revive it.
  let suspended = false;
  let sock: TerminalSocket | null = null;
  // Each connection has a generation; a handler of an older one changes nothing.
  let generation = 0;
  let phase: Phase = "connecting";
  let closed: Closed | undefined;
  let retryAt: number | undefined;
  let delay = FIRST_DELAY;
  let timer: ReturnType<typeof setTimeout> | undefined;
  // The live connection's STABLE_LIVE timer: when it fires the delay goes back to FIRST_DELAY.
  let stable: ReturnType<typeof setTimeout> | undefined;
  let hiddenAt: number | null = document.visibilityState === "hidden" ? Date.now() : null;

  const keyOf = (): string =>
    `${phase}|${closed?.code ?? ""}|${closed?.reason ?? ""}|${retryAt ?? ""}`;
  let last = keyOf();

  function state(): LinkState {
    const s: LinkState = { phase };
    if (closed !== undefined) s.closed = closed;
    if (retryAt !== undefined) s.retryAt = retryAt;
    return s;
  }

  function set(next: Phase, why?: Closed, at?: number): void {
    phase = next;
    closed = why;
    retryAt = at;
    const key = keyOf();
    if (key === last) return;
    last = key;
    opts.changed(state());
  }

  // drop ends the current connection, any scheduled reconnect and the stable timer. The generation
  // moves first, so the close the old socket reports at once changes nothing.
  function drop(): void {
    generation++;
    clearTimeout(timer);
    timer = undefined;
    clearTimeout(stable);
    stable = undefined;
    const old = sock;
    sock = null;
    old?.close();
  }

  function connect(): void {
    drop();
    const mine = generation;
    let seen = false;
    // The terminal is reset with a connection's first bytes, never before: the daemon sends the
    // history on every connect and the scrollback would otherwise hold it twice (spec §36), and a
    // connection that fails first leaves the old screen visible.
    const first = (): void => {
      if (seen) return;
      seen = true;
      opts.fresh();
    };
    set("connecting");
    try {
      sock = opts.open({
        state: (next, why) => {
          if (mine !== generation) return;
          if (next === "live") {
            first();
            // The delay goes back to FIRST_DELAY only when the connection stays live for
            // STABLE_LIVE (spec §66): one that ends sooner, whatever ended it, leaves it doubling, so
            // a flood that fills the daemon's buffer (4005 again and again) backs off.
            if (phase !== "live") {
              stable = setTimeout(() => {
                stable = undefined;
                delay = FIRST_DELAY;
              }, STABLE_LIVE);
            }
            set("live");
          } else if (next === "closed") {
            sock = null;
            ended(why ?? { code: NO_CLOSE_FRAME, reason: "" });
          }
        },
        data: (bytes) => {
          if (mine !== generation) return;
          first();
          opts.data(bytes);
        },
      });
    } catch {
      sock = null;
      ended({ code: NO_CLOSE_FRAME, reason: "" });
    }
  }

  function ended(why: Closed): void {
    clearTimeout(stable);
    stable = undefined;
    if (!reconnects(why)) {
      set("closed", why);
      return;
    }
    set("waiting", undefined, Date.now() + delay);
    timer = setTimeout(fire, delay);
    delay = nextDelay(delay);
  }

  // fire is the scheduled reconnect's time: it connects when the link is up, and otherwise stays
  // waiting until setLink(true).
  function fire(): void {
    timer = undefined;
    if (up) connect();
  }

  function start(): void {
    if (started) return;
    started = true;
    if (up && !final) connect();
  }

  function setLink(next: boolean): void {
    const was = up;
    up = next;
    if (!next || was || !started || final || suspended) return;
    if (phase === "live") {
      // Down and up again: the connection may be half-open, and nothing would tell the page.
      connect();
    } else if (phase !== "closed" && sock === null) {
      connect();
    }
  }

  function reconnect(): void {
    if (final) return;
    delay = FIRST_DELAY;
    suspended = false;
    drop();
    set("connecting");
    if (started && up) connect();
  }

  function close(): void {
    if (final) return;
    final = true;
    drop();
    document.removeEventListener("visibilitychange", onVisibility);
    window.removeEventListener("pagehide", onPageHide);
    window.removeEventListener("pageshow", onPageShow);
    set("closed", { code: 1000, reason: "" });
  }

  function onVisibility(): void {
    if (document.visibilityState === "hidden") {
      hiddenAt = Date.now();
      return;
    }
    const away = hiddenAt === null ? 0 : Date.now() - hiddenAt;
    hiddenAt = null;
    if (!started || final || suspended || !up) return;
    if (phase === "waiting") {
      delay = FIRST_DELAY;
      connect();
    } else if (phase === "live" && away >= LONG_HIDDEN) {
      connect();
    }
  }

  // pagehide closes the connection normally, as part 1's page did; a page restored from the
  // back-forward cache (pageshow with `persisted`) connects again as if it were waiting.
  function onPageHide(): void {
    if (!started || final || phase === "closed") return;
    suspended = true;
    drop();
    set("waiting");
  }

  function onPageShow(event: PageTransitionEvent): void {
    if (!event.persisted || !suspended) return;
    suspended = false;
    if (started && up) connect();
  }

  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("pagehide", onPageHide);
  window.addEventListener("pageshow", onPageShow);

  return {
    start,
    setLink,
    reconnect,
    close,
    send: (bytes) => phase === "live" && sock !== null && sock.send(bytes),
    resize: (cols, rows) => {
      sock?.resize(cols, rows);
    },
    state,
  };
}
