package tmux

import (
	"claude-squad/cmd/cmd_test"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTmuxSession_MonitorInitialized guards the invariant that every
// TmuxSession has a non-nil monitor for its full lifetime. Paused sessions
// are constructed via FromInstanceData without ever calling Restore, so
// leaving monitor nil made HasUpdated / CaptureAndProcess crash if anyone
// bypassed the upstream Paused/TmuxAlive filters.
func TestNewTmuxSession_MonitorInitialized(t *testing.T) {
	session := NewTmuxSession("fresh", "claude")
	require.NotNil(t, session.monitor, "monitor must be initialised by the constructor")
	// Initial hash is nil because nothing has been captured yet; this
	// matches the existing contract tested via GetContentHash.
	assert.Nil(t, session.GetContentHash())
}

// TestCaptureAndProcess_NoPanicWithoutRestore exercises the paused-session
// code path: a TmuxSession built via the constructor without ever calling
// Restore must not panic when HasUpdated and CaptureAndProcess run, and
// must correctly set the "updated" flag on first capture and clear it on
// repeat captures of identical content.
func TestCaptureAndProcess_NoPanicWithoutRestore(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "capture-pane") {
				return []byte("steady output"), nil
			}
			return nil, nil
		},
	}

	ts := newTmuxSession("paused-shape", ProgramClaude, NewMockPtyFactory(t), cmdExec)
	// Deliberately no Restore() — mirrors FromInstanceData's paused path.

	updated, _ := ts.HasUpdated()
	assert.True(t, updated, "first HasUpdated on fresh content must report updated")

	updated, _ = ts.HasUpdated()
	assert.False(t, updated, "second HasUpdated on identical content must report no change")

	_, updated, _, _, _ = ts.CaptureAndProcess()
	assert.False(t, updated, "CaptureAndProcess on identical content must report no change")
}
