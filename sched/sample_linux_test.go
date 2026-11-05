//go:build linux

package sched

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSession starts sh -c script as the leader of a session of its own,
// as the daemon starts a shim, and kills what is left of it when the test
// ends: every process of the session, whatever its group.
func startSession(t *testing.T, script string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, cmd.Start())
	sid := cmd.Process.Pid
	t.Cleanup(func() {
		if snap, err := Sample(context.Background()); err == nil {
			for _, p := range snap.Procs {
				if p.SID == sid && p.PID != sid {
					_ = syscall.Kill(p.PID, syscall.SIGKILL)
				}
			}
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return sid
}

// TestSample_ASessionKeepsItsReapedChildrensCPU: children that ran and were
// reaped between two samples still count, in their parent's children's time.
func TestSample_ASessionKeepsItsReapedChildrensCPU(t *testing.T) {
	sid := startSession(t, `for k in 1 2 3; do sh -c 'i=0; while [ $i -lt 200000 ]; do i=$((i+1)); done'; done; exec sleep 30`)
	require.Eventually(t, func() bool {
		comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", sid))
		return err == nil && strings.TrimSpace(string(comm)) == "sleep"
	}, 60*time.Second, 20*time.Millisecond, "the busy children to finish")

	snap, err := Sample(context.Background())
	require.NoError(t, err)
	_, cpu, members := snap.Session(sid)
	assert.Equal(t, 1, members, "only the sleep is left")
	assert.GreaterOrEqual(t, cpu, 100*time.Millisecond, "the three reaped children's CPU time")
}

// TestSample_ASessionKeepsAChildThatChangedGroup: coreutils timeout moves
// itself and its command into a group of their own; the session keeps them.
func TestSample_ASessionKeepsAChildThatChangedGroup(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("timeout is not installed")
	}
	sid := startSession(t, "timeout 30 sleep 30; true")
	var snap Snapshot
	require.Eventually(t, func() bool {
		s, err := Sample(context.Background())
		if err != nil {
			return false
		}
		snap = s
		_, _, members := s.Session(sid)
		return members == 3
	}, 10*time.Second, 20*time.Millisecond, "sh, timeout and sleep in the session")
	_, _, members, _ := snap.Group(sid)
	assert.Equal(t, 1, members, "only sh is left in the leader's group")
}
