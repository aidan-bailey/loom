# Session Workbench (focus view) — Design

Date: 2026-07-22
Status: approved (brainstorm complete)
Branch: aidanb/focus-view

## Purpose

A deep-dive, single-session view: the agent pane beside a switchable content
panel (markdown / diff / files / terminal). It is the place to review the
agent's *work products* — the plans and specs it writes, its code changes —
not just its output stream. The headline feature is a terminal markdown
renderer/viewer/editor.

## Mode wiring

- New `viewWorkbench` value of `m.viewMode`, alongside `viewFocus` and
  `viewOverview`. Orthogonal to `m.state`: overlays and per-state handlers
  work unchanged on top of it, same as overview.
- **Enter:** `enter` in focus mode opens the workbench for the currently
  selected session. `enter` in overview keeps its existing meaning (focus the
  card), so the path is overview → focus → workbench.
- **Exit:** `esc` returns to focus mode. `tab` goes straight to overview
  (preserves "tab toggles overview").
- **Session retargeting:** `]`/`[` (jump-to-waiting) works inside the
  workbench and retargets it to the next waiting session, crossing open
  workspaces as in focus mode. `j`/`k` scroll the content panel; they are not
  session navigation here.
- **Key gating:** `state_default.go` gates script dispatch through a
  `workbenchKeyAllowed` whitelist (pattern copied from
  `overviewKeyAllowed`); non-whitelisted keys no-op rather than acting on
  invisible UI. Workbench handlers do not clear attention/bell state.
- **Persistence:** not persisted across restarts in v1 — quitting from the
  workbench saves `view_mode` as `focus`. This avoids healing logic for
  "restart into a workbench whose session no longer exists".

## Layout

Rail hidden; workspace tab bar kept (one line of orientation); header line
with session title, branch, and diff stats; then a horizontal split:

```
┌ ws1 │ ws2 ─────────────────────────────────────────────────────┐
│ ● focus-view  aidanb/focus-view  +142 -18                      │
│ ┌─ agent ──────────────────────┐┌─ [1 md] 2 diff 3 files 4 term┐
│ │ Claude is writing the spec…  ││ ▸ docs/specs/plan.md (follow)│
│ │                              ││ ┃ Plan                       │
│ │ > _                          ││ • Step 1 — scaffold          │
│ │                              ││ • Step 2 — wire up           │
│ └──────────────────────────────┘└──────────────────────────────┘
│  i attach  1-4 panel  e edit  esc back                          │
└─────────────────────────────────────────────────────────────────┘
```

- **Agent pane** (left): the same `PreviewPane`/emulator rendering as focus
  mode. `i`/`ctrl+a` inline-attach and `alt+a` full-screen attach unchanged.
- **Content panel** (right): tabs selected with `1`–`4` — markdown, diff,
  files, terminal. Last-selected tab remembered per session for the life of
  the process.
- **Split:** default 50/50, resizable with `ctrl+left`/`ctrl+right`,
  persisted per session title in a new `UIPrefs` map (sibling of
  `split_ratios`).
- **Diff panel:** reuses the existing diff rendering (`ui/diff.go`) inside
  the panel frame.
- **Files panel:** reuses `session/files` enumeration; `enter` on a `.md`
  file opens it in the markdown panel (pinned) and switches to the markdown
  tab.
- **Terminal panel:** the session's terminal tmux pane rendered in the panel
  area instead of the bottom split; `ctrl+t` attach as usual.
- **Focus model:** panel-first. Workbench keys drive the content panel;
  attaching (`i`/`ctrl+a`/`ctrl+t`) routes keys to the PTY exactly as in
  focus mode; `ctrl+q`/double-`esc` detaches back to panel control. No new
  focus concept.
- **Mouse:** agent-pane drag-select/copy keeps working; wheel over the panel
  scrolls it (hit-test on x against the split boundary). Otherwise
  keyboard-driven.

## Markdown panel

- **Rendering:** `glamour`, with a style built from loom's theme roles inside
  a `ui.RegisterThemeHook` callback — no init-time styles (per the theme
  gotcha). A live theme switch rebuilds the style and re-renders the current
  document. Rendered output is windowed for scrolling (`j`/`k`/`g`/`G`),
  `ScrollModel`-style.
- **Follow mode (default):** the panel shows the most recently modified
  `.md` file in the session's worktree. The scan rides the existing 3s
  health tick: a bounded walk (skipping `.git`, `node_modules`, and similar)
  compares mtimes only. A new most-recent file swaps the panel to it; the
  same file touched reloads and re-renders, preserving scroll position when
  content length allows.
- **Pin:** manually opening a file (files panel) pins it; `f` returns to
  follow mode. The panel header shows `(following)` or `pinned`.
- **Edit mode:** `e` flips the panel to a `bubbles/textarea` seeded with the
  raw file. `ctrl+s` saves; `esc` exits — immediately if clean, via the
  existing confirmation overlay if dirty. Follow is suspended while editing.
- **Conflict guard:** the file's mtime is captured at load. If it changed on
  disk by save time (the agent wrote it), a confirmation overlay offers
  overwrite / reload / cancel.
- **Edge cases:** file deleted while viewing → placeholder, revert to
  follow; binary or unreadable file → error line in the panel; files over
  ~1 MB render truncated with a notice.

## Concurrency & data flow

- Glamour rendering runs in a `tea.Cmd`; the rendered string is delivered
  via a message and applied to the model in `Update` — honoring the
  no-model-mutation-from-Cmd-goroutines rule.
- The health-tick scan only compares mtimes and emits a "reload needed"
  message; file I/O and rendering happen in the Cmd.
- No new goroutines, no fsnotify dependency; ~3s follow latency is accepted.
- Nothing touches the emulator/tmux locking paths.

## Dependencies

- `glamour` (new; brings goldmark + chroma into vendor).
- `charm.land/bubbles/v2/textarea` (already in tree).

## Testing

Mirrors `app/overview_mode_test.go` conventions:

- Mode transitions: enter/esc/tab, `]`/`[` retargeting, n/N drop-to-focus
  behavior parity.
- `workbenchKeyAllowed` gating: non-whitelisted keys no-op.
- Follow-selection logic: most-recent `.md` wins; skip rules honored.
- Pin/unpin state machine, including files-panel open → pinned.
- Edit save-conflict detection (mtime changed under the editor).
- Theme hook: glamour style rebuilds on `ApplyTheme`, doc re-renders.
- File I/O tests use temp dirs (house style).

## Out of scope (v1)

- Persisting workbench mode across restarts.
- Side-by-side raw/preview markdown editing.
- Auto-follow of non-markdown files.
- fsnotify-based instant follow.

## Deviations (v1)

Where the shipped implementation diverged from this spec, and why:

- **No separate full-width session header.** Branch name and diff stats
  already live in the agent pane's title border (same as focus mode), so a
  dedicated header row would have duplicated that information for no
  benefit.
- **Conflict dialog is binary (overwrite / cancel), not the three-way
  overwrite / reload / cancel this spec originally called for.** "Reload"
  collapses to cancel + `esc` + letting follow mode re-load the file
  naturally — a fourth confirmation-overlay branch wasn't worth the
  complexity for a case that resolves the same way through existing keys.
- **Mouse wheel over the terminal tab no-ops.** The terminal tab shares
  scroll state with the hidden focus-mode split, and there is no hardware
  cursor while attached to it in workbench mode. Drag-select works on the
  agent pane only — the terminal tab's `HitTest` region isn't wired for it
  in v1.
- **Workbench does not survive implicit workspace slot switches.**
  Workspace nav keys, the workspace picker, and a cross-workspace `]`/`[`
  jump all exit cleanly to focus mode in the target workspace rather than
  carrying the workbench across; a same-slot `]`/`[` jump retargets the
  panel to the new session instead of leaving the mode.
- **`g`/`G` and `pgup`/`pgdown` route per-tab, not uniformly.** `g`/`G` jump
  to top/bottom on the diff, files, and markdown tabs; `pgup`/`pgdown` only
  page the markdown and diff tabs (files and terminal have no page-scroll
  concept in v1).
- **Workbench mode is never persisted across restarts, by design** (this
  matches the spec's "out of scope" call), but `tab`'s hop from workbench to
  overview *does* persist the resulting overview mode — consistent with the
  existing focus-mode `tab` toggle, which also persists.
