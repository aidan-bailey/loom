// The app's pinned header (spec 2026-11-01): the status row on every screen
// with a snapshot, then the screen's context row (its Head in screens.mjs),
// then a session's tabs. app.mjs draws the header; the rows here only draw
// what they are given.
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

// StatusRow is the header's first row: the link's pill, loom@host (with the
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

// ContextRow is the header's second row: an optional way back, the screen's
// title as its h1 on one line, and the screen's actions (children).
export function ContextRow({ back, title, children }) {
  return html`<div class="sc-context">
    ${back ? html`<a class="sc-back" href=${back.href}>‹ ${back.label}</a>` : null}
    <h1 class="sc-h1">${title}</h1>
    ${children}
  </div>`;
}
