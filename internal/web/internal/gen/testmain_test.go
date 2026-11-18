package gen

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
)

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories:
// the package imports internal/web, whose imports reach config, so its tests
// must not resolve the developer's real ~/.loom.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
