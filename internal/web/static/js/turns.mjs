// The conversation screen: a session's turns from Claude's transcript
// (GET /api/v1/sessions/{id}/turns), oldest first and opening at the newest;
// earlier pages on request, and the newest turn refreshed while the session
// works. Claude's text is drawn from its tree, everything else as text.
import { html, useState, useEffect, useRef, useLayoutEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { allSessions, ago } from "./fleet.mjs";
import { Markdown } from "./markdown.mjs";
import { SubLine, actionsFor } from "./session.mjs";
import { ReplyBox } from "./reply.mjs";

// POLL is how often the screen refreshes the newest turn while the session
// is Running; PROMPT_LINES how many lines of a prompt show before "more".
const POLL = 5000;
const PROMPT_LINES = 12;

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
// is Running) or "no result".
function Step({ st, live, now }) {
  const running = live && st.state === "pending";
  let glyph = "";
  let cls = "sc-dim";
  if (running) {
    glyph = "◐";
    cls = "sc-accent";
  } else if (st.state === "failed") {
    glyph = "×";
    cls = "sc-err";
  } else if (st.state === "interrupted") {
    glyph = "×";
  }
  const end = running ? "running " + ago(st.at, now) : st.state === "pending" ? "no result" : "";
  return html`
    <span class=${"sc-step-glyph " + cls} aria-hidden="true">${glyph}</span>
    <span class="sc-step-tool">${st.tool}</span>
    <span class="sc-step-sum">${st.summary}</span>
    <span class=${"sc-step-end " + (running ? "sc-accent" : "sc-dim")}>${end}</span>
  `;
}

function Steps({ it, live, open, now }) {
  const total = it.total || 0;
  const failed = it.failed || 0;
  const steps = it.steps || [];
  const hidden = total - steps.length;
  return html`<details class="sc-steps" open=${open}>
    <summary>${total} ${total === 1 ? "step" : "steps"}${failed ? html`<span class="sc-err">${" · " + failed + " failed"}</span>` : null}</summary>
    ${hidden > 0 ? html`<p class="sc-dim sc-small">${hidden} earlier steps not shown</p>` : null}
    <div class="sc-step-grid">${steps.map((st, i) => html`<${Step} key=${i} st=${st} live=${live} now=${now} />`)}</div>
  </details>`;
}

function Item({ it, live, open, now, remote }) {
  switch (it.kind) {
    case "text":
      return html`<div class="sc-said-text">
        <${Markdown} blocks=${it.blocks} />
        ${it.truncated
          ? html`<p class="sc-dim sc-small">… continues in ${remote ? html`<a href=${remote} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
          : null}
      </div>`;
    case "steps":
      return html`<${Steps} it=${it} live=${live} open=${open} now=${now} />`;
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
// fold starts open, and its pending steps are running.
function Turn({ t, live, now, remote }) {
  const items = t.items || [];
  let lastFold = -1;
  items.forEach((it, i) => {
    if (it.kind === "steps") lastFold = i;
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
          ${items.map((it, i) => html`<${Item} key=${i} it=${it} live=${live} open=${live && i === lastFold} now=${now} remote=${remote} />`)}
        </div>`
      : null}
  </article>`;
}

export function Turns({ snap, stale, route, act, toast }) {
  const id = route.id;
  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const [held, setHeld] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const heldRef = useRef(null);
  const loading = useRef(false);
  const again = useRef(false);
  const alive = useRef(true);
  // anchor says where the scroll goes after the next page lands: "bottom",
  // or {fromBottom} to keep the position an earlier page pushed down.
  const anchor = useRef(null);
  heldRef.current = held;
  const working = Boolean(s && s.status === "Running");

  // load fetches a page: "newest" (on open), "earlier" (the page before the
  // held turns) or "refresh" (from the newest turn held). One is in flight
  // at a time; a refresh asked for meanwhile runs once it lands.
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
      setHeld(mergeTurns(cur, r.data, ask));
    }
    if (again.current) {
      again.current = false;
      load("refresh");
    }
  }

  useLayoutEffect(() => {
    const a = anchor.current;
    anchor.current = null;
    if (!a) return;
    const doc = document.documentElement;
    window.scrollTo(0, a === "bottom" ? doc.scrollHeight : doc.scrollHeight - a.fromBottom);
  }, [held]);

  useEffect(() => {
    alive.current = true;
    load("newest");
    return () => {
      alive.current = false;
    };
  }, [id]);

  // The stream changing the session's status or last message refreshes the
  // newest turn; so does the poll while the session is Running.
  const sig = s ? s.status + "\n" + (s.last_message || "") : "";
  useEffect(() => {
    if (heldRef.current) load("refresh");
  }, [sig]);
  useEffect(() => {
    if (!working) return undefined;
    const timer = setInterval(() => load("refresh"), POLL);
    return () => clearInterval(timer);
  }, [working, id]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const now = Date.now();
  const turns = held ? held.turns : [];

  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", "/api/v1/sessions/" + s.id + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }

  return html`
    <section class="sc-screen sc-turns">
      <nav class="sc-topbar" aria-label="Conversation"><a class="sc-back" href=${"#s/" + s.id}>‹ ${s.title}</a></nav>
      <h1 class="sc-h1">Conversation</h1>
      <${SubLine} s=${s} snap=${snap} now=${now} />
      ${held && held.skipped ? html`<p class="sc-dim sc-small">Some records could not be read.</p>` : null}
      ${held && held.earlier ? html`<button class="sc-earlier" type="button" onClick=${() => load("earlier")}>↑ earlier turns</button>` : null}
      ${!held
        ? html`<div class="sc-empty">
            <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
            ${err ? html`<button class="sc-btn" type="button" onClick=${() => load("newest")}>retry</button>` : null}
          </div>`
        : null}
      ${held && turns.length === 0 ? html`<p class="sc-dim sc-empty">No turns yet.</p>` : null}
      <div class="sc-turn-list">
        ${turns.map((t, i) => html`<${Turn} key=${t.id} t=${t} live=${working && i === turns.length - 1} now=${now} remote=${s.remote_url} />`)}
      </div>
      ${held && err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load("refresh")}>retry</button></p>`
        : null}
      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}
    </section>
  `;
}
