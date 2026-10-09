package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests sets up the package test environment and runs the tests. It
// returns the exit code rather than exiting so its deferred cleanup runs.
func runTests(m *testing.M) int {
	// Initialize the logger before any tests run
	_ = log.Initialize("", false)
	defer log.Close()

	// Belt and suspenders: LOOM_TMUX_SOCKET is the only variable
	// tmux.Command consults (an explicit -L outranks $TMUX), but any test
	// that clears it — deliberately or by accident — would otherwise fall
	// through to $TMUX and land on whatever real server encloses this
	// process. Unset it so that fallback can't reach a live developer
	// session, and point TMUX_TMPDIR at a throwaway directory so even a
	// bare `tmux -L <socket>` (no explicit tmpdir) resolves its socket
	// file under a directory this run owns and removes on exit. This must
	// be set up — and its cleanup deferred — before the private-socket kill
	// below is deferred, so RemoveAll runs after (not before) kill-server:
	// defers unwind LIFO, and kill-server needs the socket file's directory
	// to still exist when it runs.
	//
	// Names below are kept short and never hard-code a base directory
	// (os.MkdirTemp("", …) resolves $TMPDIR/os.TempDir(), which may not be
	// /tmp — e.g. a Nix build sandbox uses TMPDIR=/build): tmux's socket
	// path is TMUX_TMPDIR/tmux-<uid>/<name>, and sun_path is capped at 108
	// bytes on Linux, 104 on macOS, where the default $TMPDIR alone can run
	// ~49 bytes.
	os.Unsetenv("TMUX")

	// Keep every config-dir resolution off the developer's real ~/.loom.
	// enterGlobalMode loads config.GlobalWorkspaceContext (LOOM_GLOBAL_DIR);
	// the registry writes workspaces.json there too — SetOpenWorkspaces
	// reloads and saves the on-disk registry even through the bare
	// &config.WorkspaceRegistry{} fleetHome installs — and a nil workspace
	// context resolves LOOM_HOME. Tests that read or seed those directories
	// set their own with t.Setenv; this is the default for the rest.
	loomCleanup, err := testenv.IsolateLoomDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer loomCleanup()

	tmuxTmpDir, err := os.MkdirTemp("", "lt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdir tmux tmpdir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmuxTmpDir)
	if err := os.Setenv("TMUX_TMPDIR", tmuxTmpDir); err != nil {
		fmt.Fprintf(os.Stderr, "set TMUX_TMPDIR: %v\n", err)
		return 1
	}

	// Point every tmux.Command in this package's tests — and in the loom
	// code they drive — at a private server, never the developer's default
	// one, whose live loom_* sessions a stray orphan sweep or kill would
	// destroy (and where tests used to leave loom_term_* sessions behind).
	// Tests that want a fresh server of their own layer isolateTmux on top.
	// Kill the private server afterwards so nothing outlives the run. The
	// existing -L cleanups (here and in isolateTmux) still resolve the same
	// server: tmux derives the socket path from TMUX_TMPDIR + socket name,
	// and both are fixed for the duration of this process.
	sock := fmt.Sprintf("lt-a-%d", os.Getpid())
	if err := os.Setenv(tmux.EnvTmuxSocket, sock); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", tmux.EnvTmuxSocket, err)
		return 1
	}
	defer func() { _ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run() }()

	return m.Run()
}

// TestConfirmationModalStateTransitions tests state transitions without full instance setup
func TestConfirmationModalStateTransitions(t *testing.T) {
	// Create a minimal home struct for testing state transitions
	h := wireCore(t, &home{
		workspaceSlot: slotWith(testWS(core.WorkspaceParts{Config: config.DefaultConfig()}), &workspaceSlot{}),
		ctx:           context.Background(),
		state:         stateDefault,
	})

	t.Run("shows confirmation on D press", func(t *testing.T) {
		// Simulate pressing 'D'
		h.state = stateDefault
		h.dismissOverlay()

		// Manually trigger what would happen in handleKeyPress for 'D'
		h.state = stateConfirm
		h.setOverlay(overlay.NewConfirmationOverlay("[!] Kill session 'test'?"), overlayConfirmation)

		co := h.confirmation()
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, co)
		assert.False(t, co.Dismissed)
	})

	t.Run("returns to default on y press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.setOverlay(overlay.NewConfirmationOverlay("Test confirmation"), overlayConfirmation)
		co := h.confirmation()

		// Simulate pressing 'y' using HandleKeyPress
		keyMsg := tea.KeyPressMsg{Code: 'y', Text: "y"}
		shouldClose := co.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.dismissOverlay()
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmation())
	})

	t.Run("returns to default on n press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.setOverlay(overlay.NewConfirmationOverlay("Test confirmation"), overlayConfirmation)
		co := h.confirmation()

		// Simulate pressing 'n' using HandleKeyPress
		keyMsg := tea.KeyPressMsg{Code: 'n', Text: "n"}
		shouldClose := co.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.dismissOverlay()
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmation())
	})

	t.Run("returns to default on esc press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.setOverlay(overlay.NewConfirmationOverlay("Test confirmation"), overlayConfirmation)
		co := h.confirmation()

		// Simulate pressing ESC using HandleKeyPress
		keyMsg := tea.KeyPressMsg{Code: tea.KeyEsc}
		shouldClose := co.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.dismissOverlay()
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmation())
	})
}

// TestConfirmationModalKeyHandling tests the actual key handling in confirmation state
func TestConfirmationModalKeyHandling(t *testing.T) {
	// Import needed packages
	ws := testWS(core.WorkspaceParts{Config: config.DefaultConfig()})
	list := fixtureList(t)

	// Create enough of home struct to test handleKeyPress in confirmation state
	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
		ctx:   context.Background(),
		state: stateConfirm,
		menu:  ui.NewMenu(),
	})
	h.setOverlay(overlay.NewConfirmationOverlay("Kill session?"), overlayConfirmation)

	testCases := []struct {
		name              string
		key               string
		expectedState     state
		expectedDismissed bool
		expectedNil       bool
	}{
		{
			name:              "y key confirms and dismisses overlay",
			key:               "y",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "n key cancels and dismisses overlay",
			key:               "n",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "esc key cancels and dismisses overlay",
			key:               "esc",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "other keys are ignored",
			key:               "x",
			expectedState:     stateConfirm,
			expectedDismissed: false,
			expectedNil:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset state
			h.state = stateConfirm
			h.setOverlay(overlay.NewConfirmationOverlay("Kill session?"), overlayConfirmation)

			// Create key message
			var keyMsg tea.KeyPressMsg
			if tc.key == "esc" {
				keyMsg = tea.KeyPressMsg{Code: tea.KeyEsc}
			} else {
				keyMsg = tea.KeyPressMsg{Code: rune(tc.key[0]), Text: tc.key}
			}

			// Call handleKeyPress
			model, _ := h.handleKeyPress(keyMsg)
			homeModel, ok := model.(*home)
			require.True(t, ok)

			assert.Equal(t, tc.expectedState, homeModel.state, "State mismatch for key: %s", tc.key)
			co := homeModel.confirmation()
			if tc.expectedNil {
				assert.Nil(t, co, "Overlay should be nil for key: %s", tc.key)
			} else {
				assert.NotNil(t, co, "Overlay should not be nil for key: %s", tc.key)
				assert.Equal(t, tc.expectedDismissed, co.Dismissed, "Dismissed mismatch for key: %s", tc.key)
			}
		})
	}
}

// TestConfirmationMessageFormatting tests that confirmation messages are formatted correctly
func TestConfirmationMessageFormatting(t *testing.T) {
	testCases := []struct {
		name            string
		sessionTitle    string
		expectedMessage string
	}{
		{
			name:            "short session name",
			sessionTitle:    "my-feature",
			expectedMessage: "[!] Kill session 'my-feature'? (y/n)",
		},
		{
			name:            "long session name",
			sessionTitle:    "very-long-feature-branch-name-here",
			expectedMessage: "[!] Kill session 'very-long-feature-branch-name-here'? (y/n)",
		},
		{
			name:            "session with spaces",
			sessionTitle:    "feature with spaces",
			expectedMessage: "[!] Kill session 'feature with spaces'? (y/n)",
		},
		{
			name:            "session with special chars",
			sessionTitle:    "feature/branch-123",
			expectedMessage: "[!] Kill session 'feature/branch-123'? (y/n)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the message formatting directly
			actualMessage := fmt.Sprintf("[!] Kill session '%s'? (y/n)", tc.sessionTitle)
			assert.Equal(t, tc.expectedMessage, actualMessage)
		})
	}
}

// TestConfirmationFlowSimulation tests the confirmation flow by simulating the state changes
func TestConfirmationFlowSimulation(t *testing.T) {
	// Create a minimal setup

	// Add test instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	ws := testWS(core.WorkspaceParts{Config: config.DefaultConfig()}, instance)
	list := fixtureList(t)
	list.SetSelectedInstance(0)

	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list: list,
		}),
		ctx:   context.Background(),
		state: stateDefault,
		menu:  ui.NewMenu(),
	})

	// Simulate what happens when D is pressed
	selected := h.list.GetSelectedInstance()
	require.NotNil(t, selected)

	// This is what the KeyKill handler does
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	h.setOverlay(overlay.NewConfirmationOverlay(message), overlayConfirmation)
	h.state = stateConfirm

	// Verify the state
	co := h.confirmation()
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, co)
	assert.False(t, co.Dismissed)
	// Test that overlay renders with the correct message
	rendered := co.Render()
	assert.Contains(t, rendered, "Kill session 'test-session'?")
}

// TestConfirmActionWithDifferentTypes tests that confirmAction works with different action types
func TestConfirmActionWithDifferentTypes(t *testing.T) {
	h := wireCore(t, &home{
		workspaceSlot: slotWith(testWS(core.WorkspaceParts{Config: config.DefaultConfig()}), &workspaceSlot{}),
		ctx:           context.Background(),
		state:         stateDefault,
	})

	t.Run("works with simple action returning nil", func(t *testing.T) {
		actionCalled := false
		action := func() tea.Msg {
			actionCalled = true
			return nil
		}

		// Set up callback to track action execution
		actionExecuted := false
		h.setOverlay(overlay.NewConfirmationOverlay("Test action?"), overlayConfirmation)
		co := h.confirmation()
		co.OnConfirm = func() {
			h.state = stateDefault
			actionExecuted = true
			action() // Execute the action
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, co)
		assert.False(t, co.Dismissed)
		assert.NotNil(t, co.OnConfirm)

		// Execute the confirmation callback
		co.OnConfirm()
		assert.True(t, actionCalled)
		assert.True(t, actionExecuted)
	})

	t.Run("works with action returning error", func(t *testing.T) {
		expectedErr := fmt.Errorf("test error")
		action := func() tea.Msg {
			return expectedErr
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.setOverlay(overlay.NewConfirmationOverlay("Error action?"), overlayConfirmation)
		co := h.confirmation()
		co.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, co)
		assert.False(t, co.Dismissed)
		assert.NotNil(t, co.OnConfirm)

		// Execute the confirmation callback
		co.OnConfirm()
		assert.Equal(t, expectedErr, receivedMsg)
	})

	t.Run("works with action returning custom message", func(t *testing.T) {
		action := func() tea.Msg {
			return instanceChangedMsg{}
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.setOverlay(overlay.NewConfirmationOverlay("Custom message action?"), overlayConfirmation)
		co := h.confirmation()
		co.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, co)
		assert.False(t, co.Dismissed)
		assert.NotNil(t, co.OnConfirm)

		// Execute the confirmation callback
		co.OnConfirm()
		_, ok := receivedMsg.(instanceChangedMsg)
		assert.True(t, ok, "Expected instanceChangedMsg but got %T", receivedMsg)
	})
}

// TestMultipleConfirmationsDontInterfere tests that multiple confirmations don't interfere with each other
func TestMultipleConfirmationsDontInterfere(t *testing.T) {
	h := wireCore(t, &home{
		workspaceSlot: slotWith(testWS(core.WorkspaceParts{Config: config.DefaultConfig()}), &workspaceSlot{}),
		ctx:           context.Background(),
		state:         stateDefault,
	})

	// First confirmation
	action1Called := false
	action1 := func() tea.Msg {
		action1Called = true
		return nil
	}

	// Set up first confirmation
	h.setOverlay(overlay.NewConfirmationOverlay("First action?"), overlayConfirmation)
	co := h.confirmation()
	firstOnConfirm := func() {
		h.state = stateDefault
		action1()
	}
	co.OnConfirm = firstOnConfirm
	h.state = stateConfirm

	// Verify first confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, co)
	assert.False(t, co.Dismissed)
	assert.NotNil(t, co.OnConfirm)

	// Cancel first confirmation (simulate pressing 'n')
	keyMsg := tea.KeyPressMsg{Code: 'n', Text: "n"}
	shouldClose := co.HandleKeyPress(keyMsg)
	if shouldClose {
		h.state = stateDefault
		h.dismissOverlay()
	}

	// Second confirmation with different action
	action2Called := false
	action2 := func() tea.Msg {
		action2Called = true
		return fmt.Errorf("action2 error")
	}

	// Set up second confirmation
	h.setOverlay(overlay.NewConfirmationOverlay("Second action?"), overlayConfirmation)
	co2 := h.confirmation()
	var secondResult tea.Msg
	secondOnConfirm := func() {
		h.state = stateDefault
		secondResult = action2()
	}
	co2.OnConfirm = secondOnConfirm
	h.state = stateConfirm

	// Verify second confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, co2)
	assert.False(t, co2.Dismissed)
	assert.NotNil(t, co2.OnConfirm)

	// Execute second action to verify it's the correct one
	co2.OnConfirm()
	err, ok := secondResult.(error)
	assert.True(t, ok)
	assert.Equal(t, "action2 error", err.Error())
	assert.True(t, action2Called)
	assert.False(t, action1Called, "First action should not have been called")

	// Test that cancelled action can still be executed independently
	firstOnConfirm()
	assert.True(t, action1Called, "First action should be callable after being replaced")
}

// mockInstanceStorage implements config.InstanceStorage for testing.
type mockInstanceStorage struct{}

func (m *mockInstanceStorage) SaveInstances(_ json.RawMessage) error { return nil }
func (m *mockInstanceStorage) GetInstances() json.RawMessage         { return nil }
func (m *mockInstanceStorage) DeleteAllInstances() error             { return nil }

// TestAutoFocusAgentAfterInstanceStart verifies that after a new session finishes
// starting, the app auto-enters inline attach mode focused on the agent pane.
func TestAutoFocusAgentAfterInstanceStart(t *testing.T) {
	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	menu := ui.NewMenu()

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	finishStart(t, instance)

	storage, err := session.NewStorage(&mockInstanceStorage{}, t.TempDir())
	require.NoError(t, err)
	ws := testWS(core.WorkspaceParts{Config: config.DefaultConfig(), Storage: storage}, instance)
	list := fixtureList(t)
	list.SetSelectedInstance(0)

	h := wirePanes(t, wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: splitPane,
		}),
		ctx:   context.Background(),
		state: stateDefault,
		menu:  menu,
	}))

	// Simulate a start's result (no prompt, no error)
	deliver(t, h, core.CausedForTest(1, core.StartResult{
		Instance: instance,
		Err:      nil,
	}))
	homeModel := h

	assert.Equal(t, stateInlineAttach, homeModel.state, "should auto-focus into inline attach")
	assert.Equal(t, ui.FocusAgent, homeModel.splitPane.GetFocusedPane(), "should focus agent pane")
}

// TestConfirmationModalVisualAppearance tests that confirmation modal has distinct visual appearance
func TestConfirmationModalVisualAppearance(t *testing.T) {
	h := wireCore(t, &home{
		workspaceSlot: slotWith(testWS(core.WorkspaceParts{Config: config.DefaultConfig()}), &workspaceSlot{}),
		ctx:           context.Background(),
		state:         stateDefault,
	})

	// Create a test confirmation overlay
	message := "[!] Delete everything?"
	h.setOverlay(overlay.NewConfirmationOverlay(message), overlayConfirmation)
	h.state = stateConfirm

	// Verify the overlay was created with confirmation settings
	co := h.confirmation()
	assert.NotNil(t, co)
	assert.Equal(t, stateConfirm, h.state)
	assert.False(t, co.Dismissed)

	// Test the overlay render (we can test that it renders without errors)
	rendered := co.Render()
	assert.NotEmpty(t, rendered)

	// Test that it includes the message content and instructions
	assert.Contains(t, rendered, "Delete everything?")
	assert.Contains(t, rendered, "Press")
	assert.Contains(t, rendered, "to confirm")
	assert.Contains(t, rendered, "to cancel")

	// Test that the danger indicator is preserved
	assert.Contains(t, rendered, "[!")
}

// TestKillSetsStatusToDeletingImmediately verifies that confirming a kill
// sets the instance status to Deleting before the async cleanup Cmd runs.
func TestKillSetsStatusToDeletingImmediately(t *testing.T) {

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-delete",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = instance.TransitionTo(session.Running)
	ws := testWS(core.WorkspaceParts{Config: config.DefaultConfig()}, instance)
	list := fixtureList(t)
	list.SetSelectedInstance(0)

	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
		ctx:   context.Background(),
		state: stateDefault,
		menu:  ui.NewMenu(),
	})

	// Set up a task like the kill handler does: its Sync step is the
	// request, whose job the model runs.
	h.confirmTask("[!] Kill session 'test-delete'?", overlay.ConfirmationTask{
		Sync: func() {
			_ = instance.TransitionTo(session.Deleting)
		},
	})

	// Simulate confirming (pressing 'y')
	keyMsg := tea.KeyPressMsg{Code: 'y', Text: "y"}
	_, _ = h.handleKeyPress(keyMsg)

	// Sync step should have run — status should be Deleting
	assert.Equal(t, session.Deleting, instance.GetStatus())
}

// TestOpFailedRevertsStatus verifies that a failed operation's
// result (core.OpFailed) reverts the instance status to its previous value.
func TestOpFailedRevertsStatus(t *testing.T) {

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-revert",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = instance.TransitionTo(session.Deleting)
	ws := testWS(core.WorkspaceParts{Config: config.DefaultConfig()}, instance)
	list := fixtureList(t)

	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		errBox: ui.NewErrBox(),
	})

	msg := core.OpFailed{
		Instance: instance,
		Title:    "test-revert",
		Op:       "delete",
		Previous: session.Running,
		Err:      fmt.Errorf("branch is checked out"),
	}
	deliver(t, h, msg)

	assert.Equal(t, session.Running, instance.GetStatus())
}

// TestPendingConfirmationClearedOnCancel verifies that cancelling a
// confirmation clears the bundled task so a stale Sync/Async pair
// can't leak into the next confirmation.
func TestPendingConfirmationClearedOnCancel(t *testing.T) {
	ws := testWS(core.WorkspaceParts{Config: config.DefaultConfig()})
	list := fixtureList(t)

	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
		ctx:   context.Background(),
		state: stateDefault,
		menu:  ui.NewMenu(),
	})

	syncCalled := false
	h.confirmTask("Test?", overlay.ConfirmationTask{
		Sync:  func() { syncCalled = true },
		Async: func() tea.Msg { return nil },
	})

	// Cancel with 'n'
	keyMsg := tea.KeyPressMsg{Code: 'n', Text: "n"}
	_, _ = h.handleKeyPress(keyMsg)

	assert.False(t, syncCalled, "Sync should not have been called on cancel")
	assert.Nil(t, h.pendingConfirmation.Sync, "pendingConfirmation.Sync should be nil after cancel")
	assert.Nil(t, h.pendingConfirmation.Async, "pendingConfirmation.Async should be nil after cancel")
}

// quitWatch is a home's Core that records a SaveForQuit.
type quitWatch struct {
	core.Core
	saved bool
}

func (q *quitWatch) SaveForQuit() error {
	q.saved = true
	return q.Core.SaveForQuit()
}

// TestHandleQuit_SavesNoSession: quitting the TUI saves no session (the
// daemon serves them on and saves them itself, as requests and its own
// probes change them and when it stops), so a workspace whose state can't
// be written no longer holds the quit. The TUI writes only its own state, the open list
// (TestHandleQuit_PersistsTheOpenList).
func TestHandleQuit_SavesNoSession(t *testing.T) {
	cfgDir := t.TempDir()
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "a", Path: t.TempDir(), Program: "claude",
	})
	require.NoError(t, err)
	wsCtx := &config.WorkspaceContext{Name: "test-ws", ConfigDir: cfgDir}
	ws := testWS(core.WorkspaceParts{Ctx: wsCtx, Storage: storage, Config: config.DefaultConfig(), State: state}, inst)
	h := &home{
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		errBox: ui.NewErrBox(),
	}
	focusSlots(h, 0, slotWith(ws, &workspaceSlot{
		list:      fixtureList(t),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
	}))
	wireCore(t, h)
	watch := &quitWatch{Core: h.core}
	h.core = watch
	require.NoError(t, os.Chmod(cfgDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })

	_, cmd := h.handleQuit()
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd(), "the quit is not held by a workspace it does not save")
	assert.False(t, watch.saved, "the TUI saves no session")
}

// TestRunNow_RunsEveryCmdOfABatch: a batched Cmd called directly only
// returns the BatchMsg the runtime would expand, so none of its Cmds run.
// restoreSavedWorkspaces runs activateWorkspace's release before the
// program starts, and that release is a batch.
func TestRunNow_RunsEveryCmdOfABatch(t *testing.T) {
	var ran []string
	step := func(name string) tea.Cmd {
		return func() tea.Msg { ran = append(ran, name); return nil }
	}

	runNow(tea.Batch(step("terminal clients"), tea.Batch(step("pane clients"), step("more")), nil))
	runNow(nil)

	assert.ElementsMatch(t, []string{"terminal clients", "pane clients", "more"}, ran)
}
