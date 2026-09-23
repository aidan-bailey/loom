//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
)

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories,
// so nothing this process resolves can reach the developer's real ~/.loom.
// Each sandbox still hands its loom build directories of its own.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
