# PWA header and footer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One pinned header for every screen of the phone app: a status row (link, host, counts that open each group's next session), a context row per screen, and a session's three views as tabs, each ending in the reply box.

**Architecture:** `app.mjs` draws `<header class="sc-chrome">` (the status row, the route's head, the toasts) above `<main>`; `screens.mjs` maps each route to `{Screen, Head}`; the rows are presentational components in a new `js/chrome.mjs`, fed by pure helpers in `js/fleet.mjs`. No Go, no route, no JSON field changes.

**Tech Stack:** Preact + htm (`internal/web/static/lib/preact-htm.mjs`), ES modules (`.mjs`), one stylesheet (`app.css`) under a `default-src 'self'` CSP (no `style` attributes).

**Spec:** `docs/superpowers/specs/2026-11-01-pwa-header-footer-design.md`.

## Details settled while planning

The spec is updated in this plan's commit to match:

1. **`fleetGroups(snap)` and `nextInGroup(groups, group, currentId)` live in `js/fleet.mjs`, not `js/chrome.mjs`.** `fleet.mjs` imports `ContextRow` from `chrome.mjs` for its head, so helpers in `chrome.mjs` that import `fleet.mjs` would make a cycle. `chrome.mjs` imports only `skin.mjs`. `fleetGroups` returns the groups' arrays (the counts are their lengths), and `nextInGroup` takes them, so one grouping serves both.
2. **`sessionOf(snap, id)` joins `fleet.mjs`**, replacing the same `find` in the three session screens and their head.
3. **`FLEET_BACK` (`{href: "#", label: "fleet"}`) is exported by `chrome.mjs`** for the four heads that go back to the fleet.
4. **A session's views fill the space under the header with `flex: 1`**, `main` becoming a flex column, instead of today's `min-height: calc(100dvh - …)`, which with a header in the flow would make every short page scroll by the header's height.
5. **The shell drops its top inset only when the header is drawn** (`.shell.sc-chromed`): pairing and the splash keep it.

## Verification tools (both tasks)

There is no JS test harness and CI has no Node. Each task runs:

- `for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done` — no output means pass.
- `CGO_ENABLED=0 go test ./internal/web/` — the static tests hold the modules to the CSP (`TestStatic_ModulesSetNoStyleAttribute`), resolve every import (`TestStatic_EveryReferenceResolves`), check every named import is exported (`TestStatic_NamedImportsAreExported`) and balance the stylesheet's braces.
- Screenshots from a canned loom (Task 1, step 1 builds it) with headless Chromium, outside the worktree.

`$SCRATCH` below is this session's scratchpad directory; nothing under it is committed. `$WT` is the worktree root.

---

### Task 1: The header

**Files:**
- Create: `internal/web/static/js/chrome.mjs`
- Modify: `internal/web/static/js/app.mjs`, `screens.mjs`, `fleet.mjs`, `session.mjs`, `turns.mjs`, `changes.mjs`, `settings.mjs`, `newsession.mjs`
- Modify: `internal/web/static/app.css`
- Modify: `USAGE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`, `docs/superpowers/specs/2026-11-01-pwa-header-footer-design.md`
- Scratch (not committed): `$SCRATCH/render/serve.py`, `$SCRATCH/render/groups.mjs`

- [ ] **Step 1: Build the canned loom and take the "before" shots**

Create `$SCRATCH/render/serve.py`:

```python
#!/usr/bin/env python3
# A canned loom for the phone app's screenshots: serves internal/web/static
# and answers the API with fixed data.
# Usage: serve.py <static dir> <port> <mode>
#   live   snapshot, then pings
#   toast  snapshot, a notice (a toast), then pings
#   slow   the stream sends nothing (CONNECTING over a cached snapshot)
#   bye    snapshot, then bye, then close (RESTARTING)
#   drop   the first stream: snapshot, then close; later ones 503 (OFFLINE)
import json, sys, time
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from pathlib import Path

STATIC, PORT, MODE = Path(sys.argv[1]), int(sys.argv[2]), sys.argv[3]
NOW = "2026-11-01T10:00:00Z"
LABEL = {"needs_you": "Needs you", "your_turn": "Your turn", "working": "Working", "paused": "Paused"}
CONNECTS = [0]


def S(i, ws, title, group, status, since, **kw):
    s = dict(id=i, workspace=ws, title=title, branch="aidanb/" + title, group=group, label=LABEL[group],
             status=status, since=since, wait={"reason": "", "detail": ""}, last_message="",
             diff={"added": 0, "removed": 0}, ahead=0, behind=0, parity_known=False, pr=None,
             account="", agents=[], remote_url="", claude=False)
    s.update(kw)
    return s


SNAP = {
    "build": "dev",
    "host": sys.argv[4] if len(sys.argv) > 4 else "fresco",
    "workspaces": [{"id": 1, "name": "loom", "label": "loom", "shown": True},
                   {"id": 2, "name": "flow", "label": "flow", "shown": True}],
    "sessions": {
        "1": [
            S(1, 1, "fix-pane-cursor", "working", "Running", "2026-11-01T09:58:00Z", claude=True,
              remote_url="https://claude.ai/code/x", diff={"added": 212, "removed": 40},
              last_message="Running the ui tests now."),
            S(2, 1, "sandbox-ask", "needs_you", "Prompting", "2026-11-01T09:40:00Z", claude=True,
              wait={"reason": "Allow Bash", "detail": "go test ./ui/..."}),
            S(3, 1, "plain-shell", "working", "Loading", "2026-11-01T09:50:00Z"),
            S(7, 1, "a-very-long-session-title-that-will-not-fit-on-one-line", "your_turn", "Ready",
              "2026-11-01T09:30:00Z", claude=True, last_message="Done: the header is pinned."),
        ],
        "2": [
            S(4, 2, "flow-docs", "needs_you", "Prompting", "2026-11-01T09:10:00Z", claude=True,
              wait={"reason": "Plan approval", "detail": ""}),
            S(5, 2, "flow-old", "paused", "Paused", "2026-10-30T09:00:00Z"),
            S(6, 2, "flow-run", "working", "Running", "2026-11-01T09:55:00Z", claude=True),
        ],
    },
    "device": {"name": "iPhone", "notify": {"waiting": True, "finished": True, "paused_until": ""}, "push": ""},
    "badge": 2, "notices": [], "usage": [], "usage_stale_after_s": 900,
    "quick_replies": ["yes", "continue", "/compact", "/clear"],
}
TURNS = {"conversation": "c", "earlier": None, "reset": True, "skipped": 0, "turns": [
    {"id": 1, "at": NOW, "from": "you", "name": "", "text": "Fix the cursor in the pane.", "cut": False,
     "command": "", "partial": False, "items": [{"kind": "note", "text": "Interrupted", "error": False}]},
]}
DIFF = {"base": "a1b2c3d4e5f6", "files": [
    {"path": "ui/header.go", "old_path": "", "status": "M", "binary": False, "added": 12, "removed": 3,
     "hunks": [{"header": "@@ -1,3 +1,4 @@", "lines": [" a", "-b", "+c", "+d"], "cut": []}], "omitted": ""},
]}
DEFAULTS = {"account": "", "accounts": [], "model": "", "models": [], "permission_mode": "",
            "permission_modes": [], "effort": "", "efforts": []}
TYPES = {".html": "text/html", ".mjs": "text/javascript", ".js": "text/javascript", ".css": "text/css",
         ".webmanifest": "application/manifest+json", ".png": "image/png", ".svg": "image/svg+xml"}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def answer(self, code, body, ctype="application/json"):
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def event(self, name, data):
        self.wfile.write(f"event: {name}\ndata: {json.dumps(data)}\n\n".encode())
        self.wfile.flush()

    def stream(self):
        CONNECTS[0] += 1
        if MODE == "drop" and CONNECTS[0] > 1:
            return self.answer(503, {"error": "down"})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        if MODE == "slow":
            time.sleep(60)
            return
        self.event("snapshot", SNAP)
        if MODE in ("bye",):
            self.event("bye", {})
            return
        if MODE == "drop":
            return
        if MODE == "toast":
            self.event("notice", {"text": "Sent.", "error": False, "at": NOW})
        for _ in range(30):
            time.sleep(2)
            self.wfile.write(b": ping\n\n")
            self.wfile.flush()

    def do_GET(self):
        p = self.path.split("?")[0]
        if p == "/api/v1/stream":
            return self.stream()
        if p.endswith("/turns"):
            return self.answer(200, TURNS)
        if p.endswith("/diff"):
            return self.answer(200, DIFF)
        if p == "/api/v1/launch-defaults":
            return self.answer(200, DEFAULTS)
        if p.startswith("/api/"):
            return self.answer(404, {"error": "not here"})
        f = STATIC / ("index.html" if p == "/" else p.lstrip("/"))
        if not f.is_file():
            return self.answer(404, b"no", "text/plain")
        body = f.read_bytes()
        if f.name == "index.html":
            body = body.replace(b"__LOOM_BUILD__", b"dev")
        self.answer(200, body, TYPES.get(f.suffix, "application/octet-stream"))


ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
```

Create `$SCRATCH/render/shot.sh`:

```bash
#!/usr/bin/env bash
# shot.sh <out.png> <hash> [width] [profile dir]: one screenshot of the app
# from the canned loom on port 8790, with the console's errors after it.
set -u
out=$1; hash=$2; w=${3:-390}; prof=${4:-$(mktemp -d)}
chromium --headless=new --disable-gpu --no-first-run --user-data-dir="$prof" \
  --window-size="$w",844 --virtual-time-budget=4000 --enable-logging=stderr --v=0 \
  --screenshot="$out" "http://127.0.0.1:8790/$hash" 2>&1 | grep -iE "console|uncaught" || true
```

Run (from `$WT`):
```bash
python3 $SCRATCH/render/serve.py internal/web/static 8790 live &
for h in "" "#s/1" "#s/1/turns" "#s/1/diff" "#settings"; do bash $SCRATCH/render/shot.sh "$SCRATCH/render/before-${h//[#\/]/_}.png" "$h"; done
kill %1
```
Expected: five PNGs that show today's app (the fleet's `loom@fresco` heading and bottom status line, the session's `‹ fleet` link), and no `Uncaught` line. Read each PNG to confirm the canned loom draws the app.

- [ ] **Step 2: Add the fleet helpers**

In `internal/web/static/js/fleet.mjs`, after `groupSessions` (which ends `return out;\n}`), add:

```js
// fleetGroups groups the sessions of the shown workspaces as the fleet does,
// whatever the fleet's chip filter: the status row's counts and the sessions
// a count's tap cycles through.
export function fleetGroups(snap) {
  const shown = new Set(shownWorkspaces(snap).map((w) => String(w.id)));
  return groupSessions(allSessions(snap).filter((s) => shown.has(String(s.workspace))), "");
}

// nextInGroup is the session a count's tap opens: the one after currentId in
// the group's order, wrapping to the first, or the first when currentId is
// not in the group; undefined for an empty group.
export function nextInGroup(groups, group, currentId) {
  const list = groups[group] || [];
  if (list.length === 0) return undefined;
  const at = list.findIndex((s) => s.id === currentId);
  return list[(at + 1) % list.length];
}

// sessionOf is the session the snapshot lists under id, or undefined.
export function sessionOf(snap, id) {
  return snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
}
```

- [ ] **Step 3: Check the helpers with a throwaway script**

Create `$SCRATCH/render/groups.mjs`:

```js
// node groups.mjs <path to fleet.mjs>: asserts fleetGroups and nextInGroup.
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

const m = await import(pathToFileURL(process.argv[2]).href);
const S = (id, workspace, group, since) => ({ id, workspace, group, since });
const snap = {
  workspaces: [{ id: 1, shown: true }, { id: 2, shown: false }, { id: 3 }],
  sessions: {
    1: [S(1, 1, "needs_you", "2026-11-01T10:00:00Z"), S(2, 1, "needs_you", "2026-11-01T09:00:00Z"), S(3, 1, "working", "2026-11-01T08:00:00Z")],
    2: [S(4, 2, "needs_you", "2026-11-01T07:00:00Z")],
    3: [S(5, 3, "your_turn", "2026-11-01T06:00:00Z"), S(6, 3, "mystery", "2026-11-01T05:00:00Z")],
  },
};
const g = m.fleetGroups(snap);
assert.deepEqual(g.needs_you.map((s) => s.id), [2, 1], "longest wait first; an unshown workspace left out");
assert.deepEqual(g.your_turn.map((s) => s.id), [5], "a workspace without the flag is shown");
assert.deepEqual(g.working.map((s) => s.id), [3]);
assert.deepEqual(g.paused.map((s) => s.id), [6], "a group this build does not know is Paused, in no count");
assert.equal(m.nextInGroup(g, "needs_you", undefined).id, 2, "from no session: the first");
assert.equal(m.nextInGroup(g, "needs_you", 2).id, 1, "the next");
assert.equal(m.nextInGroup(g, "needs_you", 1).id, 2, "wraps");
assert.equal(m.nextInGroup(g, "needs_you", 5).id, 2, "from outside the group: the first");
assert.equal(m.nextInGroup(g, "working", 3).id, 3, "a lone session is its own next");
assert.equal(m.nextInGroup({ needs_you: [] }, "needs_you", 1), undefined, "an empty group");
assert.equal(m.nextInGroup(g, "unknown", 1), undefined, "a group not counted");
assert.equal(m.sessionOf(snap, 5).id, 5);
assert.equal(m.sessionOf(snap, 99), undefined);
assert.equal(m.sessionOf(null, 5), undefined);
console.log("ok");
```

Run: `node $SCRATCH/render/groups.mjs internal/web/static/js/fleet.mjs`
Expected: `ok`. (If Node cannot import `fleet.mjs` because a module it imports touches the DOM at load, copy the three functions and `groupSessions`/`shownWorkspaces`/`allSessions` into the script instead and say so.)

- [ ] **Step 4: Create `js/chrome.mjs`**

Create `internal/web/static/js/chrome.mjs`:

```js
// The app's pinned header (spec 2026-11-01): the status row on every screen
// with a snapshot, then the screen's context row (its Head in screens.mjs),
// then a session's tabs. app.mjs draws the header; the rows here only draw
// what they are given.
import { html } from "../lib/preact-htm.mjs";
import { glyphOf } from "./skin.mjs";

// FLEET_BACK is the way back every screen but the fleet offers.
export const FLEET_BACK = { href: "#", label: "fleet" };

// PILL is the status row's word for the link (app.mjs's link).
const PILL = { live: "LIVE", connecting: "CONNECTING", restarting: "RESTARTING", offline: "OFFLINE" };

// COUNTED are the groups the status row counts, in its order, with the words
// a count's label reads.
const COUNTED = [
  { key: "needs_you", words: "need you" },
  { key: "your_turn", words: "your turn" },
  { key: "working", words: "working" },
];

// clock is the time of day of a ms timestamp, as the stale bar gave it.
function clock(ms) {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// StatusRow is the header's first row: the link's pill, loom@host (with the
// last update's time while offline) and a count per group; a count opens the
// group's next session (onJump). While not live the counts are the cached
// snapshot's, drawn faint and still buttons; a zero is no button.
export function StatusRow({ link, host, updated, counts, onJump }) {
  const state = PILL[link] ? link : "offline";
  const live = state === "live";
  const when = state === "offline" && updated ? " · " + clock(updated) : "";
  return html`<div class=${"sc-status-row" + (state === "offline" ? " sc-off" : "")}>
    <span class=${"sc-pill sc-pill-" + state} aria-live="polite">${PILL[state]}</span>
    <span class="sc-host">${host ? html`<span class="sc-dim">loom@</span>${host}` : "loom"}${when}</span>
    ${COUNTED.map((g) => {
      const n = counts[g.key] || 0;
      const cls = "sc-count sc-count-" + g.key + (live ? "" : " sc-faint");
      const inner = html`<span aria-hidden="true">${glyphOf(g.key)}</span>${n}`;
      if (n === 0) return html`<span key=${g.key} class=${cls + " sc-nil"}>${inner}</span>`;
      return html`<button key=${g.key} class=${cls} type="button" aria-label=${n + " " + g.words + ": open the next"} onClick=${() => onJump(g.key)}>${inner}</button>`;
    })}
  </div>`;
}

// ContextRow is the header's second row: an optional way back, the screen's
// title as its h1 on one line, and the screen's actions (children).
export function ContextRow({ back, title, children }) {
  return html`<div class="sc-context">
    ${back ? html`<a class="sc-back" href=${back.href}>‹ ${back.label}</a>` : null}
    <h1 class="sc-h1">${title}</h1>
    ${children}
  </div>`;
}
```

- [ ] **Step 5: Give each screen its head**

Replace `internal/web/static/js/screens.mjs` with:

```js
// The screens app.mjs renders by route name: each a Screen (the body, in
// <main>) and a Head (its context row and, for a session, its tabs, in the
// header under the status row), both with the props {snap, stale, link,
// route, go, act, toast, pending}. Pairing is app.mjs's own screen.
import { Fleet, FleetHead } from "./fleet.mjs";
import { Session, SessionHead } from "./session.mjs";
import { NewSession, NewHead } from "./newsession.mjs";
import { Settings, SettingsHead } from "./settings.mjs";
import { Turns } from "./turns.mjs";
import { Changes } from "./changes.mjs";

export const screens = {
  fleet: { Screen: Fleet, Head: FleetHead },
  session: { Screen: Session, Head: SessionHead },
  new: { Screen: NewSession, Head: NewHead },
  settings: { Screen: Settings, Head: SettingsHead },
  turns: { Screen: Turns, Head: SessionHead },
  changes: { Screen: Changes, Head: SessionHead },
};
```

In `internal/web/static/js/fleet.mjs`:

1. Change the import of `skin.mjs` and add `chrome.mjs`:
   ```js
   import { glyphOf, GearIcon } from "./skin.mjs";
   import { ContextRow } from "./chrome.mjs";
   ```
   (the first line is unchanged; add the second under it).
2. Delete the `LINK` constant and its comment (`// LINK is the status line's word …` and `const LINK = { … };`).
3. Change `export function Fleet({ snap, stale, link, go }) {` to `export function Fleet({ snap }) {`.
4. Delete the fleet's header:
   ```js
      <header class="sc-header">
        <h1 class="sc-h1"><span class="sc-dim sc-light">loom@</span>${snap.host || "loom"}</h1>
        <a class="sc-icon" href="#settings" aria-label="Settings"><${GearIcon} /></a>
        <button class="sc-btn sc-primary" type="button" disabled=${stale} onClick=${() => go("#new")}>+ new</button>
      </header>
   ```
5. Delete the status line:
   ```js
      <footer class="sc-status">
        <span class=${"sc-link sc-link-" + (LINK[link] ? link : "offline")}>${LINK[link] || LINK.offline}</span>
        <span>${snap.host}</span>
        <span class="sc-sep" aria-hidden="true">│</span>
        <span>${plural(listed, "session")}</span>
        <span class="sc-fill"></span>
        ${groups.paused.length > 0 ? html`<span class="sc-dim">${groups.paused.length} paused</span>` : null}
      </footer>
   ```
6. After the `Fleet` function, add:
   ```js
   // FleetHead is the fleet's context row: its title, the gear and + new
   // (off while the stream is down).
   export function FleetHead({ stale, go }) {
     return html`<${ContextRow} title="Fleet">
       <a class="sc-icon" href="#settings" aria-label="Settings"><${GearIcon} /></a>
       <button class="sc-btn sc-primary" type="button" disabled=${stale} onClick=${() => go("#new")}>+ new</button>
     <//>`;
   }
   ```

In `internal/web/static/js/session.mjs`:

1. Change `import { allSessions, ago, markSeen } from "./fleet.mjs";` to `import { allSessions, ago, markSeen, sessionOf } from "./fleet.mjs";` and add `import { ContextRow, FLEET_BACK } from "./chrome.mjs";` under it.
2. Change `const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;` to `const s = sessionOf(snap, id);`.
3. In the `if (!s)` branch, delete the line `<nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>`.
4. In the main return, delete:
   ```js
      <nav class="sc-topbar" aria-label="Session">
        <a class="sc-back" href="#">‹ fleet</a>
        <span class="sc-fill"></span>
        ${s.remote_url && !waiting
          ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
          : null}
      </nav>
      <h1 class="sc-h1 sc-wrap">${s.title}</h1>
   ```
5. After the `Session` function, add:
   ```js
   // SessionHead is the context row of a session's views (session,
   // conversation, changes): back to the fleet, its title, and the way to
   // Claude while the session doesn't wait on you (the needs-you box has its
   // own button).
   export function SessionHead({ snap, route, pending }) {
     const s = sessionOf(snap, route.id);
     if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
     return html`<${ContextRow} back=${FLEET_BACK} title=${s.title}>
       ${s.remote_url && s.group !== "needs_you"
         ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
         : null}
     <//>`;
   }
   ```

In `internal/web/static/js/turns.mjs`:

1. Change `import { allSessions, ago } from "./fleet.mjs";` to `import { sessionOf, ago } from "./fleet.mjs";`.
2. Change `const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;` to `const s = sessionOf(snap, id);`.
3. Delete both `<nav class="sc-topbar" …>…</nav>` lines (the `if (!s)` branch's and the main return's `<nav class="sc-topbar" aria-label="Conversation"><a class="sc-back" href=${"#s/" + s.id}>‹ ${s.title}</a></nav>`).
4. Delete `<h1 class="sc-h1">Conversation</h1>`.

In `internal/web/static/js/changes.mjs`:

1. Change `import { allSessions } from "./fleet.mjs";` to `import { sessionOf } from "./fleet.mjs";`.
2. Change `const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;` to `const s = sessionOf(snap, id);`.
3. Delete both `<nav class="sc-topbar" …>…</nav>` lines and `<h1 class="sc-h1">Changes</h1>`.

In `internal/web/static/js/settings.mjs`:

1. Add `import { ContextRow, FLEET_BACK } from "./chrome.mjs";` under the `fleet.mjs` import.
2. Delete `<nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>` and `<h1 class="sc-h1 sc-wrap">Settings</h1>`.
3. After the `Settings` function, add:
   ```js
   // SettingsHead is the settings screen's context row.
   export function SettingsHead() {
     return html`<${ContextRow} back=${FLEET_BACK} title="Settings" />`;
   }
   ```

In `internal/web/static/js/newsession.mjs`:

1. Add `import { ContextRow, FLEET_BACK } from "./chrome.mjs";` under the `sheets.mjs` import.
2. Delete `<nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>` and `<h1 class="sc-h1 sc-wrap">New session</h1>`.
3. After the `NewSession` function, add:
   ```js
   // NewHead is the new-session screen's context row.
   export function NewHead() {
     return html`<${ContextRow} back=${FLEET_BACK} title="New session" />`;
   }
   ```

- [ ] **Step 6: Draw the header from `app.mjs`**

In `internal/web/static/js/app.mjs`:

1. Change the file's first comment line `// The app: routes, the live snapshot, the stale bar and the toasts. The` to `// The app: routes, the live snapshot, the header and the toasts. The` and add, after its last line (`// they draw, as the props {snap, stale, link, route, go, act, toast, pending}.`), the line `// The header's rows are chrome.mjs's; each screen's context row is its Head.`
2. Change `import { allSessions } from "./fleet.mjs";` to `import { allSessions, fleetGroups, nextInGroup } from "./fleet.mjs";` and add `import { StatusRow } from "./chrome.mjs";` under it.
3. Delete `staleText` and its comment (from `// staleText is the stale bar's words:` to the function's closing `}`).
4. After the `onPaired` callback (`const onPaired = useCallback(() => { … }, []);`), add:
   ```js
   // jump opens the next session of a group the status row counts, after the
   // one on screen, in the group's order as it stands at the tap.
   const jump = useCallback((group) => {
     const next = nextInGroup(fleetGroups(snapRef.current), group, routeRef.current.id);
     if (next) go("#s/" + next.id);
   }, []);
   ```
5. Replace everything from `  let body;` to the end of the `App` function's `return` (the closing `` `; `` before `}`) with:
   ```js
     // link is the stream's state for the status row: connecting until the
     // first snapshot or a failed connect, restarting after a bye.
     const link = !stale ? "live" : restarting ? "restarting" : offline ? "offline" : "connecting";
     const entry = screens[route.name];
     const chromed = Boolean(snap) && route.name !== "pair";
     const props = { snap, stale, link, route, go, act, toast, pending };

     let body;
     if (route.name === "pair") {
       body = html`<${Pair} onPaired=${onPaired} />`;
     } else if (!snap) {
       body = html`<div class="splash">${restarting ? "loom is restarting…" : offline ? "Can't reach loom. Retrying…" : "Connecting…"}</div>`;
     } else {
       body = entry
         ? html`<${entry.Screen} key=${route.name + (route.id === undefined ? "" : route.id)} ...${props} />`
         : html`<div class="splash">Nothing here yet.</div>`;
     }

     // The toasts hang from the header's bottom edge; with no header they
     // come down from the top.
     const toastList = html`<div class="toasts" aria-live="polite">
       ${toasts.map(
         (t) => html`<div key=${t.id} class=${"toast" + (t.error ? " error" : "")} role=${t.error ? "alert" : "status"} onClick=${() => dismissToast(t.id)}>${t.text}</div>`,
       )}
     </div>`;

     let header = null;
     if (chromed) {
       const groups = fleetGroups(snap);
       const counts = { needs_you: groups.needs_you.length, your_turn: groups.your_turn.length, working: groups.working.length };
       header = html`<header class="sc-chrome">
         <div class="sc-chrome-in">
           <${StatusRow} link=${link} host=${snap.host} updated=${lastUpdated} counts=${counts} onJump=${jump} />
           ${entry ? html`<${entry.Head} ...${props} />` : null}
         </div>
         ${toastList}
       </header>`;
     }

     return html`
       <div class=${"shell" + (chromed ? " sc-chromed" : "")}>
         ${header}
         <main>${body}</main>
         ${chromed ? null : toastList}
       </div>
     `;
   ```

- [ ] **Step 7: The stylesheet**

In `internal/web/static/app.css`:

1. Change
   ```css
   .shell > main {
     flex: 1;
   ```
   to
   ```css
   .shell > main {
     flex: 1;
     display: flex;
     flex-direction: column;
   ```
2. Delete the whole `.stale-bar { … }` rule.
3. Change the toasts' comment `/* Toasts come down from the top: the fleet's status line and the session's\n   reply box hold the bottom. */` to:
   ```css
   /* Toasts come down from the top while there is no header; under one they
      hang from its bottom edge (.sc-chrome > .toasts). The reply box holds the
      bottom. */
   ```
4. In the `.sc-group-title,\n.sc-rule` rule, change `scroll-margin-top: 48px;` to `scroll-margin-top: calc(env(safe-area-inset-top) + 88px);` and put above the rule the comment `/* scroll-margin-top clears the pinned header (the fleet's status and context\n   rows, 78px, plus room) when a tile jumps to a group. */`.
5. Delete the lines `.sc-header { display: flex; align-items: center; gap: 4px; }` and `.sc-h1.sc-wrap { margin: 4px 0 6px; white-space: normal; overflow: visible; }`.
6. Delete `.sc-fleet { padding-bottom: calc(56px + env(safe-area-inset-bottom)); }`, the whole `.sc-status { … }` rule, and the lines `.sc-link { … }`, `.sc-link-live { … }`, `.sc-link-offline { … }` and `.sc-sep { … }`.
7. Delete `.sc-topbar { display: flex; align-items: center; margin: 0 -12px; }`.
8. Replace
   ```css
   /* The session is a full-height column, so on a short page the reply box sits
      at the bottom (margin-top: auto) and not under the actions; it reaches into
      the shell's bottom inset, since it pads for the home indicator itself. */
   .sc-session,
   .sc-turns {
     display: flex;
     flex-direction: column;
     min-height: calc(100dvh - env(safe-area-inset-top) - env(safe-area-inset-bottom));
     padding-bottom: 0;
   }
   .sc-session > .sc-h1,
   .sc-turns > .sc-h1 { flex: none; }
   ```
   with
   ```css
   /* A session's views fill main's column under the header (flex: 1, never a
      100dvh height, which would add the header's to every page), so on a short
      page the reply box sits at the bottom (margin-top: auto) and not under the
      actions; it reaches into the shell's bottom inset, since it pads for the
      home indicator itself. */
   .sc-session,
   .sc-turns {
     flex: 1;
     display: flex;
     flex-direction: column;
     padding-bottom: 0;
   }
   ```
9. Before `/* ---- the fleet ---- */`, add:
   ```css
   /* ---- the header ---- */
   /* The app's pinned header: the status row, the screen's context row and a
      session's tabs. It pads the top inset itself, so it fills behind the iOS
      status bar, and the shell then drops its own (.sc-chromed). */
   .shell.sc-chromed { padding-top: 0; }
   .sc-chrome {
     position: sticky;
     top: 0;
     z-index: 30;
     flex: none;
     padding-top: env(safe-area-inset-top);
     background: var(--bg);
     border-bottom: 1px solid var(--rule);
   }
   .sc-chrome-in { max-width: 40rem; margin: 0 auto; }
   .sc-chrome > .toasts { position: absolute; top: calc(100% + 8px); }
   .sc-status-row {
     display: flex;
     align-items: center;
     gap: 4px;
     min-height: 32px;
     padding: 0 6px 0 16px;
     font-size: 12px;
     background: var(--panel);
     border-bottom: 1px solid var(--rule);
   }
   .sc-status-row.sc-off { border-bottom-color: var(--error-border); }
   .sc-pill { flex: none; margin-right: 4px; padding: 0 6px; font-weight: 700; color: var(--bg); background: var(--dim); }
   .sc-pill-live { background: var(--ok); }
   .sc-pill-offline { background: var(--error); }
   .sc-host { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
   /* A count: a 40×32 target, coloured as its group; faint while not live,
      and at zero, when it is no button. */
   .sc-count {
     flex: none;
     display: inline-flex;
     align-items: center;
     justify-content: center;
     gap: 2px;
     min-width: 40px;
     min-height: 32px;
     padding: 0 4px;
     border: 0;
     background: none;
     font: inherit;
     font-weight: 700;
     color: var(--fg);
   }
   .sc-count-needs_you { color: var(--attention); }
   .sc-count-your_turn { color: var(--accent); }
   .sc-count.sc-faint,
   .sc-count.sc-nil { color: var(--faint); font-weight: 400; }
   .sc-context { display: flex; align-items: center; gap: 4px; min-height: 44px; padding: 0 16px; }
   .sc-context .sc-back { margin-left: -12px; }
   .sc-context .sc-claude { margin-right: -12px; }

   ```

- [ ] **Step 8: Check the modules and the static tests**

Run: `for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done; CGO_ENABLED=0 go test ./internal/web/`
Expected: no `FAIL` line; `ok  github.com/aidan-bailey/loom/internal/web`.
Also run: `grep -rn "sc-topbar\|sc-header\|stale-bar\|sc-status\b\|sc-link\|sc-wrap\|staleText\|LINK\b" internal/web/static`
Expected: nothing (every use of the removed classes and names is gone).

- [ ] **Step 9: See the screens**

Run (from `$WT`):
```bash
cd $WT
python3 $SCRATCH/render/serve.py internal/web/static 8790 toast &
for h in "" "#s/1" "#s/1/turns" "#s/1/diff" "#s/2" "#s/7" "#settings" "#new"; do bash $SCRATCH/render/shot.sh "$SCRATCH/render/t1-${h//[#\/]/_}.png" "$h"; done
kill %1
python3 $SCRATCH/render/serve.py internal/web/static 8790 bye & bash $SCRATCH/render/shot.sh $SCRATCH/render/t1-bye.png "#s/1"; kill %1
python3 $SCRATCH/render/serve.py internal/web/static 8790 drop & bash $SCRATCH/render/shot.sh $SCRATCH/render/t1-drop.png ""; kill %1
P=$(mktemp -d)
python3 $SCRATCH/render/serve.py internal/web/static 8790 live & bash $SCRATCH/render/shot.sh /dev/null "" 390 "$P"; kill %1
python3 $SCRATCH/render/serve.py internal/web/static 8790 slow & bash $SCRATCH/render/shot.sh $SCRATCH/render/t1-slow.png "" 390 "$P"; kill %1
python3 $SCRATCH/render/serve.py internal/web/static 8790 live a-very-long-hostname.example.org & bash $SCRATCH/render/shot.sh $SCRATCH/render/t1-320.png "#s/7" 320; kill %1
```
Read each PNG and check, with no `Uncaught` line printed:
- every screen opens with the status row `LIVE loom@fresco ●2 ◆1 ◐4` (fleet groups: needs you 2 and 4; your turn 7; working 1, 3, 6 — paused 5 is in no count), then the context row: `Fleet ⚙ + new` on the fleet, `‹ fleet <title>` elsewhere, `claude ↗` on `#s/1` and not on `#s/2` (needs you);
- no bottom status line on the fleet; the session's body starts at its subline, with no h1;
- the toast `Sent.` hangs just below the header, not over the status row;
- `t1-bye.png` says `RESTARTING`, `t1-drop.png` `OFFLINE loom@fresco · <time>` with a red rule under the row, `t1-slow.png` `CONNECTING` over the cached fleet (if the profile kept no snapshot, it shows the splash: note it and move on, since the pill is the same code as the others), and the counts are faint in all three;
- `t1-320.png`: the long host truncates and all three counts show; the long title truncates in one line.

- [ ] **Step 10: The docs and the spec**

In `USAGE.md`, in "Use Loom from Your Phone", insert before the bullet that starts `- **The fleet.** Tiles count`:

```markdown
- **The header.** Every screen opens under a pinned header. Its first row
  says whether the phone is live (`LIVE`, `CONNECTING`, `RESTARTING`, or
  `OFFLINE` with when it last heard from loom), which machine it shows
  (`loom@<host>`), and how many sessions need you (●), are your turn (◆)
  and are working (◐) in the workspaces it shows. Tap a count to open that
  group's next session, and again for the one after. The second row is the
  screen's own: the way back, its title, its actions.
```

In `internal/web/README.md`, in the skin paragraph, replace `The fleet lists the `shown` workspaces under count tiles and the usage boxes (`js/usage.mjs` formats a window), with a status line at the bottom;` with:

```markdown
Every screen with a snapshot opens under the app's pinned header, which `app.mjs` draws from `js/chrome.mjs`'s rows: the status row (the link's pill, `loom@host`, and the counts of Needs you, Your turn and Working over the shown workspaces, each opening its group's next session: `fleetGroups` and `nextInGroup` in `js/fleet.mjs`), then the screen's context row, its `Head` in `js/screens.mjs`; the toasts hang from its bottom edge. The fleet lists the `shown` workspaces under count tiles and the usage boxes (`js/usage.mjs` formats a window);
```

In `docs/claude/web-layer.md`, append to the "Changing the app" list (after the bullet about a flex container dropping a leading space):

```markdown
- The header is pinned (`.sc-chrome`, sticky), so an in-page jump (`scrollIntoView`, an anchor) lands under it unless the target's `scroll-margin-top` clears it, as `.sc-group-title`'s does. A screen that fills the space under the header takes `flex: 1` in `main`'s column; a `100dvh` height adds the header's height to every page, which then always scrolls.
```

In `docs/superpowers/specs/2026-11-01-pwa-header-footer-design.md`:
- in "The status row", change `**The counts** are `fleetCounts(snap)`:` to `**The counts** are the lengths of `fleetGroups(snap)`'s groups:`, and `**A tap** goes to `#s/<id>` of `nextInGroup(snap, group, currentId)`:` to `**A tap** goes to `#s/<id>` of `nextInGroup(fleetGroups(snap), group, currentId)`:`;
- in the Architecture table, change the `js/chrome.mjs (new)` row to `` `StatusRow`, `ContextRow`, `SessionTabs`, `FLEET_BACK`; it imports only `skin.mjs` `` and add to the `js/fleet.mjs` row `; adds `fleetGroups(snap)`, `nextInGroup(groups, group, currentId)` and `sessionOf(snap, id)`, pure (here rather than in `chrome.mjs`, which `fleet.mjs` imports)`;
- in the `app.css` row, after `.shell` drops its top inset`, add ` (only while the header is drawn: `.sc-chromed`); `main` becomes a flex column and a session's views fill it with `flex: 1` instead of a `100dvh` min-height, which the header would add to`;
- in "Testing", change `` `nextInGroup` and `fleetCounts` get a throwaway Node script that imports `chrome.mjs` `` to `` `nextInGroup`, `fleetGroups` and `sessionOf` get a throwaway Node script that imports `fleet.mjs` ``.

- [ ] **Step 11: Format check and commit**

Run: `git status --short` — expected: only the files this task lists, no scratch file.
Run: `CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/`
Expected: `ok` for both.

```bash
git add -- internal/web/static/js/chrome.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs internal/web/static/js/fleet.mjs internal/web/static/js/session.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs internal/web/static/js/settings.mjs internal/web/static/js/newsession.mjs internal/web/static/app.css USAGE.md internal/web/README.md docs/claude/web-layer.md docs/superpowers/specs/2026-11-01-pwa-header-footer-design.md
git commit -m "feat(web): one pinned header for every screen of the phone app

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/web/static/js/chrome.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs internal/web/static/js/fleet.mjs internal/web/static/js/session.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs internal/web/static/js/settings.mjs internal/web/static/js/newsession.mjs internal/web/static/app.css USAGE.md internal/web/README.md docs/claude/web-layer.md docs/superpowers/specs/2026-11-01-pwa-header-footer-design.md
```

---

### Task 2: The tabs, and the reply box on every tab

**Files:**
- Modify: `internal/web/static/js/chrome.mjs`, `reply.mjs`, `session.mjs`, `turns.mjs`, `changes.mjs`
- Modify: `internal/web/static/app.css`
- Modify: `USAGE.md`, `internal/web/README.md`

- [ ] **Step 1: `SessionTabs`**

In `internal/web/static/js/chrome.mjs`, append:

```js
// SessionTabs are a session's views under its context row: the session, its
// conversation (conversation: a Claude session) and its changes (changes:
// while its diff can be read, with its line counts). A tab shows when it
// applies or is the current one, so a session that pauses on its changes
// keeps the tab under the user; a lone session tab draws no row.
export function SessionTabs({ s, current, conversation, changes }) {
  const base = "#s/" + s.id;
  const d = s.diff || {};
  const tabs = [{ key: "session", label: "session", href: base }];
  if (conversation || current === "turns") tabs.push({ key: "turns", label: "conversation", href: base + "/turns" });
  if (changes || current === "changes") {
    tabs.push({ key: "changes", label: "changes", href: base + "/diff", diff: d.added || d.removed ? d : null });
  }
  if (tabs.length === 1) return null;
  return html`<nav class="sc-tabs" aria-label="Session views">
    ${tabs.map(
      (t) => html`<a key=${t.key} class=${"sc-tab" + (t.key === current ? " sc-tab-on" : "")} href=${t.href} aria-current=${t.key === current ? "page" : undefined}>
        ${t.label}
        ${t.diff ? html`<span class="sc-tab-diff"><span class="sc-add">+${t.diff.added || 0}</span> <span class="sc-del">−${t.diff.removed || 0}</span></span>` : null}
      </a>`,
    )}
  </nav>`;
}
```

In `internal/web/static/js/session.mjs`:

1. Change `import { ContextRow, FLEET_BACK } from "./chrome.mjs";` to `import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";`.
2. Replace `SessionHead`'s second `return` (the one with `title=${s.title}`) with:
   ```js
     return html`
       <${ContextRow} back=${FLEET_BACK} title=${s.title}>
         ${s.remote_url && s.group !== "needs_you"
           ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
           : null}
       <//>
       <${SessionTabs} s=${s} current=${route.name} conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
     `;
   ```
   and change its comment's first line to `// SessionHead is the context row and the tabs of a session's views (session,`.

- [ ] **Step 2: `useRun` and the drafts in `reply.mjs`**

Replace `internal/web/static/js/reply.mjs` with:

```js
// The reply box a session's views end in: the quick replies while it is
// empty, a text box and Send; and useRun, their shared runner. run is the
// screen's own, so the box and the screen's actions are off together while
// either is in flight.
import { html, useState } from "../lib/preact-htm.mjs";
import { utf8Len } from "./sheets.mjs";

export const MAX_REPLY = 64 * 1024;

// drafts are the unsent replies by session id: a tab switch remounts the
// screen, and what was typed stays. They last as long as the page.
const drafts = new Map();

// useRun posts an action on session id, holding busy while it runs, and says
// when its result will follow on the stream (a 202).
export function useRun(act, toast, id) {
  const [busy, setBusy] = useState(false);
  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", "/api/v1/sessions/" + id + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }
  return { busy, run };
}

export function ReplyBox({ snap, off, run, id }) {
  const [text, setShown] = useState(() => drafts.get(id) || "");
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;
  const replies = snap.quick_replies || [];

  function setText(v) {
    setShown(v);
    if (v) drafts.set(id, v);
    else drafts.delete(id);
  }

  async function send() {
    const r = await run("send", { text: reply });
    if (r.ok && r.status !== 202) setText("");
  }

  return html`<form class="sc-reply" onSubmit=${(e) => { e.preventDefault(); if (!off && reply && !tooLong) send(); }}>
    ${text === "" && replies.length > 0
      ? html`<div class="sc-quick" role="group" aria-label="Quick replies">
          ${replies.map(
            (q) => html`<button key=${q} class="sc-chip-btn" type="button" disabled=${off} onClick=${() => setText(q)}><span>${q}</span></button>`,
          )}
        </div>`
      : null}
    <div class="sc-reply-row">
      <textarea
        rows="2"
        placeholder="reply, or /command…"
        aria-label="Reply"
        value=${text}
        onInput=${(e) => setText(e.target.value)}
      ></textarea>
      <button class="sc-btn sc-primary" type="submit" disabled=${off || !reply || tooLong}>send</button>
    </div>
    ${tooLong ? html`<div class="sc-small sc-err">Too long: a reply is at most 64 KiB.</div>` : null}
  </form>`;
}
```

- [ ] **Step 3: The three views use `useRun`; changes gets the reply box**

In `internal/web/static/js/session.mjs`:

1. Change `import { ReplyBox } from "./reply.mjs";` to `import { ReplyBox, useRun } from "./reply.mjs";`.
2. Change `const [busy, setBusy] = useState(false);` to `const { busy, run } = useRun(act, toast, id);`.
3. Delete `const base = "/api/v1/sessions/" + s.id;` and the local `async function run(action, body) { … }` (from `async function run` to its closing `}`, which holds `setBusy(true)` and `if (r.status === 202) toast(…)`).
4. Change `<${ReplyBox} snap=${snap} off=${off} run=${run} />` to `<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`.

In `internal/web/static/js/turns.mjs`:

1. Change `import { ReplyBox } from "./reply.mjs";` to `import { ReplyBox, useRun } from "./reply.mjs";`.
2. Change `const [busy, setBusy] = useState(false);` to `const { busy, run } = useRun(act, toast, id);`.
3. Delete the local `async function run(action, body) { … }` (it posts to `"/api/v1/sessions/" + s.id + "/" + action`).
4. Change `<${ReplyBox} snap=${snap} off=${off} run=${run} />` to `<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`.

In `internal/web/static/js/changes.mjs`:

1. Change the file's first comment to:
   ```js
   // The changes screen: a session's worktree diff against its base commit,
   // file by file (GET /api/v1/sessions/{id}/diff); a file opens to its hunks.
   // Every line is drawn as text. It ends in the reply box, as the session's
   // other views do.
   ```
2. Change `import { SubLine } from "./session.mjs";` to `import { SubLine, actionsFor } from "./session.mjs";` and add `import { ReplyBox, useRun } from "./reply.mjs";` under it.
3. Change `export function Changes({ snap, route }) {` to `export function Changes({ snap, stale, route, act, toast }) {`, and add as the function's third line (after `const s = sessionOf(snap, id);`) `const { busy, run } = useRun(act, toast, id);`.
4. After `const since = patch && patch.base ? patch.base.slice(0, 7) : "";` add:
   ```js
     const can = actionsFor(s);
     const off = stale || busy;
   ```
5. In the main return, after the `${patch && err ? … : null}` block and before `</section>`, add:
   ```js
         ${can.send
           ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`
           : s.group === "paused"
             ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
             : null}
   ```

- [ ] **Step 4: The stylesheet**

In `internal/web/static/app.css`:

1. Change the selector list `.sc-session,\n.sc-turns {` (the flex-column rule) to `.sc-session,\n.sc-turns,\n.sc-changes {`.
2. After `.sc-context .sc-claude { margin-right: -12px; }` add:
   ```css
   /* A session's views as tabs: a 40px target each, the current one
      underlined; they shrink to their words on a narrow phone. */
   .sc-tabs { display: flex; border-top: 1px solid var(--rule); }
   .sc-tab {
     flex: 1 1 auto;
     display: flex;
     align-items: center;
     justify-content: center;
     gap: 6px;
     min-height: 40px;
     padding: 0 6px;
     font-size: 12px;
     color: var(--dim);
     text-decoration: none;
     white-space: nowrap;
   }
   .sc-tab-on { color: var(--title); box-shadow: inset 0 -2px var(--accent); }
   .sc-tab-diff { font-size: 11px; }
   ```

- [ ] **Step 5: Check the modules and the static tests**

Run: `for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done; CGO_ENABLED=0 go test ./internal/web/`
Expected: no `FAIL` line; `ok`.
Run: `grep -n "setBusy" internal/web/static/js/session.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs`
Expected: nothing.

- [ ] **Step 6: See the tabs**

Run (from `$WT`):
```bash
python3 $SCRATCH/render/serve.py internal/web/static 8790 live &
for h in "#s/1" "#s/1/turns" "#s/1/diff" "#s/3" "#s/5" "#s/7"; do bash $SCRATCH/render/shot.sh "$SCRATCH/render/t2-${h//[#\/]/_}.png" "$h"; done
bash $SCRATCH/render/shot.sh $SCRATCH/render/t2-320.png "#s/1/diff" 320
kill %1
```
Read each PNG and check, with no `Uncaught` line:
- `#s/1` (Claude, Running, +212 −40): tabs `session │ conversation │ changes +212 −40`, the current one underlined; the reply box at the bottom of all three tabs, the changes tab included;
- `#s/3` (not Claude, Loading: no diff): no tab row;
- `#s/5` (paused, not Claude): no tab row; "Resume the session to reply.";
- `#s/7` (Claude, Ready, no diff counts): tabs `session │ conversation │ changes` with no counts;
- `t2-320.png`: the three tabs fit in one row at 320 px.

The draft surviving a tab switch, and a count's cycle, need taps: they go on the iPhone checklist below.

- [ ] **Step 7: The docs**

In `USAGE.md`, replace the bullet that starts `- **The conversation and the changes.** On a Claude session, **conversation ›**` (through `the changes are offered only while the session runs.`) with:

```markdown
- **The conversation and the changes.** A session's screen has tabs under
  its title: **session**, **conversation** on a Claude session (also
  **conversation ›** beside its last message) and **changes** while the
  session runs (also the **Changes** tile), with its line counts. The
  conversation is its recent turns: each prompt (yours, a background
  task's, or a subagent's report), Claude's replies as it formatted them,
  and its tool calls as one-line steps folded under a count (tap to open;
  the newest turn's run is open while Claude works, its running step
  marked). **↑ earlier turns** loads more. The changes are the worktree's
  diff against its base commit, file by file: tap a file for its hunks.
  Both refresh while the session works; neither shows tool output. Every
  tab ends in the reply box, and a reply you started stays as you switch
  tabs.
```

In `internal/web/README.md`, replace `Both session screens end in `js/reply.mjs`'s reply box.` with `A session's three views (session, conversation, changes) are tabs under its context row (`SessionTabs`), and each ends in `js/reply.mjs`'s reply box, which keeps a draft per session across them; `useRun` is their shared runner.`

- [ ] **Step 8: Commit**

Run: `git status --short` — expected: only this task's files.
Run: `CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/` — expected: `ok` for both.

```bash
git add -- internal/web/static/js/chrome.mjs internal/web/static/js/reply.mjs internal/web/static/js/session.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs internal/web/static/app.css USAGE.md internal/web/README.md
git commit -m "feat(web): a session's views as tabs, each ending in the reply box

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/web/static/js/chrome.mjs internal/web/static/js/reply.mjs internal/web/static/js/session.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs internal/web/static/app.css USAGE.md internal/web/README.md
```

---

## The iPhone checklist (the owner's, after both tasks)

Through a dev build (`loom-dev` skill), never `./loom` in a loom pane:

1. Standalone (Home Screen): the header fills behind the status bar; scrolling a long conversation keeps all three rows pinned.
2. Open the reply box on each tab: the keyboard and the header together; note if iOS scrolls the header away (accepted by the spec, recorded only).
3. Tap ● twice with two sessions waiting: the first opens the longest wait, the second the other; a third wraps.
4. Type a reply on the session tab, switch to conversation and back: the text is still there; send it: the box empties on every tab.
5. On the fleet, tap the Needs you tile: its group's heading lands below the header.
6. Turn off the tunnel or the daemon: the pill goes `OFFLINE · <time>` and the counts fade; a count still navigates.
7. A toast (send a reply) hangs below the header, on a two-row and a three-row header.
