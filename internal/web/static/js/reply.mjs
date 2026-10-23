// The reply box a session's screens end in: the quick replies while it is
// empty, a text box and Send. run is the screen's own runner, so the box
// and the screen's actions are off together while either is in flight.
import { html, useState } from "../lib/preact-htm.mjs";
import { utf8Len } from "./sheets.mjs";

export const MAX_REPLY = 64 * 1024;

export function ReplyBox({ snap, off, run }) {
  const [text, setText] = useState("");
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;
  const replies = snap.quick_replies || [];

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
        rows="2"
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
