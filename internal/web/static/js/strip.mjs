// The fact strip pinned under a session's tabs on the conversation: its
// status and age, its changes, its PR and its account's share, on one
// ellipsised line that opens the info tab. A fact the session lacks is left
// out.
import { html } from "../lib/preact-htm.mjs";
import { ago, PrMark } from "./fleet.mjs";
import { glyphOf } from "./skin.mjs";
import { rowShare } from "./usage.mjs";

export function FactStrip({ s, snap, now }) {
  const d = s.diff || {};
  const share = rowShare(snap, s, now);
  const facts = [
    {
      key: "status",
      node: html`<span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span> ${(s.label || "").toLowerCase()}${s.since ? " " + ago(s.since, now) : ""}`,
    },
  ];
  if (d.added || d.removed) {
    facts.push({ key: "diff", node: html`<span class="sc-add">+${d.added || 0}</span> <span class="sc-del">−${d.removed || 0}</span>` });
  }
  if (s.pr) facts.push({ key: "pr", node: html`PR #${s.pr.number} <${PrMark} pr=${s.pr} />` });
  if (share) facts.push({ key: "share", node: html`<span class=${share.hot ? "sc-hot" : ""}>${share.text}</span>` });
  return html`<a class="sc-strip" href=${"#s/" + s.id + "/info"}>
    ${facts.map((f, i) => html`<span key=${f.key}>${i > 0 ? " · " : ""}${f.node}</span>`)}
  </a>`;
}
