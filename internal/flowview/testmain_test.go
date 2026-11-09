package flowview

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

// TestMain isolates the loom dirs: the package's tests write run states
// through flowrun, which reaches config (config.AtomicWriteFile).
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	cleanup()
	os.Exit(code)
}
