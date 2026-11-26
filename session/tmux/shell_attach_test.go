//go:build !windows

package tmux

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellPtyFactory hands StartShellAttach the master of a real pseudo-terminal
// pair, made pollable as Pty.Start's are, and keeps the slave, which stands
// in for the tmux client: what the test writes to it arrives as the client's
// output, and what the attach writes arrives on it as the client's input.
// A real pair has a real window size, which setPtySize sets and the test
// reads back from the slave.
type shellPtyFactory struct {
	t        *testing.T
	startErr error
	cmds     []*exec.Cmd
	slaves   []*os.File
}

func (f *shellPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	f.t.Cleanup(func() { _ = slave.Close() })
	polled, err := pollable(master)
	if err != nil {
		return nil, err
	}
	f.cmds = append(f.cmds, cmd)
	f.slaves = append(f.slaves, slave)
	return polled, nil
}

func (f *shellPtyFactory) Close() {}

// blockingPtyFactory hands StartShellAttach the master of a real pair as it
// comes, blocking (creack/pty takes its fd through os.File.Fd, which makes it
// so, and no pollable call here undoes that): the master Pty.Start falls back
// to where the poller refuses a PTY (Darwin), on which closing the file does
// not end a pending Read. A
// real child (`sleep`) stands in for the tmux client: it holds the slave as
// its stdio, the factory keeps no copy, so the master reads EIO once the child
// ends, and cmd.Process is set as pty.Start sets it. exited closes when the
// child has been reaped; child is the process, for a test that ends it itself.
type blockingPtyFactory struct {
	t      *testing.T
	exited chan struct{}
	child  *os.Process
}

func (f *blockingPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	child := exec.Command("sleep", "60")
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	if err := child.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return nil, err
	}
	_ = slave.Close()
	cmd.Process = child.Process
	f.child = child.Process
	f.t.Cleanup(func() { _ = child.Process.Kill() })
	go func() {
		_ = child.Wait()
		close(f.exited)
	}()
	return master, nil
}

func (f *blockingPtyFactory) Close() {}

// fixedPtyFactory hands StartShellAttach one file it was given.
type fixedPtyFactory struct{ f *os.File }

func (p fixedPtyFactory) Start(*exec.Cmd) (*os.File, error) { return p.f, nil }
func (p fixedPtyFactory) Close()                            {}

// startShell attaches to "loom_term_demo" through a shellPtyFactory.
func startShell(t *testing.T, cols, rows int) (*ShellAttach, *shellPtyFactory) {
	t.Helper()
	f := &shellPtyFactory{t: t}
	a, err := StartShellAttach("loom_term_demo", cols, rows, f)
	require.NoError(t, err)
	require.Len(t, f.slaves, 1, "fixture: the attach started one client")
	t.Cleanup(func() { _ = a.Close() })
	return a, f
}

// readN reads n bytes from r, failing the test when they do not arrive
// within two seconds.
func readN(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, n)
		_, err := io.ReadFull(r, buf)
		done <- result{buf, err}
	}()
	select {
	case res := <-done:
		require.NoError(t, res.err)
		return string(res.data)
	case <-time.After(2 * time.Second):
		t.Fatal("the bytes never arrived")
		return ""
	}
}

// ptySize is the window size the slave sees.
func ptySize(t *testing.T, slave *os.File) (cols, rows int) {
	t.Helper()
	ws, err := pty.GetsizeFull(slave)
	require.NoError(t, err)
	return int(ws.Cols), int(ws.Rows)
}

func TestStartShellAttach_RunsAttachSessionOnTheExactTarget(t *testing.T) {
	_, f := startShell(t, 80, 24)

	cmd := f.cmds[0]
	assert.Equal(t, []string{"tmux", "-u", "attach-session", "-t", "=loom_term_demo"}, cmd.Args,
		"the exact target, never a bare name that prefix-matches another session")
	require.NotEmpty(t, cmd.Env)
	assert.Equal(t, "TERM=xterm-256color", cmd.Env[len(cmd.Env)-1],
		"a daemon started by a service has no TERM, and tmux refuses a client without one")
}

func TestStartShellAttach_SizesThePTYAtOnce(t *testing.T) {
	_, f := startShell(t, 100, 30)

	cols, rows := ptySize(t, f.slaves[0])

	assert.Equal(t, 100, cols)
	assert.Equal(t, 30, rows)
}

func TestStartShellAttach_RefusesWhatItCannotTargetOrSize(t *testing.T) {
	for _, tc := range []struct {
		name       string
		session    string
		cols, rows int
	}{
		{"an empty name", "", 80, 24},
		{"a name with a colon", "loom_term_a:b", 80, 24},
		{"a name with a dot", "loom_term_a.b", 80, 24},
		{"no columns", "loom_term_demo", 0, 24},
		{"no rows", "loom_term_demo", 80, 0},
		{"negative columns", "loom_term_demo", -1, 24},
		{"more columns than a window size holds", "loom_term_demo", 1 << 16, 24},
		{"more rows than a window size holds", "loom_term_demo", 80, 1 << 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &shellPtyFactory{t: t}

			a, err := StartShellAttach(tc.session, tc.cols, tc.rows, f)

			require.Error(t, err)
			assert.Nil(t, a)
			assert.Empty(t, f.cmds, "no client was started")
		})
	}
}

func TestStartShellAttach_ReturnsAPTYFactoryError(t *testing.T) {
	boom := errors.New("no ptys left")
	f := &shellPtyFactory{t: t, startErr: boom}

	a, err := StartShellAttach("loom_term_demo", 80, 24, f)

	require.ErrorIs(t, err, boom)
	assert.Nil(t, a)
}

// A pty that refuses its size (a socket pair answers ENOTTY) ends the client
// rather than leaving one at a size nobody chose.
func TestStartShellAttach_ASizeThePTYRefusesEndsTheClient(t *testing.T) {
	attach, _ := testpty.Pair(t)

	a, err := StartShellAttach("loom_term_demo", 80, 24, fixedPtyFactory{attach})

	require.Error(t, err)
	assert.Nil(t, a)
	_, werr := attach.Write([]byte("x"))
	assert.ErrorIs(t, werr, os.ErrClosed, "the PTY was closed")
}

func TestShellAttach_ReadsTheClientsRawOutput(t *testing.T) {
	a, f := startShell(t, 80, 24)
	out := "\x1b[?1049h\x1b[31mred"

	_, err := f.slaves[0].Write([]byte(out))
	require.NoError(t, err)

	assert.Equal(t, out, readN(t, a, len(out)), "unfiltered: the alternate-screen switch is still there")
}

func TestShellAttach_WritesGoToTheClientsInput(t *testing.T) {
	a, f := startShell(t, 80, 24)

	n, err := a.Write([]byte("typed\n"))

	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, "typed\n", readN(t, f.slaves[0], 6))
}

func TestShellAttach_ResizeChangesThePTYAndRefusesABadSize(t *testing.T) {
	a, f := startShell(t, 80, 24)

	require.NoError(t, a.Resize(120, 40))
	cols, rows := ptySize(t, f.slaves[0])
	assert.Equal(t, 120, cols)
	assert.Equal(t, 40, rows)

	require.Error(t, a.Resize(0, 40))
	require.Error(t, a.Resize(120, -5))
	require.Error(t, a.Resize(1<<16, 40))
	cols, rows = ptySize(t, f.slaves[0])
	assert.Equal(t, 120, cols, "a refused size changes nothing")
	assert.Equal(t, 40, rows)
}

func TestShellAttach_CloseEndsABlockedReadAndIsSafeTwice(t *testing.T) {
	a, _ := startShell(t, 80, 24)
	read := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 16))
		read <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, a.Close())

	select {
	case err := <-read:
		assert.ErrorIs(t, err, io.EOF, "a Read ended by Close reads EOF")
	case <-time.After(2 * time.Second):
		t.Fatal("Close left a Read blocked")
	}
	assert.NoError(t, a.Close(), "a second Close is safe")
	_, err := a.Write([]byte("x"))
	assert.Error(t, err, "nothing is written after Close")
	assert.Error(t, a.Resize(80, 24), "nor resized")
}

// Where the poller refuses a PTY the master is blocking, and closing it does
// not end a Read in progress: Close must end the client process, which closes
// the slave and so wakes the Read, and it must leave no client attached.
func TestShellAttach_CloseEndsTheClientProcessWhereTheMasterIsBlocking(t *testing.T) {
	f := &blockingPtyFactory{t: t, exited: make(chan struct{})}
	a, err := StartShellAttach("loom_term_demo", 80, 24, f)
	require.NoError(t, err)
	read := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 16))
		read <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, a.Close())

	select {
	case <-f.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("Close left the client process running")
	}
	select {
	case err := <-read:
		assert.ErrorIs(t, err, io.EOF, "the Read the client's end woke reads EOF")
	case <-time.After(2 * time.Second):
		t.Fatal("Close left a Read blocked on a blocking master")
	}
	assert.NoError(t, a.Close(), "a second Close is safe, the client already ended")
}

// A client that ended by itself has been reaped (Pty.Start waits on it), and
// Kill on a reaped process returns os.ErrProcessDone: the first Close is not
// an error for that.
func TestShellAttach_CloseOfAClientThatAlreadyEndedIsNotAnError(t *testing.T) {
	f := &blockingPtyFactory{t: t, exited: make(chan struct{})}
	a, err := StartShellAttach("loom_term_demo", 80, 24, f)
	require.NoError(t, err)
	require.NoError(t, f.child.Kill())
	select {
	case <-f.exited: // killed and reaped, before any Close
	case <-time.After(2 * time.Second):
		t.Fatal("the client did not end")
	}

	assert.NoError(t, a.Close(), "a client that already ended is not an error to Close")
}

// The client ending by itself (the session died, so tmux exits and its end of
// the pty closes) is an error to the reader, never a Read that returns nothing
// and no error.
func TestShellAttach_ReadReturnsAnErrorOnceTheClientEnds(t *testing.T) {
	a, f := startShell(t, 80, 24)
	read := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 16))
		read <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, f.slaves[0].Close())

	select {
	case err := <-read:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a client that ended left a Read blocked")
	}
}
