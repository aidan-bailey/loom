package ui

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/aidan-bailey/loom/session/tmux"
)

// TestMain isolates this package's tests from the developer's real tmux
// server. Most ui tests drive tmux through a MockCmdExec and never exec the
// real binary, but preview_test.go's setupTestEnvironment does run one real
// `tmux kill-session -t loom_test-preview-*` as best-effort cleanup before
// creating its mocked session — and any future test that does the same.
// Without this, that call follows LOOM_TMUX_SOCKET (unset) through $TMUX (if
// running inside a loom pane, e.g. via tools/loomdev) or the default socket,
// which is exactly the path a prior incident used to kill real agent
// sessions (see loom-smoke-test-isolation-hazard in project memory). Mirrors
// app/app_test.go's runTests.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// Unset TMUX first and point TMUX_TMPDIR at a throwaway directory so
	// even a bare `tmux -L <socket>` (no explicit tmpdir) resolves its
	// socket file under a directory this run owns and removes on exit —
	// and so any tmux invocation that ends up without an explicit -L can
	// never reach the enclosing server via $TMUX. Cleanup is deferred
	// before the private-socket kill-server below so RemoveAll runs after
	// (not before) it: defers unwind LIFO, and kill-server needs the
	// socket file's directory to still exist when it runs.
	//
	// Names below are kept short and never hard-code a base directory
	// (os.MkdirTemp("", …) resolves $TMPDIR/os.TempDir(), which may not be
	// /tmp — e.g. a Nix build sandbox uses TMPDIR=/build): tmux's socket
	// path is TMUX_TMPDIR/tmux-<uid>/<name>, and sun_path is capped at 108
	// bytes on Linux, 104 on macOS, where the default $TMPDIR alone can run
	// ~49 bytes.
	os.Unsetenv("TMUX")
	tmuxTmpDir, err := os.MkdirTemp("", "lt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdir tmux tmpdir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmuxTmpDir)
	if err := os.Setenv("TMUX_TMPDIR", tmuxTmpDir); err != nil {
		fmt.Fprintf(os.Stderr, "set TMUX_TMPDIR: %v\n", err)
		return 1
	}

	// Point every real tmux.Command in this package's tests at a private
	// server, never the developer's default one. Kill it afterwards so
	// nothing outlives the run.
	sock := fmt.Sprintf("lt-u-%d", os.Getpid())
	if err := os.Setenv(tmux.EnvTmuxSocket, sock); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", tmux.EnvTmuxSocket, err)
		return 1
	}
	defer func() { _ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run() }()

	return m.Run()
}
