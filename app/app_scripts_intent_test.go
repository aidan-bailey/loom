package app

import (
	"testing"

	"claude-squad/script"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDispatchScriptDrainsIntents verifies that when a handler
// enqueues an intent (via cs.await(cs.actions.quit())) the resulting
// scriptDoneMsg carries it out of the host. Task 11 adds the handler
// that actually executes the drained intents; this test only covers
// the plumbing between the engine's yield and the app's message bus.
func TestDispatchScriptDrainsIntents(t *testing.T) {
	m := newTestHome(t)
	m.scripts = script.NewEngine(nil)
	defer m.scripts.Close()

	m.scripts.BeginLoad("t.lua")
	require.NoError(t, m.scripts.L.DoString(`
		cs.bind("q", function()
			cs.await(cs.actions.quit())
		end)
	`))
	m.scripts.EndLoad()

	cmd, ok := m.dispatchScript("q")
	require.True(t, ok)

	msg, ok := cmd().(scriptDoneMsg)
	require.True(t, ok, "expected scriptDoneMsg")
	require.Len(t, msg.pendingIntents, 1)
	_, ok = msg.pendingIntents[0].intent.(script.QuitIntent)
	assert.True(t, ok, "expected QuitIntent")
}
