// The flow screens (spec docs/superpowers/specs/2026-11-09-flow-visualisation-design.md
// §5): a session's flow board (#s/<id>/flow?plan=) and one of its rows
// (#s/<id>/flow/<story>?plan=), both read from GET …/flow?plan= on open
// and again whenever the session's flows in the snapshot change (a step
// started or ended); elapsed times tick on the page's 30 s clock. Nothing
// here acts: the question box opens the manager's conversation. Everything
// is drawn as text or elements, never from an HTML string, and no element
// takes a style attribute (bar widths are classes).
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { sessionOf } from "./fleet.mjs";
import { elapsed } from "./session.mjs";

// KINDS is each row's step kinds in their order, LABEL a cell's word.
const KINDS = { story: ["dev", "gates", "review", "fix"], P: ["premise"], F: ["gates", "review", "fix"] };
const LABEL = { dev: "dev", gates: "gate", review: "rev", fix: "fix", premise: "prem" };

// The stages' words, in order.
const STAGES = ["design", "spec", "draft", "run", "record"];

// ROLE_CLASS is a role's colour in the cost bar; any other role is "other".
const ROLE_CLASS = {
  "junior-dev": "dev",
  "intermediate-dev": "dev",
  "senior-dev": "dev",
  "code-quality-reviewer": "review",
  "story-reviewer": "review",
  "spec-reviewer": "review",
  "premise-check": "other",
  manager: "manager",
};

// held keeps the last board read per session and plan, so a trip to
// another tab comes back to it at once.
const held = new Map();

// flowHref is the address of a flow screen: the board, or rest under it
// (a story, doc/<doc>), with the plan and any extra query.
export function flowHref(id, plan, rest, extra) {
  const q = new URLSearchParams();
  if (plan) q.set("plan", plan);
  for (const [k, v] of Object.entries(extra || {})) if (v !== undefined && v !== "") q.set(k, v);
  const qs = q.toString();
  return "#s/" + id + "/flow" + (rest ? "/" + rest : "") + (qs ? "?" + qs : "");
}

// usd is a figure as "$4.71", "$0.0312" below a cent.
export function usd(v) {
  if (typeof v !== "number") return "";
  return "$" + (v > 0 && v < 0.01 ? v.toFixed(4) : v.toFixed(2));
}

// flowChip is a flow's fleet chip (spec §5): "flow 2/5 · reviewing · $4.71"
// during a run, "asks" while parked, "lost" when its engine has ended;
// "flow · design", "flow · draft 3/4" before a run.
export function flowChip(f) {
  if (f.stage === "design" || f.stage === "spec") return "flow · design";
  if (f.status === "drafting") return "flow · draft " + f.drafted + "/" + f.total;
  if (f.status === "drafted" || f.status === "designed") return "flow · " + f.status;
  const parts = ["flow " + f.done + "/" + f.total];
  parts.push(f.asks ? "asks" : f.status === "lost" ? "lost" : f.status === "running" && f.phase ? f.phase : f.status);
  if (typeof f.spent === "number") parts.push(usd(f.spent));
  return parts.join(" · ");
}

// chipClass is a flow chip's classes: amber while it asks, red when lost.
export function chipClass(f) {
  return "sc-flow" + (f.asks ? " sc-flow-asks" : "") + (f.status === "lost" ? " sc-flow-lost" : "") + (f.unreadable ? " sc-flow-lost" : "");
}

// planOf is the route's plan, else the session's newest flow's.
function planOf(s, route) {
  if (route.plan) return route.plan;
  const flows = (s && s.flows) || [];
  return flows.length > 0 ? flows[0].plan : "";
}

// useClock re-renders every 30 s, for elapsed times.
function useClock() {
  const [, setTick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);
}

// useFlow reads the board of session id's flow plan, again whenever key
// (the session's flows in the snapshot) changes.
function useFlow(id, plan, key) {
  const cacheKey = id + " " + plan;
  const [state, setState] = useState(() => ({ data: held.get(cacheKey) || null, err: "" }));
  useEffect(() => {
    if (!plan) return undefined;
    let live = true;
    call("GET", "/api/v1/sessions/" + id + "/flow?plan=" + encodeURIComponent(plan)).then(({ status, data }) => {
      if (!live) return;
      if (status === 200 && data) {
        held.set(cacheKey, data);
        setState({ data, err: "" });
      } else {
        setState((s) => ({ data: s.data, err: (data && data.error) || "loom answered " + status }));
      }
    });
    return () => {
      live = false;
    };
  }, [id, plan, key]);
  return state;
}

// Strip is the five stages: done, now or to come, each opening its
// document when it has one.
function Strip({ flow, id, plan }) {
  const byName = Object.fromEntries((flow.stages || []).map((s) => [s.name, s]));
  const sum = flow.summary;
  return html`<nav class="sc-stages" aria-label="Stages">
    ${STAGES.map((name) => {
      const s = byName[name] || { name, state: "todo" };
      let word = name;
      if (name === "run" && sum.run) word = "run " + sum.done + "/" + sum.total;
      if (s.state === "done") word += " ✓";
      const cls = "sc-stage sc-stage-" + s.state;
      return s.doc
        ? html`<a key=${name} class=${cls} href=${flowHref(id, plan, "doc/" + s.doc)} title=${s.detail}>${word}</a>`
        : html`<span key=${name} class=${cls} title=${s.detail}>${word}</span>`;
    })}
  </nav>`;
}

// Question is a parked run's open question, and the way to the manager.
function Question({ q, id }) {
  return html`<div class="sc-box sc-attn-box sc-flow-q">
    <span class="sc-legend">parked · ${q.kind}${q.story ? " · story " + q.story : ""}</span>
    <p class="sc-flow-qtext">${q.text}</p>
    <a class="sc-btn sc-attn-btn" href=${"#s/" + id}>open manager ›</a>
  </div>`;
}

// Cells is a row's step cells in its kinds' order.
function Cells({ row, kinds }) {
  return html`<span class="sc-cells">
    ${kinds.map((k) => {
      const state = (row.cells && row.cells[k]) || "todo";
      const word = state === "skipped" ? "—" : LABEL[k] + (state === "asks" ? "?" : "");
      return html`<span key=${k} class=${"sc-cell sc-cell-" + state} aria-label=${LABEL[k] + ": " + state}>${word}</span>`;
    })}
  </span>`;
}

// rowKinds is a row's kinds: a story's, the premise row's or the final's.
function rowKinds(row) {
  return KINDS[row.id === "P" || row.id === "F" ? row.id : "story"];
}

// rowSpend is a row's spend in the cost box.
function rowSpend(costs, id) {
  return costs && costs.by_story ? costs.by_story[id] : undefined;
}

// StoryRow is a row of the stories box: its id, cells, and on the right
// its commits and spend, or its tier and review while it hasn't started.
function StoryRow({ row, costs, href }) {
  const spend = rowSpend(costs, row.id);
  const started = (row.steps || []).length > 0;
  let right = "";
  if (row.id === "P") right = "premises";
  else if (row.id === "F") right = "final";
  else if (started) right = (row.commits ? row.commits + "c" : "") + (typeof spend === "number" ? (row.commits ? " · " : "") + usd(spend) : "");
  else right = [row.tier, row.review].filter(Boolean).join(" · ");
  return html`<a class="sc-flow-row" href=${href}>
    <span class=${"sc-flow-id" + (row.id === "P" || row.id === "F" ? " sc-faint" : "")}>${row.id}</span>
    <${Cells} row=${row} kinds=${rowKinds(row)} />
    <span class=${"sc-flow-right" + (started ? " sc-dim" : " sc-faint")}>${right}</span>
  </a>`;
}

// width is a share as a bar width class, in steps of five.
function width(share) {
  const n = Math.max(0, Math.min(100, Math.round((share * 100) / 5) * 5));
  return "sc-w" + n;
}

// CostBox is the cost box: the run's spend, the whole flow, the
// projection, a bar by role, and this run's cost per story beside the
// repo's past runs.
export function CostBox({ costs, hasRun }) {
  if (!costs) return null;
  if (costs.error) {
    return html`<div class="sc-box sc-flow-cost">
      <span class="sc-legend">cost</span>
      <p class="sc-err-text">${costs.error}</p>
    </div>`;
  }
  const roles = Object.entries(costs.by_role || {}).filter(([, v]) => v > 0);
  const byClass = {};
  for (const [role, v] of roles) {
    const c = ROLE_CLASS[role] || "other";
    byClass[c] = (byClass[c] || 0) + v;
  }
  const total = roles.reduce((a, [, v]) => a + v, 0);
  const segs = ["dev", "review", "manager", "other"].filter((c) => byClass[c] > 0);
  const flowLine =
    costs.flow_parts > 0 ? "flow " + usd(costs.flow) + (costs.flow_parts < 3 ? " (" + costs.flow_parts + " of 3 parts)" : "") : "";
  return html`<div class="sc-box sc-flow-cost">
    <span class="sc-legend">cost · list prices</span>
    <div>
      ${hasRun ? html`run <b class="sc-title-text">${usd(costs.spent)}</b>` : html`<span class="sc-dim">no run yet</span>`}
      ${flowLine ? html`<span class="sc-dim"> · ${flowLine}</span>` : null}
      ${typeof costs.projection === "number"
        ? html`<span class="sc-dim"> · ~${usd(costs.projection)} projected</span>`
        : hasRun && costs.why
          ? html`<span class="sc-faint"> · ${costs.why}</span>`
          : null}
    </div>
    ${total > 0
      ? html`<div class="sc-bar" aria-hidden="true">
            ${segs.map((c) => html`<span key=${c} class=${"sc-seg sc-seg-" + c + " " + width(byClass[c] / total)}></span>`)}
          </div>
          <div class="sc-legend-row">
            ${segs.map((c) => html`<span key=${c} class=${"sc-key sc-key-" + c}>■ ${c} ${usd(byClass[c])}</span>`)}
          </div>`
      : null}
    ${(costs.unpriced || []).length > 0 ? html`<p class="sc-faint sc-small">not priced yet: ${costs.unpriced.length} running</p>` : null}
    <${Compare} costs=${costs} />
  </div>`;
}

// Compare is the repo's past runs' cost per story as bars, oldest first,
// with this run's in the accent colour.
function Compare({ costs }) {
  const past = costs.past || [];
  if (past.length === 0) return null;
  const all = typeof costs.this === "number" ? [...past, costs.this] : past;
  const top = Math.max(...all, 0.0001);
  const height = (v) => "sc-h" + Math.max(5, Math.round((v / top) * 20) * 5);
  return html`<div class="sc-compare">
    <div class="sc-spark" aria-hidden="true">
      ${past.map((v, i) => html`<span key=${i} class=${"sc-sbar " + height(v)}></span>`)}
      ${typeof costs.this === "number" ? html`<span class=${"sc-sbar sc-sbar-me " + height(costs.this)}></span>` : null}
    </div>
    <p class="sc-faint sc-small">
      per story: last ${past.length} runs${typeof costs.median === "number" ? ", median " + usd(costs.median) : ""}${typeof costs.this === "number" ? html` · <span class="sc-accent-text">this run ${usd(costs.this)}</span>` : ""}
    </p>
  </div>`;
}

// Picker is the plan picker of a session with two flows or more.
function Picker({ s, plan, go }) {
  const flows = s.flows || [];
  if (flows.length < 2) return null;
  return html`<label class="sc-flow-pick">
    <span class="sc-dim">flow</span>
    <select value=${plan} onChange=${(e) => go(flowHref(s.id, e.currentTarget.value))}>
      ${flows.map((f) => html`<option key=${f.plan} value=${f.plan}>${f.title || f.plan}</option>`)}
    </select>
  </label>`;
}

// Flow is the board (option A of the brainstorm): the strip, the question,
// the rows, the cost box, the plan's other runs.
export function Flow({ snap, route, go }) {
  const s = sessionOf(snap, route.id);
  useClock();
  const plan = planOf(s, route);
  const { data, err } = useFlow(route.id, plan, JSON.stringify((s && s.flows) || []));
  if (!s) return html`<section class="sc-screen"><p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p></section>`;
  if (!plan) return html`<section class="sc-screen"><p class="sc-dim sc-empty">No flow in this session's worktree.</p></section>`;
  if (!data) return html`<section class="sc-screen"><p class=${err ? "sc-err-text sc-empty" : "sc-dim sc-empty"}>${err || "Loading…"}</p></section>`;
  const sum = data.summary;
  const rows = [...(data.premise ? [data.premise] : []), ...data.stories, ...(data.final ? [data.final] : [])];
  return html`<section class="sc-screen sc-flow-board">
    <${Picker} s=${s} plan=${plan} go=${go} />
    <p class="sc-flow-title">${sum.title || plan}</p>
    ${err ? html`<p class="sc-err-text sc-small">${err}</p>` : null}
    ${sum.unreadable ? html`<p class="sc-err-text sc-small">unreadable: ${sum.unreadable}</p>` : null}
    <${Strip} flow=${data} id=${s.id} plan=${plan} />
    ${sum.status === "lost" ? html`<p class="sc-err-text sc-small">The run's engine has ended without it: loom flow run resume ${sum.run}</p>` : null}
    ${sum.phase && !data.question ? html`<p class="sc-dim sc-small">${sum.status} · ${sum.phase}${data.start ? " · " + elapsed(data.start, data.end || Date.now()) : ""}</p>` : null}
    ${data.question ? html`<${Question} q=${data.question} id=${s.id} />` : null}
    <div class="sc-box sc-flow-rows">
      <span class="sc-legend">stories</span>
      ${rows.map((row) => html`<${StoryRow} key=${row.id} row=${row} costs=${data.costs} href=${flowHref(s.id, plan, encodeURIComponent(row.id))} />`)}
    </div>
    <${CostBox} costs=${data.costs} hasRun=${Boolean(sum.run)} />
    ${(data.others || []).length > 0
      ? html`<div class="sc-box">
          <span class="sc-legend">other runs</span>
          ${data.others.map((o) => html`<div key=${o.id} class="sc-flow-row"><span>${o.id}</span><span class="sc-flow-right sc-dim">${o.unreadable ? "unreadable" : o.status}</span></div>`)}
        </div>`
      : null}
  </section>`;
}

// stepWord is a step's short name: its kind and attempt (dev1, gates2).
function stepWord(step) {
  return (step.kind === "gates" ? "gates" : step.kind) + step.k;
}

// stepOutcome is a step's dot: running, passed or failed.
function stepOutcome(step) {
  if (step.status === "running" || step.status === "starting") return "running";
  if (step.status === "DONE" || step.status === "DONE_WITH_CONCERNS" || step.status === "passed") return "passed";
  if (!step.status) return "todo";
  return "failed";
}

// specLinks is a story's Reading line as links into the spec: each "§N"
// it names, or the line as text when it names none.
function specLinks(reading, id, plan) {
  const parts = (reading || "").match(/§\s*[\d.]+/g) || [];
  return parts.map((p) => {
    const at = p.replace(/\s+/g, "");
    return html`<a key=${at} class="sc-flow-link" href=${flowHref(id, plan, "doc/spec", { at })}>spec ${at}</a>`;
  });
}

// FlowStory is one row: its facts, its steps as a timeline (a step opens
// its report or its gate's job log), its findings and what it reads.
export function FlowStory({ snap, route }) {
  const s = sessionOf(snap, route.id);
  useClock();
  const plan = planOf(s, route);
  const { data, err } = useFlow(route.id, plan, JSON.stringify((s && s.flows) || []));
  if (!s || !data) return html`<section class="sc-screen"><p class=${err ? "sc-err-text sc-empty" : "sc-dim sc-empty"}>${err || (snap ? "Loading…" : "Loading…")}</p></section>`;
  const id = decodeURIComponent(route.story);
  const row = id === "P" ? data.premise : id === "F" ? data.final : data.stories.find((r) => r.id === id);
  if (!row) return html`<section class="sc-screen"><p class="sc-dim sc-empty">The board has no row ${id}.</p></section>`;
  const costs = data.costs || {};
  const spend = rowSpend(costs, row.id);
  const steps = row.steps || [];
  const first = steps.find((st) => st.started);
  const last = [...steps].reverse().find((st) => st.ended);
  const running = steps.some((st) => stepOutcome(st) === "running");
  const facts = [
    row.developer,
    row.id !== "P" && row.id !== "F" ? [row.tier, row.review].filter(Boolean).join(" · ") : "",
    row.commits ? row.commits + " commits" : "",
    first ? elapsed(first.started, running || !last ? Date.now() : last.ended) : "",
    typeof spend === "number" ? usd(spend) : "",
  ].filter(Boolean);
  const name = row.id === "P" ? "Premises" : row.id === "F" ? "Final round" : "Story " + row.id;
  return html`<section class="sc-screen sc-flow-story">
    <p class="sc-flow-title"><a class="sc-back" href=${flowHref(s.id, plan)}>‹ board</a> ${name}</p>
    <p class="sc-dim">${facts.join(" · ")}</p>
    <div class="sc-box">
      <span class="sc-legend">steps</span>
      ${steps.length === 0 ? html`<p class="sc-faint">no step yet</p>` : null}
      <ol class="sc-tl">
        ${steps.map((st) => {
          const outcome = stepOutcome(st);
          const cost = costs.by_agent ? costs.by_agent[st.name] : undefined;
          const when = st.started ? elapsed(st.started, st.ended || Date.now()) : "";
          const right = [when, typeof cost === "number" ? usd(cost) : ""].filter(Boolean).join(" ");
          const head = html`<span class="sc-tl-name">${stepWord(st)}</span>
            <span class="sc-dim"> ${st.role || "gates"} · ${st.status || "unreadable"}</span>
            <span class="sc-flow-right sc-dim">${right}</span>`;
          return html`<li key=${st.name} class=${"sc-tl-ev sc-tl-" + outcome}>
            ${st.report
              ? html`<a class="sc-tl-head" href=${flowHref(s.id, plan, "doc/" + encodeURIComponent("report:" + st.name))}>${head}</a>`
              : html`<div class="sc-tl-head">${head}</div>`}
            ${st.unreadable ? html`<div class="sc-err-text sc-small">${st.unreadable}</div>` : null}
            ${(st.gates || []).map(
              (g, i) => html`<a key=${i} class="sc-tl-gate" href=${g.job ? "#s/" + s.id + "/job/" + g.job : undefined}>
                <span class=${"sc-verdict" + (g.verdict && g.verdict !== "PASS" ? " sc-err-text" : g.verdict ? " sc-ok-text" : " sc-dim")}>${g.verdict || "…"}</span> ${g.text}
              </a>`,
            )}
          </li>`;
        })}
      </ol>
    </div>
    ${row.id !== "P"
      ? html`<div class="sc-box">
          <span class="sc-legend">findings</span>
          <p class=${row.findings && row.findings.some((n) => n > 0) ? "" : "sc-dim"}>
            ${row.findings ? "Critical " + row.findings[0] + " · Important " + row.findings[1] + " · Minor " + row.findings[2] : "none yet"}
          </p>
          ${row.id === "F" && (data.kept || []).length > 0 ? data.kept.map((k, i) => html`<p key=${i} class="sc-faint sc-small">${k}</p>`) : null}
        </div>`
      : null}
    ${row.id !== "P" && row.id !== "F"
      ? html`<div class="sc-box">
          <span class="sc-legend">reads</span>
          <a class="sc-flow-link" href=${flowHref(s.id, plan, "doc/plan", { at: row.line || "" })}>plan · Story ${row.id}${row.line ? " (L" + row.line + "–" + row.end + ")" : ""}</a>
          ${specLinks(row.reading, s.id, plan)}
          ${row.commits ? html`<a class="sc-flow-link" href=${"#s/" + s.id + "/diff"}>${row.commits} commits → changes</a>` : null}
        </div>`
      : null}
  </section>`;
}
