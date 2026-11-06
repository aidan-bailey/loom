// The subagent screen (#s/<id>/agent/<agentId>): one subagent's turns from
// its own transcript (GET …/turns?agent=), drawn by the conversation's
// renderer. Its brief is the first turn; there is no reply box and no
// needs-you box. The header (AgentHead) draws the agent's description and a
// strip by its state, which the screen only learns with its page, so the
// screen publishes the page's agent object to a store the header watches.
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";
import { sessionOf } from "./fleet.mjs";
import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";
import { AgentStrip } from "./strip.mjs";
import { actionsFor } from "./session.mjs";
import { TurnList, useTurnPages } from "./turns.mjs";

// GONE_AFTER is how long an agent's file may stay unwritten before the screen
// says its record is gone.
const GONE_AFTER = 30000;

// previousHash is the hash the app was at before the current one, from the
// hashchange events: when it is this session's conversation, the way back
// is history.back(), which returns to the conversation's scroll position.
let previousHash = "";
window.addEventListener("hashchange", (e) => {
  previousHash = new URL(e.oldURL).hash;
});

// infos are the agent objects the screens have published, by session and
// agent, and watchers the headers to redraw when one changes.
const infos = new Map();
const watchers = new Set();

function publish(key, agent) {
  if (agent) infos.set(key, agent);
  else infos.delete(key);
  watchers.forEach((f) => f());
}

function useInfo(key) {
  const [, tick] = useState(0);
  useEffect(() => {
    const f = () => tick((n) => n + 1);
    watchers.add(f);
    return () => watchers.delete(f);
  }, []);
  return infos.get(key) || null;
}

function keyOf(route) {
  return route.id + "/" + route.agent;
}

// AgentHead is the subagent screen's context row, tabs (conversation
// current) and strip. Its way back says "conversation": a click goes back in
// history when the previous entry is this session's conversation, and
// otherwise follows the link to it.
export function AgentHead({ snap, route, pending }) {
  const s = sessionOf(snap, route.id);
  const info = useInfo(keyOf(route));
  if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
  const back = {
    href: "#s/" + s.id,
    label: "conversation",
    onClick: (e) => {
      if (previousHash === "#s/" + s.id) {
        e.preventDefault();
        history.back();
      }
    },
  };
  const title = (info && (info.description || info.type)) || "subagent";
  return html`
    <${ContextRow} back=${back} title=${title} />
    <${SessionTabs} s=${s} current="turns" conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
    ${info ? html`<${AgentStrip} s=${s} agent=${info} now=${Date.now()} />` : null}
  `;
}

export function Agent({ snap, route }) {
  const id = route.id;
  const key = keyOf(route);
  const s = sessionOf(snap, id);
  const working = Boolean(s && s.status === "Running");
  const [live, setLive] = useState(false);
  const { held, err, load } = useTurnPages({ id, agent: route.agent, live });
  const info = held ? held.agent : null;
  const state = info ? info.state : "";
  const missingSince = useRef(0);
  const lastState = useRef("");

  // The header draws its strip from the page's agent object.
  useEffect(() => {
    publish(key, info);
  }, [held]);
  useEffect(() => () => publish(key, null), [key]);

  // missingSince is when a page first said the agent's file is not there.
  useEffect(() => {
    if (info && info.missing) {
      if (!missingSince.current) missingSince.current = Date.now();
    } else {
      missingSince.current = 0;
    }
  }, [held]);

  // The agent's file is missing: not yet written (meta lands first) until
  // the state says finished, or until it has been missing for GONE_AFTER.
  const missing = Boolean(info && info.missing);
  const waited = missing && missingSince.current ? Date.now() - missingSince.current : 0;
  const gone = missing && (state === "finished" || waited > GONE_AFTER);
  const starting = missing && !gone;
  // The agent is working: running, or its state unknown while the session is.
  const running = state === "running" || (state === "unknown" && working);
  const want = running || starting;
  useEffect(() => {
    setLive(want);
  }, [want]);

  // Once more when the state turns finished: the page that said so may have
  // been read before the agent's last records.
  useEffect(() => {
    if (lastState.current && lastState.current !== "finished" && state === "finished") load("refresh");
    lastState.current = state;
  }, [state]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  function retry() {
    missingSince.current = 0;
    load("newest");
  }

  let body;
  if (!held) {
    body = html`<div class="sc-empty">
      <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
      ${err ? html`<button class="sc-btn" type="button" onClick=${() => load("newest")}>retry</button>` : null}
    </div>`;
  } else if (gone) {
    body = html`<div class="sc-empty">
      <p class="sc-dim">This subagent's record is gone.</p>
      <button class="sc-btn" type="button" onClick=${retry}>retry</button>
      <p class="sc-small"><a href=${"#s/" + s.id}>‹ conversation</a></p>
    </div>`;
  } else if (starting) {
    body = html`<p class="sc-dim sc-empty">Starting…</p>`;
  } else {
    body = html`
      ${held.skipped ? html`<p class="sc-dim sc-small">Some records could not be read.</p>` : null}
      ${held.earlier ? html`<button class="sc-earlier" type="button" onClick=${() => load("earlier")}>↑ earlier turns</button>` : null}
      ${held.turns.length === 0 ? html`<p class="sc-dim sc-empty">No turns yet.</p>` : null}
      <${TurnList} turns=${held.turns} live=${running} now=${Date.now()} remote="" sessionId=${s.id} />
      ${err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load("refresh")}>retry</button></p>`
        : null}
    `;
  }

  return html`<section class="sc-screen sc-subagent">${body}</section>`;
}
