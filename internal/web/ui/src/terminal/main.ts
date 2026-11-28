// The terminal's document (spec §23): xterm.js on a session's worktree shell, over the daemon's
// WebSocket. It is the only script that imports xterm.js, and the web layer serves its page under
// terminalCSP, which lets xterm.js write the <style> elements the app's policy drops.
//
// In a frame it is driven by the app through `window.loomTerminal` (frame.ts), which exists from the
// moment the script runs: a call that sets something before the terminal has started takes effect
// when it starts, and `send` and `key` answer false and `text` answers "" until then. Opened on its
// own the page keeps part 1's status line and focuses the terminal when it goes live.
import "@xterm/xterm/css/xterm.css";
import "../styles/app.css";
import "./terminal.css";

import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal } from "@xterm/xterm";

import { api } from "../api/request";
import {
  startFailed,
  type FrameState,
  type KeyName,
  type LoomTerminal,
  type Renderer,
} from "./frame";
import { handleKey, keyBytes } from "./keys";
import {
  NO_CLOSE_FRAME,
  SIZE_SETTLE,
  createLink,
  freshScreen,
  lastCall,
  type LinkState,
} from "./reconnect";
import {
  binaryBytes,
  describeClose,
  openSocket,
  sessionIdFromHash,
  socketURL,
  textBytes,
} from "./socket";
import { plainText, type BufferLine } from "./text";
import { bindTouchScroll } from "./touch";

// The stack of app.css's --font-mono, which has no variable at run time.
const FONT = '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace';
const FONT_SIZE = 13;

// token reads one of the app's colour tokens, for xterm.js's theme.
function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// webglAvailable reports whether the browser gives a WebGL2 context. The WebGL addon throws from
// open() when it cannot get one, so it is loaded only when this says it can; a context lost later
// is handled by the addon's own event.
function webglAvailable(): boolean {
  const gl = document.createElement("canvas").getContext("webgl2");
  if (gl === null) return false;
  gl.getExtension("WEBGL_lose_context")?.loseContext();
  return true;
}

function element(id: string): HTMLElement {
  const el = document.getElementById(id);
  if (el === null) throw new Error(`terminal.html has no #${id}`);
  return el;
}

// bufferRows gives plainText the buffer's rows as the terminal holds them: untrimmed, so a row that
// the next one continues keeps its trailing blanks (spec §56).
function* bufferRows(term: Terminal): Generator<BufferLine> {
  const buffer = term.buffer.active;
  for (let y = 0; y < buffer.length; y++) {
    const line = buffer.getLine(y);
    if (line === undefined) continue;
    yield { text: line.translateToString(false), wrapped: line.isWrapped };
  }
}

// statusText is the status line of a page that is not in a frame.
function statusText(state: LinkState): string {
  switch (state.phase) {
    case "connecting":
      return "Connecting…";
    case "waiting":
      return "Reconnecting…";
    case "closed":
      return describeClose(state.closed ?? { code: NO_CLOSE_FRAME, reason: "" });
    case "live":
      return "";
  }
}

function main(): void {
  const host = element("terminal");
  const status = element("status");
  const say = (text: string): void => {
    status.textContent = text;
    status.hidden = text === "";
  };

  // Framed, the panel shows the state and the page shows no status line (terminal.css).
  const framed = window.parent !== window;
  if (framed) document.documentElement.dataset.framed = "";

  const session = sessionIdFromHash(window.location.hash);
  if (session === null) {
    say("Open this page as /terminal.html#<session id>.");
    return;
  }
  const url = socketURL(api.terminalURL(session), window.location);

  // What the interface sets before the terminal exists, and what it holds after.
  let term: Terminal | null = null;
  let touch = false;
  let wantFocus = false;
  let renderer: Renderer = "dom";
  let unbindTouch: (() => void) | null = null;
  let onState: ((state: FrameState) => void) | null = null;
  let onChord: (() => void) | null = null;
  // Set when the terminal could not start (spec §68): the state the panel then gets, for good.
  let failed: FrameState | null = null;

  const frameState = (): FrameState => failed ?? { ...link.state(), renderer };
  const publish = (): void => {
    onState?.(frameState());
  };
  // In touch mode the keyboard must never rise, so xterm.js is never focused.
  const focusTerminal = (): void => {
    if (term !== null && !touch) term.focus();
  };

  const link = createLink({
    framed,
    open: (on) =>
      openSocket(
        url,
        () => (term === null ? { cols: 80, rows: 24 } : { cols: term.cols, rows: term.rows }),
        on,
      ),
    // RIS goes through the write queue, so it lands after whatever the old connection left in it
    // and before the history that follows, and it leaves an escape sequence the old connection was
    // cut inside, which term.reset() does not (spec §65).
    fresh: freshScreen(() => term),
    data: (bytes) => {
      term?.write(bytes);
    },
    changed: (state) => {
      publish();
      if (framed) return;
      say(statusText(state));
      if (state.phase === "live") focusTerminal();
    },
  });

  // Input to the shell: true when it went to an open connection, and then the terminal scrolls to
  // the bottom; otherwise it is dropped, never queued (spec §55).
  const sendInput = (bytes: Uint8Array<ArrayBuffer>): boolean => {
    const sent = link.send(bytes);
    if (sent) term?.scrollToBottom();
    return sent;
  };

  const applyTouch = (): void => {
    unbindTouch?.();
    unbindTouch = null;
    const t = term;
    if (t === null || !touch) return;
    t.blur();
    unbindTouch = bindTouchScroll(host, {
      scrollLines: (lines) => {
        t.scrollLines(lines);
      },
      // The terminal's height over its rows, in CSS pixels.
      cellHeight: () => (t.element?.clientHeight ?? 0) / t.rows,
    });
  };

  const copySelection = (): void => {
    const selected = term?.getSelection() ?? "";
    if (selected === "") return;
    try {
      void navigator.clipboard.writeText(selected).catch(() => undefined);
    } catch {
      // No clipboard in this context: the browser's own copy still works.
    }
  };

  const loomTerminal: LoomTerminal = {
    send: (bytes) => sendInput(bytes),
    key: (name: KeyName) =>
      sendInput(keyBytes(name, term?.modes.applicationCursorKeysMode ?? false)),
    text: () => (term === null ? "" : plainText(bufferRows(term))),
    setTouch: (on) => {
      touch = on;
      applyTouch();
    },
    setLink: (up) => {
      link.setLink(up);
    },
    reconnect: () => {
      link.reconnect();
    },
    focus: () => {
      if (term === null) wantFocus = true;
      else focusTerminal();
    },
    close: () => {
      link.close();
    },
    onState: (cb) => {
      onState = cb;
      cb(frameState());
    },
    onChord: (cb) => {
      onChord = cb;
    },
  };
  window.loomTerminal = loomTerminal;

  const start = (): void => {
    const t = new Terminal({
      cursorBlink: true,
      fontFamily: FONT,
      fontSize: FONT_SIZE,
      scrollback: 10000,
      theme: {
        background: token("--background"),
        foreground: token("--foreground"),
        cursor: token("--attention"),
        selectionBackground: token("--input"),
      },
    });
    term = t;
    const fit = new FitAddon();
    t.loadAddon(fit);
    // Loaded before open(): the addon waits for it (the spike's item 1 measured the fewest style
    // violations this way). The DOM renderer serves when WebGL is unavailable or its context is
    // lost, and terminalCSP allows its styles.
    if (webglAvailable()) {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => {
        webgl.dispose();
        renderer = "dom";
        publish();
      });
      t.loadAddon(webgl);
      renderer = "webgl";
    }
    t.attachCustomKeyEventHandler((ev) =>
      handleKey(ev, {
        chord: () => {
          onChord?.();
        },
        copy: copySelection,
      }),
    );
    t.open(host);
    fit.fit();

    t.onData((text) => {
      link.send(textBytes(text));
    });
    t.onBinary((text) => {
      link.send(binaryBytes(text));
    });
    // The new size goes to the daemon 100 ms after the last change of the window's size.
    const settle = lastCall((cols: number, rows: number) => {
      link.resize(cols, rows);
    }, SIZE_SETTLE);
    t.onResize(({ cols, rows }) => {
      settle(cols, rows);
    });
    new ResizeObserver(() => {
      fit.fit();
    }).observe(host);

    applyTouch();
    if (wantFocus) {
      wantFocus = false;
      focusTerminal();
    }
    publish();
    link.start();
    if (!framed) say(statusText(link.state()));
  };

  say("Loading…");
  // xterm.js measures a cell when it opens: wait for the font, or the grid is sized for the
  // fallback face.
  void document.fonts
    .load(`${FONT_SIZE}px "JetBrains Mono Variable"`)
    .catch(() => [])
    .then(() => {
      start();
    })
    .catch((err: unknown) => {
      // A start that threw (the WebGL addon, open) leaves the panel a closed state to show, and the
      // link, if it began, ends; a page that is not in a frame shows the same text in its status line.
      const state = startFailed(err, renderer);
      failed = state;
      link.close();
      publish();
      if (!framed) say(state.closed?.reason ?? "");
    });
}

main();
