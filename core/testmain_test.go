package core

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests sets up the package test environment and runs the tests. It
// returns the exit code rather than exiting so its deferred cleanup runs.
func runTests(m *testing.M) int {
	// Initialize the logger before any tests run
	_ = log.Initialize("", false)
	defer log.Close()

	// Belt and suspenders: LOOM_TMUX_SOCKET is the only variable
	// tmux.Command consults (an explicit -L outranks $TMUX), but any test
	// that clears it — deliberately or by accident — would otherwise fall
	// through to $TMUX and land on whatever real server encloses this
	// process. Unset it so that fallback can't reach a live developer
	// session, and point TMUX_TMPDIR at a throwaway directory so even a
	// bare `tmux -L <socket>` (no explicit tmpdir) resolves its socket
	// file under a directory this run owns and removes on exit. This must
	// be set up — and its cleanup deferred — before the private-socket kill
	// below is deferred, so RemoveAll runs after (not before) kill-server:
	// defers unwind LIFO, and kill-server needs the socket file's directory
	// to still exist when it runs.
	//
	// Names below are kept short and never hard-code a base directory
	// (os.MkdirTemp("", …) resolves $TMPDIR/os.TempDir(), which may not be
	// /tmp — e.g. a Nix build sandbox uses TMPDIR=/build): tmux's socket
	// path is TMUX_TMPDIR/tmux-<uid>/<name>, and sun_path is capped at 108
	// bytes on Linux, 104 on macOS, where the default $TMPDIR alone can run
	// ~49 bytes.
	os.Unsetenv("TMUX")

	// Keep every config-dir resolution off the developer's real ~/.loom.
	// enterGlobalMode loads config.GlobalWorkspaceContext (LOOM_GLOBAL_DIR);
	// the registry writes workspaces.json there too — SetOpenWorkspaces
	// reloads and saves the on-disk registry even through the bare
	// &config.WorkspaceRegistry{} fleetHome installs — and a nil workspace
	// context resolves LOOM_HOME. Tests that read or seed those directories
	// set their own with t.Setenv; this is the default for the rest.
	loomCleanup, err := testenv.IsolateLoomDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer loomCleanup()

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

	// Point every tmux.Command in this package's tests — and in the loom
	// code they drive — at a private server, never the developer's default
	// one, whose live loom_* sessions a stray orphan sweep or kill would
	// destroy (and where tests used to leave loom_term_* sessions behind).
	// Tests that want a fresh server of their own layer isolateTmux on top.
	// Kill the private server afterwards so nothing outlives the run. The
	// existing -L cleanups (here and in isolateTmux) still resolve the same
	// server: tmux derives the socket path from TMUX_TMPDIR + socket name,
	// and both are fixed for the duration of this process.
	sock := fmt.Sprintf("lt-a-%d", os.Getpid())
	if err := os.Setenv(tmux.EnvTmuxSocket, sock); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", tmux.EnvTmuxSocket, err)
		return 1
	}
	defer func() { _ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run() }()

	return m.Run()
}
