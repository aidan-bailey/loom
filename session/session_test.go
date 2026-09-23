package session

import (
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns the exit code rather than exiting, so its deferred
// cleanup runs.
func runTests(m *testing.M) int {
	_ = log.Initialize("", false)
	defer log.Close()
	// Worktrees built with an empty ConfigDir, storage and sweep scopes
	// resolve the config dirs: keep them off the developer's ~/.loom.
	defer testenv.MustIsolateLoomDirs()()
	// Argv-exact mock assertions (reconcile_test.go) assume the default server.
	os.Unsetenv(tmux.EnvTmuxSocket)
	return m.Run()
}
