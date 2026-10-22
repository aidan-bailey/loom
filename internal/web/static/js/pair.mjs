// The two screens before the fleet: the Add to Home Screen steps, shown in
// Safari on an iPhone or iPad (the installed app keeps its own cookies, so
// pairing in Safari would pair the wrong place), and pairing itself.
import { html, useState } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { pref } from "./store.mjs";

const NOT_IN_ALPHABET = /[^23456789ABCDEFGHJKMNPQRSTUVWXYZ]/g;
const CODE_LEN = 8;

// needsInstall reports whether to show the Add to Home Screen steps: on an
// iPhone or iPad, outside the installed app, unless the user chose the
// browser.
export function needsInstall() {
  const ua = navigator.userAgent || "";
  const ios = /iPhone|iPad|iPod/.test(ua) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const standalone =
    navigator.standalone === true || (window.matchMedia && window.matchMedia("(display-mode: standalone)").matches);
  return ios && !standalone && pref("browser") !== true;
}

// formatCode upper-cases what was typed, drops what the alphabet lacks and
// puts the dash after four characters: K7QM-2XPA.
function formatCode(raw) {
  const code = raw.toUpperCase().replace(NOT_IN_ALPHABET, "").slice(0, CODE_LEN);
  return code.length > 4 ? code.slice(0, 4) + "-" + code.slice(4) : code;
}

// pairError words the answer of POST /api/v1/pair: the server's own text for
// 400 (a wrong, used or expired code), 429 (locked), 409 (the name is taken or
// 16 devices are paired) and 503 (the web is off); the 429's text already
// says to run loom web pair again.
function pairError(status, data) {
  if (status === 0) return "Can't reach loom. Check the connection and try again.";
  return data && data.error ? data.error : "That didn't work.";
}

export function AddToHome({ onContinue }) {
  return html`
    <div class="shell">
      <main>
        <div class="pair">
          <h1 class="wordmark">Loom</h1>
          <p class="lede">Add Loom to your Home Screen to use it. The installed app keeps its own pairing, separate from this tab.</p>
          <ol class="steps">
            <li>Tap the Share button in Safari's toolbar.</li>
            <li>Scroll down and tap <strong>Add to Home Screen</strong>.</li>
            <li>Tap <strong>Add</strong>, then open Loom from your Home Screen.</li>
          </ol>
          <p class="hint">The app then asks for a pairing code.</p>
          <button type="button" class="link" onClick=${onContinue}>Continue in the browser anyway</button>
        </div>
      </main>
    </div>
  `;
}

export function Pair({ onPaired }) {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const ready = code.replace("-", "").length === CODE_LEN;

  const submit = async (ev) => {
    ev.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setMessage("");
    const { status, data } = await call("POST", "/api/v1/pair", { code });
    setBusy(false);
    if (status >= 200 && status < 300 && data && data.device) {
      onPaired(data.device);
      return;
    }
    setMessage(pairError(status, data));
  };

  return html`
    <div class="pair">
      <h1 class="wordmark">Loom</h1>
      <p class="lede">Pair this phone with your loom.</p>
      <ol class="steps">
        <li>On the computer running loom, run <code>loom web pair</code>.</li>
        <li>Enter the code it prints. It works once, for 10 minutes.</li>
      </ol>
      <form onSubmit=${submit}>
        <input
          class="text code"
          type="text"
          inputmode="text"
          autocapitalize="characters"
          autocomplete="off"
          autocorrect="off"
          spellcheck="false"
          autofocus
          placeholder="K7QM-2XPA"
          aria-label="Pairing code"
          value=${code}
          onInput=${(ev) => {
            const formatted = formatCode(ev.target.value);
            ev.target.value = formatted;
            setCode(formatted);
          }}
        />
        <button type="submit" class="primary" disabled=${!ready || busy}>${busy ? "Pairing…" : "Pair"}</button>
      </form>
      ${message ? html`<p class="form-error" role="alert">${message}</p>` : null}
    </div>
  `;
}
