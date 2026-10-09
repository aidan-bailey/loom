package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestStop_HasKeepDaemon(t *testing.T) {
	root := newRootCmd(io.Discard, io.Discard)
	stop, _, err := root.Find([]string{"stop"})
	require.NoError(t, err)
	assert.NotNil(t, stop.Flags().Lookup("keep-daemon"))
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
