package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// privateTmux points this test's tmux commands at a fresh private server
// and kills it when the test ends, skipping where tmux is not installed.
// $TMUX is cleared and TMUX_TMPDIR is a short throwaway directory, so
// nothing can fall through to the developer's (or CI's) server, and the
// socket path stays under sun_path's 104/108-byte cap (see
// TestCaptureHistoryRealTmux). The attach clients Restore spawns need a
// usable TERM on tty-less CI runners.
func privateTmux(t *testing.T, tag string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH; skipping real-tmux test")
	}
	t.Setenv("TERM", "xterm-256color")
	dir, err := os.MkdirTemp("", "lt")
	if err != nil {
		t.Fatalf("mkdir tmux tmpdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	sock := fmt.Sprintf("lt-%s-%d", tag, os.Getpid())
	t.Setenv(EnvTmuxSocket, sock)
	t.Cleanup(func() { _ = CommandOnSocket(context.Background(), sock, "kill-server").Run() })
}

// newRawSession starts a detached session called name running cmd on the
// test's private server, bypassing TmuxSession (whose own naming and
// targeting are what the tests exercise).
func newRawSession(t *testing.T, name, cmd string) {
	t.Helper()
	if out, err := Command(context.Background(), "new-session", "-d", "-s", name, "-x", "80", "-y", "24", cmd).CombinedOutput(); err != nil {
		t.Fatalf("start %s: %v: %s", name, err, out)
	}
}

// serverSessions lists the session names on the test's private server.
func serverSessions(t *testing.T) []string {
	t.Helper()
	out, err := Command(context.Background(), "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil // no server: no sessions
	}
	return strings.Fields(string(out))
}
