package session

import (
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	defer log.Close()
	// Argv-exact mock assertions (reconcile_test.go) assume the default server.
	os.Unsetenv(tmux.EnvTmuxSocket)
	os.Exit(m.Run())
}
