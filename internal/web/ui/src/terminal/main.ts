// The terminal's document (spec §23): xterm.js on a session's worktree shell, over the daemon's
// WebSocket. It is the only script that imports xterm.js, and the web layer serves its page under
// terminalCSP, which lets xterm.js write the <style> elements the app's policy drops. Part 1
// ships it bare: no reconnecting, no panel and no phone input.
import "@xterm/xterm/css/xterm.css";
import "../styles/app.css";
import "./terminal.css";

import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal } from "@xterm/xterm";

import { api } from "../api/request";
import {
  binaryBytes,
  describeClose,
  openSocket,
  sessionIdFromHash,
  socketURL,
  textBytes,
} from "./socket";

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

function start(host: HTMLElement, say: (text: string) => void, session: number): void {
  const term = new Terminal({
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
  const fit = new FitAddon();
  term.loadAddon(fit);
  // Loaded before open(): the addon waits for it (the spike's item 1 measured the fewest style
  // violations this way). The DOM renderer serves when WebGL is unavailable or its context is
  // lost, and terminalCSP allows its styles.
  if (webglAvailable()) {
    const webgl = new WebglAddon();
    webgl.onContextLoss(() => {
      webgl.dispose();
    });
    term.loadAddon(webgl);
  }
  term.open(host);
  fit.fit();

  const url = socketURL(api.terminalURL(session), window.location);
  const sock = openSocket(url, () => ({ cols: term.cols, rows: term.rows }), {
    state: (state, closed) => {
      if (state === "connecting") {
        say("Connecting…");
      } else if (state === "live") {
        say("");
        term.focus();
      } else {
        say(describeClose(closed ?? { code: 1006, reason: "" }));
      }
    },
    data: (bytes) => {
      term.write(bytes);
    },
  });
  term.onData((text) => {
    sock.send(textBytes(text));
  });
  term.onBinary((text) => {
    sock.send(binaryBytes(text));
  });
  term.onResize(({ cols, rows }) => {
    sock.resize(cols, rows);
  });
  new ResizeObserver(() => {
    fit.fit();
  }).observe(host);
  window.addEventListener("pagehide", () => {
    sock.close();
  });
}

function main(): void {
  const host = element("terminal");
  const status = element("status");
  const say = (text: string): void => {
    status.textContent = text;
    status.hidden = text === "";
  };

  const session = sessionIdFromHash(window.location.hash);
  if (session === null) {
    say("Open this page as /terminal.html#<session id>.");
    return;
  }
  say("Loading…");
  // xterm.js measures a cell when it opens: wait for the font, or the grid is sized for the
  // fallback face.
  void document.fonts
    .load(`${FONT_SIZE}px "JetBrains Mono Variable"`)
    .catch(() => [])
    .then(() => {
      start(host, say, session);
    })
    .catch((err: unknown) => {
      say(`The terminal failed to start: ${err instanceof Error ? err.message : "unknown error"}`);
    });
}

main();
