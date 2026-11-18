package pprofserve

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/log"
)

// TestMain runs the package's tests with logging up. The package reaches no
// config, so there are no loom dirs to isolate.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns the exit code rather than exiting, so its deferred
// cleanup runs.
func runTests(m *testing.M) int {
	_ = log.Initialize("", false)
	defer log.Close()
	return m.Run()
}
