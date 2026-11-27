// The interface the terminal's document gives the app (spec §33). The document's script
// (main.ts) sets `window.loomTerminal`; the panel in the app reads it off the loaded frame through
// `frameTerminal` and drives the terminal only through it, never through the frame's DOM. This
// module imports nothing of xterm.js, so the app's entry bundle may import it.
import type { Closed } from "./socket";

/** A key the phone's key row sends; the frame encodes it (the arrows depend on the cursor mode). */
export type KeyName = "up" | "down" | "left" | "right" | "tab" | "esc" | "ctrl-c" | "ctrl-d";

/** The connection's phase: `waiting` is a reconnect scheduled or held, `closed` has none coming. */
export type Phase = "connecting" | "live" | "waiting" | "closed";

/** The renderer xterm.js draws with. */
export type Renderer = "webgl" | "dom";

export interface FrameState {
  phase: Phase;
  /** The close, set only while `closed`; code 0 means the terminal could not start (spec §68). */
  closed?: Closed;
  /** The time of the next attempt, in milliseconds since the epoch, set only while `waiting`. */
  retryAt?: number;
  renderer: Renderer;
}

export interface LoomTerminal {
  /**
   * Types bytes into the shell and scrolls the terminal to the bottom. True when they went to an
   * open connection; false otherwise, and then they are dropped, never queued.
   */
  send(bytes: Uint8Array<ArrayBuffer>): boolean;
  /** `send` of a named key's bytes, in the cursor mode the program has set. */
  key(name: KeyName): boolean;
  /** The whole buffer, scrollback and screen, as plain text; "" until the terminal has started. */
  text(): string;
  /** The phone's mode: the terminal takes no focus and a finger scrolls it. */
  setTouch(on: boolean): void;
  /** Whether the app's snapshot is fresh; while it is not, no new connection starts. */
  setLink(up: boolean): void;
  /** Connects again, from any state, at once when the link is up. */
  reconnect(): void;
  focus(): void;
  /** Ends the connection for good. */
  close(): void;
  /** Holds one callback, the last one given, and calls it at once with the current state. */
  onState(cb: (state: FrameState) => void): void;
  /** Holds one callback, the last one given: ⌘K was pressed inside the frame. */
  onChord(cb: () => void): void;
}

declare global {
  interface Window {
    /** Set by the terminal document's script as it first runs, before the font has loaded. */
    loomTerminal?: LoomTerminal;
  }
}

/**
 * The state of a terminal whose start threw (spec §68): `closed`, with code 0 and the failure's
 * text as the reason, which the panel shows as it shows any closed reason.
 */
export function startFailed(err: unknown, renderer: Renderer): FrameState {
  const text = err instanceof Error && err.message !== "" ? err.message : "unknown error";
  return {
    phase: "closed",
    closed: { code: 0, reason: `The terminal failed to start: ${text}` },
    renderer,
  };
}

/**
 * The interface of a loaded frame, or null while its script has not run (or its window cannot be
 * read). It is the only code outside the frame that reads the frame's window.
 */
export function frameTerminal(frame: HTMLIFrameElement): LoomTerminal | null {
  try {
    return frame.contentWindow?.loomTerminal ?? null;
  } catch {
    return null;
  }
}
