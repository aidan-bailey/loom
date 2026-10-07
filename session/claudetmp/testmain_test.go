package claudetmp

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	os.Exit(code)
}
