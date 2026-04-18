package script

import (
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
		_, err := e.Dispatch(k, h)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"CursorUp", "CursorDown", "ToggleDiff", "WorkspacePrev", "WorkspaceNext"}, h.calls)
}
