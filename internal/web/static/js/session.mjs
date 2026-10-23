// The session screen: why it waits, its tiles, the last message, its agents,
// the actions, and the reply box with the quick replies.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { ConfirmSheet, utf8Len } from "./sheets.mjs";
import { allSessions, ago, markSeen } from "./fleet.mjs";
import { GLYPHS, glyphOf, OutIcon } from "./skin.mjs";
import { usageFor, shareText, isStale } from "./usage.mjs";
import { Markdown } from "./markdown.mjs";

export const MAX_REPLY = 64 * 1024;

// actionsFor says which buttons to show. It follows status only; the
// daemon's admit stays the authority and a refusal comes back as a toast.
// It shows only what admit accepts: every op but resume is refused while the
// session is Loading or Deleting, and resume is refused unless it is Paused
// (a Recoverable or Not started session has nothing to resume).
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

// Tiles are the session's facts as boxes: its changes, its pull request (a
// link to it on GitHub when the daemon has its URL) and its account's usage.
// A tile whose data is missing is left out, and the row reflows.
function Tiles({ s, snap, now }) {
  const tiles = [];
  const d = s.diff || {};
  if (d.added || d.removed || s.parity_known) {
    const parity = parityText(s);
    tiles.push(html`<div key="changes" class="sc-box sc-tile">
      <span class="sc-legend">Changes</span>
      <span class="sc-tile-fig"><span class="sc-add">+${d.added || 0}</span> <span class="sc-del">−${d.removed || 0}</span></span>
      ${parity ? html`<span class="sc-tile-sub">${parity}</span>` : null}
    </div>`);
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
  const w = u && (u.five_hour || u.seven_day);
  if (w) {
    const old = isStale(u.at, snap.usage_stale_after_s, now);
    tiles.push(html`<div key="account" class=${"sc-box sc-tile" + (old ? " sc-stale" : "")}>
      <span class="sc-legend">Account</span>
      <span class="sc-tile-fig">${shareText(w, now)}</span>
      <span class="sc-tile-sub">${u.account} ${u.five_hour ? "5h" : "7d"}</span>
    </div>`);
  }
  return tiles.length > 0 ? html`<div class="sc-tiles">${tiles}</div>` : null;
}

// Message is the last message: its tree once fetched for this very text, the
// plain text before that and whenever the fetch fails.
function Message({ s, tree }) {
  const ok = tree && tree.text === s.last_message;
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Last message</h2>
      ${ok ? html`<${Markdown} blocks=${tree.blocks} />` : html`<p class="sc-pre">${s.last_message}</p>`}
      ${ok && tree.truncated
        ? html`<p class="sc-dim sc-small">… continues in ${s.remote_url ? html`<a href=${s.remote_url} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
        : null}
    </section>
  `;
}

// Agents are the session's subagents by name, each running or idle, with its
// description under it.
function Agents({ agents }) {
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Agents ${agents.length}</h2>
      ${agents.map(
        (a, i) => html`<div key=${i} class="sc-agent">
          <div class="sc-row-top">
            <span class="sc-glyph" aria-hidden="true">${a.idle ? "○" : "◐"}</span>
            <span class="sc-row-title">${a.name}</span>
            <span class="sc-age">${a.idle ? "idle" : "running"}</span>
          </div>
          ${a.description ? html`<div class="sc-agent-desc">${a.description}</div>` : null}
        </div>`,
      )}
    </section>
  `;
}

export function Session({ snap, stale, route, go, act, toast, pending }) {
  const id = route.id;
  const [text, setText] = useState("");
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);
  const [tree, setTree] = useState(null);

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it.
  useEffect(() => {
    setText("");
    setSheet("");
    setTree(null);
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const msg = s ? s.last_message : "";

  // The tree is fetched for the message the stream holds, and again when the
  // stream brings another. The plain text shows until it arrives, and stays
  // when the fetch fails or the app is offline.
  useEffect(() => {
    if (id === undefined || !msg || stale || (tree && tree.text === msg)) return undefined;
    let live = true;
    call("GET", "/api/v1/sessions/" + id + "/message").then(({ status, data }) => {
      if (live && status === 200 && data && data.text === msg) setTree(data);
    });
    return () => {
      live = false;
    };
  }, [id, msg, stale]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>
        <p class="sc-dim sc-empty">${snap && pending !== id ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const base = "/api/v1/sessions/" + s.id;
  const candidates = mergeCandidates(snap, s);
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;
  const replies = snap.quick_replies || [];
  const waiting = s.group === "needs_you";
  const ws = (snap.workspaces || []).find((w) => w.id === s.workspace);
  const now = Date.now();

  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", base + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }

  async function send() {
    const r = await run("send", { text: reply });
    if (r.ok && r.status !== 202) setText("");
  }

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
      <nav class="sc-topbar" aria-label="Session">
        <a class="sc-back" href="#">‹ fleet</a>
        <span class="sc-fill"></span>
        ${s.remote_url && !waiting
          ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
          : null}
      </nav>
      <h1 class="sc-h1 sc-wrap">${s.title}</h1>
      <div class="sc-subline">
        <span><span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span> ${s.label.toLowerCase()}${s.since ? " · " + ago(s.since, now) : ""}</span>
        ${ws ? html`<span class="sc-ws">${ws.label || ws.name}</span>` : null}
        ${s.branch ? html`<span>${s.branch}</span>` : null}
      </div>
      ${waiting
        ? html`<div class="sc-box sc-attn-box">
            <span class="sc-legend">${GLYPHS.needs_you} Needs you</span>
            <div class="sc-attn">${(s.wait && s.wait.reason) || "Needs you"}</div>
            ${s.wait && s.wait.detail ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
            ${s.remote_url
              ? html`<a class="sc-btn sc-primary sc-block" href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
              : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
          </div>`
        : null}
      <${Tiles} s=${s} snap=${snap} now=${now} />
      ${s.last_message ? html`<${Message} s=${s} tree=${tree} />` : null}
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} />` : null}

      <section class="sc-actions">
        ${can.pause ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => run("pause")}>pause</button>` : null}
        ${can.resume ? html`<button class="sc-btn sc-primary" type="button" disabled=${off} onClick=${() => run("resume")}>resume</button>` : null}
        ${can.push ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet("push")}>push</button>` : null}
        ${can.merge ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${openMerge}>merge</button>` : null}
        ${can.kill ? html`<button class="sc-btn sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("kill")}>kill</button>` : null}
      </section>

      ${can.send
        ? html`<form class="sc-reply" onSubmit=${(e) => { e.preventDefault(); if (!off && reply && !tooLong) send(); }}>
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
          </form>`
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
