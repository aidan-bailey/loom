package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// shellAttachTerm is the TERM a shell attach client runs under. A daemon
// started by a service has none, and tmux refuses a client without a usable
// one ("open terminal failed"); xterm.js, which draws the client's output,
// is an xterm-256color terminal.
const shellAttachTerm = "TERM=xterm-256color"

// maxShellDim is the largest column or row count a pty's window size holds
// (a uint16).
const maxShellDim = 1<<16 - 1

// ShellAttach is one `tmux attach-session` client in a pty, for the web
// layer's terminal (internal/web): the browser's keys go in through Write
// and the client's raw output comes out of Read. It is not a TmuxSession: it
// holds no emulator, monitor, pump or status state, and nothing here reads
// the pane but the caller. Close ends the client, never the session.
type ShellAttach struct {
	ptmx      *os.File
	cmd       *exec.Cmd // the client; its Process is nil for a factory that starts none (a fake)
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// checkShellSize refuses a size a pty's window size cannot hold.
func checkShellSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > maxShellDim || rows > maxShellDim {
		return fmt.Errorf("the size %dx%d is outside 1..%d", cols, rows, maxShellDim)
	}
	return nil
}

// endShellClient ends a client: it closes the pty, and kills the client
// process where the factory started one. Closing the pty is not enough where
// the master is blocking (Pty.Start falls back to one where the poller refuses
// a PTY, as on Darwin): Close then neither ends a Read in progress nor
// detaches the client, whose end closes the slave and wakes the Read. Process
// is os.Process, which kills the same way on every platform; a client that has
// already gone is not an error.
func endShellClient(ptmx *os.File, cmd *exec.Cmd) error {
	err := ptmx.Close()
	if cmd.Process != nil {
		if kerr := cmd.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
			err = errors.Join(err, fmt.Errorf("kill the client: %w", kerr))
		}
	}
	return err
}

// StartShellAttach attaches a client to the tmux session called name (as
// Reply.Terminal gives it, ready for SessionTarget) in a pty from
// ptyFactory. The pty is set to cols x rows as soon as the client has
// started, as the TUI's attach does, and tmux redraws to it. A name that
// cannot be targeted exactly, or a size a pty cannot hold, starts nothing;
// a pty that refuses its size ends the client (endShellClient).
func StartShellAttach(name string, cols int, rows int, ptyFactory PtyFactory) (*ShellAttach, error) {
	if !ExactlyTargetable(name) {
		return nil, fmt.Errorf("shell attach: %q cannot be targeted exactly", name)
	}
	if err := checkShellSize(cols, rows); err != nil {
		return nil, fmt.Errorf("shell attach: %w", err)
	}
	cmd := Command(context.Background(), "attach-session", "-t", SessionTarget(name))
	cmd.Env = append(cmd.Environ(), shellAttachTerm)
	ptmx, err := ptyFactory.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("shell attach: open the PTY: %w", err)
	}
	if err := setPtySize(ptmx, cols, rows); err != nil {
		_ = endShellClient(ptmx, cmd)
		return nil, fmt.Errorf("shell attach: size the PTY: %w", err)
	}
	return &ShellAttach{ptmx: ptmx, cmd: cmd}, nil
}

// Read reads the client's raw output, unfiltered. It returns io.EOF once
// Close has ended it, and the PTY's own error once the client has ended by
// itself (the session is gone).
func (a *ShellAttach) Read(p []byte) (int, error) {
	n, err := a.ptmx.Read(p)
	if err != nil && a.closed.Load() {
		err = io.EOF
	}
	return n, err
}

// Write sends p to the client as input, which tmux passes to the pane.
func (a *ShellAttach) Write(p []byte) (int, error) {
	return a.ptmx.Write(p)
}

// Resize sets the pty's size, which tmux follows (its latest-client rule
// applies). It goes through setPtySize, so a Read in progress stays
// interruptible.
func (a *ShellAttach) Resize(cols int, rows int) error {
	if err := checkShellSize(cols, rows); err != nil {
		return fmt.Errorf("shell attach: %w", err)
	}
	return setPtySize(a.ptmx, cols, rows)
}

// Close ends the client (endShellClient: the pty closed and the client
// process killed), which detaches it and ends a Read in progress. It never
// closes the session: the shell is the TUI terminal pane's too, and a browser
// leaving must not end it. Safe to call twice.
func (a *ShellAttach) Close() error {
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		a.closeErr = endShellClient(a.ptmx, a.cmd)
	})
	return a.closeErr
}
