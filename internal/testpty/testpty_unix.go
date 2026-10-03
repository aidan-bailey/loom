//go:build unix

package testpty

import (
	"os"
	"syscall"
	"testing"
)

// Pair returns a connected, bidirectional pair: attach stands in for the
// PTY a client reads its session's output from and writes keys to, and
// closing peer is what the session ending does to it (reads return EOF).
// attach is non-blocking, so os.NewFile registers it with the runtime
// poller. Both ends are closed when t finishes.
func Pair(t testing.TB) (attach, peer *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("testpty: socketpair: %v", err)
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	if err := syscall.SetNonblock(fds[0], true); err != nil {
		t.Fatalf("testpty: set non-blocking: %v", err)
	}
	attach = os.NewFile(uintptr(fds[0]), "testpty-attach")
	peer = os.NewFile(uintptr(fds[1]), "testpty-peer")
	t.Cleanup(func() {
		_ = peer.Close()
		_ = attach.Close()
	})
	return attach, peer
}
