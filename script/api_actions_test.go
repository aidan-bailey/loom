package script

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingHost captures each sync primitive method call by name so
// tests can assert the Lua primitive wired through to the right Host
// method in the right order.
type recordingHost struct {
	fakeHost
	calls []string
}

func (h *recordingHost) CursorUp()      { h.calls = append(h.calls, "CursorUp") }
func (h *recordingHost) CursorDown()    { h.calls = append(h.calls, "CursorDown") }
func (h *recordingHost) ToggleDiff()    { h.calls = append(h.calls, "ToggleDiff") }
func (h *recordingHost) WorkspacePrev() { h.calls = append(h.calls, "WorkspacePrev") }
func (h *recordingHost) WorkspaceNext() { h.calls = append(h.calls, "WorkspaceNext") }

// dispatchExpectYield runs a handler bound to key and asserts it
// yielded (deferred primitives always yield). Returns the fakeHost so
// tests can inspect the enqueued intent.
func dispatchExpectYield(t *testing.T, e *Engine, key string) *fakeHost {
	t.Helper()
	h := &fakeHost{}
	_, err := e.Dispatch(context.Background(), key, h)
	if err != nil {
		t.Fatalf("dispatch %q: %v", key, err)
	}
	if len(h.enqueued) == 0 {
		t.Fatalf("dispatch %q: no intent enqueued", key)
	}
	return h
}

func TestCsActionsQuitEnqueuesIntent(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("q", function() cs.actions.quit() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "q")
	require.Len(t, h.enqueued, 1)
	_, ok := h.enqueued[0].(QuitIntent)
	assert.True(t, ok)
}

func TestCsActionsPushSelectedDefaultsConfirmTrue(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("p", function() cs.actions.push_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "p")
	intent := h.enqueued[0].(PushSelectedIntent)
	assert.True(t, intent.Confirm)
}

func TestCsActionsPushSelectedRespectsConfirmFalse(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("p", function() cs.actions.push_selected{confirm=false} end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "p")
	intent := h.enqueued[0].(PushSelectedIntent)
	assert.False(t, intent.Confirm)
}

func TestCsActionsKillSelectedDefaultsConfirmTrue(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("k", function() cs.actions.kill_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "k")
	intent := h.enqueued[0].(KillSelectedIntent)
	assert.True(t, intent.Confirm)
}

func TestCsActionsKillSelectedRespectsConfirmFalse(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("k", function() cs.actions.kill_selected{confirm=false} end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "k")
	intent := h.enqueued[0].(KillSelectedIntent)
	assert.False(t, intent.Confirm)
}

func TestCsActionsCheckoutSelectedDefaults(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("c", function() cs.actions.checkout_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "c")
	intent := h.enqueued[0].(CheckoutIntent)
	assert.True(t, intent.Confirm)
	assert.True(t, intent.Help)
}

func TestCsActionsCheckoutSelectedAllowsOverrides(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("c", function() cs.actions.checkout_selected{confirm=false, help=false} end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "c")
	intent := h.enqueued[0].(CheckoutIntent)
	assert.False(t, intent.Confirm)
	assert.False(t, intent.Help)
}

func TestCsActionsResumeSelectedEnqueues(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("r", function() cs.actions.resume_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "r")
	_, ok := h.enqueued[0].(ResumeIntent)
	assert.True(t, ok)
}

func TestCsActionsNewInstanceDefaults(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("n", function() cs.actions.new_instance() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "n")
	intent := h.enqueued[0].(NewInstanceIntent)
	assert.False(t, intent.Prompt)
	assert.Equal(t, "", intent.Title)
}

func TestCsActionsNewInstanceParsesPromptAndTitle(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("N", function() cs.actions.new_instance{prompt=true, title="hello"} end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "N")
	intent := h.enqueued[0].(NewInstanceIntent)
	assert.True(t, intent.Prompt)
	assert.Equal(t, "hello", intent.Title)
}

func TestCsActionsShowHelpEnqueues(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("?", function() cs.actions.show_help() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "?")
	_, ok := h.enqueued[0].(ShowHelpIntent)
	assert.True(t, ok)
}

func TestCsActionsOpenWorkspacePickerEnqueues(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("W", function() cs.actions.open_workspace_picker() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "W")
	_, ok := h.enqueued[0].(WorkspacePickerIntent)
	assert.True(t, ok)
}

func TestCsActionsInlineAttachPanes(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		cs.bind("a", function() cs.actions.inline_attach_agent() end)
		cs.bind("t", function() cs.actions.inline_attach_terminal() end)
	`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "a")
	agent := h.enqueued[0].(InlineAttachIntent)
	assert.Equal(t, AttachPaneAgent, agent.Pane)

	h = dispatchExpectYield(t, e, "t")
	term := h.enqueued[0].(InlineAttachIntent)
	assert.Equal(t, AttachPaneTerminal, term.Pane)
}

func TestCsActionsFullscreenAttachPanes(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		cs.bind("A", function() cs.actions.fullscreen_attach_agent() end)
		cs.bind("T", function() cs.actions.fullscreen_attach_terminal() end)
	`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "A")
	agent := h.enqueued[0].(FullscreenAttachIntent)
	assert.Equal(t, AttachPaneAgent, agent.Pane)

	h = dispatchExpectYield(t, e, "T")
	term := h.enqueued[0].(FullscreenAttachIntent)
	assert.Equal(t, AttachPaneTerminal, term.Pane)
}

func TestCsActionsQuickInputPanes(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		cs.bind("a", function() cs.actions.quick_input_agent() end)
		cs.bind("t", function() cs.actions.quick_input_terminal() end)
	`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "a")
	agent := h.enqueued[0].(QuickInputIntent)
	assert.Equal(t, AttachPaneAgent, agent.Pane)

	h = dispatchExpectYield(t, e, "t")
	term := h.enqueued[0].(QuickInputIntent)
	assert.Equal(t, AttachPaneTerminal, term.Pane)
}

func TestCsActionsSyncPrimitivesCallHost(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	h := &recordingHost{}
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		cs.bind("a", function() cs.actions.cursor_up() end)
		cs.bind("b", function() cs.actions.cursor_down() end)
		cs.bind("c", function() cs.actions.toggle_diff() end)
		cs.bind("d", function() cs.actions.workspace_prev() end)
		cs.bind("e", function() cs.actions.workspace_next() end)
	`))
	e.EndLoad()

	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_, err := e.Dispatch(context.Background(), k, h)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"CursorUp", "CursorDown", "ToggleDiff", "WorkspacePrev", "WorkspaceNext"}, h.calls)
}
