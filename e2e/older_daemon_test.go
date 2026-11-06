//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A newer loom run inside one of loom's own tmux sessions, on the daemon's
// server, never replaces the older daemon (replaceGuard), but its
// subcommands use that daemon as it is: a rebuilt binary must not stop an
// agent's `loom job run` until the owner restarts the daemon. The command
// runs in a real loom_* session, so replaceGuard reads the $TMUX tmux gives
// the pane, as it does in an agent's.
func TestE2E_Job_ANewerLoomInsideLoomUsesTheOlderDaemon(t *testing.T) {
	sb := newSandbox(t, "")
	newer := buildLoom(t, sb, "loom-99", "-X main.version=99.0.0")

	// The sandbox's own build starts the daemon: it is the older one.
	out, err := runLoom(t, sb, "env", claudemod.EnvLaunch+"=", sb.LoomBin(), "job", "list")
	require.NoError(t, err, out)
	rec := servingDaemon(t, sb)

	dir := t.TempDir()
	result, code := filepath.Join(dir, "out"), filepath.Join(dir, "code")
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	for _, kv := range sb.Env() {
		name, value, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&script, "export %s=%s\n", name, shellQuote(value))
	}
	// The pane is a plain shell's, with no link to any daemon's launch.
	fmt.Fprintf(&script, "unset %s %s %s\n", claudemod.EnvSocket, claudemod.EnvLaunch, sched.EnvJob)
	fmt.Fprintf(&script, "cd %s || exit 99\n", shellQuote(sb.RepoDir()))
	fmt.Fprintf(&script, "%s job run -d inside -- sh -c 'echo from-the-job; exit 3' >%s 2>&1\n",
		shellQuote(newer), shellQuote(result))
	fmt.Fprintf(&script, "echo $? >%s.tmp && mv %s.tmp %s\n", shellQuote(code), shellQuote(code), shellQuote(code))
	scriptPath := filepath.Join(dir, "inside.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script.String()), 0o755))

	const session = tmux.TmuxPrefix + "e2einside"
	require.NoError(t, tmux.CommandOnSocket(context.Background(), sb.Socket(),
		"new-session", "-d", "-s", session, "sh", scriptPath).Run())
	t.Cleanup(func() {
		_ = tmux.CommandOnSocket(context.Background(), sb.Socket(), "kill-session", "-t="+session).Run()
	})

	var got []byte
	require.Eventually(t, func() bool {
		got, err = os.ReadFile(code)
		return err == nil
	}, 2*uiTimeout, 200*time.Millisecond, "the command in the loom session never finished")
	printed, err := os.ReadFile(result)
	require.NoError(t, err)
	exit, err := strconv.Atoi(strings.TrimSpace(string(got)))
	require.NoError(t, err)

	assert.Equal(t, 3, exit, "the job's own exit code, not 125:\n%s", printed)
	assert.Contains(t, string(printed), "from-the-job", "the job ran on the older daemon")
	assert.Contains(t, string(printed), "loom: using the loom daemon as it is (")
	assert.NotContains(t, string(printed), "nothing was submitted")
	now, held := sb.Daemon()
	require.True(t, held, "a daemon still serves")
	assert.Equal(t, rec.PID, now.PID, "the older daemon was not replaced")
	assert.True(t, alive(rec.PID))
	serveLog, err := os.ReadFile(daemon.LogPath(sb.GlobalDir()))
	require.NoError(t, err)
	assert.NotContains(t, string(serveLog), "msg=serve.stopped", "nothing stopped the daemon")
}

// shellQuote quotes s for sh, single quotes and all.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
