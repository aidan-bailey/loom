// What the terminal page does with keys (spec §33, §37): the bytes of a named key, and the policy of
// the custom key handler xterm.js runs before it handles a key event itself.
import type { KeyName } from "./frame";
import { textBytes } from "./socket";

const ESC = "\x1b";
const ARROW_LETTER = { up: "A", down: "B", right: "C", left: "D" } as const;

function keyText(name: KeyName, applicationCursor: boolean): string {
  switch (name) {
    case "up":
    case "down":
    case "right":
    case "left":
      // ESC [ A–D normally, ESC O A–D while the program has set application cursor keys.
      return `${ESC}${applicationCursor ? "O" : "["}${ARROW_LETTER[name]}`;
    case "tab":
      return "\t";
    case "esc":
      return ESC;
    case "ctrl-c":
      return "\x03";
    case "ctrl-d":
      return "\x04";
  }
}

/** The bytes a named key sends, in the cursor mode the program has set. */
export function keyBytes(name: KeyName, applicationCursor: boolean): Uint8Array<ArrayBuffer> {
  return textBytes(keyText(name, applicationCursor));
}

/**
 * What a key event in the frame does: `chord` is ⌘K, which the app answers (key events in a frame
 * never reach the app's document); `copy` is Ctrl+Shift+C; `paste` is Ctrl+Shift+V; everything
 * else, Ctrl+K and Ctrl+C included, is `shell`.
 */
export type KeyAction = "chord" | "copy" | "paste" | "shell";

export function keyAction(ev: KeyboardEvent): KeyAction {
  const key = ev.key.toLowerCase();
  if (ev.metaKey && !ev.ctrlKey && !ev.altKey && !ev.shiftKey && key === "k") return "chord";
  if (ev.ctrlKey && ev.shiftKey && !ev.altKey && !ev.metaKey) {
    if (key === "c") return "copy";
    if (key === "v") return "paste";
  }
  return "shell";
}

export interface KeyHooks {
  /** ⌘K was pressed. */
  chord(): void;
  /** Ctrl+Shift+C was pressed: copy the selection. */
  copy(): void;
}

/**
 * The body of xterm.js's custom key event handler: false keeps the event from xterm.js (and so from
 * the shell), true lets it handle it. The chord and the copy key act on their keydown only, once
 * (not on a repeat), and cancel the browser's own action (Firefox opens its inspector on
 * Ctrl+Shift+C); Ctrl+Shift+V is kept from the shell and left to the browser, whose paste event
 * xterm.js turns into a paste, bracketed when the shell asked.
 */
export function handleKey(ev: KeyboardEvent, hooks: KeyHooks): boolean {
  const action = keyAction(ev);
  if (action === "shell") return true;
  if (ev.type === "keydown" && action !== "paste") {
    ev.preventDefault();
    if (!ev.repeat) {
      if (action === "chord") hooks.chord();
      else hooks.copy();
    }
  }
  return false;
}
