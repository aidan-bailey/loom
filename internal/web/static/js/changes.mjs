// The changes screen: a session's worktree diff against its base commit,
// file by file (GET /api/v1/sessions/{id}/diff); a file opens to its hunks.
// Every line is drawn as text.
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { allSessions } from "./fleet.mjs";
import { SubLine } from "./session.mjs";

// POLL is how often the screen refreshes while the session is Running.
const POLL = 10000;

// HUNK_HEAD splits a hunk header into its @@ ranges and the section after.
const HUNK_HEAD = /^(@@[^@]*@@)(.*)$/;

// lineClass is a diff line's class, by its prefix.
export function lineClass(l) {
  switch (l[0]) {
    case "+":
      return "sc-dl sc-dl-add";
    case "-":
      return "sc-dl sc-dl-del";
    case "\\":
      return "sc-dl sc-dim";
    default:
      return "sc-dl";
  }
}

// splitPath is a path's file name and its directory ("./" at the top).
export function splitPath(p) {
  const i = p.lastIndexOf("/");
  return i < 0 ? [p, "./"] : [p.slice(i + 1), p.slice(0, i + 1)];
}

function HunkView({ h }) {
  const cut = new Set(h.cut || []);
  const m = (h.header || "").match(HUNK_HEAD);
  return html`
    <div class="sc-dl sc-dl-hunk">${m ? html`<span class="sc-accent">${m[1]}</span><span class="sc-dim">${m[2]}</span>` : h.header}</div>
    ${(h.lines || []).map((l, i) => html`<div key=${i} class=${lineClass(l)}>${l}${cut.has(i) ? html`<span class="sc-dim">…</span>` : null}</div>`)}
  `;
}

// Body is an open file: its hunks, or why it has none.
function Body({ f }) {
  let why = "";
  if (f.omitted === "lines") why = "Too long to show here (" + (f.added + f.removed) + " changed lines).";
  else if (f.omitted === "budget") why = "Not shown: the diff is over 1 MiB.";
  else if (f.binary) why = "Binary file.";
  else if (!f.hunks || f.hunks.length === 0) why = "No text changes.";
  if (why) return html`<p class="sc-dim sc-small sc-file-why">${why}</p>`;
  return html`<div class="sc-hunks"><div class="sc-hunks-in">${f.hunks.map((h, i) => html`<${HunkView} key=${i} h=${h} />`)}</div></div>`;
}

function File({ f, open, onToggle }) {
  const [name, dir] = splitPath(f.path);
  const sub = f.old_path && (f.status === "R" || f.status === "C") ? "from " + f.old_path : dir;
  return html`<details class="sc-file" open=${open} onToggle=${(e) => onToggle(f.path, e.currentTarget.open)}>
    <summary class="sc-file-row">
      <span class=${"sc-file-st sc-st-" + f.status}>${f.status}</span>
      <span class="sc-file-name"><span class="sc-file-base">${name}</span><span class="sc-file-dir">${sub}</span></span>
      <span class="sc-file-n">${f.binary
        ? html`<span class="sc-dim">binary</span>`
        : html`${f.added ? html`<span class="sc-add">+${f.added}</span>` : null}${f.added && f.removed ? " " : null}${f.removed ? html`<span class="sc-del">−${f.removed}</span>` : null}`}</span>
    </summary>
    ${open ? html`<${Body} f=${f} />` : null}
  </details>`;
}

export function Changes({ snap, route }) {
  const id = route.id;
  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const [patch, setPatch] = useState(null);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState(() => new Set());
  const loading = useRef(false);
  const again = useRef(false);
  const alive = useRef(true);
  const seeded = useRef(false);
  const working = Boolean(s && s.status === "Running");

  // load fetches the diff; one is in flight at a time, and a load asked for
  // meanwhile runs once it lands. A lone file starts open, on the first
  // diff only, so a refresh never reopens one the user closed.
  async function load() {
    if (id === undefined) return;
    if (loading.current) {
      again.current = true;
      return;
    }
    loading.current = true;
    let r;
    try {
      r = await call("GET", "/api/v1/sessions/" + id + "/diff");
    } finally {
      loading.current = false;
    }
    if (!alive.current) return;
    if (r.status !== 200 || !r.data) {
      setErr((r.data && r.data.error) || "Could not read the diff.");
    } else {
      setErr("");
      setPatch(r.data);
      const files = r.data.files || [];
      if (!seeded.current) {
        seeded.current = true;
        if (files.length === 1) setOpen(new Set([files[0].path]));
      }
    }
    if (again.current) {
      again.current = false;
      load();
    }
  }

  function toggle(path, isOpen) {
    setOpen((o) => {
      if (o.has(path) === isOpen) return o;
      const n = new Set(o);
      if (isOpen) n.add(path);
      else n.delete(path);
      return n;
    });
  }

  useEffect(() => {
    alive.current = true;
    load();
    return () => {
      alive.current = false;
    };
  }, [id]);

  // The stream changing the session's counts or status refreshes the diff;
  // so does the poll while the session is Running.
  const d = (s && s.diff) || {};
  const sig = s ? s.status + ":" + (d.added || 0) + ":" + (d.removed || 0) : "";
  useEffect(() => {
    if (patch) load();
  }, [sig]);
  useEffect(() => {
    if (!working) return undefined;
    const timer = setInterval(load, POLL);
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

  const now = Date.now();
  const files = patch ? patch.files || [] : [];
  const added = files.reduce((n, f) => n + (f.added || 0), 0);
  const removed = files.reduce((n, f) => n + (f.removed || 0), 0);
  const since = patch && patch.base ? patch.base.slice(0, 7) : "";

  return html`
    <section class="sc-screen sc-changes">
      <nav class="sc-topbar" aria-label="Changes"><a class="sc-back" href=${"#s/" + s.id}>‹ ${s.title}</a></nav>
      <h1 class="sc-h1">Changes</h1>
      ${patch
        ? html`<div class="sc-subline">
            <span>${files.length} ${files.length === 1 ? "file" : "files"}</span>
            <span><span class="sc-add">+${added}</span> <span class="sc-del">−${removed}</span></span>
            ${since ? html`<span>since ${since}</span>` : null}
          </div>`
        : html`<${SubLine} s=${s} snap=${snap} now=${now} />`}
      ${!patch
        ? html`<div class="sc-empty">
            <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
            ${err ? html`<button class="sc-btn" type="button" onClick=${() => load()}>retry</button>` : null}
          </div>`
        : null}
      ${patch && files.length === 0 ? html`<p class="sc-dim sc-empty">No changes${since ? " since " + since : ""}.</p>` : null}
      ${files.length > 0
        ? html`<div class="sc-files">${files.map((f, i) => html`<${File} key=${i + ":" + f.path} f=${f} open=${open.has(f.path)} onToggle=${toggle} />`)}</div>`
        : null}
      ${patch && err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load()}>retry</button></p>`
        : null}
    </section>
  `;
}
