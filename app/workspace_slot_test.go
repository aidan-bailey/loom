package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// focusSlots installs slots as h's open workspace tabs and focuses
// slots[focused], so the embedded focused slot is the same pointer as
// h.slots[focused] (the invariant checkSlotInvariant enforces). A home
// already given a model (wireCore) gets the model's tabs re-installed to
// mirror the new slots.
func focusSlots(h *home, focused int, slots ...*workspaceSlot) {
	h.slots = slots
	h.focusedSlot = focused
	h.workspaceSlot = slots[focused]
	if h.core != nil {
		var tabs []*core.Workspace
		for _, s := range slots {
			tabs = append(tabs, s.ws())
		}
		testModel(h).SetWorkspacesForTest(tabs...)
	}
}

// focusedName is the focused slot's workspace name ("" for a nil wsCtx).
func focusedName(m *home) string {
	if m.wsCtx() == nil {
		return ""
	}
	return m.wsCtx().Name
}

// TestSlotInvariant_HoldsAcrossSlotLifecycle drives every m.slots
// mutation — activation from classic mode, more activations, a switch,
// closing the focused tab, closing a tab left of focus, and the return to
// global mode — and checks the embedded-focused-slot invariant after each.
func TestSlotInvariant_HoldsAcrossSlotLifecycle(t *testing.T) {
	isolateTmux(t)
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir) // enterGlobalMode's global storage
	m := newRestoreHome(t, &recordingExec{})
	require.NoError(t, m.checkSlotInvariant(), "classic home")
	classic := m.workspaceSlot

	for _, name := range []string{"ws-a", "ws-b", "ws-c"} {
		def := preservedTerminalWorkspace(t, name)
		registerWorkspaces(t, m, def)
		_, err := m.activateWorkspace(def)
		require.NoError(t, err)
		require.NoError(t, m.checkSlotInvariant(), "after activating %s", name)
	}
	require.Len(t, m.slots, 3)
	assert.NotSame(t, classic, m.workspaceSlot, "the first tab opened from classic mode takes focus")
	assert.Equal(t, "ws-a", focusedName(m))

	m.switchWorkspaceSlot(1)
	require.NoError(t, m.checkSlotInvariant(), "after switching")
	require.Equal(t, "ws-b", focusedName(m))

	// Closing the focused tab refocuses the tab that slid into its index.
	// The model keeps serving the closed tab's workspace: only this TUI
	// stopped showing it.
	idB := m.id
	_, err := m.deactivateWorkspace("ws-b")
	require.NoError(t, err)
	require.NoError(t, m.checkSlotInvariant(), "after closing the focused tab")
	assert.Equal(t, []string{"ws-a", "ws-c"}, m.slotNames())
	assert.Equal(t, "ws-c", focusedName(m))
	assert.True(t, m.core.IsLoaded(idB), "the closed tab's workspace is still served")

	// Closing a tab left of focus shifts the index, not the focus.
	_, err = m.deactivateWorkspace("ws-a")
	require.NoError(t, err)
	require.NoError(t, m.checkSlotInvariant(), "after closing a tab left of focus")
	assert.Equal(t, "ws-c", focusedName(m))

	// The last tab only closes through global mode.
	_, err = m.deactivateWorkspace("ws-c")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the last open workspace")
	require.NoError(t, m.checkSlotInvariant(), "after refusing to close the last tab")
	require.Len(t, m.slots, 1)

	_ = m.applyWorkspaceToggle(nil)
	require.NoError(t, m.checkSlotInvariant(), "after returning to global mode")
	assert.Empty(t, m.slots)
	require.NotNil(t, m.wsCtx(), "global mode's slot carries the global context")
	assert.Equal(t, config.WorkspaceContext{ConfigDir: globalDir}, *m.wsCtx())
	assert.NotNil(t, m.workbench, "the global slot keeps a workbench")
	assert.NotNil(t, m.splitPane, "the global slot keeps a split pane")
}

// TestSlotInvariant_ToggleClosingFocusedTabRefocuses covers the picker's
// mid-session toggle closing the focused tab while another stays open.
func TestSlotInvariant_ToggleClosingFocusedTabRefocuses(t *testing.T) {
	isolateTmux(t)
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
	wsA := preservedTerminalWorkspace(t, "ws-a")
	wsB := preservedTerminalWorkspace(t, "ws-b")
	registerWorkspaces(t, m, wsA, wsB)
	_ = m.applyWorkspaceToggle([]config.Workspace{wsA, wsB})
	require.NoError(t, m.checkSlotInvariant())
	require.Equal(t, "ws-a", focusedName(m))

	_ = m.applyWorkspaceToggle([]config.Workspace{wsB})
	require.NoError(t, m.checkSlotInvariant())
	assert.Equal(t, []string{"ws-b"}, m.slotNames())
	assert.Equal(t, "ws-b", focusedName(m))
}

// TestSlotInvariant_ToggleKeepsTabsWhenEveryActivationFails: a toggle
// whose every desired workspace fails to load used to close all the open
// tabs anyway, stranding the user on the last one's state with no tab
// open. The open tabs must survive and the failure be reported.
func TestSlotInvariant_ToggleKeepsTabsWhenEveryActivationFails(t *testing.T) {
	isolateTmux(t)
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
	wsA, bad := preservedTerminalWorkspace(t, "ws-a"), corruptWorkspaces(t, "ws-bad")
	registerWorkspaces(t, m, append(bad, wsA)...)
	_ = m.applyWorkspaceToggle([]config.Workspace{wsA})
	require.Equal(t, []string{"ws-a"}, m.slotNames())

	cmd := m.applyWorkspaceToggle(bad)
	require.NotNil(t, cmd)
	require.NoError(t, m.checkSlotInvariant())
	assert.Equal(t, []string{"ws-a"}, m.slotNames(), "the open tab must survive a toggle that opened nothing")
	assert.Equal(t, "ws-a", focusedName(m))
	assert.Contains(t, m.errBox.String(), "ws-bad")
}

// TestSlotOwnsState_MutationVisibleWithoutSave: the focused slot's state
// is the slot's own, so a write through m.list (or a promoted field
// assignment) is visible through m.slots at once — there is no save step
// to forget.
func TestSlotOwnsState_MutationVisibleWithoutSave(t *testing.T) {
	m := fleetHome(t)
	inst := &session.Instance{Title: "added", Status: session.Ready}
	m.ws().AddForTest(inst)
	m.syncViews()
	assert.Equal(t, idOf(m, inst), titleID(m.slots[0].list, "added"))

	replacement := fixtureList(t)
	m.list = replacement
	assert.Same(t, replacement, m.slots[0].list, "a promoted-field write lands in the focused slot")

	m.switchWorkspaceSlot(1)
	require.NoError(t, m.checkSlotInvariant())
	other := &session.Instance{Title: "peer-added", Status: session.Ready}
	m.ws().AddForTest(other)
	m.syncViews()
	assert.Equal(t, idOf(m, other), titleID(m.slots[1].list, "peer-added"))
	assert.Nil(t, m.slots[0].list.GetInstanceByTitle("peer-added"), "the other slot is untouched")
}

// TestClassicSlot_NonNilAndOutsideSlots: classic startup focuses a slot
// that is not an open tab, and a restore where every workspace failed
// keeps the classic slot focused.
func TestClassicSlot_NonNilAndOutsideSlots(t *testing.T) {
	isolateTmux(t)
	t.Setenv("LOOM_HOME", t.TempDir())
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	t.Run("classic startup", func(t *testing.T) {
		// A nameless startup context is global startup's: its name is all
		// newHome reads. The "true" program is no Claude: the boot probes
		// no auth.
		m, err := newHome(context.Background(), &config.WorkspaceContext{}, nil, "true", "", true)
		require.NoError(t, err)
		t.Cleanup(m.stopCore)
		require.NotNil(t, m.workspaceSlot)
		assert.Empty(t, m.slots)
		assert.NotNil(t, m.list)
		assert.NotNil(t, m.splitPane)
		assert.NotNil(t, m.workbench)
		require.NoError(t, m.checkSlotInvariant())
	})

	t.Run("restore fallback keeps the classic slot", func(t *testing.T) {
		m, _ := restoreModeHome(t, &recordingExec{}, `[]`, corruptWorkspaces(t, "ws-bad")...)
		require.Empty(t, m.slots)
		require.NotNil(t, m.workspaceSlot)
		assert.Empty(t, m.name(), "the classic slot shows the global workspace")
		require.NoError(t, m.checkSlotInvariant())
	})
}

// failingInstanceStorage refuses every save, standing in for a full or
// read-only disk.
type failingInstanceStorage struct{ calls int }

func (f *failingInstanceStorage) SaveInstances(json.RawMessage) error {
	f.calls++
	return errors.New("disk full")
}
func (f *failingInstanceStorage) GetInstances() json.RawMessage { return nil }
func (f *failingInstanceStorage) DeleteAllInstances() error     { return nil }

// deadTmuxExec records every command like recordingExec, but reports every
// tmux session dead (has-session fails), so a load of Running records
// crash-restarts them.
type deadTmuxExec struct{ recordingExec }

func (d *deadTmuxExec) Run(c *exec.Cmd) error {
	d.record(c)
	if containsHasSession(c) {
		return exec.ErrNotFound
	}
	return nil
}

// scriptSlotTestScript creates an instance, and in Y also switches to the
// next workspace from inside the same handler, after the instance exists;
// W switches before creating it, so its result carries both.
const scriptSlotTestScript = `
cs.bind("X", function(ctx)
  ctx:new_instance{title = "made"}
end)
cs.bind("Y", function(ctx)
  ctx:new_instance{title = "made"}
  cs.actions.workspace_next()
end)
cs.bind("W", function(ctx)
  cs.actions.workspace_next()
  ctx:new_instance{title = "made"}
end)
`

func newScriptSlotHome(t *testing.T) *home {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slots.lua"), []byte(scriptSlotTestScript), 0o644))
	m := fleetHome(t)
	m.viewMode = viewFocus
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(400, 1)
	initScriptsIn(m, dir, false)
	require.True(t, m.scripts.HasAction("X"))
	return m
}

// runScript dispatches key, lets between run on the Update goroutine
// while the script is in flight, then delivers the result and pumps what
// follows: the requests its intents make, their Replies and the resumes
// (pumpRequests).
func runScript(t *testing.T, m *home, key string, between func()) {
	t.Helper()
	cmd, ok := m.dispatchScript(key)
	require.True(t, ok)
	if between != nil {
		between()
	}
	msg := cmd()
	done, ok := msg.(scriptDoneMsg)
	require.True(t, ok, "got %T", msg)
	require.NoError(t, done.err)
	pumpRequests(t, m, func() tea.Msg { return done })
}

// TestScriptDone_DropsInstanceWhenFocusChangedMidDispatch: ctx:new_instance
// builds its instance from the dispatch-time slot (its repo path).
// handleScriptDone used to add it to whichever slot was focused when the
// result arrived, so switching workspace while a script ran filed one
// workspace's session under another. It creates in the dispatch-time slot,
// and is refused (it raises) when focus moved before its result landed.
func TestScriptDone_DropsInstanceWhenFocusChangedMidDispatch(t *testing.T) {
	t.Run("control: focus unchanged, instance adopted", func(t *testing.T) {
		m := newScriptSlotHome(t)
		runScript(t, m, "X", nil)
		assert.NotNil(t, m.slots[0].list.GetInstanceByTitle("made"))
	})

	t.Run("focus switched mid-dispatch: dropped with a notice", func(t *testing.T) {
		m := newScriptSlotHome(t)
		runScript(t, m, "X", func() { m.switchWorkspaceSlot(1) })
		require.NoError(t, m.checkSlotInvariant())
		assert.Equal(t, "bpeer", focusedName(m), "the deferred UI change still applies")
		assert.Nil(t, m.slots[0].list.GetInstanceByTitle("made"))
		assert.Nil(t, m.slots[1].list.GetInstanceByTitle("made"), "must not land in the newly focused workspace")
		assert.Contains(t, m.errBox.String(), "made")
	})

	t.Run("script switches itself: instance stays in its own workspace", func(t *testing.T) {
		m := newScriptSlotHome(t)
		runScript(t, m, "Y", nil)
		assert.Equal(t, "bpeer", focusedName(m), "the script's own workspace_next applies")
		assert.NotNil(t, m.slots[0].list.GetInstanceByTitle("made"), "adopted by the slot it was built for")
		assert.Nil(t, m.slots[1].list.GetInstanceByTitle("made"))
	})

	t.Run("script switches before creating: instance stays in its own workspace", func(t *testing.T) {
		m := newScriptSlotHome(t)
		runScript(t, m, "W", nil)
		assert.Equal(t, "bpeer", focusedName(m), "the script's own workspace_next applies")
		assert.NotNil(t, m.slots[0].list.GetInstanceByTitle("made"),
			"created in the slot it was built for: the switch is the script's own, decided on before it applies")
		assert.Nil(t, m.slots[1].list.GetInstanceByTitle("made"))
	})

	t.Run("a resume is stamped with the slot its snapshot was taken on", func(t *testing.T) {
		m := newScriptSlotHome(t)
		cmd := m.handleScriptResume(scriptResumeMsg{id: script.NewIntentID()})
		resumeSlot := m.workspaceSlot
		m.switchWorkspaceSlot(1)
		done, ok := cmd().(scriptDoneMsg)
		require.True(t, ok)
		assert.Same(t, resumeSlot, done.slot)
	})
}

// TestStartupPicker_FlushesPendingRatiosIntoClassicState: the startup
// picker focused its new tab via loadSlot without flushing pending
// split-ratio saves, so the throttle tick later drained the classic
// slot's title→ratio into the workspace's state.json — a durable wrong
// ratio whenever the two share a session title.
func TestStartupPicker_FlushesPendingRatiosIntoClassicState(t *testing.T) {
	isolateTmux(t)
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
	m.ws().AddForTest(&session.Instance{Title: "main", Status: session.Running})
	m.syncViews()
	m.list.SetSelectedInstance(0)
	m.pendingRatioSaves = map[string]float64{"main": 0.4}
	classicState := m.appState()

	ws := preservedTerminalWorkspace(t, "ws-a")
	registerWorkspaces(t, m, ws)
	m.setOverlay(overlay.NewStartupWorkspacePicker([]config.Workspace{ws}), overlayWorkspacePickerStartup)
	m.state = stateWorkspace
	_, _ = handleStateWorkspaceKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, []string{"ws-a"}, m.slotNames())
	require.NoError(t, m.checkSlotInvariant())
	assert.Empty(t, m.pendingRatioSaves, "the switch must flush pending ratios")
	assert.Equal(t, 0.4, classicState.GetUIPrefs().SplitRatios["main"], "flushed into the departing (classic) state")
	_, leaked := m.appState().GetUIPrefs().SplitRatios["main"]
	assert.False(t, leaked, "the new workspace's state must not receive the classic ratio")
}

// twoTabsWithPendingRatio opens tabs ws-a and ws-b (ws-a focused), adds a
// "main" session to ws-a and records a pending split ratio for it, as a
// resize just before a transition leaves one.
func twoTabsWithPendingRatio(t *testing.T) (*home, config.Workspace) {
	t.Helper()
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	m := newRestoreHome(t, &recordingExec{})
	a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
	registerWorkspaces(t, m, a, b)
	_, err := m.activateWorkspace(a)
	require.NoError(t, err)
	_, err = m.activateWorkspace(b)
	require.NoError(t, err)
	m.loadSlot(0)
	require.Equal(t, "ws-a", m.name())
	m.ws().AddForTest(&session.Instance{Title: "main", Status: session.Running})
	m.syncViews()
	m.pendingRatioSaves = map[string]float64{"main": 0.4}
	return m, a
}

// TestCloseFocusedTab_FlushesPendingRatiosIntoItsState: closing the focused
// tab flushes its pending ratio into its own state.json, not into that of
// the tab that takes focus.
func TestCloseFocusedTab_FlushesPendingRatiosIntoItsState(t *testing.T) {
	m, a := twoTabsWithPendingRatio(t)

	_, err := m.deactivateWorkspace("ws-a")
	require.NoError(t, err)

	assert.Empty(t, m.pendingRatioSaves)
	saved := config.LoadStateFrom(config.WorkspaceConfigDir(&a)).GetUIPrefs().SplitRatios
	assert.InDelta(t, 0.4, saved["main"], 0, "flushed into the closed tab's state")
}

// TestEnterGlobalMode_FlushesPendingRatiosIntoTheTabsState: the same for
// leaving every tab for global mode.
func TestEnterGlobalMode_FlushesPendingRatiosIntoTheTabsState(t *testing.T) {
	m, a := twoTabsWithPendingRatio(t)

	_ = m.enterGlobalMode()

	require.Empty(t, m.slots, "now in global mode")
	assert.Empty(t, m.pendingRatioSaves)
	saved := config.LoadStateFrom(config.WorkspaceConfigDir(&a)).GetUIPrefs().SplitRatios
	assert.InDelta(t, 0.4, saved["main"], 0, "flushed into the departing tab's state")
}

// TestActivateWorkspace_ARenamedWorkspaceIsRefused: renamed while loom
// runs (`loom workspace rename`), a workspace stays served under the name
// it had when the model first served it. The TUI keys its tabs and the open
// list on names, so a tab opened for the new name would show the old one,
// be closed by the next picker commit and be dropped from the open list.
// Opening it by its new name is refused, naming both, with nothing opened
// or persisted, until a restart serves it under its new name.
func TestActivateWorkspace_ARenamedWorkspaceIsRefused(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	old, other := preservedTerminalWorkspace(t, "ws-old"), preservedTerminalWorkspace(t, "ws-other")
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add(old.Name, old.Path))
	require.NoError(t, reg.Add(other.Name, other.Path))
	m := newRestoreHome(t, &recordingExec{})
	m.ctx = cancelledCtx()
	_, ok := m.servedNamed("ws-old")
	require.True(t, ok, "fixture: the model serves it from boot")
	_ = m.applyWorkspaceToggle([]config.Workspace{other})
	require.Equal(t, []string{"ws-other"}, m.slotNames())

	renamer, err := config.LoadWorkspaceRegistry() // another process's
	require.NoError(t, err)
	require.NoError(t, renamer.Rename("ws-old", "ws-new"))
	require.NoError(t, m.core.ReloadRegistry(), "the picker rereads the registry before it lists it")
	renamed := config.Workspace{Name: "ws-new", Path: old.Path}

	_, err = m.activateWorkspace(renamed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ws-new is the workspace loom serves as ws-old")
	assert.Contains(t, err.Error(), "restart loom", "a rename is not a twin: ws-old is no longer registered")
	assert.Equal(t, []string{"ws-other"}, m.slotNames(), "no tab opens")

	// A picker commit that keeps ws-other and checks ws-new opens nothing
	// more, and keeps the tab it kept.
	_ = m.applyWorkspaceToggle([]config.Workspace{other, renamed})
	assert.Equal(t, []string{"ws-other"}, m.slotNames(), "the kept tab stays, and nothing else opens")
	assert.Contains(t, m.errBox.String(), "restart loom")
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-other"}, fresh.OpenWorkspaces, "the open list is the tabs, unchanged")
	require.NoError(t, m.checkSlotInvariant())
}

// TestActivateWorkspace_ASecondNameForOneDirectoryIsRefused: two names
// registered for one directory, one through a symlink, are one workspace,
// which the model serves once, under the first name. Opening it under the
// second is refused, pointing at the first; a tab under the second would be
// keyed on a name the model does not serve.
func TestActivateWorkspace_ASecondNameForOneDirectoryIsRefused(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	first := preservedTerminalWorkspace(t, "ws-first")
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(first.Path, link))
	second := config.Workspace{Name: "ws-second", Path: link}
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add(first.Name, first.Path))
	require.NoError(t, reg.Add(second.Name, second.Path))
	m := newRestoreHome(t, &recordingExec{})
	_, ok := m.servedNamed("ws-second")
	require.False(t, ok, "fixture: one directory is served once")

	_, err = m.activateWorkspace(second)
	require.Error(t, err)
	assert.Equal(t, "ws-second is the same directory as ws-first, which loom serves: open ws-first", err.Error())
	assert.Empty(t, m.slots)
}

// TestReopenedTab_IsTheSameWorkspace: closing a tab only stops this TUI
// showing its workspace, which the model keeps serving. Reopening it shows
// that very workspace, under the same ID, with whatever happened to it
// meanwhile (here a session that landed while it was closed).
func TestReopenedTab_IsTheSameWorkspace(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	m := newRestoreHome(t, &recordingExec{})
	a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
	registerWorkspaces(t, m, a, b)
	_, err := m.activateWorkspace(a)
	require.NoError(t, err)
	_, err = m.activateWorkspace(b)
	require.NoError(t, err)
	idA := m.slots[0].id

	_, err = m.deactivateWorkspace("ws-a")
	require.NoError(t, err)
	require.Equal(t, []string{"ws-b"}, m.slotNames())
	landed := &session.Instance{Title: "landed-meanwhile", Status: session.Paused}
	testModel(m).WorkspaceForTest(idA).AddForTest(landed)

	_, err = m.activateWorkspace(a)
	require.NoError(t, err)
	require.Equal(t, []string{"ws-b", "ws-a"}, m.slotNames())
	reopened := m.slots[1]
	assert.Equal(t, idA, reopened.id, "the same workspace, the same ID")
	assert.NotNil(t, reopened.list.GetInstanceByTitle("landed-meanwhile"), "with what landed while it was closed")
	require.NoError(t, m.checkSlotInvariant())
}
