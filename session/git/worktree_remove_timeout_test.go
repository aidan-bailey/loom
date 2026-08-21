package git

import (
	"os/exec"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowRemoveRunner fakes a git whose `worktree remove` takes `d` (a big
// target/ tree on a loaded box) while every other command returns at once.
func slowRemoveRunner(d time.Duration) cmd_test.MockCmdExec {
	slow := func(c *exec.Cmd) ([]byte, error) {
		for _, a := range c.Args {
			if a == "remove" {
				time.Sleep(d)
				break
			}
		}
		return []byte{}, nil
	}
	return cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := slow(c); return err },
		OutputFunc:         slow,
		CombinedOutputFunc: slow,
	}
}

func withTimeouts(t *testing.T, tick, remove time.Duration) {
	t.Helper()
	prevTick, prevRemove := gitTimeout, gitWorktreeRemoveTimeout
	gitTimeout, gitWorktreeRemoveTimeout = tick, remove
	t.Cleanup(func() { gitTimeout, gitWorktreeRemoveTimeout = prevTick, prevRemove })
}

// TestRemove_NotBoundByTickTimeout pins the 2026-08-21 gutting: `worktree
// remove` ran under the 8s metadata-tick budget, was SIGKILLed mid-delete
// on a large tree, and left a half-removed worktree (.git unlinked,
// sources partly gone). A remove that outlives gitTimeout must be allowed
// to finish.
func TestRemove_NotBoundByTickTimeout(t *testing.T) {
	withTimeouts(t, 20*time.Millisecond, 5*time.Second)
	gw := NewGitWorktreeFromStorageWithRunner("/repo", "/repo/.loom/wt", "s", "b", "", false, t.TempDir(), slowRemoveRunner(120*time.Millisecond))

	require.NoError(t, gw.Remove(), "a remove slower than the tick budget must still complete")
}

// TestRemove_HasItsOwnDeadline proves the remove path is bounded by
// gitWorktreeRemoveTimeout, not unbounded — a truly hung git must still
// surface as an error rather than pinning Pause forever.
func TestRemove_HasItsOwnDeadline(t *testing.T) {
	withTimeouts(t, 5*time.Second, 20*time.Millisecond)
	gw := NewGitWorktreeFromStorageWithRunner("/repo", "/repo/.loom/wt", "s", "b", "", false, t.TempDir(), slowRemoveRunner(120*time.Millisecond))

	err := gw.Remove()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 20ms", "the error must report the remove deadline that was actually applied")
}

// TestRunGitCommand_StillBoundByTickTimeout guards the other direction:
// ordinary commands keep the short budget the metadata tick depends on.
func TestRunGitCommand_StillBoundByTickTimeout(t *testing.T) {
	withTimeouts(t, 20*time.Millisecond, 5*time.Second)
	sleepy := func(c *exec.Cmd) ([]byte, error) { time.Sleep(120 * time.Millisecond); return []byte{}, nil }
	gw := NewGitWorktreeFromStorageWithRunner("/repo", "/repo/.loom/wt", "s", "b", "", false, t.TempDir(), cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { _, err := sleepy(c); return err }, OutputFunc: sleepy, CombinedOutputFunc: sleepy,
	})

	_, err := gw.runGitCommand("/repo", "status")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 20ms")
}
