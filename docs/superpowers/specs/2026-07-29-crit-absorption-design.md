# Crit Absorption — Native Review Loop

**Date:** 2026-07-29
**Status:** Approved design
**Upstream:** [kevindutra/crit](https://github.com/kevindutra/crit) (MIT, declared in README §License; no LICENSE file upstream)

## Problem

Loom has no way to close the human-in-the-loop review cycle: the diff overlay and workbench markdown pane are read-only, so feedback on an agent's plan or code changes travels by hand-typed prompt with no line anchors. Crit solves exactly this — inline comments on plans and diffs, persisted as structured data an agent can act on — but it is a standalone full-screen TUI. Full-screen context switches away from loom (losing sight of the live agent pane) were ruled out; shelling out to crit inside a terminal pane was considered and rejected in favor of full absorption.

## Decision

Fork-and-absorb crit's codebase into loom (precedent: loom itself is an absorbed fork of claude-squad). Crit is ~3k lines of non-test Go on the identical UI stack (`charm.land/bubbletea/v2`, `bubbles/v2`, `lipgloss/v2`), making its `AppModel` embeddable as a workbench panel rather than a subprocess.

Alternatives considered:

- **Shell-out (`crit review` in the session terminal pane, bridge via `crit status` JSON):** days of work, but external binary dependency, inline-attach driving, coupling to crit's CLI surface.
- **Native commenting from scratch:** best UX, weeks of work (comment model, anchoring, review UI all new).
- **tomasz-tomczyk/crit (browser-based, unrelated project of the same name):** rendered markdown in a browser, but leaves the terminal; rejected in favor of the TUI workflow.

Absorption is native UX at fork cost: the comment model, store, diff parsing, and review rendering arrive working.

## Architecture

### 1. Vendoring & attribution

Copy from upstream, rewriting import paths and dropping `internal/cli` (cobra), `cmd/`, and the Claude-plugin/skill layers:

| Upstream | Loom destination | Contents |
|---|---|---|
| `internal/review` | `review/` | `Comment` (ID, Line, EndLine, ContentSnippet, Body, CreatedAt), `ReviewState`, YAML store with flock + atomic rename, code-review session manifest |
| `internal/git` | `review/gitdiff/` | `go-gitdiff`-based diff parsing, `FileChange`, `DiffInfo` (kept separate from `session/git`, which shells out for worktree ops; merging is a possible later cleanup) |
| `internal/document` | `review/` | `.crit/` path derivation |
| `internal/tui` | `ui/review/` | `AppModel` review UI: file tabs, viewports, comment sidebar, comment/edit modals, inline markdown highlighter, chroma syntax highlighting |

New dependencies: `github.com/bluekeyes/go-gitdiff`, `github.com/alecthomas/chroma/v2`, `github.com/gofrs/flock`, `github.com/google/uuid` — all small and permissively licensed. MIT attribution for kevindutra/crit added to `NOTICE.md` alongside the claude-squad notice.

Upstream improvements arrive by manual cherry-pick; loom owns these lines from vendoring onward.

### 2. Review panel (workbench tab 5)

Crit's `AppModel` becomes the workbench's fifth panel tab, "review", selected with `5` alongside markdown/diff/files/terminal. Adaptations:

- **Sizing/focus:** driven by `Workbench.SetSize` and loom key routing; the model never owns the terminal. `SplitPane.SetSize` before `Workbench.SetSize` ordering is unchanged.
- **Quit semantics:** crit's `q` (save & quit program) becomes "save & leave review tab" (return to the previous panel tab). `cleanupWorkbench` remains the single teardown choke point; review-tab state teardown hooks into it so implicit workspace switches can't strand a frozen review.
- **Theming:** `styles.go` is rebuilt with loom color roles constructed inside `ui.RegisterThemeHook` callbacks (init-time lipgloss styles go stale on live theme switch). No literal colors.
- **Two modes, one component:** doc review (single markdown file) and code review (multi-file worktree diff with tabs) are both modes of the same `AppModel`; the embed work is shared.
- **Concurrency:** store loads/saves run in `tea.Cmd` goroutines; results are applied only in `Update` handlers, gated on session title to drop stale deliveries (same pattern as workbench markdown save). No model mutation from Cmd goroutines. Verified with `go test -race`.

### 3. Storage & anchoring

- Comments persist in crit's on-disk format: `.crit/*.yaml` **inside the session's worktree**, with all paths rooted at the worktree path (never `os.Getwd`). Benefits: survives loom restarts, per-session isolation for free (one worktree per session), and interop — agents with the crit CLI or skill installed read the same files.
- `.crit/` is added to the worktree's git exclusions (via `.git/info/exclude` at worktree setup, so it never pollutes diff stats or auto-commits).
- **Frozen-snapshot review:** entering the review tab pins the doc/diff content it opened with; markdown follow-mode is paused (same mechanism as edit mode `e`). Exiting the tab (or `f`) resumes follow. Comments therefore anchor against stable line numbers; anchor rot from a live-editing agent is impossible during review. If the underlying file changed while frozen, the existing mtime conflict guard semantics apply on any save path.
- Workspace terminals: doc review works (no worktree isolation, comments land in the repo root's `.crit/`); code review targets uncommitted root-repo changes, matching the existing diff semantics for workspace terminals.

### 4. Bridge: send review to agent

A "send review to agent" action, available from the review tab (bound key, e.g. `S`):

1. Aggregate all comments for the current review (doc, or all files in the code-review session) from the store.
2. Compose a prompt: instruction header ("Address the following review comments; confirm each as resolved"), then per comment — `path:line` or `path:line–endline`, the stored `ContentSnippet` as a quoted block, and the comment body.
3. Show the composed message in a confirmation overlay (existing overlay machinery) so the user sees exactly what will be sent.
4. On confirm, deliver via the existing quick-input SendKeys path to the session's live agent pane.
5. Zero comments → notify "no review comments", no overlay, no send.

Loom does **not** auto-clear comments after sending; resolution is the agent's/user's job (delete in the review UI, or a future "clear resolved" affordance).

### 5. Entry points & keybindings

All bindings live in `script/defaults.lua` (Lua-rebindable, last-write-wins rules apply; new keys added to the overview whitelist review needs — none, review is workbench-only):

- Workbench: `5` → review tab on the session's worktree diff (code mode).
- Workbench, markdown tab active: `c` → review tab on the currently shown doc (doc mode).
- Focus mode: `c` → open the workbench directly on the review tab for the selected session.
- Excluded states: `Paused`/`Recoverable` instances and empty targets (no diff / no doc) get a notify, not a broken panel.

### 6. Testing

- Vendored store/diff/document tests come along and must keep passing under loom's module.
- New: prompt composition from fixture comments (table-driven); freeze-on-enter / resume-on-exit semantics; exit-restores-panel-state mirroring existing workbench cleanup tests; send path against the mocked `cmd.Executor`; theme-hook style construction (no init-time role capture).
- `go test -race ./review/... ./ui/review/...` in CI via the existing race job.

### 7. Phasing

1. **Phase 1 — vendor + doc review + bridge:** packages land with attribution; workbench review tab reviews the markdown doc; send-to-agent works end to end.
2. **Phase 2 — code review mode:** multi-file diff tabs enabled in the same panel; focus-mode entry point.
3. **Phase 3 (optional, later):** retire the read-only diff overlay (`d`) in favor of the review tab, after it has proven itself.

## Out of scope

- crit's CLI (`crit review/status/comment/setup-claude`) — loom never shells out to or reimplements the CLI surface.
- The `.crit/` schema as a public contract — on-disk compat with upstream crit is best-effort interop, not a guarantee loom documents or version-locks.
- Auto-detecting "review finished" — sending is an explicit human action.
- tomasz-tomczyk/crit (web) integration.
