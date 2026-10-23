// The fleet screen, and the helpers the session screen shares with it.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { pref } from "./store.mjs";
import { glyphOf, GearIcon } from "./skin.mjs";
import { cells, resetPassed, resetText, isStale, planName, shareText } from "./usage.mjs";

export const GROUPS = [
  { key: "needs_you", title: "Needs you" },
  { key: "your_turn", title: "Your turn" },
  { key: "working", title: "Working" },
  { key: "paused", title: "Paused" },
];

// LINK is the status line's word for the stream's state (app.mjs's link).
const LINK = { live: "LIVE", restarting: "RESTARTING", offline: "OFFLINE" };

export function allSessions(snap) {
  return Object.values((snap && snap.sessions) || {}).flat();
}

// shownWorkspaces are the workspaces the fleet lists, in the daemon's order
// (the open tabs first). One without the flag, from an older daemon, is shown.
export function shownWorkspaces(snap) {
  return ((snap && snap.workspaces) || []).filter((w) => w.shown !== false);
}

// groupSessions returns {needs_you, your_turn, working, paused}, each an
// array: Needs you with the longest wait first, the others newest first.
// A filter of "" keeps every workspace.
export function groupSessions(sessions, filter) {
  const out = { needs_you: [], your_turn: [], working: [], paused: [] };
  for (const s of sessions) {
    if (filter !== "" && String(s.workspace) !== filter) continue;
    // A group this build does not know is listed under Paused, never dropped.
    (out[s.group] || out.paused).push(s);
  }
  const t = (s) => Date.parse(s.since) || 0;
  out.needs_you.sort((a, b) => t(a) - t(b));
  for (const k of ["your_turn", "working", "paused"]) out[k].sort((a, b) => t(b) - t(a));
  return out;
}

// readSeen is {"<session id>": ms}: when this phone last opened the session.
export function readSeen() {
  try {
    const v = JSON.parse(pref("seen") || "{}");
    return v && typeof v === "object" ? v : {};
  } catch (e) {
    return {};
  }
}

export function markSeen(id) {
  const seen = readSeen();
  seen[id] = Date.now();
  pref("seen", JSON.stringify(seen));
}

// pruneSeen drops the times of sessions the daemon no longer lists. An
// empty list prunes nothing: it is more likely not loaded than a fleet of none.
export function pruneSeen(ids) {
  if (ids.length === 0) return;
  const keep = new Set(ids.map(String));
  const seen = readSeen();
  let changed = false;
  for (const k of Object.keys(seen)) {
    if (!keep.has(k)) {
      delete seen[k];
      changed = true;
    }
  }
  if (changed) pref("seen", JSON.stringify(seen));
}

// hasNewTurn: a Your turn session whose turn ended after this phone last
// opened it, or that it never opened.
export function hasNewTurn(s, seen) {
  if (s.group !== "your_turn") return false;
  const opened = seen[s.id];
  return opened === undefined || (Date.parse(s.since) || 0) > opened;
}

export function ago(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const secs = Math.max(0, Math.round((now - t) / 1000));
  if (secs < 60) return "now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return mins + "m";
  const hours = Math.floor(mins / 60);
  if (hours < 48) return hours + "h";
  return Math.floor(hours / 24) + "d";
}

// runningAgents is how many of a session's subagents are running.
export function runningAgents(s) {
  return (s.agents || []).filter((a) => !a.idle).length;
}

function plural(n, word) {
  return n + " " + word + (n === 1 ? "" : "s");
}

// PrMark is a PR's state on a row: ✓, ✗ or … for an open PR's checks, else
// draft, merged or closed.
function PrMark({ pr }) {
  if (pr.state !== "open") return html`<span>${pr.state}</span>`;
  if (pr.checks === "passing") return html`<span class="sc-add">✓</span>`;
  if (pr.checks === "failing") return html`<span class="sc-del">✗</span>`;
  if (pr.checks === "pending") return html`<span>…</span>`;
  return null;
}

// Row is one session: its glyph, title and age; why it waits, or what it said
// last; and what it holds (its label where the group's title doesn't say it,
// running agents, changes, PR, workspace).
function Row({ s, chip, fresh }) {
  const waiting = s.group === "needs_you";
  const reason = waiting && s.wait ? s.wait.reason : "";
  const detail = waiting && s.wait ? s.wait.detail : "";
  const title = (GROUPS.find((g) => g.key === s.group) || {}).title;
  const agents = runningAgents(s);
  const d = s.diff || {};
  const meta = [];
  if (s.label && s.label !== title) meta.push(html`<span>${s.label}</span>`);
  if (agents > 0) meta.push(html`<span>${plural(agents, "agent")}</span>`);
  if (d.added || d.removed) meta.push(html`<span><span class="sc-add">+${d.added}</span> <span class="sc-del">−${d.removed}</span></span>`);
  if (s.pr) meta.push(html`<span>PR #${s.pr.number} <${PrMark} pr=${s.pr} /></span>`);
  if (chip && !waiting) meta.push(html`<span class="sc-ws">${chip}</span>`);
  return html`
    <a class="sc-row-s" href=${"#s/" + s.id}>
      <div class="sc-row-top">
        <span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span>
        <span class="sc-row-title">${s.title}</span>
        ${fresh ? html`<span class="sc-new">NEW</span>` : null}
        <span class="sc-age">${ago(s.since)}</span>
      </div>
      ${reason || (waiting && chip)
        ? html`<div class="sc-row-line">
            ${reason ? html`<span class="sc-attn">${reason}</span>` : null}
            ${waiting && chip ? html`<span class="sc-ws">${chip}</span>` : null}
          </div>`
        : null}
      ${detail ? html`<div class="sc-detail">${detail}</div>` : null}
      ${!waiting && s.last_message ? html`<div class="sc-last">${s.last_message}</div>` : null}
      ${meta.length > 0 ? html`<div class="sc-meta">${meta}</div>` : null}
    </a>
  `;
}

// scrollToGroup brings a group's heading into view; the route stays.
function scrollToGroup(key) {
  const el = document.getElementById("g-" + key);
  if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
}

// Tile is a count tile: a button that scrolls to its group, or a plain box at
// 0, which has nowhere to go.
function Tile({ group, title, count, sub }) {
  const cls = "sc-box sc-tile sc-tile-" + group + (count === 0 ? " sc-zero" : "");
  const inner = html`
    <span class="sc-legend"><span class=${"sc-glyph sc-glyph-" + group} aria-hidden="true">${glyphOf(group)}</span> ${title}</span>
    <span class="sc-tile-n">${count}</span>
    ${sub ? html`<span class="sc-tile-sub">${sub}</span>` : null}
  `;
  if (count === 0) return html`<div class=${cls}>${inner}</div>`;
  return html`<button class=${cls} type="button" onClick=${() => scrollToGroup(group)}>${inner}</button>`;
}

function Bar({ w, now }) {
  const on = resetPassed(w, now) ? 0 : cells(w.pct);
  return html`<span class="sc-bar" aria-hidden="true"><span class="sc-bar-on">${"█".repeat(on)}</span><span class="sc-bar-off">${"░".repeat(8 - on)}</span></span>`;
}

function Window({ label, w, now }) {
  if (!w) return null;
  return html`
    <div class="sc-win">
      <span class="sc-dim">${label}</span>
      <span class="sc-win-pct">${shareText(w, now)}</span>
      <${Bar} w=${w} now=${now} />
      <span class="sc-win-reset sc-dim">${resetPassed(w, now) ? "" : resetText(w.resets_at, now)}</span>
    </div>
  `;
}

// UsageBoxes is a box per account with plan limits, its name and plan in the
// border and a row per window; a sample past the daemon's bound is dimmed and
// shows its age.
function UsageBoxes({ usage, staleAfter, now }) {
  if (!usage || usage.length === 0) return null;
  return html`
    <section class="sc-usage" aria-label="Subscription usage">
      ${usage.map((u) => {
        const old = isStale(u.at, staleAfter, now);
        return html`<div key=${u.account} class=${"sc-box sc-usage-box" + (old ? " sc-stale" : "")}>
          <span class="sc-legend">${u.account}${u.plan ? html`<span class="sc-dim"> · ${planName(u.plan)}</span>` : null}${old ? html`<span class="sc-dim"> · ${ago(u.at, now)}</span>` : null}</span>
          <${Window} label="5h" w=${u.five_hour} now=${now} />
          <${Window} label="7d" w=${u.seven_day} now=${now} />
        </div>`;
      })}
    </section>
  `;
}

export function Fleet({ snap, stale, link, go }) {
  const [filter, setFilter] = useState(() => pref("filter") || "");
  const [, setTick] = useState(0);

  // Ages, the NEW marks and the usage's staleness move with the clock, not
  // only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);

  const everything = allSessions(snap);
  useEffect(() => {
    pruneSeen(everything.map((s) => s.id));
  }, [snap]);

  if (!snap) return html`<section class="sc-screen"><p class="sc-dim">Loading…</p></section>`;

  const workspaces = shownWorkspaces(snap);
  const shown = new Set(workspaces.map((w) => String(w.id)));
  const active = shown.has(filter) ? filter : "";
  const names = new Map(workspaces.map((w) => [String(w.id), w.label || w.name]));
  const groups = groupSessions(everything.filter((s) => shown.has(String(s.workspace))), active);
  const seen = readSeen();
  const now = Date.now();
  const listed = GROUPS.reduce((n, g) => n + groups[g.key].length, 0);
  const oldest = groups.needs_you.length > 0 ? ago(groups.needs_you[0].since, now) : "";
  const fresh = groups.your_turn.filter((s) => hasNewTurn(s, seen)).length;
  const agents = groups.working.reduce((n, s) => n + runningAgents(s), 0);
  const chipOf = (s) => (active === "" && workspaces.length > 1 ? names.get(String(s.workspace)) || "" : "");

  function pick(id) {
    setFilter(id);
    pref("filter", id);
  }

  const rows = (list) => list.map((s) => html`<${Row} key=${s.id} s=${s} chip=${chipOf(s)} fresh=${hasNewTurn(s, seen)} />`);

  return html`
    <section class="sc-screen sc-fleet">
      <header class="sc-header">
        <h1 class="sc-h1"><span class="sc-dim sc-light">loom@</span>${snap.host || "loom"}</h1>
        <a class="sc-icon" href="#settings" aria-label="Settings"><${GearIcon} /></a>
        <button class="sc-btn sc-primary" type="button" disabled=${stale} onClick=${() => go("#new")}>+ new</button>
      </header>
      <nav class="sc-tiles" aria-label="Sessions by state">
        <${Tile} group="needs_you" title="Needs you" count=${groups.needs_you.length} sub=${oldest ? "oldest " + oldest : ""} />
        <${Tile} group="your_turn" title="Your turn" count=${groups.your_turn.length} sub=${fresh > 0 ? fresh + " new" : ""} />
        <${Tile} group="working" title="Working" count=${groups.working.length} sub=${agents > 0 ? plural(agents, "agent") : ""} />
      </nav>
      <${UsageBoxes} usage=${snap.usage} staleAfter=${snap.usage_stale_after_s} now=${now} />
      ${workspaces.length > 1
        ? html`<nav class="sc-chips" aria-label="Workspaces">
            <button class=${"sc-chip-btn" + (active === "" ? " sc-on" : "")} type="button" aria-pressed=${active === ""} onClick=${() => pick("")}><span>all</span></button>
            ${workspaces.map(
              (w) => html`<button
                key=${w.id}
                class=${"sc-chip-btn" + (active === String(w.id) ? " sc-on" : "")}
                type="button"
                aria-pressed=${active === String(w.id)}
                onClick=${() => pick(String(w.id))}
              ><span>${w.label || w.name}</span></button>`,
            )}
          </nav>`
        : null}
      ${listed === 0 ? html`<p class="sc-dim sc-empty">No sessions${active ? " in this workspace" : ""}.</p>` : null}
      ${GROUPS.filter((g) => g.key !== "paused" && groups[g.key].length > 0).map(
        (g) => html`<section key=${g.key} class="sc-group">
          <h2 id=${"g-" + g.key} class=${"sc-group-title sc-t-" + g.key}>${g.title} ${groups[g.key].length}</h2>
          ${rows(groups[g.key])}
        </section>`,
      )}
      ${groups.paused.length > 0
        ? html`<details class="sc-group">
            <summary class="sc-group-title sc-t-paused">Paused ${groups.paused.length}</summary>
            ${rows(groups.paused)}
          </details>`
        : null}
      <footer class="sc-status">
        <span class=${"sc-link sc-link-" + (LINK[link] ? link : "offline")}>${LINK[link] || LINK.offline}</span>
        <span>${snap.host}</span>
        <span class="sc-sep" aria-hidden="true">│</span>
        <span>${plural(listed, "session")}</span>
        <span class="sc-fill"></span>
        ${groups.paused.length > 0 ? html`<span class="sc-dim">${groups.paused.length} paused</span>` : null}
      </footer>
    </section>
  `;
}
