# Loom Daemon Stage 1C: The Instance Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan package by package: each package (A–E) is one task for the sub-skill. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The TUI and the Lua engine stop holding `*session.Instance`. They see instances as `core.InstanceView` values, named by a model-assigned `core.InstanceID`, and they change them only through requests to the model. Nothing changes for the user, except two Lua fixes that this stage makes possible (decision 9).

**Architecture:**
- **Views.** The model gives each instance an ID the first time it reports it. At every `core.Model.Sync` it diffs the views of each loaded workspace against the last ones it published and sends a `ViewsChanged` event for each workspace that changed, ahead of all other events. Each TUI slot keeps those views as its store, and `ui` renders only views.
- **Requests and replies.** Every lifecycle action is a request by ID (`Kill`, `Pause`, `Resume`, `ResumeWith`, `Recover`, `Merge`, `Push`, `SendPrompt`, `Create`, `FetchIssue`). A request can carry a client-chosen `ReqID`, and the model answers it with a `Reply` event when the action finishes.
- **TUI-owned state.** Creation flows edit a TUI-owned draft until `Create`. The pane-driven status ladder and bells become display overlays in the TUI.
- **Lua.** Lua lifecycle calls become intents that yield until their reply.
- **The interface.** At the end, the methods `app` calls are gathered into a `core.Core` interface, and `home.core` has that type.

**Tech Stack:** Go 1.25, Bubble Tea v2 (app only), gopher-lua (script), tmux ≥ 3.6, testify. Spec: [`docs/superpowers/specs/2026-10-03-loom-daemon-design.md`](../specs/2026-10-03-loom-daemon-design.md), stage 1. Previous plan: [1B, core model](2026-10-04-daemon-stage1b-core-model.md), whose closing section lists the follow-ups this plan picks up.

---

## Where this stage fits

| Plan | Scope | State |
|---|---|---|
| 1A | The TUI owns pane clients, attached by name. | Done |
| 1B | `core.Model` owns workspaces, instances, lifecycle, the tick and the gated jobs, still called on the Update goroutine. The TUI drains core's events after every Update. | Done: local `main` 9030942 |
| **1C (this)** | The instance half of the boundary: `InstanceView` + `InstanceID`, requests and replies by ID, the TUI's view store, draft rows, the display-only pane ladder, Lua lifecycle through the model, issue fetches as requests, the `core.Core` interface. | |
| 1D | The workspace half: workspace, config, state, registry, account and GitHub views and requests. Then the model on its own goroutine. | |

The user decided two things on 2026-10-07:
- 1C covers the instance boundary, and the workspace, registry, account and GitHub views move to 1D.
- Instances are identified by a model-assigned ID, not by title + workspace path. The spec is amended in package E.

## Decisions

| # | Decision |
|---|---|
| 1 | **Instances cross the boundary as values.** `core.InstanceView` is a plain struct copied out of the model. It holds every field the TUI renders or gates a key on, and nothing that acts. `app`, `ui` and `script` hold no `*session.Instance`, and `TestTUIHoldsNoInstance` enforces it (package C, extended in D). |
| 2 | **Model-assigned IDs.** `core.InstanceID` is a `uint64` the model assigns on first sight (`idOf`) and never reuses within the process. 0 is never assigned: the TUI uses it for a draft row. A request whose ID no loaded workspace holds is refused with `core.ErrNoSession`. An instance that leaves every loaded workspace loses its ID at the next publish. A closed-and-reopened workspace's records get new IDs, which is correct, since they are new objects. |
| 3 | **Views are published by diffing at `Sync`.** The model does not emit an event per mutation. `Sync` builds every loaded workspace's views, compares each workspace's list with the last one published (`reflect.DeepEqual`), and puts a `ViewsChanged{Workspace, Views}` for each changed workspace first in the drained events. Appliers of later events in the same drain therefore see the new views. This catches every mutation source, including the ones jobs make. It costs about N view builds per Update. A dirty flag can come later if profiling asks for it. `Drain` stays as it is for core's tests; the TUI calls `Sync`. |
| 4 | **Requests by ID, optional replies.** Each request takes the target ID and a `ReqID`, where 0 means no reply wanted. When the request finishes, the model emits `Reply{Req, ID, Err, Notice, Issue}`: at once for a refusal, at completion for a long operation. The notices the TUI shows today are unchanged. A Reply is extra, for the requester that waits on it (Lua, `Create`, `FetchIssue`). With `ReqID` 0 a refusal is only logged, as today's TUI paths log `resume.skipped`. |
| 5 | **Draft rows.** A creation flow no longer adds an unstarted instance to the workspace. The TUI holds a `draft`: title, prompt, program, selected branch, issue and target workspace. It is shown as a row with ID 0 at the end of its slot's list. Confirming sends `Create`, which builds, configures and starts the instance in one step. Cancelling discards the draft, with no kill job. A script's `ctx:new_instance` becomes `Create` with `Start: false` (D). |
| 6 | **The pane ladder becomes a display overlay** (spec decision 6). The TUI's content-scrape ladder no longer calls `TransitionTo`. It covers `statusDetectedMsg`, the snapshot scan, and the Ready→Running promotion on output. It writes `m.ladder[id]` instead: a status plus since-when. The TUI shows that status only for an active instance whose view has no reported status (`!v.StatusReported`). The model's status is then lifecycle plus Claude's hooks and roster. A non-Claude session stays Running in the model, and the TUI shows its ladder status. |
| 7 | **Bells are TUI state** (`m.bells[id]`): a pane event sets one and focus clears it. `InstanceView.Bell` is filled by the TUI and never by core. |
| 8 | **The TUI uses tmux by session name, never through an instance.** Full-screen attach uses `tmux.NewSession(name, program).FullScreenAttachCmd`. The liveness guards use `m.sessionAlive(name)`, a by-name `has-session` probe with a test seam. Lua's `send_keys`, `tap_enter` and `preview` go through a by-name `tmux.Session` on the script goroutine. |
| 9 | **Lua lifecycle goes through the model.** `inst:kill()`, `inst:pause()`, `inst:resume()` and `inst:send_prompt()` enqueue an `InstanceOpIntent` and yield. The TUI sends the request with a `ReqID` and resumes the coroutine with the Reply's outcome. A thin Lua shim raises when it is an error, so these stay "void, raise on error". `ctx:new_instance` yields until `Create` replies and returns the created instance. The Lua API keeps its names and return shapes. Two behaviours change, both bug fixes: `inst:kill()` now removes the session from the list and storage, and `inst:pause()`/`inst:resume()` now persist and show the spinner. Before, they bypassed the model. |
| 10 | **Kill and pause no longer take a TUI hook.** `Instance.Kill` and `Instance.Pause` already end the terminal pane's shell by name (`CloseRelatedSession`). The TUI drops its terminal-pane clients through `prunePanes`, which now also detaches terminal clients of titles with no active row (`DetachExcept`). `beforeKill`/`beforePause` and `closeTerminalFor` go away. |
| 11 | **Issue fetches are requests.** `core.Model.FetchIssue(repo, n, req)` runs `github.View` in a job and answers with `Reply.Issue`. The issue picker's pick and the `#n` expansion use it. |
| 12 | **Prompt sends are serialized against later input** (1B follow-up 3). While a `SendPrompt` to an instance is in flight (its Reply not yet in), the TUI refuses inline attach and another send to that instance with an info line. Keys typed right after a send can then no longer land between its paste and its Enter. |
| 13 | **`core.Core`.** At the end of package C, every model method `app` calls is listed in an interface `core.Core`, and `home.core` has that type. The workspace and account methods keep their 1B signatures (pointers to `*core.Workspace`, the account registry and so on) until 1D converts them to values; the interface's doc says so. App tests reach `*core.Model` for its test seams through `testModel(m)`. |
| 14 | **Tests follow the code.** App and ui tests build views as literals, or build instances in core (`ws.AddForTest`) and read them through the TUI after `syncViews(m)`. No assertion is dropped. Each package's coordinator check compares assertion counts. |

Out of scope:
- Workspace, config, state, registry, account and GitHub views and requests: 1D.
- The model's own goroutine: 1D. The socket codec: stage 2.
- A dirty-flag optimization for publishing (decision 3).
- 1B follow-up 7: a failed workspace-terminal `Restart` leaves it unstarted.
- `Instance.SetBellPending`/`BellPending` and the instance setters only the old TUI paths used. They become unused by `app`; deleting them is stage-4 cleanup.

## What moves where

| Today | 1C | Package |
|---|---|---|
| Instances shown by pointer (`ui.List`, cards, overview, menu, split pane, panes, workbench, help) | `core.InstanceView` values, from the slot's view store | B |
| `m.core.InstanceForSession`, `ActiveInstances`, `Holding`, `StatusEligible`, `ActiveInstance` read by app | lookups in the TUI's view store (`viewBySession`, `activeViews`, view methods) | B |
| Pointer-taking model operations (`Start`, `Kill`, …, `Tick`, `PaneOutput`, …) | ID-taking requests; the pointer versions are renamed `…Inst` in A and deleted in C | A, C |
| Creation flows editing an unstarted instance (`pendingNew`, `SetTitle`, `SetPrompt`, …) | a TUI `draft` and `core.Create` | C |
| `applyChosenLaunch` (app) | inside `Create` and `ResumeWith` (core) | C |
| The ladder's `TransitionTo` (app) | `m.ladder` overlay | C |
| `SetBellPending` (app) | `m.bells` overlay | B |
| `closeTerminalFor` / `beforeKill` / `beforePause` | terminal-client release in `prunePanes` | C |
| `TmuxSession().FullScreenAttachCmd`, `Pane().TmuxAlive()` | by-name `tmux.Session`, `m.sessionAlive` | C |
| Lua userdata over `*session.Instance`; Lua lifecycle calling `session` directly | userdata over `InstanceView`; lifecycle intents; Replies | D |
| `issuePickedMsg`/`issueExpandCmd` running `github.View` in app | `core.FetchIssue` | D |

## Packages

| Package | Delivers | Commit |
|---|---|---|
| **A** | Core side, additive: `InstanceID`, `InstanceView`, `Sync`/`ViewsChanged`, `Views`/`View`, the ID requests + `Reply` + `ReqID`, `Create`, `ResumeWith`, `FetchIssue`, ID fields on events. The pointer operations are renamed `…Inst`. | `feat(core): instance views, IDs, and requests by ID` |
| **B** | The TUI reads views: each slot's view store, `ui` over `InstanceView`, app read sites over views, bells as an overlay. Writes still reach instances through a temporary bridge (`InstanceOf`). | `refactor(ui,app): the TUI renders instance views` |
| **C** | The TUI writes by request: operations by ID, events by ID, drafts, `ResumeWith`, the display-only ladder, terminal-client release, tmux by name, send serialization, the `core.Core` interface, deletion of the pointer API, and the enforcement test (app, ui) | `refactor(app,core): the TUI acts on instances only by request` |
| **D** | Lua through the model (views in userdata, lifecycle intents with replies, `ctx:new_instance` via `Create`, by-name pane ops) and issue fetches as requests. Removes the bridge; the enforcement test covers `script`. | `refactor(script,app): Lua and issue fetches go through the model` |
| **E** | CLAUDE.md, `docs/specs/scripting.md`, the spec (identity by ID, the 1C/1D entries), then full verification: the suite, race, e2e, and a sandbox smoke run that includes Lua. | `docs: CLAUDE.md, scripting and spec for the instance boundary (daemon stage 1C)` |

A package is one unit of work: one implementer, one review, one commit, plus any fixups the review asks for. Its numbered checkpoints are steps, not commits. Every checkpoint ends with the build and the touched packages' tests green, except where it says otherwise.

## Conventions for every package

- Run Go commands from the worktree root, with `CGO_ENABLED=0` (no gcc here). The race run is `CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./script/... ./session/...`, and e2e is `CGO_ENABLED=0 go test -tags e2e ./e2e/...`.
- Format only tracked non-vendor files plus new ones: `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`. Never `gofmt -w .`.
- Use `go vet ./...` (the local golangci-lint is v2 while the repo config is v1).
- Commit messages end with exactly these two lines:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7
  ```
  Commit with explicit paths.
- Never use `git stash` in any form. Stay on this worktree's branch: no checkout, switch, rebase, new branches or worktrees.
- Don't read or write anything under `~/.claude`.
- A security hook rejects any file write containing the word `exec` immediately followed by `(`. Name helpers `executor()`, `runner()`, `cmdExec`.
- Never run `./loom` directly. Use the `loom-dev` skill.
- No test may reach the developer's tmux server or `~/.loom`.
- **Moving code** keeps its body and comments verbatim except the edits a step lists. The coordinator diffs moved bodies mechanically, so list any other change with its reason.
- **Tests.** Never weaken or delete an assertion. If one can't survive a change, stop and report it.
- **The view field map** (B, C, D). Replace an instance read with its view field as follows:

  | Instance | View |
  |---|---|
  | `inst.Title` | `v.Title` |
  | `inst.Path` | `v.RepoPath` |
  | `inst.IsWorkspaceTerminal` | `v.IsWorkspaceTerminal` |
  | `inst.GetStatus()` | `v.Status` |
  | `inst.Started()` | `v.Started` |
  | `inst.Paused()` | `v.Paused()` |
  | `inst.StatusAge()` | `v.StatusAge()` |
  | `inst.GetBranch()` | `v.Branch` |
  | `inst.GetWorktreePath()` | `v.WorktreePath` |
  | `inst.Program()` | `v.Program` |
  | `inst.Account()` | `v.Account` |
  | `inst.HeadroomProxy()` | `v.HeadroomProxy` |
  | `inst.CacheTTL1h()` | `v.CacheTTL1h` |
  | `inst.IssueNumber()` | `v.Issue` |
  | `inst.WaitReason()` | `v.WaitReason` |
  | `inst.Subagents()` | `v.Subagents` |
  | `inst.GitHubState()` | `v.GitHub` |
  | `inst.Parity()` | `v.Ahead, v.Behind, v.ParityKnown` |
  | `inst.LastMessage()` | `v.LastMessage, v.HasLastMessage` |
  | `inst.GetDiffStats()` | `&v.Diff` when `v.HasDiff`, else `nil` |
  | `inst.BellPending()` | `v.Bell` |
  | `inst.Pane().TmuxSessionName()` | `v.TmuxSession` |
  | `inst.Pane().SessionProgram()` | `v.SessionProgram` |
  | `core.ActiveInstance(inst)` / `core.StatusEligible(inst)` | `v.Active()` |
  | `_, _, ok := inst.ClaudeStatus()` | `v.StatusReported` |

## File structure

**New**
| File | Responsibility |
|---|---|
| `core/view.go` | `InstanceID`, `InstanceView` and its methods |
| `core/views.go` | `idOf`, `lookup`, `viewOf`, `Views`, `View`, `publishViews`, `Sync` |
| `core/requests.go` | `ReqID`, `Reply`, `ErrNoSession`, `tracked`, `outcome`; the ID requests (`Kill`, `Pause`, `Resume`, `ResumeWith`, `Recover`, `Merge`, `Push`, `SendPrompt`, `Create`, `FetchIssue`, and the ID versions of `Tick`, `PaneOutput`, `PaneQuiet` and `VerifyDead`) |
| `core/iface.go` | (C) `Core` |
| `core/view_test.go`, `core/requests_test.go` | Tests |
| `app/views.go` | The slot's view store, `slotRows` (the list source with overlays and the draft), `syncViews`, `viewBySession`, `activeViews`, `sessionAlive` |
| `app/drafts.go` | (C) `draft`, `newDraft`, `discardDraft`, `confirmDraft` |
| `app/requests.go` | (C) `nextReq`, `pending`, `handleReply` |
| `internal/testenv/instance_enforce_test.go` | (C) `TestTUIHoldsNoInstance` |

**Modified**
| File | Change |
|---|---|
| `session/instance.go` | `StatusSince()` accessor (A) |
| `core/events.go` | `ViewsChanged`, `Reply`, plus ID/Title fields on `Started`, `Recovered`, `SessionLaunched`, `Reactivated` and `Alive` (A); the pointer fields are removed in C |
| `core/ops.go`, `core/tick.go`, `core/claude_status.go`, `core/workspace.go`, `core/workspaces.go` | Pointer operations renamed `…Inst` (A) then deleted or unexported (C); `Workspace` instance edits unexported with `AddForTest`/`InstancesForTest` (C) |
| `ui/list.go`, `ui/card.go`, `ui/overview.go`, `ui/menu.go`, `ui/diff.go`, `ui/terminal.go`, `ui/preview.go`, `ui/split_pane.go`, `ui/cursor.go`, `ui/panes.go` + tests | Views instead of instances (B) |
| `app/*.go` | Reads over views (B); writes by request, drafts, overlays (C); script host over views (D) |
| `script/host.go`, `script/userdata_instance.go`, `script/userdata_ctx.go`, `script/userdata_worktree.go`, `script/engine.go`, `script/intent.go`, `script/api_actions.go` + tests | Views, lifecycle intents, resume values (D) |
| `CLAUDE.md`, `docs/specs/scripting.md`, the daemon spec, this plan | E |

---

## Package A: instance views, IDs and requests by ID (core, additive)

Everything here is new core code, plus one mechanical rename. The TUI keeps working exactly as before through the renamed pointer operations. B and C switch it over. One commit at the end.

### A1. Rename the pointer operations

- [ ] **Step 1: Rename these exported `*core.Model` methods**, which take a `*session.Instance`, by adding the suffix `Inst`:

| Old | New |
|---|---|
| `Start` | `StartInst` |
| `Kill` | `KillInst` |
| `Pause` | `PauseInst` |
| `Resume` | `ResumeInst` |
| `ResumeIfLoading` | `ResumeIfLoadingInst` |
| `Recover` | `RecoverInst` |
| `Merge` | `MergeInst` |
| `SendPrompt` | `SendPromptInst` |
| `Push` | `PushInst` |
| `Tick` | `TickInst` |
| `PaneOutput` | `PaneOutputInst` |
| `PaneQuiet` | `PaneQuietInst` |
| `VerifyDead` | `VerifyDeadInst` |

Rename each declaration (in `core/ops.go`, `core/github.go`, `core/tick.go` and `core/claude_status.go`) and every caller in `core/`, `app/` and their tests. The compiler lists the callers: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./...`. Update the doc comments' first word to the new name. Nothing else changes.

Run: `CGO_ENABLED=0 go test ./core/ ./app/`
Expected: PASS.

### A2. `InstanceID` and `InstanceView`

**Files:** Create `core/view.go`, `core/view_test.go`. Modify `session/instance.go`.

- [ ] **Step 1: Add `StatusSince` to `session/instance.go`**, next to `StatusAge`:
```go
// StatusSince returns when the instance entered its current status, or the
// zero time when no transition has been observed this process. StatusAge is
// the time since then.
func (i *Instance) StatusSince() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.statusChangedAt
}
```

- [ ] **Step 2: Write `core/view.go`.**
```go
package core

import (
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/subagent"
)

// InstanceID names one instance for the life of the process. The model
// assigns it the first time it reports the instance (idOf) and never reuses
// it, so a request naming an ID can't reach another instance: a same-titled
// one in another workspace, or a reopened workspace's fresh copy of the same
// record. 0 is never assigned; the TUI uses it for a draft row.
type InstanceID uint64

// InstanceView is an instance as a client sees it: a value copied out of the
// model (viewOf) carrying everything the TUI renders or gates a key on, and
// nothing that acts. Clients change an instance only through requests that
// name its ID. The model publishes each workspace's views when they change
// (Sync, ViewsChanged).
type InstanceView struct {
	ID    InstanceID
	Title string
	// RepoPath is the repository the session works in (Instance.Path).
	RepoPath     string
	WorktreePath string
	Branch       string
	// TmuxSession is the agent's tmux session name ("" before it has one);
	// pane clients attach by it. SessionProgram is the program that session
	// was launched with, which picks the client's adapter (trust-prompt and
	// pending-prompt patterns); Program can already name a new one after R.
	TmuxSession    string
	SessionProgram string
	Program        string
	Status         session.Status
	// StatusSince is when Status was entered (zero: not this process).
	StatusSince time.Time
	// StatusReported is set when Claude's hooks or roster have an opinion
	// on the status (Instance.ClaudeStatus). Without one the TUI may show its
	// own pane-scraped status instead (its ladder overlay).
	StatusReported bool
	WaitReason     string
	LastMessage    string
	HasLastMessage bool
	Subagents      []subagent.View
	// Diff is the latest diff stats, valid when HasDiff.
	Diff                git.DiffStats
	HasDiff             bool
	GitHub              github.State
	Ahead, Behind       int
	ParityKnown         bool
	Issue               int
	Account             string
	HeadroomProxy       bool
	CacheTTL1h          bool
	IsWorkspaceTerminal bool
	Started             bool
	// Bell is the TUI's own overlay (a bell rang in the pane since it was
	// last focused). The model never sets it.
	Bell bool
}

// Paused reports whether the view's status is Paused.
func (v InstanceView) Paused() bool { return v.Status == session.Paused }

// StatusAge is how long the instance has been in its status, 0 when no
// transition was observed this process (as Instance.StatusAge).
func (v InstanceView) StatusAge() time.Duration {
	if v.StatusSince.IsZero() {
		return 0
	}
	return time.Since(v.StatusSince)
}

// Active reports whether the instance is running for the purposes of the
// background jobs and the TUI's pane clients: started, and not Paused,
// Deleting, Recoverable or Loading. It is ActiveInstance's and
// StatusEligible's rule, for a view.
func (v InstanceView) Active() bool {
	switch v.Status {
	case session.Paused, session.Deleting, session.Recoverable, session.Loading:
		return false
	}
	return v.Started
}
```
Check `ActiveInstance` and `StatusEligible` in `core/`. If either rule differs from `Active`, make `Active` match `ActiveInstance` exactly and report the difference.

### A3. Assign IDs, build views, and publish them at `Sync`

**Files:** Create `core/views.go`. Modify `core/model.go` (fields, `newModel`), `core/events.go`.

- [ ] **Step 1: Add the fields to `Model`, and initialise them in `newModel`:**
```go
	// ids is each reported instance's ID (idOf). An instance no loaded
	// workspace holds is forgotten at the next publish; IDs are never reused
	// (nextID only grows).
	ids    map[*session.Instance]InstanceID
	nextID InstanceID
	// published is each loaded workspace's views as last published (Sync).
	published map[*Workspace][]InstanceView
```

- [ ] **Step 2: Add the event, in `core/events.go`:**
```go
// ViewsChanged carries a loaded workspace's instance views, in display
// order, whenever any of them changed since the last Sync. The TUI replaces
// its store for that workspace with Views. Sync puts these first in the
// events it returns, so the appliers of the events that follow see the new
// views.
type ViewsChanged struct {
	Workspace *Workspace
	Views     []InstanceView
}
```
plus its `coreEvent()` method.

- [ ] **Step 3: Write `core/views.go`.**
```go
package core

import (
	"reflect"
	"slices"

	"github.com/aidan-bailey/loom/session"
)

// idOf returns inst's ID, assigning the next one the first time.
func (m *Model) idOf(inst *session.Instance) InstanceID {
	if id, ok := m.ids[inst]; ok {
		return id
	}
	m.nextID++
	m.ids[inst] = m.nextID
	return m.nextID
}

// lookup resolves id to its instance and the loaded workspace holding it,
// or nil, nil when no loaded workspace holds it.
func (m *Model) lookup(id InstanceID) (*session.Instance, *Workspace) {
	if id == 0 {
		return nil, nil
	}
	for _, ws := range m.Loaded() {
		for _, inst := range ws.insts {
			if m.ids[inst] == id {
				return inst, ws
			}
		}
	}
	return nil, nil
}

// viewOf copies inst's state into a view, under the instance's own locks.
func (m *Model) viewOf(inst *session.Instance) InstanceView {
	pane := inst.Pane()
	v := InstanceView{
		ID:                  m.idOf(inst),
		Title:               inst.Title,
		RepoPath:            inst.Path,
		WorktreePath:        inst.GetWorktreePath(),
		Branch:              inst.GetBranch(),
		TmuxSession:         pane.TmuxSessionName(),
		SessionProgram:      pane.SessionProgram(),
		Program:             inst.Program(),
		Status:              inst.GetStatus(),
		StatusSince:         inst.StatusSince(),
		WaitReason:          inst.WaitReason(),
		Subagents:           slices.Clone(inst.Subagents()),
		GitHub:              inst.GitHubState(),
		Issue:               inst.IssueNumber(),
		Account:             inst.Account(),
		HeadroomProxy:       inst.HeadroomProxy(),
		CacheTTL1h:          inst.CacheTTL1h(),
		IsWorkspaceTerminal: inst.IsWorkspaceTerminal,
		Started:             inst.Started(),
	}
	_, _, v.StatusReported = inst.ClaudeStatus()
	v.LastMessage, v.HasLastMessage = inst.LastMessage()
	if d := inst.GetDiffStats(); d != nil {
		v.Diff, v.HasDiff = *d, true
	}
	v.Ahead, v.Behind, v.ParityKnown = inst.Parity()
	return v
}

// Views returns ws's instances as views, in display order (a fresh slice).
// The TUI seeds a new slot's store with it; afterwards ViewsChanged keeps
// the store current.
func (m *Model) Views(ws *Workspace) []InstanceView {
	if ws == nil {
		return nil
	}
	out := make([]InstanceView, len(ws.insts))
	for i, inst := range ws.insts {
		out[i] = m.viewOf(inst)
	}
	return out
}

// View returns the view of the loaded instance id.
func (m *Model) View(id InstanceID) (InstanceView, bool) {
	inst, _ := m.lookup(id)
	if inst == nil {
		return InstanceView{}, false
	}
	return m.viewOf(inst), true
}

// publishViews builds every loaded workspace's views and returns a
// ViewsChanged for each workspace whose views differ from the last
// published ones. It then forgets the published views of workspaces no
// longer loaded and the IDs of instances no loaded workspace holds.
func (m *Model) publishViews() []Event {
	var events []Event
	next := make(map[*Workspace][]InstanceView, len(m.published))
	live := make(map[*session.Instance]bool, len(m.ids))
	for _, ws := range m.Loaded() {
		views := m.Views(ws)
		for _, inst := range ws.insts {
			live[inst] = true
		}
		next[ws] = views
		if prev, ok := m.published[ws]; !ok || !reflect.DeepEqual(prev, views) {
			events = append(events, ViewsChanged{Workspace: ws, Views: views})
		}
	}
	m.published = next
	for inst := range m.ids {
		if !live[inst] {
			delete(m.ids, inst)
		}
	}
	return events
}

// Sync publishes the views that changed (ViewsChanged, first) and then
// returns everything else produced since the last call (Drain). The TUI
// calls it where it drained; core's own tests may still call Drain.
func (m *Model) Sync() Out {
	views := m.publishViews()
	out := m.Drain()
	out.Events = append(views, out.Events...)
	return out
}
```
`publishViews` builds the same views twice: once inside `m.Views` and once for the IDs pass. That's fine, but a single loop that fills `views` and `live` together is clearer, so write it that way if you prefer. Keep the behaviour: published views are compared per workspace, and a workspace seen for the first time always publishes.

- [ ] **Step 4: Tests, in `core/view_test.go`:**
```go
package core

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestView_CopiesTheInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	views := m.Views(ws)
	require.Len(t, views, 1)
	v := views[0]
	assert.NotZero(t, v.ID)
	assert.Equal(t, "x", v.Title)
	assert.Equal(t, session.Paused, v.Status)
	assert.True(t, v.Paused())
	assert.True(t, v.Started)
	assert.False(t, v.Active())
	assert.Equal(t, inst.Program(), v.Program)

	got, ok := m.View(v.ID)
	require.True(t, ok)
	assert.Equal(t, v, got)
}

func TestIDs_StableNeverReusedAndForgotten(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	a, b := pausedInst(t, "a"), pausedInst(t, "b")
	ws.Add(a)
	ws.Add(b)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	idA, idB := m.idOf(a), m.idOf(b)
	assert.NotEqual(t, idA, idB)
	assert.Equal(t, idA, m.idOf(a), "stable")

	ws.Remove(a)
	m.Sync() // a is no longer loaded: forgotten
	_, ok := m.View(idA)
	assert.False(t, ok)
	ws.Add(a)
	assert.NotEqual(t, idA, m.idOf(a), "a forgotten instance gets a new ID, never an old one")
	assert.Greater(t, m.idOf(a), idB)
}

func TestSync_PublishesChangedWorkspacesFirst(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.notifyInfo("hello")
	out := m.Sync()
	require.Len(t, out.Events, 2)
	vc, ok := out.Events[0].(ViewsChanged)
	require.True(t, ok, "views first")
	assert.Same(t, ws, vc.Workspace)
	assert.Equal(t, Notice{Info: "hello"}, out.Events[1])

	assert.Empty(t, m.Sync().Events, "nothing changed")

	require.NoError(t, inst.TransitionTo(session.Loading))
	out = m.Sync()
	require.Len(t, out.Events, 1)
	assert.Equal(t, session.Loading, out.Events[0].(ViewsChanged).Views[0].Status)
}
```
`ws.Add` is still exported here; package C unexports it. Use the test helper names in `core/testhelpers_test.go` as they are.

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestView|TestIDs|TestSync'`
Expected: PASS.

### A4. Requests by ID and replies

**Files:** Create `core/requests.go`, `core/requests_test.go`. Modify `core/model.go` (`Deliver`), `core/events.go`, `core/completions.go`, `core/tick.go`, `core/claude_status.go`.

- [ ] **Step 1: Add the reply types to `core/events.go`:**
```go
// Reply answers a request made with a ReqID: at once when the request is
// refused, when it finishes for a long operation. ID names the instance it
// concerns (for Create the new one, for Recover the adopted one). Err is
// the failure. Notice is what a success must still show (a stash it could
// not drop). Issue is FetchIssue's result. The notices the TUI shows for an
// operation are emitted as usual; the Reply is for the requester that waits
// on it.
type Reply struct {
	Req    ReqID
	ID     InstanceID
	Err    error
	Notice error
	Issue  github.Issue
}
```
plus `coreEvent()`. Then add the ID fields to the existing events, keeping their pointer fields until C:
- `SessionLaunched` and `Reactivated` gain `ID InstanceID`.
- `Started` and `Recovered` gain `ID InstanceID` and `Title string`. The title is for the notice, since a Started for an owner that was closed has no view left in the TUI.
- `Alive` gains `IDs []InstanceID`.

Every place that emits one of these sets the new fields too: `ID: m.idOf(inst)`, `Title: inst.Title`, and `IDs` built in the same loop as `Instances`.

- [ ] **Step 2: Write `core/requests.go`.**
```go
package core

import (
	"context"
	"errors"
	"fmt"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
)

// ReqID names a request so its Reply can find the requester. The client
// chooses it; 0 asks for no Reply.
type ReqID uint64

// ErrNoSession refuses a request naming an ID no loaded workspace holds:
// the session was removed, or its workspace closed, after the client last
// saw it.
var ErrNoSession = errors.New("no such session")

// tracked carries a request's job result back to Deliver together with the
// request it answers.
type tracked struct {
	req    ReqID
	id     InstanceID
	result any
}

// issueResult is FetchIssue's job result.
type issueResult struct {
	issue github.Issue
	err   error
}

// track wraps job so its result reaches Deliver as a tracked result; nil
// for a nil job.
func (m *Model) track(req ReqID, id InstanceID, job Job) Job {
	if job == nil {
		return nil
	}
	return func() any { return tracked{req: req, id: id, result: job()} }
}

// deliverTracked handles a request's result as if it had arrived on its
// own, then answers the request (when it asked for a Reply).
func (m *Model) deliverTracked(t tracked) {
	m.Deliver(t.result)
	if t.req == 0 {
		return
	}
	r := Reply{Req: t.req, ID: t.id}
	r.Err, r.Notice = outcome(t.result)
	switch res := t.result.(type) {
	case RecoverResult:
		if res.Err == nil && res.Recovered != nil {
			r.ID = m.idOf(res.Recovered)
		}
	case issueResult:
		r.Issue = res.issue
	}
	m.emit(r)
}

// outcome reads an operation's failure and notice out of its result.
func outcome(result any) (err, notice error) {
	switch r := result.(type) {
	case OpFailed:
		return r.Err, nil
	case KillResult:
		return nil, r.Notice
	case ResumeResult:
		return nil, r.Notice
	case RecoverResult:
		return r.Err, nil
	case MergeResult:
		return r.Err, nil
	case pushResult:
		return r.err, nil
	case promptFailed:
		return r.err, nil
	case issueResult:
		return r.err, nil
	}
	return nil, nil
}

// refuse answers a request the model won't start: with a Reply when one was
// asked for, else only in the log (as the TUI's own paths have always
// logged a refused transition).
func (m *Model) refuse(req ReqID, id InstanceID, err error) {
	if req == 0 {
		log.For("core").Debug("request.refused", "id", uint64(id), "err", err)
		return
	}
	m.emit(Reply{Req: req, ID: id, Err: err})
}

// reply answers a request that finished at once.
func (m *Model) reply(req ReqID, r Reply) {
	if req == 0 {
		return
	}
	r.Req = req
	m.emit(r)
}
```
Route the new results in `Deliver`:
```go
	case tracked:
		m.deliverTracked(msg)
	case issueResult:
		// Only its Reply carries it (deliverTracked).
```

- [ ] **Step 3: The requests**, in `core/requests.go`:
```go
// Kill kills the session id: its pre-step moves it to Deleting at once (the
// spinner shows), and its job checks, kills and deletes the record
// (KillInst), answering with a KillResult or OpFailed.
func (m *Model) Kill(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	pre, job := m.KillInst(ws, inst, nil)
	pre()
	m.spawn(m.track(req, id, job))
}

// Pause pauses the session id: it moves to Loading at once (a refused
// transition is logged and the pause runs anyway, as the TUI's path always
// did), then the job stashes, kills the session and removes the worktree
// (PauseInst).
func (m *Model) Pause(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("pause.preaction_transition_failed", "err", err)
	}
	m.spawn(m.track(req, id, m.PauseInst(ws, inst, nil)))
}

// Resume resumes the Paused session id (see ResumeInst). A refused
// transition refuses the request.
func (m *Model) Resume(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		m.refuse(req, id, fmt.Errorf("resume %s: %w", inst.Title, err))
		return
	}
	m.spawn(m.track(req, id, m.ResumeIfLoadingInst(ws, inst)))
}

// ResumeWith resumes the session id with new launch options (the R flow):
// the program recomposed from base (applyLaunch), then as Resume.
func (m *Model) ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	m.applyLaunch(inst, opts, base)
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		m.refuse(req, id, fmt.Errorf("resume %s: %w", inst.Title, err))
		return
	}
	m.spawn(m.track(req, id, m.ResumeIfLoadingInst(ws, inst)))
}

// Recover adopts the Recoverable orphan id (RecoverInst). Its Reply names
// the adopted instance.
func (m *Model) Recover(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	job := m.RecoverInst(ws, inst)
	if job == nil {
		m.refuse(req, id, fmt.Errorf("recover %s: it is not recoverable", inst.Title))
		return
	}
	m.spawn(m.track(req, id, job))
}

// Merge merges source's branch into target's worktree (MergeInst).
func (m *Model) Merge(target, source InstanceID, req ReqID) {
	t, _ := m.lookup(target)
	s, _ := m.lookup(source)
	if t == nil || s == nil {
		m.refuse(req, target, ErrNoSession)
		return
	}
	m.spawn(m.track(req, target, m.MergeInst(t, s)))
}

// Push commits and pushes the session id's worktree (PushInst).
func (m *Model) Push(id InstanceID, req ReqID) {
	inst, _ := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	m.spawn(m.track(req, id, m.PushInst(inst)))
}

// SendPrompt types text into the session id's pane and presses Enter
// (SendPromptInst). A failure is a notice, and the Reply's Err.
func (m *Model) SendPrompt(id InstanceID, text string, req ReqID) {
	inst, _ := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	m.spawn(m.track(req, id, m.SendPromptInst(inst, text)))
}

// NewInstance describes an instance to create (Create).
type NewInstance struct {
	Title   string
	Path    string // the repository it works in
	Program string // the base program; Launch is composed onto it
	Prompt  string // sent once it has started
	Branch  string // the branch picker's choice; "" for a new branch
	Issue   int    // the GitHub issue it was made from, 0 for none
	// Launch is the Session Launch Options choice. Its account and branch
	// prefix are recorded on the instance; the rest is composed onto
	// Program with the account's remote-control auth (applyLaunch). Only
	// with Start.
	Launch launch.Options
	// Start starts it at once (the creation flows). Without it the
	// instance is added unstarted (a script's ctx:new_instance).
	Start bool
}

// Create builds an instance in ws from spec, adds it, and (with Start)
// configures and starts it: the owner is stamped now, and the start's
// completion is a StartResult as before. The Reply comes at once and names
// the new instance.
func (m *Model) Create(ws *Workspace, spec NewInstance, req ReqID) {
	cfgDir := ""
	if ws.ctx != nil {
		cfgDir = ws.ctx.ConfigDir
	}
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:     spec.Title,
		Path:      spec.Path,
		Program:   spec.Program,
		Prompt:    spec.Prompt,
		Branch:    spec.Branch,
		ConfigDir: cfgDir,
	})
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	if spec.Issue != 0 {
		inst.SetIssue(spec.Issue)
	}
	if spec.Start {
		m.applyLaunch(inst, spec.Launch, spec.Program)
		// Always recorded, edited or not, so branch composition has a single
		// source of truth instead of a re-read of config.json in git.
		inst.SetBranchPrefix(spec.Launch.BranchPrefix)
		if err := inst.TransitionTo(session.Loading); err != nil {
			log.For("core").Warn("create.transition_failed", "title", spec.Title, "err", err)
		}
	}
	ws.Add(inst)
	if spec.Issue != 0 {
		m.ApplyGitHubState()
	}
	id := m.idOf(inst)
	m.reply(req, Reply{ID: id})
	if spec.Start {
		m.spawn(m.StartInst(inst, ws))
	}
}

// applyLaunch records chosen launch options on inst: the program composed
// from base with the chosen account's remote-control auth, the env toggles,
// and the account. Formerly app.applyChosenLaunch.
func (m *Model) applyLaunch(inst *session.Instance, opts launch.Options, base string) {
	inst.SetLaunchOptions(launch.Compose(opts, m.RCAuthFor(opts.Account), base, inst.Title), opts.HeadroomProxy, opts.CacheTTL1h)
	inst.SetAccount(opts.Account)
}

// FetchIssue reads issue n of repo through gh (github.View) in a job and
// answers with the Reply's Issue (or Err).
func (m *Model) FetchIssue(repo string, n int, req ReqID) {
	m.spawn(m.track(req, 0, fetchIssueJob(repo, n, internalexec.Default{})))
}

// fetchIssueJob is FetchIssue's job, with its executor injectable for tests.
func fetchIssueJob(repo string, n int, r internalexec.Executor) Job {
	return func() any {
		is, err := github.View(context.Background(), repo, n, r)
		return issueResult{issue: is, err: err}
	}
}
```
Check that `SetIssue` and `ApplyGitHubState` are the names in the tree; adjust to the real ones and list any change. The prompt flow used to call `SetSelectedBranch`; `InstanceOptions.Branch` sets the same field (`selectedBranch: opts.Branch`), so `Create` passes it there. A `ws` with no context (bare tests) creates with an empty config dir, as `session.NewInstance` allows.

- [ ] **Step 4: The ID versions of the triggers.** In `core/tick.go` and `core/claude_status.go`, add these. Each resolves the ID and calls the `…Inst` version, ignoring an ID no loaded workspace holds:
```go
// Tick runs the health tick's model half (TickInst); selected is the
// TUI's selected instance, 0 for none.
func (m *Model) Tick(selected InstanceID) {
	inst, _ := m.lookup(selected)
	m.TickInst(inst)
}

// PaneOutput reports that id's pane produced output (PaneOutputInst).
func (m *Model) PaneOutput(id InstanceID) {
	if inst, _ := m.lookup(id); inst != nil {
		m.PaneOutputInst(inst)
	}
}

// PaneQuiet reports that id's pane went quiet (PaneQuietInst).
func (m *Model) PaneQuiet(id InstanceID) {
	if inst, _ := m.lookup(id); inst != nil {
		m.PaneQuietInst(inst)
	}
}

// VerifyDead queues the probe a pane's Dead event asks for on id's session
// (VerifyDeadInst); its result is a DeadVerified as before.
func (m *Model) VerifyDead(id InstanceID) {
	if inst, _ := m.lookup(id); inst != nil {
		m.spawn(m.VerifyDeadInst(inst))
	}
}
```
`TickInst(nil)` must accept a nil selection: it does today, since app passes a nil selection. Check this.

- [ ] **Step 5: Tests, in `core/requests_test.go`:**
```go
package core

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// run executes the jobs of out and delivers their results, returning what
// the model produced after them.
func run(m *Model, out Out) Out {
	for _, j := range out.Jobs {
		m.Deliver(j())
	}
	return m.Drain()
}

func replies(out Out) []Reply {
	var rs []Reply
	for _, ev := range out.Events {
		if r, ok := ev.(Reply); ok {
			rs = append(rs, r)
		}
	}
	return rs
}

func TestRequests_AnUnknownIDIsRefused(t *testing.T) {
	m := NewForTest(Options{})
	m.Kill(99, 5)
	assert.Equal(t, []Reply{{Req: 5, ID: 99, Err: ErrNoSession}}, replies(m.Drain()))
	m.Kill(99, 0) // no reply wanted
	assert.Empty(t, replies(m.Drain()))
}

// TestKill_RepliesWhenItFinishes: an unstarted instance has no worktree, so
// the kill job refuses (OpFailed); the Reply comes when its result lands,
// not when the request is made.
func TestKill_RepliesWhenItFinishes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Kill(id, 7)
	out := m.Drain()
	assert.Empty(t, replies(out), "no reply before the job finishes")
	assert.Equal(t, session.Deleting, inst.GetStatus(), "the pre-step ran")
	require.Len(t, out.Jobs, 1)

	rs := replies(run(m, out))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(7), rs[0].Req)
	assert.Equal(t, id, rs[0].ID)
	assert.Error(t, rs[0].Err)
}

func TestCreate_RepliesWithTheNewIDAndStarts(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Create(ws, NewInstance{Title: "new", Path: t.TempDir(), Program: "claude", Prompt: "hi", Start: true,
		Launch: launch.Options{BranchPrefix: "me/"}}, 3)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	require.NotZero(t, rs[0].ID)
	v, ok := m.View(rs[0].ID)
	require.True(t, ok)
	assert.Equal(t, "new", v.Title)
	assert.Equal(t, session.Loading, v.Status)
	assert.Len(t, out.Jobs, 1, "the start job")
}

func TestCreate_WithoutStartLeavesItUnstarted(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Create(ws, NewInstance{Title: "s", Path: t.TempDir(), Program: "claude"}, 4)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	v, _ := m.View(rs[0].ID)
	assert.False(t, v.Started)
	assert.Equal(t, session.Ready, v.Status)
	assert.Empty(t, out.Jobs)
}

func TestResumeWith_AppliesTheLaunchOptions(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.ResumeWith(m.idOf(inst), launch.Options{Model: "sonnet", Account: "default"}, "claude", 0)
	assert.Contains(t, inst.Program(), "sonnet")
	assert.Equal(t, session.Loading, inst.GetStatus())
	assert.Len(t, m.Drain().Jobs, 1)
}

func TestFetchIssue_RepliesWithTheIssue(t *testing.T) {
	r := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "issue view") {
				return []byte(`{"number":12,"title":"Fix it","body":"do","url":"u","state":"OPEN","labels":[]}`), nil
			}
			return nil, errors.New("unexpected " + c.String())
		},
	}
	m := NewForTest(Options{})
	m.spawn(m.track(9, 0, fetchIssueJob("/repo", 12, r)))
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.NoError(t, rs[0].Err)
	assert.Equal(t, 12, rs[0].Issue.Number)
	assert.Equal(t, "Fix it", rs[0].Issue.Title)
}
```
Match `TestFetchIssue`'s mock JSON to what `github.View` parses (`session/github/cli.go`), and its executor mock to what `View` calls (`Output` or `CombinedOutput`). Adjust the fixture, not the assertions. `TestResumeWith` relies on `launch.Compose` adding `--model` for a Claude program. If the account `default` makes `RCAuthFor` look up a registry, use `Account: ""`. The assertion on `sonnet` must hold either way.

Run: `CGO_ENABLED=0 go vet ./core/ && CGO_ENABLED=0 go test ./core/ ./app/`
Expected: PASS. app is untouched apart from A1's renames.

### A5. Verify and commit

- [ ] **Step 1:** `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && CC=clang CGO_ENABLED=1 go test -race ./core/...`. Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - `TestCoreImportsNoUI` and `TestNoProductionCallsOfTestSeams` still pass.
  - The A1 rename is mechanical: `git diff` shows only renamed identifiers on changed lines.
  - The assertion count goes up and nothing is dropped.
- [ ] **Step 3: Commit.**
```bash
git add core/ session/instance.go app/
git commit -m "feat(core): instance views, IDs, and requests by ID" -m "Each instance gets a model-assigned InstanceID; InstanceView is the value a
client sees, published per workspace at Sync (ViewsChanged, first) when it
changed. Lifecycle requests by ID (Kill, Pause, Resume, ResumeWith, Recover,
Merge, Push, SendPrompt, Create, FetchIssue) answer with a Reply when asked
to. The pointer operations are renamed …Inst until the TUI switches. Daemon
stage 1C, package A." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

**Review focus for Package A:**
- `viewOf` reads only through locked accessors, apart from the two fields every caller reads unlocked today: `Title` and `IsWorkspaceTerminal`.
- `publishViews` publishes a workspace seen for the first time, forgets dropped ones, and drops IDs without reusing them.
- `Sync` puts the views first.
- Each request matches the TUI path it replaces: the same transitions, the same logging on a refused transition, the same owner and save snapshot. `Create` must match today's confirm Sync and Async order, which `app/state_issue_picker.go:openLaunchOptionsForNew` and `app/state_prompt.go` show.
- `deliverTracked` answers a Recover with the adopted instance's ID.

---

## Package B: the TUI renders instance views

`ui` stops taking `*session.Instance`. `app` keeps each slot's views as a store fed by `ViewsChanged`, and every read in `app` goes through a view. Writes still need an instance in this package. They reach it through a temporary bridge, `core.Model.InstanceOf(id)`. C moves them to requests and D removes the bridge. Bells become a TUI overlay. One commit at the end.

B1 adds the bridge and the store. B2 (ui) and B3 (app) are one switch: the build is red from B2 until B3 ends.

### B1. The bridge and the view store

- [ ] **Step 1: The bridge**, in `core/views.go`:
```go
// InstanceOf resolves id to its instance, nil when no loaded workspace
// holds it. A bridge: it serves the TUI's write paths until package C turns
// them into requests, and the script host until package D. Deleted in D.
func (m *Model) InstanceOf(id InstanceID) *session.Instance {
	inst, _ := m.lookup(id)
	return inst
}

// IDFor returns inst's ID, if a loaded workspace holds it. A bridge for the
// script host's resumed instances until package D. Deleted in D.
func (m *Model) IDFor(inst *session.Instance) (InstanceID, bool) {
	if m.Holding(inst) == nil {
		return 0, false
	}
	return m.idOf(inst), true
}

// IDForTest returns inst's ID (assigning one): for tests that build
// instances in the model and then find them through the TUI.
func (m *Model) IDForTest(inst *session.Instance) InstanceID { return m.idOf(inst) }
```
Put `IDForTest` in `core/seams.go` with the other seams.

- [ ] **Step 2: Create `app/views.go`, the store and its lookups.**
```go
package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/tmux"
)

// The TUI's view of instances: each slot keeps its workspace's
// core.InstanceView values (workspaceSlot.views), replaced wholesale by
// core.ViewsChanged and seeded from core.Model.Views when the slot is
// built. The rail and every other reader see them through slotRows, which
// lays the TUI's own overlays (bells; the pane ladder and the creation draft
// from package C) over them. Nothing in app holds a *session.Instance.

// slotRows is a slot's list source (ui.InstanceSource): its views with the
// TUI's overlays applied.
type slotRows struct {
	m *home
	s *workspaceSlot
}

// Rows returns the slot's rows, freshly copied.
func (r slotRows) Rows() []core.InstanceView { return r.m.rowsOf(r.s) }

// rowsOf copies s's views with the TUI's overlays applied.
func (m *home) rowsOf(s *workspaceSlot) []core.InstanceView {
	rows := make([]core.InstanceView, len(s.views), len(s.views)+1)
	copy(rows, s.views)
	for i := range rows {
		rows[i].Bell = m.bells[rows[i].ID]
	}
	return rows
}

// syncViews reseeds every open slot's store from the model. Production
// keeps the stores current through ViewsChanged; tests that change the
// model outside an Update call this before reading through the TUI.
func (m *home) syncViews() {
	for _, s := range m.openSlots() {
		s.views = m.core.Views(s.ws)
	}
}

// viewByID returns the row with id in any open slot (overlays applied), and
// its slot.
func (m *home) viewByID(id core.InstanceID) (*core.InstanceView, *workspaceSlot) {
	for _, s := range m.openSlots() {
		for _, v := range m.rowsOf(s) {
			if v.ID == id {
				return &v, s
			}
		}
	}
	return nil, nil
}

// viewBySession resolves a tmux session name (as pane events carry it) to
// the row of the instance whose agent session it is, across every open
// slot; nil for terminal-pane sessions and unknown names.
func (m *home) viewBySession(name string) (*core.InstanceView, *workspaceSlot) {
	if name == "" {
		return nil, nil
	}
	for _, s := range m.openSlots() {
		for _, v := range m.rowsOf(s) {
			if v.TmuxSession == name {
				return &v, s
			}
		}
	}
	return nil, nil
}

// activeViews returns the rows of every open slot that are active (see
// core.InstanceView.Active): those the pane clients and the snapshot scan
// cover.
func (m *home) activeViews() []core.InstanceView {
	var out []core.InstanceView
	for _, s := range m.openSlots() {
		for _, v := range m.rowsOf(s) {
			if v.Active() {
				out = append(out, v)
			}
		}
	}
	return out
}
```
The `tmux` import is for C's `sessionAlive`; drop it here if unused.

`workspaceSlot` gains a field, documented as: "views is this workspace's instances as the model last published them (core.ViewsChanged), in display order. Read them through slotRows/rowsOf, which add the TUI's overlays."
```go
	views []core.InstanceView
```
`home` gains this field, initialised in `newHome`:
```go
	// bells holds the instances whose pane rang a bell since they were last
	// focused (TUI state; laid over rows as InstanceView.Bell).
	bells map[core.InstanceID]bool
```

### B2. `ui` over views

The build is red from here until B3 ends.

- [ ] **Step 1: `ui/list.go`.**
  - `InstanceSource` becomes `interface{ Rows() []core.InstanceView }`, and `items()` returns `[]core.InstanceView`.
  - The selection is kept by ID: `selected core.InstanceID`, `hasSel bool` ("a row is selected"), `selectedIdx`, and `seen []core.InstanceID`. `resolveSelection`, `lostSelectionRow`, `replacedOnlyAt` and `remember` keep their rules, comparing IDs where they compared pointers. Rows' IDs are unique; the one draft row has ID 0.
  - API:
    - `GetSelectedInstance() *core.InstanceView` returns a copy of the selected row, or nil.
    - `SelectInstance(target)` becomes `SelectID(id core.InstanceID)`.
    - `GetInstanceByTitle(title) *core.InstanceView` returns a copy.
    - `GetInstances() []core.InstanceView` returns the rows.
    - `DisplayIndex(items []core.InstanceView, i int) int`.
    - `Down`, `Up`, `PageUp`, `PageDown`, `Top` and `Bottom` read `items[i].Status`.
  - `SetSessionPreviewSize` sizes each started, unpaused row's client through `l.panes.For(&row)`. It no longer runs `Pane().TmuxAlive()` (a tmux subprocess per row on the Update goroutine): a row with no attached client has a zero `Pane`, whose `SetPreviewSize` does nothing. Say so in the comment.
- [ ] **Step 2: Cards and overview.**
  - `ui/card.go`: `BuildCardData(v core.InstanceView, pane Pane, selected bool, spinnerFrame string, tailN int)`, `accountLabel(v core.InstanceView)`, `SortForOverview(items []core.InstanceView)`, `overviewTier(v core.InstanceView)`. Reads use the field map (Conventions).
  - `ui/overview.go`: `OverviewGroup.Items []core.InstanceView`.
- [ ] **Step 3: Menu, panes and pane views.** Each switches its instance type to `*core.InstanceView` (a copy, so nil means none):
  - `ui/menu.go`: `instance`, `Instance()`, `SetInstance()`.
  - `ui/diff.go`: `SetDiff`. When the view has no diff (`!v.HasDiff`), keep today's `GetDiffStats() == nil` branch; otherwise use `&v.Diff`.
  - `ui/terminal.go`: `UpdateContent` and `ensureSessionLocked`. Delete the dead `ensureSession`.
  - `ui/preview.go`: every method that takes an instance.
  - `ui/split_pane.go`: the `instance` field, `SetInstance`, `Instance`, `UpdateAgent`, `UpdateDiff`, `UpdateTerminal`, `ResetAgentToNormalMode` and `agentPaneTitle`.
  - `ui/cursor.go`: `CursorScreenPosition`.
  - `ui/panes.go`: `For(v *core.InstanceView) Pane` returns the zero `Pane` for nil, `!v.Started` or `v.Paused()`, and otherwise `p.Get(v.TmuxSession)`.
- [ ] **Step 4: ui tests.** Rewrite the `ui` tests' fixtures to build views as literals, e.g. `core.InstanceView{ID: 1, Title: "a", Status: session.Running, Started: true}`, giving every row a distinct ID. The list tests' `sliceSource` holds `[]core.InstanceView` and implements `Rows()`. Where a test compared instances by pointer (`assert.Same`), compare IDs. Keep every assertion.

### B3. `app` reads views

- [ ] **Step 1: The store's plumbing.**
  - `drainCore` calls `m.core.Sync()` where it called `m.core.Drain()`.
  - `applyCoreEvent` gains:
    ```go
    	case core.ViewsChanged:
    		if s := m.slotFor(ev.Workspace); s != nil {
    			s.views = ev.Views
    		}
    ```
  - `newSlotView` builds the slot first, then its list over `slotRows{m, slot}`, and seeds `slot.views = m.core.Views(ws)`. `newHome`'s classic slot and `enterGlobalMode`'s global view do the same. A workspace's first `Sync` after it loads publishes it again (`publishViews` publishes a workspace it hasn't published before), which is harmless.
  - **Copies go stale; refresh them.** The split pane and the menu used to hold the selected instance's live pointer, so a status, branch or diff change showed in the pane title, the paused screen and the menu's options at the next render. They now hold a copy. Add:
    ```go
    // refreshSelection repoints the split pane and the menu at a fresh copy
    // of the selected row. They hold copies, so a change to the row (a
    // status, a diff, an overlay) reaches them only through this. It has
    // none of instanceChanged's side effects (focus forwarding, the stored
    // ratio, the workbench retarget, the bell clear).
    func (m *home) refreshSelection() {
    	sel := m.list.GetSelectedInstance()
    	m.splitPane.SetInstance(sel)
    	m.menu.SetInstance(sel)
    }
    ```
    The `ViewsChanged` applier calls it when `s == m.workspaceSlot`, and so does every change to an overlay (bells here, the ladder and the draft in C). Check whether any other component keeps an instance between renders (`git grep -n 'instance \*core.InstanceView' -- ui/`). Refresh any you find the same way, or explain why its copy is meant to be a snapshot. The merge picker's source list, for example, is a snapshot by design.
- [ ] **Step 2: Bells.**
  - `bellMsg`: `if v, _ := m.viewBySession(msg.session); v != nil && (sel == nil || v.ID != sel.ID) { m.bells[v.ID] = true; m.refreshPeerSections() }`.
  - `instanceChanged` clears the selected row's bell: `delete(m.bells, selected.ID)`, under the same not-in-overview condition as today.
  - `jumpWaiting`, `peerSectionFor` and `overviewTier` (via the rows) read `v.Bell`.
- [ ] **Step 3: Pane events and the tick.**
  - `paneDirtyMsg`, `paneQuietMsg`, `redetectMsg`, `ptyDeadMsg` and `bellMsg` resolve the session with `m.viewBySession(msg.session)`, not `m.core.InstanceForSession`.
  - They call `m.core.PaneOutput(v.ID)`, `m.core.PaneQuiet(v.ID)` and `m.core.VerifyDead(v.ID)`. `VerifyDead` now queues its job, so drop the `coreCmd` around it.
  - The tick calls `m.core.Tick(selectedID)`, with 0 when nothing is selected.
  - `livePaneNames` and `snapshotScan` walk `m.activeViews()`. `core.StatusEligible(inst)` and `core.ActiveInstance(inst)` become `v.Active()`.
  - `statusDetectCmd` takes `(v core.InstanceView, pane ui.Pane)`, and `statusDetectedMsg` and `snapshotStatus` carry `id core.InstanceID` and `title string` instead of the instance. Their appliers still change status in B, through the bridge: `inst := m.core.InstanceOf(msg.id)`, skipping when it is nil. C replaces that with the ladder overlay.
- [ ] **Step 4: The appliers of B's events read IDs.**
  - `SessionLaunched` → `replacePane(view of ev.ID)`; `Reactivated` → `ensurePane(view of ev.ID)`.
  - `Alive` loops over `ev.IDs`.
  - `applyStarted` and `applyRecovered` use `ev.ID` and `ev.Title`; the view comes from `m.viewByID(ev.ID)`, which can be nil when the owner is not loaded.
  - `ensurePane` and `replacePane` take `*core.InstanceView` and read `v.TmuxSession`, `v.Active()` and `v.SessionProgram`.
  - The core events keep their pointer fields until C, but nothing in app reads them.
- [ ] **Step 5: Every other read site.**
  - Variables holding `*session.Instance` for reading become `*core.InstanceView`. `m.list.GetSelectedInstance()` already returns one.
  - Reads follow the field map. The app inventory in the plan's research covers: `app.go`, `core_glue.go`, `completions.go`, `events.go`, `panes.go`, `intents.go`, `state_*.go`, `interact.go`, `workbench.go`, `overview.go`, `workspaces.go`, `help.go`, `accounts.go` and `app_scripts.go`.
  - `attachingInstance` becomes `attachingID core.InstanceID`, set and compared by ID.
  - `startFullScreenAttachMsg.instance` and `attachDoneMsg.instance` become `*core.InstanceView`. Guard `attachDoneMsg`'s title read, which today dereferences a possibly nil instance at app.go:1300.
  - `pendingMergeTarget` and `pendingMergeSourceItems` hold views.
  - `helpTypeInstanceStart.instance` is a `*core.InstanceView`.
  - `windowTitle`, `forwardFocus`, `attachCursor`, `previewTickMsg`, the mouse paths, the workbench and the overview all read views.
  - Delete the unused `pendingAttachTarget` field.
- [ ] **Step 6: Write sites go through the bridge**, until C. Each site where app mutates an instance or hands one to a `…Inst` operation resolves it first: `inst := m.core.InstanceOf(v.ID)`, and does nothing when that is nil. That covers:
  - the three ladder `TransitionTo` calls;
  - the Loading transitions in `runStashSelectedOpts` and `runRestartWithOptionsSelected`;
  - `applyChosenLaunch`;
  - the full-screen attach's `TmuxSession()`;
  - the six `Pane().TmuxAlive()` guards;
  - `KillInst`, `PauseInst`, `ResumeInst`, `ResumeIfLoadingInst`, `RecoverInst`, `MergeInst`, `PushInst` and `SendPromptInst`;
  - `runMergeSelected`'s `GetGitWorktree`.

  The creation flows keep `pendingNew *session.Instance` in this package, since the instance they edit is unstarted and in the workspace, as before. Its row shows like any other. The script host keeps handing `*session.Instance` to Lua: `newScriptHost` maps the selected row and the rows through `m.core.InstanceOf`, and `handleScriptDone`'s resumed instances go through `m.core.IDFor`, then `m.viewByID`, then `replacePane`.
- [ ] **Step 7: Tests.**
  - `wireCore` ends with `m.syncViews()`.
  - A test that adds or changes instances after wiring (`ws.Add`, a `TransitionTo` it then reads through the TUI) calls `m.syncViews()` before reading.
  - Where a test compared the selection or a message's instance by pointer, compare IDs: `testModel(m).IDForTest(inst)`, or `m.core.IDForTest(inst)` while `home.core` is still `*core.Model`.
  - Tests that built `statusDetectedMsg`, `snapshotStatus` and so on with an instance build them with its ID.
  - Keep every assertion.

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: PASS.

### B4. Verify and commit

- [ ] **Step 1:** Run `CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/...` and `CGO_ENABLED=0 go test -tags e2e ./e2e/...`. Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - `git grep -n 'session\.Instance' -- 'ui/*.go' 'ui/**/*.go' ':!*_test.go'` prints only comments.
  - `git grep -n 'InstanceForSession\|ActiveInstances\|\.Holding(' -- 'app/*.go' ':!app/*_test.go'` prints nothing.
  - The assertion count holds.
- [ ] **Step 3: Commit.**
```bash
git add core/ ui/ app/
git commit -m "refactor(ui,app): the TUI renders instance views" -m "Each slot keeps its workspace's views (fed by core.ViewsChanged) and ui reads
only core.InstanceView; app's reads go through views, with selection kept by
ID. Bells are a TUI overlay. Writes reach the instance through a temporary
bridge (core.Model.InstanceOf) until package C. Daemon stage 1C, package B." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

**Review focus for Package B:**
- **The store's freshness.** Every model change reaches the rows within the same Update: `Sync` at every drain, views first. A newly built slot must not show an empty or stale list (the seed).
- **The list's ID selection** against B's old pointer rules: the draft row's ID 0 (C), duplicate IDs, and the overview cursor.
- **Places that held a live pointer across Updates** and now hold a copy that goes stale:
  - `pendingMergeSourceItems`, `helpTypeInstanceStart` and the menu's instance;
  - the split pane's `instance`, which must be refreshed by `instanceChanged` on every view change.

  Check that `instanceChanged` (or the `ViewsChanged` applier) refreshes them whenever a view they show changes. A status change used to show through the shared pointer at once.
- **Bells:** set and cleared under the old conditions.

---

## Package C: the TUI acts on instances only by request

Every remaining write in `app` (other than the script host's, which D handles) becomes a request by ID. The creation flows hold a draft. The pane ladder becomes a display overlay. Kill and pause lose their TUI hooks. The TUI uses tmux by session name. Prompt sends are serialized against later input. The pointer API leaves core, and `core.Core` becomes `home.core`'s type. A test enforces the boundary for `app` and `ui`. One commit at the end.

C1–C4 each keep the build green. C5 (the interface and deletions) is a switch.

### C1. Requests from the TUI

**Files:** Create `app/requests.go`. Modify `app/intents.go`, `app/state_mergepicker.go`, `app/state_quick_interact.go`, `app/state_prompt.go`, `app/workbench.go`, `app/core_glue.go`, `app/app.go`.

- [ ] **Step 1: The TUI's request book, `app/requests.go`.**
```go
package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"

	tea "charm.land/bubbletea/v2"
)

// pendingReq is what the TUI asked for with a ReqID, so its Reply can find
// the flow waiting on it. Exactly one field is set. (Package D adds
// script, a Lua call waiting to resume, and issue, an issue fetch.)
type pendingReq struct {
	create *pendingCreate // a creation flow's Create (C2)
	send   *pendingSend   // a prompt send (C4)
}

// newReq records p and returns the ReqID to send with its request.
func (m *home) newReq(p pendingReq) core.ReqID {
	if m.pending == nil {
		m.pending = make(map[core.ReqID]pendingReq)
	}
	m.nextReq++
	m.pending[m.nextReq] = p
	return m.nextReq
}

// handleReply routes a Reply to the flow that asked for it.
func (m *home) handleReply(r core.Reply) tea.Cmd {
	p, ok := m.pending[r.Req]
	if !ok {
		log.For("app").Warn("reply.unexpected", "req", uint64(r.Req))
		return nil
	}
	delete(m.pending, r.Req)
	switch {
	case p.create != nil:
		return m.createReplied(p.create, r)
	case p.send != nil:
		return m.sendReplied(p.send, r)
	}
	return nil
}
```
`home` gains `pending map[core.ReqID]pendingReq` and `nextReq core.ReqID`. `applyCoreEvent` gains `case core.Reply: return m.handleReply(ev)`. Package D adds the `script` and `issue` fields, their types, and their `handleReply` cases.

- [ ] **Step 2: Operations by ID.** In `app/intents.go`:
  - **`runKillSelected`:** the confirmation task becomes `overlay.ConfirmationTask{Sync: func() { m.core.Kill(selected.ID, 0) }}`. The model's pre-step (Deleting) and job both run there, and the job reaches the runtime at the end of that Update.
  - **`runKillSelectedNoConfirm`:** calls `m.core.Kill(selected.ID, 0)` and returns `nil` for its Cmd.
  - **`runStashSelectedOpts`:** a confirmed pause's `Sync` calls `m.core.Pause(selected.ID, 0)`. The no-confirm path calls it directly. Both drop their own `TransitionTo(Loading)`, which `Pause` now does.
  - **`runResumeSelected`:** `m.core.Resume(selected.ID, 0)`, returning `tea.Batch(tea.RequestWindowSize, m.instanceChanged())`.
  - **`runRestartWithOptionsSelected`:** the confirmation's `Sync` resets the state and menu as today, then calls `m.core.ResumeWith(selected.ID, newOpts, base, 0)`. Its `Async` is `tea.RequestWindowSize`. Delete `applyChosenLaunch`, which `ResumeWith` and `Create` now do.
  - **`runRecoverSelected`:** `m.core.Recover(sel.ID, 0)`, returning `m.instanceChanged()`.
  - **The merge commit** (`state_mergepicker.go`): `m.core.Merge(target.ID, source.ID, 0)`.
  - **Push:** `m.core.Push(selected.ID, 0)`.
  - **`closeTerminalFor`:** delete it (C3 covers the terminal clients). `KillInst` and `PauseInst` lose their `beforeKill`/`beforePause` parameters in C5.

  Each of these used to return the job as a Cmd. The request queues it in the outbox instead, and the Update's drain hands it to the runtime, so the Cmds these functions return no longer include it.

- [ ] **Step 3: The status of a request the TUI didn't need to know about.** With `ReqID` 0 a refusal is only logged (`request.refused`). The paths these requests replace already ignored the same refusals, or logged them (`resume.skipped`), so nothing new reaches the user.

### C2. Drafts

**Files:** Create `app/drafts.go`. Modify `app/intents.go` (`runNewInstance`, `runPromptNewInstance`), `app/state_new.go`, `app/state_prompt.go`, `app/state_issue_picker.go`, `app/state_launch_options.go`, `app/remote_control.go`, `app/completions.go`, `app/views.go`.

- [ ] **Step 1: `app/drafts.go`.**
```go
package app

import (
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"

	tea "charm.land/bubbletea/v2"
)

// draft is a creation flow's session-to-be. It belongs to the TUI alone:
// the model hears of it only when the flow confirms (Create). Until then it
// is a row with ID 0 at the end of its slot's rail. Cancelling the flow
// discards it; there is no instance to kill. One draft at a time.
type draft struct {
	slot    *workspaceSlot
	path    string // the repo it will work in (m.repoPath() when the flow began)
	title   string
	prompt  string
	program string
	branch  string // the branch picker's choice; "" for a new branch
	issue   int
	created time.Time
}

// row is the draft as the rail shows it.
func (d *draft) row(m *home) core.InstanceView {
	v := core.InstanceView{
		Title:       d.title,
		RepoPath:    d.path,
		Program:     d.program,
		Status:      session.Ready,
		StatusSince: d.created,
		Issue:       d.issue,
	}
	if d.issue != 0 {
		snap, known := m.core.GitHubSnapshot(d.path)
		v.GitHub = github.StateFor(snap, known, "", d.issue)
	}
	return v
}

// newDraft opens a draft in the focused slot, selects its row and returns it.
func (m *home) newDraft(title, prompt string, issue int) *draft {
	m.draft = &draft{
		slot:    m.workspaceSlot,
		path:    m.repoPath(),
		title:   title,
		prompt:  prompt,
		program: m.core.Program(),
		issue:   issue,
		created: time.Now(),
	}
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.refreshSelection()
	return m.draft
}

// discardDraft drops the open draft, if any. Every creation-flow cancel path
// goes through it; nothing in the model needs undoing.
func (m *home) discardDraft() {
	m.draft = nil
	m.refreshSelection()
}

// pendingCreate is a confirmed draft waiting for its Create's Reply.
type pendingCreate struct {
	slot  *workspaceSlot
	title string
}

// confirmDraft sends d's Create, with opts from the Session Launch Options
// modal, and drops the draft. The new instance's row takes the draft's
// place (both are appended), and createReplied selects it.
func (m *home) confirmDraft(d *draft, opts launch.Options) {
	req := m.newReq(pendingReq{create: &pendingCreate{slot: d.slot, title: d.title}})
	m.draft = nil
	m.core.Create(d.slot.ws, core.NewInstance{
		Title:   d.title,
		Path:    d.path,
		Program: d.program,
		Prompt:  d.prompt,
		Branch:  d.branch,
		Issue:   d.issue,
		Launch:  opts,
		Start:   true,
	}, req)
}

// createReplied finishes a confirmed draft: a refusal is shown; otherwise
// the new row is selected where the draft's was, if the selection is
// still there. The start's own completion (core.Started) follows later and
// does the rest.
func (m *home) createReplied(p *pendingCreate, r core.Reply) tea.Cmd {
	if r.Err != nil {
		return m.handleError(fmt.Errorf("create %s: %w", p.title, r.Err))
	}
	if s := p.slot; s.list != nil {
		if sel := s.list.GetSelectedInstance(); sel == nil || sel.ID == 0 || sel.ID == r.ID {
			s.list.SelectID(r.ID)
		}
	}
	m.refreshSelection()
	return nil
}
```
`home`'s `pendingNew *session.Instance` becomes `draft *draft`, with its comment rewritten to match. `rowsOf` appends `m.draft.row(m)` when `m.draft != nil && m.draft.slot == s`. Use the draft's slot pointer, so a draft never shows in another workspace's rail.

- [ ] **Step 2: Rewrite the creation flows over the draft.** Keep each function's comment, and update the names it mentions.
  - **`runNewInstance` and `runPromptNewInstance`:** keep the limit and latch guards, then `m.newDraft("", "", 0)` replaces `session.NewInstance` + `ws.Add` + `SetSelectedInstance` + `pendingNew`. `runPromptNewInstance` keeps its fetch and `resolveBaseBranchCmd`.
  - **`handleStateNewKey`:** edits `m.draft.title`, with the same 32-cell cap and the same Backspace and Space handling. `SetTitle` refused only a started instance, which a draft never is. On Enter, `preservedTitleErr(m.draft.title)` and the prompt-or-launch-options branch run as before, now opening `openLaunchOptionsForNew(m.draft, "")`. Both cancel paths (ctrl+c, Esc) call `m.discardDraft()` with no kill Cmd. `instanceChanged` still follows.
  - **`handleStatePromptKey`:** the target is `m.draft` when one is open, else the selected row (a running session).
    - For the draft: `branch`, `program` and `prompt` are set where `SetSelectedBranch`, `SetProgram` and `SetPrompt` were.
    - The `#n` shorthand keeps the draft open (`m.draft` stays set and its row stays shown) and runs `issueExpandCmd(repo, n, m.draft, rest, prompt, selectedBranch)`. That function and `issueExpandedMsg` carry `*draft` instead of the instance.
    - The start closure is the one in `openLaunchOptionsForNew`. Delete the duplicate in this file and call `m.openLaunchOptionsForNew(m.draft, selectedBranch)`.
  - **`openLaunchOptionsForNew(d *draft, selectedBranch string)`:**
    - Set `d.branch` when `selectedBranch != ""`.
    - The `pendingLaunchOptions` closure builds `startTask := overlay.ConfirmationTask{Sync: func() { m.promptAfterName = false; m.state = stateDefault; m.menu.SetState(ui.StateDefault); m.confirmDraft(d, opts) }, Async: tea.RequestWindowSize}`.
    - The remote-control check reads `d.program` and `opts.Account` as before.
    - `pendingLaunchOptionsCancel` stays `killPendingLaunchOptionsCancel`, which now calls `discardDraft`.
    - The overlay is built from `launch.FromConfig(m.appConfig())` and `d.program`.
  - **`handleIssuePicked`:**
    - Keep every guard.
    - `session.NewInstance` + `SetIssue` + `ws.Add` + select become `m.newDraft(slug, github.SeedPrompt(...), msg.issue.Number)`, with the prompt built as today.
    - `m.core.ExpediteGitHub()` stays. Drop `ApplyGitHubState()`: the draft row computes its own badge, and `Create` re-joins.
    - Then `openLaunchOptionsForNew(m.draft, "")`.
  - **`handleIssueExpanded`:** its guards keep their order: repo, state, then the error. Add one more first: when `m.draft != msg.draft`, a newer flow replaced it, so drop the expansion with `issueExpandDropped`'s notice. Otherwise set `d.prompt` (and `d.issue` on success), then `openLaunchOptionsForNew(d, msg.selectedBranch)`.
  - **`promptRemoteControlBlocked`:** its `OnCancel` calls `m.discardDraft()` and sets the zero task (no kill job).
  - **Delete:** `dropPendingNew` and `core.DropUnstarted`. Delete the latter in C5, once nothing calls it.
- [ ] **Step 3: Drafts in the TUI's reads.**
  - The draft row has ID 0.
  - Every place that acts on "the selected row" by ID must treat ID 0 as not-a-session. These are the intents' predicates (`selectedNotBusyNotWorkspace` and the rest), quick input, inline attach, the workbench and the merge picker. The draft row is unstarted and Ready, so today's `Started`/status predicates already refuse most actions. Add `v.ID == 0` where a predicate would pass for an unstarted Ready row.
  - `instanceChanged` treats the draft like the unstarted instance it replaces.

### C3. The display-only ladder, terminal clients and tmux by name

**Files:** Modify `app/app.go`, `app/views.go`, `app/events.go`, `app/panes.go`, `app/interact.go`, `app/state_inline_attach.go`, `app/state_quick_interact.go`, `app/intents.go`, `app/workbench.go`, `session/tmux/session.go` (only if `FullScreenAttachCmd` needs more than the name).

- [ ] **Step 1: The ladder overlay.**
```go
// ladderState is a status the TUI scraped from a pane (its content
// ladder), shown in place of the model's status for an active instance
// whose view has no reported status (decision 6). The model never sees it.
type ladderState struct {
	status session.Status
	since  time.Time
}

// setLadder records st as id's scraped status. A repeat of the same status
// keeps its since.
func (m *home) setLadder(id core.InstanceID, st session.Status) {
	if m.ladder == nil {
		m.ladder = make(map[core.InstanceID]ladderState)
	}
	if prev, ok := m.ladder[id]; ok && prev.status == st {
		return
	}
	m.ladder[id] = ladderState{status: st, since: time.Now()}
	m.refreshSelection()
}
```
`rowsOf` lays the overlay over each view after the bell:
```go
		if l, ok := m.ladder[rows[i].ID]; ok && rows[i].Active() && !rows[i].StatusReported {
			rows[i].Status, rows[i].StatusSince = l.status, l.since
		}
```
The `ViewsChanged` applier prunes `m.ladder`: it drops every entry whose ID is no longer in the slot's new views, or whose view is inactive or reported. A ladder status must not resurface after a pause/resume or a Claude report.

Rewrite the three ladder sites. Each reads the model's view, never the overlaid row, to decide eligibility:
- **`statusDetectedMsg`:**
  1. Look up the row: `v, _ := m.viewByID(msg.id)`. The overlay only swaps one active status for another, so `Active()` and `StatusReported` read the same on the row as on the model's view.
  2. If it is nil or `!v.Active()`, return. On an error, log and return.
  3. If `v.StatusReported`, return: the model applies the reported status (`deliverHealth`), and a report retires the re-detection.
  4. Otherwise compute `target` with the same ladder and call `m.setLadder(msg.id, target)`. The `maybeRedetect` rule is unchanged (`msg.updated` → re-arm).
  5. Delete the `AdoptClaudeStatus` and `TransitionTo` calls.
- **`snapshotStatusMsg`:** the same shape, per result, keeping the `MarkOutput` order (before the reported check) and skipping a capture error.
- **`paneDirtyMsg`:** the Ready→Running promotion becomes `if row.Status == session.Ready && !row.StatusReported { m.setLadder(row.ID, session.Running) }`, where `row` is the overlaid row. The status the user sees is the one promoted.
- **`updateTabBarStatuses`, `jumpWaiting`, the overview and the cards** read rows, so they show the overlaid status.

Today the ladder also persisted (the model saved Ready or Prompting for a non-Claude session). A non-Claude session's saved status is now its lifecycle status, Running. Note this in the commit message: it is decision 6.

- [ ] **Step 2: Terminal-pane clients are released by the prune.** `prunePanes` keeps doing what it does, and also detaches, per open slot, the terminal-pane clients of titles that slot has no active row for:
```go
	for _, s := range m.openSlots() {
		keep := make(map[string]bool)
		for _, v := range m.rowsOf(s) {
			if v.Active() {
				keep[v.Title] = true
			}
		}
		cmds = append(cmds, releaseClientsCmd(attachedClients(s.splitPane.Terminal().DetachExcept(keep))))
	}
```
It returns the batch of both releases. `Instance.Kill` and `Instance.Pause` end the terminal shell by name (`CloseRelatedSession`). The client then reads EOF, and the next prune, after the completion's `ClientsStale`, releases it. Check that `DetachExcept` only detaches (`PausePreview`), never closes (kills), and that it is safe to call on every tick.

- [ ] **Step 3: tmux by name.**
  - The full-screen attach builds its command as `tmux.NewSession(v.TmuxSession, v.SessionProgram).FullScreenAttachCmd(attachCtx)`. Check that `FullScreenAttachCmd` uses only the session name (and the socket). If it reads state a fresh `Session` lacks, add a package function `tmux.AttachCmd(ctx, name)` that both use.
  - Add to `app/views.go`:
    ```go
    // sessionAlive reports whether the tmux session name exists (has-session,
    // exact target), as AgentPane.TmuxAlive did through the instance. Tests
    // replace the probe (aliveProbe); production asks tmux.
    func (m *home) sessionAlive(name string) bool {
    	if m.aliveProbe != nil {
    		return m.aliveProbe(name)
    	}
    	return name != "" && tmux.NewSession(name, "").DoesSessionExist()
    }
    ```
    `home` gains `aliveProbe func(name string) bool`, nil in production. The six `Pane().TmuxAlive()` guards become `m.sessionAlive(v.TmuxSession)`.
  - Tests that drove those guards through an instance's mock tmux (`aliveCmdExecForTest`, `deadCmdExecForTest` and similar) set `m.aliveProbe`.
  - `runMergeSelected`'s dirty check builds its worktree handle from the view: `git.NewGitWorktreeFromStorage(target.RepoPath, target.WorktreePath, target.TmuxSession, target.Branch, "", false, "")`. Check what `IsDirty` reads from the handle. If it needs a field the view lacks, add that field to `InstanceView` and `viewOf`, and say so. This check still runs on the Update goroutine, as before; that is a 1D follow-up.

### C4. Prompt sends are serialized

- [ ] **Step 1:** Add to `app/requests.go`:
```go
// pendingSend is a prompt send waiting for its Reply. While it is in flight
// the TUI holds input to its instance (decision 12): keys typed into inline
// attach, or a second send, would land between the paste and its Enter.
type pendingSend struct {
	id    core.InstanceID
	title string
}

// sendPrompt sends text to v's agent through the model, holding further
// input to v until the send's Reply.
func (m *home) sendPrompt(v *core.InstanceView, text string) {
	if m.sending == nil {
		m.sending = make(map[core.InstanceID]bool)
	}
	m.sending[v.ID] = true
	m.core.SendPrompt(v.ID, text, m.newReq(pendingReq{send: &pendingSend{id: v.ID, title: v.Title}}))
}

// sendReplied releases the hold. The model has already shown a failure as a
// notice ("prompt not sent to …"), so there is nothing more to do.
func (m *home) sendReplied(p *pendingSend, r core.Reply) tea.Cmd {
	delete(m.sending, p.id)
	return nil
}

// sendingTo reports whether a prompt send to id is in flight; when it is,
// it says so on the info line.
func (m *home) sendingTo(v *core.InstanceView) bool {
	if v == nil || !m.sending[v.ID] {
		return false
	}
	m.errBox.SetInfo(fmt.Sprintf("still sending the last prompt to %s", v.Title))
	return true
}
```
`home` gains `sending map[core.InstanceID]bool`.
  - The three sends call `m.sendPrompt(selected, text)`, replacing `coreCmd(m.core.SendPromptInst(...))`. These are the prompt overlay's send to a running session, the quick input to the agent, and the workbench review's send.
  - `runInlineAttachAgent`, `runQuickInputAgent` and those three send paths return early when `m.sendingTo(selected)`. Inline attach that is already active keeps forwarding keys: the hold stops only entering it. A test pins that inline attach and a second quick send are refused while a send to that instance is in flight, and allowed after its Reply.

### C5. The `core.Core` interface, deletions and enforcement

The build is red from here until this checkpoint ends.

- [ ] **Step 1: Events lose their pointers.** Delete the `Instance`/`Instances` fields of `SessionLaunched`, `Reactivated`, `Started`, `Recovered` and `Alive`, and every place core sets them. app already reads only IDs.
- [ ] **Step 2: The pointer API leaves the exported surface.** `InstanceOf`, `IDFor` and `AdoptForScript` stay until D.
  - Rename `StartInst`, `KillInst`, `PauseInst`, `ResumeInst`, `ResumeIfLoadingInst`, `RecoverInst`, `MergeInst`, `SendPromptInst`, `PushInst`, `TickInst`, `PaneOutputInst`, `PaneQuietInst` and `VerifyDeadInst` to lower case. They stay as the implementation of the ID requests.
  - Delete `KillInst`'s `beforeKill` and `PauseInst`'s `beforePause` parameters, and their calls.
  - Delete `DropUnstarted`, `ResumeInst` (if unused after C1) and `InstanceForSession`.
  - Unexport `ActiveInstances`, `Holding`, `ActiveInstance`, `StatusEligible` and `AdoptClaudeStatus`. Keep them unexported where core still uses them (`applyLiveness`, `deliverHealth` and so on), and delete them otherwise.
  - Keep `InstanceOf` and `IDFor`: the script host uses them until D.
- [ ] **Step 3: `Workspace`'s instance edits leave the exported surface.**
  - `Add`, `Remove`, `Replace`, `Holds`, `ByTitle` and `Instances` become `add`, `remove`, `replace`, `holds`, `byTitle` and `instances`.
  - Add the seams `AddForTest(inst)` and `InstancesForTest()` in `core/seams.go`.
  - app tests' `ws.Add(` becomes `ws.AddForTest(`; their reads of `ws.Instances()` become `ws.InstancesForTest()`.
  - `TestNoProductionCallsOfTestSeams` now also guards production code against touching a workspace's instances.
  - `handleScriptDone` still adds a script's queued `*session.Instance` values until D. It does so through a temporary bridge on the model, deleted in D:
    ```go
    // AdoptForScript adds an instance a script built (ctx:new_instance) to ws.
    // A bridge for the script host until package D routes ctx:new_instance
    // through Create. Deleted in D.
    func (m *Model) AdoptForScript(ws *Workspace, inst *session.Instance) { ws.add(inst) }
    ```
- [ ] **Step 4: `core/iface.go`.**
```go
package core

// Core is the session model as its clients use it: every method the TUI
// calls. In stage 1C the instance half is final: requests by InstanceID,
// InstanceView values, Reply. The workspace and account half still passes
// the model's own objects (*Workspace, *config.WorkspaceRegistry,
// *account.Registry, …), and stage 1D converts it to values before the
// model moves to its own goroutine. InstanceOf and IDFor are a bridge for
// the script host until package D. *Model is the only implementation.
type Core interface {
	// (every method app calls, with its exact signature, grouped by area
	// with a comment per group: loop (Sync, Deliver, Begin), workspaces,
	// instances (views and requests), Claude status and the tick, GitHub,
	// accounts.)
}

var _ Core = (*Model)(nil)
```
Build the method list from `git grep -ohE 'm\.core\.[A-Z][A-Za-z]+' -- 'app/*.go' ':!app/*_test.go' | sort -u`, taking each signature from `core/`. `home.core` becomes `core.Core`, and `newHome` assigns the `*core.Model` from `core.New`. App tests reach the seams through:
```go
// testModel returns the home's model for its test seams.
func testModel(m *home) *core.Model { return m.core.(*core.Model) }
```
(in `app/testcore_test.go`). Every `m.core.XForTest(` in app tests becomes `testModel(m).XForTest(`.

- [ ] **Step 5: Enforcement, `internal/testenv/instance_enforce_test.go`.**
```go
// TestTUIHoldsNoInstance fails when the TUI (app, ui and its subpackages)
// names a session instance in production code: the type, its constructors
// or its options. The TUI sees instances only as core.InstanceView values
// and changes them only by request. The script host (app/app_scripts.go)
// is exempt until stage 1C package D. Test files are exempt.
func TestTUIHoldsNoInstance(t *testing.T)
```
Walk the module like `TestNoProductionCallsOfTestSeams`, over non-test `.go` files under `app/` and `ui/`, skipping `app/app_scripts.go`. Flag every `*ast.SelectorExpr` whose package is the `session` import and whose name is `Instance`, `NewInstance`, `FromInstanceData` or `InstanceOptions`, and every selector named `InstanceOf` or `IDFor`. Resolve the import alias as the existing enforcement tests do. Self-check: a temporary file holding `var _ *session.Instance` must fail the test. Delete the file before committing.

- [ ] **Step 6: Tests.**
  - Creation-flow tests drive drafts. Where they asserted on the pending instance (`pendingNew.Title`, `inst.Prompt()`), assert on `m.draft` until confirm, and on the created instance's view after it (through `testModel(m)` and the Reply).
  - Ladder tests (`app/status_redetect_test.go` and the rest) assert on the row's status and on `m.ladder`, and that the model's status is unchanged for a non-Claude instance.
  - Kill and pause tests no longer pass a terminal hook. The terminal pane's client release is covered by a prune test. Pin that a killed instance's terminal client is detached by the prune after `KillResult`, and that an active one's is kept.
  - Keep every assertion. If a test pinned a removed behaviour, such as the kill job closing the terminal before `Kill`, replace it with the behaviour that took its place and report it.

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: PASS.

### C6. Verify and commit

- [ ] **Step 1:** Run `CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/...` and `CGO_ENABLED=0 go test -tags e2e ./e2e/...`. Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - `TestTUIHoldsNoInstance`, `TestCoreImportsNoUI` and `TestNoProductionCallsOfTestSeams` pass.
  - `git grep -n 'TransitionTo\|SetTitle\|SetPrompt\|SetProgram\|SetIssue\|SetBranchPrefix\|SetSelectedBranch\|SetBellPending\|SetLaunchOptions\|SetAccount' -- 'app/*.go' ':!app/*_test.go' ':!app/app_scripts.go'` prints nothing.
  - The assertion count holds.
- [ ] **Step 3: Commit.**
```bash
git add core/ app/ ui/ session/ internal/
git commit -m "refactor(app,core): the TUI acts on instances only by request" -m "Lifecycle actions are requests by InstanceID; creation flows hold a draft row
until core.Create; the pane-scraped status is a display overlay (a non-Claude
session's saved status is now its lifecycle status); kill and pause lose their
TUI hooks (the prune releases terminal clients); the TUI uses tmux by session
name; prompt sends hold further input to their session until they land. The
TUI's calls are the core.Core interface. TestTUIHoldsNoInstance enforces the
boundary for app and ui. Daemon stage 1C, package C." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

**Review focus for Package C:**
- **Drafts.** Each creation path against b81f34d's and 9030942's behaviour: naming, the prompt overlay, `#n` expansion, the issue picker, the remote-control-blocked prompt, cancel at every step, and a flow interrupted by a workspace switch or a script. In particular:
  - the draft row never shows in another workspace;
  - the selection moves to the created row;
  - a refused `Create` leaves no residue.
- **The ladder overlay.** It never overrides a reported or lifecycle status, it is pruned when the view changes, and `maybeRedetect` still terminates. Status-dependent keys use the shown status, and the model's gates use the model's status.
- **Kill/pause without hooks.** The terminal shell still ends, the TUI's client is released, and nothing shows a dead terminal for a live session.
- **The send hold.** It is always released, including on a refused request (the `ErrNoSession` Reply).
- **`core.Core` completeness**, and that tests reach the seams only through `testModel`.

---

## Package D: Lua and issue fetches go through the model

The script engine sees instances as views. Lua's lifecycle calls (`kill`, `pause`, `resume`, `send_prompt`) and `ctx:new_instance` become intents that yield until the model's Reply. `send_keys`, `tap_enter` and `preview` act on tmux by session name. The issue picker's fetches become `core.FetchIssue`. The bridges (`InstanceOf`, `IDFor`, `AdoptForScript`) are deleted, and `TestTUIHoldsNoInstance` covers `script/` and `app/app_scripts.go`. One commit at the end.

D1 is a switch (the script engine and its host change together). D2 is green on its own.

### D1. The script engine over views and intents

**Files:** Modify `script/host.go`, `script/intent.go`, `script/engine.go`, `script/api_actions.go`, `script/userdata_instance.go`, `script/userdata_ctx.go`, `script/userdata_worktree.go`, `script/host_fake_test.go` and the script tests; `app/app_scripts.go`, `app/requests.go`; `core/views.go`, `core/workspaces.go` (bridge deletion); `internal/testenv/instance_enforce_test.go`.

- [ ] **Step 1: The host and the intents.** In `script/host.go`:
  - `SelectedInstance() (core.InstanceView, bool)` and `Instances() []core.InstanceView` replace the pointer versions.
  - `SendTerminalKeys(v core.InstanceView, text string) error` replaces its pointer version.
  - Delete `QueueInstance` and `InstanceResumed`.

  `script` now imports `core` for the view types. That is allowed: `core` must not import `script`, and `TestCoreImportsNoUI` already guards that direction.

  In `script/intent.go`, add:
```go
// InstanceOpIntent runs a lifecycle operation on an instance through the
// model: inst:kill(), inst:pause(), inst:resume() and inst:send_prompt().
// The host resumes the coroutine with the outcome once the model replies:
// nothing on success, an error message the Lua method raises otherwise.
type InstanceOpIntent struct {
	ID    core.InstanceID
	Title string
	Op    string // "kill", "pause", "resume", "send_prompt"
	Text  string // send_prompt's text
}

func (InstanceOpIntent) intent() {}

// CreateInstanceIntent creates an instance, unstarted, in the workspace
// the dispatch began in (ctx:new_instance). The host resumes the coroutine
// with the new instance, or an error message ctx:new_instance raises.
type CreateInstanceIntent struct {
	Title, Program, Path, Prompt, Branch string
}

func (CreateInstanceIntent) intent() {}

// ResumeValue is what the call that yielded returns when its coroutine is
// resumed: nothing (the zero value), an error message (Err, which the
// yielding method raises), or an instance (ctx:new_instance's result).
type ResumeValue struct {
	Err      string
	Instance *core.InstanceView
}
```

- [ ] **Step 2: Resume with a value.**
  - `Engine.ResumeWithHost(ctx context.Context, id IntentID, h Host, v ResumeValue) error` takes the value and hands `e.luaValue(v)` to `resumeLocked` where it handed `lua.LNil`:
    ```go
    // luaValue turns a resume value into the Lua value the yielding call
    // returns. Built inside the engine's lock: the Lua state is not
    // goroutine-safe.
    func (e *Engine) luaValue(v ResumeValue) lua.LValue {
    	switch {
    	case v.Err != "":
    		return lua.LString(v.Err)
    	case v.Instance != nil:
    		return pushInstance(e.L, v.Instance)
    	}
    	return lua.LNil
    }
    ```
    `pushInstance` must build the userdata without pushing it on the stack. If it pushes today, add `newInstanceUD(L, v) *lua.LUserData` and use that.
  - Every existing caller passes `ResumeValue{}`.
  - `app`'s `scriptResumeMsg` gains `value script.ResumeValue`, and `handleScriptResume` passes it through.
- [ ] **Step 3: The instance userdata** (`script/userdata_instance.go`).
  - Its `Value` is a `core.InstanceView` (a copy), and `checkInstance` returns one.
  - **Reads come from the view:** `title`, `status` (`v.Status.String()`), `branch`, `path` (`v.RepoPath`), `program`, `started`, `paused`, `diff_stats` (nil unless `v.HasDiff`), and `__tostring`.
  - **`worktree`** returns nil when `!v.Started` or `v.WorktreePath == ""`. Otherwise it builds the handle from the view: `git.NewGitWorktreeFromStorage(v.RepoPath, v.WorktreePath, v.TmuxSession, v.Branch, "", false, "")`. First check that every method the worktree userdata calls works on such a handle: `GetBranchName`, `GetWorktreePath`, `GetRepoPath`, `IsDirty`, `IsBranchCheckedOut`, `CommitChanges` and `PushChanges`. Also check that `GetRepoPath` there equals `inst.Path`. Report any field the view needs and add it.
  - **`preview`, `send_keys` and `tap_enter`** act by session name on the script goroutine, with the guards `AgentPane` had, read from the view:
```go
// paneOf is the agent's tmux session, by name (no client, no instance).
func paneOf(v core.InstanceView) *tmux.Session { return tmux.NewSession(v.TmuxSession, v.SessionProgram) }

func instancePreview(L *lua.LState) int {
	v := checkInstance(L, 1)
	if !v.Started || v.Paused() || v.TmuxSession == "" {
		L.Push(lua.LString(""))
		return 1
	}
	s := paneOf(v)
	if !s.DoesSessionExist() {
		L.Push(lua.LString(""))
		return 1
	}
	out, err := s.CapturePaneContent()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LString(out))
	return 1
}
```
`send_keys` refuses (raises `send_keys: cannot send keys to instance that has not been started or is paused`) on the same guard, and otherwise calls `paneOf(v).TypeText(keys)`, raising `send_keys: <err>`. `tap_enter` does nothing on the guard, and otherwise calls `paneOf(v).PressKeys("Enter")`, logging a failure as `AgentPane.TapEnter` did. The view is the dispatch-time snapshot; a session that died since makes tmux fail, and the method raises or returns its error as before.
  - **`send_terminal_keys`** calls `e.curHost.SendTerminalKeys(v, text)`.
  - **The lifecycle methods** yield:
```go
// lifecycleOp is the yielding half of inst:kill(), :pause(), :resume() and
// :send_prompt(): it enqueues the operation for the host, which runs it
// through the model and resumes this coroutine with its outcome. Without a
// host (no dispatch) it raises.
func lifecycleOp(e *Engine, op string) lua.LGFunction {
	return func(L *lua.LState) int {
		v := checkInstance(L, 1)
		text := ""
		if op == "send_prompt" {
			text = L.CheckString(2)
		}
		if e.curHost == nil {
			L.RaiseError("%s: no host context", op)
			return 0
		}
		return e.enqueueAndYield(L, InstanceOpIntent{ID: v.ID, Title: v.Title, Op: op, Text: text})
	}
}
```
Use the helper that `cs.actions.*` uses to enqueue and yield (`script/api_actions.go`), under whatever name it has, adapting the call. A thin Lua wrapper, installed once when the type is registered, raises the error message the coroutine is resumed with. The methods therefore keep "void, raise on error":
```lua
-- loom: these calls yield to the TUI, which resumes them with nil (done) or
-- an error message, raised here so the methods keep raising on error.
local methods, names = ...
for _, name in ipairs(names) do
  local yielding = methods[name]
  methods[name] = function(...)
    local r = yielding(...)
    if type(r) == "string" then error(r, 2) end
    return r
  end
end
```
Load it with `L.LoadString` and call it with `L.CallByParam`, passing the methods table and `{"kill", "pause", "resume", "send_prompt"}`.
  - Delete `noticeAware`. A notice is now the model's (a `Notice` event the TUI shows), and the Reply only resumes the coroutine.

- [ ] **Step 4: `ctx:new_instance`** (`script/userdata_ctx.go`).
  1. It keeps its argument checks (title required, as an ArgError) and its defaults: program from `host.DefaultProgram()`, path from `host.RepoPath()`.
  2. It then enqueues a `CreateInstanceIntent` and yields.
  3. The same wrapper (Step 3), applied to the ctx methods table for `{"new_instance"}`, raises an error message and otherwise returns the instance userdata.
  4. Without a host it raises `new_instance: no host context`.
  5. The prefix `new_instance: ` is added by the host (Step 5) to keep today's message format.
  - `ctx:selected()` and `ctx:instances()` build userdata from the snapshot views.
  - `ctx:find(title)` searches them.
- [ ] **Step 5: The host side** (`app/app_scripts.go`, `app/requests.go`).
  - **`newScriptHost`** snapshots `selected` and `instances` as views, without the draft row (ID 0). Delete its `pending` and `resumed`, `QueueInstance`, `InstanceResumed`, and `scriptDoneMsg`'s `pendingInstances` and `resumedInstances`, together with `handleScriptDone`'s handling of both. Model operations now replace the pane client themselves (`SessionLaunched`), and new instances arrive through `Create`.
  - **`SendTerminalKeys(v, text)`** calls `s.splitPane.SendTerminalKeysToInstance(v.Title, text)`.
  - **`handleScriptDone`** passes its `msg.slot` to `handleScriptIntent(p, msg.slot)`.
  - **`handleScriptIntent`** handles the two new intents without resuming at once:
```go
	case script.InstanceOpIntent:
		req := m.newReq(pendingReq{script: &pendingScript{intent: p.id, trace: p.trace, op: i.Op}})
		switch i.Op {
		case "kill":
			m.core.Kill(i.ID, req)
		case "pause":
			m.core.Pause(i.ID, req)
		case "resume":
			m.core.Resume(i.ID, req)
		case "send_prompt":
			m.core.SendPrompt(i.ID, i.Text, req)
		default:
			delete(m.pending, req)
			return m.resumeScript(p, script.ResumeValue{Err: fmt.Sprintf("%s: unknown operation", i.Op)})
		}
		return nil // resumed by the Reply (scriptReplied)
	case script.CreateInstanceIntent:
		if slot != m.workspaceSlot {
			return m.resumeScript(p, script.ResumeValue{Err: fmt.Sprintf("new_instance: workspace changed while a script ran; not creating %s here", i.Title)})
		}
		req := m.newReq(pendingReq{script: &pendingScript{intent: p.id, trace: p.trace, op: "new_instance"}})
		m.core.Create(slot.ws, core.NewInstance{Title: i.Title, Path: i.Path, Program: i.Program, Prompt: i.Prompt, Branch: i.Branch}, req)
		return nil
```
  `pendingReq` gains `script *pendingScript` (and, in D2, `issue *pendingIssue`), with
```go
// pendingScript is a Lua call waiting on the model: its coroutine resumes
// when the Reply lands.
type pendingScript struct {
	intent script.IntentID
	trace  string
	op     string
}

// resumeScript resumes p's coroutine with v.
func (m *home) resumeScript(p pendingIntent, v script.ResumeValue) tea.Cmd {
	return func() tea.Msg { return scriptResumeMsg{id: p.id, trace: p.trace, value: v} }
}

// scriptReplied resumes the Lua call a Reply answers: with "<op>: <err>" on
// failure (the method raises it, as it raised before the model ran Lua's
// calls), with the new instance for new_instance, and with nothing
// otherwise.
func (m *home) scriptReplied(p *pendingScript, r core.Reply) tea.Cmd {
	var v script.ResumeValue
	switch {
	case r.Err != nil:
		v.Err = fmt.Sprintf("%s: %s", p.op, r.Err)
	case p.op == "new_instance":
		if row, _ := m.viewByID(r.ID); row != nil {
			v.Instance = row
		} else if cv, ok := m.core.View(r.ID); ok {
			v.Instance = &cv
		}
	}
	return func() tea.Msg { return scriptResumeMsg{id: p.intent, trace: p.trace, value: v} }
}
```
  `handleReply` gains `case p.script != nil: return m.scriptReplied(p.script, r)`.
  - **Behaviour that changes.** Note both in the commit message and in `docs/specs/scripting.md` (E):
    - A Lua kill now removes the session from the list and its record from storage. A Lua pause or resume now shows the spinner and saves. Before, these called `session` directly from the script goroutine and skipped all of that.
    - `ctx:new_instance` now yields until the instance exists and returns it. Before, it was added after the handler returned.
- [ ] **Step 6: Remove the bridges.**
  - Delete `core.Model.InstanceOf`, `IDFor` and `AdoptForScript`, plus their entries in `core.Core`.
  - `TestTUIHoldsNoInstance` drops its `app/app_scripts.go` exemption, adds `script/` to the directories it walks, and still flags `InstanceOf`, `IDFor` and `AdoptForScript` selectors in case one comes back.
- [ ] **Step 7: Tests.**
  - `script/host_fake_test.go`'s `fakeHost` works over views. Its `QueueInstance`/`InstanceResumed` records go away.
  - Script tests that called lifecycle methods on a fake instance now assert the enqueued `InstanceOpIntent`, then resume the coroutine (`ResumeWithHost` with a `ResumeValue`). They check that `Err` raises `"<op>: …"` from the Lua call, and that the zero value returns normally.
  - `TestNoticeAware` becomes a test that a Reply carrying only a `Notice` resumes the coroutine with nothing. The model shows the notice; the TUI adds none.
  - `TestResume_ReportsOnlyASuccessfulResume` becomes a test that a refused resume raises `"resume: …"`.
  - `TestCtxNewInstancePassesPromptThroughConstructor` asserts the `CreateInstanceIntent` fields and the defaults.
  - `app/panes_test.go`'s `TestScriptResume_ReplacesThePaneClient` drives `ctx:selected():resume()` through dispatch, the intent, `core.Resume`, the job, the Reply and the resume (`pumpCore` and the script message loop). It asserts the same pane replacement, now through `SessionLaunched`, and that the coroutine completed without error.
  - Add `app` tests for the two fixed behaviours:
    - `ctx:selected():kill()` removes the row and the stored record once the job lands;
    - `ctx:selected():pause()` leaves the record saved as Paused.
  - Keep every assertion. Where an assertion pinned the old direct path (e.g. `done.resumedInstances == [inst]`), replace it with the assertion on its replacement and report the mapping.

### D2. Issue fetches through the model

**Files:** Modify `app/state_issue_picker.go`, `app/state_prompt.go`, `app/requests.go`.

- [ ] **Step 1:**
```go
// pendingIssue is an issue fetch waiting for its Reply: the picker's pick
// (picked), or a "#n" prompt's expansion (expand, filled in as
// issueExpandedMsg minus the issue and error).
type pendingIssue struct {
	picked *issuePickedMsg
	expand *issueExpandedMsg
}
```
  - **The picker's pick** (`handleStateIssuePickerKey`) replaces its `github.View` Cmd with `m.core.FetchIssue(repo, n, m.newReq(pendingReq{issue: &pendingIssue{picked: &issuePickedMsg{repo: repo}}}))`.
  - **The `#n` expansion** replaces `issueExpandCmd` with `m.core.FetchIssue(repo, n, m.newReq(pendingReq{issue: &pendingIssue{expand: &issueExpandedMsg{draft: d, repo: repo, number: n, rest: rest, literal: literal, selectedBranch: selectedBranch}}}))`.
  - Delete `issueExpandCmd`.
  - `handleReply` gains:
```go
	case p.issue != nil && p.issue.picked != nil:
		msg := *p.issue.picked
		msg.issue, msg.err = r.Issue, r.Err
		_, cmd := m.handleIssuePicked(msg)
		return cmd
	case p.issue != nil && p.issue.expand != nil:
		msg := *p.issue.expand
		msg.issue, msg.err = r.Issue, r.Err
		_, cmd := m.handleIssueExpanded(msg)
		return cmd
```
  The handlers and their guards are unchanged. Tests that `Update` with `issuePickedMsg` or `issueExpandedMsg` directly keep working; add one test each that drives the fetch through `FetchIssue`'s Reply, using `core`'s `fetchIssueJob` with a mock executor through a seam, if `app` needs one. Do not add a production seam for it: if the round trip can't be driven from `app` without one, leave that case to core's `TestFetchIssue_RepliesWithTheIssue` and say so.

### D3. Verify and commit

- [ ] **Step 1:** Run `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`, then `CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./script/...` and `CGO_ENABLED=0 go test -tags e2e ./e2e/...`. Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - `TestTUIHoldsNoInstance`, `TestCoreImportsNoUI` and `TestNoProductionCallsOfTestSeams` pass.
  - `git grep -n 'InstanceOf\|IDFor\|AdoptForScript\|QueueInstance\|InstanceResumed' -- '*.go'` prints nothing.
  - `git grep -n 'github.View' -- 'app/*.go'` prints nothing.
  - The assertion count holds.
- [ ] **Step 3: Commit.**
```bash
git add script/ app/ core/ internal/
git commit -m "refactor(script,app): Lua and issue fetches go through the model" -m "Lua sees instances as views. inst:kill/pause/resume/send_prompt and
ctx:new_instance yield until the model's Reply and raise its error, as before;
a Lua kill now removes the session from the list and storage, and a Lua pause
or resume shows the spinner and saves. preview/send_keys/tap_enter act on tmux
by session name. The issue picker's fetches are core.FetchIssue. The TUI holds
no session instance anywhere (TestTUIHoldsNoInstance covers script). Daemon
stage 1C, package D." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

**Review focus for Package D:**
- **The coroutine lifecycle.** Every pending script request resumes exactly once, including on a refusal (an immediate Reply), a script whose context is cancelled, and engine shutdown with a call in flight. Look at `Engine.Shutdown` and `TestDroppedCoroutineCancelsItsContext`: a Reply for a dropped coroutine must be harmless.
- **The Lua wrapper.** Errors raise at the caller's level with the old messages, and a successful call returns nothing (or the instance).
- **The by-name pane ops** keep `AgentPane`'s guards and messages.
- **`ctx:new_instance`'s workspace rule** (focus changed → refused) against `handleScriptDone`'s old `adopt` rule.
- **The issue fetches' guards** run in the same order as before.

---

## Package E: documentation and verification

### E1. CLAUDE.md, the scripting reference and the spec

- [ ] **Step 1: CLAUDE.md.** Find every stale reference with:
  ```
  git grep -n -w -e pendingNew -e dropPendingNew -e applyChosenLaunch -e closeTerminalFor -e InstanceForSession -e ActiveInstances -e DropUnstarted -e AdoptClaudeStatus -e SetBellPending -e QueueInstance -e InstanceResumed -e noticeAware -e issueExpandCmd -e ensureSession -e GetSelectedInstance -e 'session.Instance' -- CLAUDE.md docs/specs/scripting.md
  ```
  Fix each hit so it names where the code lives now. Then:
  - **Architecture.**
    - The `core/` bullet gains `InstanceID`/`InstanceView`, `Sync`/`ViewsChanged`, requests by ID with `Reply`/`ReqID`, `Create`/`ResumeWith`/`FetchIssue`, and the `core.Core` interface (its workspace and account half is still pointer-typed until 1D).
    - The `app/` bullet gains each slot's view store (`slotRows`), drafts, the bell and ladder overlays, the request book (`newReq`/`handleReply`), the send hold, and tmux by name.
    - The `ui/` bullet says ui renders only `core.InstanceView`.
    - The `script/` bullet says Lua sees views, its lifecycle calls go through the model as intents resumed by Replies, and pane ops act by session name.
  - **Gotchas.**
    - Every gotcha that tells the TUI to act on an instance now says how it acts by ID: the focused slot, destructive actions by identity (now by `InstanceID`), the pane clients (the prune also releases terminal clients; kill and pause have no TUI hook), event-driven panes (the ladder is a display overlay; the saved status of a non-Claude session is its lifecycle status), the roster and Claude status (`StatusReported`), the Lua LState, and no model mutation from Cmds (the TUI holds no instance: `TestTUIHoldsNoInstance`).
    - **Add a gotcha: the TUI's copies go stale.** The split pane, the menu and any other holder of a view keep a copy, refreshed by `refreshSelection` on every view or overlay change. Code that keeps a view beyond one Update must re-read it, or be a deliberate snapshot (the merge picker).
  - **Testing Patterns.** Add `syncViews`, `IDForTest`, `testModel`, `AddForTest`/`InstancesForTest` and `aliveProbe`.
- [ ] **Step 2: `docs/specs/scripting.md`.** Update:
  - the Host interface (views; no `QueueInstance`/`InstanceResumed`);
  - the `ctx` table (`new_instance` yields and returns the created, unstarted instance; it is refused when the workspace changed while the script ran);
  - the instance table: reads come from the dispatch-time view; `kill`/`pause`/`resume`/`send_prompt` go through the model, raise `"<op>: <err>"`, and a notice is shown by the model; `kill` removes the session from the list and storage; `pause`/`resume` show the spinner and save; `preview`/`send_keys`/`tap_enter` act on tmux by session name;
  - the intents list (`InstanceOpIntent`, `CreateInstanceIntent`) and resume values (`ResumeValue`).

  Fix the gaps the investigation found in the same pass: the Host listing omits methods, and the `cs.actions` table omits `restart_with_options_selected`, `open_review`, `open_settings`, `toggle_file_explorer`, `merge_selected` and `new_from_issue`.
- [ ] **Step 3: The spec.**
  - In §2, a reply names the instance by its `InstanceID` (model-assigned and never reused within a daemon's life; a reconnecting client re-lists), with title and workspace in the view for display. Replace "title plus workspace path".
  - The `InstanceView` paragraph lists the fields as built.
  - In Rollout stage 1, the 1C entry links this plan, with a one-line summary.
  - The 1D entry reads: the workspace half of the boundary (workspace, config, state, registry, account and GitHub views and requests; `core.Core` fully value-typed), then the model's own goroutine.
- [ ] **Step 4: Commit.**
```bash
git add CLAUDE.md docs/
git commit -m "docs: CLAUDE.md, scripting and spec for the instance boundary (daemon stage 1C)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

### E2. Full verification

- [ ] **Step 1: The suite.**
```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
CGO_ENABLED=0 go test -tags e2e ./e2e/...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
```
Expected: all pass; gofmt prints nothing.

- [ ] **Step 2: Sandbox smoke run.** Use the `loom-dev` skill. The smoke agent also builds the base commit (9030942) into a second sandbox (`loomdev -s base1b`), and checks every oddity it sees against that baseline before calling it a regression.

For an account check, set `CLAUDE_CONFIG_DIR` to a throwaway directory: the accounts refresh syncs against the main config dir, which otherwise defaults to `~/.claude`.

Run the 1B smoke's ten checks (restore with two tabs, N with a prompt, kill/pause/resume/R, recover/discard, tabs and global mode, quit and restart, agent exit and the workspace terminal's restart, sends, the account strip, the snapshot path). Then:
11. **Drafts.**
    - `n` then Esc leaves no row and nothing in `state.json`.
    - `n`, a title, Enter, then cancelling Launch Options leaves nothing.
    - `N` with a prompt starts the session, with the created row selected and inline attach entered after the prompt was sent.
    - During a draft, switching workspace with `}` does not show the draft in the other tab.
12. **The ladder overlay.**
    - A fake (non-Claude) agent shows Running while `work 5` runs and Ready after it, on both the event path and the snapshot path.
    - After a quit and restart, its saved status is its lifecycle status. Note what the rail shows on restart compared with the 1B baseline.
13. **The send hold.** `a` to send `work 3`, then `i` at once: an info line says the prompt is still being sent. A moment later `i` works.
14. **Lua.** Put this script in the sandbox's scripts dir (find it from loomdev's environment; never `~/.loom`):
```lua
cs.bind("ctrl+k", "smoke kill", function(ctx) ctx:selected():kill() end)
cs.bind("ctrl+p", "smoke pause", function(ctx) ctx:selected():pause() end)
cs.bind("ctrl+r", "smoke resume", function(ctx) ctx:selected():resume() end)
cs.bind("ctrl+n", "smoke new", function(ctx)
  local inst = ctx:new_instance{title = "luamade"}
  ctx:log("info", "made " .. inst:title() .. " " .. inst:status())
end)
cs.bind("ctrl+s", "smoke send", function(ctx) ctx:selected():send_prompt("work 2") end)
cs.bind("ctrl+v", "smoke preview", function(ctx) ctx:log("info", "preview " .. #ctx:selected():preview()) end)
```
Use the real `cs.bind` signature from `docs/specs/scripting.md`, and choose keys that the default keymap and tmux leave free.

Check:
- **ctrl+p** pauses the selected session: spinner, then Paused, saved (stop and start: still Paused).
- **ctrl+r** resumes it.
- **ctrl+k** kills it: the row and its record are gone. This is the 1C fix; on the baseline sandbox it stays.
- **ctrl+n** logs `made luamade ready` and adds an unstarted row.
- **ctrl+s** sends `work 2` to the agent.
- **ctrl+v** logs a non-zero preview length.
- **An error raises:** the kill on a workspace terminal must be refused by the model and raise into the error bar.
15. **Issue picker.** Only if `gh` is authenticated in the sandbox; otherwise mark it SKIPPED. Pick an issue and confirm the draft carries its title and seed prompt.

- [ ] **Step 3: Report** to the user: the test totals, e2e, each smoke check's outcome, and every deviation from this plan with its reason.

### E3. Outcome (coordinator, after the final review)

- [ ] Append an "Outcome and follow-ups" section to this plan (the 1B format), and update the `loom-scrum-daemon-direction` memory: 1C is done, and the next step is plan 1D.
