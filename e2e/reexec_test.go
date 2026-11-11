//go:build e2e

package e2e

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A flow wait of an older loom, waiting when a newer build replaces the
// daemon, carries on under the newer build (feedback thread 16): its
// rejoin is refused as newer, it re-executes the daemon's executable in
// place, keeping its PID, and the new process follows the job to its end.
// Before, it was refused at every rejoin and gave up.
func TestE2E_Flow_AWaitCarriesOnUnderANewerDaemon(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed: flow's config script needs it")
	}
	sb := newSandbox(t, "")
	newer := buildLoom(t, sb, "loom-99", "-X main.version=99.0.0")
	// Plain shells: no launch, so no owner; no flow root, so this build's.
	plain := []string{"env", "LOOM_LAUNCH=", "LOOM_FLOW_ROOT="}
	loom := func(bin string, args ...string) []string {
		return append(append(append([]string{}, plain...), bin), args...)
	}

	// The sandbox's own build, the older one, starts the daemon and the
	// step: a job that outlasts the upgrade.
	out, err := runLoom(t, sb, loom(sb.LoomBin(), "flow", "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 12; echo finished")...)
	require.NoError(t, err, out)
	old := servingDaemon(t, sb)

	waiter := sb.Cmd(loom(sb.LoomBin(), "flow", "wait", "wait", "slow", "60")...)
	var waited bytes.Buffer
	waiter.Stdout, waiter.Stderr = &waited, &waited
	require.NoError(t, waiter.Start())
	pid := waiter.Process.Pid
	ended := make(chan error, 1)
	go func() { ended <- waiter.Wait() }()
	t.Cleanup(func() { _ = waiter.Process.Kill() })
	time.Sleep(time.Second) // the waiter joins the older daemon and follows the job

	// The newer build replaces the daemon.
	out, err = runLoom(t, sb, loom(newer, "job", "list")...)
	require.NoError(t, err, out)
	fresh := servingDaemon(t, sb, old.PID)
	if exe := exeOf(fresh.PID); exe != "" {
		require.Equal(t, newer, exe, "the newer build serves")
	}
	if exe := exeOf(pid); exe != "" {
		require.Eventually(t, func() bool { return exeOf(pid) == newer }, uiTimeout, 100*time.Millisecond,
			"the waiter's PID runs the newer build:\n%s", waited.String())
	}

	select {
	case err = <-ended:
	case <-time.After(2 * uiTimeout):
		t.Fatalf("the waiter never ended:\n%s", waited.String())
	}
	text := waited.String()
	require.NoError(t, err, "the job ended within the wait, exit 0:\n%s", text)
	assert.Contains(t, text, "carrying on under it ("+newer+")")
	assert.Contains(t, text, "done slow exit 0")
	assert.NotContains(t, text, "the loom daemon was upgraded, and only the new loom")
}
