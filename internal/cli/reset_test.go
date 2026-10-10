package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetCmd is a command for Reset to report through, its stderr captured.
func resetCmd() (*cobra.Command, *bytes.Buffer) {
	var errOut bytes.Buffer
	c := &cobra.Command{}
	c.SetErr(&errOut)
	return c, &errOut
}

// globalRecord stores a paused session in the global workspace, before
// the daemon boots.
func globalRecord(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(os.Getenv(config.EnvGlobalDir), config.StateFileName),
		[]byte(`{"instances":[{"title":"busy","status":3,"program":"true","worktree":{"worktree_path":"/tmp/wt-busy"}}]}`), 0o644))
}

// Reset is the daemon's request, waited for: once it returns, the daemon
// serves no session of the workspace.
func TestReset_TheDaemonResetsTheWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	globalRecord(t)
	d := serveDaemon(t, "true")
	require.Len(t, d.client.Views(d.workspace(t, "").ID), 1, "fixture: the daemon serves the session")
	c, _ := resetCmd()
	var out bytes.Buffer

	require.NoError(t, Reset(c, &out, ""))

	assert.Contains(t, out.String(), "The loom daemon reset global")
	require.Eventually(t, func() bool {
		return len(d.client.Views(d.workspace(t, "").ID)) == 0
	}, 5*time.Second, 10*time.Millisecond)
}

// A reset that fails is reported once, as Reset's error: the daemon also
// shows a failed job as a Notice, which it routes to the request's client,
// and that one is not printed again.
func TestReset_AFailureIsReportedOnce(t *testing.T) {
	isolateWorkspaces(t)
	globalRecord(t)
	serveDaemonOn(t, "true", deadTmux{listErr: errors.New("tmux timed out")})
	c, errOut := resetCmd()
	var out bytes.Buffer

	err := Reset(c, &out, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tmux cleanup failed")
	assert.NotContains(t, errOut.String(), "tmux cleanup failed", "the error is not printed again as a notice")
	assert.NotContains(t, out.String(), "The loom daemon reset")
}
