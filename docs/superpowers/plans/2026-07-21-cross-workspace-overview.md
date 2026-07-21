# Cross-Workspace Overview (Mission Control Phase 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the overview a LIVE global fleet view across every registered workspace, loaded lazily and progressively on first overview entry, with cross-workspace `enter`/`]`/`[`/`D`/`r` navigation.

**Architecture:** Extend the existing `m.slots` mechanism (liveness/monitoring/notify are already global across open slots) with a `background` slot flag (live but not a tab). First overview entry fires background `tea.Cmd`s that reconcile each missing workspace off the Update goroutine and hand domain data back via `workspaceActivatedMsg`. The overview becomes multi-group with a global `(slot,inst)` cursor; a single `focusCursorSlot()` primitive lets cross-workspace actions reuse existing focus-mode intents.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2, gopher-lua, testify. Spec: `docs/superpowers/specs/2026-07-21-cross-workspace-overview-design.md`.

---

## Required background (read before Task 1)

- **Build/test:** `CGO_ENABLED=0 go build ./...` · `CGO_ENABLED=0 go test ./<pkg>/ -v` · race: `CC=clang CGO_ENABLED=1 go test -race ./...`. Format: `gofmt -w .`. Vet: `go vet ./...` (do NOT run golangci-lint — local v2 is incompatible with the repo config).
- **Concurrency rules (CLAUDE.md):** no `home`/model mutation from `tea.Cmd` goroutines. A Cmd may spawn tmux and touch its own fresh instances, but the only `m.slots`/`m.list` mutation happens in an `Update` message handler. `tea.Program.Send` (the notifier) is safe from any goroutine.
- **Slots:** `home.slots []workspaceSlot` (`app/app.go:170,297`). The focused slot's `list`/`splitPane`/`storage`/`appConfig`/`appState` are hoisted onto `home` by `loadSlot` (`app/app.go:2426`); `saveCurrentSlot` (`app/app.go:2405`) writes them back. `activateWorkspace` (`app/app.go:2255`) reconciles + PTY-attaches + appends a slot.
- **Already global (do NOT rebuild):** `instanceForSession` resolves across all slots (`app/events.go:42`); `paneQuietMsg`/`bellMsg` handlers act on whatever slot's instance resolves; the metadata tick fans out across all slots (`app/app.go:1108`). A background slot's agents notify and transition today.
- **Overview today:** `ui.Overview.Render(OverviewData)` (`ui/overview.go:78`) renders ONE active group + peer-count footer; `overviewData()` (`app/app.go:2714`) builds it from `m.list` only; `moveCursor` (`app/app.go:2736`) and `jumpWaiting` (`app/app.go:768`) are scoped to `m.list`. Overview key routing is in `app/state_default.go:48`.
- **Test homes:** `newTestHome(t)` (`app/actions_test.go:22`) for engine-backed tests; a bare `&home{list:..., slots:..., focusedSlot:...}` literal for pure slot-logic tests (see `app/peer_sections_test.go:50`). Instances for status tests: `&session.Instance{Title:"x", Status: session.Ready}`.
- **Sizing:** lipgloss `.Width`/`.Height` are TOTAL box size and `.Height` is a min not a cap — always clamp (`clampHeight`, `ui/split_pane.go`).

## File structure

| File | Change |
|---|---|
| `app/app.go` | `workspaceSlot.background`; `home.fleetLoading/fleetLoadErrors/fleetEngaged/overviewCursor`; foreground helpers; `promoteSlot`/`demoteSlot`; `loadWorkspaceForFleet`; `ensureFleetLoaded`; `workspaceActivatedMsg` handler; `overviewData` multi-group; `moveCursor` global; `focusCursorSlot`; `jumpWaiting` fleet-wide; `fleetSlotOrder`; `applyWorkspaceToggle` fleet branch; tab-bar/persistence via foreground helpers |
| `app/app_scripts.go` | `ToggleOverview` fires `ensureFleetLoaded` |
| `app/state_default.go` | overview `enter`/`D`/`r`/`R`/`n`/`N` route through `focusCursorSlot` |
| `app/events.go` | (read-only reference) |
| `ui/overview.go` | `GroupState`, `OverviewGroup`, `OverviewCursor`, new `OverviewData`; multi-group `Render`; per-group grid; combined windowing; drop `Peers`/`peerBudget` |
| `ui/overview_test.go` | rewrite expectations for the new shape + new state/window tests |
| `app/*_test.go` (new files) | slot-model, fleet-load, cursor, nav, teardown tests |

---

# Phase A — Slot model foundation

### Task 1: `background` slot flag + foreground helpers

**Files:**
- Modify: `app/app.go` (`workspaceSlot` struct ~170; `slotNames` ~2796; tab-bar calls at 2439/2530; `saveOpenWorkspaces` ~2771)
- Test: `app/slot_model_test.go` (new)

- [ ] **Step 1: Write the failing test**

```go
// app/slot_model_test.go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
)

func TestForegroundSlotNames_ExcludesBackground(t *testing.T) {
	m := &home{
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "a"}},
			{wsCtx: &config.WorkspaceContext{Name: "b"}, background: true},
			{wsCtx: &config.WorkspaceContext{Name: "c"}},
		},
	}
	assert.Equal(t, []string{"a", "c"}, m.foregroundSlotNames())
}

func TestForegroundSlotsAndSelected_RemapsFocusIndex(t *testing.T) {
	// Foreground slots are a=0 (bg b skipped) and c=2. Focused is c
	// (m.slots index 2) → foreground-subset index 1.
	m := &home{
		focusedSlot: 2,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "a"}},
			{wsCtx: &config.WorkspaceContext{Name: "b"}, background: true},
			{wsCtx: &config.WorkspaceContext{Name: "c"}},
		},
	}
	names, sel := m.foregroundSlotsAndSelected()
	assert.Equal(t, []string{"a", "c"}, names)
	assert.Equal(t, 1, sel)
}
```

- [ ] **Step 2: Run to verify failure** — `CGO_ENABLED=0 go test ./app/ -run 'TestForegroundSlot' -v` — Expected: FAIL (undefined: `background`, `foregroundSlotNames`, `foregroundSlotsAndSelected`).

- [ ] **Step 3: Implement**

Add to `workspaceSlot` (after `recovery`, `app/app.go:179`):

```go
	// background marks a slot loaded solely to feed the live global
	// overview: fully reconciled and PTY-attached like any slot, but
	// hidden from the tab bar and never persisted to OpenWorkspaces.
	// Cleared (promoted) when the slot becomes focused. The focused slot
	// is never background.
	background bool
```

Add after `slotNames` (`app/app.go:2796`):

```go
// foregroundSlotNames returns the names of non-background slots, in slot
// order — the set the tab bar shows and saveOpenWorkspaces persists.
func (m *home) foregroundSlotNames() []string {
	names := make([]string, 0, len(m.slots))
	for _, slot := range m.slots {
		if !slot.background {
			names = append(names, slot.wsCtx.Name)
		}
	}
	return names
}

// foregroundSlotsAndSelected returns the foreground slot names plus the
// focused slot remapped to its index within that subset. Safe because
// the focused slot is never background; falls back to 0 if it somehow is.
func (m *home) foregroundSlotsAndSelected() ([]string, int) {
	names := make([]string, 0, len(m.slots))
	sel := 0
	for i, slot := range m.slots {
		if slot.background {
			continue
		}
		if i == m.focusedSlot {
			sel = len(names)
		}
		names = append(names, slot.wsCtx.Name)
	}
	return names, sel
}
```

Change `saveOpenWorkspaces` (`app/app.go:2775`) to persist foreground only:

```go
	if err := m.registry.SetOpenWorkspaces(m.foregroundSlotNames()); err != nil {
```

Change both `m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)` calls (`app/app.go:2439` in `loadSlot`, and `:2530` in `applyWorkspaceToggle`) to:

```go
	fgNames, fgSel := m.foregroundSlotsAndSelected()
	m.tabBar.SetWorkspaces(fgNames, fgSel)
```

Also update `restoreSavedWorkspaces`' persistence at `app/app.go:651` (`SetOpenWorkspaces(m.slotNames())`) to `m.foregroundSlotNames()` — at that point no slots are background, so behavior is unchanged, but it keeps the invariant that OpenWorkspaces only ever holds foreground names.

- [ ] **Step 4: Run** — same command — Expected: PASS.
- [ ] **Step 5: Build + vet** — `CGO_ENABLED=0 go build ./... && go vet ./app/` — Expected: clean.
- [ ] **Step 6: Commit**

```bash
git add app/app.go app/slot_model_test.go
git commit -m "feat(app): background slot flag and foreground-only tab bar/persistence

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 2: `promoteSlot` / `demoteSlot`

**Files:**
- Modify: `app/app.go` (near `saveOpenWorkspaces`)
- Test: `app/slot_model_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestPromoteSlot_ClearsBackgroundAndPersists(t *testing.T) {
	m := newTestHome(t)
	m.registry = config.NewWorkspaceRegistry() // in-memory registry
	m.slots = []workspaceSlot{
		{wsCtx: &config.WorkspaceContext{Name: "a"}},
		{wsCtx: &config.WorkspaceContext{Name: "b"}, background: true},
	}
	m.focusedSlot = 0

	m.promoteSlot(1)
	assert.False(t, m.slots[1].background, "promoted slot is no longer background")
	assert.Equal(t, []string{"a", "b"}, m.foregroundSlotNames())

	m.demoteSlot(1)
	assert.True(t, m.slots[1].background, "demoted slot is background again")
	assert.Equal(t, []string{"a"}, m.foregroundSlotNames())
}
```

If `config.NewWorkspaceRegistry` does not exist, construct a registry the way `newTestHome`-adjacent tests do — check `grep -n "WorkspaceRegistry{" app/*_test.go config/*_test.go` and mirror; a `&config.WorkspaceRegistry{}` literal is acceptable here since `SetOpenWorkspaces` tolerates an empty registry (it just persists to the tempdir the test home uses). If `m.registry` is nil, `saveOpenWorkspaces` already no-ops, so setting the field is only needed to exercise the persistence path.

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./app/ -run TestPromoteSlot -v` — Expected: FAIL (undefined `promoteSlot`/`demoteSlot`).

- [ ] **Step 3: Implement** (add after `saveOpenWorkspaces`):

```go
// promoteSlot clears a slot's background flag (making it a real tab),
// refreshes the tab bar, and persists the new open-workspace set. Called
// when a background slot becomes the focus target. Main-goroutine only.
func (m *home) promoteSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) || !m.slots[idx].background {
		return
	}
	m.slots[idx].background = false
	fgNames, fgSel := m.foregroundSlotsAndSelected()
	m.tabBar.SetWorkspaces(fgNames, fgSel)
	m.saveOpenWorkspaces()
}

// demoteSlot marks a foreground slot as background (dropping it from the
// tab bar and OpenWorkspaces) while keeping it live. Never demotes the
// focused slot — callers move focus away first. Main-goroutine only.
func (m *home) demoteSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) || m.slots[idx].background || idx == m.focusedSlot {
		return
	}
	m.slots[idx].background = true
	fgNames, fgSel := m.foregroundSlotsAndSelected()
	m.tabBar.SetWorkspaces(fgNames, fgSel)
	m.saveOpenWorkspaces()
}
```

- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/slot_model_test.go
git commit -m "feat(app): promoteSlot/demoteSlot for background-slot transitions

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

# Phase B — Progressive fleet loading

### Task 3: `loadWorkspaceForFleet` + `workspaceActivatedMsg` + `ensureFleetLoaded`

**Files:**
- Modify: `app/app.go` (near `activateWorkspace` ~2255; `home` struct fields ~302)
- Test: `app/fleet_load_test.go` (new)

- [ ] **Step 1: Add `home` fields** (after `lastHeight`, `app/app.go:304`):

```go
	// fleetLoading holds the names of workspaces whose background
	// activation Cmd is in flight (so ensureFleetLoaded doesn't
	// double-fire). fleetLoadErrors records the last background-load
	// error per workspace, surfaced as an error group in the overview.
	// fleetEngaged is set once the user has opened the overview at least
	// once this session; it flips tab-close from teardown to demote.
	fleetLoading    map[string]bool
	fleetLoadErrors map[string]error
	fleetEngaged    bool
	// overviewCursor is the domain-space overview selection: a slot index
	// into m.slots and an instance index into that slot's list. Distinct
	// from the render-space ui.OverviewCursor overviewData() translates to.
	overviewCursor overviewCursor
```

Add the type near `viewMode` (`app/app.go:161`):

```go
// overviewCursor is the fleet overview's selection in domain coordinates.
type overviewCursor struct {
	slot int // index into home.slots
	inst int // index into that slot's list
}
```

- [ ] **Step 2: Write the failing test**

```go
// app/fleet_load_test.go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ensureFleetLoaded should emit one activation per registered workspace
// that is not already a slot and not already loading, and mark each as
// loading. Workspaces with no git repo still get attempted (the Cmd
// itself reports the error); this test only checks the loading set + that
// already-present slots are skipped.
func TestEnsureFleetLoaded_SkipsOpenAndLoading(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
		{Name: "c", Path: "/tmp/c"},
	}}
	// "a" already a slot; "b" already loading; only "c" should be queued.
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}}}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"b": true}

	cmd := m.ensureFleetLoaded()
	require.NotNil(t, cmd, "a workspace remained to load")
	assert.True(t, m.fleetLoading["c"], "c marked loading")
	assert.True(t, m.fleetEngaged, "overview engaged")
	assert.False(t, m.fleetLoading["a"], "already-open workspace not queued")
}

func TestEnsureFleetLoaded_NothingToLoad(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{{Name: "a"}}}
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}}}
	m.focusedSlot = 0
	cmd := m.ensureFleetLoaded()
	assert.Nil(t, cmd, "no workspaces left to load → no Cmd")
	assert.True(t, m.fleetEngaged)
}
```

- [ ] **Step 3: Run** — `CGO_ENABLED=0 go test ./app/ -run TestEnsureFleetLoaded -v` — Expected: FAIL.

- [ ] **Step 4: Implement** (add after `activateWorkspace`, `app/app.go:2369`):

```go
// workspaceActivatedMsg carries the reconciled domain data for a
// background-loaded workspace back to the Update goroutine, where the
// slot's UI is built and appended. Produced off-goroutine by
// loadWorkspaceForFleet.
type workspaceActivatedMsg struct {
	name      string
	wsCtx     *config.WorkspaceContext
	storage   *session.Storage
	appConfig *config.Config
	appState  config.AppState
	instances []*session.Instance
	err       error
}

// loadWorkspaceForFleet reconciles one workspace off the Update goroutine
// (git validation + PTY attach happen inside LoadAndReconcile). It builds
// NO UI components and mutates NO model state — only the returned message
// crosses back. On any failure it returns a msg carrying err (the slot is
// simply not created; the overview shows an error group). Safe to run in
// a tea.Cmd goroutine.
func (m *home) loadWorkspaceForFleet(ws config.Workspace) workspaceActivatedMsg {
	wsCtx := config.WorkspaceContextFor(&ws)
	state := config.LoadStateFrom(wsCtx.ConfigDir)
	appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)
	storage, err := session.NewStorage(state, wsCtx.ConfigDir)
	if err != nil {
		return workspaceActivatedMsg{name: ws.Name, err: fmt.Errorf("storage: %w", err)}
	}
	instances, err := storage.LoadAndReconcile(cmd2.MakeExecutor())
	if err != nil {
		return workspaceActivatedMsg{name: ws.Name, err: fmt.Errorf("reconcile: %w", err)}
	}
	return workspaceActivatedMsg{
		name: ws.Name, wsCtx: wsCtx, storage: storage,
		appConfig: appConfig, appState: state, instances: instances,
	}
}

// ensureFleetLoaded marks fleet-engaged and returns a batched Cmd that
// background-activates every registered workspace not already a slot or
// already loading. Nil when nothing remains — safe to call on every
// overview entry (it naturally retries errored/newly-registered ones).
// Main-goroutine only.
func (m *home) ensureFleetLoaded() tea.Cmd {
	m.fleetEngaged = true
	if m.registry == nil {
		return nil
	}
	if m.fleetLoading == nil {
		m.fleetLoading = map[string]bool{}
	}
	open := map[string]bool{}
	for _, slot := range m.slots {
		open[slot.wsCtx.Name] = true
	}
	var cmds []tea.Cmd
	for _, ws := range m.registry.Workspaces {
		if ws.Name == "" || open[ws.Name] || m.fleetLoading[ws.Name] {
			continue
		}
		m.fleetLoading[ws.Name] = true
		wsCopy := ws
		cmds = append(cmds, func() tea.Msg { return m.loadWorkspaceForFleet(wsCopy) })
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
```

Note: `loadWorkspaceForFleet` reads no `home` fields except `m.registry` (already read on the main goroutine before the Cmd fires) — it is a method only for namespacing; it touches only locals. This satisfies the tea.Cmd rule.

- [ ] **Step 5: Run** — Expected: PASS. **Build** — `CGO_ENABLED=0 go build ./...` — Expected: clean (handler comes in Task 4; the msg type is defined, just unhandled — that compiles).
- [ ] **Step 6: Commit**

```bash
git add app/app.go app/fleet_load_test.go
git commit -m "feat(app): off-goroutine workspace reconcile and ensureFleetLoaded

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 4: `workspaceActivatedMsg` handler

**Files:**
- Modify: `app/app.go` (Update switch, alongside `workspaceRegisteredMsg` ~1493)
- Test: `app/fleet_load_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestHandleWorkspaceActivated_AppendsBackgroundSlot(t *testing.T) {
	m := newTestHome(t)
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}, list: m.list}}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"b": true}

	st := config.LoadStateFrom(t.TempDir())
	stor, err := session.NewStorage(st, t.TempDir())
	require.NoError(t, err)
	msg := workspaceActivatedMsg{
		name: "b", wsCtx: &config.WorkspaceContext{Name: "b"},
		storage: stor, appConfig: config.DefaultConfig(), appState: st,
		instances: []*session.Instance{{Title: "sess", Status: session.Ready}},
	}
	m.handleWorkspaceActivated(msg)

	require.Len(t, m.slots, 2)
	assert.Equal(t, "b", m.slots[1].wsCtx.Name)
	assert.True(t, m.slots[1].background, "fleet-loaded slot is background")
	assert.False(t, m.fleetLoading["b"], "loading flag cleared")
	assert.Equal(t, 0, m.focusedSlot, "focus unchanged by a background load")
	assert.NotEqual(t, m.list, m.slots[1].list, "background slot has its own list")
}

func TestHandleWorkspaceActivated_ErrorRecorded(t *testing.T) {
	m := newTestHome(t)
	m.fleetLoading = map[string]bool{"b": true}
	m.handleWorkspaceActivated(workspaceActivatedMsg{name: "b", err: assertErr})
	assert.Empty(t, m.slots)
	assert.False(t, m.fleetLoading["b"])
	assert.Error(t, m.fleetLoadErrors["b"])
}

func TestHandleWorkspaceActivated_DuplicateDiscarded(t *testing.T) {
	m := newTestHome(t)
	m.slots = []workspaceSlot{
		{wsCtx: &config.WorkspaceContext{Name: "a", ConfigDir: "/x/a"}, list: m.list},
	}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"a": true}
	m.handleWorkspaceActivated(workspaceActivatedMsg{
		name: "a", wsCtx: &config.WorkspaceContext{Name: "a", ConfigDir: "/x/a"},
	})
	assert.Len(t, m.slots, 1, "duplicate ConfigDir discarded")
}

var assertErr = fmt.Errorf("boom")
```

Add `"fmt"` to imports if the test file lacks it.

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./app/ -run TestHandleWorkspaceActivated -v` — Expected: FAIL (undefined `handleWorkspaceActivated`).

- [ ] **Step 3: Implement**

Add a case in the Update `switch msg := msg.(type)` (next to `workspaceRegisteredMsg`, `app/app.go:1493`):

```go
	case workspaceActivatedMsg:
		m.handleWorkspaceActivated(msg)
		return m, m.instanceChanged()
```

Add the handler (near `activateWorkspace`):

```go
// handleWorkspaceActivated finalizes a background workspace load on the
// Update goroutine: builds the slot's UI from the reconciled instances
// and appends it as a background slot. Errors are recorded (not fatal);
// a duplicate (the user opened this workspace via the picker mid-load) is
// discarded. Main-goroutine only.
func (m *home) handleWorkspaceActivated(msg workspaceActivatedMsg) {
	if m.fleetLoading != nil {
		delete(m.fleetLoading, msg.name)
	}
	if msg.err != nil {
		if m.fleetLoadErrors == nil {
			m.fleetLoadErrors = map[string]error{}
		}
		m.fleetLoadErrors[msg.name] = msg.err
		log.For("app").Warn("fleet_load_failed", "workspace", msg.name, "err", msg.err)
		return
	}
	// Duplicate guard: a slot for this ConfigDir may already exist if the
	// user opened it via the picker while the Cmd was in flight.
	for _, slot := range m.slots {
		if slot.wsCtx.ConfigDir == msg.wsCtx.ConfigDir {
			return
		}
	}
	if m.fleetLoadErrors != nil {
		delete(m.fleetLoadErrors, msg.name)
	}

	list := ui.NewList(&m.spinner)
	for _, inst := range msg.instances {
		if inst.CrashRecovered {
			if err := inst.CrashRestart(); err != nil {
				log.For("app").Error("fleet_crash_restart_failed", "instance", inst.Title, "err", err)
				if tErr := inst.TransitionTo(session.Paused); tErr != nil {
					log.For("app").Warn("fleet_crash_transition_failed", "instance", inst.Title, "err", tErr)
				}
			}
			inst.CrashRecovered = false
		}
		list.AddInstance(inst)
	}
	list.SetWorkspaceName(msg.name)

	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		list.SetSize(listWidth, contentHeight)
		splitPane.SetSize(paneWidth, contentHeight)
	}

	// Orphan reconcile on the main goroutine (bounded disk scan; matches
	// activateWorkspace). Background slots suppress workspace-terminal
	// auto-spawn — no WT is created here, by design.
	recovery := m.reconcileOrphans(msg.wsCtx.ConfigDir, msg.appConfig.GetProgram(), list, msg.storage, cmd2.MakeExecutor())

	m.slots = append(m.slots, workspaceSlot{
		wsCtx: msg.wsCtx, storage: msg.storage, appConfig: msg.appConfig,
		appState: msg.appState, list: list, splitPane: splitPane,
		recovery: recovery, background: true,
	})
	m.refreshPeerSections()
}
```

- [ ] **Step 4: Run** — Expected: PASS. **Build** — `CGO_ENABLED=0 go build ./...`.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/fleet_load_test.go
git commit -m "feat(app): append background slot on workspaceActivatedMsg

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 5: Fire `ensureFleetLoaded` on overview entry

**Mechanism:** `ToggleOverview` (`app/app_scripts.go:332`) is a `scriptHost` deferred action — it records a `func(*home)` applied later in `handleScriptDone`, and deferred funcs cannot return a `tea.Cmd`. So the load is dispatched in two steps: the deferred func sets `viewMode`, seeds the cursor, and raises `m.pendingOverviewLoad`; `handleScriptDone` (main goroutine, after applying deferred funcs) drains that flag by batching in `m.ensureFleetLoaded()`. The `applyUIPrefs` startup restore path uses the same flag.

**Files:**
- Modify: `app/app.go` (`home` struct — add `pendingOverviewLoad bool`; add `enterOverview`; `applyUIPrefs` overview branch ~727)
- Modify: `app/app_scripts.go` (`ToggleOverview` ~332)
- Modify: the file defining `handleScriptDone` (find: `grep -n "func (m \*home) handleScriptDone" app/*.go`)
- Test: `app/fleet_load_test.go` (append)

- [ ] **Step 1: Read the two functions** — `grep -n "func (s \*scriptHost) ToggleOverview" app/app_scripts.go` and read its body (it currently calls `s.deferModelMutation(func(m *home){ ... m.viewMode = ... })`); and read `handleScriptDone` to see how it returns its `tea.Cmd` (it ends in a `tea.Batch(...)` or single return you will fold `extra` into).

- [ ] **Step 2: Write the failing test**

```go
func TestEnterOverview_SetsPendingLoad(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "a", Path: "/tmp/a"}, {Name: "b", Path: "/tmp/b"},
	}}
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}, list: m.list}}
	m.focusedSlot = 0

	m.enterOverview()
	assert.Equal(t, viewOverview, m.viewMode)
	assert.True(t, m.pendingOverviewLoad, "overview entry raises the load flag")

	// handleScriptDone drains it into a real load Cmd.
	cmd := m.ensureFleetLoaded()
	assert.NotNil(t, cmd, "load Cmd for workspace b")
	assert.True(t, m.fleetLoading["b"])
}
```

- [ ] **Step 3: Implement**

Add `pendingOverviewLoad bool` to `home` (next to the fleet fields from Task 3). Add to `app/app.go` near `overviewData`:

```go
// enterOverview switches to overview mode and seeds the cursor. It does
// NOT dispatch the fleet-load Cmd itself (callers run inside deferred
// funcs that can't return a Cmd); it raises pendingOverviewLoad, which
// handleScriptDone drains via ensureFleetLoaded. Main-goroutine only.
func (m *home) enterOverview() {
	m.viewMode = viewOverview
	m.seedOverviewCursor()
	m.pendingOverviewLoad = true
}
```

Add the temporary `seedOverviewCursor` (Task 9 replaces it with the fleet-aware version):

```go
// seedOverviewCursor points the overview cursor at the focused slot's
// selection. (Expanded to skip non-selectable positions in Task 9.)
func (m *home) seedOverviewCursor() {
	m.overviewCursor = overviewCursor{slot: m.focusedSlot, inst: m.list.SelectedIdx()}
}
```

In `ToggleOverview`'s deferred body (`app/app_scripts.go`), replace the branch that sets overview mode so it calls `m.enterOverview()` instead of assigning `m.viewMode = viewOverview` directly. Leave the focus-mode branch (and its `mutateUIPrefs` persistence) as-is.

In `handleScriptDone`, immediately before it builds its return `tea.Cmd`, add:

```go
	var overviewLoad tea.Cmd
	if m.pendingOverviewLoad {
		m.pendingOverviewLoad = false
		overviewLoad = m.ensureFleetLoaded()
	}
```

and include `overviewLoad` in the batch it returns. `handleScriptDone` returns a single `tea.Cmd` (signature `func (m *home) handleScriptDone(msg scriptDoneMsg) tea.Cmd`), so fold it as `return tea.Batch(existingCmd, overviewLoad)` — `tea.Batch` ignores nil.

In `applyUIPrefs` (`app/app.go:727`, the `p.ViewMode == "overview"` branch), replace `m.viewMode = viewOverview` with `m.enterOverview()`. The startup caller of `applyUIPrefs` already batches `tea.RequestWindowSize`; add `m.ensureFleetLoaded()` (draining `pendingOverviewLoad`) to that same batch so a restored-into-overview launch loads the fleet. Find the caller (`grep -n "applyUIPrefs()" app/app.go`) and fold the Cmd in; clear `pendingOverviewLoad` there after draining.

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./app/ -run 'TestEnterOverview|TestEnsureFleetLoaded|TestHandleWorkspaceActivated' -v` — Expected: PASS. **Build**.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/app_scripts.go app/fleet_load_test.go
git commit -m "feat(app): trigger progressive fleet load on overview entry

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

# Phase C — Multi-group overview data + rendering

> Tasks 6 and 8 change `ui.OverviewData`'s shape and its app-side builder together — they compile as one unit and commit together (like the phase-1 theme migration). Task 7 (windowing) follows.

### Task 6+8: Multi-group `OverviewData`, `Render`, and `overviewData()` builder

**Files:**
- Modify: `ui/overview.go` (types ~27–34; `Render` ~78; `renderGrid` ~122; delete `peerBudget`)
- Modify: `app/app.go` (`overviewData` ~2714)
- Test: `ui/overview_test.go` (rewrite affected tests), `app/overview_data_test.go` (new)

- [ ] **Step 1: Write the failing ui test** (append/replace in `ui/overview_test.go`)

```go
func TestOverview_RendersMultipleGroups(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, 40)
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "alpha", State: GroupLoaded,
				Items: []*session.Instance{{Title: "a1", Status: session.Ready}},
				Order: []int{0}},
			{Name: "beta", State: GroupLoading},
			{Name: "gamma", State: GroupError, Err: "reconcile: boom"},
			{Name: "delta", State: GroupEmpty},
		},
		Cursor: OverviewCursor{Group: 0, Item: 0},
	}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "ALPHA")
	assert.Contains(t, out, "a1")
	assert.Contains(t, out, "BETA")
	assert.Contains(t, out, "loading")
	assert.Contains(t, out, "GAMMA")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "DELTA")
	assert.Contains(t, out, "no sessions")
}
```

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./ui/ -run TestOverview_RendersMultipleGroups -v` — Expected: FAIL (undefined types).

- [ ] **Step 3: Implement the ui types + Render**

Replace `OverviewData` (`ui/overview.go:27-34`) and add group types:

```go
// GroupState classifies how an overview group renders.
type GroupState int

const (
	GroupLoaded  GroupState = iota // reconciled; render cards
	GroupLoading                   // background activation in flight
	GroupError                     // background activation failed
	GroupEmpty                     // loaded but no instances
)

// OverviewGroup is one workspace's slice of the fleet overview.
type OverviewGroup struct {
	Name  string
	Items []*session.Instance
	Order []int // SortForOverview(Items); empty for non-loaded states
	State GroupState
	Err   string // populated when State == GroupError
}

// OverviewCursor is the render-space selection: a group index into
// Groups and an item position within that group's Order.
type OverviewCursor struct {
	Group int
	Item  int
}

// OverviewData is everything Render needs, assembled by the app on the
// Update goroutine each frame.
type OverviewData struct {
	Groups  []OverviewGroup
	Cursor  OverviewCursor
	Spinner string
}
```

Rewrite `Render` (`ui/overview.go:78`) to iterate groups:

```go
// Render draws the multi-group fleet overview. Height is hard-clamped;
// the combined vertical window (Task 7) keeps the cursor group/card
// visible.
func (o *Overview) Render(d OverviewData) string {
	if o.width == 0 || o.height == 0 {
		return ""
	}
	blocks := o.groupBlocks(d) // []string, one rendered block per group
	windowed := o.window(blocks, d)
	return clampHeight(lipgloss.Place(o.width, o.height, lipgloss.Left, lipgloss.Top, windowed), o.height)
}

// groupBlocks renders each group to a string block (header + body).
func (o *Overview) groupBlocks(d OverviewData) []string {
	blocks := make([]string, len(d.Groups))
	wsStyle := lipgloss.NewStyle().Foreground(Workspace)
	dim := lipgloss.NewStyle().Foreground(Dim)
	errStyle := lipgloss.NewStyle().Foreground(ErrorColor)
	for gi, g := range d.Groups {
		collapsed := o.IsCollapsed(g.Name)
		marker := "▾"
		if collapsed {
			marker = "▸"
		}
		header := wsStyle.Render(fmt.Sprintf("%s %s · %d", marker, strings.ToUpper(g.Name), len(g.Items)))
		var body string
		switch {
		case collapsed:
			body = ""
		case g.State == GroupLoading:
			body = dim.Render("  loading…")
		case g.State == GroupError:
			body = errStyle.Render("  failed to load — " + g.Err)
		case g.State == GroupEmpty || len(g.Order) == 0:
			body = dim.Render("  no sessions")
		default:
			body = o.renderGroupGrid(g, gi, d)
		}
		if body == "" {
			blocks[gi] = header
		} else {
			blocks[gi] = header + "\n" + body
		}
	}
	return blocks
}
```

Replace `renderGrid` with `renderGroupGrid` (per-group, no self-windowing — the combined window is Task 7; here render ALL rows of the group):

```go
// renderGroupGrid lays one group's cards in rows of overviewColumns.
// Highlighting keys on the render cursor matching this group + position.
func (o *Overview) renderGroupGrid(g OverviewGroup, gi int, d OverviewData) string {
	cols := overviewColumns(o.width)
	cardW := (o.width - (cols - 1)) / cols
	nRows := (len(g.Order) + cols - 1) / cols
	rows := make([]string, 0, nRows)
	for r := 0; r < nRows; r++ {
		start := r * cols
		end := start + cols
		if end > len(g.Order) {
			end = len(g.Order)
		}
		cards := make([]string, 0, end-start)
		for pos := start; pos < end; pos++ {
			idx := g.Order[pos]
			selected := d.Cursor.Group == gi && d.Cursor.Item == pos
			cd := BuildCardData(g.Items[idx], selected, d.Spinner, overviewCardTailLines)
			cd.Index = DisplayIndex(g.Items, idx)
			cards = append(cards, renderOverviewCard(cd, cardW))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(cards)...))
	}
	return strings.Join(rows, "\n")
}
```

Add a temporary pass-through `window` for this task (Task 7 replaces it):

```go
// window joins group blocks vertically. Combined cursor-aware windowing
// lands in Task 7; for now it joins all blocks (clampHeight bounds it).
func (o *Overview) window(blocks []string, d OverviewData) string {
	return strings.Join(blocks, "\n")
}
```

Delete `peerBudget` (`ui/overview.go:180`) — no longer referenced.

- [ ] **Step 4: Rewrite `overviewData()`** (`app/app.go:2714`) to build multi-group data. Add the fleet ordering helper too:

```go
// fleetSlotOrder returns slot indices in overview display order: focused
// slot first, then the rest alphabetical by workspace name. Stable
// regardless of background-load arrival order.
func (m *home) fleetSlotOrder() []int {
	order := make([]int, 0, len(m.slots))
	if m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		order = append(order, m.focusedSlot)
	}
	rest := make([]int, 0, len(m.slots))
	for i := range m.slots {
		if i != m.focusedSlot {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool {
		return m.slots[rest[a]].wsCtx.Name < m.slots[rest[b]].wsCtx.Name
	})
	return append(order, rest...)
}

func (m *home) overviewData() ui.OverviewData {
	slotOrder := m.fleetSlotOrder()
	groups := make([]ui.OverviewGroup, 0, len(slotOrder)+len(m.fleetLoadErrors)+len(m.fleetLoading))
	cursor := ui.OverviewCursor{}
	for _, si := range slotOrder {
		slot := m.slots[si]
		var list *ui.List
		if si == m.focusedSlot {
			list = m.list
		} else {
			list = slot.list
		}
		items := list.GetInstances()
		g := ui.OverviewGroup{Name: m.slotGroupName(slot), Items: items, State: ui.GroupLoaded}
		if len(items) == 0 {
			g.State = ui.GroupEmpty
		} else {
			g.Order = ui.SortForOverview(items)
		}
		// Translate the domain cursor (slot,inst) → render cursor
		// (group,item) when this is the cursor's slot.
		if si == m.overviewCursor.slot {
			for pos, idx := range g.Order {
				if idx == m.overviewCursor.inst {
					cursor = ui.OverviewCursor{Group: len(groups), Item: pos}
					break
				}
			}
		}
		groups = append(groups, g)
	}
	// Loading + errored (not-yet-slot) workspaces as trailing groups,
	// alphabetical.
	extras := make([]ui.OverviewGroup, 0)
	for name := range m.fleetLoading {
		extras = append(extras, ui.OverviewGroup{Name: name, State: ui.GroupLoading})
	}
	for name, err := range m.fleetLoadErrors {
		extras = append(extras, ui.OverviewGroup{Name: name, State: ui.GroupError, Err: err.Error()})
	}
	sort.SliceStable(extras, func(a, b int) bool { return extras[a].Name < extras[b].Name })
	groups = append(groups, extras...)

	return ui.OverviewData{Groups: groups, Cursor: cursor, Spinner: m.spinner.View()}
}

// slotGroupName is the display name for a slot's overview group,
// falling back to "global" for the unnamed classic slot.
func (m *home) slotGroupName(slot workspaceSlot) string {
	if slot.wsCtx != nil && slot.wsCtx.Name != "" {
		return slot.wsCtx.Name
	}
	return "global"
}
```

Ensure `sort` is imported in `app/app.go` (`grep -n '"sort"' app/app.go`; add if missing). Remove the now-unused `overviewGroupName`/`peerSectionFor`-for-overview only if nothing else references them — `overviewGroupName` is still used by the `z` collapse handler (`app/state_default.go:57`); keep it. `peerSectionFor` is still used by `refreshPeerSections` (rail); keep it.

- [ ] **Step 5: Write the app-side test** (`app/overview_data_test.go`, new):

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/assert"
)

func TestOverviewData_GroupsFocusedFirstThenAlpha(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	mk := func(title string) *ui.List { l := ui.NewList(&s); l.AddInstance(&session.Instance{Title: title, Status: session.Ready}); return l }
	focused := mk("f1")
	m := &home{
		spinner:     s,
		list:        focused,
		focusedSlot: 1,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "zebra"}, list: mk("z1"), background: true},
			{wsCtx: &config.WorkspaceContext{Name: "focused"}, list: focused},
			{wsCtx: &config.WorkspaceContext{Name: "apple"}, list: mk("a1"), background: true},
		},
		fleetLoading:    map[string]bool{"loadingws": true},
		fleetLoadErrors: map[string]error{"errws": assertErr},
	}
	d := m.overviewData()
	names := make([]string, len(d.Groups))
	states := make([]ui.GroupState, len(d.Groups))
	for i, g := range d.Groups {
		names[i], states[i] = g.Name, g.State
	}
	// focused first, then alpha (apple, zebra), then extras alpha (errws, loadingws).
	assert.Equal(t, []string{"focused", "apple", "zebra", "errws", "loadingws"}, names)
	assert.Equal(t, ui.GroupError, states[3])
	assert.Equal(t, ui.GroupLoading, states[4])
}
```

- [ ] **Step 6: Fix pre-existing ui overview tests** — the old tests reference `OverviewData{ActiveName, Items, Order, SelectedIdx, Peers}`. Update each to the new `Groups`/`Cursor` shape. Run `CGO_ENABLED=0 go test ./ui/ 2>&1 | head -40` and fix every compile error; keep structural assertions, migrate field names.

- [ ] **Step 7: Run both packages** — `CGO_ENABLED=0 go test ./ui/ ./app/ 2>&1 | tail -20` — Expected: PASS. **Build all** — `CGO_ENABLED=0 go build ./...`.
- [ ] **Step 8: Commit**

```bash
git add ui/overview.go ui/overview_test.go app/app.go app/overview_data_test.go
git commit -m "feat(overview): multi-group fleet data model and rendering

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 7: Combined vertical windowing across groups

**Files:**
- Modify: `ui/overview.go` (`window`, add `rowOffset`-style state)
- Test: `ui/overview_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestOverview_WindowKeepsCursorGroupVisible(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, overviewCardHeight+4) // room for ~1 card row + a header
	// Two loaded groups, each with 3 cards; cursor deep in the SECOND
	// group must be visible (the first group scrolls off the top).
	mkItems := func(p string) ([]*session.Instance, []int) {
		its := []*session.Instance{
			{Title: p + "1", Status: session.Ready},
			{Title: p + "2", Status: session.Ready},
			{Title: p + "3", Status: session.Ready},
		}
		return its, []int{0, 1, 2}
	}
	i1, o1 := mkItems("g")
	i2, o2 := mkItems("h")
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "one", State: GroupLoaded, Items: i1, Order: o1},
			{Name: "two", State: GroupLoaded, Items: i2, Order: o2},
		},
		Cursor: OverviewCursor{Group: 1, Item: 2}, // last card of second group
	}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "h3", "cursor card is within the visible window")
	// Height budget respected.
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), overviewCardHeight+4)
}
```

- [ ] **Step 2: Run** — Expected: FAIL (cursor card scrolled off / height blown).

- [ ] **Step 3: Implement combined windowing**

Replace the placeholder `window` with a line-based combined window. Because each group block is a string of known line count, and the cursor's absolute line range is computable, scroll by whole lines so the cursor's card rows stay visible:

```go
// window vertically scrolls the joined group blocks so the cursor card's
// line range stays visible within o.height. Mutates o.rowOffset exactly
// once per render pass (same discipline as the old single-group grid).
func (o *Overview) window(blocks []string, d OverviewData) string {
	joined := strings.Join(blocks, "\n")
	lines := strings.Split(joined, "\n")
	budget := o.height
	if len(lines) <= budget {
		o.rowOffset = 0
		return joined
	}
	// Absolute line span of the cursor card.
	top, bottom := o.cursorLineSpan(blocks, d)
	if top < o.rowOffset {
		o.rowOffset = top
	}
	if bottom >= o.rowOffset+budget {
		o.rowOffset = bottom - budget + 1
	}
	if o.rowOffset > len(lines)-budget {
		o.rowOffset = len(lines) - budget
	}
	if o.rowOffset < 0 {
		o.rowOffset = 0
	}
	end := o.rowOffset + budget
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[o.rowOffset:end], "\n")
}

// cursorLineSpan returns the absolute [top,bottom] line indices (into the
// joined blocks) of the cursor's card. Each group block is 1 header line
// plus its body; a card row is overviewCardHeight lines. Groups are
// separated by one join newline (already part of the block boundaries).
func (o *Overview) cursorLineSpan(blocks []string, d OverviewData) (int, int) {
	line := 0
	cols := overviewColumns(o.width)
	for gi, b := range blocks {
		blockLines := strings.Count(b, "\n") + 1
		if gi == d.Cursor.Group {
			cardRow := d.Cursor.Item / cols
			top := line + 1 + cardRow*overviewCardHeight // +1 skips the header
			return top, top + overviewCardHeight - 1
		}
		line += blockLines
	}
	return 0, 0
}
```

Note: the `+1` header offset assumes non-collapsed loaded groups render `header\n<grid>`; collapsed/loading/error/empty groups can't hold the cursor (nav skips them, Task 9), so the cursor group is always a loaded grid with a header line. Add the `rowOffset int` field if not already present (it exists on `Overview` at `ui/overview.go:43`).

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./ui/ -run 'TestOverview_Window|TestOverview_Renders' -v` — Expected: PASS. Run the whole ui suite: `CGO_ENABLED=0 go test ./ui/`.
- [ ] **Step 5: Commit**

```bash
git add ui/overview.go ui/overview_test.go
git commit -m "feat(overview): combined vertical windowing keeps fleet cursor visible

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

# Phase D — Global cursor + navigation

### Task 9: Global `moveCursor` + cursor seeding

**Files:**
- Modify: `app/app.go` (`moveCursor` ~2736; replace the Task-5 `seedOverviewCursor` stub)
- Test: `app/overview_cursor_test.go` (new)

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleetSlot builds a fully-wired workspaceSlot (tempdir storage/state,
// own list + splitPane) so loadSlot/saveCurrentSlot never nil-deref.
// Shared across the fleet nav/teardown tests.
func fleetSlot(t *testing.T, name string, titles ...string) workspaceSlot {
	t.Helper()
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s)
	for _, ti := range titles {
		list.AddInstance(&session.Instance{Title: ti, Status: session.Ready})
	}
	dir := t.TempDir()
	st := config.LoadStateFrom(dir)
	stor, err := session.NewStorage(st, dir)
	require.NoError(t, err)
	return workspaceSlot{
		wsCtx:     &config.WorkspaceContext{Name: name, ConfigDir: dir},
		list:      list,
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:   stor,
		appConfig: config.DefaultConfig(),
		appState:  st,
	}
}

// fleetHome wires a focused slot ("afocus", f1/f2) and a background peer
// ("bpeer", b1), with home's active fields hoisted from the focused slot.
func fleetHome(t *testing.T) *home {
	t.Helper()
	focus := fleetSlot(t, "afocus", "f1", "f2")
	peer := fleetSlot(t, "bpeer", "b1")
	peer.background = true
	m := &home{
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		viewMode:    viewOverview,
		focusedSlot: 0,
		overview:    ui.NewOverview(), // fleetOrder() reads m.overview.IsCollapsed
		tabBar:      ui.NewWorkspaceTabBar(),
		registry:    &config.WorkspaceRegistry{},
		slots:       []workspaceSlot{focus, peer},
		// Hoist focused slot onto home (what loadSlot/saveCurrentSlot expect).
		list:      focus.list,
		splitPane: focus.splitPane,
		storage:   focus.storage,
		appConfig: focus.appConfig,
		appState:  focus.appState,
	}
	m.seedOverviewCursor()
	return m
}

func TestMoveCursor_CrossesGroupBoundary(t *testing.T) {
	m := fleetHome(t)
	// Start at focused slot inst 0. Two forward steps: f2, then into bpeer/b1.
	assert.Equal(t, overviewCursor{slot: 0, inst: 0}, m.overviewCursor)
	m.moveCursor(1)
	assert.Equal(t, overviewCursor{slot: 0, inst: 1}, m.overviewCursor)
	m.moveCursor(1)
	assert.Equal(t, overviewCursor{slot: 1, inst: 0}, m.overviewCursor, "crossed into peer group")
	// Does not fall off the end.
	m.moveCursor(1)
	assert.Equal(t, overviewCursor{slot: 1, inst: 0}, m.overviewCursor)
	// Backward returns.
	m.moveCursor(-1)
	assert.Equal(t, overviewCursor{slot: 0, inst: 1}, m.overviewCursor)
}
```

- [ ] **Step 2: Run** — Expected: FAIL (`moveCursor` still list-scoped; cursor unchanged across groups).

- [ ] **Step 3: Implement**

Add a fleet-position flattener and rewrite `moveCursor`:

```go
// fleetPos is one selectable card in fleet display order.
type fleetPos struct{ slot, inst int }

// fleetOrder flattens all loaded, non-collapsed slots into a single
// display-ordered, attention-sorted list of selectable positions,
// skipping Deleting instances. Mirrors overviewData's grouping so cursor
// motion matches what's on screen.
func (m *home) fleetOrder() []fleetPos {
	var out []fleetPos
	for _, si := range m.fleetSlotOrder() {
		if m.overview.IsCollapsed(m.slotGroupName(m.slots[si])) {
			continue
		}
		list := m.slots[si].list
		if si == m.focusedSlot {
			list = m.list
		}
		items := list.GetInstances()
		for _, idx := range ui.SortForOverview(items) {
			if items[idx].GetStatus() == session.Deleting {
				continue
			}
			out = append(out, fleetPos{slot: si, inst: idx})
		}
	}
	return out
}

// seedOverviewCursor points the cursor at the focused slot's selection,
// or the first selectable fleet position if that isn't selectable.
func (m *home) seedOverviewCursor() {
	m.overviewCursor = overviewCursor{slot: m.focusedSlot, inst: m.list.SelectedIdx()}
	order := m.fleetOrder()
	for _, p := range order {
		if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
			return
		}
	}
	if len(order) > 0 {
		m.overviewCursor = overviewCursor{slot: order[0].slot, inst: order[0].inst}
	}
}

// moveCursor advances selection: list order in focus mode, fleet display
// order (across all groups) in overview mode. No wrap in the grid.
func (m *home) moveCursor(dir int) {
	if m.viewMode != viewOverview {
		if dir < 0 {
			m.list.Up()
		} else {
			m.list.Down()
		}
		return
	}
	order := m.fleetOrder()
	if len(order) == 0 {
		return
	}
	cur := 0
	for i, p := range order {
		if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
			cur = i
			break
		}
	}
	np := cur + dir
	if np < 0 || np >= len(order) {
		return
	}
	m.overviewCursor = overviewCursor{slot: order[np].slot, inst: order[np].inst}
}
```

Replace the Task-5 `seedOverviewCursor` stub with this full version.

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./app/ -run TestMoveCursor -v` — Expected: PASS. **Build**.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/overview_cursor_test.go
git commit -m "feat(app): global fleet cursor across overview groups

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 10: `focusCursorSlot` primitive

**Files:**
- Modify: `app/app.go`
- Test: `app/overview_cursor_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestFocusCursorSlot_PromotesAndFocuses(t *testing.T) {
	m := fleetHome(t)
	m.registry = &config.WorkspaceRegistry{}
	m.tabBar = ui.NewWorkspaceTabBar()
	// Cursor on the background peer slot.
	m.overviewCursor = overviewCursor{slot: 1, inst: 0}

	m.focusCursorSlot()
	assert.Equal(t, 1, m.focusedSlot, "focus moved to cursor slot")
	assert.False(t, m.slots[1].background, "background slot promoted on focus")
	assert.Equal(t, m.slots[1].list, m.list, "focused list hoisted")
	assert.Equal(t, 0, m.list.SelectedIdx(), "cursor instance selected")
}
```

`fleetHome` already wires `tabBar`, `registry`, and fully-built slots (Task 9), so `promoteSlot`/`loadSlot` run without nil-derefs — no extra setup needed here.

- [ ] **Step 2: Run** — Expected: FAIL (undefined `focusCursorSlot`).

- [ ] **Step 3: Implement**

```go
// focusCursorSlot makes the overview cursor's slot the focused slot,
// promoting it out of background first, and selects the cursor's
// instance. No-op fast path when already focused. The single primitive
// every cursor-committing overview action routes through, so they reuse
// focus-mode intents unchanged. Main-goroutine only.
func (m *home) focusCursorSlot() {
	c := m.overviewCursor
	if c.slot < 0 || c.slot >= len(m.slots) {
		return
	}
	if c.slot != m.focusedSlot {
		m.saveCurrentSlot()
		if m.slots[c.slot].background {
			m.promoteSlot(c.slot)
		}
		m.loadSlot(c.slot)
	}
	if c.inst >= 0 && c.inst < len(m.list.GetInstances()) {
		m.list.SetSelectedInstance(c.inst)
	}
}
```

- [ ] **Step 4: Run** — Expected: PASS. **Build**.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/overview_cursor_test.go
git commit -m "feat(app): focusCursorSlot primitive for cross-workspace actions

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 11: Route overview `enter`/`D`/`r`/`R`/`n`/`N` through `focusCursorSlot`

**Files:**
- Modify: `app/state_default.go` (overview branch ~48–80)
- Test: `app/overview_nav_test.go` (new)

- [ ] **Step 1: Write the failing test** — drive the overview key handler and assert focus moves to the cursor's slot before the action:

```go
package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestOverviewEnter_FocusesCursorSlotThenFocusMode(t *testing.T) {
	m := fleetHome(t)
	m.registry = &config.WorkspaceRegistry{}
	m.tabBar = ui.NewWorkspaceTabBar()
	m.scripts = nil // enter/esc handled before dispatch; engine unused here
	m.overviewCursor = overviewCursor{slot: 1, inst: 0}

	_, _ = handleStateDefaultKey(m, keyPress("enter"))
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Equal(t, 1, m.focusedSlot, "enter committed the cross-workspace cursor")
}

// keyPress builds a KeyPressMsg for a single key string. Mirror however
// sibling tests construct one (grep -n "KeyPressMsg{" app/*_test.go).
func keyPress(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s, Code: rune(s[0])}
}
```

Verify `keyPress`/`keyMsg` against existing app tests (`grep -n "KeyPressMsg{" app/*_test.go`) and reuse the established helper rather than redefining if one exists (avoid a duplicate-symbol build error).

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./app/ -run TestOverviewEnter -v` — Expected: FAIL (enter currently just flips viewMode without focusing the cursor slot).

- [ ] **Step 3: Implement** — in `app/state_default.go`'s `m.viewMode == viewOverview` switch (`:48`):

Change `enter`:

```go
		case "enter":
			m.focusCursorSlot()
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			return m, m.instanceChanged()
```

Change `n`/`N`:

```go
		case "n", "N":
			m.focusCursorSlot()
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			cmds := []tea.Cmd{m.instanceChanged()}
			if cmd, handled := m.dispatchScript(msg.String()); handled {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
```

For `D`/`r`/`R` (currently they fall through the whitelist to `dispatchScript`), intercept them in the overview switch to focus the cursor slot first, then dispatch and re-clamp the cursor. Add before the `overviewKeyAllowed` gate:

```go
		case "D", "r", "R":
			m.focusCursorSlot()
			cmd, _ := m.dispatchScript(msg.String())
			m.seedOverviewCursor() // re-clamp: the target may be gone/kill-pending
			return m, cmd
```

(`esc`/`z` unchanged.)

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./app/ -run 'TestOverviewEnter|TestMoveCursor|TestFocusCursor' -v` — Expected: PASS. **Build**.
- [ ] **Step 5: Commit**

```bash
git add app/state_default.go app/overview_nav_test.go
git commit -m "feat(app): cross-workspace enter/D/r/n via focusCursorSlot in overview

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 12: Fleet-wide `jumpWaiting` (`]`/`[`)

**Files:**
- Modify: `app/app.go` (`jumpWaiting` ~768)
- Test: `app/jump_waiting_fleet_test.go` (new)

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJumpWaiting_CrossesToPeerWorkspace(t *testing.T) {
	focus := fleetSlot(t, "focused", "f-idle") // idle
	peer := fleetSlot(t, "peer")               // one prompting instance below
	peer.background = true
	waiter := &session.Instance{Title: "p-wait", Status: session.Ready}
	require.NoError(t, waiter.TransitionTo(session.Prompting))
	peer.list.AddInstance(waiter)

	m := &home{
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		focusedSlot: 0, registry: &config.WorkspaceRegistry{},
		tabBar: ui.NewWorkspaceTabBar(), overview: ui.NewOverview(),
		slots:     []workspaceSlot{focus, peer},
		list:      focus.list,
		splitPane: focus.splitPane, storage: focus.storage,
		appConfig: focus.appConfig, appState: focus.appState,
	}
	// The only waiting agent is in the background peer workspace.
	m.jumpWaiting(1)
	assert.Equal(t, 1, m.focusedSlot, "focus crossed to the peer workspace")
	assert.False(t, m.slots[1].background, "landing promoted the peer slot")
	assert.Equal(t, "p-wait", m.list.GetSelectedInstance().Title)
}
```

- [ ] **Step 2: Run** — Expected: FAIL (jumpWaiting is single-list; focus stays 0).

- [ ] **Step 3: Implement** — rewrite `jumpWaiting` to walk fleet order:

```go
// jumpWaiting moves selection to the next/prev fleet agent needing
// attention (Prompting or bell), across all workspaces, wrapping. When
// the target is in another slot it saves the current slot, promotes the
// target if background, focuses it, and selects there. No-op when none
// wait. Main-goroutine only.
func (m *home) jumpWaiting(dir int) {
	// Build fleet display order over ALL slots (not just loaded/expanded
	// — waiting agents in collapsed groups are still reachable).
	var order []fleetPos
	for _, si := range m.fleetSlotOrder() {
		list := m.slots[si].list
		if si == m.focusedSlot {
			list = m.list
		}
		items := list.GetInstances()
		for _, idx := range ui.SortForOverview(items) {
			if items[idx].GetStatus() == session.Deleting {
				continue
			}
			order = append(order, fleetPos{slot: si, inst: idx})
		}
	}
	n := len(order)
	if n == 0 {
		return
	}
	// Current position: focused slot's current selection (or -1).
	start := -1
	selIdx := m.list.SelectedIdx()
	for i, p := range order {
		if p.slot == m.focusedSlot && p.inst == selIdx {
			start = i
			break
		}
	}
	if start < 0 {
		start = 0
	}
	for step := 1; step <= n; step++ {
		i := ((start+dir*step)%n + n) % n
		p := order[i]
		list := m.slots[p.slot].list
		if p.slot == m.focusedSlot {
			list = m.list
		}
		inst := list.GetInstances()[p.inst]
		if inst.GetStatus() == session.Prompting || inst.BellPending() {
			if p.slot != m.focusedSlot {
				m.saveCurrentSlot()
				if m.slots[p.slot].background {
					m.promoteSlot(p.slot)
				}
				m.loadSlot(p.slot)
			}
			m.list.SetSelectedInstance(p.inst)
			return
		}
	}
}
```

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./app/ -run TestJumpWaiting -v` — Expected: PASS. Also run the existing single-workspace jump test (`grep -rln "jumpWaiting\|next_waiting" app/*_test.go`) and confirm it still passes (single slot → `fleetSlotOrder` returns `[0]`, behavior identical). **Build**.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/jump_waiting_fleet_test.go
git commit -m "feat(app): fleet-wide jump-to-waiting across workspaces

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

# Phase E — Persistence & teardown edge cases

### Task 13: Close-tab demote + picker promote (fleet-engaged branch)

**Files:**
- Modify: `app/app.go` (`applyWorkspaceToggle` ~2473)
- Test: `app/workspace_toggle_fleet_test.go` (new)

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestApplyWorkspaceToggle_FleetEngagedDemotesInsteadOfTeardown(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{}
	m.tabBar = ui.NewWorkspaceTabBar()
	m.fleetEngaged = true
	// fleetSlot is the shared helper from overview_cursor_test.go (same
	// package): fully-wired slots so demote/loadSlot never nil-deref.
	m.slots = []workspaceSlot{fleetSlot(t, "a"), fleetSlot(t, "b")}
	m.focusedSlot = 0
	m.list = m.slots[0].list

	// Desired = only "a" foreground. "b" must DEMOTE (stay a slot), not be removed.
	_ = m.applyWorkspaceToggle([]config.Workspace{{Name: "a"}})
	assert.Len(t, m.slots, 2, "fleet-engaged: b demoted, not torn down")
	for _, sl := range m.slots {
		if sl.wsCtx.Name == "b" {
			assert.True(t, sl.background, "b is now background")
		}
	}
	assert.Equal(t, []string{"a"}, m.foregroundSlotNames())
}
```

`fleetSlot(t, name, titles...)` is the shared helper defined in `app/overview_cursor_test.go` (Task 9) — a fully-wired slot (tempdir `storage`/`appState`, own `list`/`splitPane`) so `loadSlot`/`demote` never panic. It's visible here because all `app/*_test.go` share the package.

- [ ] **Step 2: Run** — Expected: FAIL (current toggle deactivates non-desired slots).

- [ ] **Step 3: Implement** — in `applyWorkspaceToggle`, gate the activate/deactivate diff on `m.fleetEngaged`. Replace the deactivation loop (step 2, `app/app.go:2510-2520`) and promotion of desired:

```go
	if m.fleetEngaged {
		// Fleet mode: every registered workspace is already (or will be) a
		// slot. The picker only chooses which are foreground tabs — promote
		// desired, demote the rest. Never tear down (agents stay live).
		for i := range m.slots {
			if desiredNames[m.slots[i].wsCtx.Name] {
				if m.slots[i].background {
					m.promoteSlot(i)
				}
			} else if i != m.focusedSlot {
				m.demoteSlot(i)
			}
		}
		// If the focused slot got dropped from desired, move focus to the
		// first desired foreground slot before it too becomes background.
		if !desiredNames[m.slots[m.focusedSlot].wsCtx.Name] {
			for i := range m.slots {
				if desiredNames[m.slots[i].wsCtx.Name] {
					m.saveCurrentSlot()
					m.loadSlot(i)
					break
				}
			}
			// Now demote the previously-focused slot if still non-desired.
			for i := range m.slots {
				if i != m.focusedSlot && !desiredNames[m.slots[i].wsCtx.Name] {
					m.demoteSlot(i)
				}
			}
		}
	} else {
		// Pre-fleet behavior: activate new, deactivate missing (existing
		// step-1/step-2 code stays here unchanged).
		// ... existing activation loop (2501-2508) ...
		// ... existing deactivation loop (2513-2520) ...
	}
```

Keep the existing step-1 activation loop and step-2 deactivation loop verbatim inside the `else`. Read the surrounding function and splice carefully so the `activationErrors`/`deactivationErrors` handling below still compiles (in the fleet branch those slices stay empty).

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./app/ -run TestApplyWorkspaceToggle -v` — Expected: PASS. Run the existing workspace-toggle tests too and keep them green (they run with `fleetEngaged == false`). **Build**.
- [ ] **Step 5: Commit**

```bash
git add app/app.go app/workspace_toggle_fleet_test.go
git commit -m "feat(app): fleet-engaged tab toggle demotes/promotes instead of teardown

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

### Task 14: Quit saves background slots (verify + pin)

**Files:**
- Modify: `app/app.go` (`handleQuit` ~1704 — likely already correct)
- Test: `app/fleet_load_test.go` (append)

- [ ] **Step 1: Read `handleQuit`** (`app/app.go:1700-1730`). The save loop iterates `m.slots` (all of them, background included) and `SaveInstances` each — so background slots are already saved, and `saveOpenWorkspaces` (now foreground-only from Task 1) already excludes them. This task pins that with a test; no code change expected unless the read shows a `background` filter (there is none).

- [ ] **Step 2: Write the pinning test**

```go
func TestHandleQuit_SavesBackgroundSlots(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{}
	fg := fleetSlot(t, "fg")
	bg := fleetSlot(t, "bg")
	bg.background = true
	bg.list.AddInstance(&session.Instance{Title: "bgsess", Status: session.Ready})
	m.slots = []workspaceSlot{fg, bg}
	m.focusedSlot = 0
	m.list = fg.list

	_, _ = m.handleQuit()

	// Reload bg's storage from disk and confirm the instance persisted.
	reloaded, err := bg.storage.LoadInstanceData() // session/storage.go:288
	require.NoError(t, err)
	titles := make([]string, 0, len(reloaded))
	for _, d := range reloaded {
		titles = append(titles, d.Title)
	}
	assert.Contains(t, titles, "bgsess", "background slot persisted on quit")
}
```

`Storage.LoadInstanceData()` returns `[]session.InstanceData` (whose `Title` field carries the session title). `persistableInstances` filters Recoverable, not background, so a Ready instance persists. Confirm `InstanceData.Title` exists (`grep -n "Title" session/storage.go` near the `InstanceData` struct); it does.

- [ ] **Step 3: Run** — `CGO_ENABLED=0 go test ./app/ -run TestHandleQuit_SavesBackground -v` — Expected: PASS (no code change) OR FAIL if a filter exists → then remove any `background` filtering from the save loop so all slots save.
- [ ] **Step 4: Commit** (test-only, or with the fix if needed)

```bash
git add app/app.go app/fleet_load_test.go
git commit -m "test(app): pin that quit persists background fleet slots

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

# Phase F — Integration, regression, race, manual

### Task 15: Full regression, race, format, manual smoke

**Files:** none (verification only)

- [ ] **Step 1: Format + vet** — `gofmt -w . && go vet ./...` — Expected: no diffs after commit, vet clean.
- [ ] **Step 2: Full unit suite** — `CGO_ENABLED=0 go test ./... 2>&1 | tail -30` — Expected: all PASS. Pay attention to `ui/overview_test.go`, `app/status_redetect_test.go`, `ui/scroll_test.go`, `ui/pane_border_test.go`, `app/peer_sections_test.go` (rail unaffected).
- [ ] **Step 3: Race** — `CC=clang CGO_ENABLED=1 go test -race ./app/ ./ui/ ./session/... 2>&1 | tail -20` — Expected: no data races. Specifically exercises fleet-load Cmds attaching PTYs while a pump writes and overview rendering reading peer slots' emulators.
- [ ] **Step 4: Build the binary** — `CGO_ENABLED=0 go build -o loom` — Expected: success.
- [ ] **Step 5: Manual smoke** (document results in the commit body):
  - Register 3+ workspaces, each with ≥1 session; start Loom in one.
  - Press `tab` → overview: groups appear; peers fill in progressively (loading→loaded); no multi-second freeze.
  - A prompt-waiting agent in a non-focused workspace shows gold in its group.
  - `j/k` moves across group boundaries; `enter` on a peer card switches into that workspace in focus mode (and it now shows as a tab).
  - In focus mode, `]` reaches a waiting agent in another workspace (switching focus).
  - Overview `D` on a peer card kills that workspace's session (verify it disappears from that group, not the focused one).
  - Confirm NO workspace terminal was auto-spawned in the background-loaded repos (check tmux: `tmux ls | grep loom_`).
  - Open the picker, uncheck a workspace → its tab disappears but its agents stay live in the overview (demoted, not killed).
  - Quit and relaunch → only the workspace(s) you focused eager-load (startup stays lazy); the rest reappear only after you open the overview again.
- [ ] **Step 6: Final commit** (if any format/fixups)

```bash
git add -A
git commit -m "chore(app): phase 4 cross-workspace overview regression pass + smoke notes

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

- [ ] **Step 7: Update CLAUDE.md + memory** — document the fleet-overview behavior (lazy progressive load, background vs foreground slots, `focusCursorSlot`, fleet-wide `]`) in the Overview/Gotchas sections of `CLAUDE.md`, and update the `loom-mission-control-status` memory to mark phase 4 shipped. Commit separately:

```bash
git add CLAUDE.md
git commit -m "docs: cross-workspace fleet overview (phase 4) behavior and gotchas

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Keep the tree green per commit.** The one multi-file compile unit is Task 6+8 (ui shape + app builder) — commit them together. Every other task builds and tests independently.
- **Reuse, don't reinvent.** Cross-workspace actions deliberately route through `focusCursorSlot` + existing focus-mode intents. Do not fork `runKillSelected`/`runResumeOrRecover`.
- **The `+1` header assumption in `cursorLineSpan`** holds only because nav never lets the cursor land on a collapsed/loading/error/empty group. If you add a selectable non-grid group later, revisit it.
- **Deferred (per spec):** concurrent-activation worker cap (measure first); cross-workspace merge/move; per-group scroll independence.
