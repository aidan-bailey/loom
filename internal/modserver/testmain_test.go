package modserver

import (
	"fmt"
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

// TestMain runs the package's tests with logging up and the loom dirs
// isolated from the developer's.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns the exit code rather than exiting, so its deferred
// cleanup runs.
func runTests(m *testing.M) int {
	_ = log.Initialize("", false)
	defer log.Close()
	os.Unsetenv("TMUX")
	cleanup, err := testenv.IsolateLoomDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}
