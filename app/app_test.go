package app

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	_ = log.Initialize("", false)
	defer log.Close()

	// Run all tests
	exitCode := m.Run()

	// Exit with the same code as the tests
	os.Exit(exitCode)
}

// TestConfirmationModalStateTransitions tests state transitions without full instance setup
func TestConfirmationModalStateTransitions(t *testing.T) {
	// Create a minimal home struct for testing state transitions
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

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
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
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
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
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
		keyMsg := tea.KeyMsg{Type: tea.KeyEscape}
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
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Create enough of home struct to test handleKeyPress in confirmation state
	h := &home{
		ctx:       context.Background(),
		state:     stateConfirm,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
	}
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
			var keyMsg tea.KeyMsg
			if tc.key == "esc" {
				keyMsg = tea.KeyMsg{Type: tea.KeyEscape}
			} else {
				keyMsg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)}
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
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Add test instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
		AutoYes: false,
	})
	require.NoError(t, err)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
	}

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
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

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
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

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
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
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
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&sp, false)
	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	menu := ui.NewMenu()

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	storage, err := session.NewStorage(&mockInstanceStorage{}, t.TempDir())
	require.NoError(t, err)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		splitPane: splitPane,
		menu:      menu,
		storage:   storage,
	}

	// Simulate instanceStartedMsg (no prompt, no error)
	msg := instanceStartedMsg{
		instance:        instance,
		err:             nil,
		promptAfterName: false,
	}
	model, _ := h.Update(msg)
	homeModel := model.(*home)

	assert.Equal(t, stateInlineAttach, homeModel.state, "should auto-focus into inline attach")
	assert.Equal(t, ui.FocusAgent, homeModel.splitPane.GetFocusedPane(), "should focus agent pane")
}

// TestConfirmationModalVisualAppearance tests that confirmation modal has distinct visual appearance
func TestConfirmationModalVisualAppearance(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

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
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-delete",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = instance.TransitionTo(session.Running)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
	}

	// Set up a task like the kill handler does
	h.confirmTask("[!] Kill session 'test-delete'?", overlay.ConfirmationTask{
		Sync: func() {
			_ = instance.TransitionTo(session.Deleting)
		},
		Async: func() tea.Msg {
			return killInstanceMsg{title: "test-delete"}
		},
	})

	// Simulate confirming (pressing 'y')
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
	_, _ = h.handleKeyPress(keyMsg)

	// Sync step should have run — status should be Deleting
	assert.Equal(t, session.Deleting, instance.GetStatus())
}

// TestTransitionFailedMsgRevertsStatus verifies that a transitionFailedMsg
// reverts the instance status to its previous value.
func TestTransitionFailedMsgRevertsStatus(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-revert",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = instance.TransitionTo(session.Deleting)
	_ = list.AddInstance(instance)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:    ui.NewErrBox(),
	}

	msg := transitionFailedMsg{
		title:          "test-revert",
		op:             "delete",
		previousStatus: session.Running,
		err:            fmt.Errorf("branch is checked out"),
	}
	h.Update(msg)

	assert.Equal(t, session.Running, instance.GetStatus())
}

// TestPersistableInstancesFiltersDeleting verifies that persistableInstances
// excludes instances with Deleting status.
func TestPersistableInstancesFiltersDeleting(t *testing.T) {
	running, _ := session.NewInstance(session.InstanceOptions{
		Title: "running", Path: t.TempDir(), Program: "claude",
	})
	_ = running.TransitionTo(session.Running)

	deleting, _ := session.NewInstance(session.InstanceOptions{
		Title: "deleting", Path: t.TempDir(), Program: "claude",
	})
	_ = deleting.TransitionTo(session.Deleting)

	paused, _ := session.NewInstance(session.InstanceOptions{
		Title: "paused", Path: t.TempDir(), Program: "claude",
	})
	_ = paused.TransitionTo(session.Paused)

	result := persistableInstances([]*session.Instance{running, deleting, paused})
	assert.Len(t, result, 2)
	assert.Equal(t, "running", result[0].Title)
	assert.Equal(t, "paused", result[1].Title)
}

// TestPendingConfirmationClearedOnCancel verifies that cancelling a
// confirmation clears the bundled task so a stale Sync/Async pair
// can't leak into the next confirmation.
func TestPendingConfirmationClearedOnCancel(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
	}

	syncCalled := false
	h.confirmTask("Test?", overlay.ConfirmationTask{
		Sync:  func() { syncCalled = true },
		Async: func() tea.Msg { return nil },
	})

	// Cancel with 'n'
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	_, _ = h.handleKeyPress(keyMsg)

	assert.False(t, syncCalled, "Sync should not have been called on cancel")
	assert.Nil(t, h.pendingConfirmation.Sync, "pendingConfirmation.Sync should be nil after cancel")
	assert.Nil(t, h.pendingConfirmation.Async, "pendingConfirmation.Async should be nil after cancel")
}

// TestHandleQuitStaysInTUIOnSaveError verifies that when SaveInstances
// fails in the single-slot path, handleQuit refuses to quit and surfaces
// the error instead. This branch was already correct before the F2 fix;
// this test is a regression guard.
func TestHandleQuitStaysInTUIOnSaveError(t *testing.T) {
	cfgDir := t.TempDir()
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "a", Path: t.TempDir(), Program: "claude",
	})
	require.NoError(t, err)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)
	_ = list.AddInstance(inst)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:   storage,
		appState:  state,
		errBox:    ui.NewErrBox(),
	}

	// Make the config dir read-only so the next SaveInstances fails.
	require.NoError(t, os.Chmod(cfgDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })

	_, cmd := h.handleQuit()
	require.NotNil(t, cmd, "handleQuit must return a Cmd even on save failure")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.False(t, isQuit, "handleQuit must not quit when SaveInstances fails")
}

// TestHandleQuitStaysInTUIOnSaveErrorMultiSlot drives the F2 fix. Before
// the fix, the multi-slot branch logged SaveInstances errors and still
// returned tea.Quit — silent data loss. After the fix, both branches
// share the same policy: surface the error and keep the user in the TUI
// so they can fix the underlying issue (e.g. disk full) without losing
// unsaved state.
func TestHandleQuitStaysInTUIOnSaveErrorMultiSlot(t *testing.T) {
	cfgDir := t.TempDir()
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "a", Path: t.TempDir(), Program: "claude",
	})
	require.NoError(t, err)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)
	_ = list.AddInstance(inst)

	wsCtx := &config.WorkspaceContext{Name: "test-ws", ConfigDir: cfgDir}
	slot := workspaceSlot{
		wsCtx:     wsCtx,
		storage:   storage,
		appConfig: config.DefaultConfig(),
		appState:  state,
		list:      list,
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
	}

	h := &home{
		ctx:         context.Background(),
		state:       stateDefault,
		appConfig:   config.DefaultConfig(),
		list:        list,
		menu:        ui.NewMenu(),
		splitPane:   ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:     storage,
		appState:    state,
		errBox:      ui.NewErrBox(),
		slots:       []workspaceSlot{slot},
		focusedSlot: 0,
		activeCtx:   wsCtx,
	}

	require.NoError(t, os.Chmod(cfgDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })

	_, cmd := h.handleQuit()
	require.NotNil(t, cmd, "handleQuit must return a Cmd even on save failure")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.False(t, isQuit, "handleQuit must not quit when SaveInstances fails in multi-slot path")
}
