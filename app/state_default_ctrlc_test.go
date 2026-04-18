package app

import (
	"fmt"
	"testing"

	"claude-squad/script"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCtrlCQuitsEvenAfterUnbind proves ctrl+c remains a panic-exit
// backstop even when a user script has unbound it. Without this
// hard-reserve a broken or malicious script could trap the user in
// the TUI with no keyboard escape; the only recourse would be
// SIGKILL from another terminal.
func TestCtrlCQuitsEvenAfterUnbind(t *testing.T) {
	m := newTestHome(t)
	m.scripts = script.NewEngine(buildReservedKeys())

	// ctrl+c is in buildReservedKeys, so this unbind is already a
	// no-op at the engine level — but we issue it anyway to model a
	// user script that thinks it can capture ctrl+c. The Go-level
	// short-circuit in handleStateDefaultKey is what actually
	// guarantees the escape hatch.
	m.scripts.BeginLoad("t.lua")
	require.NoError(t, m.scripts.L.DoString(`cs.unbind("ctrl+c")`))
	m.scripts.EndLoad()

	_, cmd := handleStateDefaultKey(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	assert.Equal(t, "tea.QuitMsg", fmt.Sprintf("%T", cmd()))
}
