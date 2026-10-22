// The fleet screen, and the helpers the session screen shares with it.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { pref } from "./store.mjs";

export const GROUPS = [
  { key: "needs_you", title: "Needs you" },
  { key: "your_turn", title: "Your turn" },
  { key: "working", title: "Working" },
  { key: "paused", title: "Paused" },
];

export function allSessions(snap) {
  return Object.values((snap && snap.sessions) || {}).flat();
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

export function diffText(s) {
  const d = s.diff;
  if (!d || (!d.added && !d.removed)) return "";
  return "+" + d.added + " −" + d.removed;
}

export function prText(s) {
  const pr = s.pr;
  if (!pr) return "";
  const checks = pr.checks && pr.checks !== "none" ? ", checks " + pr.checks : "";
  return "PR #" + pr.number + " " + pr.state + checks;
}

function Card({ s, chip, dot }) {
  const reason = s.group === "needs_you" && s.wait ? s.wait.reason : "";
  const detail = s.group === "needs_you" && s.wait ? s.wait.detail : "";
  const meta = [s.label, diffText(s), prText(s)].filter(Boolean).join(" · ");
  return html`
    <a class=${"sc-card sc-g-" + s.group} href=${"#s/" + s.id}>
      <div class="sc-card-top">
        <span class="sc-card-title">${s.title}</span>
        ${dot ? html`<span class="sc-dot" role="img" aria-label="New turn"></span>` : null}
        <span class="sc-age">${ago(s.since)}</span>
      </div>
      ${chip ? html`<span class="sc-chip">${chip}</span>` : null}
      ${reason ? html`<div class="sc-wait">${reason}</div>` : null}
      ${detail ? html`<div class="sc-detail">${detail}</div>` : null}
      ${!reason && s.last_message ? html`<div class="sc-last">${s.last_message}</div>` : null}
      <div class="sc-meta">${meta}</div>
    </a>
  `;
}

export function Fleet({ snap, stale, go }) {
  const [filter, setFilter] = useState(() => pref("filter") || "");
  const [, setTick] = useState(0);

  // Ages and the dot move with the clock, not only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);

  const everything = allSessions(snap);
  useEffect(() => {
    pruneSeen(everything.map((s) => s.id));
  }, [snap]);

  if (!snap) return html`<section class="sc-screen"><p class="sc-dim">Loading…</p></section>`;

  const workspaces = snap.workspaces || [];
  const known = new Set(workspaces.map((w) => String(w.id)));
  const active = known.has(filter) ? filter : "";
  const names = new Map(workspaces.map((w) => [String(w.id), w.label || w.name]));
  const groups = groupSessions(everything, active);
  const seen = readSeen();
  const shown = GROUPS.reduce((n, g) => n + groups[g.key].length, 0);

  function pick(id) {
    setFilter(id);
    pref("filter", id);
  }

  const renderCards = (list) =>
    list.map(
      (s) => html`<${Card}
        key=${s.id}
        s=${s}
        chip=${active === "" && workspaces.length > 1 ? names.get(String(s.workspace)) : ""}
        dot=${hasNewTurn(s, seen)}
      />`,
    );

  return html`
    <section class="sc-screen">
      <header class="sc-header">
        <h1 class="sc-h1">${snap.host || "loom"}</h1>
        <a class="sc-icon" href="#settings" aria-label="Settings">⚙</a>
        <button class="sc-btn sc-primary" type="button" disabled=${stale} onClick=${() => go("#new")}>New</button>
      </header>
      ${workspaces.length > 1
        ? html`<nav class="sc-chips" aria-label="Workspaces">
            <button class=${"sc-chip-btn" + (active === "" ? " sc-on" : "")} type="button" aria-pressed=${active === ""} onClick=${() => pick("")}>All</button>
            ${workspaces.map(
              (w) => html`<button
                key=${w.id}
                class=${"sc-chip-btn" + (active === String(w.id) ? " sc-on" : "")}
                type="button"
                aria-pressed=${active === String(w.id)}
                onClick=${() => pick(String(w.id))}
              >${w.label || w.name}</button>`,
            )}
          </nav>`
        : null}
      ${shown === 0 ? html`<p class="sc-dim sc-empty">No sessions${active ? " in this workspace" : ""}.</p>` : null}
      ${GROUPS.filter((g) => g.key !== "paused" && groups[g.key].length > 0).map(
        (g) => html`<section key=${g.key} class="sc-group">
          <h2 class=${"sc-group-title sc-t-" + g.key}>${g.title} <span class="sc-count">${groups[g.key].length}</span></h2>
          ${renderCards(groups[g.key])}
        </section>`,
      )}
      ${groups.paused.length > 0
        ? html`<details class="sc-group">
            <summary class="sc-group-title sc-t-paused">Paused <span class="sc-count">${groups.paused.length}</span></summary>
            ${renderCards(groups.paused)}
          </details>`
        : null}
    </section>
  `;
}
