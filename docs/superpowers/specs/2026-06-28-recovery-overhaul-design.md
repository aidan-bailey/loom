# Recovery Overhaul Design

*Date: 2026-06-28 · Branch: `aidanb/recovery-overhaul`*

Builds on [docs/plans/2026-04-16-crash-recovery-design.md](../../plans/2026-04-16-crash-recovery-design.md), which introduced startup reconciliation. This overhaul reworks the *orphan-recovery* layer that was added on top of it.

## Problem

Two user-reported pain points, which share one root cause:

1. **The recovery modal appears on every startup.** When loom finds worktree directories on disk that aren't referenced in `state.json` ("orphans"), it opens a blocking overlay (`OrphanRecoveryPicker`) and forces a decision. The user reports these orphans are usually *cruft they keep skipping* — and skipping is non-terminal: it neither deletes the worktree nor records the choice, so the same orphans are rediscovered and re-prompted on the next launch, forever.

2. **Opening a workspace from within loom shows none of the recovered sessions.** Switching to / opening a workspace mid-session surfaces only what `LoadAndReconcile` finds in `state.json`. Orphan discovery does not run, so disk-only sessions never appear until a full restart.

### Root cause: discovery and prompting are welded together

`recordOrphans()` (the disk scan, `app/app.go:1720`) and "show the blocking picker" (`app/app.go:453-459`, gated only by `len(h.pendingOrphans) > 0`) fire as a single, **startup-only** unit. That coupling forces the modal to be fire-once, which is *why* `activateWorkspace` deliberately omits discovery — see the explicit comment at `app/app.go:1539-1545`: *"orphan discovery is intentionally NOT done here … the overlay only opens from newHome."*

So the two complaints pull in opposite directions (interrupt **less**; surface recovered work **more**), and both are resolvable by decoupling discovery from prompting.

There is also no dismissal memory: `config.AppState` persists only a `HelpScreensSeen` bitmask (`config/state.go:32-38`), with no orphan-skip equivalent.

## Goals

- No focus-stealing modal on startup for the common (cruft) case.
- Recovered/recoverable sessions appear consistently on **every** workspace-load path: startup, workspace-switch, multi-workspace restore, and registration.
- Stale leftovers are reclaimed automatically but **visibly** (a summary line).
- Genuinely-ambiguous orphans (those with unsaved work or a live agent) are surfaced **inline** in the session list, never as a blocking overlay.
- Net reduction in code: one orphan-handling path instead of three scattered calls plus an overlay.

## Non-goals

- No new persisted "skipped orphans" state (the worktree's existence on disk *is* the state — see Ephemeral semantics).
- No write-ahead log / journaling of lifecycle operations (already out of scope in the 2026-04 design).
- No automatic conflict resolution for dirty worktrees beyond the explicit recover/discard choice.
- No async/background discovery in this iteration (synchronous, matching today's `LoadAndReconcile`; revisitable if switch-latency regresses).

## Design

### Core reframe: one path, two outcomes

Move orphan handling **into** `activateWorkspace` (`app/app.go:1525`). After `LoadAndReconcile` populates the slot's tracked instances, call a new helper:

```
reconcileOrphans(list, storage, cfgDir, cmdExec) -> RecoverySummary
```

It runs `session.DiscoverOrphans` (`session/orphan.go:87`), then **classifies** each `OrphanCandidate` and acts:

| Signal of life | Outcome |
|---|---|
| Dead tmux **AND** clean worktree (no uncommitted changes) | **Auto-clean**: `git worktree remove`; branch + commits preserved; tally into summary. |
| Uncommitted changes **OR** live tmux | **Inline recoverable**: build a `Recoverable`-status instance and add it to the slot's list. |

Two buckets, not three. Live-tmux orphans fold into *inline* rather than silent auto-recovery: an orphan with a live agent is, by definition, one loom lost track of (absent from `state.json`), which is odd enough to warrant one explicit keystroke rather than silent resurrection. The entry renders `● running`, so adopting it is a single keypress.

> **Default decision (vetoable):** live-tmux → inline. Flipping to silent auto-recover for live agents is a localized change in `reconcileOrphans` if preferred later.

Classification inputs already exist:
- Live tmux: `OrphanCandidate.HasLiveTmux` (set by `buildOrphanCandidate`, `session/orphan.go:140`).
- Uncommitted changes: `GitWorktree.IsDirty()` (`session/git/worktree_git.go:195`) / `DiffUncommitted` (`session/git/diff.go:85`), probed only for unclaimed worktrees.

### Inline recoverable sessions

- **New status `Recoverable`**, added to the `Status` enum (`session/instance.go:27-34`, currently `Running/Ready/Loading/Paused`, plus `Prompting/Deleting`) and to the `allowedTransitions` map (`session/instance.go:62-67`): `Recoverable -> {Loading, Running, Deleting}`.
- **List rendering**: a distinct marker plus a hint — e.g. `⟳ recoverable · +12 −3` (dirty) or `● running` (live tmux).
- **Actions reuse the existing keymap**, made status-aware rather than adding new keys:
  - **`r` (recover)** — when the selected instance is `Recoverable`, adopt it into `state.json` and resume worktree/tmux by reusing the current `applyOrphanRecovery` logic (`app/app.go:1758`) and `FromInstanceData` (the pure constructor, `session/instance.go:235`). Mirrors today's "resume paused" intent.
  - **`D` (discard)** — remove the worktree but **keep the branch** (commits survive; only uncommitted edits are dropped — the single explicitly-chosen destructive act). Mirrors today's "kill" intent.

  > **Default decision (vetoable):** discard keeps the branch. Deleting the branch too could be a later, harder confirmation.

- **Ephemeral, disk-is-truth**: recoverable entries are re-derived from disk on each activation and never persisted to `state.json`. Recover → becomes a real persisted instance. Discard → worktree gone, never reappears. Ignore → it quietly reappears on the next activation (a persistent reminder, not a nag). This is why no new persisted skip-state is required.

### Non-blocking summary

After activation of the **focused** slot, emit one line via the existing bottom-row surface (`ui.ErrBox`, `ui/err.go`; routed like `scriptHost.Notify`, `app/app_scripts.go:117-120`):

> `Recovery: cleaned 3 stale worktrees · 2 sessions need review (in list)`

`ErrBox` is error-styled today (see `app/app_scripts.go:596`); add an info style so the summary doesn't read as an error. Background-slot activations (multi-restore / toggle of non-focused slots) still auto-clean and add inline entries, but their summary is suppressed to avoid a toast for a workspace the user isn't looking at.

### What gets removed (the simplification)

Deleting the overlay dissolves the very constraint that created complaint #2:

- `ui/overlay/orphan_recovery.go` (+ its test) — the `OrphanRecoveryPicker`.
- `app/state_orphan_recovery.go` (+ its test) and the `stateOrphanRecovery` state constant.
- The auto-popup gate at `app/app.go:453-459` and the `pendingOrphans` / `pendingStartupOverlay` plumbing tied to it.
- The three scattered `recordOrphans` call sites (`app/app.go:366`, `:506`, `:1011`) and the deferred per-slot loop in `restoreSavedWorkspaces` (`app/app.go:502-507`).

All collapse into the single `reconcileOrphans` invoked from `activateWorkspace`.

### Edge cases

- **Classic startup** (no saved workspaces) loads into `h.list` directly (`app/app.go:331`) without `activateWorkspace`. `reconcileOrphans` is therefore called from both spots via a shared helper, so behavior is identical.
- **Orphaned-tmux cleanup**: `CleanupOrphanedSessions` (kills unclaimed `loom_*` tmux) must **exempt** live-tmux recoverable entries, exactly as it exempts `pendingOrphans` today (`app/app.go:373`). Otherwise a live recoverable agent would be killed before the user can adopt it.
- **Unrecovered cache** (`session/storage.go:82-91`) continues to mark its worktree paths as claimed (`UnrecoveredWorktreePaths`, `:298-311`) so reconcile-failed records aren't double-surfaced as orphans.

### Concurrency

`reconcileOrphans` runs synchronously inside `activateWorkspace` on the main goroutine, matching the existing synchronous `LoadAndReconcile`. Work is bounded by the worktree-directory count, and the per-orphan `IsDirty` probe runs only on unclaimed directories. No `tea.Cmd`-goroutine model mutation is introduced. If switch-latency later regresses, discovery can move to a `tea.Cmd` that returns a message applied on the main goroutine (per the no-mutation-from-Cmd rule); deferred as YAGNI.

## How each complaint is resolved

| Complaint | Resolution |
|---|---|
| Modal every startup | No modal. Cruft auto-cleans (+ summary line); ambiguous orphans appear inline. The skip→rediscover loop is broken because auto-clean removes the worktree and inline entries terminate via explicit recover/discard. |
| Switch shows no recovered sessions | `reconcileOrphans` lives in `activateWorkspace`, which every workspace-load path flows through. Removing the overlay removes the reason discovery was gated to startup. |

## Open items to confirm during planning

- Exact `Recoverable` list-row styling/marker (consistent with existing status glyphs in `ui/list.go`).
- Whether `r`/`D` dispatch through Lua (`script/defaults.lua`) needs status-aware branching in the action handler vs. the Go key handler.
- Info-style addition to `ui.ErrBox` (color + auto-clear timing; today errors auto-clear on a 3s schedule).
