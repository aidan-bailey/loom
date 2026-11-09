// A flow's document (#s/<id>/flow/doc/<doc>?plan=&at=): the spec, the
// skeleton, the plan, the record, the drafts' state or an agent's report,
// read from GET …/flow/doc?plan=&doc= as the daemon's markdown tree and
// drawn by Markdown, never from an HTML string. A section index lists its
// headings; at= opens it at a section: "§4" the heading that starts "4",
// a number the last heading at or above that line (a story's plan range).
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { sessionOf } from "./fleet.mjs";
import { Markdown } from "./markdown.mjs";
import { flowHref } from "./flow.mjs";

// The document keys' words.
const NAMES = { spec: "spec", skeleton: "skeleton", plan: "plan", record: "record", "drafts-state": "drafts' state" };

// textOf is a node's text.
function textOf(n) {
  if (!n) return "";
  if (n.t === "text" || n.t === "code_inline") return n.v || "";
  return (n.c || []).map(textOf).join("");
}

// headings is the tree's headings in document order, each with its text,
// level and source line.
function headings(blocks) {
  const out = [];
  const walk = (nodes) => {
    for (const n of nodes || []) {
      if (n.t === "h") out.push({ text: textOf(n), level: n.level, line: n.line || 0 });
      else if (n.c && n.t !== "code") walk(n.c);
    }
  };
  walk(blocks);
  return out;
}

// target is the index of the heading at names, -1 for none.
function target(hs, at) {
  if (!at) return -1;
  const sec = at.match(/^§\s*([\d.]+)/);
  if (sec) {
    const n = sec[1].replace(/\.$/, "");
    return hs.findIndex((h) => {
      const t = h.text.trim();
      return t === n || t.startsWith(n + ".") || t.startsWith(n + " ") || t.startsWith("§" + n);
    });
  }
  const line = Number(at);
  if (!Number.isFinite(line) || line <= 0) return -1;
  let best = -1;
  hs.forEach((h, i) => {
    if (h.line && h.line <= line) best = i;
  });
  return best;
}

export function FlowDoc({ snap, route }) {
  const s = sessionOf(snap, route.id);
  const [state, setState] = useState({ data: null, err: "" });
  const body = useRef(null);
  const plan = route.plan || (s && s.flows && s.flows[0] ? s.flows[0].plan : "");
  const doc = decodeURIComponent(route.doc || "");

  useEffect(() => {
    if (!plan || !doc) return undefined;
    let live = true;
    call("GET", "/api/v1/sessions/" + route.id + "/flow/doc?plan=" + encodeURIComponent(plan) + "&doc=" + encodeURIComponent(doc)).then(
      ({ status, data }) => {
        if (!live) return;
        if (status === 200 && data) setState({ data, err: "" });
        else setState({ data: null, err: (data && data.error) || "loom answered " + status });
      },
    );
    return () => {
      live = false;
    };
  }, [route.id, plan, doc]);

  const hs = state.data ? headings(state.data.blocks) : [];
  const jump = (i) => {
    const el = body.current && body.current.querySelectorAll("h3, h4")[i];
    if (el) el.scrollIntoView({ block: "start" });
  };
  useEffect(() => {
    const i = target(hs, route.at);
    if (i >= 0) jump(i);
  }, [state.data, route.at]);

  const name = doc.startsWith("report:") ? "report · " + doc.slice("report:".length) : NAMES[doc] || doc;
  return html`<section class="sc-screen sc-flow-doc">
    <p class="sc-flow-title"><a class="sc-back" href=${flowHref(route.id, plan)}>‹ board</a> ${name}</p>
    ${state.err ? html`<p class="sc-err-text">${state.err}</p>` : null}
    ${!state.data && !state.err ? html`<p class="sc-dim sc-empty">Loading…</p>` : null}
    ${state.data
      ? html`<p class="sc-faint sc-small sc-flow-path">${state.data.path}</p>
          ${hs.filter((h) => h.level <= 3).length > 2
            ? html`<details class="sc-box sc-flow-index">
                <summary class="sc-dim">sections</summary>
                ${hs.map((h, i) =>
                  h.level <= 3
                    ? html`<button key=${i} type="button" class=${"sc-flow-sec sc-flow-sec-" + h.level} onClick=${() => jump(i)}>${h.text}</button>`
                    : null,
                )}
              </details>`
            : null}
          <div class="sc-flow-md" ref=${body}><${Markdown} blocks=${state.data.blocks} /></div>
          ${state.data.cut ? html`<p class="sc-faint sc-small">The document was cut at 1 MiB.</p>` : null}`
      : null}
  </section>`;
}
