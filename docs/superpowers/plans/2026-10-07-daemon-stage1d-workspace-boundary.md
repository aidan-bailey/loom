# Daemon Stage 1D: the Workspace Boundary — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The TUI reaches no model-owned object. Workspaces are named by a model-assigned `WorkspaceID` and seen as `WorkspaceView` values. Config, UI prefs, registry and account state cross the boundary as copies, and every change to them is a request. Every `core.Core` signature is plain data, apart from the job plumbing that stage 1E removes.

**Architecture:** This follows stage 1C's pattern for the workspace half.
- **Views:** the model publishes `WorkspaceView`s at `Sync`, diffed like instance views, in a `WorkspacesChanged` event that comes ahead of `ViewsChanged`. Each TUI slot caches its view.
- **Settings:** `config.Config` splits into a plain `config.Settings` struct (the persisted fields and their lock-free getters) embedded beside its mutex. Views carry `Settings` copies, and the settings overlay edits a TUI-owned `Config` that a `SaveSettings` request hands back.
- **Model still synchronous:** the model stays on the Update goroutine. Stage 1E moves it onto its own loop, where it runs its own jobs.

**Tech Stack:** Go 1.25, Bubble Tea v2, testify. Reflection is used for the value-typed boundary test.

---

## Where this stage fits

The daemon spec is `docs/superpowers/specs/2026-10-03-loom-daemon-design.md`, Rollout stage 1. What has landed so far:
- **1A** split the panes.
- **1B** extracted `core.Model`.
- **1C** drew the instance boundary: `InstanceView`, `InstanceID`, requests and `Reply`, drafts, the display-only ladder, Lua through the model, and `core.Core`.

Still left:
- **1D (this plan)** makes the rest of `core.Core` value-typed.
- **1E** puts the model on its own goroutine. The model then runs its own jobs, pushes a coalesced wake to the TUI, and the TUI drains it. Stage 2 can then be pure codec and transport.

The user decided both of these on 2026-10-07: the split into 1D and 1E, and that the model runs its own jobs.

## Decisions

1. **Scope.** 1D covers the workspace half of the boundary only. The model is still called synchronously on the Update goroutine, so the read-after-write sites (`syncViews`, the drains after a transition) keep working unchanged. 1E keeps them working over a synchronous call loop. `Sync`, `Deliver` and `Out.Jobs` stay as they are; 1E removes `Deliver` and `Jobs` from the boundary.
2. **Workspace identity is a model-assigned `WorkspaceID`** (uint64, never reused within a model's life; 0 means none), like `InstanceID`.
   - The classic and global workspaces get one too.
   - A reopened workspace is a new workspace object, so it gets a new ID. That is what lets the TUI tell a completion for the closed tab from the reopened one; 1B's `ClosedNote` does the same by object identity.
   - Name and label are display only.
3. **`WorkspaceView` is a value the model publishes:**
   - identity and display: ID, name, label, repo path and config dir;
   - copies of persisted state: config (`Settings`), UI prefs and help screens seen;
   - storage flags: writes refused, preserved titles;
   - the last recovery summary.

   `Sync` diffs every loaded workspace's view (`reflect.DeepEqual`) and emits `WorkspacesChanged`, with the loaded views in order, **before** any `ViewsChanged`. Each TUI slot caches its view (`slot.info`) and refreshes it from that event. A slot that needs its own write back within the same Update rereads it with `syncWorkspaces()`, the counterpart of `syncViews`.
4. **`config.Settings`.** The persisted fields of `config.Config` move into an exported struct, `Settings`, which `Config` embeds beside its mutex.
   - JSON flattens embedded structs, so `config.json` keeps its format. A round-trip test pins it.
   - The unlocked getters move to `Settings` (value receivers), which promotes them to `Config` as before.
   - The locked getters stay on `Config` as wrappers.
   - New helpers: `Settings.Clone()` (a deep copy: `Profiles` and every pointer field), `Config.Snapshot()` (a locked clone), `Config.ReplaceSettings(s)` (locked) and `config.FromSettings(s) *Config`.
   - `launch.FromSettings(s)` replaces `FromConfig`'s body, and `FromConfig(cfg)` becomes `FromSettings(cfg.Snapshot())`.
5. **The settings overlay edits a TUI-owned `*config.Config`,** built with `config.FromSettings(view.Settings)`.
   - On save, the TUI sends `SaveSettings(ws, cfg.Snapshot()) error`.
   - The model runs the save's side effects, which used to live in `app/state_settings.go`:
     - applies the settings under the workspace config's lock;
     - writes `config.json` to the workspace's config dir, or the default dir for a bare context;
     - sets the agent program;
     - sets the two process globals (`session.SetLoomContextEnabled`, `SetSubagentTrackingEnabled`).
   - The theme stays a TUI-only global (`ui.ApplyTheme`).
6. **UI prefs and help screens are requests:** `SetUIPrefs(ws, prefs) error` and `SetHelpScreensSeen(ws, bits) error`.
   - `mutateUIPrefs` reads the slot's cached prefs (a deep copy), applies its change, sends it, and on success writes the result into `slot.info.UIPrefs`. The next read in the same Update then sees it. The model's own publish at the next `Sync` agrees, since it reads the same state.
7. **Storage is not visible to the TUI.** `WritesRefused` and `PreservedTitles` live in the view.
8. **The registry crosses as a `RegistryView`:** a copy of the registered workspaces plus the open list.
   - `Registry() RegistryView` replaces the pointer accessor.
   - `ReloadRegistry() error` reloads the model's registry from disk. The mid-session workspace picker calls it, then `Registry()`. That replaces its own `config.LoadWorkspaceRegistry()` call, the one registry read that bypasses the model.
   - After the model is constructed, `newHome` reads the open list and the workspaces through `Registry()`, not through the pointer it handed over.
9. **Accounts:** `Accounts() *account.Registry` becomes `AccountNames() AccountNames{Present bool; Default string; Names []string}`.
   - The per-account queries (`Account`, `AccountUsage`, `AccountSync`, `AccountLoggedOut`, `RCAuthFor`, `AccountEnv`, `ClaudeProgram`) already return values. They now return copies: the `Usage` windows and the `SyncReport` slices are cloned. A test per query pins that changing a returned value does not change the next one.
10. **GitHub:** `GitHubSnapshot` returns a deep copy (`Snapshot.Clone()`: maps, plus each issue's labels and linked slices).
11. **Events stop carrying workspaces.**
    - `ViewsChanged.Workspace` becomes `ViewsChanged.WS WorkspaceID`.
    - `Started` and `Recovered` lose `Owner *Workspace` and gain `Owner WorkspaceID`, `OwnerLabel string` and `ClosedNote string`, filled at emit time from what `ClosedNote(ws)` computes today.
    - `Loaded` stays.
12. **Transitions and queries by ID:**

| Today | 1D |
|---|---|
| `Classic() *Workspace` | `Classic() (WorkspaceView, bool)` |
| `Tabs() []*Workspace` | `Tabs() []WorkspaceView` (a copy) |
| `IsLoaded(ws)` | `IsLoaded(id)` |
| `ClosedNote(ws)` | removed (now in the events) |
| `OpenTab(def) (*Workspace, error)` | `OpenTab(def) (WorkspaceView, error)` |
| `CloseTab(name) (*Workspace, error)` | `CloseTab(name) error` |
| `EnterGlobal(focused *Workspace) (*Workspace, error)` | `EnterGlobal(focused WorkspaceID) (WorkspaceView, error)` |
| `Save(ws)` | `Save(id)` |
| `Views(ws)` | `Views(id)` |
| `Create(ws, …)` | `Create(id, …)` |
| `RestoreFailed() []string` | a copy |

    New: `Workspace(id) (WorkspaceView, bool)`.
13. **Enforcement:**
    - `TestCoreIsValueTyped` (core) walks `core.Core` by reflection, plus every concrete `Event` type. Every parameter, result and event field must be plain data:
      - **allowed:** basic kinds; structs, slices, arrays and maps of plain data; pointers to plain data (serializable, and copied by contract, decision 9); `time.Time`; and the interfaces `error` and `core.Event`;
      - **forbidden:** func, chan and unsafe pointers; every other interface; `sync` types; and a deny list of model-owned named types (`core.Workspace`, `core.Model`, `session.Instance`, `session.Storage`, `config.Config`, `config.WorkspaceRegistry`, `config.State`, `account.Registry`).
      - `Sync` and `Deliver` are exempt by name until 1E, with a comment that says so.
    - `TestTUIHoldsNoModelObject` (internal/testenv) extends `TestTUIHoldsNoInstance`:
      - **flags,** in `app/`, `ui/` and `script/` production files: `core.Workspace`, `core.WorkspaceParts`, `core.NewWorkspace`, `session.Storage`, `config.WorkspaceRegistry`, `config.LoadWorkspaceRegistry`, `account.Registry`, `account.LoadRegistry` and `config.AppState`;
      - **exempt:** `app/app_init.go`'s handover of the startup objects to `core.New`, by function name (`Run`, `newHome`), listed in the test with the reason.
14. **What 1D leaves for 1E, recorded here so 1E's plan starts from it:**
    - `Sync() Out` with `Jobs`, `Deliver(any)`, the TUI's `coreCmd` and `coreResultMsg`.
    - The read-after-write sites: `syncViews`, `syncWorkspaces`, and the drains after `OpenTab`/`EnterGlobal`/`RestoreSaved`/`LoadClassic`/`ReloadAccounts`.
    - The nested drain in `newLaunchOptionsOverlay`.
    - The merge dirty check (git I/O on Update).

    Under 1E's synchronous call loop these keep their meaning. Stage 2's transport must revisit them.

## Out of scope

- The model's own goroutine, its jobs and the wake path (1E).
- Loading every registered workspace, and open tabs as a per-client preference (stage 3).
- The 1C follow-ups (send-hold gaps, the shell leak, `cs.actions` under pcall). They are tracked in the 1C plan's outcome.

## What moves where

| Today (TUI reaches into the model) | 1D |
|---|---|
| `slot.ws *core.Workspace`, and its accessors `wsCtx()`, `storage()`, `appConfig()`, `appState()` | `slot.id core.WorkspaceID` + `slot.info core.WorkspaceView`; accessors `name()`, `repoPath()`, `configDir()`, `settings()`, `uiPrefs()`, `writesRefused()`, `preservedTitles()`, `recovery()` |
| The settings overlay mutating the model's `*config.Config`, then `config.SaveConfigTo` + `SetProgram` + session globals in the TUI | The overlay edits `config.FromSettings(m.settings())`; `m.core.SaveSettings(id, cfg.Snapshot())` does the rest in the model |
| `appState().SetUIPrefs` / `SetHelpScreensSeen` | `m.core.SetUIPrefs(id, p)` / `SetHelpScreensSeen(id, b)` |
| `m.core.Registry()` / `registry.Workspaces` / `config.LoadWorkspaceRegistry()` | `m.core.Registry()` (a `RegistryView`), `m.core.ReloadRegistry()` |
| `m.core.Accounts().Default()/Names()` | `m.core.AccountNames()` |
| `ViewsChanged.Workspace`, `Started.Owner`, `Recovered.Owner`, `slotFor(*Workspace)` | IDs; `slotFor(id)` |
| `ws.Recovery()`, `ws.Name()`, `ws.Label()` | `slot.info.Recovery`, `.Name`, `.Label` |

## Packages

| Pkg | Delivers | Commit |
|---|---|---|
| **A** | Core, additive: `WorkspaceID`, `WorkspaceView`, publishing and `WorkspacesChanged`, `config.Settings`, the new requests and value queries, ID versions of the transitions (pointer versions renamed `…WS`), ID fields on events, copies from every query | `feat(core,config): workspace views, IDs and settings as values` |
| **B** | The TUI on values: slots keyed by ID with a cached view, every read through the view, every write a request, settings overlay over a TUI-owned config, registry, account and GitHub through value queries | `refactor(app,ui): the TUI holds workspace views, not workspaces` |
| **C** | The pointer API leaves `core.Core` and the events; `TestCoreIsValueTyped`; `TestTUIHoldsNoModelObject` | `refactor(core,app): core.Core is value-typed` |
| **D** | CLAUDE.md, the spec, full verification and a sandbox smoke run | `docs: …` |

## Conventions for every package

- **Build and test.** Work from the repo root. Build with `CGO_ENABLED=0`. For race runs, use `CC=clang CGO_ENABLED=1 go test -race …`. Format with `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`, never `gofmt -w .`. Run `go vet`, not golangci-lint.
- **Git.** Never use `git stash`. Stay on the branch. Never run `./loom` (use `go run ./tools/loomdev`). No test may reach the developer's tmux server or `~/.loom`. Never write the word `exec` immediately followed by `(` in any file (a security hook rejects it).
- **Moved and renamed code.** Keep each comment and update only the names it mentions.
- **Assertions.** Never weaken or delete one. Get the count with `git grep -h 'assert\.\|require\.' -- '*_test.go' ':!vendor' | wc -l`. A conversion that keeps an assertion's meaning is fine; list any whose meaning changed.
- **The plan is a first draft.** Where the code differs from what it assumes, adapt minimally and record the deviation. If a difference needs a design decision, stop and ask.
- **Tests run in the foreground** with a generous timeout. A background run can fail to wake you.
- **Commit trailers.** Commits end with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7
  ```
- **Copies.** Every query result and every event field the TUI receives is a copy. No slice, map or pointer in it may alias model memory. Each new query gets an aliasing test: change the returned value, query again, and assert that nothing changed.

## File structure

| File | Package | Responsibility |
|---|---|---|
| `config/settings.go` (new) | A | `Settings`, its getters, `Clone`, `FromSettings`; `Config.Snapshot`, `ReplaceSettings` |
| `config/config.go` | A | `Config` embeds `Settings`; locked wrappers stay |
| `session/launch/launch.go` | A | `FromSettings`; `FromConfig` delegates |
| `session/github/types.go` | A | `Snapshot.Clone` |
| `account/usage.go`, `account/link.go` | A | `Usage.Clone`, `SyncReport.Clone` |
| `core/workspace_view.go` (new) | A | `WorkspaceID`, `WorkspaceView`, `RegistryView`, `AccountNames` |
| `core/workspace_views.go` (new) | A | `wsIDOf`, `wsLookup`, `wsViewOf`, `Workspace(id)`, `publishWorkspaces`, the `Sync` hook |
| `core/settings.go` (new) | A | `SaveSettings`, `SetUIPrefs`, `SetHelpScreensSeen` |
| `core/workspaces.go`, `core/accounts.go`, `core/github.go`, `core/views.go`, `core/events.go`, `core/completions.go`, `core/iface.go` | A, C | The ID versions, copies, event fields; C deletes the pointer versions |
| `core/boundary_test.go` | C | `TestCoreIsValueTyped` |
| `internal/testenv/instance_enforce_test.go` | C | `TestTUIHoldsNoModelObject` |
| `app/workspaces.go`, `app/core_glue.go`, `app/views.go`, `app/app_init.go`, `app/state_settings.go`, `app/intents.go`, `app/accounts.go`, `app/help.go`, `app/app.go`, `app/workbench.go`, `app/completions.go`, `app/drafts.go`, `app/overview*.go`, `app/state_*.go`, `app/app_scripts.go` | B | The slot over a view; reads and writes through `Core` |

---

## Package A: workspace views, IDs and settings as values (core and config, additive)

Everything new sits beside the pointer API, and app keeps working through the renamed pointer methods. One commit at the end.

### A1. Rename the pointer API

- [ ] **Step 1:** Rename with `gopls rename` (or carefully by hand), updating every caller in `app/` and every core test. Comments that name these methods change with them.

| Method | Renamed to |
|---|---|
| `Classic` | `ClassicWS` |
| `Tabs` | `TabsWS` |
| `IsLoaded` | `IsLoadedWS` |
| `ClosedNote` | `ClosedNoteWS` |
| `OpenTab` | `OpenTabWS` |
| `CloseTab` | `CloseTabWS` |
| `EnterGlobal` | `EnterGlobalWS` |
| `Save` | `SaveWS` |
| `Views` | `ViewsWS` |
| `Create` | `CreateWS` |
| `Registry` | `RegistryObj` |
| `Accounts` | `AccountsRegistry` |

`core/iface.go` lists the renamed methods, so `home.core` keeps compiling. Package C deletes them.

- [ ] **Step 2:** Run `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./core/ ./app/`. Expected: PASS. Then confirm with `git diff --word-diff` on `app/` that only identifiers changed.

### A2. `config.Settings`

**Files:**
- Create: `config/settings.go`, `config/settings_test.go`
- Modify: `config/config.go`, `session/launch/launch.go`, and every `config.Config{…}` composite literal (28 in tests, plus `DefaultConfig`)

- [ ] **Step 1: Write the failing round-trip test** (`config/settings_test.go`):
```go
// TestSettings_ConfigJSONIsUnchanged: Config embeds Settings, and JSON
// flattens an embedded struct, so config.json keeps its format. A file
// with every field set loads and saves back byte for byte.
func TestSettings_ConfigJSONIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	in := []byte(`{
  "default_program": "claude",
  "branch_prefix": "me/",
  "base_branch": "develop",
  "profiles": [
    {
      "name": "fast",
      "program": "claude --model haiku"
    }
  ],
  "claude_remote_control": false,
  "claude_loom_context": true,
  "claude_subagent_tracking": false,
  "claude_permission_mode": "plan",
  "theme": "legacy"
}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), in, 0o644))
	cfg := LoadConfigFrom(dir)
	require.NoError(t, SaveConfigTo(cfg, dir))
	out, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	require.NoError(t, err)
	assert.JSONEq(t, string(in), string(out))
}

// TestSettings_CloneSharesNothing: a clone's profiles and pointer fields
// are its own.
func TestSettings_CloneSharesNothing(t *testing.T) {
	on := true
	s := Settings{Profiles: []Profile{{Name: "a", Program: "x"}}, ClaudeRemoteControl: &on}
	c := s.Clone()
	c.Profiles[0].Program = "y"
	*c.ClaudeRemoteControl = false
	assert.Equal(t, "x", s.Profiles[0].Program)
	assert.True(t, *s.ClaudeRemoteControl)
}
```
The fixture must hold every field `Config` has at this HEAD; check the struct and add any that are missing (theme, model, effort, the 1M context, Headroom, the cache TTL, `claude_tmp_archive_dir`, …). A field left out is a field the test doesn't pin.

- [ ] **Step 2:** Run `CGO_ENABLED=0 go test ./config/ -run TestSettings`. Expected: FAIL (undefined: `Settings`).

- [ ] **Step 3: Split the struct.** In `config/config.go`, every persisted field moves, with its comment, into `Settings` in `config/settings.go`:
```go
// Settings is config.json's content: every persisted field, with the
// getters that read them. A plain value: the model hands copies of it
// across the boundary (core.WorkspaceView.Settings), and the TUI's
// settings overlay edits its own Config built from one (FromSettings).
// Config embeds it beside the mutex that guards the live, shared copy.
type Settings struct {
	// (the persisted fields, moved verbatim with their comments and tags)
}

// Clone deep-copies s: Profiles and every pointer field are its own.
func (s Settings) Clone() Settings {
	out := s
	out.Profiles = slices.Clone(s.Profiles)
	// one line per pointer field, e.g.:
	out.ClaudeRemoteControl = clonePtr(s.ClaudeRemoteControl)
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// FromSettings builds a Config holding a copy of s: a new object with its
// own lock, for a caller that edits settings without touching anyone
// else's (the TUI's settings overlay).
func FromSettings(s Settings) *Config { return &Config{Settings: s.Clone()} }
```
`Config` becomes:
```go
type Config struct {
	// mu guards Settings once the model shares the Config (see the comment
	// this field had); unexported, so encoding/json skips it.
	mu sync.RWMutex
	Settings
}

// Snapshot is a deep copy of the settings, taken under the lock.
func (c *Config) Snapshot() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Settings.Clone()
}

// ReplaceSettings replaces every setting with a copy of s, under the lock.
func (c *Config) ReplaceSettings(s Settings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Settings = s.Clone()
}
```
Keep the mutex field's existing comment.

The getters move as follows:
- **The unlocked getters move to `Settings` with value receivers.** These are `RemoteControlEnabled`, `LoomContextEnabled`, `SubagentTrackingEnabled`, `PermissionMode`, `HeadroomProxyEnabled`, `Model`, `Effort`, `CacheTTL1hEnabled`, `Context1MEnabled`, `GetProgram` and `GetProfiles`. `Config` gets them by promotion, so callers don't change.
- **The locked getters keep their `*Config` versions and gain a `Settings` version.** These are `GetBranchPrefix`, `GetBaseBranch`, `GetTheme` and `ClaudeTmpArchiveRoot`. Each `Config` version takes the read lock and returns `c.Settings.X()`. `Config`'s own method shadows the promoted one.
- **`Mutate` and `SaveConfigTo` stay on `Config`.** `SaveConfigTo` marshals under the read lock. It doesn't today; that is a race the 1E loop would expose.
- **Composite literals** become `config.Config{Settings: config.Settings{…}}`.

- [ ] **Step 4:** In `session/launch/launch.go`, add `FromSettings(s config.Settings) Options`, which holds today's body, reading `s.GetBranchPrefix()`. `FromConfig(cfg)` returns `Options{}` for nil, else `FromSettings(cfg.Snapshot())`.

- [ ] **Step 5:** Run `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./config/ ./session/launch/ ./core/ ./app/`. Expected: PASS. `go vet`'s copylocks check must stay quiet: no `Config` is copied by value.

### A3. Copies

**Files:** `session/github/types.go`, `account/usage.go`, `account/link.go`, `core/github.go`, `core/accounts.go`, `core/workspaces.go`, plus tests beside each.

- [ ] **Step 1: The clone methods,** each with a test that changes the clone and checks the original:
```go
// Clone deep-copies s: its maps and each issue's slices are its own.
func (s Snapshot) Clone() Snapshot

// Clone deep-copies u: the windows are its own.
func (u Usage) Clone() Usage

// Clone deep-copies r's slices.
func (r SyncReport) Clone() SyncReport
```
Check `github.Issue` and `github.PR` for every slice or map field (labels, linked PRs, …) and clone each one.

- [ ] **Step 2: Return copies.**
  - `GitHubSnapshot` returns `snap.Clone()`.
  - `AccountUsage` returns `u.Clone()`.
  - `AccountSync` returns `r.Clone()`.
  - `RestoreFailed` returns `slices.Clone(m.restoreFailed)`.
  - `TabsWS` returns `slices.Clone(m.tabs)`.

  Add an aliasing test per query in `core/copies_test.go`. Each one changes what it got back, queries again, and asserts the second result is unchanged.

### A4. `WorkspaceID`, `WorkspaceView` and their publishing

**Files:**
- Create: `core/workspace_view.go`, `core/workspace_views.go`, `core/workspace_views_test.go`
- Modify: `core/model.go`, `core/events.go`, `core/views.go` (`Sync`), `core/load.go` (`RecoverySummary`)

- [ ] **Step 1: The types** (`core/workspace_view.go`):
```go
package core

import "github.com/aidan-bailey/loom/config"

// WorkspaceID names a loaded workspace. The model assigns it the first
// time it reports the workspace and never reuses it, so a workspace closed
// and reopened is a new workspace with a new ID (its completions tell the
// two apart, as ClosedNote did by identity). 0 means none.
type WorkspaceID uint64

// WorkspaceView is a loaded workspace as its clients see it: a value the
// model publishes (WorkspacesChanged) and answers queries with. Every
// field is a copy.
type WorkspaceView struct {
	ID WorkspaceID
	// Name is the registered name, "" for the global context; Label names
	// it in notices (its name, or "global").
	Name, Label string
	// RepoPath and ConfigDir are the workspace context's ("" in a bare
	// context).
	RepoPath, ConfigDir string
	// Settings is a copy of the workspace's config.json.
	Settings config.Settings
	// UIPrefs and HelpScreensSeen are copies of its state.json.
	UIPrefs         config.UIPrefs
	HelpScreensSeen uint32
	// WritesRefused is set while the workspace's storage refuses writes
	// (its load failed: Storage.WritesRefused). PreservedTitles are the
	// titles of records its storage preserves but could not load.
	WritesRefused   bool
	PreservedTitles []string
	// Recovery is the summary of its last orphan reconcile.
	Recovery RecoverySummary
}

// RegistryView is a copy of the workspace registry: every registered
// workspace and the open list.
type RegistryView struct {
	Workspaces []config.Workspace
	Open       []string
}

// AccountNames are the registered accounts, default first: Present is
// false until the account registry is set up (InitAccounts).
type AccountNames struct {
	Present bool
	Default string
	Names   []string
}
```
`RecoverySummary`'s fields become exported (`Cleaned`, `Review`, `Failed`, `Undecodable`). Update every use in core, keeping the comments, so the value crosses the boundary intact.

- [ ] **Step 2: Assigning IDs and building views** (`core/workspace_views.go`). Mirror `core/views.go`. The `Model` gains `wsIDs map[*Workspace]WorkspaceID`, `nextWSID` and `publishedWS []WorkspaceView`, all initialised in `newModel` and lazily in `wsIDOf`.
```go
// wsIDOf returns ws's ID, assigning the next one the first time.
func (m *Model) wsIDOf(ws *Workspace) WorkspaceID

// wsLookup resolves id to its loaded workspace, or nil.
func (m *Model) wsLookup(id WorkspaceID) *Workspace

// wsViewOf copies ws's state into a view. Every handle locks itself
// (Config.Snapshot, the state's getters, the storage's), and every
// reference field is a fresh copy.
func (m *Model) wsViewOf(ws *Workspace) WorkspaceView {
	v := WorkspaceView{ID: m.wsIDOf(ws), Name: ws.Name(), Label: ws.Label(), Recovery: ws.recovery}
	if ws.ctx != nil {
		v.RepoPath, v.ConfigDir = ws.ctx.RepoPath, ws.ctx.ConfigDir
	}
	if ws.cfg != nil {
		v.Settings = ws.cfg.Snapshot()
	}
	if ws.state != nil {
		v.UIPrefs = ws.state.GetUIPrefs()
		v.HelpScreensSeen = ws.state.GetHelpScreensSeen()
	}
	if ws.storage != nil {
		v.WritesRefused = ws.storage.WritesRefused()
		v.PreservedTitles = ws.storage.PreservedTitles()
	}
	return v
}

// Workspace is the view of the loaded workspace id.
func (m *Model) Workspace(id WorkspaceID) (WorkspaceView, bool)

// publishWorkspaces returns a WorkspacesChanged with every loaded
// workspace's view, in Loaded order, when any of them differs from the
// last publish (or the loaded set changed), and forgets the IDs of
// workspaces no longer loaded.
func (m *Model) publishWorkspaces() []Event
```
`GetUIPrefs` already deep-copies. `PreservedTitles` builds a fresh slice. Check both, and clone if either turns out not to.

- [ ] **Step 3: The event and `Sync`.** Add to `core/events.go`:
```go
// WorkspacesChanged carries every loaded workspace's view, in Loaded
// order, whenever any of them (or the loaded set) changed since the last
// Sync. Sync puts it first, ahead of ViewsChanged, so the appliers of
// everything after it see the new workspace views.
type WorkspacesChanged struct {
	Views []WorkspaceView
}
```
`Sync` becomes `out.Events = append(append(m.publishWorkspaces(), views...), out.Events...)`. The event carries its own copy of the views, not the slice `publishedWS` keeps.

- [ ] **Step 4: Tests** (`core/workspace_views_test.go`):
  - IDs are stable across calls and never reused after a close and reopen.
  - `Workspace(id)` returns nothing for a closed workspace.
  - The first `Sync` publishes, and an unchanged second `Sync` publishes nothing.
  - A changed UI pref, setting or recovery summary republishes.
  - `WorkspacesChanged` comes before `ViewsChanged`.
  - The view's `Settings` and `UIPrefs` don't alias the model's: change them, then `Sync` again.

  Use `storedWorkspace` and `NewForTest` from `core/testhelpers_test.go`.

### A5. Requests and queries by ID

**Files:**
- Create: `core/settings.go`, `core/settings_test.go`
- Modify: `core/workspaces.go`, `core/persist.go`, `core/requests.go`, `core/views.go`, `core/accounts.go`, `core/events.go`, `core/completions.go`, `core/iface.go`, `core/seams.go`

- [ ] **Step 1: The transitions and queries by ID.** Each wraps its `…WS` version:
```go
func (m *Model) Classic() (WorkspaceView, bool)          // the classic workspace's view; false while tabs are open
func (m *Model) Tabs() []WorkspaceView                    // the open tabs' views, in order
func (m *Model) IsLoaded(id WorkspaceID) bool
func (m *Model) OpenTab(def config.Workspace) (WorkspaceView, error)
func (m *Model) CloseTab(name string) error
func (m *Model) EnterGlobal(focused WorkspaceID) (WorkspaceView, error)
func (m *Model) Save(id WorkspaceID) error                // an unknown id: nil, as SaveWS(nil) is today (check it)
func (m *Model) Views(id WorkspaceID) []InstanceView
func (m *Model) Create(id WorkspaceID, spec NewInstance, req ReqID)
func (m *Model) Registry() RegistryView                   // copies of the workspaces and the open list
func (m *Model) ReloadRegistry() error                    // reloads the model's registry from disk
func (m *Model) AccountNames() AccountNames
```
  - **An unknown `WorkspaceID`** gets the same answer the pointer version gives a nil or unloaded workspace: `Create` refuses with the message it already uses (`create <title>: its workspace is no longer open`), `Views` returns nil, `IsLoaded` returns false.
  - **`EnterGlobal(0)`** is valid: it means the caller had no focused tab. Pass nil to the `…WS` version.
  - **`ReloadRegistry`:** check `config.WorkspaceRegistry` for a reload method. Use it if there is one; otherwise load a fresh one and copy it in. Reloading must not drop or rewrite the open list.

- [ ] **Step 2: The settings requests** (`core/settings.go`):
```go
// SaveSettings replaces the workspace's settings with s and writes its
// config.json: to its context's config dir, or to the default config dir
// for a bare context (state_settings.go's rule until 1D). Then it applies
// what a settings change does at once: the agent program (SetProgram) and
// the two launch toggles the session package reads globally
// (SetLoomContextEnabled, SetSubagentTrackingEnabled). An unknown id is
// an error. Moved from app/state_settings.go.
func (m *Model) SaveSettings(id WorkspaceID, s config.Settings) error

// SetUIPrefs replaces and persists the workspace's UI prefs.
func (m *Model) SetUIPrefs(id WorkspaceID, p config.UIPrefs) error

// SetHelpScreensSeen replaces and persists the workspace's seen help
// screens.
func (m *Model) SetHelpScreensSeen(id WorkspaceID, seen uint32) error
```
Each one takes the model's copy of the input (`s.Clone()`, the prefs clone), so the caller's value is never aliased. Tests:
  - `SaveSettings` writes the file, changes the next view's `Settings`, sets `Program()`, and flips both session globals. Read them back with the getters `session` has, and restore them in `t.Cleanup`.
  - Each request with an unknown ID returns an error.
  - `SetUIPrefs` persists: reload `state.json` from disk.

- [ ] **Step 3: ID fields on events.** These sit beside the pointer fields, which Package C deletes:
  - `ViewsChanged.WS WorkspaceID`.
  - `Started` and `Recovered` keep `Owner *Workspace` for now, and gain `OwnerID WorkspaceID`, `OwnerLabel string` and `ClosedNote string`. The ID is `0` when the owner is nil. `ClosedNote` is `ClosedNoteWS(owner)` computed at emit time when `!Loaded`, otherwise "". Find every emit site and set them all. Package C renames `OwnerID` to `Owner` when the pointer goes.

- [ ] **Step 4: `core/iface.go`.** Add the new methods. The `…WS` ones stay until C.

- [ ] **Step 5: Tests** (`core/workspace_requests_test.go`):
  - `OpenTab`'s view equals `Workspace(id)`'s.
  - `CloseTab` by name, including the last-tab refusal.
  - `EnterGlobal` from a tab and with `0`.
  - `Save(unknown)` and `Views(unknown)`.
  - `Create` on an unknown workspace is refused.
  - `Registry()` is a copy, with an aliasing test.
  - `ReloadRegistry` sees a workspace another process registered: write the registry file directly, then reload.
  - `AccountNames` before and after `InitAccounts`.
  - The event ID fields.

### A6. Verify and commit

- [ ] **Step 1: Run the checks.**
  ```bash
  CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...
  CC=clang CGO_ENABLED=1 go test -race ./core/... ./config/...
  ```
  Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - The A1 app diff is rename-only.
  - `TestCoreImportsNoUI` and `TestNoProductionCallsOfTestSeams` pass.
  - The assertion count holds.
- [ ] **Step 3: Commit.**
```bash
git add config/ session/launch/ session/github/ account/ core/ app/
git commit -m "feat(core,config): workspace views, IDs and settings as values" -m "Workspaces get a model-assigned WorkspaceID and a WorkspaceView (context,
config.Settings copy, UI prefs, help screens, storage flags, recovery summary),
published as WorkspacesChanged ahead of ViewsChanged. config.Config embeds a plain
Settings beside its lock. SaveSettings, SetUIPrefs, SetHelpScreensSeen, Registry
and ReloadRegistry, AccountNames, ID versions of the transitions, and copies from
every query. The pointer API is renamed ...WS until package C. Daemon stage 1D,
package A." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

**Review focus for Package A:**
- **The `Settings` split.** The JSON format must be unchanged, with every field in the round-trip fixture. `go vet` copylocks must stay quiet. Each locked getter must still lock.
- **Copies.** Nothing a query or event returns may alias model memory.
- **Publishing.** `WorkspacesChanged` must come first, carry no churn (no view that changes on every call), and fire when the loaded set changes.
- **`SaveSettings`** must apply the same side effects `app/state_settings.go` applied, in the same order.
- **The ID versions** must behave like their pointer versions with nil, and `EnterGlobal(0)` must be valid.

---

## Package B: the TUI holds workspace views, not workspaces

The TUI moves wholesale onto the values from Package A. Every slot is keyed by `WorkspaceID` and caches its `WorkspaceView`. Every read goes through the view, and every write is a request. The `…WS` pointer methods stay in core until Package C, but `app/` stops calling them here. The package is one switch: the build is red from B1 until B4 ends. One commit at the end.

### B1. The slot over a view

**Files:** `app/workspaces.go`, `app/core_glue.go`, `app/views.go`.

- [ ] **Step 1: The slot.** `ws *core.Workspace` is replaced by an ID and a cached view:
```go
type workspaceSlot struct {
	// id names the workspace this slot shows; 0 only in bare test homes.
	id core.WorkspaceID
	// info is the workspace as the model last published it
	// (core.WorkspacesChanged), or as this slot last wrote it
	// (syncWorkspaces). Read it through the accessors below.
	info core.WorkspaceView
	views []core.InstanceView
	list *ui.List
	splitPane *ui.SplitPane
	workbench *ui.Workbench
}
```
The four handle accessors are replaced by value accessors, each safe on a bare slot (zero view):
```go
func (s *workspaceSlot) name() string                 { return s.info.Name }
func (s *workspaceSlot) label() string                { return s.info.Label }
func (s *workspaceSlot) repoPath() string             { return s.info.RepoPath }
func (s *workspaceSlot) configDir() string            { return s.info.ConfigDir }
func (s *workspaceSlot) settings() config.Settings    { return s.info.Settings }
func (s *workspaceSlot) uiPrefs() config.UIPrefs      { return s.info.UIPrefs }
func (s *workspaceSlot) recovery() core.RecoverySummary { return s.info.Recovery }
```
`home`'s existing `repoPath()` and `configDir()` (app.go:1826 and :1839) collapse into these. Keep their comments, and keep the global-dir fallback `configDir()` has today for a bare context.

- [ ] **Step 2: The store.** Add to `app/views.go`:
```go
// syncWorkspaces rereads every open slot's view from the model, for a
// caller that changed one (a settings save, a prefs write) and reads it
// back in the same Update (the workspace counterpart of syncViews).
func (m *home) syncWorkspaces() {
	for _, s := range m.openSlots() {
		if v, ok := m.core.Workspace(s.id); ok {
			s.info = v
		}
	}
}
```
The `WorkspacesChanged` applier in `applyCoreEvent` does the same from the event's views, matched by ID. A view with no slot is ignored.

- [ ] **Step 3: Slot lookup by ID.** `slotFor(ws *core.Workspace)` becomes `slotFor(id core.WorkspaceID)`. `newSlotView(ws *core.Workspace)` becomes `newSlotView(v core.WorkspaceView)`, which sets `id` and `info` and then the rest as today. `seedViews` and `syncViews` call `m.core.IsLoaded(s.id)` and `m.core.Views(s.id)`. `checkSlotInvariant` compares `m.slots[i].id` with `m.core.Tabs()[i].ID`, and the classic slot's `id` with `Classic()`'s.

### B2. Reads

Every read through a workspace handle becomes a read of the view, at every site the inventory found. The groups (line numbers are at ffdb9ab):

| Was | Becomes |
|---|---|
| `m.wsCtx().RepoPath`, `m.repoPath()` (14 callers) | `m.repoPath()` (now `slot.info.RepoPath`) |
| `m.wsCtx().ConfigDir`, `m.configDir()` | `m.configDir()` |
| `wsCtx().Name` (overview.go:184, 205-206, 225, 233-234; workspaces.go:475; app_init.go:136-137, 220) | `slot.name()` / `m.name()` |
| `ws.Name()`, `ws.Label()`, `ws.Recovery()` | `slot.name()`, `slot.label()`, `slot.recovery()`; for an event, its `OwnerLabel` |
| `m.appConfig().GetX()` (12 sites) | `m.settings().GetX()` |
| `m.appState().GetUIPrefs()` (app.go:465/469, 489/502, 570/574; workbench.go:64-65) | `m.uiPrefs()` |
| `m.appState().GetHelpScreensSeen()` (help.go:278, 280) | `m.info.HelpScreensSeen` |
| `m.storage().PreservedTitles()` / `WritesRefused()` (state_new.go:105, 117) | `m.info.PreservedTitles` / `m.info.WritesRefused` |
| `launch.FromConfig(m.appConfig())` (state_issue_picker.go:264) | `launch.FromSettings(m.settings())` |
| `m.core.Accounts().Default()/Names()`, nil checks (accounts.go:43-48, 151-161, 254) | `n := m.core.AccountNames()`: `n.Present`, `n.Default`, `n.Names` |

A nil check on a handle (`if m.appConfig() == nil`) becomes a check on the slot. If it guarded bare test homes, the zero view is the guard. Otherwise it is an `m.id == 0` check. Keep each check's meaning.

### B3. Writes and transitions

- [ ] **Step 1: UI prefs.** In `mutateUIPrefs`:
```go
// mutateUIPrefs applies fn to a copy of the focused workspace's prefs and
// asks the model to persist it; on success the slot keeps the result, so
// a read later in the same Update sees it. Save errors are logged, not
// surfaced (layout prefs are best-effort). Persistence is a synchronous
// write-through to state.json — fine for rare toggles; debounce burst
// callers (e.g. key-repeat ratio changes).
func (m *home) mutateUIPrefs(fn func(*config.UIPrefs)) {
	if m.id == 0 {
		// Bare test homes load no workspace; nothing to persist.
		return
	}
	p := m.uiPrefs()
	fn(&p)
	if err := m.core.SetUIPrefs(m.id, p); err != nil {
		log.For("app").Warn("ui_prefs_save_failed", "err", err)
		return
	}
	m.info.UIPrefs = p
}
```
`m.uiPrefs()` must return a deep copy, since `fn` writes into its maps. Either make the accessor clone (`config.UIPrefs` has an unexported `clone`, so export it as `Clone`), or clone here. Pick one and use it everywhere prefs are changed.

- [ ] **Step 2: Help screens.** `showHelpScreen` calls `m.core.SetHelpScreensSeen(m.id, seen|flag)`, then updates `m.info.HelpScreensSeen` on success.

- [ ] **Step 3: Settings.**
  - `home` gains `settingsEdit *config.Config`.
  - `runOpenSettings` sets `m.settingsEdit = config.FromSettings(m.settings())` and passes it to `NewSettingsOverlay`.
  - On save, `handleStateSettingsKey` (state_settings.go:33-54) replaces the save and its side effects with:
```go
	if changed {
		if err := m.core.SaveSettings(m.id, m.settingsEdit.Snapshot()); err != nil {
			return m, m.handleError(fmt.Errorf("save settings: %w", err))
		}
		m.syncWorkspaces()
	}
```
  - Closing the overlay clears `m.settingsEdit`.
  - The live theme change (settingsOverlay.go:188, `ui.ApplyTheme`) stays as it is.
  - Check that every overlay reads and writes only the `*config.Config` it was given. The profiles manager and Claude preferences get the same pointer through the settings overlay.
  - **The save path decision.** A bare context with no config dir saved to `config.GetConfigDir()` before. `SaveSettings` now does that in core (decision 5). The check `m.id == 0` covers bare test homes. Decide what the settings key does there, and keep the behaviour the existing tests pin.

- [ ] **Step 4: Transitions.**
  - `activateWorkspace` does `v, err := m.core.OpenTab(def)` and builds `m.newSlotView(v)`.
  - `deactivateWorkspace` calls `m.core.CloseTab(name)`, which returns only an error.
  - `enterGlobalMode` does `v, err := m.core.EnterGlobal(m.id)`, then reads `v.Recovery`.
  - `applyWorkspaceToggle` calls `m.core.Save(m.id)`.
  - `newHome` builds the classic slot from `m.core.Classic()` and the tab slots from `m.core.Tabs()`.
  - `restoreSavedWorkspaces` and `showRecoverySummary` read the slots' `recovery()`.
  - `Create`, `Views`, `IsLoaded` and `EnterGlobal` take `slot.id`. That covers `drafts.go:92` and `app_scripts.go:839`.

- [ ] **Step 5: The registry.**
  - `runOpenWorkspacePicker` replaces `config.LoadWorkspaceRegistry()` with `m.core.ReloadRegistry()`, which shows its error as before, followed by `reg := m.core.Registry()`. The picker reads `reg.Workspaces`.
  - `newHome` reads the open list and the registered workspaces through `h.core.Registry()` once the model exists. That covers app_init.go:150, 212-213 and 220-221.
  - Before `core.New`, `newHome` may still read the objects `Run` handed it, but after it, never. Order its reads so.

- [ ] **Step 6: Events.**
  - The `ViewsChanged` applier resolves `slotFor(ev.WS)`.
  - `applyStarted` and `applyRecovered` (completions.go) compare `ev.OwnerID` with `m.id` for "is it the focused slot", and resolve the owner's slot with `slotFor(ev.OwnerID)`. Their notices use `ev.OwnerLabel` and `ev.ClosedNote` in place of `owner.Label()` and `m.core.ClosedNote(owner)`.

### B4. Tests, verify, commit

- [ ] **Step 1: Test helpers** (`app/testcore_test.go`).
  - `wireCore` keeps installing workspaces through `SetWorkspacesForTest`, then sets each slot's `id` and `info` from the model before `syncViews`.
  - `testWS` and `slotOver` stay. The slot gets its ID when it is wired.
  - Add `wsOf(m, slot) *core.Workspace`, a seam for tests that still need the model's object, through `testModel(m)`.
  - Add `core.WorkspaceForTest(id) *core.Workspace` to `core/seams.go`.
- [ ] **Step 2: Convert the tests.**
  - Tests that read `slot.ws.X()` read the view or go through `wsOf`.
  - Tests that compare `slot.ws == ws` compare IDs.
  - Tests of the settings save assert on the model's view and on `config.json` on disk.
  - Tests of prefs assert on `state.json`, or on the next view after a `Sync`.
  - Never weaken an assertion. List any whose meaning changed.
- [ ] **Step 3: Grep for leftovers.** `git grep -n 'wsCtx()\|appConfig()\|appState()\|storage()\|\.ws\b\|ClassicWS\|TabsWS\|IsLoadedWS\|ClosedNoteWS\|OpenTabWS\|CloseTabWS\|EnterGlobalWS\|SaveWS\|ViewsWS\|CreateWS\|RegistryObj\|AccountsRegistry\|LoadWorkspaceRegistry' -- 'app/*.go' 'ui/*.go' ':!*_test.go'` must print nothing. If a line is legitimate, explain it.
- [ ] **Step 4: Run the checks.**
  ```bash
  CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...
  CC=clang CGO_ENABLED=1 go test -race ./app/... ./ui/... ./core/...
  CGO_ENABLED=0 go test -tags e2e ./e2e/...
  ```
  Expected: PASS.
- [ ] **Step 5: Commit** with the message `refactor(app,ui): the TUI holds workspace views, not workspaces`. The body:
  > Slots are keyed by WorkspaceID and cache their WorkspaceView. Reads go through the view; UI prefs, help screens and settings saves are requests (SaveSettings carries the config.json write and its side effects into the model); the settings overlay edits a TUI-owned config; the registry and the accounts' names cross as copies. Daemon stage 1D, package B.

  End the message with both trailers.

**Review focus for Package B:**
- **Prefs freshness.** Every same-Update reread of prefs and settings must see the write: the split ratio after a resize flush, the view mode after a toggle, and the program after a settings save.
- **The settings overlay never touches the model's config.** Cancelling it leaves the model's settings unchanged, and a theme preview is still reverted on cancel the way it is today.
- **Notices and the recovery summary keep their order.** The pin is `TestRegisterWorkspace_RecoverySummaryWinsOverRCOffLine`.
- **The startup handover.** Nothing reads the objects `Run` handed to `core.New` after the call.

---

## Package C: `core.Core` is value-typed

The pointer API leaves the interface and the events. Two tests then make the boundary permanent. The build is red from C1 until C3 ends. One commit at the end.

### C1. Deletions

- [ ] **Step 1: Events.**
  - Delete `ViewsChanged.Workspace`, `Started.Owner` and `Recovered.Owner` (the pointers).
  - Rename `OwnerID` to `Owner` in both events, and update every reader in app and core.
- [ ] **Step 2: The pointer methods.**
  - **Delete from `core.Core`:** `ClassicWS`, `TabsWS`, `IsLoadedWS`, `ClosedNoteWS`, `OpenTabWS`, `CloseTabWS`, `EnterGlobalWS`, `SaveWS`, `ViewsWS`, `CreateWS`, `RegistryObj` and `AccountsRegistry`.
  - **In `core`:** unexport those that core still uses (`openTab`, `enterGlobal`, `saveWS`, …), and delete the rest.
  - **`core/seams.go`:** add the seams that app tests still need, such as `WorkspaceForTest` from B4. `TestNoProductionCallsOfTestSeams` guards them.
- [ ] **Step 3: The `Core` doc comment.** Rewrite it: the boundary is value-typed (decision 13's rule), and `Sync`/`Deliver` are the job plumbing that stage 1E removes.

### C2. `TestCoreIsValueTyped`

**Files:** `core/boundary_test.go`.

- [ ] **Step 1: The test.**
```go
// TestCoreIsValueTyped fails when a core.Core method, or an Event the
// model emits, carries anything but plain data: values a client in
// another process could receive (stage 2 serializes them) without sharing
// the model's memory. Sync and Deliver carry the job plumbing stage 1E
// moves into the model, and are exempt until then.
func TestCoreIsValueTyped(t *testing.T) {
	exempt := map[string]string{
		"Sync":    "returns Out.Jobs (funcs) until stage 1E runs jobs in the model",
		"Deliver": "takes a job's result (any) until stage 1E",
	}
	core := reflect.TypeOf((*Core)(nil)).Elem()
	for i := 0; i < core.NumMethod(); i++ {
		m := core.Method(i)
		if _, ok := exempt[m.Name]; ok {
			continue
		}
		for j := 0; j < m.Type.NumIn(); j++ {
			checkPlain(t, m.Name+" param", m.Type.In(j), nil)
		}
		for j := 0; j < m.Type.NumOut(); j++ {
			checkPlain(t, m.Name+" result", m.Type.Out(j), nil)
		}
	}
	for _, ev := range allEvents {
		checkPlain(t, fmt.Sprintf("%T", ev), reflect.TypeOf(ev), nil)
	}
}
```
`checkPlain(t, where, typ, seen)` walks the type:
- **Basic kinds pass.** That covers bool, ints, uints, floats and string.
- **`time.Time` and `time.Duration` pass.**
- **Composite types are walked.** The test recurses into struct fields (exported and unexported alike), slice, array and map elements, map keys and pointer elements.
- **Two interfaces pass:** `error` and `core.Event`.
- **Everything else fails:** `func`, `chan`, `unsafe.Pointer`, every other interface, any type from package `sync`, and the deny list:
  - `core.Workspace`, `core.Model`;
  - `session.Instance`, `session.Storage`;
  - `config.Config`, `config.WorkspaceRegistry`, `config.State`;
  - `account.Registry`.

  Match these by `PkgPath()` and `Name()`.
- **`seen` guards against recursive types.**

Each failure names the path, for example `Tabs result → []WorkspaceView → Settings → …`.

- [ ] **Step 2: The event list.** `allEvents` is a literal list of zero values of every concrete event type. A second test keeps it complete: `TestAllEventsListsEveryEvent` parses the package's non-test files with `go/parser`, collects every type with a `coreEvent()` method, and compares that set with `allEvents`.

- [ ] **Step 3: Check it bites.** Temporarily add `Bad() *Workspace` to `Core` and `func (m *Model) Bad() *Workspace { return nil }`, and confirm the test fails, naming `Bad`. Remove both. Do the same with an event field `X func()`.

### C3. `TestTUIHoldsNoModelObject`

**Files:** `internal/testenv/instance_enforce_test.go`.

- [ ] **Step 1: Extend the 1C test,** and rename it `TestTUIHoldsNoModelObject` (keep a sentence in its comment saying it grew from `TestTUIHoldsNoInstance`). In `app/`, `ui/` and `script/` production files, it now also flags these selectors:
  - **core:** `core.Workspace`, `core.WorkspaceParts`, `core.NewWorkspace`, `core.Model` (except `core.New`'s result type, see below);
  - **session:** `session.Storage`, `session.SetLoomContextEnabled`, `session.SetSubagentTrackingEnabled`;
  - **config:** `config.WorkspaceRegistry`, `config.LoadWorkspaceRegistry`, `config.AppState`, `config.SaveConfigTo`;
  - **account:** `account.Registry`, `account.LoadRegistry`.

  It also keeps 1C's list.

  **The handover exemption.** `app/app_init.go`'s `Run` and `newHome` receive the startup objects from `main.go` and pass them to `core.New`. Exempt those two functions by name, for the selectors they need (`config.WorkspaceRegistry` in a signature, `core.New`), with the reason written in the test. `TestTUIHoldsNoModelObject` must still flag any other use of the same selectors inside them.

  Check how `app/accounts.go` builds the login command. If it needs `account.Command` or similar helpers, those are free functions over values, not the registry, and are allowed. Say which ones you allowed and why.

- [ ] **Step 2: Self-check.** Temporarily add `var _ *core.Workspace` to an app file, and `session.SetLoomContextEnabled(true)` in another function. The test must fail on both. Remove them.

### C4. Verify and commit

- [ ] **Step 1: Run the checks.**
  ```bash
  CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...
  CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./script/...
  CGO_ENABLED=0 go test -tags e2e ./e2e/...
  ```
  Expected: PASS.
- [ ] **Step 2: Coordinator checks.**
  - `TestCoreIsValueTyped`, `TestAllEventsListsEveryEvent`, `TestTUIHoldsNoModelObject`, `TestCoreImportsNoUI` and `TestNoProductionCallsOfTestSeams` all pass.
  - `git grep -n '\*core\.Workspace\|core\.Workspace{' -- 'app/*.go' 'ui/*.go' 'script/*.go' ':!*_test.go'` prints nothing.
  - The assertion count holds.
- [ ] **Step 3: Commit.**
```bash
git add core/ app/ ui/ internal/
git commit -m "refactor(core,app): core.Core is value-typed" -m "The pointer API leaves core.Core and the events: workspaces are named by
WorkspaceID everywhere. TestCoreIsValueTyped walks every Core method and event and
fails on anything but plain data (Sync and Deliver exempt until stage 1E moves the
jobs); TestTUIHoldsNoModelObject keeps app, ui and script off the model's
objects. Daemon stage 1D, package C." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WdG6KL8iCdQeM21uVaqCM7"
```

**Review focus for Package C:**
- **Does `checkPlain` bite?** It must reject each forbidden category, and `allEvents` must be complete.
- **Is the handover exemption as narrow as it can be?**
- **Is anything left that a TUI could use to reach model memory?** Look for a query returning a slice the model keeps, a pointer field in a view that aliases, or a seam called in production.

---

## Package D: documentation and verification

### D1. CLAUDE.md and the spec

- [ ] **Step 1: CLAUDE.md.**
  - **`app/` bullet.** A slot is `{id, info, views, list, splitPane, workbench}`. Settings, prefs and help are requests, the settings overlay edits a TUI-owned config, and `syncWorkspaces` rereads the store.
  - **`core/` bullet.** Add `WorkspaceID` and `WorkspaceView`, `WorkspacesChanged` (published first), the settings requests, `Registry`/`ReloadRegistry`, `AccountNames`, and the value-typed `Core` with its two tests.
  - **`config/` bullet.** Add `Settings`, embedded in `Config`, with `Snapshot`, `ReplaceSettings` and `FromSettings`.
  - **Focused-slot gotcha.** The slot is a view over a `WorkspaceID`, not a `core.Workspace`. Rewrite its accessor sentence: read through the view, write through the model.
  - **Destructive-actions gotcha.** `Started`/`Recovered` name the owner by ID.
  - **Persistent State bullet for `config.json`.** Only the model writes it (`SaveSettings`).
  - **Testing Patterns.** Add `wsOf`/`WorkspaceForTest`, and say that `wireCore` sets slot IDs.
  - **Stale names.** Find them with `git grep -n -w -e wsCtx -e appConfig -e appState -e 'storage()' -e 'Recovery()' -e ClosedNote -e LoadWorkspaceRegistry -e 'Accounts()' -- CLAUDE.md`, and fix each hit.
- [ ] **Step 2: The spec.** In Rollout stage 1, link the 1D entry to this plan with a one-line summary. Add a 1E entry: the model loop on its own goroutine, the model running its own jobs, the coalesced wake, and the synchronous call loop that keeps the read-after-write sites working. Stage 2's line notes that its transport must revisit those sites (decision 14).
- [ ] **Step 3: Commit** with `docs: CLAUDE.md and spec for the workspace boundary (daemon stage 1D)`, ending with both trailers.

### D2. Verification and smoke run

- [ ] **Step 1: The suite.** Run `go vet`, `go test ./...`, `-race ./...`, e2e and gofmt, as in 1C's E2. All must be green.
- [ ] **Step 2: Sandbox smoke run.** Use the loom-dev skill and the 1C brief's safety rules:
  - Build once at a named SHA, then `start --no-build`.
  - Build a baseline sandbox from the 1C merge commit (`git archive` into the scratchpad, no worktree).
  - Point `CLAUDE_CONFIG_DIR` at a throwaway dir.
  - Never touch `~/.loom`, `~/.claude` or the user's tmux server.

  Run the 1C smoke's checks 1–14, then these new ones:
  1. **Settings.** Change the default program, the branch prefix and a Claude preference. Save, quit and restart: each persists. `config.json` holds them with the same keys. A new session uses the new program. The theme cycles live and persists.
  2. **Settings cancel.** Change several settings, then cancel. Nothing changes, on screen or on disk.
  3. **UI prefs.** Toggle the rail, the terminal and the overview, and resize a split. Quit and restart: each persists, per workspace (two tabs with different prefs).
  4. **Help screens.** A help screen seen once is not shown again after a restart.
  5. **Registry.** Register a new workspace from a second shell (`loom workspace add` against the sandbox's global dir) while the TUI runs. `W` shows it. Open it, close it, and enter global mode: the open list on disk follows each step.
  6. **Recovery summary.** An orphan worktree shows the recovery summary on load, after the load's own notices.
  7. **Storage latch.** A workspace whose `state.json` holds a corrupt instances payload refuses new sessions with the latched message.

  Check every oddity against the baseline before calling it a regression.

- [ ] **Step 3: Report** the test totals, each smoke check's outcome, and every deviation from this plan.

### D3. Outcome (coordinator, after the final review)

- [ ] Append "Outcome and follow-ups" to this plan, and update the `loom-scrum-daemon-direction` memory: 1D is done, and the next step is plan 1E.
