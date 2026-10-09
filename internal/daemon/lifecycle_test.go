package daemon

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// otherDaemon is a live process that passes for a `loom serve` (servesLoom:
// its arguments say serve), killed when the test ends.
func otherDaemon(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 60; true", "serve")
	require.NoError(t, cmd.Start())
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	})
	require.Eventually(t, func() bool { return servesLoom(cmd.Process.Pid) }, 5*time.Second, 10*time.Millisecond)
	return cmd
}

// A daemon started while another runs, or while another stops, stands down
// at once. Waiting for the lock instead, it took over the moment the other
// stopped: a daemon nobody asked for, running after a `loom serve stop`.
func TestServe_StandsDownAtOnceForALiveDaemon(t *testing.T) {
	dir := globalDir(t)
	other := otherDaemon(t)
	l, _, err := TryAcquire(dir, Record{PID: other.Process.Pid, Build: "b", Socket: "/run/x.sock"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	start := time.Now()
	err = Serve(Options{GlobalDir: dir, LockWait: 10 * time.Second, Stop: make(chan struct{}),
		NewModel: func() (*core.Model, []core.Notice, error) {
			t.Error("built a model without the lock")
			return core.New(core.Options{}), nil, nil
		}})
	require.ErrorIs(t, err, ErrRunning)
	assert.Less(t, time.Since(start), 2*time.Second, "waited for the lock instead of standing down")
}

// Stop is done once the daemon it signalled has exited, whoever holds the
// lock by then: a client may have started a daemon of its own the moment
// the old one let go.
func TestStop_ReturnsOnceItsProcessHasGone(t *testing.T) {
	dir := globalDir(t)
	old := otherDaemon(t)
	l, _, err := TryAcquire(dir, Record{PID: old.Process.Pid, Build: "b", Socket: "/run/x.sock"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) // held on, as by the next daemon
	prev := signalStop
	signalStop = func(int) error {
		_ = old.Process.Kill()
		_ = old.Wait()
		return nil
	}
	t.Cleanup(func() { signalStop = prev })

	require.NoError(t, Stop(dir, 5*time.Second))
}

// startTmuxServer starts a tmux server listening at a socket of its own
// and returns the socket's path; the server is killed when the test ends.
func startTmuxServer(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	require.NoError(t, exec.Command("tmux", "-f", os.DevNull, "-S", sock, "new-session", "-d", "-s", "keep", "sleep 60").Run())
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	return sock
}

// A daemon keeps the last daemon's tmux server while it runs: started from
// an environment that selects another (an ssh login, a client inside
// another server, a newer loom replacing an older daemon), it would find
// every agent dead and relaunch each one there. Once that server has gone,
// this environment's is used.
func TestTmuxServer_KeepsTheLastDaemonsServerWhileItRuns(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	dir := globalDir(t)
	sock := startTmuxServer(t)
	l, _, err := TryAcquire(dir, Record{PID: os.Getpid(), Build: "b", Tmux: sock})
	require.NoError(t, err)
	require.NoError(t, l.Close()) // the last daemon has stopped; its record stays

	got, err := TmuxServer(dir)
	require.NoError(t, err)
	assert.Equal(t, sock, got, "the last daemon's server, while it runs")

	require.NoError(t, exec.Command("tmux", "-S", sock, "kill-server").Run())
	got, err = TmuxServer(dir)
	require.NoError(t, err)
	want, err := tmux.ResolveServer()
	require.NoError(t, err)
	assert.Equal(t, want, got, "this environment's, once that server has gone")
}

// A daemon a client started can stand down for another (it found it
// running), which can then stop before the client dials it: the client
// starts another rather than fail.
func TestConnect_StartsAnotherWhenItsDaemonStoodDown(t *testing.T) {
	dir := globalDir(t)
	stop, served := make(chan struct{}), make(chan struct{})
	spawned := 0 // spawn runs on Connect's goroutine alone
	calls := stubSpawn(t, func() (<-chan error, error) {
		exited := make(chan error, 1)
		if spawned++; spawned == 1 {
			exited <- nil // as `loom serve` exits on ErrRunning
			return exited, nil
		}
		go func() {
			defer close(served)
			exited <- Serve(Options{GlobalDir: dir, Stop: stop,
				NewModel: func() (*core.Model, []core.Notice, error) {
					return core.New(core.Options{CmdExec: deadExec()}), nil, nil
				}})
		}()
		return exited, nil
	})

	nc, _, err := Connect(dir, 30*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() {
		close(stop)
		<-served
	})
	handshake(t, nc).Workspaces()
	assert.Equal(t, int32(2), calls.Load())
}

// A boot relaunches every agent whose session died before the daemon
// listens, which after a reboot can outlast a client's timeout: a live
// daemon still booting is waited for past it.
func TestConnect_WaitsOutALiveDaemonsLongBoot(t *testing.T) {
	dir := globalDir(t)
	booting := otherDaemon(t)
	l, _, err := TryAcquire(dir, Record{PID: booting.Process.Pid, Build: "b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	stubSpawn(t, func() (<-chan error, error) { return nil, errors.New("spawned") })

	type result struct {
		nc  net.Conn
		err error
	}
	done := make(chan result, 1)
	go func() {
		nc, _, err := Connect(dir, 200*time.Millisecond)
		done <- result{nc, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("gave up on a live daemon still booting: %v", r.err)
	case <-time.After(800 * time.Millisecond):
	}

	socket := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "boot.sock")
	ln, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	require.NoError(t, l.Write(Record{PID: booting.Process.Pid, Build: "b", Socket: socket}))
	select {
	case r := <-done:
		require.NoError(t, r.err)
		_ = r.nc.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("never dialed the daemon once it listened")
	}
}

// A runtime crash goes to serve-crash.log, not serve.log: a daemon that
// crashes as it starts is explained by quoting it, and only what it wrote.
func TestConnect_QuotesTheCrashLog(t *testing.T) {
	dir := globalDir(t)
	crash := CrashLogPath(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(crash), 0o755))
	require.NoError(t, os.WriteFile(crash, []byte("an earlier crash\n"), 0o600))
	stubSpawn(t, func() (<-chan error, error) {
		f, err := os.OpenFile(crash, os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		_, _ = f.WriteString("fatal error: concurrent map writes\n")
		_ = f.Close()
		exited := make(chan error, 1)
		exited <- errors.New("exit status 2")
		return exited, nil
	})

	_, _, err := Connect(dir, 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "concurrent map writes")
	assert.NotContains(t, err.Error(), "an earlier crash")
}

// A dev loom run in a loom pane while the user's daemon is down, with
// LOOM_TMUX_SOCKET naming another server, starts a daemon that pins the
// last daemon's server, the very one its pane runs on: the nesting guard
// is decided on that server, so it refuses, whatever the socket names.
func TestNesting_DecidedOnTheServerTheDaemonPins(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	dir := globalDir(t)
	users := startTmuxServer(t) // where the dev loom's pane runs
	l, _, err := TryAcquire(dir, Record{PID: os.Getpid(), Build: "b", Socket: "/x.sock", Tmux: users})
	require.NoError(t, err)
	require.NoError(t, l.Close()) // the user's daemon has stopped; its record stays
	t.Setenv(tmux.EnvTmuxSocket, "devsock")
	env := map[string]string{"TMUX": users + ",1,0", tmux.EnvTmuxSocket: "devsock"}

	server, err := TmuxServer(dir)
	require.NoError(t, err)
	require.Equal(t, users, server, "fixture: the daemon pins the user's server")
	err = tmux.CheckNesting(func(k string) string { return env[k] }, func() (string, error) { return "loom_agent", nil }, server)
	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested, "refused: the daemon would manage the server its pane runs on")
	assert.Equal(t, "loom_agent", nested.Session)
}

// StopPID stops only the daemon it names: one a client dialed and found
// older. Two new looms starting together each dial the old daemon; the
// first stops it and starts its own, and the second must not stop that.
func TestStopPID_StopsOnlyTheDaemonItNames(t *testing.T) {
	noSignal := func(t *testing.T) {
		prev := signalStop
		signalStop = func(pid int) error { t.Errorf("signalled %d", pid); return nil }
		t.Cleanup(func() { signalStop = prev })
	}

	t.Run("another daemon holds the lock now", func(t *testing.T) {
		dir := globalDir(t)
		next := otherDaemon(t)
		l, _, err := TryAcquire(dir, Record{PID: next.Process.Pid, Build: "b", Socket: "/run/x.sock"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		noSignal(t)
		assert.ErrorIs(t, StopPID(dir, deadPID(t), time.Second), ErrNotRunning)
	})

	t.Run("it has died, and its record stays under another holder", func(t *testing.T) {
		// A client's probe, or the next daemon before it writes its own
		// record, holds the lock over the dead daemon's record.
		dir := globalDir(t)
		dead := deadPID(t)
		l, _, err := TryAcquire(dir, Record{PID: dead, Build: "b", Socket: "/run/x.sock"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		noSignal(t)
		assert.ErrorIs(t, StopPID(dir, dead, time.Second), ErrNotRunning)
	})

	t.Run("none holds it", func(t *testing.T) {
		dir := globalDir(t)
		noSignal(t)
		assert.ErrorIs(t, StopPID(dir, deadPID(t), time.Second), ErrNotRunning)
	})

	t.Run("no pid", func(t *testing.T) {
		dir := globalDir(t)
		next := otherDaemon(t)
		l, _, err := TryAcquire(dir, Record{PID: next.Process.Pid, Build: "b", Socket: "/run/x.sock"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		noSignal(t)
		assert.Error(t, StopPID(dir, 0, time.Second))
	})

	t.Run("the daemon it names", func(t *testing.T) {
		dir := globalDir(t)
		r := serve(t, dir, Options{})
		prev := signalStop
		signalled := 0
		signalStop = func(pid int) error {
			signalled = pid
			close(r.stop)
			return nil
		}
		t.Cleanup(func() { signalStop = prev })
		require.NoError(t, StopPID(dir, os.Getpid(), 30*time.Second))
		assert.Equal(t, os.Getpid(), signalled)
		require.NoError(t, <-r.done)
	})
}
