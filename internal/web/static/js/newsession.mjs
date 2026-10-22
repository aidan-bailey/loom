// The new-session screen. The daemon sends the launch defaults and the
// lists to choose from; the app hard-codes none of them and sends a field
// only when it differs from its default. The daemon's checks stay the authority.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { pref } from "./store.mjs";
import { utf8Len } from "./sheets.mjs";

const MAX_PROMPT = 64 * 1024;

// choices is the list with the default among it, first when the list lacks it.
export function choices(list, def) {
  const out = (list || []).slice();
  if (!out.includes(def)) out.unshift(def);
  return out;
}

export function choiceLabel(v) {
  return v === "" ? "(default)" : v;
}

// launchBody builds the POST /api/v1/sessions body: the workspace, the
// trimmed title and prompt, and each launch override that differs from its default.
export function launchBody({ workspace, title, prompt, defaults, picks }) {
  const body = { workspace, title: title.trim(), prompt: prompt.trim() };
  for (const k of ["account", "model", "permission_mode", "effort"]) {
    if (picks[k] !== defaults[k]) body[k] = picks[k];
  }
  return body;
}

function Pick({ label, list, def, value, onChange }) {
  return html`
    <label class="sc-field">
      <span>${label}</span>
      <select value=${value} onChange=${(e) => onChange(e.target.value)}>
        ${choices(list, def).map((v) => html`<option key=${v} value=${v}>${choiceLabel(v)}</option>`)}
      </select>
    </label>
  `;
}

export function NewSession({ snap, stale, go, act }) {
  const workspaces = (snap && snap.workspaces) || [];
  const [ws, setWs] = useState(() => {
    const want = pref("filter") || "";
    const hit = workspaces.find((w) => String(w.id) === want);
    return String(hit ? hit.id : workspaces.length > 0 ? workspaces[0].id : "");
  });
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  const [defs, setDefs] = useState(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [picks, setPicks] = useState({ account: "", model: "", permission_mode: "", effort: "" });
  const [busy, setBusy] = useState(false);

  // The defaults are per workspace (its settings and accounts), so they are
  // fetched again when the workspace changes.
  useEffect(() => {
    if (ws === "") return undefined;
    let live = true;
    setDefs(null);
    setFailed(false);
    act("GET", "/api/v1/launch-defaults?workspace=" + encodeURIComponent(ws)).then((r) => {
      if (!live) return;
      if (!r.ok || !r.data) {
        setFailed(true);
        return;
      }
      setDefs(r.data);
      setPicks({
        account: r.data.account,
        model: r.data.model,
        permission_mode: r.data.permission_mode,
        effort: r.data.effort,
      });
    });
    return () => {
      live = false;
    };
  }, [ws, attempt]);

  if (!snap) return html`<main class="sc-screen"><p class="sc-dim">Loading…</p></main>`;

  const chosen = workspaces.find((w) => String(w.id) === ws);
  const promptTooLong = utf8Len(prompt.trim()) > MAX_PROMPT;
  const ready = !stale && !busy && defs && chosen && title.trim() !== "" && !promptTooLong;
  const set = (k) => (v) => setPicks((p) => ({ ...p, [k]: v }));

  async function create() {
    setBusy(true);
    try {
      const r = await act(
        "POST",
        "/api/v1/sessions",
        launchBody({ workspace: chosen.id, title, prompt, defaults: defs, picks }),
      );
      if (!r.ok) return;
      if (r.status === 201 && r.data && r.data.id !== undefined) go("#s/" + r.data.id);
      else go("#");
    } finally {
      setBusy(false);
    }
  }

  return html`
    <main class="sc-screen">
      <a class="sc-back" href="#">‹ Fleet</a>
      <h1 class="sc-h1 sc-wrap">New session</h1>
      <form class="sc-form" onSubmit=${(e) => { e.preventDefault(); if (ready) create(); }}>
        <label class="sc-field">
          <span>Workspace</span>
          <select value=${ws} onChange=${(e) => setWs(e.target.value)}>
            ${workspaces.map((w) => html`<option key=${w.id} value=${String(w.id)}>${w.label || w.name}</option>`)}
          </select>
        </label>
        <label class="sc-field">
          <span>Title</span>
          <input type="text" value=${title} autocapitalize="none" autocomplete="off" onInput=${(e) => setTitle(e.target.value)} />
        </label>
        <label class="sc-field">
          <span>Prompt</span>
          <textarea rows="6" value=${prompt} onInput=${(e) => setPrompt(e.target.value)}></textarea>
          ${promptTooLong ? html`<span class="sc-small sc-err">Too long: a prompt is at most 64 KiB.</span>` : null}
        </label>
        ${failed
          ? html`<p class="sc-small sc-err">
              Could not load the launch defaults.
              <button class="sc-btn" type="button" onClick=${() => setAttempt((n) => n + 1)}>Retry</button>
            </p>`
          : null}
        ${defs
          ? html`<details class="sc-launch">
              <summary>Launch options</summary>
              ${defs.accounts && defs.accounts.length > 0
                ? html`<${Pick} label="Account" list=${defs.accounts} def=${defs.account} value=${picks.account} onChange=${set("account")} />`
                : null}
              <${Pick} label="Model" list=${defs.models} def=${defs.model} value=${picks.model} onChange=${set("model")} />
              <${Pick} label="Permission mode" list=${defs.permission_modes} def=${defs.permission_mode} value=${picks.permission_mode} onChange=${set("permission_mode")} />
              <${Pick} label="Effort" list=${defs.efforts} def=${defs.effort} value=${picks.effort} onChange=${set("effort")} />
            </details>`
          : null}
        <button class="sc-btn sc-primary sc-block" type="submit" disabled=${!ready}>Start session</button>
      </form>
    </main>
  `;
}
