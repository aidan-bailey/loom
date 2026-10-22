// The session screen: why it waits, Open in Claude, its facts, the
// actions and the reply box.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { ConfirmSheet, utf8Len } from "./sheets.mjs";
import { allSessions, ago, diffText, prText, markSeen } from "./fleet.mjs";

export const MAX_REPLY = 64 * 1024;

// actionsFor says which buttons to show. It follows status only; the
// daemon's admit stays the authority and a refusal comes back as a toast.
export function actionsFor(s) {
  const busy = s.label === "Loading" || s.label === "Deleting";
  const paused = s.group === "paused";
  return {
    pause: !busy && !paused,
    resume: !busy && paused,
    push: !busy && !paused,
    merge: !busy && !paused,
    send: !busy && !paused,
    kill: s.label !== "Deleting",
  };
}

// mergeCandidates: the other sessions of the same workspace, whose branch
// can be merged into this one.
export function mergeCandidates(snap, s) {
  return allSessions(snap).filter((o) => o.workspace === s.workspace && o.id !== s.id);
}

export function Session({ snap, stale, route, go, act, toast }) {
  const id = route.id;
  const [text, setText] = useState("");
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);

  // The time this phone last opened the session feeds the fleet's dot; it
  // is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light the dot.
  useEffect(() => {
    setText("");
    setSheet("");
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  if (!s) {
    return html`
      <main class="sc-screen">
        <a class="sc-back" href="#">‹ Fleet</a>
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </main>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const base = "/api/v1/sessions/" + s.id;
  const candidates = mergeCandidates(snap, s);
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;

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

  const waiting = s.group === "needs_you";
  const facts = [
    ["Status", s.status],
    ["Since", ago(s.since)],
    ["Changes", diffText(s)],
    ["Branch", s.branch],
    ["Ahead / behind", s.parity_known ? s.ahead + " ahead, " + s.behind + " behind" : ""],
    ["Pull request", prText(s)],
    ["Account", s.account],
    ["Agents", s.agents.length > 0 ? s.agents.length + " running" : ""],
  ].filter(([, v]) => v);

  return html`
    <main class="sc-screen sc-session">
      <a class="sc-back" href="#">‹ Fleet</a>
      <h1 class="sc-h1 sc-wrap">${s.title}</h1>
      <div class=${"sc-lead sc-g-" + s.group}>
        <div class=${"sc-lead-label" + (waiting ? " sc-attn" : "")}>
          ${waiting ? (s.wait && s.wait.reason) || "Needs you" : s.label}
        </div>
        ${waiting && s.wait && s.wait.detail ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
        ${s.remote_url
          ? html`<a class="sc-btn sc-primary sc-block" href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
          : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
      </div>

      ${s.last_message
        ? html`<section class="sc-section">
            <h2 class="sc-group-title">Last message</h2>
            <p class="sc-pre">${s.last_message}</p>
          </section>`
        : null}

      <section class="sc-section">
        <h2 class="sc-group-title">Details</h2>
        <dl class="sc-facts">
          ${facts.map(([k, v]) => html`<div key=${k} class="sc-fact"><dt>${k}</dt><dd>${v}</dd></div>`)}
        </dl>
      </section>

      <section class="sc-actions">
        ${can.pause ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => run("pause")}>Pause</button>` : null}
        ${can.resume ? html`<button class="sc-btn sc-primary" type="button" disabled=${off} onClick=${() => run("resume")}>Resume</button>` : null}
        ${can.push ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet("push")}>Push</button>` : null}
        ${can.merge ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${openMerge}>Merge</button>` : null}
        ${can.kill ? html`<button class="sc-btn sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("kill")}>Kill</button>` : null}
      </section>

      ${can.send
        ? html`<form class="sc-reply" onSubmit=${(e) => { e.preventDefault(); if (!off && reply && !tooLong) send(); }}>
            <textarea
              rows="3"
              placeholder="Reply to Claude…"
              value=${text}
              onInput=${(e) => setText(e.target.value)}
            ></textarea>
            ${tooLong ? html`<div class="sc-small sc-err">Too long: a reply is at most 64 KiB.</div>` : null}
            <button class="sc-btn sc-primary" type="submit" disabled=${off || !reply || tooLong}>Send</button>
          </form>`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}

      ${sheet === "kill"
        ? html`<${ConfirmSheet}
            title="Kill this session?"
            confirm="Kill"
            danger=${true}
            body=${html`<p>This removes the session's worktree and its tmux session, and deletes its branch <code>${s.branch}</code>. Uncommitted changes are stashed first, and the notice names the stash (Settings, Recent notices).</p>`}
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
    </main>
  `;
}
