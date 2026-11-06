// The conversation screen: a session's turns from Claude's transcript
// (GET /api/v1/sessions/{id}/turns), oldest first and opening at the newest;
// earlier pages on request, and the newest turn refreshed while the session
// works. Claude's text is drawn from its tree, everything else as text. The
// subagent screen (agent.mjs) reads and draws its turns with useTurnPages
// and TurnList too.
import { html, useState, useEffect, useRef, useLayoutEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { sessionOf, ago, markSeen } from "./fleet.mjs";
import { Markdown } from "./markdown.mjs";
import { Info, actionsFor, AGENT_ID } from "./session.mjs";
import { NeedsYou } from "./needs.mjs";
import { ReplyBox, useRun } from "./reply.mjs";

// POLL is how often the screen refreshes the newest turn while the session
// is Running; PROMPT_LINES how many lines of a prompt show before "more".
const POLL = 5000;
const PROMPT_LINES = 12;

// kept holds each session's conversation as it was left, by session id: the
// turns held and the scroll position. Leaving the screen (a subagent's, the
// info tab) and coming back restores both before the first paint and then
// refreshes, instead of loading at the bottom. Like the reply drafts it
// lasts as long as the page.
const kept = new Map();

// mergeTurns folds a page into the turns held: the first page and a reset
// replace them, an earlier page goes before them, and a refresh replaces
// every held turn from its first turn on. skipped stays set once a page
// reported a record it could not read.
export function mergeTurns(held, page, kind) {
  const skipped = Boolean((held && held.skipped) || page.skipped > 0);
  const turns = page.turns || [];
  if (!held || page.reset || kind === "newest") {
    return { conversation: page.conversation, turns, earlier: page.earlier || 0, skipped };
  }
  if (kind === "earlier") {
    const first = held.turns.length > 0 ? held.turns[0].id : Infinity;
    return { ...held, turns: turns.filter((t) => t.id < first).concat(held.turns), earlier: page.earlier || 0, skipped };
  }
  if (turns.length === 0) return { ...held, skipped };
  const from = turns[0].id;
  return { ...held, turns: held.turns.filter((t) => t.id < from).concat(turns), skipped };
}

// turnLabel is the label in a turn's box: whose the prompt is.
export function turnLabel(t) {
  switch (t.from) {
    case "you":
      return "YOU";
    case "plugin":
      return (t.name || "plugin").toUpperCase();
    case "peer":
      return t.name ? "FROM " + t.name.toUpperCase() : "FROM ANOTHER SESSION";
    case "task":
      return "BACKGROUND TASK";
    default:
      return String(t.from || "").toUpperCase();
  }
}

// clock is a time as the phone's 24-hour hh:mm, "" for none.
export function clock(iso) {
  const t = Date.parse(iso || "");
  if (Number.isNaN(t)) return "";
  return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

function Prompt({ t }) {
  const [open, setOpen] = useState(false);
  const lines = (t.text || "").split("\n");
  const folded = lines.length > PROMPT_LINES && !open;
  const at = clock(t.at);
  return html`<div class="sc-box sc-prompt">
    <span class="sc-legend">${turnLabel(t)}${at ? " · " + at : ""}</span>
    <div class=${"sc-pre" + (t.command ? " sc-cmd" : "")}>${folded ? lines.slice(0, PROMPT_LINES).join("\n") : t.text}${t.cut && !folded ? "…" : ""}</div>
    ${folded ? html`<button class="sc-more" type="button" onClick=${() => setOpen(true)}>▸ ${lines.length - PROMPT_LINES} more lines</button>` : null}
  </div>`;
}

// Step is one row of a fold: its state's glyph, the tool, the summary, and
// for a pending step "running" (only in the newest turn while the session
// is Running) or "no result". A step that launched a subagent is a link to
// the subagent's screen, ending in "›", with ◐ while the agent has not
// finished.
function Step({ st, live, now, sessionId }) {
  const linked = Boolean(st.agent) && AGENT_ID.test(st.agent);
  const running = live && st.state === "pending";
  let glyph = "";
  let cls = "sc-dim";
  if (running || (linked && !st.finished)) {
    glyph = "◐";
    cls = "sc-accent";
  } else if (st.state === "failed") {
    glyph = "×";
    cls = "sc-err";
  } else if (st.state === "interrupted") {
    glyph = "×";
  }
  let end = running ? "running " + ago(st.at, now) : st.state === "pending" ? "no result" : "";
  if (linked) end = end ? end + " ›" : "›";
  const cells = html`
    <span class=${"sc-step-glyph " + cls} aria-hidden="true">${glyph}</span>
    <span class="sc-step-tool">${st.tool}</span>
    <span class="sc-step-sum">${st.summary}</span>
    <span class=${"sc-step-end " + (running ? "sc-accent" : "sc-dim")}>${end}</span>
  `;
  return linked ? html`<a class="sc-step-link" href=${"#s/" + sessionId + "/agent/" + st.agent}>${cells}</a>` : cells;
}

function Steps({ it, live, open, now, sessionId }) {
  const total = it.total || 0;
  const failed = it.failed || 0;
  const steps = it.steps || [];
  const hidden = total - steps.length;
  return html`<details class="sc-steps" open=${open}>
    <summary>${total} ${total === 1 ? "step" : "steps"}${failed ? html`<span class="sc-err">${" · " + failed + " failed"}</span>` : null}</summary>
    ${hidden > 0 ? html`<p class="sc-dim sc-small">${hidden} earlier steps not shown</p>` : null}
    <div class="sc-step-grid">${steps.map((st, i) => html`<${Step} key=${i} st=${st} live=${live} now=${now} sessionId=${sessionId} />`)}</div>
  </details>`;
}

// Thinking is a run of Claude's thinking, folded: its first line in the
// summary, the whole text (plain, never markdown) in the body.
function Thinking({ it, open }) {
  const first = (it.text || "").split("\n").map((l) => l.trim()).find((l) => l !== "") || "";
  return html`<details class="sc-think" open=${open}>
    <summary>▸ thinking: ${first}</summary>
    <p class="sc-think-body">${it.text}${it.truncated ? "…" : ""}</p>
  </details>`;
}

function Item({ it, live, open, now, remote, sessionId }) {
  switch (it.kind) {
    case "text":
      return html`<div class="sc-said-text">
        <${Markdown} blocks=${it.blocks} />
        ${it.truncated
          ? html`<p class="sc-dim sc-small">… continues in ${remote ? html`<a href=${remote} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
          : null}
      </div>`;
    case "steps":
      return html`<${Steps} it=${it} live=${live} open=${open} now=${now} sessionId=${sessionId} />`;
    case "thinking":
      return html`<${Thinking} it=${it} open=${open} />`;
    case "compacted":
      return html`<div class="sc-compacted"><span>COMPACTED${it.at ? " " + clock(it.at) : ""}</span></div>`;
    case "note":
      return html`<p class=${"sc-note " + (it.error ? "sc-err" : "sc-dim")}>${it.error ? it.text : "× " + it.text}</p>`;
    default:
      return null;
  }
}

// Turn is one turn: its prompt's box, then Claude's side. live is set on the
// newest turn while the session is Running: its label says so, its last
// fold and its last thinking item start open, and its pending steps are
// running.
function Turn({ t, live, now, remote, sessionId }) {
  const items = t.items || [];
  let lastFold = -1;
  let lastThink = -1;
  items.forEach((it, i) => {
    if (it.kind === "steps") lastFold = i;
    if (it.kind === "thinking") lastThink = i;
  });
  const last = items.length > 0 ? items[items.length - 1] : null;
  // A turn whose only items are compaction rules has nothing of Claude's to
  // label, as after a /compact.
  const said = items.some((it) => it.kind !== "compacted");
  return html`<article class="sc-turn">
    ${t.partial ? html`<p class="sc-dim sc-small">Its start is earlier.</p>` : html`<${Prompt} t=${t} />`}
    ${last
      ? html`<div class="sc-said">
          ${said
            ? html`<div class="sc-said-label">CLAUDE · ${live ? html`<span aria-hidden="true">◐</span> WORKING` : clock(last.at)}</div>`
            : null}
          ${items.map((it, i) => html`<${Item} key=${i} it=${it} live=${live} open=${live && (i === lastFold || i === lastThink)} now=${now} remote=${remote} sessionId=${sessionId} />`)}
        </div>`
      : null}
  </article>`;
}

// TurnList draws turns oldest first. live is set while the newest turn's
// side is still being written (the session is Running); remote is the
// "Open in Claude" address a cut text item points to; sessionId is the
// session an Agent step's link belongs to.
export function TurnList({ turns, live, now, remote, sessionId }) {
  return html`<div class="sc-turn-list">
    ${turns.map((t, i) => html`<${Turn} key=${t.id} t=${t} live=${live && i === turns.length - 1} now=${now} remote=${remote} sessionId=${sessionId} />`)}
  </div>`;
}

// useTurnPages reads a session's turns, or a subagent's when agent is set:
// the newest page on open, an earlier page or a refresh of the newest turn on
// load("earlier") and load("refresh"), and a refresh every POLL ms while live
// is set. It returns the turns held ({conversation, turns, earlier, skipped,
// agent}; null until the first page lands, agent the page's subagent object
// or null) and the last error ("" for none). One request is in flight at a
// time; a refresh asked for meanwhile runs once it lands. The scroll is kept
// where it was, or moves to the bottom for a first page and for a refresh
// that lands while the reader is at the bottom.
export function useTurnPages({ id, agent, live }) {
  const [held, setHeld] = useState(() => (agent ? null : (kept.get(id) || {}).held || null));
  const [err, setErr] = useState("");
  const heldRef = useRef(null);
  const loading = useRef(false);
  const again = useRef(false);
  const alive = useRef(true);
  // anchor says where the scroll goes after the next page lands: "bottom",
  // or {fromBottom} to keep the position an earlier page pushed down.
  const anchor = useRef(null);
  heldRef.current = held;

  // load fetches a page: "newest" (on open), "earlier" (the page before the
  // held turns) or "refresh" (from the newest turn held).
  async function load(kind) {
    if (id === undefined) return;
    if (loading.current) {
      if (kind === "refresh") again.current = true;
      return;
    }
    const cur = heldRef.current;
    let ask = kind;
    if (ask === "refresh" && (!cur || cur.turns.length === 0)) ask = "newest";
    if (ask === "earlier" && (!cur || !cur.earlier)) return;
    const q = new URLSearchParams();
    if (agent) q.set("agent", agent);
    if (cur && cur.conversation) q.set("conversation", cur.conversation);
    if (ask === "earlier") q.set("before", String(cur.earlier));
    if (ask === "refresh") q.set("after", String(cur.turns[cur.turns.length - 1].id));
    const doc = document.documentElement;
    const atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 48;
    const fromBottom = doc.scrollHeight - window.scrollY;
    loading.current = true;
    let r;
    try {
      r = await call("GET", "/api/v1/sessions/" + id + "/turns" + (q.toString() ? "?" + q.toString() : ""));
    } finally {
      loading.current = false;
    }
    if (!alive.current) return;
    if (r.status !== 200 || !r.data) {
      setErr((r.data && r.data.error) || "Could not read the conversation.");
    } else {
      setErr("");
      const first = !cur || r.data.reset || ask === "newest";
      anchor.current = first ? "bottom" : ask === "earlier" ? { fromBottom } : atBottom ? "bottom" : null;
      setHeld({ ...mergeTurns(cur, r.data, ask), agent: r.data.agent || null });
    }
    if (again.current) {
      again.current = false;
      load("refresh");
    }
  }

  // A conversation that was kept is scrolled back to where it was left,
  // before the first paint; a subagent's page is not kept.
  useLayoutEffect(() => {
    const k = agent ? undefined : kept.get(id);
    if (k && k.scrollY !== undefined && heldRef.current) window.scrollTo(0, k.scrollY);
  }, [id, agent]);

  useEffect(() => {
    if (agent || !held) return;
    kept.set(id, { ...(kept.get(id) || {}), held });
  }, [held]);

  useEffect(() => {
    if (agent || id === undefined) return undefined;
    const onScroll = () => kept.set(id, { ...(kept.get(id) || {}), scrollY: window.scrollY });
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, [id, agent]);

  useLayoutEffect(() => {
    const a = anchor.current;
    anchor.current = null;
    if (!a) return;
    const doc = document.documentElement;
    window.scrollTo(0, a === "bottom" ? doc.scrollHeight : doc.scrollHeight - a.fromBottom);
  }, [held]);

  useEffect(() => {
    alive.current = true;
    // Turns kept from an earlier visit are refreshed from their newest turn;
    // anything else opens at the newest page.
    load(heldRef.current ? "refresh" : "newest");
    return () => {
      alive.current = false;
    };
  }, [id, agent]);

  useEffect(() => {
    if (!live) return undefined;
    const timer = setInterval(() => load("refresh"), POLL);
    return () => clearInterval(timer);
  }, [live, id, agent]);

  return { held, err, load };
}

// Conversation is a Claude session's turns, the needs-you box while it waits,
// and the reply box.
function Conversation({ snap, stale, route, act, toast }) {
  const id = route.id;
  const s = sessionOf(snap, id);
  const { busy, run } = useRun(act, toast, id);
  const working = Boolean(s && s.status === "Running");
  const { held, err, load } = useTurnPages({ id, live: working });

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it. The conversation is where a fleet
  // row and a push land.
  useEffect(() => {
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  // The stream changing the session's status or last message refreshes the
  // newest turn; so does the poll while the session is Running.
  const sig = s ? s.status + "\n" + (s.last_message || "") : "";
  const sigSeen = useRef(sig);
  useEffect(() => {
    if (sigSeen.current === sig) return;
    sigSeen.current = sig;
    if (held) load("refresh");
  }, [sig]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const now = Date.now();
  const turns = held ? held.turns : [];

  return html`
    <section class="sc-screen sc-turns">
      ${held && held.skipped ? html`<p class="sc-dim sc-small">Some records could not be read.</p>` : null}
      ${held && held.earlier ? html`<button class="sc-earlier" type="button" onClick=${() => load("earlier")}>↑ earlier turns</button>` : null}
      ${!held
        ? html`<div class="sc-empty">
            <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
            ${err ? html`<button class="sc-btn" type="button" onClick=${() => load("newest")}>retry</button>` : null}
          </div>`
        : null}
      ${held && turns.length === 0 ? html`<p class="sc-dim sc-empty">No turns yet.</p>` : null}
      <${TurnList} turns=${turns} live=${working} now=${now} remote=${s.remote_url} sessionId=${s.id} />
      ${held && err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load("refresh")}>retry</button></p>`
        : null}
      ${s.group === "needs_you" ? html`<${NeedsYou} s=${s} off=${off} run=${run} />` : null}
      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}
    </section>
  `;
}

// Turns is the screen at #s/<id>: the conversation of a Claude session, and
// for any other program (which has no transcript to read) the info tab.
export function Turns(props) {
  const s = sessionOf(props.snap, props.route.id);
  if (s && !s.claude) return html`<${Info} ...${props} />`;
  return html`<${Conversation} ...${props} />`;
}
