# Loom PWA: the brainstorm companion's mockups on the phone

**Date:** 2026-11-04
**Status:** Approved design. Brainstormed with the user; the layout was
chosen in the visual companion (option C), and the sketch below is the
record.

## Problem

The brainstorming skill's visual companion shows mockups, diagrams and
A/B/C choices as HTML pages. It is a Node server (`server.cjs`) that the
skill starts on `127.0.0.1` at a random port (49152–65535), serving the
newest file of its `content/` dir at `/`. Away from the desk the owner runs
loom from the phone app, which reaches only the daemon, through the
tunnel (`loom.aidanbailey.me` → `127.0.0.1:5666`), so a brainstorm's
visual question can't be answered from the phone.

This spec adds a **mockup** tab to a session's views in the app: the page
the session's companion is serving now, drawn in a sandboxed iframe, with
the reply box below it, so a visual question is answered from the phone the
way a text one is. The phone views only; the answer is the typed reply.

## Findings

What the companion does, read from superpowers 5.1.0
(`skills/brainstorming/scripts/`):

- `start-server.sh --project-dir <dir>` puts a session dir at
  `<dir>/.superpowers/brainstorm/<pid>-<epoch>/` with `content/` (the
  screens, one file each, never reused) and `state/` (`server-info`,
  `events`). The skill passes the project root, which in a loom session is
  its worktree. Without `--project-dir` the files go to
  `/tmp/brainstorm-<id>/`, which no session can be tied to.
- `state/server-info` is one JSON line:
  `{"type":"server-started","port":52599,"host":"127.0.0.1","url":…,"screen_dir":…,"state_dir":…}`.
  On shutdown (owner process gone, or 30 minutes with no request) the server
  deletes `server-info` and writes `state/server-stopped`. A killed server
  leaves `server-info` behind, and the skill's own `stop-server.sh` kills
  it (SIGTERM, which `server.cjs` doesn't handle): it leaves `server-info`
  too, and deletes `state/server.pid`, which `start-server.sh` writes in
  both its modes. (Found running the real companion during the build.)
- `GET /` serves the newest `content/*.html` by mtime: a full document as
  is, a fragment wrapped in `frame-template.html`. Either way it injects
  `helper.js` as an inline `<script>`, which defines `window.brainstorm`,
  `toggleSelect` and a WebSocket to `ws://<location.host>` that reloads the
  page on a new screen and sends clicks to `state/events`. `GET /files/<name>`
  serves a file from `content/`. Every request counts as activity.
- Screens use inline `onclick` handlers and inline `<style>`. The frame
  template has no viewport meta and a `max-width: 700px` breakpoint that
  stacks its split views.
- When the conversation moves back to text, the skill pushes a waiting
  screen named `waiting.html` (`waiting-2.html`, …): "Continuing in
  terminal…".
- After pushing a screen, Claude ends its turn, which is when the waiting
  push reaches the phone.
- This repo ignores `.superpowers/` (`.gitignore`), so loom's
  `git add -N .` diff probe leaves the files out of the index.

What loom has:

- The app's CSP is `default-src 'self'; img-src 'self' data:;
  frame-ancestors 'none'; base-uri 'none'; form-action 'self'`
  (`internal/web/server.go`), set by `guard` on every answer. A companion
  page under it loses every handler and style; a companion page on the
  app's origin without it would be same-origin with the app, and its script
  could call `/api/v1` with the device cookie.
- `InstanceView.WorktreePath` names every session's worktree. A read
  route (`…/turns`, `…/diff`) is a Core request whose job runs off the loop
  and whose `Reply` carries the data (`core/reads.go`,
  `internal/web/reads.go`); a patch is capped at 1 MiB
  (`session/git/patch.go`).
- The hook scan (`core/hook_scan.go`) reads each hooked Claude session's
  events every 250 ms in a job; the status a `Stop` moves to lands in the
  same delivery, and the web layer's push trigger sees it in the next
  projection.

## Decisions

| # | Decision |
|---|---|
| 1 | **View, then reply.** The tab shows the page read-only; a tap on an option selects it on the phone only and reaches no `state/events`. The reply box below is the answer. Chosen over tap-to-choose relayed to the companion, and over a tap that also sends the reply. |
| 2 | **The current screen only**: what the companion serves now. The tab appears while a companion runs and goes when it stops; no history of screens, no finished brainstorms read from disk. |
| 3 | **A tab, and the push and the fleet row link to it**: while the screen changed during the session's latest turn ("fresh") and no ask is open, the waiting push and the fleet row open the mockup tab instead of the session view. An open question or permission prompt keeps them on the session view, where it is answered; the tab's dot still shows. |
| 4 | **Layout C**: the page laid out at the phone's width, scrolling inside the tab with the reply box visible, plus a full-screen toggle that hides the header, tabs and reply box. Chosen over fit-to-width alone (A) and a 1024px page scaled down (B). |
| 5 | **The model finds and fetches** (approach 1). The model looks for the companion and publishes `InstanceView.Mockup`; a Core request, `ReadMockup`, fetches the page; the web layer serves it. Chosen over a web layer that proxies the companion itself (it would read the host's disk and loopback ports, which a web layer on another machine, as the cloud direction has it, could not) and over the loom mod reporting each screen written (tied to the skill's habits, and every session needs a relaunch). |
| 6 | **The page may load `https:` scripts, styles, images and fonts**, so CDN fonts and Unsplash images render; those hosts see the phone's IP. |
| 7 | **No `/files/*`**: a screen referencing the companion's own files shows them broken. |

## Design

### Finding the companion (`session/mockup`)

A new pure-reader package, `session/mockup`, beside `session/transcript`:
no tmux, UI or app imports.

```go
// Companion is a running companion found in a worktree.
type Companion struct {
    Port    int       // from server-info, 1–65535
    Screen  string    // newest content/*.html, a base name
    Changed time.Time // its mtime
}

// Find looks in <worktree>/.superpowers/brainstorm/*/ for the newest session
// dir whose state/ holds server-info and no server-stopped, and returns its
// port and newest screen. ok is false on any doubt.
func Find(worktree string) (c Companion, ok bool, err error)
```

- "Newest session dir" is the one whose `server-info` has the latest mtime,
  among those with a `server.pid` and no `server-stopped`.
- A dial that finds nothing listening is `ErrNotRunning`, on which
  `ReadMockup` records no companion, so a killed companion's tab goes when
  it is opened.
- `ok` is false, with nothing guessed, when: there is no
  `.superpowers/brainstorm`; no session dir has a live `server-info`; the
  newest `server-info` doesn't parse, or its port is outside 1–65535;
  `content/` holds no `.html`; or the newest screen's name starts with
  `waiting` (the skill's waiting screen), so a spent question never opens
  the tab. `err` is set only for an I/O error other than not-exist, and is
  logged at debug by the caller.

```go
// Page is the companion's current page.
type Page struct{ HTML string }

// Fetch GETs http://127.0.0.1:<port>/ and returns the page.
func Fetch(ctx context.Context, port int) (Page, error)
```

- The host is fixed: `Fetch` takes only a port. Its client has a 5 s
  timeout, follows no redirect (`CheckRedirect` returns
  `http.ErrUseLastResponse`, and a 3xx is refused), and its transport has
  no proxy.
- It refuses, with an error that names the reason and never the body: a
  status other than 200; a `Content-Type` that isn't `text/html`; a body
  over 1 MiB (`maxPageBytes`, read through a limit of one byte more); and a
  body without the helper's `window.brainstorm` marker. The marker proves
  the companion answered, not a process that took the port a stale
  `server-info` names.

### The model

`InstanceView` gains a field. It is a wire field: no `rpc.Protocol` bump,
`docs/specs/protocol.md` regenerated.

```go
// Mockup is the session's brainstorm companion as the model last found it;
// Screen "" means none.
Mockup MockupView

type MockupView struct {
    Screen  string
    Changed time.Time
    // Fresh: Changed is at or after the start of the session's latest
    // turn, and the session has not entered a new turn since.
    Fresh bool
}
```

The instance keeps its mockup in memory, never in `state.json` (no schema
change), along with `turnStart`: the time it last entered Running from any
status but Prompting, so a permission prompt or a question in the middle of
a turn doesn't split it. `turnStart` is zero until the model sees a turn
start in this process.

**When the model looks.** The hook scan's job, which already reads each
hooked instance's events off the loop, also calls `mockup.Find` on an
instance's worktree when that instance's scan read a parent event that
hands the turn to the user: `Stop`, `PermissionRequest` or
`Notification`. The worktree path is captured on the loop when the job is
built. Any other scan does no extra disk work. `deliverHookScan` applies
the mockup with the status, so both are in one `Sync` and the push trigger
sees them in the same projection. A change of mockup counts as moved (no
`noWake`).

**Fresh.** On applying a found companion: `Fresh` is true when `turnStart`
is non-zero and `Changed` is not before it. Entering a new turn (Running
from any status but Prompting) clears `Fresh`; a later find sets it again
only for a screen changed in that turn. With no companion found, `Mockup`
is cleared.

**`ReadMockup(id InstanceID, req ReqID)`**, a request (no `rpc:` comment
in `core/iface.go`), added by `docs/claude/adding-a-core-method.md`:

- `admit(opReadMockup, …)` first; `precondition` refuses nothing for it,
  since a companion can outlive any status.
- Its job (`spawn`, so the Reply routes to the requester) captures the
  worktree path on the loop, then runs `Find` and, when found, `Fetch` with
  the found port.
- Its result updates `Mockup` as a find at a turn end does (an empty find
  clears it), and answers the `Reply`: with `Reply.Mockup`
  (`{Screen, Changed, HTML}`) on success, or a refusal matching
  `ErrRefused` naming the reason ("no brainstorm companion is running in
  this session's worktree", or `Fetch`'s error). The `Reply`'s own codec
  (`replyJSON`) carries the new field.

### The web layer

- `sessionJSON` gains `mockup: {screen, changed, fresh}`, `null` when
  `Screen` is empty. Additive; no field of `/api/v1` changes.
- **`GET /api/v1/sessions/{id}/mockup`**, in `routeAPI`, wrapped in
  `authed`, answered through `l.read` (`readSession` first, so a workspace
  terminal is 404). A refusal is 409 and no answer within `replyWait` 504,
  each answered as a small HTML page under the same CSP that says why in
  the app's colours, since the route is only ever an iframe's `src`; an
  unknown session or a workspace terminal stays the JSON 404 every route
  answers. On success it writes the HTML with
  `Content-Type: text/html; charset=utf-8`, `Cache-Control: no-store`, and
  replaces the CSP `guard` set with exactly:

  ```
  sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:;
  style-src 'unsafe-inline' https:; img-src data: https:; font-src data: https:;
  connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'
  ```

  `sandbox` without `allow-same-origin` puts the document in an opaque
  origin, even opened directly, so its scripts can't read the app's DOM,
  send the cookie or call `/api/v1`; `connect-src 'none'` ends the helper's
  WebSocket, whose retries fail without effect. It is the only route that
  replaces `guard`'s CSP. The action is logged (`device`, `mockup`,
  session, result); the HTML never is.
- **A push links the tab.** `internal/web/triggers.go` sets a push's
  `url`, waiting or finished (a brainstorm's turn ends Running → Ready, a
  finished push), to `/#s/<id>/mockup` when the projection that
  triggers it has `Mockup.Fresh` and no open ask (`InstanceView.Asks`
  empty), and to `/#s/<id>` otherwise. Title, body, urgency and topic are
  unchanged.

### The phone

```
┌──────────────────────────────┐   ┌──────────────────────────────┐
│ LIVE loom@fresco    ● 1  ◆ 2 │   │                    ✕ done    │
│ ‹  pwa-mockups      Claude › │   │ Which layout works better?   │
│ session conv. changes mockup●│   │ ┌──────────────────────────┐ │
│ tab-layout.html · 2m   ↻  ⤢ │   │ │ A  Single column         │ │
│ ┌──────────────────────────┐ │   │ └──────────────────────────┘ │
│ │ Which layout works …     │ │   │ ┌──────────────────────────┐ │
│ │ A  Single column         │ │   │ │ B  Two column            │ │
│ │ B  Two column            │ │   │ └──────────────────────────┘ │
│ └──────────────────────────┘ │   │                              │
│ [Reply to Claude…    ] Send  │   │                              │
└──────────────────────────────┘   └──────────────────────────────┘
        the mockup tab                    ⤢ full screen
```

- **The tab.** `SessionTabs` (`internal/web/static/js/chrome.mjs`) adds
  "mockup" after "changes" while `s.mockup` is set, with an accent dot while
  `s.mockup.fresh`, and keeps it while it is the current route, as the other
  tabs do. `parseRoute` (`internal/web/static/js/app.mjs`) reads
  `#s/<id>/mockup`.
- **The screen**, a new module, is a `.sc-mockup` column filling `main`
  (`flex: 1`, as `.sc-session`, `.sc-turns` and `.sc-changes`): a strip with
  the screen's name and age, ↻ and ⤢; the iframe
  (`sandbox="allow-scripts"`, which repeats the CSP's sandbox in case the
  header is ignored; `src` `/api/v1/sessions/<id>/mockup?v=<changed>`)
  taking the rest; then the reply box with its kept draft. Styles are
  classes in `app.css`; no `style` attribute.
- **Reload.** A new `changed` in the snapshot changes the iframe's `src`, so
  a new screen found at a turn end reloads it; ↻ re-fetches (a counter in
  the query), which also re-runs `Find`.
- **Full screen.** ⤢ goes to `#s/<id>/mockup/full`, which renders no
  header (`.sc-chrome`), no tabs and no reply box; the iframe fills the
  screen within the safe areas, and "✕ done" (back to `#s/<id>/mockup`)
  sits under the Dynamic Island (`env(safe-area-inset-top)`). Plain CSS,
  not the Fullscreen API; a route of its own, so webshot can render it.
- **The fleet row** (`internal/web/static/js/fleet.mjs`) links to
  `#s/<id>/mockup` while `s.mockup.fresh` and the session has no open ask,
  and to `#s/<id>` otherwise. The status row's counts still open the
  session view.
- **Empty and failed.** With `s.mockup` null the tab's body says "No
  mockup is showing; the companion may have stopped."; a failed read shows
  its page in the frame, and ↻ retries. The next snapshot drops the tab
  once the model has cleared `Mockup`.

## Limits

- The mockup is found only when the skill starts its companion with
  `--project-dir` on the session's worktree, as the skill tells it to.
- A screen pushed in the middle of a turn shows when the turn hands back to
  the user (or on ↻), not at once: the model looks only then.
- A companion that stops between turns leaves the tab until it is opened
  (409, then cleared) or the next turn end.
- A screen drawn at a fixed desktop width scrolls sideways in the tab.
- The page's own selection indicator still says "return to the terminal";
  loom does not edit the page.
- A repository that doesn't ignore `.superpowers/` gets its screens in
  loom's diff probe's index (`git add -N .`), as it would without this
  feature; the skill asks the user to ignore the dir.
- A `server-info` committed to a repository could name any loopback port;
  `Fetch` returns a page only when it carries the companion's marker, and
  the phone draws it sandboxed.

## Testing

- **`session/mockup`**: `TestFind_PicksTheNewestLiveCompanion` (two session
  dirs, one stopped; two screens, the newer chosen); `TestFind_NoMockup`, a
  table of every doubt above (no dir, only stopped, garbled `server-info`,
  port 0 and 65536, no screens, a newest `waiting*.html`);
  `TestFetch_RefusesWhatIsNotTheCompanion` against a loopback `httptest`
  server (no marker, over 1 MiB, not HTML, a non-200, a redirect, a slow
  answer past the timeout) and its success case.
- **`core`**: `TestHookScan_AStopFindsTheMockupInTheSameDelivery` (one
  `Sync` carries the status and the mockup);
  `TestHookScan_OnlyAHandBackLooks`, swapping the unexported `findMockup`
  var to count `Find` calls; `TestMockup_FreshUntilTheNextTurn`
  (fresh after a turn that changed the screen, not after a Prompting
  stretch inside the turn, cleared on the next turn, false with no
  `turnStart`); `TestReadMockup_AnswersThePageAndRefreshesTheView` and its
  refusal; the method's cases in `TestMethods_CoverCore` and
  `TestLoopForwardsEachMethodToItsNamesake`; `TestProtocolReference`
  regenerated.
- **`internal/web`**: `TestMockup_ServesThePageSandboxed` (the exact CSP,
  `no-store`, the content type, a workspace terminal 404);
  `TestMockup_ARefusalIsAPageThatSaysWhy`; a case in
  `TestAPI_AReadIsNotTrackedOnceAnsweredAndATimedOutOneIs504`;
  `TestPush_AFreshMockupLinksItsTab` (a stale mockup, or a fresh one with
  an open ask, links the session view); a fleet projection case for
  `mockup`.
- **The app**: `node --check` on every module; `go run ./tools/webshot`
  renders the tab and full screen from a fixture session with a fresh
  mockup and a canned page (`tools/webshot/fixture`).
- **By hand**: a real companion in a `loomdev` sandbox session, then on the
  iPhone: the tab, full screen and back, a waiting push opening the tab, a
  stopped companion's empty state.

## Docs

- `internal/web/CLAUDE.md`: a rule that the mockup route is the only one
  that replaces `guard`'s CSP, and only with the sandbox policy above.
  **Enforced** by `TestMockup_ServesThePageSandboxed`.
- `session/CLAUDE.md`: `session/mockup` joins the pure readers kept free of
  tmux, UI and app imports; `session/README.md` lists it.
- `internal/web/README.md`: the route in the API table, `mockup` in the
  session JSON; `docs/claude/web-layer.md`: the route's own CSP among the
  traps.

## Out of scope

- Choosing by tapping, relayed to `state/events`, or a tap that replies.
- Earlier screens, and the screens of a stopped companion.
- The companion's `/files/*` assets.
- Showing the mockup in the TUI.
- Companions started without `--project-dir`.
