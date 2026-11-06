package flowrun

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

// TestMain isolates the loom dirs: the package reaches config (its state
// files are written with config.AtomicWriteFile).
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	cleanup()
	os.Exit(code)
}
