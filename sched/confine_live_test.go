//go:build systemd_live && linux

package sched

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// TestConfiner_Live_OutlivesItsDialContext pins that a confiner keeps working
// after the context it was made with ends: the daemon's probes dial with a
// timeout they cancel on return, and godbus closes a connection once its dial
// context is done, so every confiner the daemon made died with its probe
// ("dbus: connection closed by user") and no job was contained.
func TestConfiner_Live_OutlivesItsDialContext(t *testing.T) {
	addr := os.Getenv(testenv.EnvRealDBus)
	if addr == "" {
		t.Skip("no user bus")
	}
	t.Setenv(testenv.EnvDBus, addr)
	dial, cancelDial := context.WithTimeout(context.Background(), 10*time.Second)
	c, err := NewConfiner(dial)
	cancelDial()
	require.NoError(t, err)
	defer c.Close()
	// godbus closes a connection from a goroutine of its own once the
	// context ends; give it the moment that takes.
	time.Sleep(100 * time.Millisecond)

	slice := fmt.Sprintf("loom_test%d_dial.slice", os.Getpid())
	t.Cleanup(func() { removeTestSlice(t, slice) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.EnsureSlice(ctx, slice, SliceProps{CPUWeight: 30, MemoryMaxBytes: 1 << 30}))
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

// TestHelperHoldMemory is the child of TestConfiner_Live_ReclaimsAFrozenScope.
// It waits for a line on stdin, fills LOOM_TEST_HOLD_MB of memory with a
// known pattern and says "ready". It then waits for another line, and exits
// 0 only if the memory still holds the pattern. It does nothing in an
// ordinary run.
func TestHelperHoldMemory(t *testing.T) {
	mb, _ := strconv.Atoi(os.Getenv("LOOM_TEST_HOLD_MB"))
	if mb == 0 {
		t.Skip("only as the lend contract's child")
	}
	in := bufio.NewReader(os.Stdin)
	_, _ = in.ReadString('\n')
	buf := make([]byte, mb<<20)
	for i := range buf {
		buf[i] = byte(i ^ i>>9)
	}
	fmt.Println("ready")
	_, _ = in.ReadString('\n')
	for i := range buf {
		if buf[i] != byte(i^i>>9) {
			fmt.Println("corrupt")
			os.Exit(1)
		}
	}
	fmt.Println("intact")
	os.Exit(0)
}

// TestConfiner_Live_ReclaimsAFrozenScope is the lend's contract (memory
// borrowing spec §11.3): a frozen scope's memory goes to swap through
// CgroupReclaim, and after a thaw its process carries on, its memory
// intact. The child joins its scope before it allocates, since a process
// moved later leaves its memory charged where it was.
func TestConfiner_Live_ReclaimsAFrozenScope(t *testing.T) {
	addr := os.Getenv(testenv.EnvRealDBus)
	if addr == "" {
		t.Skip("no user bus")
	}
	t.Setenv(testenv.EnvDBus, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := NewConfiner(ctx)
	require.NoError(t, err)
	defer c.Close()
	slice := fmt.Sprintf("loom_test%d_lend.slice", os.Getpid())
	unit := fmt.Sprintf("loom-test%d-job-2-1.scope", os.Getpid())
	t.Cleanup(func() { removeTestSlice(t, slice) })
	require.NoError(t, c.EnsureSlice(ctx, slice, SliceProps{CPUWeight: 30, MemoryMaxBytes: 1 << 30}))

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldMemory$")
	cmd.Env = append(os.Environ(), "LOOM_TEST_HOLD_MB=200")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.NoError(t, c.JoinScope(ctx, ScopeSpec{Unit: unit, Slice: slice, CPUWeight: 100}, cmd.Process.Pid))
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", cmd.Process.Pid))
	require.NoError(t, err)
	path, err := parseSelfCgroup(data)
	require.NoError(t, err)

	out := bufio.NewReader(stdout)
	_, err = io.WriteString(stdin, "go\n")
	require.NoError(t, err)
	line, err := out.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", line)
	before, err := strconv.ParseInt(readKnob(t, path, "memory.current"), 10, 64)
	require.NoError(t, err)
	require.Greater(t, before, int64(190<<20), "the child's 200 MB is charged to its scope")

	require.NoError(t, c.Freeze(ctx, unit))
	require.NoError(t, CgroupReclaim(CgroupRoot, path, before>>10))
	after, err := strconv.ParseInt(readKnob(t, path, "memory.current"), 10, 64)
	require.NoError(t, err)
	t.Logf("memory.current %d MB before, %d MB after the reclaim", before>>20, after>>20)
	assert.Less(t, after, before/2, "the frozen scope's memory went to swap")
	if swap, err := os.ReadFile(filepath.Join(CgroupRoot, path, "memory.swap.current")); err == nil {
		n, _ := strconv.ParseInt(strings.TrimSpace(string(swap)), 10, 64)
		t.Logf("memory.swap.current %d MB", n>>20)
		assert.Greater(t, n, int64(50<<20), "and into swap, not only dropped cache")
	}

	require.NoError(t, c.Thaw(ctx, unit))
	_, err = io.WriteString(stdin, "check\n")
	require.NoError(t, err)
	line, err = out.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "intact\n", line)
}
