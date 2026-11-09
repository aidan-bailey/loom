// The Flows screen (#flows, spec 2026-11-09 §5): the host's live flows,
// each with its chip and a way to its board, and its past runs from the
// records, by repo, with each repo's cost per story as bars. Read from
// GET /api/v1/flows on open and on ↻; it acts on nothing.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { ago } from "./fleet.mjs";
import { ContextRow, FLEET_BACK } from "./chrome.mjs";
import { chipClass, flowChip, flowHref, usd } from "./flow.mjs";

// SHOWN is how many of a repo's past runs the screen lists.
const SHOWN = 8;
// BARS is how many of a repo's past runs its bars compare.
const BARS = 5;

// repoName is a records key as a name: the key is the main checkout's path
// with every other character made "-", so the part after "-Source-" (or the
// key itself) reads best.
export function repoName(key) {
  const i = key.lastIndexOf("-Source-");
  return i >= 0 ? key.slice(i + "-Source-".length) : key.replace(/^-+/, "");
}

let refresh = () => {};

export function FlowsHead() {
  return html`<${ContextRow} back=${FLEET_BACK} title="Flows">
    <button class="sc-icon" type="button" aria-label="Read the flows again" onClick=${() => refresh()}>↻</button>
  <//>`;
}

// Bars is a repo's last runs' cost per story, oldest first.
function Bars({ runs }) {
  const per = runs
    .filter((r) => typeof r.usd === "number" && r.stories > 0)
    .slice(0, BARS)
    .reverse()
    .map((r) => r.usd / r.stories);
  if (per.length < 2) return null;
  const top = Math.max(...per);
  const sorted = [...per].sort((a, b) => a - b);
  const mid = sorted.length % 2 ? sorted[(sorted.length - 1) / 2] : (sorted[sorted.length / 2 - 1] + sorted[sorted.length / 2]) / 2;
  return html`<div class="sc-compare">
    <div class="sc-spark" aria-hidden="true">
      ${per.map((v, i) => html`<span key=${i} class=${"sc-sbar sc-h" + Math.max(5, Math.round((v / top) * 20) * 5)}></span>`)}
    </div>
    <p class="sc-faint sc-small">run cost per story, last ${per.length} runs · median ${usd(mid)}</p>
  </div>`;
}

export function Flows() {
  const [state, setState] = useState({ data: null, err: "" });
  const [again, setAgain] = useState(0);
  refresh = () => setAgain((n) => n + 1);
  useEffect(() => {
    let live = true;
    call("GET", "/api/v1/flows").then(({ status, data }) => {
      if (!live) return;
      if (status === 200 && data) setState({ data, err: "" });
      else setState((s) => ({ data: s.data, err: (data && data.error) || "loom answered " + status }));
    });
    return () => {
      live = false;
    };
  }, [again]);

  const { data, err } = state;
  if (!data) return html`<section class="sc-screen"><p class=${err ? "sc-err-text sc-empty" : "sc-dim sc-empty"}>${err || "Loading…"}</p></section>`;
  const byKey = new Map();
  for (const r of data.past) {
    if (!byKey.has(r.key)) byKey.set(r.key, []);
    byKey.get(r.key).push(r);
  }
  return html`<section class="sc-screen sc-flows">
    ${err ? html`<p class="sc-err-text sc-small">${err}</p>` : null}
    <div class="sc-box">
      <span class="sc-legend">live</span>
      ${data.live.length === 0 ? html`<p class="sc-faint">No session's worktree holds a flow.</p>` : null}
      ${data.live.map(
        (l) => html`<a key=${l.session + l.flow.plan} class="sc-flow-row" href=${flowHref(l.session, l.flow.plan)}>
          <span class="sc-title-text">${l.title}</span>
          <span class=${"sc-flow-right " + chipClass(l.flow)}>${flowChip(l.flow)}</span>
        </a>`,
      )}
    </div>
    ${[...byKey.entries()].map(
      ([key, runs]) => html`<div key=${key} class="sc-box">
        <span class="sc-legend">past · ${repoName(key)}</span>
        ${runs.slice(0, SHOWN).map(
          (r) => html`<div key=${r.file} class="sc-flow-row">
            <span>${r.stem.replace(/^\d{4}-\d{2}-\d{2}-/, "")}</span>
            <span class=${"sc-flow-right " + (r.unreadable ? "sc-err-text" : "sc-dim")}>
              ${r.unreadable ? "unreadable" : [r.stories + " st", typeof r.usd === "number" ? usd(r.usd) : "", r.ended, r.end ? ago(r.end) : ""].filter(Boolean).join(" · ")}
            </span>
          </div>`,
        )}
        ${runs.length > SHOWN ? html`<p class="sc-faint sc-small">and ${runs.length - SHOWN} more</p>` : null}
        <${Bars} runs=${runs} />
      </div>`,
    )}
    ${data.past.length === 0 ? html`<p class="sc-faint sc-small">No run records yet.</p>` : null}
  </section>`;
}
