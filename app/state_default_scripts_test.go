package app

import (
	"testing"

	"github.com/aidan-bailey/loom/script"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultStateRoutesKeysThroughScripts verifies the Task 15
// migration: keys that used to flow through ActionRegistry now flow
// through the Lua engine. "n" is bound in defaults.lua to
// cs.actions.new_instance{}, which must enqueue a NewInstanceIntent
// via the scriptDoneMsg pipeline.
func TestDefaultStateRoutesKeysThroughScripts(t *testing.T) {
	m := newTestHome(t)
	m.scripts = script.NewEngine(buildReservedKeys())
	m.scripts.LoadDefaults()

	_, cmd := handleStateDefaultKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	require.NotNil(t, cmd)

	msg, ok := cmd().(scriptDoneMsg)
	require.True(t, ok, "expected scriptDoneMsg, got %T", cmd())
	require.NoError(t, msg.err)
	require.Len(t, msg.pendingIntents, 1)
	_, ok = msg.pendingIntents[0].intent.(script.NewInstanceIntent)
	assert.True(t, ok, "expected NewInstanceIntent, got %T", msg.pendingIntents[0].intent)
}
