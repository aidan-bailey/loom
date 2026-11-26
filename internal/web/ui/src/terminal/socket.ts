// The terminal page's socket (spec §17): one WebSocket to the daemon's terminal route, as a small
// state machine with no reconnecting (part 2's panel adds that). The first frame the page sends is
// its size; text frames carry JSON control messages and binary frames carry terminal bytes both
// ways. A binary frame the page sends is at most FRAME_MAX bytes, because the daemon ends a longer
// one with 4006 and the app does not reconnect after that.

/** The longest frame the daemon accepts from the page (`termFrameMax` in internal/web/terminal_conn.go). */
export const FRAME_MAX = 64 * 1024;

// The sizes the daemon accepts (`termMinCols` and its neighbours in terminal_conn.go).
const MIN_COLS = 2;
const MAX_COLS = 500;
const MIN_ROWS = 1;
const MAX_ROWS = 200;

/** The part of a WebSocket this module uses, so a test can stand in for one. */
export interface WireSocket {
  binaryType: BinaryType;
  onopen: ((ev: Event) => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onclose: ((ev: CloseEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
  send(data: string | Uint8Array<ArrayBuffer>): void;
  close(code?: number): void;
}

export type SocketState = "connecting" | "live" | "closed";

export interface Size {
  cols: number;
  rows: number;
}

/** A close frame's code and reason; a connection that dropped with none has code 1006 and "". */
export interface Closed {
  code: number;
  reason: string;
}

export interface Handlers {
  /** "connecting" at once, "live" when the daemon says it attached, then "closed" with the close. */
  state(state: SocketState, closed?: Closed): void;
  /** Each binary frame the daemon sends: the shell's output, its history first. */
  data(bytes: Uint8Array): void;
}

export interface TerminalSocket {
  /** Sends input in frames of at most FRAME_MAX; false while the socket is not open. */
  send(bytes: Uint8Array<ArrayBuffer>): boolean;
  /** Tells the daemon the terminal's new size; nothing while the socket is not open. */
  resize(cols: number, rows: number): void;
  /** Ends the connection with a normal close. */
  close(): void;
}

function clamp(value: number, low: number, high: number): number {
  return Math.min(high, Math.max(low, Number.isFinite(value) ? Math.trunc(value) : low));
}

/** The text frame that tells the daemon the size, inside its bounds. */
export function resizeFrame(size: Size): string {
  const cols = clamp(size.cols, MIN_COLS, MAX_COLS);
  const rows = clamp(size.rows, MIN_ROWS, MAX_ROWS);
  return JSON.stringify({ resize: { cols, rows } });
}

// isLive reports whether a text frame is the daemon's {"state":"live"}.
function isLive(text: string): boolean {
  try {
    const frame: unknown = JSON.parse(text);
    return (
      typeof frame === "object" && frame !== null && "state" in frame && frame.state === "live"
    );
  } catch {
    return false;
  }
}

/**
 * Opens the terminal route at url. The first frame is the size, which `size` gives when the socket
 * opens, so a window resized meanwhile is not sent stale. `make` builds the WebSocket (a test
 * passes a fake).
 */
export function openSocket(
  url: string,
  size: () => Size,
  on: Handlers,
  make: (url: string) => WireSocket = (u) => new WebSocket(u),
): TerminalSocket {
  const ws = make(url);
  ws.binaryType = "arraybuffer";
  let state: SocketState = "connecting";
  let opened = false;

  const set = (next: SocketState, closed?: Closed): void => {
    if (state === "closed" || next === state) return;
    state = next;
    on.state(next, closed);
  };
  on.state("connecting");

  ws.onopen = () => {
    ws.send(resizeFrame(size()));
    opened = true;
  };
  ws.onmessage = (ev) => {
    const data: unknown = ev.data;
    if (typeof data === "string") {
      if (isLive(data)) set("live");
      return;
    }
    if (data instanceof ArrayBuffer) on.data(new Uint8Array(data));
  };
  ws.onclose = (ev) => {
    opened = false;
    set("closed", { code: ev.code, reason: ev.reason });
  };
  ws.onerror = () => {
    // A close event follows an error and carries the code.
  };

  return {
    send(bytes) {
      if (!opened) return false;
      for (let at = 0; at < bytes.length; at += FRAME_MAX) {
        ws.send(bytes.subarray(at, at + FRAME_MAX));
      }
      return true;
    },
    resize(cols, rows) {
      if (!opened) return;
      ws.send(resizeFrame({ cols, rows }));
    },
    close() {
      opened = false;
      // The close code is the only sign the page closed the socket itself, and the browser may
      // report 1006 for it later (before the open, or from the back-forward cache): the state
      // is closed here, with code 1000, and the later `onclose` changes nothing.
      set("closed", { code: 1000, reason: "" });
      ws.close(1000);
    },
  };
}

const encoder = new TextEncoder();

/** What xterm.js's onData gives (typed or pasted text) as the bytes to send. */
export function textBytes(text: string): Uint8Array<ArrayBuffer> {
  return encoder.encode(text);
}

/** What xterm.js's onBinary gives (a string of one byte a character) as the bytes to send. */
export function binaryBytes(text: string): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(text.length);
  for (let i = 0; i < text.length; i++) bytes[i] = text.charCodeAt(i) & 0xff;
  return bytes;
}

/** The socket's URL: the route's path on the page's own host, wss on an https page. */
export function socketURL(path: string, loc: { protocol: string; host: string }): string {
  return `${loc.protocol === "https:" ? "wss:" : "ws:"}//${loc.host}${path}`;
}

/** The session id in a `#<id>` hash, or null when the hash is anything else. */
export function sessionIdFromHash(hash: string): number | null {
  const match = hash.match(/^#(\d+)$/);
  if (match === null) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

/**
 * The text the page shows when the socket closes: the daemon's reason; else, for the normal close
 * the page asked for itself (`close()` from `pagehide`, which Safari may restore from its
 * back-forward cache), that it did, and to reload; else that the connection was refused or lost.
 * A refusal before the upgrade (for example 403, 404, 409, 426, 429, 500, 503 or 504; the list is
 * of examples, not the whole route's) reaches the browser as code 1006 with no reason, so the
 * causes are named as possible, never asserted.
 */
export function describeClose(closed: Closed): string {
  if (closed.reason !== "") return closed.reason;
  if (closed.code === 1000) {
    return "This terminal was closed from this page. Reload the page to open it again.";
  }
  return (
    `The connection was refused or lost (code ${closed.code}). ` +
    "The terminal may be off on this host (loom web terminal on), the session may be paused or not running, " +
    "or too many terminals may be open."
  );
}
