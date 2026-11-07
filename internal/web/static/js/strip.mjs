// The fact strip pinned under a session's tabs on the conversation: its
// status and age, its changes, its PR and its account's share, on one
// ellipsised line that opens the info tab, and while the session has jobs
// that have yet to end, their badge on the right, which opens the job it
// names (jobs_focus) or, for "⚙ queued", the info tab. A fact the session
// lacks is left out.
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
  const info = "#s/" + s.id + "/info";
  const line = facts.map((f, i) => html`<span key=${f.key}>${i > 0 ? " · " : ""}${f.node}</span>`);
  if (!s.jobs_label) return html`<a class="sc-strip" href=${info}>${line}</a>`;
  const job = s.jobs_focus ? "#s/" + s.id + "/job/" + s.jobs_focus : info;
  return html`<div class="sc-strip sc-strip-split">
    <a class="sc-strip-main" href=${info}>${line}</a>
    <a class="sc-strip-job" href=${job}>${s.jobs_label}</a>
  </div>`;
}

// AgentStrip is the strip under the tabs of a subagent's screen, by the
// page's agent.state: running (with its age since agent.started), finished
// (idle for a teammate, which stays up for more work) or unknown, then the
// agent's type. While the session waits on you it ends in a link back to the
// conversation, where the needs-you box is. It is not itself a link, so the
// link to the session nests in no other.
export function AgentStrip({ s, agent, now }) {
  let lead;
  if (agent.state === "running") {
    lead = html`<span class="sc-accent">${["◐ subagent running", ago(agent.started, now)].filter(Boolean).join(" ")}</span>`;
  } else if (agent.state === "finished") {
    lead = html`<span class="sc-dim">${agent.teammate ? "○ idle" : "✓ finished"}</span>`;
  } else {
    lead = html`<span class="sc-dim">subagent</span>`;
  }
  return html`<div class="sc-strip sc-strip-agent">
    <span class="sc-strip-main">${lead}${agent.type ? " · " + agent.type : ""}</span>
    ${s.group === "needs_you" ? html`<a class="sc-strip-needs" href=${"#s/" + s.id}>● session needs you ›</a>` : null}
  </div>`;
}
