package tmux

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// PtyFactory abstracts creation of a pseudo-terminal for the tmux
// subprocess so tests can inject a scripted fake instead of opening a
// real PTY device. The production implementation is [Pty]; tests supply
// a stub that records invocations and returns a synthetic *os.File.
type PtyFactory interface {
	// Start launches cmd attached to a freshly-allocated PTY and returns
	// the controlling file descriptor. Ownership transfers to the caller,
	// who must Close the returned file when done.
	Start(cmd *exec.Cmd) (*os.File, error)
	// Close releases any factory-level resources. The default Pty has none,
	// but a mock factory may use it to flush recorded state.
	Close()
}

// Pty is the production [PtyFactory] implementation. It wraps
// github.com/creack/pty so every new tmux session gets a real PTY that
// tmux can drive with terminal control sequences.
type Pty struct{}

// Start allocates a PTY, launches cmd attached to it, and returns the
// master file descriptor. The caller closes the returned file when the
// session ends.
func (pt Pty) Start(cmd *exec.Cmd) (*os.File, error) {
	return pty.Start(cmd)
}

// Close is a no-op for the production factory — each PTY is owned by
// its caller, not by the factory.
func (pt Pty) Close() {}

// MakePtyFactory returns the production [PtyFactory]. Call sites that
// need a fake for tests should construct one inline rather than
// shadowing this function.
func MakePtyFactory() PtyFactory {
	return Pty{}
}
