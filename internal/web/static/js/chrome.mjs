// The app's pinned chrome (spec 2026-11-01): a header holding the screen's
// context row (its Head in screens.mjs) and a session's tabs, and a footer
// holding the status row on every screen with a snapshot. app.mjs draws both;
// the rows here only draw what they are given.
import { html } from "../lib/preact-htm.mjs";
import { glyphOf } from "./skin.mjs";

// FLEET_BACK is the way back every screen but the fleet offers.
export const FLEET_BACK = { href: "#", label: "fleet" };

// PILL is the status row's word for the link (app.mjs's link).
const PILL = { live: "LIVE", connecting: "CONNECTING", restarting: "RESTARTING", offline: "OFFLINE" };

// COUNTED are the groups the status row counts, in its order, with the words
// a count's label reads.
const COUNTED = [
  { key: "needs_you", words: "need you" },
  { key: "your_turn", words: "your turn" },
  { key: "working", words: "working" },
];

// clock is the time of day of a ms timestamp, as the stale bar gave it.
function clock(ms) {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// StatusRow is the footer's row: the link's pill, loom@host (with the
// last update's time while offline) and a count per group; a count opens the
// group's next session (onJump). While not live the counts are the cached
// snapshot's, drawn faint and still buttons; a zero is no button.
export function StatusRow({ link, host, updated, counts, onJump }) {
  const state = PILL[link] ? link : "offline";
  const live = state === "live";
  const when = state === "offline" && updated ? " · " + clock(updated) : "";
  return html`<div class=${"sc-status-row" + (state === "offline" ? " sc-off" : "")}>
    <span class=${"sc-pill sc-pill-" + state} aria-live="polite">${PILL[state]}</span>
    <span class="sc-host">${host ? html`<span class="sc-dim">loom@</span>${host}` : "loom"}${when}</span>
    ${COUNTED.map((g) => {
      const n = counts[g.key] || 0;
      const cls = "sc-count sc-count-" + g.key + (live ? "" : " sc-faint");
      const inner = html`<span aria-hidden="true">${glyphOf(g.key)}</span>${n}`;
      if (n === 0) return html`<span key=${g.key} class=${cls + " sc-nil"}>${inner}</span>`;
      return html`<button key=${g.key} class=${cls} type="button" aria-label=${n + " " + g.words + ": open the next"} onClick=${() => onJump(g.key)}>${inner}</button>`;
    })}
  </div>`;
}

// ContextRow is the header's first row: an optional way back (an href and a
// label, and an onClick that may take the click over), the screen's title as
// its h1 on one line, and the screen's actions (children).
export function ContextRow({ back, title, children }) {
  return html`<div class="sc-context">
    ${back ? html`<a class="sc-back" href=${back.href} onClick=${back.onClick}>‹ ${back.label}</a>` : null}
    <h1 class="sc-h1">${title}</h1>
    ${children}
  </div>`;
}

// SessionTabs are a session's views under its context row: its conversation
// (conversation: a Claude session; the tab key is "turns" and #s/<id> its
// address), its changes (changes: while its diff can be read, with its line
// counts), its info, its mockup and its flow (while its worktree holds one;
// the board, a row and a document all light it). A tab shows when it
// applies or is the current one, so a session that pauses on its changes
// keeps the tab under the user; a lone info tab draws no row.
export function SessionTabs({ s, current, conversation, changes }) {
  const base = "#s/" + s.id;
  const d = s.diff || {};
  const tabs = [];
  if (conversation) tabs.push({ key: "turns", label: "conversation", href: base });
  if (changes || current === "changes") {
    tabs.push({ key: "changes", label: "changes", href: base + "/diff", diff: d.added || d.removed ? d : null });
  }
  tabs.push({ key: "info", label: "info", href: base + "/info" });
  // The mockup tab shows while the session's brainstorm companion runs; its
  // dot while the screen is new this turn.
  if (s.mockup || current === "mockup") {
    tabs.push({ key: "mockup", label: "mockup", href: base + "/mockup", fresh: Boolean(s.mockup && s.mockup.fresh) });
  }
  // The flow tab shows while the session's worktree holds a flow
  // (s.flows), and on its own screens; its dot is amber while a run asks.
  const onFlow = current === "flow" || current === "flowstory" || current === "flowdoc";
  if ((s.flows && s.flows.length > 0) || onFlow) {
    tabs.push({ key: "flow", label: "flow", href: base + "/flow", attention: (s.flows || []).some((f) => f.asks) });
  }
  if (tabs.length === 1) return null;
  return html`<nav class="sc-tabs" aria-label="Session views">
    ${tabs.map(
      (t) => html`<a key=${t.key} class=${"sc-tab" + (t.key === current || (t.key === "flow" && onFlow) ? " sc-tab-on" : "")} href=${t.href} aria-current=${t.key === current || (t.key === "flow" && onFlow) ? "page" : undefined}>
        ${t.label}
        ${t.diff ? html`<span class="sc-tab-diff"><span class="sc-add">+${t.diff.added || 0}</span> <span class="sc-del">−${t.diff.removed || 0}</span></span>` : null}
        ${t.fresh ? html`<span class="sc-tab-dot" aria-label="new"></span>` : null}
        ${t.attention ? html`<span class="sc-tab-dot sc-tab-dot-attn" aria-label="asks"></span>` : null}
      </a>`,
    )}
  </nav>`;
}
