//go:build systemd_live && linux

package sched

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/testenv"
	sddbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readKnob reads one file of a cgroup, trimmed.
func readKnob(t *testing.T, path, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(CgroupRoot, path, name))
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

// removeTestSlice stops the test's slice and removes the runtime drop-in
// SetUnitProperties left for it, so a run leaves nothing in the user's
// manager.
func removeTestSlice(t *testing.T, slice string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := sddbus.NewUserConnectionContext(ctx)
	if err != nil {
		t.Logf("stop %s: %v", slice, err)
		return
	}
	defer conn.Close()
	done := make(chan string, 1)
	if _, err := conn.StopUnitContext(ctx, slice, "replace", done); err == nil {
		<-done
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		_ = os.RemoveAll(filepath.Join(dir, "systemd", "user.control", slice+".d"))
	}
}

// TestConfiner_Live runs the confiner against the real user manager, on a
// test slice and scope of its own (cgroup spec §13.3). Opt in with
// -tags systemd_live; it is skipped without a user bus.
func TestConfiner_Live(t *testing.T) {
	addr := os.Getenv(testenv.EnvRealDBus)
	if addr == "" {
		t.Skip("no user bus")
	}
	t.Setenv(testenv.EnvDBus, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := NewConfiner(ctx)
	require.NoError(t, err)
	defer c.Close()

	slice := fmt.Sprintf("loom_test%d.slice", os.Getpid())
	unit := fmt.Sprintf("loom-test%d-job-1-1.scope", os.Getpid())
	t.Cleanup(func() { removeTestSlice(t, slice) })
	require.NoError(t, c.EnsureSlice(ctx, slice, SliceProps{CPUWeight: 30, MemoryMaxBytes: 1 << 30}))

	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	require.NoError(t, c.JoinScope(ctx, ScopeSpec{Unit: unit, Slice: slice, CPUWeight: 200}, cmd.Process.Pid))

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", cmd.Process.Pid))
	require.NoError(t, err)
	path, err := parseSelfCgroup(data)
	require.NoError(t, err)
	require.Equal(t, unit, filepath.Base(path))
	require.Equal(t, slice, filepath.Base(filepath.Dir(path)))
	assert.True(t, strings.HasSuffix(filepath.Dir(filepath.Dir(path)), ".service"), "the slice sits directly under the user's manager, beside the panes: %s", path)
	assert.Equal(t, "200", readKnob(t, path, "cpu.weight"))
	assert.Equal(t, "1", readKnob(t, path, "memory.oom.group"), "OOMPolicy=kill")
	assert.Equal(t, "30", readKnob(t, filepath.Dir(path), "cpu.weight"))
	assert.Equal(t, "1073741824", readKnob(t, filepath.Dir(path), "memory.max"))

	s, err := ReadCgroup(CgroupRoot, path)
	require.NoError(t, err)
	assert.True(t, s.Populated)

	require.NoError(t, c.SetMemoryHigh(ctx, unit, 512<<20))
	assert.Equal(t, "536870912", readKnob(t, path, "memory.high"))
	require.NoError(t, c.SetMemoryHigh(ctx, unit, 0))
	assert.Equal(t, "max", readKnob(t, path, "memory.high"))

	require.NoError(t, c.Freeze(ctx, unit))
	require.Eventually(t, func() bool { s, _ := ReadCgroup(CgroupRoot, path); return s.Frozen }, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, c.Thaw(ctx, unit))
	require.Eventually(t, func() bool { s, _ := ReadCgroup(CgroupRoot, path); return !s.Frozen }, 5*time.Second, 50*time.Millisecond)

	require.NoError(t, CgroupKill(CgroupRoot, path))
	_, _ = cmd.Process.Wait()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(CgroupRoot, path))
		return os.IsNotExist(err)
	}, 10*time.Second, 100*time.Millisecond, "the scope is collected")
}
