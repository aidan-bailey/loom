//go:build !windows

package tmux

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// readOnce reads f once on its own goroutine and reports the error.
func readOnce(f *os.File) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, err := f.Read(buf)
		done <- err
	}()
	return done
}

// A pane client's stop closes its PTY and waits for the output pump, which
// sits in a read whenever the pane is idle: the close must end that read,
// or every stop waits out pumpWaitTimeout.
func TestPty_CloseEndsAnIdleRead(t *testing.T) {
	f, err := Pty{}.Start(exec.Command("sleep", "30"))
	require.NoError(t, err)
	done := readOnce(f)
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, f.Close())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closing the PTY left a read on an idle pane blocked")
	}
}

// A resize must keep reads interruptible: creack/pty's own Setsize takes the
// fd through os.File.Fd, which restores blocking mode on a file the os
// package made non-blocking.
func TestPty_ADeadlineStillEndsAReadAfterAResize(t *testing.T) {
	f, err := Pty{}.Start(exec.Command("sleep", "30"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	require.NoError(t, setPtySize(f, 100, 30))

	require.NoError(t, f.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
	select {
	case err := <-readOnce(f):
		require.True(t, errors.Is(err, os.ErrDeadlineExceeded), "the read ended on its deadline, got %v", err)
	case <-time.After(time.Second):
		t.Fatal("a read deadline did not end a read after a resize")
	}
}
