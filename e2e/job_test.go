//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A job outlives the daemon that started it: its runner is in a session of
// its own, so `loom serve stop` leaves it running, and the next daemon (the
// next command's join starts it) adopts it at boot and settles it, without
// starting it again. `loom job wait` returns the job's own exit code.
func TestE2E_Job_SurvivesADaemonRestart(t *testing.T) {
	sb := newSandbox(t, "")
	marks := t.TempDir()
	runs := filepath.Join(marks, "runs")
	// The job counts its starts, says it is up, outlasts the restart and
	// exits 7.
	script := `echo run >> "$1/runs"; echo up; sleep 8; echo finished; exit 7`
	job := func(args ...string) (string, error) {
		// An empty LOOM_LAUNCH makes the command a plain shell's, whatever
		// shell runs the suite.
		return runLoom(t, sb, append([]string{"env", "LOOM_LAUNCH=", sb.LoomBin(), "job"}, args...)...)
	}

	out, err := job("submit", "-d", "restart", "--no-notify", "--", "sh", "-c", script, "sh", marks)
	require.NoError(t, err, out)
	assert.Contains(t, out, "job 1 started")
	require.Eventually(t, func() bool {
		_, err := os.Stat(runs)
		return err == nil
	}, uiTimeout, 100*time.Millisecond, "the job never began to run")
	rec := servingDaemon(t, sb)
	started, err := sched.ReadStarted(sched.JobDir(filepath.Join(sb.GlobalDir(), "jobs"), 1))
	require.NoError(t, err)

	stopped, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, stopped)
	require.True(t, waitExit(rec.PID, uiTimeout))
	_, held := sb.Daemon()
	require.False(t, held, "no daemon runs")
	assert.True(t, alive(started.PID), "the job's runner outlives the daemon that started it")

	// This join starts the next daemon, which adopts the job.
	out, err = job("wait", "1")
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, out)
	assert.Equal(t, 7, exit.ExitCode(), out)
	assert.Contains(t, out, "finished", "the job's output reaches stdout")
	assert.Contains(t, out, "job 1 failed in")
	assert.Contains(t, out, "(exit 7)")
	servingDaemon(t, sb, rec.PID)
	got, err := os.ReadFile(runs)
	require.NoError(t, err)
	assert.Equal(t, "run\n", string(got), "the new daemon adopted the job and did not start it again")
}
