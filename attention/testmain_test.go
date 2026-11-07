package attention

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	cleanup()
	os.Exit(code)
}
