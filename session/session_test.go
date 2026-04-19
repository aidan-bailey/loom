package session

import (
	"claude-squad/log"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	defer log.Close()
	os.Exit(m.Run())
}
