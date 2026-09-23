package overlay

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
)

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories,
// so no test here can resolve the developer's real ~/.loom. Tests that
// need directories of their own still t.Setenv over them.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
