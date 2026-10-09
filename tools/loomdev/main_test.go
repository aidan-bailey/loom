package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd(&out, &out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestRoot_HasEverySubcommand(t *testing.T) {
	root := newRootCmd(io.Discard, io.Discard)
	var names []string
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	for _, want := range []string{"up", "build", "run", "start", "stop", "keys", "shot", "wait", "logs", "env", "ls", "down"} {
		assert.Contains(t, names, want)
	}
}

func TestEnv_PrintsExports(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, err := execute(t, "env", "--sandbox", "demo")
	require.NoError(t, err)
	assert.Contains(t, out, "export LOOM_TMUX_SOCKET='loomdev-demo'\n")
	assert.Contains(t, out, "export LOOM_GLOBAL_DIR='")
	assert.Contains(t, out, "export LOOM_HOME='")
}

func TestSandboxFlag_RejectsBadNames(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, err := execute(t, "env", "--sandbox", "../evil")
	assert.Error(t, err)
}

func TestKeys_RequiresArgs(t *testing.T) {
	_, err := execute(t, "keys", "--sandbox", "demo")
	assert.Error(t, err)
}

func TestWait_RequiresText(t *testing.T) {
	_, err := execute(t, "wait", "--sandbox", "demo")
	assert.Error(t, err)
}

func TestWait_RejectsEmptyText(t *testing.T) {
	_, err := execute(t, "wait", "--sandbox", "demo", "--text", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--text must not be empty")
}

func TestLs_EmptyBase(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, err := execute(t, "ls")
	require.NoError(t, err)
	assert.Contains(t, out, "no sandboxes")
}

func TestParseSize(t *testing.T) {
	w, h, err := parseSize("160x48")
	require.NoError(t, err)
	assert.Equal(t, 160, w)
	assert.Equal(t, 48, h)
	for _, bad := range []string{"x", "0x10", "10x0", "abc", "10x"} {
		_, _, err := parseSize(bad)
		assert.Error(t, err, bad)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestFollowLogs_StreamsAppendedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom.log")
	require.NoError(t, os.WriteFile(path, []byte("old line\n"), 0o644))
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- followLogs(ctx, &out, []string{path}, 20*time.Millisecond) }()

	time.Sleep(60 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("new line\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.Eventually(t, func() bool { return strings.Contains(out.String(), "new line") }, 2*time.Second, 20*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	assert.NotContains(t, out.String(), "old line", "follow starts at the current end")
}

func TestDriverFlag_RejectsLoomSessionNames(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, bad := range []string{"loom_x", "a:b", "Bad"} {
		_, err := execute(t, "shot", "--sandbox", "demo", "--driver", bad)
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "invalid driver session name", bad)
	}
}

// fakeSandbox opens a sandbox of a fresh name under a fresh state dir,
// with its global dir made.
func fakeSandbox(t *testing.T) *devsandbox.Sandbox {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sb, err := devsandbox.Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.GlobalDir(), 0o755))
	return sb
}

// startFakeDaemon runs exe (the test binary, or a copy of it) as a fake
// daemon of sb's global dir in mode (runFakeDaemon), and waits until it
// holds the lock. It is killed at cleanup if still running.
func startFakeDaemon(t *testing.T, sb *devsandbox.Sandbox, exe, mode string) *exec.Cmd {
	t.Helper()
	// "serve": daemon.Stop signals only a process whose command line has it.
	cmd := exec.Command(exe, "serve")
	cmd.Env = append(os.Environ(), fakeDaemonEnv+"="+sb.GlobalDir(), fakeDaemonModeEnv+"="+mode)
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	require.Eventually(t, func() bool {
		rec, held := sb.Daemon()
		return held && rec.PID == cmd.Process.Pid
	}, 10*time.Second, 20*time.Millisecond, "the fake daemon never took the lock")
	return cmd
}

// testBinary is the running test binary, a fake daemon's executable.
func testBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return exe
}

// sandboxBuild copies the test binary into sb's bin dir as its loom, so a
// fake daemon run from there is a build of the sandbox's own.
func sandboxBuild(t *testing.T, sb *devsandbox.Sandbox) string {
	t.Helper()
	data, err := os.ReadFile(testBinary(t))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.BinDir(), 0o755))
	require.NoError(t, os.WriteFile(sb.LoomBin(), data, 0o755))
	return sb.LoomBin()
}

// gone reports whether the process cmd started has ended.
func gone(cmd *exec.Cmd) bool {
	return errors.Is(cmd.Process.Signal(syscall.Signal(0)), os.ErrProcessDone)
}

// stop --keep-daemon quits the TUI only, as a real quit does; a plain stop
// stops the sandbox's daemon too.
func TestStop_KeepDaemonLeavesTheDaemonRunning(t *testing.T) {
	sb := fakeSandbox(t)
	cmd := startFakeDaemon(t, sb, testBinary(t), "")

	_, err := execute(t, "stop", "--sandbox", sb.Name, "--keep-daemon")
	require.NoError(t, err)
	rec, held := sb.Daemon()
	assert.True(t, held && rec.PID == cmd.Process.Pid, "--keep-daemon leaves the daemon running")
	assert.False(t, gone(cmd))

	_, err = execute(t, "stop", "--sandbox", sb.Name)
	require.NoError(t, err)
	_, held = sb.Daemon()
	assert.False(t, held, "stop stops the daemon")
	assert.Eventually(t, func() bool { return gone(cmd) }, 5*time.Second, 20*time.Millisecond)
}

// ls shows the pid of the process holding a sandbox's lock, and its
// socket, or that it is still starting.
func TestLs_ShowsTheDaemonsPidAndSocket(t *testing.T) {
	sb := fakeSandbox(t)
	serving := startFakeDaemon(t, sb, testBinary(t), "")
	booting, err := devsandbox.Open(sb.Name + "-booting")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(booting.GlobalDir(), 0o755))
	starting := startFakeDaemon(t, booting, testBinary(t), fakeBooting)

	out, err := execute(t, "ls")
	require.NoError(t, err)
	assert.Regexp(t, fmt.Sprintf(`(?m)^%s\s+down\s+pid %d\s+%s\s`, sb.Name, serving.Process.Pid,
		regexp.QuoteMeta(filepath.Join(sb.GlobalDir(), "fake.sock"))), out)
	assert.Regexp(t, fmt.Sprintf(`(?m)^%s\s+down\s+pid %d\s+\(starting\)\s`, booting.Name, starting.Process.Pid), out)
}

// down stops at a sandbox loom that won't stop, naming --force, which
// kills it and removes the sandbox.
func TestDown_ForceKillsASandboxLoomThatWontStop(t *testing.T) {
	sb := fakeSandbox(t)
	cmd := startFakeDaemon(t, sb, sandboxBuild(t, sb), fakePreDaemon)

	_, err := execute(t, "down", "--sandbox", sb.Name)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loomdev down --force")
	assert.DirExists(t, sb.Dir)

	out, err := execute(t, "down", "--sandbox", sb.Name, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, "removed "+sb.Dir)
	assert.NoDirExists(t, sb.Dir)
	assert.Eventually(t, func() bool { return gone(cmd) }, 5*time.Second, 20*time.Millisecond)
}

func TestLs_ShowsTheDaemonColumns(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	require.NoError(t, os.MkdirAll(filepath.Join(state, "loom-dev", "demo"), 0o755))
	out, err := execute(t, "ls")
	require.NoError(t, err)
	assert.Contains(t, out, "DAEMON")
	assert.Contains(t, out, "SOCKET")
	assert.Regexp(t, `demo\s+down\s+down\s+-`, out, "no server, no daemon")
}

func TestLogs_SaysWhenNoDaemonRuns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, err := execute(t, "logs", "--sandbox", "demo")
	require.NoError(t, err)
	assert.Contains(t, out, "daemon: not running")
}
