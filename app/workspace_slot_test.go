package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// focusSlots installs slots as h's open workspace tabs and focuses
// slots[focused], so the embedded focused slot is the same pointer as
// h.slots[focused] (the invariant checkSlotInvariant enforces).
func focusSlots(h *home, focused int, slots ...*workspaceSlot) {
	h.slots = slots
	h.focusedSlot = focused
	h.workspaceSlot = slots[focused]
}

// focusedName is the focused slot's workspace name ("" for a nil wsCtx).
func focusedName(m *home) string {
	if m.wsCtx == nil {
		return ""
	}
	return m.wsCtx.Name
}

// TestSlotInvariant_HoldsAcrossSlotLifecycle drives every m.slots
// mutation — activation from classic mode, more activations, a switch,
// closing the focused tab, closing a tab left of focus, and the return to
// global mode — and checks the embedded-focused-slot invariant after each.
func TestSlotInvariant_HoldsAcrossSlotLifecycle(t *testing.T) {
	isolateTmux(t)
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir) // enterGlobalMode's global storage
	m := newRestoreHome(&recordingExec{})
	require.NoError(t, m.checkSlotInvariant(), "classic home")
	classic := m.workspaceSlot

	for _, name := range []string{"ws-a", "ws-b", "ws-c"} {
		_, err := m.activateWorkspace(preservedTerminalWorkspace(t, name))
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
	_, err := m.deactivateWorkspace("ws-b")
	require.NoError(t, err)
	require.NoError(t, m.checkSlotInvariant(), "after closing the focused tab")
	assert.Equal(t, []string{"ws-a", "ws-c"}, m.slotNames())
	assert.Equal(t, "ws-c", focusedName(m))

	// Closing a tab left of focus shifts the index, not the focus.
	_, err = m.deactivateWorkspace("ws-a")
	require.NoError(t, err)
	require.NoError(t, m.checkSlotInvariant(), "after closing a tab left of focus")
	assert.Equal(t, "ws-c", focusedName(m))

	// The last tab only closes through global mode.
	_, err = m.deactivateWorkspace("ws-c")
	require.Error(t, err)
	require.NoError(t, m.checkSlotInvariant(), "after refusing to close the last tab")
	require.Len(t, m.slots, 1)

	_ = m.applyWorkspaceToggle(nil)
	require.NoError(t, m.checkSlotInvariant(), "after returning to global mode")
	assert.Empty(t, m.slots)
	require.NotNil(t, m.wsCtx, "global mode's slot carries the global context")
	assert.Equal(t, config.WorkspaceContext{ConfigDir: globalDir}, *m.wsCtx)
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
	_ = m.applyWorkspaceToggle([]config.Workspace{preservedTerminalWorkspace(t, "ws-a")})
	require.Equal(t, []string{"ws-a"}, m.slotNames())

	cmd := m.applyWorkspaceToggle(corruptWorkspaces(t, "ws-bad"))
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
	m.list.AddInstance(inst)
	assert.Same(t, inst, m.slots[0].list.GetInstanceByTitle("added"))

	replacement := ui.NewList(&m.spinner)
	m.list = replacement
	assert.Same(t, replacement, m.slots[0].list, "a promoted-field write lands in the focused slot")

	m.switchWorkspaceSlot(1)
	require.NoError(t, m.checkSlotInvariant())
	other := &session.Instance{Title: "peer-added", Status: session.Ready}
	m.list.AddInstance(other)
	assert.Same(t, other, m.slots[1].list.GetInstanceByTitle("peer-added"))
	assert.Nil(t, m.slots[0].list.GetInstanceByTitle("peer-added"), "the other slot is untouched")
}

// TestClassicSlot_NonNilAndOutsideSlots: classic startup focuses a slot
// that is not an open tab, and a restore where every workspace failed
// keeps that same slot focused.
func TestClassicSlot_NonNilAndOutsideSlots(t *testing.T) {
	isolateTmux(t)
	t.Setenv("LOOM_HOME", t.TempDir())
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	t.Run("classic startup", func(t *testing.T) {
		cfg := config.DefaultConfig()
		off := false
		cfg.ClaudeRemoteControl = &off // no claude auth probe
		m, err := newHome(context.Background(), &config.WorkspaceContext{ConfigDir: t.TempDir()}, nil, cfg, "true", "", true)
		require.NoError(t, err)
		require.NotNil(t, m.workspaceSlot)
		assert.Empty(t, m.slots)
		assert.NotNil(t, m.list)
		assert.NotNil(t, m.splitPane)
		assert.NotNil(t, m.workbench)
		require.NoError(t, m.checkSlotInvariant())
	})

	t.Run("restore fallback keeps the classic slot", func(t *testing.T) {
		m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
		classic := m.workspaceSlot
		m.restoreSavedWorkspaces(corruptWorkspaces(t, "ws-bad"))
		require.Empty(t, m.slots)
		assert.Same(t, classic, m.workspaceSlot)
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

// TestEnterGlobalMode_SlotSaveFailureAbortsCleanly: enterGlobalMode used
// to ignore deactivateWorkspace's error, so a tab whose save failed stayed
// open while home switched to the global slot anyway — half-switched,
// with an open tab that was no longer the focused slot. A failed save
// must abort the transition with nothing switched, and with nothing
// loaded: the global load crash-restarts agents, writes the loom-context
// files and sweeps orphan worktrees and hooks, and it used to run before
// the tab saves, so an abort left relaunched global agents running that
// nothing displayed.
func TestEnterGlobalMode_SlotSaveFailureAbortsCleanly(t *testing.T) {
	isolateTmux(t)
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)
	t.Setenv("LOOM_HOME", globalDir)
	// A Running workspace terminal whose tmux session is gone: loading the
	// global state crash-restarts it.
	const gterm = "gterm"
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.StateFileName), []byte(fmt.Sprintf(
		`{"instances":[{"title":%q,"status":0,"program":"sleep 30","is_workspace_terminal":true,"path":%q,"worktree":{}}]}`,
		gterm, t.TempDir())), 0o644))

	failing := &failingInstanceStorage{}
	storageA, err := session.NewStorage(failing, t.TempDir())
	require.NoError(t, err)
	recB := &recordingInstanceStorage{}
	storageB, err := session.NewStorage(recB, t.TempDir())
	require.NoError(t, err)

	dead := &deadTmuxExec{}
	m := newRestoreHome(dead)
	m.errBox.SetSize(400, 1)
	slotA := fleetSlot(t, "ws-a", "a1")
	slotA.storage = storageA
	slotB := fleetSlot(t, "ws-b", "b1")
	slotB.storage = storageB
	focusSlots(m, 0, slotA, slotB)

	cmd := m.applyWorkspaceToggle(nil)

	require.NotNil(t, cmd, "the failure must be surfaced")
	assert.Contains(t, m.errBox.String(), "ws-a")
	assert.GreaterOrEqual(t, failing.calls, 1, "the failing tab's save was attempted")
	require.NoError(t, m.checkSlotInvariant())
	assert.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames(), "no tab may be closed")
	assert.Same(t, slotA, m.workspaceSlot, "focus stays on the tab it was on")
	assert.Equal(t, "ws-a", focusedName(m))

	assert.Empty(t, dead.args, "nothing may be loaded: no reconcile probe, orphan discovery or hooks sweep")
	assert.Error(t, tmux.Command(context.Background(), "has-session", "-t="+tmux.ToLoomTmuxName(gterm)).Run(),
		"the global workspace terminal must not be crash-restarted")
	entries, err := os.ReadDir(globalDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{config.StateFileName}, names, "nothing may be written to the global dir")
}

// scriptSlotTestScript creates an instance, and in Y also switches to the
// next workspace from inside the same handler.
const scriptSlotTestScript = `
cs.bind("X", function(ctx)
  ctx:new_instance{title = "made"}
end)
cs.bind("Y", function(ctx)
  ctx:new_instance{title = "made"}
  cs.actions.workspace_next()
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
// while the script is in flight, then delivers the result.
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
	_, _ = m.Update(done)
}

// TestScriptDone_DropsInstanceWhenFocusChangedMidDispatch: ctx:new_instance
// builds its instance from the dispatch-time slot (ConfigDir, repo path).
// handleScriptDone used to add it to whichever slot was focused when the
// result arrived, so switching workspace while a script ran filed one
// workspace's session under another.
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
	m.list.AddInstance(&session.Instance{Title: "main", Status: session.Running})
	m.list.SetSelectedInstance(0)
	m.pendingRatioSaves = map[string]float64{"main": 0.4}
	classicState := m.appState

	ws := preservedTerminalWorkspace(t, "ws-a")
	m.setOverlay(overlay.NewStartupWorkspacePicker([]config.Workspace{ws}), overlayWorkspacePickerStartup)
	m.state = stateWorkspace
	_, _ = handleStateWorkspaceKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, []string{"ws-a"}, m.slotNames())
	require.NoError(t, m.checkSlotInvariant())
	assert.Empty(t, m.pendingRatioSaves, "the switch must flush pending ratios")
	assert.Equal(t, 0.4, classicState.GetUIPrefs().SplitRatios["main"], "flushed into the departing (classic) state")
	_, leaked := m.appState.GetUIPrefs().SplitRatios["main"]
	assert.False(t, leaked, "the new workspace's state must not receive the classic ratio")
}
