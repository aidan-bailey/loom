package cli

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/session/tmux"
)

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories,
// so no test here can resolve the developer's real ~/.loom. Tests that
// need directories of their own still t.Setenv over them.
//
// It also points every tmux command at a private server for the whole
// run. A test daemon's background jobs (the launch watch, probes) outlive
// its loop's Stop and the test's own t.Setenv of the socket, so a job
// still running afterwards would otherwise reach the developer's default
// server, where loom's live sessions are.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	sock := fmt.Sprintf("lt-cli-%d", os.Getpid())
	if err := os.Setenv(tmux.EnvTmuxSocket, sock); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", tmux.EnvTmuxSocket, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run()
	cleanup()
	os.Exit(code)
}
