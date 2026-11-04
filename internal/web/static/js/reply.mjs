// The reply box a session's views end in: the quick replies while it is
// empty, a text box and Send; and useRun, their shared runner. run is the
// screen's own, so the box and the screen's actions are off together while
// either is in flight.
import { html, useState } from "../lib/preact-htm.mjs";
import { utf8Len } from "./sheets.mjs";

export const MAX_REPLY = 64 * 1024;

// drafts are the unsent replies by session id: a tab switch remounts the
// screen, and what was typed stays. They last as long as the page.
const drafts = new Map();

// useRun posts an action on session id, holding busy while it runs, and says
// when its result will follow on the stream (a 202).
export function useRun(act, toast, id) {
  const [busy, setBusy] = useState(false);
  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", "/api/v1/sessions/" + id + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }
  return { busy, run };
}

export function ReplyBox({ snap, off, run, id }) {
  const [text, setShown] = useState(() => drafts.get(id) || "");
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;
  const replies = snap.quick_replies || [];

  function setText(v) {
    setShown(v);
    if (v) drafts.set(id, v);
    else drafts.delete(id);
  }

  async function send() {
    const r = await run("send", { text: reply });
    if (r.ok && r.status !== 202) setText("");
  }

  return html`<form class="sc-reply" onSubmit=${(e) => { e.preventDefault(); if (!off && reply && !tooLong) send(); }}>
    ${text === "" && replies.length > 0
      ? html`<div class="sc-quick" role="group" aria-label="Quick replies">
          ${replies.map(
            (q) => html`<button key=${q} class="sc-chip-btn" type="button" disabled=${off} onClick=${() => setText(q)}><span>${q}</span></button>`,
          )}
        </div>`
      : null}
    <div class="sc-reply-row">
      <textarea
        rows="1"
        placeholder="reply, or /command…"
        aria-label="Reply"
        value=${text}
        onInput=${(e) => setText(e.target.value)}
      ></textarea>
      <button class="sc-btn sc-primary" type="submit" disabled=${off || !reply || tooLong}>send</button>
    </div>
    ${tooLong ? html`<div class="sc-small sc-err">Too long: a reply is at most 64 KiB.</div>` : null}
  </form>`;
}
