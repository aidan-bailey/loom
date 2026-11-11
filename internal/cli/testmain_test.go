package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/sched"
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
	// A job's runner here is this test binary (sched.TestRunner): when the
	// daemon starts it as a shim, hand over to the shim before anything else.
	sched.MaybeRunTestShim()
	// No test may replace this binary: a test that reaches the re-exec
	// without its own fake (fakeExec) is refused.
	execProcess = func(string, []string, []string) error { return errors.New("no exec in tests") }
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
