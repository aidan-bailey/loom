// The needs-you box: why a session waits, the dialogs its Claude holds open
// (asks.mjs) and the way into Claude. The conversation draws it at its foot
// while the session's group is needs_you.
import { html } from "../lib/preact-htm.mjs";
import { GLYPHS } from "./skin.mjs";
import { Asks } from "./asks.mjs";

export function NeedsYou({ s, off, run }) {
  return html`<div class="sc-box sc-attn-box">
    <span class="sc-legend">${GLYPHS.needs_you} Needs you</span>
    <div class="sc-attn">${(s.wait && s.wait.reason) || "Needs you"}</div>
    ${s.wait && s.wait.detail && !(s.asks && s.asks.length) ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
    <${Asks} s=${s} off=${off} run=${run} />
    ${s.remote_url
      ? html`<a class=${"sc-btn sc-block" + (s.asks && s.asks.length ? "" : " sc-primary")} href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
      : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
  </div>`;
}
