//go:build windows

package testpty

import (
	"os"
	"testing"
)

// Pair is the unix Pair's stand-in, so tests still build on Windows,
// where they do not run: both ends are the null device, whose reads
// return EOF at once.
func Pair(t testing.TB) (attach, peer *os.File) {
	t.Helper()
	attach, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("testpty: %v", err)
	}
	peer, err = os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("testpty: %v", err)
	}
	t.Cleanup(func() {
		_ = peer.Close()
		_ = attach.Close()
	})
	return attach, peer
}
