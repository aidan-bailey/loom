// The info tab (#s/<id>/info, and #s/<id> for a session that does not run
// Claude): its tiles, its agents, the actions, and the reply box with the
// quick replies. The changes tile opens the changes screen. The conversation
// is turns.mjs's and the needs-you box needs.mjs's.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { ConfirmSheet } from "./sheets.mjs";
import { allSessions, ago, markSeen, sessionOf } from "./fleet.mjs";
import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";
import { FactStrip } from "./strip.mjs";
import { glyphOf, OutIcon, ChevronIcon } from "./skin.mjs";
import { usageFor, shareText, isStale, resetPassed, resetText, shownWindow } from "./usage.mjs";
import { ReplyBox, useRun } from "./reply.mjs";

// actionsFor says which buttons to show. It follows status only; the
// daemon's admit stays the authority and a refusal comes back as a toast.
// It shows only what admit accepts: every op but resume is refused while the
// session is Loading or Deleting, and resume is refused unless it is Paused
// (a Recoverable or Not started session has nothing to resume). diff is the
// changes screen's link, as send: ReadDiff refuses a busy session and one in
// the paused group.
export function actionsFor(s) {
  const busy = s.status === "Loading" || s.status === "Deleting";
  const paused = s.group === "paused";
  // Pause keeps the worktree, so push and merge work on a Paused session; a
  // Recoverable or Not started one may have no worktree to work on.
  const tree = !paused || s.status === "Paused";
  return {
    pause: !busy && !paused,
    resume: s.status === "Paused",
    push: !busy && tree,
    merge: !busy && tree,
    send: !busy && !paused,
    diff: !busy && !paused,
    kill: !busy,
  };
}

// mergeCandidates: the other sessions of the same workspace, whose branch
// can be merged into this one.
export function mergeCandidates(snap, s) {
  return allSessions(snap).filter((o) => o.workspace === s.workspace && o.id !== s.id);
}

// parityText is the TUI's parity token: "↑2 ↓0", "✓ even" at 0 and 0, and ""
// while parity is unknown.
export function parityText(s) {
  if (!s.parity_known) return "";
  if (!s.ahead && !s.behind) return "✓ even";
  return "↑" + s.ahead + " ↓" + s.behind;
}

// checksText is the PR tile's sub-line: an open PR's checks, else its state.
export function checksText(pr) {
  if (pr.state !== "open") return pr.state;
  return { passing: "✓ checks pass", pending: "checks pending", failing: "✗ checks fail", none: "no checks" }[pr.checks] || "";
}

// SubLine is the line under a session's title on its screens: its status
// and age, its workspace and its branch.
export function SubLine({ s, snap, now }) {
  const ws = (snap.workspaces || []).find((w) => w.id === s.workspace);
  return html`<div class="sc-subline">
    <span><span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span> ${s.label.toLowerCase()}${s.since ? " · " + ago(s.since, now) : ""}</span>
    ${ws ? html`<span class="sc-ws">${ws.label || ws.name}</span>` : null}
    ${s.branch ? html`<span>${s.branch}</span>` : null}
  </div>`;
}

// Tiles are the session's facts as boxes: its changes (a link to the changes
// screen when diff is set), its pull request (a link to it on GitHub when the
// daemon has its URL) and its account's usage (the shown window's share and
// reset, then the other window's). A tile whose data is missing is left out,
// and the row reflows.
function Tiles({ s, snap, now, diff }) {
  const tiles = [];
  const d = s.diff || {};
  if (d.added || d.removed || s.parity_known) {
    const parity = parityText(s);
    const inner = html`
      <span class="sc-legend">Changes</span>
      <span class="sc-tile-fig"><span class="sc-add">+${d.added || 0}</span> <span class="sc-del">−${d.removed || 0}</span>${diff ? html`<${ChevronIcon} />` : null}</span>
      ${parity ? html`<span class="sc-tile-sub">${parity}</span>` : null}
    `;
    tiles.push(
      diff
        ? html`<a key="changes" class="sc-box sc-tile" href=${"#s/" + s.id + "/diff"}>${inner}</a>`
        : html`<div key="changes" class="sc-box sc-tile">${inner}</div>`,
    );
  }
  if (s.pr) {
    const inner = html`
      <span class="sc-legend">PR</span>
      <span class="sc-tile-fig">#${s.pr.number}${s.pr.url ? html`<${OutIcon} />` : null}</span>
      <span class="sc-tile-sub">${checksText(s.pr)}</span>
    `;
    tiles.push(
      s.pr.url
        ? html`<a key="pr" class="sc-box sc-tile" href=${s.pr.url} target="_blank" rel="noopener noreferrer">${inner}</a>`
        : html`<div key="pr" class="sc-box sc-tile">${inner}</div>`,
    );
  }
  const u = usageFor(snap, s.account);
  const sw = shownWindow(u);
  if (sw) {
    const old = isStale(u.at, snap.usage_stale_after_s, now);
    const reset = (w) => (resetPassed(w, now) ? "" : resetText(w.resets_at, now));
    const first = [sw.label, reset(sw.w)].filter(Boolean).join(" · ");
    const second = sw.other ? [sw.otherLabel + " " + shareText(sw.other, now), reset(sw.other)].filter(Boolean).join(" · ") : "";
    tiles.push(html`<div key="account" class=${"sc-box sc-tile" + (old ? " sc-stale" : "")}>
      <span class="sc-legend">${u.account}</span>
      <span class="sc-tile-fig">${shareText(sw.w, now)}</span>
      <span class="sc-tile-sub">${first}</span>
      ${second ? html`<span class="sc-tile-sub">${second}</span>` : null}
    </div>`);
  }
  return tiles.length > 0 ? html`<div class="sc-tiles">${tiles}</div>` : null;
}

// AGENT_ID is what an agent id may hold for the app to link to its screen
// (parseRoute's agent route).
export const AGENT_ID = /^[A-Za-z0-9_-]+$/;

// Agents are the session's subagents by name, each running or idle, with its
// description under it. A row that carries an id is a link to the agent's
// screen.
function Agents({ agents, sessionId }) {
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Agents ${agents.length}</h2>
      ${agents.map((a, i) => {
        const linked = Boolean(a.id) && AGENT_ID.test(a.id);
        const inner = html`
          <div class="sc-row-top">
            <span class="sc-glyph" aria-hidden="true">${a.idle ? "○" : "◐"}</span>
            <span class="sc-row-title">${a.name}</span>
            <span class="sc-age">${a.idle ? "idle" : "running"}</span>
            ${linked ? html`<${ChevronIcon} />` : null}
          </div>
          ${a.description ? html`<div class="sc-agent-desc">${a.description}</div>` : null}
        `;
        return linked
          ? html`<a key=${i} class="sc-agent sc-agent-link" href=${"#s/" + sessionId + "/agent/" + a.id}>${inner}</a>`
          : html`<div key=${i} class="sc-agent">${inner}</div>`;
      })}
    </section>
  `;
}

export function Info({ snap, stale, route, go, act, toast, pending }) {
  const id = route.id;
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const { busy, run } = useRun(act, toast, id);

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it.
  useEffect(() => {
    setSheet("");
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = sessionOf(snap, id);

  if (!s) {
    return html`
      <section class="sc-screen">
        <p class="sc-dim sc-empty">${snap && pending !== id ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const candidates = mergeCandidates(snap, s);
  const now = Date.now();

  async function kill() {
    const r = await run("kill");
    setSheet("");
    if (r.ok && r.status !== 202) go("#");
  }

  async function pushBranch() {
    await run("push");
    setSheet("");
  }

  async function merge() {
    await run("merge", { source: Number(source) });
    setSheet("");
  }

  function openMerge() {
    if (candidates.length === 0) {
      toast("No other session in this workspace to merge in.", true);
      return;
    }
    setSource(String(candidates[0].id));
    setSheet("merge");
  }

  return html`
    <section class="sc-screen sc-session">
      <${SubLine} s=${s} snap=${snap} now=${now} />
      <${Tiles} s=${s} snap=${snap} now=${now} diff=${can.diff} />
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} sessionId=${s.id} />` : null}

      <section class="sc-actions">
        ${can.pause ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => run("pause")}>pause</button>` : null}
        ${can.resume ? html`<button class="sc-btn sc-primary" type="button" disabled=${off} onClick=${() => run("resume")}>resume</button>` : null}
        ${can.push ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet("push")}>push</button>` : null}
        ${can.merge ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${openMerge}>merge</button>` : null}
        ${can.kill ? html`<button class="sc-btn sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("kill")}>kill</button>` : null}
      </section>

      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}

      ${sheet === "kill"
        ? html`<${ConfirmSheet}
            title="Kill this session?"
            confirm="Kill"
            danger=${true}
            body=${html`<div>
              <p>${s.tmux ? html`It ends tmux session <code>${s.tmux}</code>.` : "It ends the session's tmux session."}</p>
              <p>${s.worktree ? html`It force-removes worktree <code>${s.worktree}</code>` : "It force-removes the session's worktree"}, discarding its uncommitted changes. Push first to keep them.</p>
              ${s.status === "Paused"
                ? html`<p>If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.</p>`
                : null}
              <p>It deletes branch <code>${s.branch}</code> unless the session checked out an existing branch.</p>
            </div>`}
            onConfirm=${kill}
            onCancel=${() => setSheet("")}
          />`
        : null}
      ${sheet === "push"
        ? html`<${ConfirmSheet}
            title="Push this branch?"
            confirm="Commit and push"
            body=${html`<p>This commits the session's uncommitted changes first, then pushes <code>${s.branch}</code>.</p>`}
            onConfirm=${pushBranch}
            onCancel=${() => setSheet("")}
          />`
        : null}
      ${sheet === "merge"
        ? html`<${ConfirmSheet}
            title="Merge a branch in"
            confirm="Merge"
            body=${html`<p>Merge which session's branch into <code>${s.branch}</code>?</p>
              <select value=${source} onChange=${(e) => setSource(e.target.value)} aria-label="Session to merge in">
                ${candidates.map((o) => html`<option key=${o.id} value=${String(o.id)}>${o.title} (${o.branch})</option>`)}
              </select>`}
            onConfirm=${merge}
            onCancel=${() => setSheet("")}
          />`
        : null}
    </section>
  `;
}

// SessionHead is the context row, the tabs and, on the conversation, the fact
// strip of a session's views (conversation, changes, info, mockup): back to
// the fleet, its title, and the way to Claude unless the needs-you box (which
// has its own button) is on screen, as it is at a waiting Claude session's
// conversation. A session that does not run Claude has no conversation:
// its #s/<id> is the info tab, and the tab row says so.
export function SessionHead({ snap, route, pending }) {
  const s = sessionOf(snap, route.id);
  if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
  const talks = route.name === "turns" && s.claude;
  const current = route.name === "turns" && !s.claude ? "info" : route.name;
  const boxed = talks && s.group === "needs_you";
  return html`
    <${ContextRow} back=${FLEET_BACK} title=${s.title}>
      ${s.remote_url && !boxed
        ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
        : null}
    <//>
    <${SessionTabs} s=${s} current=${current} conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
    ${talks ? html`<${FactStrip} s=${s} snap=${snap} now=${Date.now()} />` : null}
  `;
}
