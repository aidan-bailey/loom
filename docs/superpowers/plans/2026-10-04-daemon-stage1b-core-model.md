# Loom Daemon Stage 1B: `core.Model` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan package by package: each package (A–D) is one task for the sub-skill. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A new `core` package owns loom's session model: the loaded workspaces and their instances, loading and saving, reconcile and the sweeps, the lifecycle operations and their completions, the health tick and every background job. The TUI (`app`) keeps the view (slots as views over core's workspaces, panes, overlays, keys, Lua) and drives the model synchronously on its Update goroutine. Nothing changes for the user.

**Architecture:** `core.Model` holds the classic workspace (no tab open) or the open tabs, each a `core.Workspace` with its context, storage, config, app state and ordered instances. `ui.List` reads its rows straight from the workspace (`ui.InstanceSource`) and keeps only the selection and scroll. Model methods run on the Update goroutine. Work that blocks is a `core.Job`, which the TUI runs as a `tea.Cmd` and hands back with `Deliver`. The model reports what the view must do (notices, a list changed, a session (re)launched, a start finished) as `core.Event`s in an outbox. The TUI drains it at the end of every Update, and right after a call when the order of effects matters. `core` imports nothing of the TUI, and a test enforces that.

**Tech Stack:** Go 1.25, Bubble Tea v2 (`charm.land/bubbletea/v2`, app only), tmux ≥ 3.6, testify. Spec: [`docs/superpowers/specs/2026-10-03-loom-daemon-design.md`](../specs/2026-10-03-loom-daemon-design.md), stage 1. Previous plan: [1A, pane split](2026-10-03-daemon-stage1a-pane-split.md), whose closing section lists the follow-ups this plan picks up.

---

## Where this stage fits

| Plan | Scope | State |
|---|---|---|
| 1A | The TUI owns pane clients, attached by name. Lifecycle never attaches. | Done: local `main` at b81f34d |
| **1B (this)** | `core.Model`: workspaces, storage, reconcile, sweeps, lifecycle operations, completions, the health tick and gated jobs move out of `home`. Still called on the Update goroutine. `ui.List` reads core's order. | |
| 1C | The `Core` interface and `InstanceView` replace `*session.Instance` at the boundary. Also: draft rows for creation flows, Lua lifecycle through `Core`, issue fetches as requests, the pane status ladder as a display-only overlay. | |
| 1D | The model runs on its own goroutine, over channels. | |

Through stage 1 the model loads the TUI's open tabs, as today. Loading every registered workspace waits for the daemon (stage 3).

## Decisions

| # | Decision |
|---|---|
| 1 | **`core.Workspace` owns a workspace's instances; the TUI slot is a view over it.** `app.workspaceSlot` shrinks to `{ws, list, splitPane, workbench}`. The workspace's context, storage, config and app state are read through accessor methods (`m.wsCtx()`, `m.storage()`, `m.appConfig()`, `m.appState()`), never copied. `m.slots` mirrors `core.Model.Tabs()` in order, and with no tab open the focused slot shows `Classic()`. `checkSlotInvariant` enforces the mirror. |
| 2 | **`ui.List` reads its rows from the workspace and keeps the selection by identity.** It holds an `InstanceSource` (production: the `*core.Workspace`) plus the selected instance and its last index. The list's `AddInstance`, `RemoveInstance` and `ReplaceInstance` go away. Adds, removals and replacements are `core.Workspace` edits. A selection whose row is removed moves to the row that slid into its place, or the new last row: the rule `removeAt` applies today. This is what "`ui.List` mirrors core's order" means in 1B. In 1C the source becomes a list of `InstanceView` rows. |
| 3 | **`core` imports nothing of the TUI.** It does not import `app`, `ui/...`, `script`, `keys` or any `charm.land/...` package, directly or through another loom package. `TestCoreImportsNoUI` walks the import graph and fails on one. The launch-options value and its composition, today split between `ui/overlay` and `app/remote_control.go`, move to a new leaf package `session/launch`, because the workspace-terminal auto-create (core) composes a launch program. |
| 4 | **Blocking work is a `core.Job`, and its result comes back through `Deliver`.** `type Job func() any`. The TUI wraps a job as `coreCmd(job)`, a `tea.Cmd` returning `coreResultMsg{msg}`, and `Update` hands `msg` to `m.core.Deliver`. Jobs follow today's Cmd rule: they read no model state. A lifecycle operation that the TUI sequences returns its job (kill, pause, resume, recover, start, merge, push, drop). Follow-up work the model starts itself goes to the outbox: tick probes, gated jobs, the kill after a failed start, the initial prompt. |
| 5 | **Events, drained at the end of every Update.** The model appends `core.Event`s (and jobs) to an outbox. `home.Update` is a thin wrapper: it runs the old switch (renamed `update`), then `drainCore`, which applies each event in order (`applyCoreEvent`) and wraps each job. Workspace transitions also drain right after their core call, so effects keep today's order. For example, a recovery summary set after a load still overwrites the load's "remote control off" info line. `newHome` drains at its end and hands the resulting Cmds to `Init`. 1C turns these events into the `Core` interface's event stream, with `InstanceView` instead of `*session.Instance`. |
| 6 | **Focus stays in the TUI.** Core has no idea which tab is focused. Where today's lifecycle code fell back to the focused slot, the caller passes the workspace: `startOwner`'s fallback, a recover's owner, `enterGlobalMode`'s config rollback, the kill and pause storage. Whatever depends on focus or on `m.state` stays in the TUI's event appliers: the selection, inline attach, the "started in X" wording and the `stateDefault` guard. |
| 7 | **Completions keep their rules: by identity, stamped with the owning workspace.** `instanceStartedMsg.slot` and the rest become `core` result types stamped with `*core.Workspace` (`StartResult.Owner`, …). `reopenedTwin`, `adoptIntoReopened`, `saveSlot`'s closed-and-reopened rule and every CLAUDE.md rule on destructive actions and async completions move unchanged. Only the sender changes. |
| 8 | **The health tick splits along the pane boundary.** Core's tick probes tmux liveness and applies it (pause the dead, restart a dead workspace terminal under its circuit breaker), refreshes parity and diff stats, adopts Claude's reported status, and dispatches the gated jobs. The TUI's tick keeps everything that needs a pane client: expiring toasts, prune, the inline-attach backstop, the workbench scan, client repair for the live sessions core reports (`core.Alive`), and on the snapshot path the capture-pane status scan. Core never sees a pane. |
| 9 | **`SendPrompt` leaves the Update goroutine (1A follow-up 6).** The initial prompt of an `N` flow is sent by a core job after the start lands. The prompt overlay's "send to a running session" is a core job too. Both cost three tmux subprocesses and a 100ms sleep that today block Update. |
| 10 | **Claude status: hooks and roster in core, the pane ladder in the TUI.** Hook scans, roster queries, `observeRoster`, `applyClaudeStatus` and `adoptClaudeStatus` (exported as `AdoptClaudeStatus`, still the single choke point) move. The pane-driven ladder (`statusDetectedMsg`, the snapshot scan, `maybeRedetect`, the Ready→Running promotion on output) stays in the TUI until 1C makes it display-only. Pane events tell core about output and quiet (`PaneOutput`, `PaneQuiet`), which feed the dirty set, hook scans and the prompting roster query. |
| 11 | **Moved code logs under `log.For("core")`** with the same event names. Lines that stay in `app` keep `log.For("app")`. |
| 12 | **Tests move with the code they test.** A test of a function that moves to `core` moves to `core` (package `core`, fixtures from `core/testhelpers_test.go`). A test that exercises the TUI's response goes through `home.Update` with a `coreResultMsg` and stays in `app`. No assertion is dropped. Each package's coordinator check compares assertion counts before and after. |

Out of scope:
- `Core` interface, `InstanceView`, draft rows, Lua through `Core`, the model goroutine (1C, 1D).
- Issue-picker fetches (`issueExpandCmd` and the picker's list fetch) stay in `app`: they are stateless reads, and 1C makes them requests.
- `Session.Start` running `new-session -d` through a PTY (1A follow-up 6, third bullet): a `session/tmux` change unrelated to the model.
- The pollable attach PTY stage and the other 1A follow-ups.
- The two workspace-terminal auto-create paths differ: `OpenTab` composes with the workspace config's program and first kills an owned leftover session, while `loadWorkspace` uses the process's `-p` program and doesn't kill. Both move verbatim. Unifying them is a behavior change for its own commit.

## What moves where

| Today (`app`) | 1B home | Package |
|---|---|---|
| `workspaceSlot`'s `wsCtx`, `storage`, `appConfig`, `appState`, `recovery` | `core.Workspace` | A |
| `ui.List`'s instance slice and its edits | `core.Workspace` (`Add`, `Remove`, `Replace`) | A |
| `activateWorkspace`, `deactivateWorkspace`, `enterGlobalMode`, `stayInGlobalMode`, `restoreSavedWorkspaces`, `loadStartupStorage(Fallback)`, `loadSlotStorage` (lifecycle halves) | `core/load.go`, `core/workspaces.go` (`OpenTab`, `CloseTab`, `EnterGlobal`, `StayGlobal`, `RestoreSaved`, `LoadClassic`) | A |
| `reconcileOrphans`, `claimTitles`, `claimedWorktreePaths`, `recoverySummary`, `persistableInstances`, `applySessionConfig`, `quitSkipsSave`, `handleQuit`'s saves | `core/load.go`, `core/persist.go` | A |
| `registry` writes, `restoreFailed`, `saveOpenWorkspaces`, `openWorkspaceNames`, `persistFocusedWorkspace`'s write, `registerWorkspaceMsg`'s `Add` | `core/workspaces.go` | A |
| `allInstances`, `activeInstances`, `activeInstance`, `instanceForSession` | `core/workspaces.go` | A |
| `rcAuth` (the field) | `core.Model` | A |
| `overlay.LaunchOptions`, `launchOptionsFromConfig`, `applyLaunchOptions`, `ParseLaunchOptions`, `effectiveRemoteControl`, `effectiveModel`, the `*Program` helpers | `session/launch` | A |
| `killActionFor`, `pauseActionFor`, `snapshotSaveFunc`, `resumeResult`, the resume/recover/start Cmd bodies, `mergeActionFor`, `backgroundKillCmd`, `dropPendingNew`'s removal | `core/ops.go` | B |
| `completions.go` (`handleInstanceStarted`, `handleResumeDone`, `handleRecoverDone`, `reopenedTwin`, `adoptIntoReopened`, `owningSlot`, `startOwner`, `slotLoaded`, `reopened`, `closedOwnerNote`, `slotLabel`, `saveSlot`), and the `killInstanceMsg`, `transitionFailedMsg` and `pauseInstanceMsg` handlers, `removeInstanceEverywhere`. (`slotHolding` stays in `app`, as a view lookup over `core.Model.Holding`) | `core/completions.go`, `core/workspaces.go`, `core/persist.go` | A (the workspace queries and `saveSlot`), B (the rest) |
| Initial-prompt send, prompt overlay's send to a running session | core jobs | B |
| `pollgate.go` (all kinds but `gateRatioSave`) | `core/gate.go` | C |
| `gatherMetadataCmd` (lifecycle half), `applyLiveness` (lifecycle half), `maxWorkspaceTerminalRestartFailures`, `markDirty`, `takeDirty`, `verifyDeadCmd`'s probe | `core/tick.go` | C |
| `events.go` roster/status functions, `hook_scan.go` | `core/claude_status.go`, `core/hook_scan.go` | C |
| `github.go` (poll, availability, state, bases, `applyGitHubState`), `pushActionFor` | `core/github.go` | C |
| `accounts.go` state, reload/refresh, requests, `accountUsers`; `usage.go` | `core/accounts.go`, `core/usage.go` | C |

What stays in `app`: everything that renders or reads a pane client, `m.state`, overlays, key routing, the Lua engine and script host, the workbench, review, overview cursor, tab bar, menu, error bar, split-ratio throttle (its dedupe becomes a plain flag), full-screen attach, and the views of accounts and GitHub state.

## Packages

| Package | Delivers | Commit |
|---|---|---|
| **A** | `core` with `Model` and `Workspace`; `ui.List` reads the workspace; loading, saving, workspace transitions, sweeps and the registry move; `session/launch`; the event outbox and its drain; `TestCoreImportsNoUI` | `refactor(core): the model owns workspaces and their instances` |
| **B** | Lifecycle operations (start, kill, pause, resume, recover, merge, drop) as core methods returning jobs; completions in core; prompts sent by jobs | `refactor(core): lifecycle operations and completions move into the model` |
| **C** | The health tick split; `pollGate` and every gated job in core: roster, hook scans, GitHub and parity, accounts, usage | `refactor(core): the health tick and background jobs move into the model` |
| **D** | CLAUDE.md, the spec's stage-1 entry, then full verification (suite, race, e2e, sandbox smoke) | `docs: CLAUDE.md and spec for the core model (daemon stage 1B)` |

A package is one unit of work: one implementer, one review, one commit, plus any fixups the review asks for. Its numbered checkpoints (A1, A2, …) are steps inside it, not commits. Do them in order. Every checkpoint ends with the build and the touched packages' tests green, unless its text says otherwise.

## Conventions for every package

- Run Go commands from the worktree root. Plain tests need `CGO_ENABLED=0`, e.g. `CGO_ENABLED=0 go test ./core/... ./app/...`.
- Race detector: `CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./session/...`.
- Format only tracked non-vendor files plus new ones: `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`. Never `gofmt -w .`: it rewrites `vendor/`.
- The local golangci-lint is v2 while the repo config is v1-shaped. Use `go vet ./...` instead.
- Commit messages end with both trailers:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01Hc861zmBTH48vW7MY2DvEJ
  ```
  Commit with explicit paths (`git commit -- <paths>`) or `git add` the package's files first.
- Never use `git stash` in any form: the stash stack is shared with other worktrees and sessions. To set work aside, make a WIP commit.
- Stay on this worktree's branch. No `checkout`, `switch` or new branches or worktrees.
- Don't read or write anything under `~/.claude`.
- A security hook rejects any file write containing the word `exec` immediately followed by `(`. Name helpers `executor()`, `runner()`, `cmdExec`, never with that spelling.
- Never run `./loom` directly. To see the TUI, use the `loom-dev` skill (`go run ./tools/loomdev …`).
- No test may reach the developer's tmux server or `~/.loom`. `core`'s tests get the same `TestMain` as `app` (A2).
- **Moving code.** A function that moves keeps its body verbatim, except for the edits its checkpoint lists. The usual edits:
  - the receiver `*home` becomes `*Model`;
  - `slot.list.GetInstances()` becomes `ws.insts` (inside core) or `ws.Instances()`;
  - `slot.list.AddInstance` / `RemoveInstance` / `ReplaceInstance` become `ws.Add` / `Remove` / `Replace`;
  - `m.handleError(err)` becomes `m.notifyErr(err)`;
  - `m.errBox.SetInfo(s)` becomes `m.notifyInfo(s)`;
  - `log.For("app")` becomes `log.For("core")`;
  - `*workspaceSlot` becomes `*Workspace`, and `slotLabel(s)` becomes `s.Label()`.

  Keep every comment; update a comment only where it names a function or field that was renamed.
- **Tests.** Follow decision 12. Never weaken or delete an assertion to make a moved test pass. If an assertion can't survive the move, stop and report it.

## File structure

**New**
| File | Responsibility |
|---|---|
| `core/doc.go` | Package doc: the model's rules (Update goroutine only in 1B, jobs, events, no UI imports) |
| `core/model.go` | `Model`, `Options`, `New`, `NewForTest`, `Job`, `Out`, the outbox (`emit`, `spawn`, `Drain`), `Deliver` |
| `core/events.go` | The `Event` types |
| `core/workspace.go` | `Workspace`, `WorkspaceParts`, `NewWorkspace`, instance edits |
| `core/workspaces.go` | The loaded set (`Classic`, `Tabs`, `Loaded`, `Holding`, …), registry, `restoreFailed`, transitions (`OpenTab`, `CloseTab`, `EnterGlobal`, `StayGlobal`, `RestoreSaved`, `Register`), `ActiveInstances`, `InstanceForSession` |
| `core/load.go` | `LoadClassic`, `loadWorkspace`, `reconcileOrphans`, `claimTitles`, `claimedWorktreePaths`, `RecoverySummary`, `applySessionConfig` |
| `core/persist.go` | `Persistable`, `Save`, `SaveForQuit`, `PersistOpenList`, `quitSkipsSave` |
| `core/ops.go` | (B) lifecycle operations returning jobs |
| `core/completions.go` | (B) result types and their handlers |
| `core/gate.go` | (C) `pollGate`, `gateKind`, `dispatchGated`, delivery |
| `core/tick.go` | (C) the health tick's lifecycle half |
| `core/claude_status.go`, `core/hook_scan.go` | (C) roster, hooks, Claude status |
| `core/github.go` | (C) GitHub poll, state, parity bases, push |
| `core/accounts.go`, `core/usage.go` | (C) account registry, auth, sync, usage |
| `core/testmain_test.go` | `TestMain`, copied from `app` |
| `core/boundary_test.go` | `TestCoreImportsNoUI` |
| `core/testhelpers_test.go` | Fixtures for core tests |
| `session/launch/launch.go`, `session/launch/launch_test.go`, `session/launch/testmain_test.go` | Launch options and their composition |
| `app/core_glue.go` | `coreResultMsg`, `coreCmd`, `drainCore`, `applyCoreEvent`, `slotFor`, `newSlotView` |
| `app/testcore_test.go` | `testWS`, `slotOver`, `wireCore` |

**Modified**
| File | Change |
|---|---|
| `ui/list.go` + its tests | `InstanceSource`; identity selection; edits removed |
| `ui/overlay/sessionLaunchOptions.go` | `type LaunchOptions = launch.Options` |
| `app/workspaces.go`, `app/app_init.go`, `app/app.go` | Slots become views; transitions call core; `Update` wraps `update` + drain; the health tick splits (C) |
| `app/completions.go` | Shrinks to the event appliers (`applyStarted`, `applyRecovered`) and `dropPendingNew` (B) |
| `app/intents.go`, `app/state_*.go`, `app/app_scripts.go` | Instance edits and lifecycle operations through core |
| `app/events.go`, `app/hook_scan.go`, `app/github.go`, `app/accounts.go`, `app/usage.go`, `app/pollgate.go` | Jobs and state move to core (C); `app/pollgate.go` and `app/hook_scan.go` are deleted |
| `app/remote_control.go` | Composition helpers move to `session/launch` |
| Tests in `app/`, `ui/` | Fixtures build workspaces; moved tests go to `core/` |
| `CLAUDE.md`, the daemon spec, this plan | Package D |

---

## Package A: the model owns workspaces and their instances

This package creates `core`. It moves each workspace's instances, loading, saving, the workspace transitions, the sweeps and the registry writes into it, and turns the TUI's slots into views over core's workspaces. Lifecycle operations (B) and background work (C) still live in `app` afterwards, but they edit instances through `core.Workspace` and save through `core.Model.Save`. One commit at the end.

Checkpoints A1–A3 add code next to the old code and stay green. A4 is the switch: the build is red from its first step until its last.

### A1. `session/launch`: launch options leave the UI

**Files:**
- Create: `session/launch/launch.go`, `session/launch/launch_test.go`, `session/launch/testmain_test.go`
- Modify: `ui/overlay/sessionLaunchOptions.go`, `app/remote_control.go`, `app/remote_control_test.go`, the call sites listed below

- [ ] **Step 1: Create the package by moving code.**

`session/launch/launch.go` starts with:
```go
// Package launch is a Claude session's launch options and their
// composition into (and decoding from) the agent command line: remote
// control, permission mode, model, effort, plus the options that never
// reach the command line (Headroom proxy, the 1h cache TTL, the branch
// prefix, the account). The TUI's Session Launch Options modal edits an
// Options value; the model composes one from config for the workspace
// terminal it creates.
package launch
```

Move these declarations from `ui/overlay/sessionLaunchOptions.go` and `app/remote_control.go`, bodies and comments verbatim, renamed as shown:

| From | To |
|---|---|
| `overlay.LaunchOptions` (the struct and its field comments) | `launch.Options` |
| `remoteControlProgram`, `permissionModeProgram`, `modelProgram`, `effortProgram`, `parseModelValue`, `effectiveModel` | same names, unexported, in `launch` |
| `launchOptionsFromConfig` | `FromConfig` |
| `effectiveRemoteControl` | `EffectiveRemoteControl` |
| `applyLaunchOptions` | `Compose` |
| `ParseLaunchOptions` | `Parse` |

Inside the moved bodies, `overlay.LaunchOptions` becomes `Options`. In comments, update the names to the new ones.

Add one function. It is the body of `app`'s `remoteControlBlockedOn`, given the account's auth instead of resolving it:
```go
// RemoteControlBlocked reports whether a launch of program should be
// interrupted to tell the user remote control can't work: the toggle is
// on (rcEnabled, after EffectiveRemoteControl), the program is Claude, and
// the launching account's auth was clearly determined incompatible.
func RemoteControlBlocked(auth session.RemoteControlAuth, rcEnabled bool, program string) bool {
	return rcEnabled && session.IsClaudeProgram(program) && auth.Blocked()
}
```

- [ ] **Step 2: Point the old homes at the new package.**

In `ui/overlay/sessionLaunchOptions.go`, replace the struct with an alias, so the modal and its callers compile unchanged:
```go
// LaunchOptions is the value the modal edits; see launch.Options.
type LaunchOptions = launch.Options
```

In `app/remote_control.go`, delete the moved functions, and make `remoteControlBlockedOn` call `launch.RemoteControlBlocked(m.rcAuthFor(acct), rcEnabled, program)`. Then rewrite the call sites:
- `launchOptionsFromConfig(` → `launch.FromConfig(`
- `effectiveRemoteControl(` → `launch.EffectiveRemoteControl(`
- `applyLaunchOptions(` → `launch.Compose(`
- `ParseLaunchOptions(` → `launch.Parse(`

Today they appear in `app/accounts.go`, `app/app_init.go`, `app/intents.go`, `app/state_issue_picker.go`, `app/state_prompt.go` and `app/workspaces.go`. Find them all with `git grep -n 'launchOptionsFromConfig\|effectiveRemoteControl\|applyLaunchOptions\|ParseLaunchOptions' -- '*.go'`.

- [ ] **Step 3: Move the tests.**

These tests in `app/remote_control_test.go` test moved functions, so they move to `session/launch/launch_test.go` (`package launch`) with their calls renamed:
- `TestRemoteControlProgram`, `TestPermissionModeProgram`, `TestModelProgram`, `TestLaunchOptionsFromConfig`, `TestEffectiveRemoteControl`, `TestEffortProgram`
- `TestApplyLaunchOptions_ComposesEffort`
- every `TestParseLaunchOptions_*`
- `TestApplyLaunchOptions`, `TestLaunchOptionsFromConfig_SeedsContext1M`, `TestParseModelValue`, `TestEffectiveModel`, `TestApplyLaunchOptions_Context1M`

The helpers `boolPtrTest` and `stringPtrTest` go with them, and stay in `app` too if an `app` test still uses them. `TestRemoteControlBlocked` and `TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomProxyForcesRCOff` exercise `home` methods, so they stay in `app` with their calls renamed.

`session/launch/testmain_test.go` is a copy of `session/files/testmain_test.go` with the package name changed. The package reaches `config`, so `TestEveryConfigReachingPackageIsolatesLoomDirs` requires it.

- [ ] **Step 4: Verify.**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./session/launch/ ./app/ ./ui/... ./internal/testenv/`
Expected: PASS.

### A2. `core`: the model, the outbox and the workspace

**Files:**
- Create: `core/doc.go`, `core/model.go`, `core/events.go`, `core/workspace.go`, `core/testmain_test.go`, `core/boundary_test.go`, `core/testhelpers_test.go`, `core/workspace_test.go`, `core/model_test.go`

- [ ] **Step 1: Write the boundary test first.**

`core/boundary_test.go`:
```go
package core

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCoreImportsNoUI fails when core, or any loom package it reaches,
// imports the TUI: app, ui and its subpackages, script, keys, or a
// charm.land library (Bubble Tea, Bubbles, Lip Gloss). The daemon runs
// core with no terminal; a UI import here is a seam someone has to cut
// again. Every file counts, whatever its build tags; tests do not.
func TestCoreImportsNoUI(t *testing.T) {
	const module = "github.com/aidan-bailey/loom"
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	forbidden := func(ip string) bool {
		switch {
		case ip == module+"/app", ip == module+"/script", ip == module+"/keys":
			return true
		case ip == module+"/ui", strings.HasPrefix(ip, module+"/ui/"):
			return true
		case strings.HasPrefix(ip, "charm.land/"):
			return true
		}
		return false
	}
	seen := map[string]bool{}
	var walk func(ip string, via []string)
	walk = func(ip string, via []string) {
		if seen[ip] {
			return
		}
		seen[ip] = true
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(ip, module), "/")))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
			require.NoError(t, err)
			for _, spec := range f.Imports {
				dep, err := strconv.Unquote(spec.Path.Value)
				require.NoError(t, err)
				chain := append(append([]string(nil), via...), ip)
				if forbidden(dep) {
					t.Errorf("%s imports %s (reached via %s)", ip, dep, strings.Join(chain, " → "))
				}
				if dep == module || strings.HasPrefix(dep, module+"/") {
					walk(dep, chain)
				}
			}
		}
	}
	walk(module+"/core", nil)
}
```

`core/testmain_test.go`: copy `app/app_test.go`'s `TestMain` and `runTests` (log init, `TMUX` unset, `testenv.IsolateLoomDirs`, the private `TMUX_TMPDIR` and `LOOM_TMUX_SOCKET`, and the `kill-server` cleanup) into `package core`, verbatim apart from the package clause and imports. `app/app_test.go` keeps its own copy.

Run: `CGO_ENABLED=0 go test ./core/ -run TestCoreImportsNoUI`
Expected: FAIL to build (no non-test Go files in `core`). Step 2 fixes that.

- [ ] **Step 2: Write the package doc, model and events.**

`core/doc.go`:
```go
// Package core is loom's session model: the loaded workspaces and their
// instances, and everything that acts on them without a terminal:
// loading and saving, reconcile and the sweeps, the lifecycle operations
// and their completions, the health tick and the background jobs.
//
// In daemon stage 1B the TUI (package app) drives the model synchronously
// on its Update goroutine, so every Model method must be called there. A
// Job is the one thing that runs elsewhere: it reads no model state and
// returns its result, which the caller hands back with Deliver. The model
// reports to the TUI through Events, which the caller drains (Drain)
// after each call and applies to its view.
//
// core imports nothing of the TUI (TestCoreImportsNoUI): the daemon will
// run it with no terminal. Focus is the TUI's: where an operation needs
// "the workspace the user is looking at", the caller passes it.
package core
```

`core/model.go`:
```go
package core

import (
	"fmt"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Job is work the model hands its caller to run off the model's
// goroutine. It reads no model state and returns one message (nil for
// none), which the caller hands back to Deliver.
type Job func() any

// Out is what the model produced since the last Drain: events for the
// TUI and jobs to run, each in the order produced.
type Out struct {
	Events []Event
	Jobs   []Job
}

// Empty reports whether o holds nothing.
func (o Out) Empty() bool { return len(o.Events) == 0 && len(o.Jobs) == 0 }

// Options configure a Model.
type Options struct {
	// Registry is the workspace registry; nil in bare tests.
	Registry *config.WorkspaceRegistry
	// Program is the agent command the process was started with (-p).
	Program string
	// CmdExec replaces cmd.MakeExecutor() on the workspace load paths: a
	// test seam. nil in production.
	CmdExec cmd2.Executor
	// Ctx and Config are the startup context and its config: the classic
	// workspace's, shown while no tab is open.
	Ctx    *config.WorkspaceContext
	Config *config.Config
}

// Model is the session model (see the package doc). Methods must be
// called on one goroutine: in stage 1B, the TUI's Update goroutine.
type Model struct {
	registry *config.WorkspaceRegistry
	program  string
	cmdExec  cmd2.Executor

	// classic is the workspace shown while no tab is open: the startup
	// context's (classic startup, or the fallback when no tab could be
	// restored) or the global one EnterGlobal built. nil while tabs are
	// open: the first tab opened replaces it.
	classic *Workspace
	// tabs are the open workspace tabs, in tab order.
	tabs []*Workspace
	// restoreFailed names the workspaces the registry's open list held but
	// RestoreSaved could not open. Their live sessions were spared only
	// because that launch skipped the orphan sweep, so they stay in the
	// persisted open list (PersistOpenList) until one opens (OpenTab) or
	// is deselected (KeepRestoreFailed); EnterGlobal and StayGlobal clear
	// them all.
	restoreFailed []string

	// rcAuth is the default account's remote-control auth, detected once
	// at startup (the TUI sets it) and read by every launch decision.
	rcAuth session.RemoteControlAuth

	out Out
}

// New builds the model and its classic workspace: the startup context's
// state and storage, not yet loaded (LoadClassic loads it, or
// RestoreSaved's fallback). It first syncs the process-wide session flags
// from Config and writes the loom-context prompt files, covering both the
// classic path and the tab path (OpenTab re-syncs per workspace); without
// it a classic launch would never set the flag. Formerly the start of
// app.newHome.
func New(o Options) (*Model, error) {
	cfgDir := ""
	if o.Ctx != nil {
		cfgDir = o.Ctx.ConfigDir
	}
	session.SetLoomContextEnabled(o.Config.LoomContextEnabled())
	session.SetSubagentTrackingEnabled(o.Config.SubagentTrackingEnabled())
	if err := session.WriteLoomContextFiles(cfgDir); err != nil {
		log.For("core").Warn("loom_context.write_failed", "err", err.Error())
	}
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}
	m := NewForTest(o)
	m.classic = NewWorkspace(WorkspaceParts{Ctx: o.Ctx, Storage: storage, Config: o.Config, State: state})
	return m, nil
}

// NewForTest builds a model with no workspace and no side effects; a test
// installs its fixture's workspaces with SetWorkspacesForTest.
func NewForTest(o Options) *Model {
	return &Model{registry: o.Registry, program: o.Program, cmdExec: o.CmdExec}
}

// SetWorkspacesForTest installs a fixture's workspaces: classic when tabs
// is empty, else tabs in order (classic is then ignored, as the first tab
// replaces it).
func (m *Model) SetWorkspacesForTest(classic *Workspace, tabs []*Workspace) {
	if len(tabs) > 0 {
		m.classic, m.tabs = nil, append([]*Workspace(nil), tabs...)
		return
	}
	m.classic, m.tabs = classic, nil
}

// SetExecForTest replaces the executor of the workspace load paths.
func (m *Model) SetExecForTest(e cmd2.Executor) { m.cmdExec = e }

// SetRegistryForTest replaces the workspace registry.
func (m *Model) SetRegistryForTest(r *config.WorkspaceRegistry) { m.registry = r }

// executor returns the executor for the workspace load paths: the test
// seam when set, the production executor otherwise.
func (m *Model) executor() cmd2.Executor {
	if m.cmdExec != nil {
		return m.cmdExec
	}
	return cmd2.MakeExecutor()
}

// Program is the agent command the process was started with (-p).
func (m *Model) Program() string { return m.program }

// RCAuth is the default account's remote-control auth.
func (m *Model) RCAuth() session.RemoteControlAuth { return m.rcAuth }

// SetRCAuth records the default account's remote-control auth.
func (m *Model) SetRCAuth(a session.RemoteControlAuth) { m.rcAuth = a }

// emit queues an event for the TUI.
func (m *Model) emit(e Event) { m.out.Events = append(m.out.Events, e) }

// spawn queues a job for the caller to run; nil is ignored.
func (m *Model) spawn(j Job) {
	if j != nil {
		m.out.Jobs = append(m.out.Jobs, j)
	}
}

// notifyErr queues err for the error bar.
func (m *Model) notifyErr(err error) {
	if err != nil {
		m.emit(Notice{Err: err})
	}
}

// notifyInfo queues an info line for the error bar.
func (m *Model) notifyInfo(s string) { m.emit(Notice{Info: s}) }

// Drain returns everything produced since the last Drain, and forgets it.
func (m *Model) Drain() Out {
	out := m.out
	m.out = Out{}
	return out
}

// Deliver hands the model a job's result. Results the model does not know
// are logged and dropped.
func (m *Model) Deliver(msg any) {
	switch msg := msg.(type) {
	case nil:
	default:
		log.For("core").Error("deliver.unknown_result", "type", fmt.Sprintf("%T", msg))
	}
}
```

`core/events.go`:
```go
package core

// Event is something the TUI must apply to its view after a model call.
// The TUI applies them in order (app.applyCoreEvent).
type Event interface{ coreEvent() }

// Notice is a line for the error bar: Err as an error (shown longer the
// longer it is, and logged), or else Info as an info line.
type Notice struct {
	Err  error
	Info string
}

func (Notice) coreEvent() {}
```
Packages B and C add event types to this file.

- [ ] **Step 3: Write `Workspace`.**

`core/workspace.go`:
```go
package core

import (
	"slices"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
)

// Workspace is one loaded workspace: its context, the storage its
// instances persist to, its config and app state, its instances in
// display order, and the summary of its last orphan reconcile. The model
// owns it; the TUI reads it (its slot is a view over it) and changes its
// instances only through Add, Remove and Replace. The context, storage,
// config and state are fixed for the workspace's lifetime.
type Workspace struct {
	ctx      *config.WorkspaceContext
	storage  *session.Storage
	cfg      *config.Config
	state    config.AppState
	insts    []*session.Instance
	recovery RecoverySummary
}

// WorkspaceParts are a workspace's fixed handles. Any may be nil, as in a
// test's bare fixture.
type WorkspaceParts struct {
	Ctx     *config.WorkspaceContext
	Storage *session.Storage
	Config  *config.Config
	State   config.AppState
}

// NewWorkspace builds a workspace with no instances.
func NewWorkspace(p WorkspaceParts) *Workspace {
	return &Workspace{ctx: p.Ctx, storage: p.Storage, cfg: p.Config, state: p.State}
}

// Ctx is the workspace's context; nil only in bare test fixtures.
func (w *Workspace) Ctx() *config.WorkspaceContext { return w.ctx }

// Storage persists the workspace's instances.
func (w *Workspace) Storage() *session.Storage { return w.storage }

// Config is the workspace's config.json.
func (w *Workspace) Config() *config.Config { return w.cfg }

// State is the workspace's state.json: help screens seen and UI prefs.
func (w *Workspace) State() config.AppState { return w.state }

// Recovery is the summary of the workspace's last orphan reconcile.
func (w *Workspace) Recovery() RecoverySummary { return w.recovery }

// Name is the workspace's registered name, "" for the global context.
func (w *Workspace) Name() string {
	if w.ctx == nil {
		return ""
	}
	return w.ctx.Name
}

// Label names the workspace in notices: its name, or "global".
func (w *Workspace) Label() string {
	if n := w.Name(); n != "" {
		return n
	}
	return "global"
}

// Instances returns the workspace's instances in display order: the
// workspace terminal first when there is one, the rest in the order they
// were added. The slice is the workspace's own: callers must not modify
// it, and copy it to keep it past the next edit.
func (w *Workspace) Instances() []*session.Instance { return w.insts }

// Add adds inst: first when it is the workspace terminal, last otherwise.
func (w *Workspace) Add(inst *session.Instance) {
	if inst.IsWorkspaceTerminal {
		w.insts = append([]*session.Instance{inst}, w.insts...)
		return
	}
	w.insts = append(w.insts, inst)
}

// Remove removes inst by identity, reporting whether the workspace held
// it. Identity, never a title: two workspaces can hold same-titled
// instances. Only bookkeeping; the caller runs any Kill.
func (w *Workspace) Remove(inst *session.Instance) bool {
	i := slices.Index(w.insts, inst)
	if i < 0 {
		return false
	}
	w.insts = slices.Delete(w.insts, i, i+1)
	return true
}

// Replace puts replacement in old's place (old found by identity), so the
// order, and the row a view's selection is on, are unchanged. Reports
// whether old was held.
func (w *Workspace) Replace(old, replacement *session.Instance) bool {
	i := slices.Index(w.insts, old)
	if i < 0 {
		return false
	}
	w.insts[i] = replacement
	return true
}

// Holds reports whether inst is one of the workspace's instances.
func (w *Workspace) Holds(inst *session.Instance) bool {
	return inst != nil && slices.Contains(w.insts, inst)
}

// ByTitle returns the instance titled title, or nil.
func (w *Workspace) ByTitle(title string) *session.Instance {
	for _, inst := range w.insts {
		if inst.Title == title {
			return inst
		}
	}
	return nil
}
```

`RecoverySummary` is referenced here and moves in A3. Add a placeholder declaration now so the package builds (`type RecoverySummary struct{}` in `core/load.go`), and replace it in A3.

- [ ] **Step 4: Unit-test the workspace and the outbox.**

`core/testhelpers_test.go`:
```go
package core

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/require"
)

// newInst builds an unstarted instance titled title in a temp dir.
func newInst(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	return inst
}

// newTerminal builds an unstarted workspace terminal titled title.
func newTerminal(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	return inst
}

// pausedInst builds a started, Paused instance titled title, as a
// reconciled record comes back (FromInstanceData; no tmux contacted).
// Persistable keeps it, and a resume moves it to Loading.
func pausedInst(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: session.Paused, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	return inst
}
```
Packages B and C add their fixtures to this file.

`core/workspace_test.go`:
```go
package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkspace_TerminalFirstThenAddOrder(t *testing.T) {
	w := NewWorkspace(WorkspaceParts{})
	a, b := newInst(t, "a"), newInst(t, "b")
	wt := newTerminal(t, "ws")
	w.Add(a)
	w.Add(wt)
	w.Add(b)
	assert.Equal(t, []string{"ws", "a", "b"}, titles(w.Instances()))
}

func TestWorkspace_EditsByIdentity(t *testing.T) {
	w := NewWorkspace(WorkspaceParts{})
	a, b, c := newInst(t, "a"), newInst(t, "b"), newInst(t, "c")
	twin := newInst(t, "b") // same title, different instance
	w.Add(a)
	w.Add(b)
	w.Add(c)

	assert.False(t, w.Remove(twin), "a same-titled instance is not the held one")
	assert.True(t, w.Replace(b, twin))
	assert.Equal(t, []string{"a", "b", "c"}, titles(w.Instances()))
	assert.Same(t, twin, w.Instances()[1], "Replace keeps the row")
	assert.True(t, w.Holds(twin))
	assert.False(t, w.Holds(b))
	assert.True(t, w.Remove(a))
	assert.Same(t, twin, w.ByTitle("b"))
	assert.Nil(t, w.ByTitle("a"))
}

func TestWorkspace_Label(t *testing.T) {
	assert.Equal(t, "global", NewWorkspace(WorkspaceParts{}).Label())
}

func titles(insts []*session.Instance) []string {
	out := make([]string, len(insts))
	for i, inst := range insts {
		out[i] = inst.Title
	}
	return out
}
```
(`titles` needs `session` imported; add it.)

`core/model_test.go`:
```go
package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModel_DrainReturnsInOrderAndForgets(t *testing.T) {
	m := NewForTest(Options{})
	m.notifyInfo("one")
	m.notifyErr(errors.New("two"))
	m.notifyErr(nil) // ignored
	m.spawn(func() any { return nil })
	m.spawn(nil) // ignored

	out := m.Drain()
	assert.Equal(t, []Event{Notice{Info: "one"}, Notice{Err: errors.New("two")}}, out.Events)
	assert.Len(t, out.Jobs, 1)
	assert.True(t, m.Drain().Empty(), "a second drain finds nothing")
}

func TestModel_DeliverUnknownIsDropped(t *testing.T) {
	m := NewForTest(Options{})
	m.Deliver(struct{}{})
	assert.True(t, m.Drain().Empty())
}
```

Run: `CGO_ENABLED=0 go test ./core/ ./internal/testenv/`
Expected: PASS, including `TestCoreImportsNoUI` and `TestEveryConfigReachingPackageIsolatesLoomDirs`.

### A3. Loading, saving and the transitions, in core

**Files:**
- Create: `core/workspaces.go`, `core/load.go` (replacing A2's placeholder), `core/persist.go`, `core/load_test.go`, `core/persist_test.go`

Nothing in `app` calls these yet; A4 switches it over. Until then the old `app` code stays and the build stays green.

- [ ] **Step 1: Move the helpers verbatim.**

Move into `core/load.go`, with the usual edits (Conventions):

| From `app` | To `core` | Further edits |
|---|---|---|
| `recoverySummary`, `empty`, `String` | `RecoverySummary`, `Empty`, `String` | none |
| `claimedWorktreePaths(claimed, storage)` | same | none |
| `claimTitles(claimed, list, storage)` | `claimTitles(claimed map[string]bool, ws *Workspace)` | reads `ws.insts` and `ws.storage` (`storage` may be nil) |
| `(m *home) reconcileOrphans(cfgDir, program, list, storage, cmdExec)` | `(m *Model) reconcileOrphans(ws *Workspace, cfgDir, program string, cmdExec cmd2.Executor) RecoverySummary` | `list.AddInstance` → `ws.Add`; `list.GetInstances()` → `ws.insts`; `storage` → `ws.storage` |
| `applySessionConfig(cfg, cfgDir)` | same | none |

Into `core/persist.go`:

| From `app` | To `core` | Further edits |
|---|---|---|
| `persistableInstances` | `Persistable` (exported: `app`'s pause and resume saves use it until B) | none |
| `quitSkipsSave` | same | none |

- [ ] **Step 2: Write the loaded set and the registry in `core/workspaces.go`.**

```go
package core

import (
	"slices"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Classic is the workspace shown while no tab is open; nil while one is.
func (m *Model) Classic() *Workspace { return m.classic }

// Tabs are the open workspace tabs, in tab order. Callers must not modify
// the slice.
func (m *Model) Tabs() []*Workspace { return m.tabs }

// Loaded is every loaded workspace: the tabs, or the classic one alone.
func (m *Model) Loaded() []*Workspace {
	if len(m.tabs) == 0 {
		if m.classic == nil {
			return nil
		}
		return []*Workspace{m.classic}
	}
	return m.tabs
}

// Registry is the workspace registry (nil in bare tests). The TUI reads
// it for the picker; writes go through the model.
func (m *Model) Registry() *config.WorkspaceRegistry { return m.registry }

// RestoreFailed names the workspaces that failed to restore and are kept
// in the open list (see Model.restoreFailed).
func (m *Model) RestoreFailed() []string { return m.restoreFailed }

// Holding returns the loaded workspace holding inst (by identity), or nil.
func (m *Model) Holding(inst *session.Instance) *Workspace {
	for _, ws := range m.Loaded() {
		if ws.Holds(inst) {
			return ws
		}
	}
	return nil
}

// IsLoaded reports whether ws is still part of the model: an open tab, or
// the classic workspace.
func (m *Model) IsLoaded(ws *Workspace) bool {
	return ws != nil && slices.Contains(m.Loaded(), ws)
}

// Reopened reports whether ws's workspace is open in a loaded workspace
// other than ws itself: for one no longer loaded, whether the user has
// reopened it since.
func (m *Model) Reopened(ws *Workspace) bool {
	for _, w := range m.Loaded() {
		if w != ws && w.Label() == ws.Label() {
			return true
		}
	}
	return false
}

// ClosedNote describes, for a completion's notice, an owner workspace
// that was closed while the operation ran.
func (m *Model) ClosedNote(ws *Workspace) string {
	if m.Reopened(ws) {
		return "which was closed and reopened meanwhile"
	}
	return "which is no longer open"
}

// Instances returns every instance of every loaded workspace.
func (m *Model) Instances() []*session.Instance {
	var out []*session.Instance
	for _, ws := range m.Loaded() {
		out = append(out, ws.insts...)
	}
	return out
}
```
Then move `app`'s `activeInstances` (as `(m *Model) ActiveInstances`), `activeInstance` (as `ActiveInstance`), and `instanceForSession` (as `(m *Model) InstanceForSession`) here, comments verbatim:
- `ActiveInstances` iterates `m.Instances()`.
- `InstanceForSession` walks `m.Loaded()`. It keeps the allocation-free classic path the original comment describes: when `len(m.tabs) == 0`, check `m.classic` directly.

Then the open-list persistence:
```go
// OpenNames is the open set PersistOpenList persists and the picker shows
// selected: the tabs' names, then the workspaces that failed to restore
// and are to be retried.
func (m *Model) OpenNames() []string {
	names := make([]string, 0, len(m.tabs)+len(m.restoreFailed))
	for _, ws := range m.tabs {
		names = append(names, ws.Name())
	}
	for _, name := range m.restoreFailed {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// PersistOpenList writes OpenNames to the registry's open list, so the
// next launch restores them.
func (m *Model) PersistOpenList() {
	if m.registry == nil {
		return
	}
	if err := m.registry.SetOpenWorkspaces(m.OpenNames()); err != nil {
		log.For("core").Error("persist_open_workspaces_failed", "err", err)
	}
}

// SetLastUsed records name as the workspace the next launch focuses.
func (m *Model) SetLastUsed(name string) error {
	if m.registry == nil || name == "" {
		return nil
	}
	return m.registry.UpdateLastUsed(name)
}

// KeepRestoreFailed forgets every failed-to-restore workspace not in
// desired: the user left it unchecked in the picker, which closes it.
func (m *Model) KeepRestoreFailed(desired map[string]bool) {
	m.restoreFailed = slices.DeleteFunc(m.restoreFailed, func(n string) bool { return !desired[n] })
}

// StayGlobal applies a picker commit with nothing selected, made from
// global mode: nothing is rebuilt, and the only thing such a commit can
// change, the workspaces that failed to restore, is closed and the
// now-empty open list persisted. See app.stayInGlobalMode for why nothing
// reloads.
func (m *Model) StayGlobal() {
	m.restoreFailed = nil
	m.PersistOpenList()
}

// Register adds dir to the registry as workspace name and returns its
// entry. The registry has no lock, so this runs on the model's goroutine,
// never in a job.
func (m *Model) Register(name, dir string) (config.Workspace, error) {
	if err := m.registry.Add(name, dir); err != nil {
		return config.Workspace{}, fmt.Errorf("failed to register workspace: %w", err)
	}
	ws := m.registry.FindByPath(dir)
	if ws == nil {
		return config.Workspace{}, fmt.Errorf("workspace not found after registration")
	}
	return *ws, nil
}
```
(`fmt` is imported too.) The transitions follow in this file:

```go
// OpenTab loads a workspace as a new tab: its state, config and
// instances, reconciled against tmux and disk, crash-recovered sessions
// relaunched, its workspace terminal created when it has none, and orphan
// worktrees surfaced inline (reconcileOrphans). The first tab opened
// replaces the classic workspace (Classic is nil on return). A load error
// opens nothing and leaves state.json untouched. Formerly the lifecycle
// half of app.activateWorkspace; the TUI builds the tab's view over the
// returned workspace.
func (m *Model) OpenTab(def config.Workspace) (*Workspace, error) {
	wsCtx := config.WorkspaceContextFor(&def)
	state := config.LoadStateFrom(wsCtx.ConfigDir)
	appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)
	// Loom-context injection: keep the config-dir prompt files current and
	// sync the global enabled flag on every workspace load, before any
	// Claude session (workspace terminal, crash-restart, resume) launches.
	applySessionConfig(appConfig, wsCtx.ConfigDir)
	storage, err := session.NewStorage(state, wsCtx.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage for workspace %s: %w", def.Name, err)
	}

	cmdExec := m.executor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		// (keep the original "Fail closed" comment from activateWorkspace verbatim)
		return nil, fmt.Errorf("load instances for workspace %s: %w", def.Name, err)
	}
	ws := NewWorkspace(WorkspaceParts{Ctx: wsCtx, Storage: storage, Config: appConfig, State: state})
	hasWorkspaceTerminal := false
	for _, inst := range instances {
		if inst.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		ws.Add(inst)
	}

	// (the crash-restart loop from activateWorkspace, verbatim, over instances)

	// (the workspace-terminal auto-create block from activateWorkspace,
	// verbatim, with these edits only:
	//   - ws.Name → def.Name; list.AddInstance → ws.Add
	//   - launchOptionsFromConfig/effectiveRemoteControl/applyLaunchOptions
	//     → launch.FromConfig/launch.EffectiveRemoteControl/launch.Compose
	//   - m.remoteControlBlocked(rc, program) →
	//     launch.RemoteControlBlocked(m.rcAuth, rc, program)
	//   - m.errBox.SetInfo(s) → m.notifyInfo(s); m.rcAuth stays m.rcAuth)

	ws.recovery = m.reconcileOrphans(ws, wsCtx.ConfigDir, appConfig.GetProgram(), cmdExec)
	if len(m.tabs) == 0 {
		m.classic = nil // the first tab replaces the classic workspace
	}
	m.tabs = append(m.tabs, ws)
	// Opened at last: no longer a restore failure to retry.
	m.restoreFailed = slices.DeleteFunc(m.restoreFailed, func(n string) bool { return n == def.Name })
	return ws, nil
}
```

The bracketed comments above mark blocks to paste from `app/workspaces.go:activateWorkspace` with exactly the edits listed. Nothing else in the block changes, including its order: load, crash restart, workspace terminal, reconcile.

```go
// CloseTab saves and closes the tab named name, returning it (nil, nil
// when no tab has that name). A failed save keeps the tab open, so its
// unsaved state stays reachable (silent data loss on teardown is worse
// than a sticky tab), and the last tab is never closed: leaving no tab
// means global mode, which only EnterGlobal builds.
func (m *Model) CloseTab(name string) (*Workspace, error) {
	idx := slices.IndexFunc(m.tabs, func(w *Workspace) bool { return w.Name() == name })
	if idx == -1 {
		return nil, nil
	}
	if len(m.tabs) == 1 {
		return nil, fmt.Errorf("cannot close %s, the last open workspace: return to global mode instead", name)
	}
	ws := m.tabs[idx]
	if err := ws.storage.SaveInstances(Persistable(ws.insts)); err != nil {
		log.For("core").Error("workspace.save_failed", "name", name, "err", err)
		return nil, fmt.Errorf("failed to save workspace %s: %w", name, err)
	}
	m.tabs = slices.Delete(m.tabs, idx, idx+1)
	return ws, nil
}

// EnterGlobal replaces every loaded workspace with the global one (the
// picker's Global row). It saves every tab, then loads the global context
// like classic startup (loadWorkspace, without the tmux sweep: the
// closing tabs' sessions are unclaimed here), and only then drops what was
// loaded, so a failure (a save or the load) switches nothing. The load has
// side effects an abort could not undo (it relaunches crash-recovered
// agents, writes the loom-context files, cleans orphan worktrees, sweeps
// hooks folders); that is why every save comes first. focused is the
// workspace the TUI shows: an aborted load puts its config's session flags
// back. Returns the global workspace, now Classic.
func (m *Model) EnterGlobal(focused *Workspace) (*Workspace, error) {
	for _, ws := range m.tabs {
		if err := ws.storage.SaveInstances(Persistable(ws.insts)); err != nil {
			log.For("core").Error("workspace.save_failed", "name", ws.Name(), "err", err)
			return nil, fmt.Errorf("failed to save workspace %s (staying in workspace mode): %w", ws.Name(), err)
		}
	}
	globalCtx, err := config.GlobalWorkspaceContext()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the global config dir: %w", err)
	}
	cfgDir := globalCtx.ConfigDir
	appState := config.LoadStateFrom(cfgDir)
	appConfig := config.LoadConfigFrom(cfgDir)
	storage, err := session.NewStorage(appState, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("failed to construct global storage: %w", err)
	}
	global := NewWorkspace(WorkspaceParts{Ctx: globalCtx, Storage: storage, Config: appConfig, State: appState})
	// Sessions the load (re)starts launch under the global config's
	// settings, like OpenTab's; an abort puts the focused workspace's back.
	applySessionConfig(appConfig, cfgDir)
	if err := m.loadWorkspace(global, cfgDir, false); err != nil {
		if focused != nil {
			applySessionConfig(focused.cfg, "")
		}
		return nil, fmt.Errorf("failed to load global sessions (staying in workspace mode): %w", err)
	}
	m.tabs = nil
	m.classic = global
	// Clear the registry's open list so the next launch lands in global
	// mode rather than restoring tabs the user just closed. An explicit
	// return to global mode closes the workspaces that failed to restore
	// too.
	m.restoreFailed = nil
	if m.registry != nil {
		if err := m.registry.SetOpenWorkspaces(nil); err != nil {
			log.For("core").Warn("clear_open_workspaces_failed", "err", err)
		}
	}
	return global, nil
}

// RestoreSaved opens saved (the registry's open tabs from the last run)
// as tabs, plus the startup workspace (the classic context's name) when
// it is not among them, then sweeps orphan tmux sessions across the open
// tabs. A workspace that fails to open is logged; one that was open last
// time stays in the open list to be retried (RestoreFailed), and any
// failure skips the sweep, since that workspace's titles are unknown and
// the sweep would kill its live sessions. With no tab open it loads the
// classic workspace instead (loadClassicFallback). Returns the index of
// the tab to focus (the startup workspace's, else the registry's last
// used, else 0), or -1 when no tab opened. Formerly the lifecycle half of
// app.restoreSavedWorkspaces.
func (m *Model) RestoreSaved(saved []config.Workspace) int {
	explicit := m.classic.Name()
	// (the desired-list construction from restoreSavedWorkspaces, verbatim,
	// with m.registry as is)
	var failed []string
	for _, def := range desired {
		if _, err := m.OpenTab(def); err != nil {
			log.For("core").Error("workspace.restore_failed", "name", def.Name, "err", err)
			failed = append(failed, def.Name)
			if slices.ContainsFunc(saved, func(s config.Workspace) bool { return s.Name == def.Name }) {
				// Was open: keep it open, to be retried (restoreFailed).
				m.restoreFailed = append(m.restoreFailed, def.Name)
			}
		}
	}
	// (the sweep comment and block from restoreSavedWorkspaces, verbatim,
	// with: claimTitles(claimedTitles, ws) and owned = append(owned, ws.ctx)
	// for each ws in m.tabs, and m.executor())
	if len(m.tabs) == 0 {
		m.loadClassicFallback()
		return -1
	}
	// (the focus-by-name loop from restoreSavedWorkspaces, over m.tabs and
	// ws.Name())
	if m.registry != nil {
		m.PersistOpenList()
		if name := m.tabs[focused].Name(); name != "" {
			if err := m.registry.UpdateLastUsed(name); err != nil {
				log.For("core").Debug("registry.update_last_used_failed", "workspace", name, "err", err)
			}
		}
	}
	return focused
}
```

Note `m.classic.Name()` is read before the first `OpenTab`, which clears `m.classic`. `Name` is nil-safe.

- [ ] **Step 3: Write the classic load in `core/load.go`.**

```go
// LoadClassic loads the classic workspace's storage with startup
// semantics (loadWorkspace). sweepTmux adds the orphan tmux sweep;
// RestoreSaved's fallback passes false, because the workspaces that failed
// to load still have live sessions whose titles it cannot read. Formerly
// app.loadStartupStorage.
func (m *Model) LoadClassic(sweepTmux bool) error {
	cfgDir := ""
	if m.classic.ctx != nil {
		cfgDir = m.classic.ctx.ConfigDir
	}
	return m.loadWorkspace(m.classic, cfgDir, sweepTmux)
}

// loadClassicFallback runs when no workspace could be restored: loading
// was deferred to RestoreSaved, so without it the user would land in
// global mode over a never-loaded storage whose first save replaces its
// readable records with the empty list. On failure it fails closed: the
// storage's write latch refuses every save, and the error is a notice
// rather than an exit, so the user can still open a workspace from the
// picker. Formerly app.loadStartupStorageFallback.
func (m *Model) loadClassicFallback() {
	// Each failed OpenTab re-synced these process-wide flags from its own
	// workspace's config; put the startup config's values back before
	// anything below launches a session.
	if cfg := m.classic.cfg; cfg != nil {
		session.SetLoomContextEnabled(cfg.LoomContextEnabled())
		session.SetSubagentTrackingEnabled(cfg.SubagentTrackingEnabled())
	}
	if err := m.LoadClassic(false); err != nil {
		m.notifyErr(fmt.Errorf("no workspace could be restored, and loading sessions failed (nothing will be saved): %w", err))
	}
}
```

Then move `app.loadSlotStorage`'s body into `(m *Model) loadWorkspace(ws *Workspace, cfgDir string, sweepTmux bool) error`, keeping every comment and its order: load, crash restart, reconcile, sweep, workspace terminal. Edits:
- `storage := ws.storage`, `wsCtx := ws.ctx`, `cmdExec := m.executor()`
- `slot.list.AddInstance` → `ws.Add`; `slot.list.GetInstances()` → `ws.insts`
- `program := m.program; if ws.cfg != nil { program = ws.cfg.GetProgram() }`
- `recovery := m.reconcileOrphans(cfgDir, program, slot.list, storage, cmdExec)` → `ws.recovery = m.reconcileOrphans(ws, cfgDir, program, cmdExec)`
- the sweep: `claimTitles(claimedTitles, ws)`
- the workspace terminal: the same launch-option edits as `OpenTab`, with `slot.appConfig` → `ws.cfg` and `m.program` kept as is
- delete the trailing `m.ensureSlotPanes(slot)`: attaching clients is the TUI's (A4)
- `return nil` instead of `return recovery, nil`, and `return err` instead of `return recoverySummary{}, err`

- [ ] **Step 4: Write saving in `core/persist.go`.**

```go
// Save persists ws's instances after a change. A workspace no longer
// loaded is saved only if no loaded workspace holds the same workspace:
// that one reloaded state.json into its own, newer copy, which a save from
// the dropped one's stale copy would overwrite. Formerly app.saveSlot.
func (m *Model) Save(ws *Workspace) error {
	if !m.IsLoaded(ws) && m.Reopened(ws) {
		log.For("core").Warn("closed_slot_save_skipped", "workspace", ws.Label(), "reason", "workspace_reopened")
		return nil
	}
	return ws.storage.SaveInstances(Persistable(ws.insts))
}

// SaveForQuit saves every loaded workspace and the registry's open list
// before the TUI exits. A failed save is returned, and the TUI then
// refuses to quit so the user can fix the cause and retry (silent data
// loss on exit is worse than a sticky quit), except the storage's write
// latch (quitSkipsSave), which no retry could clear. With no tab open the
// open list is written only if it holds something: it then keeps just
// the workspaces that failed to restore. Formerly handleQuit's saves.
func (m *Model) SaveForQuit() error {
	if len(m.tabs) > 0 {
		var firstErr error
		for _, ws := range m.tabs {
			if err := ws.storage.SaveInstances(Persistable(ws.insts)); err != nil {
				if quitSkipsSave(err) {
					log.For("core").Warn("quit.save_skipped", "name", ws.Name(), "reason", "storage_load_failed", "err", err)
					continue
				}
				log.For("core").Error("workspace.save_failed", "name", ws.Name(), "err", err)
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to save workspace %s: %w", ws.Name(), err)
				}
			}
		}
		if firstErr != nil {
			return firstErr
		}
		m.PersistOpenList()
		return nil
	}
	if err := m.classic.storage.SaveInstances(Persistable(m.classic.insts)); err != nil {
		if !quitSkipsSave(err) {
			return err
		}
		log.For("core").Warn("quit.save_skipped", "reason", "storage_load_failed", "err", err)
	}
	if m.registry != nil && len(m.registry.OpenWorkspaces) > 0 {
		m.PersistOpenList()
	}
	return nil
}
```

- [ ] **Step 5: Test the new pieces.**

Move the tests of the moved helpers into `core`. `git grep -ln 'persistableInstances\|recoverySummary\|quitSkipsSave' -- 'app/*_test.go'` finds them, today in `app/app_test.go` and `app/recovery_test.go`. A test that calls only a moved helper moves with it, renamed (`persistableInstances` → `Persistable`, `recoverySummary{…}.String()` → `RecoverySummary{…}.String()`). A test that drives `home` stays in `app` for A4 to adapt.

Add `core/persist_test.go`:
```go
package core

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedWorkspace builds a workspace named name over a fresh temp config
// dir and its (empty) storage. A storage never loaded loads before its
// first write (Storage.writeLocked), so no explicit load is needed.
func storedWorkspace(t *testing.T, name string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	return NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{Name: name, ConfigDir: dir}, Storage: storage, Config: config.DefaultConfig(), State: state})
}

// TestSave_WritesALoadedWorkspace is the control for the skip below: a
// Paused record reaches disk (Persistable keeps it).
func TestSave_WritesALoadedWorkspace(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.Add(pausedInst(t, "kept"))
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	require.NoError(t, m.Save(ws))
	data, err := ws.storage.LoadInstanceData()
	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, "kept", data[0].Title)
}

func TestCloseTab_RefusesTheLastTab(t *testing.T) {
	m := NewForTest(Options{})
	a := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{a})
	_, err := m.CloseTab("a")
	require.Error(t, err)
	assert.Equal(t, []*Workspace{a}, m.Tabs())
}

func TestCloseTab_DropsTheTab(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	closed, err := m.CloseTab("a")
	require.NoError(t, err)
	assert.Same(t, a, closed)
	assert.Equal(t, []*Workspace{b}, m.Tabs())
	assert.False(t, m.IsLoaded(a))
}

// TestSave_SkipsAClosedWorkspaceThatWasReopened: a dropped workspace's
// copy is stale once its workspace is open again, so saving it would
// overwrite the reopened copy's newer state.json.
func TestSave_SkipsAClosedWorkspaceThatWasReopened(t *testing.T) {
	m := NewForTest(Options{})
	closed, reopened := storedWorkspace(t, "a"), storedWorkspace(t, "a")
	closed.Add(pausedInst(t, "stale"))
	m.SetWorkspacesForTest(nil, []*Workspace{reopened, storedWorkspace(t, "b")})

	require.NoError(t, m.Save(closed))
	data, err := closed.storage.LoadInstanceData()
	require.NoError(t, err)
	assert.Empty(t, data, "the closed copy was not written")
}
```
The `stale` row is Paused, which `Persistable` keeps, so the test fails if the skip regresses: `TestSave_WritesALoadedWorkspace` shows such a row reaches disk. `LoadInstanceData` reads the storage's `config.InstanceStorage` (the state a save writes through), so it sees what a save wrote.

Run: `CGO_ENABLED=0 go vet ./core/ && CGO_ENABLED=0 go test ./core/ ./app/`
Expected: PASS. `app` is unchanged and still uses its own copies.

### A4. The switch: slots become views over core's workspaces

**Files:**
- Create: `app/core_glue.go`, `app/testcore_test.go`
- Modify: `ui/list.go`, `ui/list_*_test.go`; `app/app.go`, `app/app_init.go`, `app/workspaces.go`, `app/completions.go`, `app/intents.go`, `app/state_*.go`, `app/app_scripts.go`, `app/github.go`, `app/events.go`, `app/panes.go`, `app/accounts.go`, `app/overview.go`, `app/remote_control.go`; tests in `app/`
- Delete from `app`: the code A3 copied into `core`

The build is red from Step 1 until Step 6.

- [ ] **Step 1: `ui.List` reads an `InstanceSource`.**

In `ui/list.go`:
- Add the interface.
- Replace the `items` field with `src`, `selected` and `selectedIdx`.
- Change `NewList`.
- Add `items`, `resolveSelection` and `selectRow`.
- Delete `AddInstance`, `RemoveInstance`, `ReplaceInstance` and `removeAt`.

```go
// InstanceSource supplies the rows a List shows, in display order. In
// production it is the list's workspace (core.Workspace), which owns the
// instances; the List only reads it, so adding, removing and replacing
// rows are edits of the source.
type InstanceSource interface {
	Instances() []*session.Instance
}

type List struct {
	// src supplies the rows; nil shows none.
	src InstanceSource
	// selected is the selected row's instance and selectedIdx its row when
	// last resolved. The rows change under the list (the source is edited
	// elsewhere), so the selection is kept by identity and re-resolved on
	// every read (resolveSelection).
	selected     *session.Instance
	selectedIdx  int
	scrollOffset int // index of the first visible item in the viewport
	// (the remaining fields unchanged)
}

// NewList constructs a List showing src's rows, bound to the given
// spinner.
func NewList(spinner *spinner.Model, src InstanceSource) *List {
	return &List{src: src, spinner: spinner}
}

// items returns the source's rows.
func (l *List) items() []*session.Instance {
	if l.src == nil {
		return nil
	}
	return l.src.Instances()
}

// resolveSelection returns the selected row's index in the current rows,
// finding the selected instance by identity: a row added or removed above
// it moves its index, not the selection (inline attach looks the
// selection up per key, so a silent shift would redirect typing). When
// the selected instance is gone, the selection moves to the row that slid
// into its place, or to the new last row; with no rows it is 0. These are
// the rules removeAt and AddInstance applied when the list held the rows.
func (l *List) resolveSelection() int {
	items := l.items()
	if len(items) == 0 {
		l.selected, l.selectedIdx = nil, 0
		return 0
	}
	if l.selectedIdx < len(items) && items[l.selectedIdx] == l.selected {
		return l.selectedIdx
	}
	if l.selected != nil {
		if i := slices.Index(items, l.selected); i >= 0 {
			l.selectedIdx = i
			return i
		}
	}
	l.selectedIdx = min(l.selectedIdx, len(items)-1)
	l.selected = items[l.selectedIdx]
	return l.selectedIdx
}

// selectRow selects row i, which must be in range.
func (l *List) selectRow(i int) {
	l.selected, l.selectedIdx = l.items()[i], i
	l.ensureSelectedVisible()
}
```

Then rewrite every method that read `l.items` or `l.selectedIdx`:
- **`ensureSelectedVisible`** starts with `sel := l.resolveSelection(); items := l.items()` and uses `sel` and `len(items)`.
- **`String`** calls `ensureSelectedVisible` as today, then takes `items := l.items()` and `sel := l.selectedIdx` once at the top and uses them in place of `l.items` and `l.selectedIdx`.
- **`Up`, `Down`, `PageUp`, `PageDown`, `Top` and `Bottom`** take `items := l.items()`, start from `l.resolveSelection()` where they read `l.selectedIdx`, and move with `l.selectRow(i)` where they assigned `l.selectedIdx = i`. They keep their Deleting-skip rules exactly.
- **`SetSelectedInstance(idx)`** does nothing when `idx < 0 || idx >= len(l.items())`, and otherwise calls `l.selectRow(idx)`.
- **`SelectInstance(target)`** calls `selectRow` on the target's index.
- **`GetSelectedInstance`** returns nil with no rows, else `items[l.resolveSelection()]`.
- **`SelectedIdx`** returns `l.resolveSelection()`.
- **`GetInstances`** returns `l.items()`.
- **`NumInstances`** returns `len(l.items())`.
- **`findByTitle`** and **`SetSessionPreviewSize`** loop over `l.items()`.

Rewrite the `ui` tests that built lists with `AddInstance` (`ui/list_height_test.go`, `ui/list_page_nav_test.go`, `ui/list_remove_test.go`) on a test source:
```go
// sliceSource is a test InstanceSource the tests edit directly, standing
// in for core.Workspace.
type sliceSource struct{ items []*session.Instance }

func (s *sliceSource) Instances() []*session.Instance { return s.items }

func (s *sliceSource) remove(inst *session.Instance) {
	s.items = slices.DeleteFunc(s.items, func(i *session.Instance) bool { return i == inst })
}

func (s *sliceSource) prepend(inst *session.Instance) {
	s.items = append([]*session.Instance{inst}, s.items...)
}
```
`newPageNavList(n)` returns the list together with its source. `l.RemoveInstance(x)` becomes `src.remove(x)`, and the workspace-terminal prepend test uses `src.prepend`. Every assertion stays as it is. The point is that the selection rules hold when the source changes under the list. Add one test for replacement:
```go
// TestList_ReplacedRowKeepsTheSelection: a recover swaps the placeholder
// for the recovered instance in place, and the selection follows the row.
func TestList_ReplacedRowKeepsTheSelection(t *testing.T) {
	l, src := newPageNavList(3)
	l.SetSelectedInstance(1)
	replacement := &session.Instance{Title: "recovered"}
	src.items[1] = replacement
	assert.Same(t, replacement, l.GetSelectedInstance())
	assert.Equal(t, 1, l.SelectedIdx())
}
```

- [ ] **Step 2: The slot becomes a view, and `home` holds the model.**

In `app/workspaces.go`, replace the struct:
```go
// workspaceSlot is the TUI's view of one loaded workspace: the model's
// workspace (ws) plus the view state over it, the rail, the split pane
// and the workbench. The model (core) owns the workspace, its instances,
// its storage, config and state; the slot never copies them. home embeds
// the focused slot, so m.list, m.splitPane, m.workbench and the accessors
// below resolve to the focused slot's. Slots are always handled by pointer.
type workspaceSlot struct {
	// ws is the workspace this slot shows; nil only in bare test homes.
	ws *core.Workspace
	// list is the session rail; it reads ws's instances.
	list *ui.List
	// splitPane displays the agent and terminal panes with diff overlay.
	splitPane *ui.SplitPane
	// workbench renders the right content panel when viewMode is
	// viewWorkbench; the left half is splitPane with its terminal hidden.
	// It pairs with this slot's splitPane (its terminal tab shows the
	// slot's shared TerminalPane). Non-nil for every slot.
	workbench *ui.Workbench
}

// wsCtx is the workspace's context (see core.Workspace.Ctx).
func (s *workspaceSlot) wsCtx() *config.WorkspaceContext {
	if s.ws == nil {
		return nil
	}
	return s.ws.Ctx()
}

// storage persists the workspace's instances.
func (s *workspaceSlot) storage() *session.Storage {
	if s.ws == nil {
		return nil
	}
	return s.ws.Storage()
}

// appConfig is the workspace's configuration.
func (s *workspaceSlot) appConfig() *config.Config {
	if s.ws == nil {
		return nil
	}
	return s.ws.Config()
}

// appState is the workspace's app state: help screens seen, UI prefs.
func (s *workspaceSlot) appState() config.AppState {
	if s.ws == nil {
		return nil
	}
	return s.ws.State()
}
```
Then rewrite every read of the removed fields as a call: `.wsCtx` → `.wsCtx()`, `.storage` → `.storage()`, `.appConfig` → `.appConfig()`, `.appState` → `.appState()`, `.recovery` → `.ws.Recovery()`. That covers `m.`, `slot.`, `s.` and so on. The compiler finds every site. Don't touch a local variable that happens to have one of those names.

In `app/app.go`:
- Add the field `core *core.Model` to `home`, next to `panes`, with this comment: "core is the session model (package core): the loaded workspaces, their instances and everything lifecycle. Never nil after newHome."
- Remove the fields `cmdExec`, `registry`, `restoreFailed` and `rcAuth`. Their readers become `m.core.Registry()`, `m.core.RestoreFailed()` and `m.core.RCAuth()`. A write of `rcAuth` becomes `m.core.SetRCAuth(x)`.
- Delete `executor()`. Its callers moved to `core`.

- [ ] **Step 3: Write `app/core_glue.go`.**

```go
package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// coreResultMsg carries a core job's result back to Update, which hands it
// to the model (core.Model.Deliver).
type coreResultMsg struct{ msg any }

// coreCmd runs job as a tea.Cmd: off the Update goroutine, its result
// coming back as a coreResultMsg. nil for a nil job.
func coreCmd(job core.Job) tea.Cmd {
	if job == nil {
		return nil
	}
	return func() tea.Msg { return coreResultMsg{msg: job()} }
}

// drainCore applies everything the model produced since the last drain:
// each event in order (applyCoreEvent), and each job as a Cmd. Applying an
// event can call the model again, so it drains until nothing is left.
// Update runs it after every message. A caller whose later steps must see
// an event's effect (a workspace transition) runs it right after the model
// call. A bare test home without a model drains nothing.
func (m *home) drainCore() tea.Cmd {
	if m.core == nil {
		return nil
	}
	var cmds []tea.Cmd
	for out := m.core.Drain(); !out.Empty(); out = m.core.Drain() {
		for _, ev := range out.Events {
			cmds = append(cmds, m.applyCoreEvent(ev))
		}
		for _, job := range out.Jobs {
			cmds = append(cmds, coreCmd(job))
		}
	}
	return tea.Batch(cmds...)
}

// applyCoreEvent applies one model event to the view. Packages B and C add
// cases.
func (m *home) applyCoreEvent(ev core.Event) tea.Cmd {
	switch ev := ev.(type) {
	case core.Notice:
		if ev.Err != nil {
			return m.handleError(ev.Err)
		}
		m.errBox.SetInfo(ev.Info)
	}
	return nil
}

// newSlotView builds the view of a loaded workspace: a rail reading its
// instances, a split pane and a workbench, sized when the terminal size is
// known. Its agent sessions get their pane clients (ensureSlotPanes).
func (m *home) newSlotView(ws *core.Workspace) *workspaceSlot {
	list := ui.NewList(&m.spinner, ws)
	list.SetPanes(m.panes)
	list.SetWorkspaceName(ws.Name())
	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	splitPane.SetPanes(m.panes)
	// Pre-size components if terminal dimensions are known.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.topChromeHeight() - 2
		list.SetSize(listWidth, contentHeight)
		splitPane.SetSize(paneWidth, contentHeight)
	}
	slot := &workspaceSlot{
		ws:        ws,
		list:      list,
		splitPane: splitPane,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), splitPane.Terminal()),
	}
	m.ensureSlotPanes(slot)
	return slot
}

// slotFor returns the loaded view of ws, or nil when no slot shows it
// (its tab was closed).
func (m *home) slotFor(ws *core.Workspace) *workspaceSlot {
	for _, s := range m.openSlots() {
		if s.ws == ws {
			return s
		}
	}
	return nil
}

// slotHolding returns the loaded slot whose workspace holds inst (by
// identity), or nil.
func (m *home) slotHolding(inst *session.Instance) *workspaceSlot {
	return m.slotFor(m.core.Holding(inst))
}
```
`slotFor(nil)` returns nil. A slot's `ws` is never nil outside bare tests, and `m.core.Holding` returns nil for an unheld instance. Delete the old `slotHolding` from `app/completions.go`.

In `app/app.go`, rename the method `Update` to `update`, keeping its body and comment, and add:
```go
// Update implements tea.Model: the message's handler (update), then
// whatever the model produced meanwhile (drainCore).
func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	return model, tea.Batch(cmd, m.drainCore())
}
```
`update` handles a delivered job result:
```go
	case coreResultMsg:
		m.core.Deliver(msg.msg)
		return m, nil
```
Calls of `m.Update(...)` inside `app` stay as they are: they nest, and a nested drain is harmless. That includes `deliverGated`, until C.

`Init` returns `tea.Batch(cmds..., m.initCmd)`. The new `home` field `initCmd tea.Cmd` holds what `newHome` drained (Step 4).

- [ ] **Step 4: Rewrite the loading paths on top of core.**

`app/app_init.go`'s `newHome` builds the model first, then the classic slot over its workspace:
```go
	model, err := core.New(core.Options{Registry: registry, Program: program, Ctx: wsCtx, Config: appConfig})
	if err != nil {
		return nil, err
	}
	h := &home{
		ctx:         ctx,
		core:        model,
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		// (the remaining fields as today, minus registry)
	}
	h.panes = ui.NewPaneClients()
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	sp.SetPanes(h.panes)
	h.workspaceSlot = &workspaceSlot{
		ws:        model.Classic(),
		splitPane: sp,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
	}
	// Built after h so the list can point at h.spinner.
	h.list = ui.NewList(&h.spinner, model.Classic())
	h.list.SetPanes(h.panes)
	if wsCtx != nil && wsCtx.Name != "" {
		h.list.SetWorkspaceName(wsCtx.Name)
	}
```
Delete `newHome`'s own flag sync, `WriteLoomContextFiles`, `LoadStateFrom` and `NewStorage`: `core.New` does them, in the same order and before anything launches. Later in `newHome`:
- The startup auth probe stores its result with `h.core.SetRCAuth(session.DetectClaudeRemoteControlAuth(program, cmdExec))`.
- The classic load becomes:
  ```go
  	if !willRestoreSlots {
  		if err := h.core.LoadClassic(true); err != nil {
  			return nil, fmt.Errorf("load instances: %w", err)
  		}
  		h.ensureSlotPanes(h.workspaceSlot)
  		startupRecovery = h.ws.Recovery()
  	}
  ```
  `startupRecovery` becomes a `core.RecoverySummary`.
- The registration overlay's closure reads `h.core.Registry()` instead of `h.registry`.
- End `newHome` with `h.initCmd = h.drainCore()` before `return h, nil`. The program isn't running yet, so this applies the load's notices now and keeps their Cmds (an error's hide timer) for `Init`.

Rewrite `restoreSavedWorkspaces`:
```go
// restoreSavedWorkspaces opens the registry's saved tabs (core's
// RestoreSaved: the activations, the restore-failure bookkeeping, the
// orphan sweep, and the classic fallback when none opens) and builds their
// views, then focuses the startup workspace's tab, else the last used.
func (m *home) restoreSavedWorkspaces(saved []config.Workspace) {
	focus := m.core.RestoreSaved(saved)
	if focus < 0 {
		// No tab opened: the classic workspace was loaded in their place.
		m.ensureSlotPanes(m.workspaceSlot)
		m.showRecoverySummary(m.ws.Recovery())
		return
	}
	classic := m.workspaceSlot
	for _, ws := range m.core.Tabs() {
		m.slots = append(m.slots, m.newSlotView(ws))
	}
	m.loadSlot(focus)
	// The first tab dropped the classic slot, which this path never
	// loaded, so the release is nil in practice. Were it not, running it
	// here is safe: the program is not running yet (Run installs the pane
	// notifier after newHome), so no pump can block on Send.
	runNow(tea.Batch(releaseSlotCmd(classic), m.prunePanes()))
	m.updateTabBarStatuses()
	m.showRecoverySummary(m.slots[focus].ws.Recovery())
}
```
A notice the restore emitted is applied by `newHome`'s closing drain, which runs after `showRecoverySummary`. Today the order is different: the fallback's load error and a workspace terminal's "remote control off" line were set before the summary. To keep that order, call `h.initCmd = tea.Batch(h.initCmd, h.drainCore())` right after `h.restoreSavedWorkspaces(...)` and right after `LoadClassic` too, before `showRecoverySummary`. Delete `loadStartupStorage`, `loadSlotStorage` and `loadStartupStorageFallback` from `app`.

Rewrite `activateWorkspace`:
```go
// activateWorkspace opens a workspace as a new tab (core's OpenTab: load,
// reconcile, crash restart, workspace terminal, orphan recovery) and
// builds its view. The first tab opened from classic/global mode takes
// focus at once (see the invariant on home.workspaceSlot); later ones open
// in the background, and callers that want to show one focus it with
// loadSlot.
//
// Focusing the first tab drops the classic slot; the returned Cmd releases
// its pane clients (releaseSlotCmd, prunePanes) and is nil otherwise.
// Callers must return it (or, before the program runs, run it).
func (m *home) activateWorkspace(def config.Workspace) (tea.Cmd, error) {
	ws, err := m.core.OpenTab(def)
	if err != nil {
		return nil, err
	}
	// The load's notices (a workspace terminal launched without remote
	// control) land before anything the caller shows next, as they did when
	// the load set them itself.
	notices := m.drainCore()
	m.slots = append(m.slots, m.newSlotView(ws))
	var release tea.Cmd
	if len(m.slots) == 1 {
		// Leaving classic mode: the classic slot is not in m.slots, so the
		// invariant needs the new tab focused before we return. loadSlot
		// runs the classic slot's workbench cleanup and ratio flush first;
		// the slot is then dropped, so its attach clients go too.
		classic := m.workspaceSlot
		m.loadSlot(0)
		release = tea.Batch(releaseSlotCmd(classic), m.prunePanes())
	}
	// Force the next health tick to poll: a newly opened workspace's repo
	// wasn't in openRepoPaths() until just now, and without this the
	// poller stays silent on it until the ambient ghInterval next elapses.
	m.gate(gateGH).expedite()
	return tea.Batch(notices, release), nil
}
```
`m.gate(gateGH)` is still `app`'s until C, which changes this line.

Rewrite `deactivateWorkspace`:
```go
func (m *home) deactivateWorkspace(name string) (tea.Cmd, error) {
	idx := slices.IndexFunc(m.slots, func(s *workspaceSlot) bool { return s.ws.Name() == name })
	if idx == -1 {
		return nil, nil
	}
	if _, err := m.core.CloseTab(name); err != nil {
		return nil, err
	}
	slot := m.slots[idx]
	wasFocused := idx == m.focusedSlot
	m.slots = slices.Delete(m.slots, idx, idx+1)
	switch {
	case wasFocused:
		// m.workspaceSlot still points at the closed slot, so loadSlot's
		// departing-slot cleanup (workbench, ratio flush) lands on it.
		m.loadSlot(min(idx, len(m.slots)-1))
	case idx < m.focusedSlot:
		m.focusedSlot--
	}
	return tea.Batch(releaseSlotCmd(slot), m.prunePanes()), nil
}
```
Keep its doc comment: save failure keeps the tab, the last tab is never closed, and the Cmd must be returned.

In `applyWorkspaceToggle`, make these replacements:
- The classic save `m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))` becomes `m.core.Save(m.ws)`, with the same error switch.
- `m.restoreFailed = slices.DeleteFunc(...)` becomes `m.core.KeepRestoreFailed(desiredNames)`.
- `currentNames` is built from `slot.ws.Name()`.
- `m.saveOpenWorkspaces()` becomes `m.core.PersistOpenList()`.
- `m.slots[m.focusedSlot].recovery` becomes `.ws.Recovery()`.

`stayInGlobalMode` becomes `m.leaveFocusedSlot(); m.core.StayGlobal(); return tea.RequestWindowSize`.

`inGlobalMode` becomes `len(m.slots) == 0 && (m.wsCtx() == nil || m.wsCtx().Name == "")`.

Rewrite `enterGlobalMode`:
```go
func (m *home) enterGlobalMode() tea.Cmd {
	global, err := m.core.EnterGlobal(m.ws)
	if err != nil {
		return m.handleError(err)
	}
	notices := m.drainCore()

	// Picker escape hatch (W → Global row) from a classic workspace slot
	// reaches here with no leaveFocusedSlot of its own — clean up workbench
	// residue (wbRatio flush, split-terminal restore) and flush pending
	// ratios while the departing slot's appState is still current, or
	// handleQuit later flushes them into the new global state.json.
	m.leaveFocusedSlot()

	// The global slot keeps the departing slot's splitPane and workbench:
	// the panes are sized and wired already, and the closed tab no longer
	// uses them.
	list := ui.NewList(&m.spinner, global)
	list.SetPanes(m.panes)
	view := &workspaceSlot{ws: global, list: list, splitPane: m.splitPane, workbench: m.workbench}

	// Everything loaded so far is dropped: every tab, or — global mode
	// entered from a classic workspace slot — that slot. The focused one's
	// panes live on in the global slot.
	dropped, carried := m.openSlots(), m.workspaceSlot
	m.slots = nil
	m.focusedSlot = 0
	m.workspaceSlot = view
	m.ensureSlotPanes(view)

	m.tabBar.SetWorkspaces(nil, 0)
	// (the applyUIPrefs comment and call, verbatim)
	m.applyUIPrefs()

	// (the DetachExcept block, verbatim)

	m.showRecoverySummary(global.Recovery())

	cmds := []tea.Cmd{notices, tea.RequestWindowSize, m.instanceChanged(), staleTerminals, m.prunePanes()}
	// (the dropped-slot release loop, verbatim)
	return tea.Batch(cmds...)
}
```
Keep the function's long doc comment and update the names in it: `loadSlotStorage` → `core.Model.EnterGlobal`. The saves, load and abort rules now live in `core.Model.EnterGlobal`'s comment.

Delete these from `app/workspaces.go`: `saveOpenWorkspaces` (callers use `m.core.PersistOpenList()`) and `openWorkspaceNames` (callers use `m.core.OpenNames()`). `persistFocusedWorkspace` keeps its range check and calls `m.core.SetLastUsed(name)`, logging an error at Error as today. `slotNames` reads `slot.ws.Name()`. `removeInstanceEverywhere` becomes:
```go
func (m *home) removeInstanceEverywhere(inst *session.Instance) {
	if inst == nil {
		return
	}
	for _, ws := range m.core.Loaded() {
		ws.Remove(inst)
	}
}
```

Extend `checkSlotInvariant`. Insert this check after the duplicate-slot loop and before `if len(m.slots) == 0 { return nil }`, so it covers the classic case too:
```go
	// The slots mirror the model's workspaces: the tabs in order, or with
	// none open the classic workspace. A bare test home has no model.
	if m.core != nil {
		tabs := m.core.Tabs()
		if len(tabs) != len(m.slots) {
			return fmt.Errorf("slot invariant: %d slots, but the model has %d tabs", len(m.slots), len(tabs))
		}
		for i, s := range m.slots {
			if s.ws != tabs[i] {
				return fmt.Errorf("slot invariant: m.slots[%d] does not show the model's tab %d", i, i)
			}
		}
		if len(m.slots) == 0 && m.ws != m.core.Classic() {
			return errors.New("slot invariant: the focused slot does not show the model's classic workspace")
		}
	}
```

Rewrite `handleQuit`'s saves:
```go
	m.flushPendingRatioSaves()
	m.flushWorkbenchRatio()
	if len(m.slots) > 0 {
		m.leaveFocusedSlot()
	}
	if err := m.core.SaveForQuit(); err != nil {
		return m, m.handleError(err)
	}
	return m, tea.Quit
```
Keep the doc comment, and point it at `core.Model.SaveForQuit` for the policy. Delete `quitSkipsSave` from `app`. Its test moved in A3.

The `registerWorkspaceMsg` case becomes:
```go
	case registerWorkspaceMsg:
		// The registry has no lock, so the Add runs here on Update, never
		// in the confirmation's Cmd.
		def, err := m.core.Register(msg.name, msg.dir)
		if err != nil {
			return m, m.handleError(err)
		}
		release, err := m.activateWorkspace(def)
		if err != nil {
			return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
		}
		if err := m.core.SetLastUsed(def.Name); err != nil {
			log.For("app").Debug("registry.update_last_used_failed", "workspace", def.Name, "err", err)
		}
		// (the rest of the case, verbatim, with m.recovery → m.ws.Recovery())
```

`showRecoverySummary` takes a `core.RecoverySummary` and calls `s.Empty()`.

- [ ] **Step 5: Instance edits and the moved helpers' call sites.**

- Every `X.list.AddInstance(inst)` in production code becomes `X.ws.Add(inst)`: `runNewInstance`, `runPromptNewInstance`, `handleIssuePicked`, `handleScriptDone`. The `SetSelectedInstance(m.list.NumInstances() - 1)` that follows stays: it selects the row the list now reads.
- `X.list.RemoveInstance(inst)` becomes `X.ws.Remove(inst)` (in `dropPendingNew`, `handleInstanceStarted`).
- `X.list.ReplaceInstance(a, b)` becomes `X.ws.Replace(a, b)` (in `adoptIntoReopened`, `handleRecoverDone`).
- `saveSlot(slot)` becomes `m.core.Save(slot.ws)`. Delete `saveSlot`, and keep its comment on `core.Model.Save`.
- `slotLoaded(s)` becomes `m.core.IsLoaded(s.ws)`, `reopened(s)` becomes `m.core.Reopened(s.ws)`, and `closedOwnerNote(s)` becomes `m.core.ClosedNote(s.ws)`. Delete all three. `slotLabel(s)` becomes `s.ws.Label()`; delete it too. B moves the remaining completion code into core.
- `snapshotSaveFunc` saves through `core.Persistable`.
- `m.allInstances()` becomes `m.core.Instances()`, `m.activeInstances()` becomes `m.core.ActiveInstances()`, `activeInstance(inst)` becomes `core.ActiveInstance(inst)`, and `m.instanceForSession(name)` becomes `m.core.InstanceForSession(name)`. Delete the `app` copies.
- `rcAuthFor` reads `m.core.RCAuth()` for the default account. The other `m.rcAuth` readers (`accounts.go`, `remote_control.go`, `state_*.go`) read `m.core.RCAuth()`, and `handleAccountsRefreshed`'s write calls `m.core.SetRCAuth`.
- `m.registry` readers (the pickers in `intents.go`, `state_workspace_picker.go` and `app_init.go`'s overlay chain) read `m.core.Registry()`, and `m.restoreFailed` readers read `m.core.RestoreFailed()`.
- `reconcileOrphans`, `claimTitles`, `claimedWorktreePaths`, `recoverySummary`, `persistableInstances` and `applySessionConfig`: delete the `app` copies. Every caller is gone or now calls `core`.

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./app/ ./core/ ./ui/...`
Expected: builds. The `app` tests don't compile yet.

- [ ] **Step 6: Migrate the `app` test fixtures.**

`app/testcore_test.go`:
```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
)

// testWS builds a fixture workspace from its handles (any may be nil, as
// in a bare test home) holding insts, added in order.
func testWS(parts core.WorkspaceParts, insts ...*session.Instance) *core.Workspace {
	ws := core.NewWorkspace(parts)
	for _, inst := range insts {
		ws.Add(inst)
	}
	return ws
}

// slotOver builds a fixture slot view over ws: a rail reading it, plus the
// split pane and workbench every slot needs. wirePanes points the rail and
// pane at the test's pane registry.
func slotOver(ws *core.Workspace) *workspaceSlot {
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return &workspaceSlot{
		ws:        ws,
		list:      ui.NewList(&s, ws),
		splitPane: sp,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
	}
}

// wireCore gives a fixture home the model production builds in newHome:
// with no tab open the focused slot's workspace is the classic one,
// otherwise the tabs are m.slots' workspaces in order. A slot with no
// workspace gets an empty one (and a list reading it, if it had none). It
// keeps a model the test installed (m.core set beforehand, e.g. with a
// registry). Call it after assembling the slots and before exercising m.
func wireCore(t *testing.T, m *home) *home {
	t.Helper()
	if m.core == nil {
		m.core = core.NewForTest(core.Options{})
	}
	for _, s := range append([]*workspaceSlot{m.workspaceSlot}, m.slots...) {
		if s == nil {
			continue
		}
		if s.ws == nil {
			s.ws = testWS(core.WorkspaceParts{})
		}
		if s.list == nil {
			s.list = ui.NewList(&m.spinner, s.ws)
		}
	}
	var tabs []*core.Workspace
	for _, s := range m.slots {
		tabs = append(tabs, s.ws)
	}
	m.core.SetWorkspacesForTest(m.ws, tabs)
	return m
}
```
Then migrate the tests mechanically. The compiler lists every site.
1. **A slot literal** such as `&workspaceSlot{wsCtx: c, storage: s, appConfig: a, appState: st, list: l, splitPane: p, workbench: w}` becomes `slotOver(testWS(core.WorkspaceParts{Ctx: c, Storage: s, Config: a, State: st}))`, followed by assignments for any pane or workbench the test built itself. A list the test built with `ui.NewList(&s)` and filled with `AddInstance` becomes the workspace's instances: pass them to `testWS`, or call `slot.ws.Add`.
2. **A list edit** `X.list.AddInstance(y)` becomes `X.ws.Add(y)`, and likewise for `RemoveInstance` and `ReplaceInstance`.
3. **`newTestHome`** builds its slot with `slotOver(testWS(core.WorkspaceParts{Storage: storage, Config: config.DefaultConfig(), State: state}))` and returns `wireCore(t, wirePanes(t, h))`. `wirePanes` sets every slot's rail and split pane on the registry, so call it after the slots exist.
4. **A `home` literal** a test builds by hand gets `wireCore(t, h)` once its slots are assembled. That covers `app_test.go`, `workspace_toggle_test.go`, `overview_*_test.go` and the rest. It also applies wherever a test appends to `h.slots` or swaps `h.workspaceSlot`: call `wireCore` again after the change, which re-installs the mirror.
5. **Test seams:**
   - `h.cmdExec = x` becomes `h.core.SetExecForTest(x)`;
   - `h.registry = r` becomes `h.core.SetRegistryForTest(r)`;
   - `h.restoreFailed = names` needs a setter. Add `SetRestoreFailedForTest(names []string)` to `core/model.go` beside the others.
6. **Field reads** `slot.recovery` become `slot.ws.Recovery()`, and `m.storage` and the other handles become calls.
7. **Moved code.** A test that called `app`'s copy of a moved helper calls the `core` one, or moved in A3.

Keep every assertion. If a fixture can't be expressed this way, stop and report it.

Run: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: PASS.

### A5. Verify and commit

- [ ] **Step 1: Run the full checks.**

Run: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./session/...`
Expected: PASS.

- [ ] **Step 2: Coordinator checks (not the implementer's).** The coordinator runs these before the review:
  - `git grep -n 'AddInstance\|RemoveInstance\|ReplaceInstance' -- '*.go' ':!vendor'` prints nothing.
  - `git grep -n '\.storage\.SaveInstances' -- 'app/*.go' ':!app/*_test.go'` prints only `snapshotSaveFunc` (B moves it).
  - `CGO_ENABLED=0 go test ./core/ -run TestCoreImportsNoUI` passes.
  - Moved bodies: for each function in A3's tables and the bracketed blocks of `OpenTab`, `RestoreSaved` and `loadWorkspace`, diff the old body (`git show b81f34d:<file>`) against the new one after applying the listed edits. Any other difference must be explained.
  - Assertion count: `git grep -h 'assert\.\|require\.' -- '*_test.go' ':!vendor' | wc -l` at b81f34d against HEAD. Fewer needs a reason the reviewer accepts.

- [ ] **Step 3: Commit.**

```bash
git add core/ session/launch/ ui/ app/
git commit -m "refactor(core): the model owns workspaces and their instances" -m "core.Model holds the loaded workspaces (core.Workspace: context, storage,
config, state, instances). Loading, saving, the workspace transitions, the
sweeps and the registry writes move out of app; the TUI's slots become views
and ui.List reads its rows from the workspace. Launch options move to
session/launch so core composes the workspace terminal's program without
importing the UI. Daemon stage 1B, package A." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Hc861zmBTH48vW7MY2DvEJ"
```

**Review focus for Package A** (for the reviewer's prompt):
- Order of effects on the load paths: notices, the recovery summary, `ensureSlotPanes`, and the classic slot's release.
- The mirror invariant at every transition: `activateWorkspace`, `deactivateWorkspace`, `applyWorkspaceToggle`, `enterGlobalMode`, `restoreSavedWorkspaces`, `registerWorkspaceMsg`.
- `RestoreSaved` reading the startup workspace's name before `OpenTab` clears `classic`.
- The list's identity selection against every old `removeAt`/`AddInstance` rule, and against the overview cursor, which indexes `slot.list.GetInstances()`.
- Fixtures that null out what production fills: lesson 7. `wireCore` must not hide a missing model in a path production always has.
- Behaviour on a latched storage (`ErrStorageLoadFailed`) through `Save`, `SaveForQuit` and `EnterGlobal`.

---

## Package B: lifecycle operations and their completions

Start, kill, discard, pause, resume, restart-with-options, recover, merge and the creation flows' drop become `core.Model` methods that return the job the TUI runs. Their completions are handled in core, by identity and on the stamped owner, and report to the TUI with events. The TUI keeps what depends on focus and `m.state`: selection, inline attach, the wording of "started in X", and the terminal pane's shell close. Every `SendPrompt` leaves the Update goroutine (decision 9). One commit at the end.

B1 adds core code next to the old code and stays green. B2 is the switch.

### B1. Operations, results and completions, in core

**Files:**
- Create: `core/ops.go`, `core/completions.go`, `core/ops_test.go`
- Modify: `core/events.go`, `core/model.go` (`Deliver`), `core/testhelpers_test.go`

- [ ] **Step 1: The result types and events.**

In `core/completions.go`, start with the package comment block moved from the top of `app/completions.go`. Keep its text, but say "the TUI" for "the UI" and "workspace" for "slot". The block covers acting by identity and on the stamped owner, attaching nothing when the owner closed, and not moving the selection while another flow is on screen; the last is now the TUI's job. Then add the result types. They are exported so the TUI's tests can deliver them:
```go
// StartResult is a start job's result (Start): the instance, Start's
// error, and the workspace that owned it at dispatch.
type StartResult struct {
	Instance *session.Instance
	Owner    *Workspace
	Err      error
}

// ResumeResult is a resume that succeeded (Resume, ResumeIfLoading).
// Notice, when set, is what it found that the user must see: a stash it
// forgot or could not drop (session.Notice). A failed resume is an
// OpFailed.
type ResumeResult struct {
	Instance *session.Instance
	Owner    *Workspace
	Notice   error
}

// RecoverResult is a recover job's result (Recover). Placeholder is the
// Recoverable row it adopts, OldTitle its title; Recovered is the adopted
// instance, or Err why adoption failed.
type RecoverResult struct {
	Placeholder *session.Instance
	Owner       *Workspace
	OldTitle    string
	Recovered   *session.Instance
	Err         error
}

// KillResult is a finished kill (Kill): tmux, worktree and branch are
// gone, or best-effort gone with the failure logged. Notice is what the
// kill could not finish but the user must see (a stash entry it could
// not drop).
type KillResult struct {
	Instance *session.Instance
	Title    string
	Notice   error
}

// PauseResult is a finished pause (Pause).
type PauseResult struct {
	Title string
}

// OpFailed is a kill, discard, pause or resume that failed: the instance
// goes back to Previous so the user can retry.
type OpFailed struct {
	Instance *session.Instance
	Title    string
	Op       string
	Previous session.Status
	Err      error
}

// MergeResult is a merge job's result (Merge).
type MergeResult struct {
	Err error
}

// promptFailed is a prompt send that failed (SendPrompt).
type promptFailed struct{ err error }
```

Add to `core/events.go`:
```go
// InstancesChanged reports that instances were added, removed or replaced,
// or that a lifecycle operation moved their status. The TUI repoints its
// panes and menu at the selection (instanceChanged); with Relayout it also
// asks for the window size again (tea.RequestWindowSize), as the old
// completion did.
type InstancesChanged struct{ Relayout bool }

// ClientsStale reports that instances stopped being active (killed,
// paused, reverted): the TUI releases the pane clients no active instance
// needs (prunePanes).
type ClientsStale struct{}

// SessionLaunched reports that Instance's tmux session was (re)launched
// or reattached while a loaded workspace holds it, so any pane client
// from before watched the session it replaced: the TUI gives it a fresh
// one (replacePane).
type SessionLaunched struct{ Instance *session.Instance }

// Reactivated reports that a failed operation put Instance back to its
// previous status. A tick may have pruned its client while it was
// Deleting or Loading, so the TUI ensures one (ensurePane, a no-op unless
// the instance is active).
type Reactivated struct{ Instance *session.Instance }

// Started reports a start that succeeded. Owner is the workspace that
// holds the instance (nil when unknown), Loaded whether it is still
// loaded. The TUI attaches its client when Loaded, and selects it or says
// where it started.
type Started struct {
	Instance *session.Instance
	Owner    *Workspace
	Loaded   bool
}

// Recovered reports an orphan adopted into its placeholder's row. Owner
// and Loaded are as for Started.
type Recovered struct {
	Instance *session.Instance
	Owner    *Workspace
	Loaded   bool
}
```
Each event type gets its `func (X) coreEvent() {}` line.

Route the results in `Deliver`:
```go
	case StartResult:
		m.deliverStart(msg)
	case ResumeResult:
		m.deliverResume(msg)
	case RecoverResult:
		m.deliverRecover(msg)
	case KillResult:
		m.deliverKill(msg)
	case PauseResult:
		m.deliverPause(msg)
	case OpFailed:
		m.deliverOpFailed(msg)
	case MergeResult:
		m.notifyErr(msg.Err)
	case promptFailed:
		m.notifyErr(msg.err)
```

- [ ] **Step 2: Move the ownership helpers.**

These go into `core/completions.go`, with their comments and with `*workspaceSlot` replaced by `*Workspace`:

| From `app/completions.go` | To |
|---|---|
| `adoptIntoReopened(twin, reopened, inst)` | `(m *Model) adoptIntoReopened(twin *session.Instance, reopened *Workspace, inst *session.Instance) *Workspace`: `reopened.list.ReplaceInstance` → `reopened.Replace` |
| `reopenedTwin(owner, inst)` | `(m *Model) reopenedTwin(owner *Workspace, inst *session.Instance) (*session.Instance, *Workspace)`: walks `m.Loaded()`, `slotLabel(x)` → `x.Label()`, `s.list.GetInstanceByTitle` → `s.ByTitle` |
| `owningSlot(stamped, inst)` | `(m *Model) owningWorkspace(stamped *Workspace, inst *session.Instance) *Workspace`: falls back to `m.Holding(inst)` |
| `removeInstanceEverywhere` | `(m *Model) removeEverywhere(inst)`: the A4 version, now in core; delete it from `app` |

`startOwner`, `slotLoaded`, `reopened`, `closedOwnerNote` and `slotLabel` have core equivalents already (A3), so delete them from `app`.

- [ ] **Step 3: The completion handlers.**

```go
// deliverStart applies an async start's result to the workspace that owns
// the instance (see the note at the top of this file).
//
//   - Failure: the instance is removed from its owner by identity, the
//     owner saved, and the instance killed (worktree, tmux) off the
//     model's goroutine.
//   - Success: the owner is saved and the pending prompt (N flow) sent by
//     a job; both belong to the instance, wherever it lives. Started tells
//     the TUI, which attaches the instance's client when the owner is
//     loaded and moves its selection only when that can't retarget a flow.
//   - Owner closed and its workspace reopened meanwhile: the reopened
//     workspace reconciled the record into a Paused twin, which the
//     instance replaces on success if its tmux session survived the
//     reopen (adoptIntoReopened); on failure the twin's record owns the
//     worktree and branch, so nothing is killed.
//
// Formerly app.handleInstanceStarted's model half.
func (m *Model) deliverStart(r StartResult) {
	inst := r.Instance
	owner := m.owningWorkspace(r.Owner, inst)
	if owner != nil && !m.IsLoaded(owner) {
		if twin, reopened := m.reopenedTwin(owner, inst); twin != nil {
			if r.Err != nil {
				m.notifyErr(r.Err)
				return
			}
			if adopted := m.adoptIntoReopened(twin, reopened, inst); adopted != nil {
				owner = adopted
			}
		}
	}
	loaded := m.IsLoaded(owner)

	if r.Err != nil {
		// The save's error first: the start's is the one the error bar
		// keeps, as when both were set in this order before.
		if owner != nil {
			owner.Remove(inst)
			if err := m.Save(owner); err != nil {
				m.notifyErr(err)
			}
		}
		m.notifyErr(r.Err)
		m.emit(InstancesChanged{})
		m.spawn(killUnstarted(inst))
		return
	}

	if owner != nil {
		if err := m.Save(owner); err != nil {
			m.notifyErr(err)
			return
		}
	}
	if prompt := inst.Prompt(); prompt != "" {
		inst.SetPrompt("")
		m.spawn(sendInitialPrompt(inst, prompt))
	}
	m.emit(Started{Instance: inst, Owner: owner, Loaded: loaded})
}

// deliverResume finishes a resume. The owner may have been closed while
// it ran, so an instance no loaded workspace holds is swapped into a
// reopened copy of its workspace when there is one and its session
// survived the reopen (adoptIntoReopened). An instance a loaded workspace
// holds gets a fresh pane client (SessionLaunched); one nothing holds
// displays nothing and gets none. Formerly app.handleResumeDone.
func (m *Model) deliverResume(r ResumeResult) {
	m.notifyErr(r.Notice)
	if inst := r.Instance; inst != nil && m.Holding(inst) == nil {
		var adopted *Workspace
		if r.Owner != nil {
			if twin, reopened := m.reopenedTwin(r.Owner, inst); twin != nil {
				adopted = m.adoptIntoReopened(twin, reopened, inst)
			}
		}
		if adopted != nil {
			if err := m.Save(adopted); err != nil {
				m.notifyErr(err)
			}
			m.notifyInfo(fmt.Sprintf("%s resumed in %s", inst.Title, adopted.Label()))
		}
	}
	if inst := r.Instance; inst != nil && m.Holding(inst) != nil {
		m.emit(SessionLaunched{Instance: inst})
	}
	m.emit(InstancesChanged{Relayout: true})
}

// deliverRecover puts the adopted instance in its placeholder's row in
// the workspace that owns it, by identity, so the order and the
// selection's row are unchanged, and saves. A failure reverts the
// placeholder to Recoverable so the user can retry r. An adoption whose
// owner was closed meanwhile is saved to the closed owner's storage
// unless its workspace has been reopened (Save skips a stale copy then);
// nothing is lost either way: the adopted session keeps running on its
// worktree, which the reopened workspace's orphan discovery re-offers as
// Recoverable. Formerly app.handleRecoverDone's model half.
func (m *Model) deliverRecover(r RecoverResult) {
	owner := m.owningWorkspace(r.Owner, r.Placeholder)
	if r.Err != nil {
		// Put the row back into Recoverable so the user can retry r
		// (Recover flipped it to Loading for the spinner).
		if r.Placeholder != nil {
			if terr := r.Placeholder.TransitionTo(session.Recoverable); terr != nil {
				log.For("core").Warn("recover.revert_failed", "title", r.OldTitle, "err", terr)
			}
		}
		m.notifyErr(fmt.Errorf("recover %s: %w", r.OldTitle, r.Err))
		return
	}
	loaded := m.IsLoaded(owner)
	if owner != nil {
		if !owner.Replace(r.Placeholder, r.Recovered) {
			owner.Add(r.Recovered)
		}
		if err := m.Save(owner); err != nil {
			log.For("core").Error("recover.save_failed", "title", r.Recovered.Title, "err", err)
		}
	}
	m.emit(Recovered{Instance: r.Recovered, Owner: owner, Loaded: loaded})
}

// deliverKill removes a killed instance from every loaded workspace, by
// identity: the kill ran for seconds, and the workspace the TUI showed may
// have been switched or closed meanwhile, so a missed removal would leave
// the row stuck in Deleting with its resources already gone.
func (m *Model) deliverKill(r KillResult) {
	m.removeEverywhere(r.Instance)
	m.notifyErr(r.Notice)
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
}

// deliverPause finishes a pause; Pause already stashed, killed the session
// and saved.
func (m *Model) deliverPause(PauseResult) {
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
}

// deliverOpFailed reverts the instance of a failed kill, discard, pause or
// resume to its previous status. That status came from the same instance,
// so the reverse transition should always be allowed; if the state machine
// rejects it, log and leave the status as it is rather than mask a real
// bug. The result carries the instance itself, never a title: the
// workspace the TUI shows may have changed since the operation started.
func (m *Model) deliverOpFailed(r OpFailed) {
	if r.Instance != nil {
		if terr := r.Instance.TransitionTo(r.Previous); terr != nil {
			log.For("core").Warn("revert_transition_failed", "err", terr)
		}
		m.emit(Reactivated{Instance: r.Instance})
	}
	log.For("core").Error("op_failed", "op", r.Op, "title", r.Title, "err", r.Err)
	m.notifyErr(r.Err)
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
}
```

- [ ] **Step 4: The operations, in `core/ops.go`.**

```go
// Start returns the job launching inst (Instance.Start(true)), reporting a
// StartResult stamped with its owner: the loaded workspace holding it,
// else fallback (the workspace the TUI shows). The owner is resolved now,
// by identity, not when the start lands, because a script's deferred
// action can change focus while a creation flow is open. Formerly
// app.startOwner and the creation flows' Async bodies.
func (m *Model) Start(inst *session.Instance, fallback *Workspace) Job {
	owner := m.Holding(inst)
	if owner == nil {
		owner = fallback
	}
	return func() any {
		return StartResult{Instance: inst, Owner: owner, Err: inst.Start(true)}
	}
}
```

`(m *Model) Kill(ws *Workspace, inst *session.Instance, beforeKill func()) (pre func(), job Job)` is `app.killActionFor` moved, with these edits:
- `storage := ws.storage` replaces `splitPane, storage := m.splitPane, m.storage`. `ws` is the workspace the TUI shows, whose storage holds the record.
- The terminal-pane block (`splitPane.DetachTerminalForInstance` … `ts.Close()`) becomes `if beforeKill != nil { beforeKill() }`, in the same place: after the checked-out check and before `inst.Kill()`.
- `transitionFailedMsg{inst: x, title: t, op: o, previousStatus: p, err: e}` becomes `OpFailed{Instance: x, Title: t, Op: o, Previous: p, Err: e}`.
- `killInstanceMsg{…}` becomes `KillResult{Instance: inst, Title: title, Notice: notice}`.
- The doc comment gains this paragraph: "beforeKill, when set, runs in the job after the checks pass and before the kill: the TUI closes its terminal pane's shell for the instance there, which it can't hand to the model (the pane is the TUI's)."

`(m *Model) Pause(ws *Workspace, inst *session.Instance, beforePause func()) Job` is `app.pauseActionFor` moved:
- `saveFunc := m.saveSnapshot(ws)`.
- The terminal block becomes `if beforePause != nil { beforePause() }`.
- The results become `OpFailed` and `PauseResult{Title: pauseTitle}`.

`app.snapshotSaveFunc` becomes:
```go
// saveSnapshot returns a save of ws's instances for Pause and Resume, to
// run in their job. It snapshots the membership now, on the model's
// goroutine, because the instance list must not be read from the job;
// status filtering still happens at save time (Persistable), so the state
// changes Pause and Resume make themselves are captured.
func (m *Model) saveSnapshot(ws *Workspace) func() error {
	storage := ws.storage
	snapshot := append([]*session.Instance(nil), ws.insts...)
	return func() error {
		return storage.SaveInstances(Persistable(snapshot))
	}
}
```

```go
// Resume moves the Paused inst to Loading, so the list shows the spinner
// while Resume's worktree and tmux setup runs, and returns the job
// resuming it (a ResumeResult, or OpFailed). TransitionTo enforces
// Paused→Loading atomically, so a concurrent reconcile flip between the
// caller's precondition check and this write can't start a resume on a
// non-Paused instance; when it refuses, Resume returns nil and nothing
// runs. ws is the workspace the TUI shows: the save goes to its storage,
// and it stamps the result when no loaded workspace holds inst. Formerly
// the core of app.runResumeSelected.
func (m *Model) Resume(ws *Workspace, inst *session.Instance) Job {
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		return nil
	}
	return m.ResumeIfLoading(ws, inst)
}

// ResumeIfLoading returns the job of a resume whose Loading transition the
// caller makes itself (the restart-with-options flow's confirmation). The
// save and the owner are taken now; the job resumes only if inst is
// Loading when it runs (the caller's transition may have failed, e.g. a
// concurrent reconcile flip), and returns nil otherwise. Formerly
// app.runRestartWithOptionsSelected's Async body.
func (m *Model) ResumeIfLoading(ws *Workspace, inst *session.Instance) Job {
	saveFunc := m.saveSnapshot(ws)
	title := inst.Title
	owner := m.Holding(inst)
	if owner == nil {
		owner = ws
	}
	return func() any {
		if inst.GetStatus() != session.Loading {
			return nil
		}
		return resumeOutcome(inst, title, owner, inst.Resume(saveFunc))
	}
}
```
`app.resumeResult` moves as `resumeOutcome(inst *session.Instance, title string, owner *Workspace, err error) any`, returning `ResumeResult` or `OpFailed` (Op `"resume"`, Previous `session.Paused`).

`Resume` keeps today's ordering. `runResumeSelected` transitioned and then snapshotted the save, and here the snapshot is taken after the transition too. That doesn't matter, since `Persistable` filters at save time.

```go
// Recover moves the Recoverable placeholder inst to Loading, for the
// spinner, and returns the job adopting its orphan: it serializes the
// placeholder, flips the record to Running and runs
// session.ReconcileAndRestore (adopting the worktree, spawning tmux),
// reporting a RecoverResult owned by ws, the workspace showing inst. nil
// when the transition is refused. Formerly the core of
// app.runRecoverSelected.
func (m *Model) Recover(ws *Workspace, inst *session.Instance) Job {
	cfgDir := ""
	if ws.ctx != nil {
		cfgDir = ws.ctx.ConfigDir
	}
	data := inst.ToInstanceData()
	data.Status = session.Running
	oldTitle := inst.Title
	cmdExec := cmd2.MakeExecutor()
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("recover.skipped", "err", err)
		return nil
	}
	return func() any {
		recovered, err := session.ReconcileAndRestore(data, cfgDir, cmdExec)
		return RecoverResult{Placeholder: inst, Owner: ws, OldTitle: oldTitle, Recovered: recovered, Err: err}
	}
}
```
On error `Recovered` is whatever `ReconcileAndRestore` returned, which `deliverRecover` never reads.

`(m *Model) Merge(target, source *session.Instance) Job` is `app.mergeActionFor` moved. It returns `MergeResult{Err: …}`, wrapping the worktree error as `fmt.Errorf("merge: %w", err)` exactly as today, and `MergeResult{}` on success.

```go
// DropUnstarted removes a creation flow's never-started instance from the
// workspace holding it, by identity, and returns the job killing it
// (worktree, tmux) off the model's goroutine; nil when inst is nil or has
// started (a started instance is no longer pending; never kill a live
// session from a cancel). Formerly the core of app.dropPendingNew.
func (m *Model) DropUnstarted(inst *session.Instance) Job {
	if inst == nil || inst.Started() {
		return nil
	}
	if ws := m.Holding(inst); ws != nil {
		ws.Remove(inst)
	}
	return killUnstarted(inst)
}

// killUnstarted returns the job running the blocking Kill of an instance
// already removed from every list (an aborted creation flow, a failed
// start); a failure is only logged. Formerly app.backgroundKillCmd.
func killUnstarted(inst *session.Instance) Job {
	if inst == nil {
		return nil
	}
	return func() any {
		if err := inst.Kill(); err != nil {
			log.For("core").Error("background_instance_kill_failed", "err", err)
		}
		return nil
	}
}

// SendPrompt returns the job typing prompt into inst's agent pane and
// pressing Enter (Pane().SendPrompt: load-buffer, paste-buffer, a 100ms
// pause, Enter: three tmux subprocesses that must not block the TUI). A
// failure comes back as a notice. For text the user sends to a running
// session: the prompt overlay, the quick input bar, a workbench review.
func (m *Model) SendPrompt(inst *session.Instance, prompt string) Job {
	return func() any {
		if err := inst.Pane().SendPrompt(prompt); err != nil {
			return promptFailed{err: err}
		}
		return nil
	}
}

// sendInitialPrompt is the start completion's send of an N flow's
// prompt. A failure is logged, as it was when the completion sent it
// on the TUI's goroutine.
func sendInitialPrompt(inst *session.Instance, prompt string) Job {
	return func() any {
		if err := inst.Pane().SendPrompt(prompt); err != nil {
			log.For("core").Error("send_prompt_failed", "err", err)
		}
		return nil
	}
}
```

- [ ] **Step 5: Core tests for the new seams.**

`core/ops_test.go`:
```go
package core

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeliverStart_FailureRemovesSavesAndKills pins the failed start's
// order: the instance leaves its owner by identity, the start's error is
// the last notice (the error bar keeps it), the TUI repoints its panes,
// and the kill runs as a job.
func TestDeliverStart_FailureRemovesSavesAndKills(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws, storedWorkspace(t, "b")})

	boom := errors.New("boom")
	m.Deliver(StartResult{Instance: inst, Owner: ws, Err: boom})

	out := m.Drain()
	assert.False(t, ws.Holds(inst))
	require.NotEmpty(t, out.Events)
	assert.Equal(t, Notice{Err: boom}, out.Events[len(out.Events)-2])
	assert.Equal(t, InstancesChanged{}, out.Events[len(out.Events)-1])
	assert.Len(t, out.Jobs, 1, "the failed instance is killed off the model's goroutine")
}

// TestDeliverStart_SuccessSendsThePromptByJob: the N flow's prompt no
// longer blocks the caller; it is cleared at once (a later save never
// re-sends it) and typed by a job.
func TestDeliverStart_SuccessSendsThePromptByJob(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	inst.SetPrompt("do the thing")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Deliver(StartResult{Instance: inst, Owner: ws})

	out := m.Drain()
	assert.Empty(t, inst.Prompt())
	assert.Len(t, out.Jobs, 1)
	assert.Equal(t, []Event{Started{Instance: inst, Owner: ws, Loaded: true}}, out.Events)
}

// TestDeliverOpFailed_Reverts pins a failed resume's revert (Loading back
// to Paused) and its events.
func TestDeliverOpFailed_Reverts(t *testing.T) {
	m := NewForTest(Options{})
	inst := pausedInst(t, "x")
	require.NoError(t, inst.TransitionTo(session.Loading))
	err := errors.New("no")
	m.Deliver(OpFailed{Instance: inst, Title: "x", Op: "resume", Previous: session.Paused, Err: err})
	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, []Event{Reactivated{Instance: inst}, Notice{Err: err}, InstancesChanged{}, ClientsStale{}}, m.Drain().Events)
}

func TestDropUnstarted_RemovesAndKillsOnlyAnUnstartedInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := NewWorkspace(WorkspaceParts{})
	pending, live := newInst(t, "pending"), pausedInst(t, "live")
	ws.Add(pending)
	ws.Add(live)
	m.SetWorkspacesForTest(ws, nil)

	assert.NotNil(t, m.DropUnstarted(pending), "an unstarted instance is killed by a job")
	assert.False(t, ws.Holds(pending))
	assert.Nil(t, m.DropUnstarted(live), "a cancel never kills a started session")
	assert.True(t, ws.Holds(live))
	assert.Nil(t, m.DropUnstarted(nil))
}
```
Move `storedWorkspace` from `core/persist_test.go` to `core/testhelpers_test.go`, next to `pausedInst`.

Run: `CGO_ENABLED=0 go vet ./core/ && CGO_ENABLED=0 go test ./core/ ./app/`
Expected: PASS.

### B2. The switch: the TUI dispatches operations and applies the events

**Files:**
- Modify: `app/completions.go` (becomes the event appliers plus `dropPendingNew`), `app/core_glue.go`, `app/app.go`, `app/intents.go`, `app/state_prompt.go`, `app/state_issue_picker.go`, `app/state_quick_interact.go`, `app/workbench.go`, `app/state_launch_options.go`, tests in `app/` and `core/`

- [ ] **Step 1: The appliers.**

Add to `applyCoreEvent`:
```go
	case core.InstancesChanged:
		cmd := m.instanceChanged()
		if ev.Relayout {
			return tea.Batch(tea.RequestWindowSize, cmd)
		}
		return cmd
	case core.ClientsStale:
		return m.prunePanes()
	case core.SessionLaunched:
		return m.replacePane(ev.Instance)
	case core.Reactivated:
		m.ensurePane(ev.Instance)
	case core.Started:
		return m.applyStarted(ev)
	case core.Recovered:
		return m.applyRecovered(ev)
```

`app/completions.go` keeps `dropPendingNew` and gains the two appliers. Replace its top comment with this note:
```go
// The model (core) applies async lifecycle completions by identity and on
// the workspace stamped at dispatch. What is left here depends on what the
// TUI shows: which slot is focused, and m.state. A completion never moves
// the focused slot's selection while another flow is on screen
// (m.state != stateDefault): a creation flow, an inline attach or a prompt
// acts on the selection, so moving it would retarget the flow.

// applyStarted follows a successful start in the view: the instance's
// client is attached when its owner is loaded, and the selection moves to
// it (focusing the agent pane in inline attach) only when its owner is the
// focused slot and no other flow is on screen; otherwise a notice says
// where it started. Formerly app.handleInstanceStarted's view half.
func (m *home) applyStarted(ev core.Started) tea.Cmd {
	inst, owner := ev.Instance, ev.Owner
	var attach tea.Cmd
	if ev.Loaded {
		attach = m.replacePane(inst)
	}
	switch {
	case owner == nil:
		// Unknown owner (unstamped, and no loaded workspace holds it):
		// only the model's half applies.
	case !ev.Loaded:
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s, %s", inst.Title, owner.Label(), m.core.ClosedNote(owner)))
	case owner != m.ws:
		// A background slot's selection drives no open flow.
		if s := m.slotFor(owner); s != nil {
			s.list.SelectInstance(inst)
		}
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s", inst.Title, owner.Label()))
	case m.state != stateDefault || !m.ws.Holds(inst):
		// Another flow owns the screen and acts on the selection; leave
		// both alone. (The second test is a belt: the owner is stamped by
		// identity, so a focused owner holds inst.)
		m.errBox.SetInfo(fmt.Sprintf("%s started", inst.Title))
	default:
		m.list.SelectInstance(inst)
		// Auto-focus agent pane and capture input
		m.setPaneFocus(ui.FocusAgent)
		m.splitPane.SetInlineAttach(true)
		m.state = stateInlineAttach
		m.menu.SetState(ui.StateInlineAttach)
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)
}

// applyRecovered follows an adoption in the view: the recovered instance
// is selected where that can't retarget an open flow, its client attached
// when its owner is loaded, and the recovery confirmed, since it is
// otherwise invisible when fast. The degraded case is spelled out: when
// the tmux session and worktree were both already gone, adoption could only
// mark the record Paused, and resume rebuilds the worktree from the branch.
// Formerly app.handleRecoverDone's view half.
func (m *home) applyRecovered(ev core.Recovered) tea.Cmd {
	owner := ev.Owner
	if owner != nil && (owner != m.ws || m.state == stateDefault) {
		if s := m.slotFor(owner); s != nil {
			s.list.SelectInstance(ev.Instance)
		}
	}
	var attach tea.Cmd
	if ev.Loaded {
		attach = m.replacePane(ev.Instance)
	}
	where := ""
	if owner != nil && owner != m.ws {
		where = " in " + owner.Label()
		if !ev.Loaded {
			where += ", " + m.core.ClosedNote(owner)
		}
	}
	if ev.Instance.GetStatus() == session.Paused {
		m.errBox.SetInfo(fmt.Sprintf("Recovered '%s'%s as paused — its session and worktree were gone; branch preserved, press r to resume", ev.Instance.Title, where))
	} else {
		m.errBox.SetInfo(fmt.Sprintf("Recovered session '%s'%s", ev.Instance.Title, where))
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)
}

// dropPendingNew removes the open creation flow's pending instance from
// its workspace, by identity, and returns a Cmd that kills it off the
// Update goroutine (core's DropUnstarted); nil when no creation flow is
// open. Every creation-flow cancel path goes through it. Killing "the
// selection" or "the last row" instead could reach an unrelated session
// once a completion or removal had moved either mid-flow.
func (m *home) dropPendingNew() tea.Cmd {
	inst := m.pendingNew
	m.pendingNew = nil
	return coreCmd(m.core.DropUnstarted(inst))
}
```
Delete the rest of `app/completions.go`.

- [ ] **Step 2: Dispatch through core.**

In `app/core_glue.go`, add:
```go
// closeTerminalFor returns the step kill and pause run in their job before
// touching the instance: closing the focused split pane's terminal shell
// for title (its loom_term_* session), which ends that shell. The pane is
// the TUI's; it is captured here on Update, and the job only runs the
// returned func. op names the operation in the log ("kill", "pause").
func (m *home) closeTerminalFor(title, op string) func() {
	splitPane := m.splitPane // the owning slot's, captured on Update
	return func() {
		if ts := splitPane.DetachTerminalForInstance(title); ts != nil {
			if err := ts.Close(); err != nil {
				log.For("app").Error(op+".terminal_close_failed", "title", title, "err", err)
			}
		}
	}
}
```

Make these changes in `app/intents.go`:
- **`runKillSelected` / `runKillSelectedNoConfirm`:** `preAction, job := m.core.Kill(m.ws, selected, m.closeTerminalFor(selected.Title, "kill"))`, then `killAction := coreCmd(job)`. The rest is unchanged. Delete `killActionFor`.
- **`runStashSelectedOpts`:** `pauseAction := coreCmd(m.core.Pause(m.ws, selected, m.closeTerminalFor(selected.Title, "pause")))`. Delete `pauseActionFor` and `snapshotSaveFunc`.
- **`runResumeSelected`:**
  ```go
  	selected := m.list.GetSelectedInstance()
  	job := m.core.Resume(m.ws, selected)
  	if job == nil {
  		return m, nil
  	}
  	return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged(), coreCmd(job))
  ```
  Keep the comment about flipping to Loading, and point it at `core.Model.Resume`. Delete `resumeResult`.
- **`runRestartWithOptionsSelected`:** replace `saveFunc := snapshotSaveFunc(m)` and `owner := m.startOwner(selected)` with `resumeJob := m.core.ResumeIfLoading(m.ws, selected)`. The Async becomes `tea.Batch(tea.RequestWindowSize, coreCmd(resumeJob))`. The guard comment about Sync's transition moves to `ResumeIfLoading`'s doc comment, which already states it.
- **`runRecoverSelected`:**
  ```go
  	job := m.core.Recover(m.ws, m.list.GetSelectedInstance())
  	if job == nil {
  		return m, nil
  	}
  	return m, tea.Batch(coreCmd(job), m.instanceChanged())
  ```
- **The merge picker's commit** (`app/state_mergepicker.go`, or wherever `mergeActionFor` is called): `coreCmd(m.core.Merge(target, source))`. Delete `mergeActionFor`.

`app/state_prompt.go` and `app/state_issue_picker.go` (`openLaunchOptionsForNew`): replace `owner := m.startOwner(x) // stamped for instanceStartedMsg` with `startJob := m.core.Start(x, m.ws) // owner stamped now`, and the Async with `tea.Batch(tea.RequestWindowSize, coreCmd(startJob))`.

`app/state_prompt.go`'s send to a running session:
```go
			// Regular flow: instance already running, just send the prompt,
			// off the Update goroutine (three tmux subprocesses and a pause).
			// The overlay closes now; a failed send comes back as an error.
			send := coreCmd(m.core.SendPrompt(selected, prompt))
```
and its return becomes `tea.Batch(send, tea.Sequence(tea.RequestWindowSize, …showHelpScreenMsg…))`.

`app/state_quick_interact.go`: the agent target becomes `send = coreCmd(m.core.SendPrompt(selected, text))`, returned with `tea.RequestWindowSize`. The terminal target (`SendTerminalPrompt`) stays as it is: that's the TUI's terminal pane.

`app/workbench.go`'s `sendReviewCmd`: the confirmation's `Sync` becomes `Async: coreCmd(m.core.SendPrompt(sel, prompt))`. Update the function's comment, which says the send runs on the main goroutine.

In `app/app.go`, delete these message types: `killInstanceMsg`, `transitionFailedMsg`, `pauseInstanceMsg`, `backgroundCleanupDoneMsg`, `resumeDoneMsg`, `recoverDoneMsg` and `instanceStartedMsg`. Delete their `update` cases and `backgroundKillCmd` too. The `error` case stays for other Cmds that return errors; push still does until C.

- [ ] **Step 3: Migrate the tests.**

Tests that injected completion messages now deliver core results through `Update`:
```go
// deliver hands m a core job's result as the runtime would, returning the
// Cmd the update produced (handler plus drained events).
func deliver(t *testing.T, m *home, result any) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(coreResultMsg{msg: result})
	return cmd
}
```
(Put this in `app/testcore_test.go`.) The mapping:

| Old message | New result |
|---|---|
| `instanceStartedMsg{instance, err, slot, selectedBranch}` | `core.StartResult{Instance, Err, Owner: slot.ws}`; `selectedBranch` was never read, so drop it |
| `resumeDoneMsg{instance, slot, notice}` | `core.ResumeResult{Instance, Owner: slot.ws, Notice}` |
| `recoverDoneMsg{oldTitle, recovered, err, placeholder, slot}` | `core.RecoverResult{OldTitle, Recovered, Err, Placeholder, Owner: slot.ws}` |
| `killInstanceMsg{inst, title, notice}` | `core.KillResult{Instance, Title, Notice}` |
| `pauseInstanceMsg{title}` | `core.PauseResult{Title}` |
| `transitionFailedMsg{inst, title, op, previousStatus, err}` | `core.OpFailed{Instance, Title, Op, Previous, Err}` |

A test that called a moved function goes the same way. `killActionFor(m, inst)` becomes `m.core.Kill(m.ws, inst, nil)`, and its job runs directly. `pauseActionFor` and `resumeResult` work like that too. A test of `reopenedTwin`, `adoptIntoReopened` or `owningSlot` that only checks model state moves to `core/` and builds its workspaces with `storedWorkspace` and `SetWorkspacesForTest`. A test of the selection, inline attach or the notices stays in `app` and goes through `deliver`.

`flow_selection_test.go`'s `reopenedHome` reconciles a twin through the real `ReconcileAndRestore` path (lesson 14). Keep that path. Build the reopened slot's workspace with `core.OpenTab` or `testWS`, whichever it used for its list before, and call `wireCore` after appending it.

Run: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: PASS.

### B3. Verify and commit

- [ ] **Step 1:** Run `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./session/...`. Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - `git grep -n 'SendPrompt(' -- 'app/*.go' ':!app/*_test.go'` prints only `core.SendPrompt` call sites.
  - `git grep -n 'instanceStartedMsg\|resumeDoneMsg\|recoverDoneMsg\|killInstanceMsg\|transitionFailedMsg' -- '*.go'` prints nothing.
  - The moved-body diff for `killActionFor` → `Kill`, `pauseActionFor` → `Pause`, `reopenedTwin`, `adoptIntoReopened`, `mergeActionFor` → `Merge`, and the two halves of each completion against the originals.
  - The assertion count against A's commit.
  - `TestCoreImportsNoUI`.
- [ ] **Step 3: Commit.**

```bash
git add core/ app/
git commit -m "refactor(core): lifecycle operations and completions move into the model" -m "Start, kill, pause, resume, recover, merge and the creation flows' drop are
core.Model methods returning the job the TUI runs; their completions apply in
core by identity and on the stamped workspace, and reach the TUI as events.
Prompts are sent by jobs, off the Update goroutine (1A follow-up 6). Daemon
stage 1B, package B." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Hc861zmBTH48vW7MY2DvEJ"
```

**Review focus for Package B:**
- Every completion's order of effects against the old handler:
  - the notices (which one the error bar keeps);
  - whether `instanceChanged` runs, and when;
  - replace/ensure/prune;
  - `RequestWindowSize`.
- `beforeKill` running after the checks and before `Kill`, as the terminal close did.
- `Start`'s owner taken at the same moment `startOwner` was.
- The prompt overlay closing before its send. On failure that is a behavior change: the old code kept the overlay open, so the reviewer must judge it acceptable or flag it.
- That nothing in a job reads model state (`saveSnapshot` and `Recover` take theirs on the caller's goroutine).

---

## Package C: the health tick and the background jobs

The health tick splits along the pane boundary (decision 8). `pollGate` moves to core with every job it throttles: the roster query, hook scans, the GitHub poll and parity, the accounts reload and refresh, and the usage probe. With them goes the state those jobs fill: the roster, `ghState`, the account registry, auth, sync reports, usage, and the dirty set. The TUI keeps the pane-driven status ladder (decision 10), and the views of accounts and GitHub state, which read core's accessors. One commit at the end.

C1 adds core's copy and stays green. C2 is the switch.

**A rule for the whole package.** A moved function that used to return a `tea.Cmd` puts its jobs and events in the outbox instead. Some call sites placed that Cmd in the middle of their own work, e.g. `newLaunchOptionsOverlay`'s `reloaded := m.reloadAccounts()`. Such a site calls the core method and then `m.drainCore()` right away, using the drained Cmd where the old one was. The views then refresh before the caller goes on, as they did before.

### C1. Core's half of the background work

**Files:**
- Create: `core/gate.go`, `core/tick.go`, `core/claude_status.go`, `core/hook_scan.go`, `core/github.go`, `core/accounts.go`, `core/usage.go`
- Modify: `core/model.go` (fields, `Deliver`, `Begin`), `core/events.go`

- [ ] **Step 1: The gate.**

`core/gate.go` is `app/pollgate.go` adapted to jobs, minus `gateRatioSave`: the split-ratio flush is the TUI's, and C2 gives it a flag. Keep `pollGate`, `due`, `expedite`, `request` and their comments verbatim, including the two invariants.
```go
// gateKind names one gated background job. A gated job's result carries
// its kind, so delivery never depends on which gate value a copy holds.
type gateKind int

const (
	// gateRoster throttles the `claude agents --json` query (maybeRosterQuery).
	gateRoster gateKind = iota
	// gateHookScan throttles the hook-event scan (maybeHookScan).
	gateHookScan
	// gateGH throttles the GitHub poll (maybeGHQuery).
	gateGH
	// gateUsage throttles the account usage probes (maybeUsageProbe).
	gateUsage
	// gateAccountsRefresh keeps one account auth refresh in flight
	// (maybeAccountsRefresh).
	gateAccountsRefresh

	numGateKinds
)
```
`String` keeps its cases except `ratio_save`. `gateIntervals` keeps its entries except `gateRatioSave` and its comment. The gate array goes on `Model`: `gates [numGateKinds]pollGate`, with the field comment from `home`.

```go
// gate resolves kind to the model's gate for it.
func (m *Model) gate(kind gateKind) *pollGate { return &m.gates[kind] }

// gateDue reports whether kind's gate is due at now under its interval.
func (m *Model) gateDue(kind gateKind, now time.Time) bool {
	return m.gate(kind).due(now, gateIntervals[kind])
}

// gatedResult carries a gated job's result back to Deliver, which disarms
// the gate for kind and then delivers result as if it had arrived on its
// own.
type gatedResult struct {
	kind   gateKind
	result any
}

// dispatchGated calls build only when kind's gate is due at now. A nil job
// from build arms nothing: no job means no result to disarm the gate.
// Otherwise it arms the gate and queues the job, its result wrapped in a
// gatedResult. Reports whether it dispatched. build runs on the model's
// goroutine, so it may read model state; the job it returns must not.
// (A job returns one value, so the old rule against a tea.Batch result is
// gone: nothing can hide a second result from the disarm.)
func (m *Model) dispatchGated(kind gateKind, now time.Time, build func() Job) bool {
	if !m.gateDue(kind, now) {
		return false
	}
	job := build()
	if job == nil {
		return false
	}
	g := m.gate(kind)
	g.inFlight = true
	g.last = now
	m.spawn(func() any { return gatedResult{kind: kind, result: job()} })
	return true
}

// deliverGated disarms r's gate before anything else, then delivers the
// inner result. A nil result still disarms. A request() made during the
// flight dispatches the job once more, after the result is handled.
func (m *Model) deliverGated(r gatedResult) {
	g := m.gate(r.kind)
	g.inFlight = false
	again := g.pending
	g.pending = false
	m.Deliver(r.result)
	if again {
		m.redispatch(r.kind)
	}
}

// redispatch runs kind's job again for a request() made mid-flight. Only
// the jobs that request() have an entry.
func (m *Model) redispatch(kind gateKind) {
	switch kind {
	case gateRoster:
		m.maybeRosterQuery(m.ActiveInstances())
	case gateHookScan:
		m.maybeHookScan(m.ActiveInstances())
	case gateUsage:
		m.maybeUsageProbe()
	case gateAccountsRefresh:
		m.maybeAccountsRefresh()
	}
}
```
`Deliver` gains `case gatedResult: m.deliverGated(msg)`. Every `maybe…` below returns `bool` (dispatched) instead of a Cmd.

- [ ] **Step 2: The events.**

Add to `core/events.go`:
```go
// StatusesChanged reports that instance statuses may have moved (a probe,
// a hook scan, a roster answer): the TUI refreshes its tab statuses and
// peer sections (updateTabBarStatuses).
type StatusesChanged struct{}

// Alive lists instances whose tmux session a probe found alive. The TUI
// re-attaches the client of any whose client is not attached (a reattach
// failed after a full-screen attach, or the client's pump hit EOF on a
// session since relaunched under its name), unless a full-screen attach
// owns it. Source names the probe for the TUI's log ("tick",
// "dead_event").
type Alive struct {
	Instances []*session.Instance
	Source    string
}

// HealthChecked reports that a health tick's probe landed and was
// applied: the TUI arms the next tick then, so probes never overlap.
type HealthChecked struct{}

// GitHubChanged reports a GitHub poll applied: the TUI refreshes an open
// issue picker.
type GitHubChanged struct{}

// AccountsChanged reports that the account registry, an account's auth,
// sync report or usage changed: the TUI refreshes every view showing
// accounts (refreshAccountViews) and whether account UI shows at all
// (ui.SetShowAccounts).
type AccountsChanged struct{}
```

- [ ] **Step 3: Claude status and hook scans.**

These move to `core/claude_status.go` with their comments. All `app` → `core` edits are in the Conventions. A function that returned a Cmd now returns what the table says and queues its job through `dispatchGated`.

| From `app` | To `core` | Edits |
|---|---|---|
| `rosterReadyMsg` | `rosterResult` | unexported; fields unchanged |
| `rosterQueryCmd(active, dirs)` | `rosterQueryJob(active, dirs) Job` | returns `Job`; the result is `rosterResult` |
| `rosterInterval`, `promptingRosterSpacing` | same | |
| `maybeRosterQuery(active)` | `(m *Model) maybeRosterQuery(active) bool` | `dispatchGated(gateRoster, …, func() Job { return rosterQueryJob(active, m.accountDirs()) })` |
| `rosterStatusFor` | `(m *Model) rosterStatusFor` | reads `m.roster`, `m.rosterByAccount` |
| `adoptClaudeStatus` | `(m *Model) AdoptClaudeStatus` | exported: the TUI's ladder calls it too (the lockstep rule) |
| `applyClaudeStatus` | `(m *Model) applyClaudeStatus` | |
| `observeRoster(at)` | `(m *Model) observeRoster(at)` | `m.activeInstances()` → `m.ActiveInstances()`; `m.updateTabBarStatuses()` → `m.emit(StatusesChanged{})` |
| `maybeRosterQuerySoon` | `(m *Model) maybeRosterQuerySoon() bool` | |
| `statusEligible` | `StatusEligible` | exported for the ladder |
| the `rosterReadyMsg` case of `update` | `(m *Model) deliverRoster(r rosterResult)` | the case body verbatim, with `log.DebugKV("app.roster…")` → `log.DebugKV("core.roster…")` |
| `markDirty`, `takeDirty`, `dirtySessions` | `(m *Model) MarkOutput(name)`, `takeDirty`, `dirtySessions` field | `MarkOutput` is the exported name of `markDirty` |
| `roster`, `rosterByAccount` fields | `Model` fields | with their comments |

Into `core/hook_scan.go`, the same way: `hookScanInterval`, `hookScanResult`, `hookScanMsg` (becomes `hookScanResults`), `hookScanCmd` (becomes `hookScanJob`), `maybeHookScan`, and `handleHookScan` (becomes `deliverHookScan`). In `deliverHookScan`, `m.updateTabBarStatuses()` becomes `m.emit(StatusesChanged{})`, and its trailing `return m.maybeRosterQuery(…)` becomes a call.

Two new methods carry what the pane events did for the model:
```go
// PaneOutput is the TUI's report that inst's pane produced output (a pane
// dirty event). Answering a permission prompt makes output (the dialog
// goes away) but fires no hook, and the roster reports busy at once, so a
// Prompting Claude session asks the roster soon. While Claude works its
// spinner keeps output flowing, so this also reads a UserPromptSubmit
// within hookScanInterval. Call it before the TUI's own status ladder
// moves inst: it reads the status the output arrived in.
func (m *Model) PaneOutput(inst *session.Instance) {
	if inst.GetStatus() == session.Prompting && session.IsClaudeProgram(inst.Program()) {
		m.maybeRosterQuerySoon()
	}
	if inst.HooksLaunched() {
		m.maybeHookScan(m.ActiveInstances())
	}
}

// PaneQuiet is the TUI's report that inst's pane went quiet (nil for a
// pane no instance owns). Stop and PermissionRequest arrive as output
// settles, often in a burst's last output, so it scans even inside
// hookScanInterval or while a scan is in flight: request().
func (m *Model) PaneQuiet(inst *session.Instance) {
	if inst == nil || !inst.HooksLaunched() {
		return
	}
	m.gate(gateHookScan).request()
	m.maybeHookScan(m.ActiveInstances())
}
```

- [ ] **Step 4: The tick's model half, in `core/tick.go`.**

```go
// maxWorkspaceTerminalRestartFailures: (move the constant and its comment
// from app/app.go verbatim)

// livenessSource names the path a liveness result reached applyLiveness
// by, for its logs: the health tick's probe or a pane's Dead event.
type livenessSource string

const (
	fromTick      livenessSource = "tick"
	fromDeadEvent livenessSource = "dead_event"
)

// ProbeResult is one instance's health probe: its tmux liveness and, for
// a live one, whether its diff refresh failed. Exported so the TUI's tests
// can deliver a HealthResult.
type ProbeResult struct {
	Instance *session.Instance
	TmuxLive tmux.Liveness
	DiffErr  error
}

// HealthResult is a health tick probe's result (Tick).
type HealthResult struct {
	Results []ProbeResult
}

// DeadVerified is the probe a pane's Dead event asks for (VerifyDead): a
// dead attach PTY does not always mean a dead session.
type DeadVerified struct {
	Instance *session.Instance
	TmuxLive tmux.Liveness
}

// Tick runs the health tick's model half: it queues the probe of every
// active instance (liveness, parity, diff stats; its result is applied by
// deliverHealth, which ends with HealthChecked) and every background job
// that is due: the roster query, the hook scan, the GitHub poll, the
// accounts file check and the usage probe. selected is the TUI's selected
// instance, whose full diff the probe refreshes. In event mode (the
// emulator path) the tick is a slow belt-and-braces sweep, because status
// rides pane events; on the snapshot path it keeps the legacy 500ms
// cadence. Formerly the lifecycle half of the tickUpdateMetadataMessage
// case.
func (m *Model) Tick(selected *session.Instance) {
	active := m.ActiveInstances()
	// Fan out I/O off the model's goroutine. A stalled tmux or git process
	// must not block it: the probe waits for its goroutines inside the job.
	m.spawn(probeJob(active, selected, m.takeDirty(), m.ghBases))
	// (the four "maybe" comments and calls from the old tick case, in the
	// same order: roster, hook scan, GitHub, accounts reload, usage, each
	// now a plain call: m.maybeRosterQuery(active), m.maybeHookScan(active),
	// m.maybeGHQuery(), m.maybeReloadAccounts(), m.maybeUsageProbe())
}

// probeJob fans out the per-instance I/O (tmux liveness, parity, git
// diffs) across goroutines and waits for all of them inside the job, so a
// stalled subprocess delays the next tick instead of freezing anything.
//
// Diff refresh is gated on pane output (Instance.ShouldRefreshDiff over
// the dirty set MarkOutput fills): an idle instance with no output does
// not run git on every tick. For N active instances with one active
// agent, the git fan-out drops from about N subprocesses per tick to one.
// On the snapshot path the TUI's status scan reports output through
// MarkOutput, so a change it sees refreshes the diff on the next tick.
// Formerly gatherMetadataCmd, minus the pane reads, which are the TUI's.
func probeJob(active []*session.Instance, selected *session.Instance, dirty map[string]bool, bases map[string]string) Job {
	return func() any {
		results := make([]ProbeResult, len(active))
		var wg sync.WaitGroup
		for i, inst := range active {
			wg.Add(1)
			go func(idx int, instance *session.Instance) {
				defer wg.Done()
				r := &results[idx]
				r.Instance = instance
				r.TmuxLive = instance.Pane().TmuxLiveness()
				if r.TmuxLive != tmux.LivenessAlive {
					return
				}
				// (the parity comment and UpdateParity call, verbatim)
				instance.UpdateParity(bases[instance.Path])

				wantFull := instance == selected
				if !instance.ShouldRefreshDiff(dirty[instance.Pane().TmuxSessionName()], wantFull) {
					return
				}
				if wantFull {
					r.DiffErr = instance.UpdateDiffStats()
				} else {
					r.DiffErr = instance.UpdateDiffStatsShort()
				}
			}(i, inst)
		}
		wg.Wait()
		return HealthResult{Results: results}
	}
}

// deliverHealth applies a health probe: dead sessions are paused (or a
// workspace terminal relaunched), and Claude's reported status applies to
// the live ones. Hook scans and roster answers already moved them when
// they landed; applying the report here again covers a move TransitionTo
// refused then. TransitionTo still validates, so an illegal transition is
// rejected rather than forced. The TUI's snapshot-path ladder never
// overrides a reported status (it asks AdoptClaudeStatus first).
func (m *Model) deliverHealth(r HealthResult) {
	var alive []*session.Instance
	for _, p := range r.Results {
		if !m.applyLiveness(p.Instance, p.TmuxLive, fromTick) {
			continue
		}
		alive = append(alive, p.Instance)
		if target, authoritative := m.AdoptClaudeStatus(p.Instance); authoritative {
			if err := p.Instance.TransitionTo(target); err != nil {
				log.For("core").Warn("tick.transition_failed", "instance", p.Instance.Title, "to", target.String(), "err", err.Error())
			}
		}
		if p.DiffErr != nil {
			log.For("core").Warn("diff_stats_update_failed", "err", p.DiffErr)
		}
	}
	m.emit(StatusesChanged{})
	m.emit(Alive{Instances: alive, Source: string(fromTick)})
	m.emit(HealthChecked{})
}

// VerifyDead returns the probe a pane's Dead event asks for, on inst's
// tmux session (a DeadVerified result). A dead attach PTY does not always
// mean a dead session: a failed reattach leaves the session alive, and a
// session relaunched under the same name leaves the old client's pump at
// EOF. The probe tells pause-the-instance from repair-the-client.
func (m *Model) VerifyDead(inst *session.Instance) Job {
	return func() any {
		return DeadVerified{Instance: inst, TmuxLive: inst.Pane().TmuxLiveness()}
	}
}

// deliverDeadVerified applies a Dead event's probe like a tick's, for one
// instance.
func (m *Model) deliverDeadVerified(r DeadVerified) {
	if !StatusEligible(r.Instance) {
		return
	}
	alive := m.applyLiveness(r.Instance, r.TmuxLive, fromDeadEvent)
	m.emit(StatusesChanged{})
	if alive {
		m.emit(Alive{Instances: []*session.Instance{r.Instance}, Source: string(fromDeadEvent)})
	}
	m.emit(InstancesChanged{})
}
```
`Deliver` routes `HealthResult` and `DeadVerified`.

`applyLiveness` moves from `app/app.go` as `(m *Model) applyLiveness(inst *session.Instance, tmuxLive tmux.Liveness, source livenessSource) bool`, keeping its comments, with these edits:
- `m.slotHolding(inst) == nil` becomes `m.Holding(inst) == nil`. The comment there is rewritten: "The probe was taken before inst's workspace was dropped. A workspace-terminal restart here would relaunch one nothing displays. Drop the result."
- The workspace-terminal restart returns `false` after `m.emit(SessionLaunched{Instance: inst})`, where it used to return `m.replacePane(inst)`.
- The client-repair tail (`if !ptmxAlive && inst != m.attachingInstance { … }`) is deleted, so the function ends `inst.ResetRestartFailures(); return true`. The repair is the TUI's, on the `Alive` event (C2).
- The doc comment loses its client-repair half and says that `Alive` carries it.

- [ ] **Step 5: GitHub and push, in `core/github.go`.**

Move with comments: `ghInterval`, `ghPollBudget`, `ghRecheckInterval`, `ghAvailability`, `ghPollRequest`, `ghReadyMsg` (becomes `ghResult`), `openRepoPaths`, `baseBranchByRepo`, `linkedIssues`, `maybeGHQuery` (returns `bool`), `ghPollCmd` (becomes `ghPollJob`, returning `Job`), `handleGHReady` (becomes `deliverGH`), `baseFor` and `applyGitHubState`. Also the fields `ghAvailable`, `ghState`, `ghErrs` and `ghBases`. Edits:
- **`openRepoPaths` and `baseBranchByRepo`.** With no tab open they read the classic workspace: `m.repoPath()` there was the process's working directory in classic mode, so use `os.Getwd()` (ignore the error, as `repoPath` did) and `m.classic.cfg`. Otherwise they walk `m.tabs`, reading `ws.ctx.RepoPath` and `ws.cfg.GetBaseBranch()`.
- **`linkedIssues`** walks `m.Instances()`.
- **`deliverGH`** ends with `m.emit(GitHubChanged{})` in place of the issue-picker refresh.

Add the accessors the TUI reads, and the expedite and push:
```go
// GitHubSnapshot returns the latest poll's snapshot of repo, if the last
// poll of it succeeded.
func (m *Model) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	s, ok := m.ghState[repo]
	return s, ok
}

// GitHubErr returns the last poll's error for repo, or nil.
func (m *Model) GitHubErr(repo string) error { return m.ghErrs[repo] }

// GitHubUnavailable reports that gh was checked and found unusable
// (missing, or not logged in).
func (m *Model) GitHubUnavailable() bool { return m.ghAvailable.checked && !m.ghAvailable.ok }

// ExpediteGitHub makes the next tick poll GitHub at once (an in-flight
// poll still lands first): after a push, an issue-born session, a newly
// opened workspace.
func (m *Model) ExpediteGitHub() { m.gate(gateGH).expedite() }

// Push returns the job committing and pushing inst's worktree, reporting
// a pushResult: an error becomes a notice, a success expedites the GitHub
// poll so the PR badge follows. Formerly app.pushActionFor.
func (m *Model) Push(inst *session.Instance) Job
```
`Push`'s body is `pushActionFor`'s, returning `pushResult{err: err}` where it returned `err`, and `pushResult{}` where it returned `ghRefreshMsg{}`. `deliverPush` does `m.notifyErr(r.err)` on error and `m.ExpediteGitHub()` on success.

`OpenTab` now expedites itself: move the `m.gate(gateGH).expedite()` line and its comment from `app.activateWorkspace` to the end of `core.Model.OpenTab`, as `m.ExpediteGitHub()`.

- [ ] **Step 6: Accounts and usage, in `core/accounts.go` and `core/usage.go`.**

The model owns the registry and everything read from it. The TUI keeps the views: the account strip, the Settings rows, the Launch Options choices, and `topChromeHeight`. It also keeps the interactive `claude auth login` (`tea.ExecProcess`).

| From `app/accounts.go` | Fate |
|---|---|
| `accountUsage` | core, unexported |
| `initAccounts` | core `InitAccounts()`: loads and adopts the registry. The load error and `warnIfRunningAsAccount`'s message become `m.notifyErr`. The credential-override log stays. The TUI creates its `accountStrip` itself (C2) |
| `credentialOverrideWarning` | stays in app (a view string) |
| `accountsScreenNotice` | stays in app, with `m.core.HasExtraAccounts()` |
| `warnIfRunningAsAccount`, `adoptAccounts`, `accountsFileStamp`, `statAccountsFile`, `accountsSignature`, `noteAccountsState`, `maybeReloadAccounts` (now returns nothing: it calls `ReloadAccounts`, whose effects go to the outbox), `ensureAccountMaps`, `accountDirs`, `syncMainDir`, `extraAccountAuth`, `accountUsers`, `slotStateDir` (becomes `wsStateDir(ws *Workspace)`), `canonicalDir` | core, unexported |
| `hasExtraAccounts`, `rcAuthFor`, `claudeProgram`, `mainConfigDir`, `accountLoggedOut`, `accountsLoaded` | core, exported: `HasExtraAccounts`, `RCAuthFor`, `ClaudeProgram`, `MainConfigDir`, `AccountLoggedOut`, `AccountsLoaded` |
| `publishAccounts` | core: `session.SetAccountDirs(…)` as today, then `m.emit(AccountsChanged{})` in place of `ui.SetShowAccounts` |
| `reloadAccounts` | core `ReloadAccounts()` |
| `accountsChanged` | core: `m.refreshAccountViews()` becomes `m.emit(AccountsChanged{})`, `m.handleError(x)` becomes `m.notifyErr(x)`, and `requestAccountsRefresh` becomes a call |
| `accountStatuses`, `refreshAccountViews`, `accountChoices`, `hasAccountChoice`, `removedAccountNotice`, `newLaunchOptionsOverlay`, `applyChosenLaunch`, `accountOrDefault`, `topChromeHeight`, `accountRows` | stay in app, reading core (below) |
| `accountsRefreshedMsg`, `accountsRefreshCmd`, `requestAccountsRefresh`, `maybeAccountsRefresh`, `handleAccountsRefreshed` | core: `accountsRefreshed`, `accountsRefreshJob`, `RequestAccountsRefresh(withDefault bool)`, `maybeAccountsRefresh() bool`, `deliverAccountsRefreshed`. The handler's `m.refreshAccountViews()` becomes `m.emit(AccountsChanged{})`, and its `m.rcAuth = …` stays as is: `rcAuth` is a model field since A |
| `afterAccountsChanged` | core, unexported: `noteAccountsState`, `publishAccounts` (which emits), `RequestUsageProbe()` |
| `accountLoginDoneMsg`, `accountLoginCmd` | stay in app; read `m.core.ClaudeProgram()`, `m.core.AccountEnv(acct)` and `m.core.Account(acct)` |
| `handleAccountRequest`, `carryOutAccountRequest` | stay in app as the dispatch, calling the core requests below. A returned error goes through `m.handleError`; an add then runs `accountLoginCmd(name)` |
| fields `accounts`, `accountAuth`, `accountSync`, `usage`, `accountsStamp`, `accountsSeen`, `syncRefusalLogged`, `refreshDefaultAuth` | `Model` fields, with their comments |

The core requests, carrying `carryOutAccountRequest`'s bodies:
```go
// AddAccount creates account name (linked against the main config dir)
// and records its first sync report. The TUI then runs its login.
func (m *Model) AddAccount(name string) (string, error)

// SetDefaultAccount makes name the account new sessions preselect.
func (m *Model) SetDefaultAccount(name string) error

// RemoveAccount removes account name, refusing while a session uses it
// (accountUsers) or while its dir holds unshared files (the error then
// names them and the CLI command that can force it; this path never
// forces).
func (m *Model) RemoveAccount(name string) error

// AccountEnv is the environment a command runs with as account name.
func (m *Model) AccountEnv(name string) ([]string, error)

// Account returns the registered account name.
func (m *Model) Account(name string) (account.Account, bool)

// AccountSync returns name's last link report.
func (m *Model) AccountSync(name string) (account.SyncReport, bool)

// AccountUsage returns name's latest usage sample and the last probe's
// error (a failed probe keeps the last good sample).
func (m *Model) AccountUsage(name string) (account.Usage, error)

// Accounts is the registry for the TUI's views to read (nil before
// InitAccounts); writes go through the requests above.
func (m *Model) Accounts() *account.Registry
```
Each request starts with what `handleAccountRequest` did first: reload, then act. An unavailable registry returns `errors.New("the account registry is unavailable")`. Each ends with `afterAccountsChanged` where the old body returned it. Keep every error text verbatim. `accountUsers` walks `m.Loaded()`, reading `ws.insts` and `ws.storage`.

From `app/usage.go`: `usageInterval`, `usageTarget`, `usageReadyMsg` (becomes `usageResult`), `usageProbeCmd` (becomes `usageProbeJob`), `maybeUsageProbe` (returns `bool`), `requestUsageProbe` (becomes `RequestUsageProbe()`), `handleUsageReady` (becomes `deliverUsage`, with `m.emit(AccountsChanged{})` in place of `m.refreshAccountViews()`), and `lostAccess`.

One more method, for `Init`:
```go
// Begin starts the background jobs the TUI's first frame wants, each when
// due: an accounts refresh and a usage probe.
func (m *Model) Begin() {
	m.maybeAccountsRefresh()
	m.maybeUsageProbe()
}
```
`Deliver` routes `rosterResult`, `hookScanResults`, `ghResult`, `pushResult`, `accountsRefreshed` and `usageResult`, each to its `deliver…`.

- [ ] **Step 7: Build.**

Run: `CGO_ENABLED=0 go vet ./core/ && CGO_ENABLED=0 go test ./core/ ./app/`
Expected: PASS. `app` still uses its own copies.

### C2. The switch: the TUI reports pane events and applies the results

**Files:**
- Modify: `app/app.go`, `app/app_init.go`, `app/events.go`, `app/core_glue.go`, `app/accounts.go`, `app/state_issue_picker.go`, `app/state_prompt.go`, `app/intents.go`, `app/workspaces.go`, `app/settings*.go` (wherever account requests are dispatched), tests in `app/` and `core/`
- Delete: `app/pollgate.go`, `app/hook_scan.go`, `app/usage.go`, and the moved parts of `app/events.go`, `app/github.go` and `app/accounts.go`

- [ ] **Step 1: The appliers.**

Add to `applyCoreEvent`:
```go
	case core.StatusesChanged:
		m.updateTabBarStatuses()
	case core.Alive:
		for _, inst := range ev.Instances {
			if inst == m.attachingInstance || m.panes.For(inst).Attached() {
				continue
			}
			// The session exists but its attach client is not attached (a
			// reattach failed after full-screen attach returned, or the
			// client's pump hit EOF on a session that has since been
			// relaunched under the same name). Self-heal here: the same
			// shape as the workspace-terminal restart, at the client layer.
			log.For("app").Warn("pane.client_dead_repairing", "title", inst.Title, "source", ev.Source)
			m.ensurePane(inst)
		}
	case core.HealthChecked:
		// A user parked on the workbench's diff tab generates none of the
		// nav traffic that refreshes the diff in focus mode, so ride the
		// health tick: re-render from the just-updated diff stats so the
		// tab tracks the agent's work live.
		if m.viewMode == viewWorkbench && m.workbench != nil && m.workbench.Tab() == ui.WbTabDiff {
			if selected := m.list.GetSelectedInstance(); selected != nil {
				m.workbench.Diff().SetDiff(selected)
			}
		}
		return tickUpdateMetadataCmd
	case core.GitHubChanged:
		if p := m.issuePicker(); p != nil {
			p.SetRows(m.issueRows())
			p.SetStatus(m.issuePickerStatus())
		}
	case core.AccountsChanged:
		ui.SetShowAccounts(m.core.HasExtraAccounts())
		return m.refreshAccountViews()
```

- [ ] **Step 2: The tick.**

The `tickUpdateMetadataMessage` case becomes:
```go
	case tickUpdateMetadataMessage:
		// (the ErrBox expiry comment and call, verbatim)
		m.errBox.ExpireIfDue(time.Now())

		// Close the clients of sessions that stopped being active since the
		// last tick (paused, killed, exited, or their slot closed).
		cmds := []tea.Cmd{m.prunePanes()}

		selected := m.list.GetSelectedInstance()
		// (the inline-attach liveness backstop, verbatim)

		// The model's half: liveness, parity, diff stats and the background
		// jobs. Its probe's result re-arms this tick (core.HealthChecked),
		// so ticks never overlap a probe still running.
		m.core.Tick(selected)

		// The status ladder on the snapshot path reads each pane's screen,
		// which only the TUI's clients have.
		if scan := m.snapshotScan(); scan != nil {
			cmds = append(cmds, scan)
		}

		// (the workbench follow-scan block, verbatim)
		return m, tea.Batch(cmds...)
```
Delete the `metadataReadyMsg` case, `metadataReadyMsg`, `metadataResult`, `gatherMetadataCmd`, and `paneSnapshot` in `app/panes.go`, whose only caller was the tick. Then add the snapshot scan:
```go
// snapshotStatusMsg carries the snapshot path's status scan back to Update.
type snapshotStatusMsg struct{ results []snapshotStatus }

// snapshotStatus is one pane's scan: whether its content changed and
// whether it shows a prompt.
type snapshotStatus struct {
	instance  *session.Instance
	updated   bool
	hasPrompt bool
	err       error
}

// snapshotScan returns a Cmd scanning, off the Update goroutine, the
// screen of every active instance whose pane client has no emulator (the
// snapshot path: LOOM_PANE_RENDERER=snapshot, or Windows); nil when there
// is none, or a scan is still running. With the emulator, quiet events
// drive the ladder (statusDetectedMsg), and a pane with no client has no
// screen to scan and no opinion. Formerly gatherMetadataCmd's capture.
func (m *home) snapshotScan() tea.Cmd {
	if m.snapshotScanning {
		return nil
	}
	type target struct {
		inst *session.Instance
		pane ui.Pane
	}
	var targets []target
	for _, inst := range m.core.ActiveInstances() {
		if pane := m.panes.For(inst); pane.Client() != nil && !pane.HasEmulator() {
			targets = append(targets, target{inst, pane})
		}
	}
	if len(targets) == 0 {
		return nil
	}
	m.snapshotScanning = true
	return func() tea.Msg {
		results := make([]snapshotStatus, len(targets))
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func(i int, t target) {
				defer wg.Done()
				r := &results[i]
				r.instance = t.inst
				r.updated, r.hasPrompt, r.err = t.pane.DetectStatus()
			}(i, t)
		}
		wg.Wait()
		return snapshotStatusMsg{results: results}
	}
}
```
`home` gains `snapshotScanning bool`: "a snapshot scan is in flight; cleared when its result lands. Update-goroutine only." Handle the result in `update`:
```go
	case snapshotStatusMsg:
		m.snapshotScanning = false
		for _, r := range msg.results {
			if !core.StatusEligible(r.instance) {
				continue
			}
			// A reported Claude status is the model's: its tick applies it.
			if _, authoritative := m.core.AdoptClaudeStatus(r.instance); authoritative {
				continue
			}
			// Same transition ladder as the event path: still-changing →
			// Running; settled with a prompt → Prompting; settled → Ready.
			target := session.Ready
			if r.updated {
				// Output: the next tick refreshes the diff (MarkOutput).
				m.core.MarkOutput(r.instance.Pane().TmuxSessionName())
				target = session.Running
			} else if r.hasPrompt {
				target = session.Prompting
			}
			if err := r.instance.TransitionTo(target); err != nil {
				log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", target.String(), "err", err.Error())
			}
			if r.err != nil {
				log.WarnKV("app.tick.capture_failed", "instance", r.instance.Title, "err", r.err.Error())
			}
		}
		m.updateTabBarStatuses()
		return m, nil
```
A failed capture still runs the ladder on its zero values before logging, as the old tick did. Keep that order, and keep the comment that explains it.

- [ ] **Step 3: The pane events.**

`paneDirtyMsg`:
```go
	case paneDirtyMsg:
		m.core.MarkOutput(msg.session)
		selected := m.list.GetSelectedInstance()
		if inst := m.core.InstanceForSession(msg.session); inst != nil {
			m.core.PaneOutput(inst)
			// (the Ready→Running promotion block and its comment, verbatim:
			// it is the pane ladder, the TUI's until 1C)
			if selected != nil && inst == selected {
				if err := m.splitPane.UpdateAgent(selected); err != nil {
					return m, m.handleError(err)
				}
			}
			return m, nil
		}
		// (the terminal-pane branch, verbatim)
```
Move the two comments that explained the roster-soon and hook-scan triggers to `core.Model.PaneOutput`, where the code now is.

`paneQuietMsg`:
```go
	case paneQuietMsg:
		inst := m.core.InstanceForSession(msg.session)
		m.core.PaneQuiet(inst)
		if !core.StatusEligible(inst) {
			// (the Loading comment, verbatim)
			if inst != nil && inst.GetStatus() == session.Loading {
				return m, m.maybeRedetect(msg.session)
			}
			return m, nil
		}
		return m, statusDetectCmd(inst, m.panes.For(inst))
```
`redetectMsg` and `statusDetectedMsg` keep their bodies, with `statusEligible` → `core.StatusEligible`, `m.adoptClaudeStatus` → `m.core.AdoptClaudeStatus` and `m.instanceForSession` → `m.core.InstanceForSession`.

`ptyDeadMsg`: keep the inline-attach exit and the guard, then `cmds = append(cmds, coreCmd(m.core.VerifyDead(inst)))`. Delete `deadVerifiedMsg`, `verifyDeadCmd`, and the `deadVerifiedMsg` case: core delivers `DeadVerified`.

Delete the cases for `gatedMsg`, `hookScanMsg`, `rosterReadyMsg`, `ghReadyMsg`, `accountsRefreshedMsg` and `usageReadyMsg`. Make these replacements:
- the `ghRefreshMsg` case and type: `m.core.ExpediteGitHub()` at their senders;
- the `accountLoginDoneMsg` case: `m.core.RequestAccountsRefresh(msg.name == account.DefaultName)` and `m.core.RequestUsageProbe()` replace the two Cmds;
- `m.gate(gateGH).expedite()` (in `handleIssuePicked`, `handleIssueExpanded`, `activateWorkspace`): `m.core.ExpediteGitHub()`. Delete the `activateWorkspace` one, because `OpenTab` expedites now.

- [ ] **Step 4: Ratio save, Init, newHome.**

Ratio save loses its gate:
```go
// maybeArmRatioSave arms the one-shot flush tick when resizeSplit has
// recorded pending ratios and no tick is already in flight. (keep the
// rest of the old comment: THROTTLE, not a debounce; called from
// handleScriptDone)
func (m *home) maybeArmRatioSave() tea.Cmd {
	if m.ratioTickArmed || len(m.pendingRatioSaves) == 0 {
		return nil
	}
	m.ratioTickArmed = true
	return tea.Tick(ratioSaveDelay, func(time.Time) tea.Msg { return ratioSaveMsg{} })
}
```
`home` gains `ratioTickArmed bool`: "the split-ratio flush tick is in flight; cleared when it lands. Update-goroutine only." The `ratioSaveMsg` case clears it first, then flushes. Delete `home.gates`, `app/pollgate.go`, `gateKind` and the rest from `app`.

`Init`:
```go
	m.core.Begin()
	cmds := []tea.Cmd{m.spinner.Tick, tickUpdateMetadataCmd, m.initCmd, m.drainCore()}
```
In `newHome`, replace `h.initAccounts()` with:
```go
	h.accountStrip = ui.NewAccountStrip()
	h.core.InitAccounts()
	// The registry's notices (a load error, loom running as an account)
	// land before the startup load's, as they did when set directly.
	h.initCmd = tea.Batch(h.initCmd, h.drainCore())
```
Its `h.hasExtraAccounts()` becomes `h.core.HasExtraAccounts()`.

- [ ] **Step 5: The views read core.**

In `app/accounts.go`, rewrite the remaining view functions over the accessors:
- `m.accounts` becomes `m.core.Accounts()`;
- `m.usage[name]` becomes `m.core.AccountUsage(name)`;
- `m.accountSync[s.Name]` becomes `m.core.AccountSync(s.Name)`;
- `m.rcAuthFor` becomes `m.core.RCAuthFor`;
- `m.accountLoggedOut` becomes `m.core.AccountLoggedOut`;
- `m.accountsLoaded()` becomes `m.core.AccountsLoaded()`;
- `m.hasExtraAccounts()` becomes `m.core.HasExtraAccounts()`.

Then:
- **`newLaunchOptionsOverlay`** starts with `m.core.ReloadAccounts(); reloaded := m.drainCore()`, per the rule at the top of this package.
- **Request callers.** Everywhere a Cmd from `requestUsageProbe()` or `requestAccountsRefresh(…)` was batched, call `m.core.RequestUsageProbe()` or `m.core.RequestAccountsRefresh(…)` and drop the Cmd from the batch: the end-of-Update drain runs the job. The sites are the Launch Options openers in `intents.go`, `state_prompt.go`, `state_issue_picker.go` and `accountLoginDoneMsg`.
- **Settings.** `runOpenSettings` and every other `reloadAccounts()` caller does the same: `m.core.ReloadAccounts()`, then `m.drainCore()` where its Cmd was used.

In `app/state_issue_picker.go`:
- `issueRows` and `issuePickerStatus` read `m.core.GitHubSnapshot(m.repoPath())` and `m.core.GitHubErr(m.repoPath())`.
- The `#123` shorthand check in `state_prompt.go` becomes `!m.core.GitHubUnavailable()`.
- `runNewFromIssue`'s own reads of `ghState` and `ghAvailable` go through the same accessors.

In `app/intents.go`, push becomes `coreCmd(m.core.Push(selected))`. Delete `pushActionFor`.

Delete everything that moved from `app/events.go` (Step 3 of C1 lists it) and from `app/github.go`. What remains of `app/github.go` (nothing, if the issue picker's helpers are in `state_issue_picker.go`) is deleted with it.

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./app/ ./core/`
Expected: builds.

- [ ] **Step 6: Migrate the tests.**

These test files test moved code, so they move to `core/` (package `core`):
- `app/pollgate_test.go`, without its ratio-save cases;
- `app/roster_status_test.go` and `app/claude_status_test.go`, except the tests that drive `statusDetectedMsg` or the snapshot ladder;
- `app/hook_scan_test.go`, except pane-event integration that needs `home`;
- `app/github_test.go`, except the issue picker's view tests;
- `app/accounts_refresh_test.go` and `app/accounts_reload_test.go`, except the views;
- the usage tests;
- the health-tick tests of `applyLiveness`'s model half: `liveness_unknown_test.go` and `workspace_terminal_restart_circuit_test.go`.

Their fixtures become core fixtures. Add them to `core/testhelpers_test.go` as needed:
```go
// activeInst builds a started, Running instance titled title on a mock
// tmux session (no tmux server contacted), held by a workspace installed
// in m as its classic one.
func activeInst(t *testing.T, m *Model, title string) *session.Instance
```
Build it the way the moved tests built theirs. They used a mock `tmux.Session` through `session` test seams, e.g. `startedInstanceWithProgram` in `app/status_redetect_test.go`. Copy the construction, and keep `NewForTest` plus `SetWorkspacesForTest` as the model. Inside the tests:
- `m.Update(gatedMsg{kind: k, msg: x})` becomes `m.Deliver(gatedResult{kind: k, result: x})`;
- a `maybe…` returning non-nil becomes the `bool` it returns now, with the queued jobs inspected through `m.Drain().Jobs`;
- `m.gate(k)` stays the same;
- message types are renamed as in C1.

Tests staying in `app` get the same treatment as B's. A tick test delivers `core.HealthResult{Results: []core.ProbeResult{…}}` through `deliver`, and asserts the TUI's half:
- client repair on `Alive`, for `metadata_ptmx_repair_test.go` (`setupPtmxDeadFixture`);
- the re-armed tick;
- the snapshot ladder (`snapshotStatusMsg`).

Ratio-save tests (`app/ratio_save_test.go`) assert `ratioTickArmed` where they asserted the gate. `TestApplyLiveness_UnknownDoesNotPause` keeps its client assertion as an `app` test: deliver a `HealthResult` with `LivenessUnknown` and assert that no repair runs and the status is unchanged. Its pause half moves to core.

Run: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: PASS.

### C3. Verify and commit

- [ ] **Step 1:** Run `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./session/...`, then `go test -tags e2e ./e2e/...`. Expected: PASS. The e2e suite covers `TestE2E_FakeClaudeHooksDriveStatus` (hooks and roster end to end).
- [ ] **Step 2: Coordinator checks.**
  - `git grep -n 'gatedMsg\|pollGate\|dispatchGated' -- 'app/*.go'` prints nothing.
  - `ls app/pollgate.go app/hook_scan.go app/usage.go` fails: the files are deleted.
  - The moved-body diff for every function in C1's tables.
  - The assertion count against B's commit.
  - `TestCoreImportsNoUI`.
- [ ] **Step 3: Commit.**

```bash
git add core/ app/
git commit -m "refactor(core): the health tick and background jobs move into the model" -m "core's tick probes liveness, parity and diffs and runs every gated job
(roster, hook scans, GitHub and push, accounts, usage) with the state they
fill; the TUI's tick keeps what needs a pane client: prune, client repair on
core.Alive, the inline-attach backstop and the snapshot path's status scan.
Pane events report output and quiet to the model. Daemon stage 1B, package C." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Hc861zmBTH48vW7MY2DvEJ"
```

**Review focus for Package C:**
- Tick cadence. A tick never overlaps its own probe (`HealthChecked` re-arms it), and the snapshot scan never stacks (`snapshotScanning`).
- Gating invariants: arm only on a job, and disarm on every delivery, including a `nil` result and an unknown one. Check that `Deliver`'s default case can't swallow a `gatedResult`.
- The lockstep rule: every status path goes through `AdoptClaudeStatus`. These include `statusDetectedMsg`, the snapshot ladder, `deliverHealth` and `applyClaudeStatus`.
- The snapshot path's diff gating, now a tick late.
- Account views refreshing before a caller reads them (the drain rule), and the order of startup notices.
- `ui.SetShowAccounts` following every registry change.
- The roster's fail-closed rules and the newest-wins merge are unchanged.

---

## Package D: documentation and verification

### D1. CLAUDE.md

**Files:** Modify: `CLAUDE.md`

- [ ] **Step 1: Find every stale reference.**

Run:
```bash
git grep -n -w -e activateWorkspace -e deactivateWorkspace -e enterGlobalMode -e restoreSavedWorkspaces \
  -e loadSlotStorage -e loadStartupStorage -e loadStartupStorageFallback -e reconcileOrphans -e claimTitles \
  -e persistableInstances -e saveSlot -e startOwner -e owningSlot -e slotHolding -e reopenedTwin \
  -e adoptIntoReopened -e handleInstanceStarted -e handleResumeDone -e handleRecoverDone -e killActionFor \
  -e pauseActionFor -e pushActionFor -e mergeActionFor -e snapshotSaveFunc -e backgroundKillCmd \
  -e removeInstanceEverywhere -e instanceStartedMsg -e resumeDoneMsg -e recoverDoneMsg -e killInstanceMsg \
  -e transitionFailedMsg -e applyLiveness -e gatherMetadataCmd -e metadataReadyMsg -e dispatchGated \
  -e gatedMsg -e pollGate -e maybeRosterQuery -e rosterQueryCmd -e adoptClaudeStatus -e applyClaudeStatus \
  -e observeRoster -e maybeHookScan -e maybeGHQuery -e handleGHReady -e maybeReloadAccounts -e reloadAccounts \
  -e accountsRefreshCmd -e maybeUsageProbe -e rcAuthFor -e markDirty -e statusEligible -e activeInstances \
  -e instanceForSession -e workspaceSlot -e restoreFailed -e applyLaunchOptions \
  -e launchOptionsFromConfig -e ParseLaunchOptions -- CLAUDE.md docs/specs/scripting.md
```
Every hit names a function that moved or was renamed, or a file that no longer holds it. Fix each one so it names where the code lives now (`core/…`, `core.Model.X`, `session/launch`), keeping the sentence's meaning. Then apply the section changes below.

- [ ] **Step 2: Architecture.**
- **Core Flow.** `main.go` → `app/app.go` (the Bubble Tea model, the view) → `core/` (`core.Model`, the session model) → `session/instance.go`. Add a short paragraph: the TUI drives the model synchronously on its Update goroutine; blocking work is a `core.Job` the TUI runs as a Cmd and hands back with `Deliver`; the model reports through `core.Event`s that `home.Update` drains after every message (`drainCore`, `applyCoreEvent`).
- **Key Packages.** Add a `core/` bullet with:
  - what it owns, using the "What moves where" table above;
  - the outbox (jobs and events);
  - focus staying in the TUI (decision 6);
  - `TestCoreImportsNoUI`.

  Add a `session/launch/` bullet: launch options and their composition and decoding. In the `app/` bullet, "controller layer" becomes "the view and key routing; lifecycle goes through `core`". In the `ui/` bullet, `list.go` reads its rows from an `InstanceSource` (the slot's `core.Workspace`) and keeps the selection by identity.

- [ ] **Step 3: Gotchas.**
- **The focused workspace slot is embedded, not copied.** Rewrite it around the 1B shape:
  - A slot is a view over a `core.Workspace`: `ws`, `list`, `splitPane`, `workbench`. The context, storage, config and state are read through `m.wsCtx()`, `m.storage()`, `m.appConfig()`, `m.appState()`.
  - `m.slots` mirrors `core.Model.Tabs()`, and with no tab open the focused slot shows `Classic()`. `checkSlotInvariant` checks that too.
  - Core owns the transitions (`OpenTab`, `CloseTab`, `EnterGlobal`, `StayGlobal`, `RestoreSaved`). The TUI builds and drops the views around them, and runs `loadSlot`'s teardown as before.
  - Keep every rule that still holds: `loadSlot` as the only focus change, the drop sites' release Cmds, and never assigning a promoted field to switch workspaces. That last rule is now enforced by there being no fields to assign.
- **Destructive actions target instances by identity; async completions act on the slot stamped at dispatch.** The rules stand; the names change:
  - the result types are `core.StartResult`, `ResumeResult`, `RecoverResult`, `KillResult` and `OpFailed`, stamped with `*core.Workspace`;
  - completions apply in `core/completions.go`;
  - `app/completions.go` holds the view's half (`applyStarted`, `applyRecovered`) and `dropPendingNew`;
  - an operation's owner or workspace is passed by the TUI (decision 6).
- **No model mutation from `tea.Cmd` goroutines.** Add that a `core.Job` follows the same rule: it reads no model state. What it needs is captured on the caller's goroutine, as `saveSnapshot` and `Recover` do. Add the outbox's rule too: events apply at the end of `Update`, and a caller whose next steps depend on an event's effect drains right after the model call.
- **Pane updates are event-driven, not polled.** The health tick splits:
  - core's half (`core.Model.Tick`): liveness, parity, diff stats and the gated jobs;
  - the TUI's half: toast expiry, prune, the inline-attach backstop, client repair on `core.Alive`, the snapshot-path status scan, and the workbench scan.

  `HealthChecked` re-arms the tick. Pane events report to the model through `MarkOutput`, `PaneOutput` and `PaneQuiet`.
- **The pane-client gotcha.** Repair now happens in two places, both through `ensurePane`: on the `core.Alive` event (tick and Dead event) and on `core.Reactivated` (a reverted kill or pause). The rest stands.
- **The roster, Claude status and GitHub gotchas.**
  - `pollGate` lives in `core/gate.go` and dispatches a `core.Job` (`dispatchGated`, `gatedResult`). The rule against a batched Cmd is gone: a job returns one value.
  - `AdoptClaudeStatus` is the exported choke point. The TUI's ladder (`statusDetectedMsg`, the snapshot scan) calls it too.
  - `ExpediteGitHub` replaces `m.gate(gateGH).expedite()`, and `OpenTab` expedites itself.
- **The account gotchas.** The registry, auth, sync, usage and reload-then-diff now live in `core/accounts.go` and `core/usage.go`. The views stay in `app/accounts.go`, and `core.AccountsChanged` refreshes them.

- [ ] **Step 4: Testing Patterns and Debugging.**
- **Testing Patterns.**
  - `app` fixtures build workspaces with `testWS` and views with `slotOver`, and install the model with `wireCore`. A hand-built home with slots needs `wireCore` after its slots are assembled.
  - `app` tests deliver job results with `deliver(t, m, result)`.
  - `core` tests use `NewForTest`, `SetWorkspacesForTest` and `storedWorkspace`, and `core`'s `TestMain` isolates tmux and `~/.loom` like `app`'s.
- **Debugging.** The model logs under `subsystem=core`.

Run: `git grep -n -w -e workspaceSlot -e pollGate -- CLAUDE.md`, and read every remaining hit to check it is right.

### D2. The spec and this plan

**Files:** Modify: `docs/superpowers/specs/2026-10-03-loom-daemon-design.md`, this plan

- [ ] **Step 1: The spec's stage-1 list.** In Rollout stage 1, give **1B** a link to this plan and a one-line summary of what it delivered. Then adjust **1C**, since 1B introduced the events (synchronously, with `*session.Instance`): 1C now brings `InstanceView` and the `Core` interface over the existing events, draft rows, Lua lifecycle through `Core`, the issue-picker fetches as requests, and the pane ladder as a display-only overlay.

- [ ] **Step 2: Commit the docs.**

```bash
git add CLAUDE.md docs/
git commit -m "docs: CLAUDE.md and spec for the core model (daemon stage 1B)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Hc861zmBTH48vW7MY2DvEJ"
```

### D3. Full verification

- [ ] **Step 1: The suite.**

Run:
```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
go test -tags e2e ./e2e/...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
```
Expected: every command passes, and `gofmt -l` prints nothing.

- [ ] **Step 2: Sandbox smoke run.** Use the `loom-dev` skill (`go run ./tools/loomdev up`, then `start`, `keys`, `wait` and `shot`). Never run `./loom` directly.

The sandbox must not reach the real `~/.claude`. For the account check, use a throwaway `CLAUDE_CONFIG_DIR` main dir and a `claude` on `PATH` that resolves to `tools/fakeagent`, as the account-selection smoke did. Check each item and record pass or fail, with a screenshot (`shot`) where useful:
1. **Restore.** Two workspaces open as tabs, then restart. Both tabs come back, the last-used one is focused, and the tab-bar statuses show.
2. **New session with `N` and a prompt.** It starts, the prompt reaches the agent (the fake agent echoes it), and inline attach is focused. Then start a session and switch tabs while it starts: the "started in X" notice shows and the focused tab's selection doesn't move.
3. **Kill, pause, resume, and `R` with a changed option.** Each completes and the list follows. After a pause, the paused row's pane releases its client (the tick prunes it).
4. **A Recoverable orphan.** Make one: kill loom with a session running, then delete its record from `state.json`. On restart it shows inline. `r` recovers it, then for a second orphan `D` discards it.
5. **The picker.** Close a tab, reopen it, go to global mode with the Global row, and come back. No error, the panes follow, and nothing a tab showed is left on screen.
6. **Quit and restart.** Every session persists, including an idle Ready one (the 1A fix), and the open tabs restore.
7. **An agent exits.** Run `/exit` in the fake agent: the tick pauses it. Kill a workspace terminal's tmux session: it restarts, and its pane shows the new shell.
8. **Quick input and prompt overlay sends.** `a` to the agent, and the prompt overlay on a running session, both deliver while the UI stays responsive.
9. **The account strip.** With one extra account added from Settings (isolated as above), the strip appears, usage fills or shows its failure state, and removing the account hides the strip.
10. **The snapshot path.** Run once with `LOOM_PANE_RENDERER=snapshot`: statuses move (Running while the fake agent works, Ready after) and diffs refresh.

- [ ] **Step 3: Report.** Report to the user the test totals, the e2e result, each smoke check's outcome, and any deviation from this plan with its reason.

### D4. Outcome (coordinator, after the final review)

- [ ] Append an "Outcome and follow-ups" section to this plan, as 1A's closing section does. Cover:
  - the commits;
  - what the reviews changed beyond the plan;
  - the follow-ups. Include those found during execution, plus the open items this plan defers: the two workspace-terminal auto-create paths, the snapshot path's diff a tick late, and the prompt overlay closing before a failed send.

  Update the `loom-scrum-daemon-direction` memory: 1B is done and where it merged, and the next step is plan 1C.
