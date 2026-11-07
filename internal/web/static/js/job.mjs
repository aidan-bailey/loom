// The job screen (#s/<id>/job/<n>): one of a session's work-queue jobs, its
// facts from the session's jobs (the snapshot's) and its output from
// GET …/jobs/<n>/log, the tail first and then what follows (after=end) every
// POLL while the job has yet to end and the page is visible, with one read
// more once it ends. The output is drawn as text, never as HTML. What a
// screen read is kept per job in a module-level map, so a trip to another
// tab comes back to the same text.
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { ago, sessionOf } from "./fleet.mjs";
import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";
import { actionsFor, elapsed, jobEnd, jobGlyph } from "./session.mjs";

// POLL is how often a live job's log is read again.
const POLL = 2000;
// KEEP is how many jobs' text the map holds, and MAX_TEXT how much of one:
// past it the oldest lines go, and the text says earlier output was cut.
const KEEP = 5;
const MAX_TEXT = 512 * 1024;
// FOLLOW is how close to the bottom the page must be for new output to
// keep it there.
const FOLLOW = 40;

const held = new Map();

function keep(key, log) {
  held.delete(key);
  held.set(key, log);
  while (held.size > KEEP) held.delete(held.keys().next().value);
}

// jobOf is job n of session s, null when the session's list lacks it.
export function jobOf(s, n) {
  return s && s.jobs ? s.jobs.find((j) => j.id === n) || null : null;
}

// live says a job has yet to end.
function live(j) {
  return Boolean(j) && (j.state === "running" || j.state === "queued" || j.state === "held");
}

// appended is log with a read d added: d replaces it when it is a reset or
// there is nothing yet, and the text is trimmed to MAX_TEXT at a line.
function appended(log, d) {
  let next = !log || d.reset ? { text: d.text, end: d.end, cut: d.cut } : { text: log.text + d.text, end: d.end, cut: log.cut };
  if (next.text.length > MAX_TEXT) {
    const from = next.text.length - MAX_TEXT;
    const nl = next.text.indexOf("\n", from);
    next = { ...next, text: next.text.slice(nl >= 0 ? nl + 1 : from), cut: true };
  }
  return next;
}

function atBottom() {
  const doc = document.documentElement;
  return window.innerHeight + window.scrollY >= doc.scrollHeight - FOLLOW;
}

// stateText is the job's state line: how long it has run, why it waits, or
// how it ended, how long it ran and when.
function stateText(j, now) {
  if (j.state === "running") return ["running", elapsed(j.started, now)].filter(Boolean).join(" · ");
  if (live(j)) return [j.state, j.waiting].filter(Boolean).join(" · ");
  const ran = j.started && j.ended ? "ran " + elapsed(j.started, j.ended) : "";
  const when = j.ended ? ago(j.ended, now) : "";
  return [jobEnd(j), ran, when && when !== "now" ? when + " ago" : when].filter(Boolean).join(" · ");
}

// JobHead is the job screen's context row, back to the info tab, titled by
// the job's description or command, and the session's tabs (info current).
export function JobHead({ snap, route, pending }) {
  const s = sessionOf(snap, route.id);
  if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
  const j = jobOf(s, route.job);
  const title = (j && (j.description || j.command)) || "job " + route.job;
  return html`
    <${ContextRow} back=${{ href: "#s/" + s.id + "/info", label: "info" }} title=${title} />
    <${SessionTabs} s=${s} current="info" conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
  `;
}

export function Job({ snap, route }) {
  const key = route.id + "/" + route.job;
  const s = sessionOf(snap, route.id);
  const j = jobOf(s, route.job);
  const [log, setLog] = useState(() => held.get(key) || null);
  const [gone, setGone] = useState("");
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  const logRef = useRef(log);
  const keyRef = useRef(key);
  const busy = useRef(false);
  const follow = useRef(true);
  const wasLive = useRef(live(j));
  logRef.current = log;
  keyRef.current = key;

  async function read() {
    if (busy.current) return;
    busy.current = true;
    const at = key;
    const cur = logRef.current;
    const q = cur ? "?after=" + cur.end : "";
    const r = await call("GET", "/api/v1/sessions/" + route.id + "/jobs/" + route.job + "/log" + q);
    busy.current = false;
    if (keyRef.current !== at) return;
    if (r.status === 200 && r.data) {
      const next = appended(logRef.current, r.data);
      keep(at, next);
      logRef.current = next;
      setLog(next);
      setGone("");
      return;
    }
    if (r.status === 409) setGone((r.data && r.data.error) || "This job is no longer in the queue.");
    // A timeout (504) or no network (0) waits for the next read.
  }

  // A job opened: what the map holds, then a read of what is new.
  useEffect(() => {
    const h = held.get(key) || null;
    logRef.current = h;
    setLog(h);
    setGone("");
    follow.current = true;
    read();
  }, [key]);

  useEffect(() => {
    const onVis = () => setVisible(document.visibilityState === "visible");
    const onScroll = () => {
      follow.current = atBottom();
    };
    document.addEventListener("visibilitychange", onVis);
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      document.removeEventListener("visibilitychange", onVis);
      window.removeEventListener("scroll", onScroll);
    };
  }, []);

  const running = live(j);
  useEffect(() => {
    if (!running || !visible || gone) return undefined;
    const t = setInterval(read, POLL);
    return () => clearInterval(t);
  }, [running, visible, gone, key]);

  // Once more when the job ends: the read before may have missed its last
  // output.
  useEffect(() => {
    if (wasLive.current && !running) read();
    wasLive.current = running;
  }, [running]);

  useEffect(() => {
    if (follow.current) window.scrollTo(0, document.documentElement.scrollHeight);
  }, [log]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const now = Date.now();
  let body;
  if (gone) {
    body = html`<p class="sc-dim sc-empty">${gone}</p>`;
  } else if (!log) {
    body = html`<p class="sc-dim sc-empty">Loading…</p>`;
  } else if (log.text === "") {
    body = html`<p class="sc-dim sc-small">${running ? "No output yet." : "No output."}</p>`;
  } else {
    body = html`
      ${log.cut ? html`<p class="sc-dim sc-small">Earlier output not shown.</p>` : null}
      <pre class="sc-log">${log.text}</pre>
    `;
  }

  return html`
    <section class="sc-screen sc-job-screen">
      ${j
        ? html`<div class="sc-job-facts">
            <div><span class=${"sc-glyph sc-job-" + j.state} aria-hidden="true">${jobGlyph(j)}</span> job ${j.id} · ${stateText(j, now)}</div>
            ${j.description ? html`<div class="sc-mono">${j.command}</div>` : null}
            ${j.agent ? html`<div class="sc-dim sc-small">submitted by subagent ${j.agent}</div>` : null}
          </div>`
        : null}
      ${body}
    </section>
  `;
}
