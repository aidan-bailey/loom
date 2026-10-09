package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSpawn replaces spawn for the test, counting its calls.
func stubSpawn(t *testing.T, f func() (<-chan error, error)) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	prev := spawn
	spawn = func(string) (<-chan error, error) {
		calls.Add(1)
		return f()
	}
	t.Cleanup(func() { spawn = prev })
	return calls
}

// spawnInProcess makes spawn start a daemon for dir in this process, as
// `loom serve` would in its own, with o's settings (NewModel defaulted).
// Each daemon started is stopped when the test ends.
func spawnInProcess(t *testing.T, dir string, o Options) *atomic.Int32 {
	t.Helper()
	return stubSpawn(t, func() (<-chan error, error) {
		stop, exited, done := make(chan struct{}), make(chan error, 1), make(chan struct{})
		o := o
		o.GlobalDir, o.Stop = dir, stop
		if o.NewModel == nil {
			o.NewModel = func() (*core.Model, []core.Notice, error) {
				return core.New(core.Options{CmdExec: deadExec()}), nil, nil
			}
		}
		go func() {
			defer close(done)
			exited <- Serve(o)
		}()
		t.Cleanup(func() {
			close(stop)
			<-done
		})
		return exited, nil
	})
}

// handshake says hello on nc and returns the client, closed when the test
// ends.
func handshake(t *testing.T, nc net.Conn) *rpc.Client {
	t.Helper()
	c, err := rpc.Dial(nc)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// A running daemon is dialed at the socket its lock record names, and
// names in its hello the tmux server its sessions run on; none is started.
func TestConnect_DialsARunningDaemon(t *testing.T) {
	dir := globalDir(t)
	r := serve(t, dir, Options{Build: "test build", Tmux: "/run/user/1000/tmux-1000/default"})
	calls := stubSpawn(t, func() (<-chan error, error) { return nil, errors.New("spawned") })

	nc, rec, err := Connect(dir, 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, r.socket, rec.Socket)
	assert.Equal(t, "test build", rec.Build)
	c := handshake(t, nc)
	assert.Equal(t, "/run/user/1000/tmux-1000/default", c.Peer().Tmux)
	c.Workspaces()
	assert.Zero(t, calls.Load())
}

// With no daemon running, Connect starts one and dials it once it listens.
func TestConnect_StartsADaemonWhenNoneRuns(t *testing.T) {
	dir := globalDir(t)
	calls := spawnInProcess(t, dir, Options{})

	nc, rec, err := Connect(dir, 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
	assert.True(t, rec.IsDaemon())
	served := handshake(t, nc).Workspaces()
	require.Len(t, served, 1, "the global workspace")
}

// A daemon listens only once booted, which loads every workspace: Connect
// waits through the boot rather than starting another or giving up.
func TestConnect_WaitsThroughADaemonStillBooting(t *testing.T) {
	dir := globalDir(t)
	booting, release := make(chan struct{}), make(chan struct{})
	calls := spawnInProcess(t, dir, Options{NewModel: func() (*core.Model, []core.Notice, error) {
		close(booting)
		<-release
		return core.New(core.Options{CmdExec: deadExec()}), nil, nil
	}})

	type result struct {
		nc  net.Conn
		err error
	}
	done := make(chan result, 1)
	go func() {
		nc, _, err := Connect(dir, 30*time.Second)
		done <- result{nc, err}
	}()
	<-booting
	var once sync.Once
	finishBoot := func() { once.Do(func() { close(release) }) }
	// Registered after the daemon's own stop, so it runs first: a failing
	// test must not leave the stop waiting on a boot that never ends.
	t.Cleanup(finishBoot)
	select {
	case r := <-done:
		t.Fatalf("Connect returned while the daemon booted: %v", r.err)
	case <-time.After(300 * time.Millisecond):
	}
	rec, held := ReadRecord(dir)
	require.True(t, held)
	assert.False(t, rec.IsPreDaemon(), "a booting daemon's record: %+v", rec)

	finishBoot()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		handshake(t, r.nc).Workspaces()
	case <-time.After(30 * time.Second):
		t.Fatal("Connect never reached the booted daemon")
	}
	assert.Equal(t, int32(1), calls.Load(), "one daemon started")
}

// A daemon may record its socket before it listens: Connect retries it
// while nothing answers there (no file yet, or a dead daemon's file that
// refuses), and dials it once it listens, starting no other.
func TestConnect_RetriesARecordedSocketUntilItListens(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "no file yet"
		if stale {
			name = "a stale file that refuses"
		}
		t.Run(name, func(t *testing.T) {
			dir := globalDir(t)
			socket := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "late.sock")
			if stale {
				ln, err := net.Listen("unix", socket)
				require.NoError(t, err)
				ln.(*net.UnixListener).SetUnlinkOnClose(false)
				require.NoError(t, ln.Close())
				require.FileExists(t, socket)
			}
			l, _, err := TryAcquire(dir, Record{PID: os.Getpid(), Build: "test build", Socket: socket})
			require.NoError(t, err)
			t.Cleanup(func() { _ = l.Close() })
			calls := stubSpawn(t, func() (<-chan error, error) { return nil, errors.New("spawned") })

			type result struct {
				nc  net.Conn
				err error
			}
			done := make(chan result, 1)
			go func() {
				nc, _, err := Connect(dir, 30*time.Second)
				done <- result{nc, err}
			}()
			select {
			case r := <-done:
				t.Fatalf("Connect returned before the daemon listened: %v", r.err)
			case <-time.After(300 * time.Millisecond):
			}
			_ = os.Remove(socket)
			ln, err := net.Listen("unix", socket)
			require.NoError(t, err)
			t.Cleanup(func() { _ = ln.Close() })
			select {
			case r := <-done:
				require.NoError(t, r.err)
				_ = r.nc.Close()
			case <-time.After(30 * time.Second):
				t.Fatal("Connect never dialed the socket once it listened")
			}
			assert.Zero(t, calls.Load())
		})
	}
}

// A loom TUI from before the daemon holds the lock: the two would load and
// write one global dir's sessions each, so Connect refuses, starting
// nothing.
func TestConnect_RefusesALoomFromBeforeTheDaemon(t *testing.T) {
	dir := globalDir(t)
	l, _, err := TryAcquire(dir, Record{PID: os.Getpid(), TTY: "/dev/pts/2", Started: time.Now()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	calls := stubSpawn(t, func() (<-chan error, error) { return nil, errors.New("spawned") })

	_, _, err = Connect(dir, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("a loom from before the daemon is running (pid %d on /dev/pts/2", os.Getpid()))
	assert.Contains(t, err.Error(), "quit it first")
	assert.Zero(t, calls.Load())
}

// The global dir is made before a daemon is started in it, as its working
// directory: a first launch with a LOOM_GLOBAL_DIR nobody has made yet (or
// LOOM_HOME, with no ~/.loom) failed to start any ("no such file or
// directory"). The spawn here fails as the real one's does without it.
func TestConnect_MakesTheGlobalDirBeforeItSpawns(t *testing.T) {
	dir := filepath.Join(globalDir(t), "fresh")
	t.Setenv(config.EnvGlobalDir, dir)
	spawnInProcess(t, dir, Options{})
	inProcess := spawn
	spawn = func(d string) (<-chan error, error) {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("start the loom daemon: chdir %s: no such file or directory", d)
		}
		return inProcess(d)
	}
	t.Cleanup(func() { spawn = inProcess })

	nc, _, err := Connect(dir, 30*time.Second)
	require.NoError(t, err, "the daemon starts in a global dir nobody had made")
	handshake(t, nc).Workspaces()
}

// deadPID is the pid of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

// The lock file keeps the last holder's record, and a loom from before the
// daemon wrote one too. A lock held while it still says so (a daemon that
// has taken it and not yet written its own, a client probing it) is a
// daemon starting, not that loom: Connect waits rather than refuse.
func TestConnect_AStalePreDaemonRecordIsNotRefused(t *testing.T) {
	dir := globalDir(t)
	l, _, err := TryAcquire(dir, Record{PID: deadPID(t), TTY: "/dev/pts/2", Started: time.Now()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	stubSpawn(t, func() (<-chan error, error) { return nil, errors.New("spawned") })

	_, _, err = Connect(dir, 300*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still starting")
	assert.NotContains(t, err.Error(), "from before the daemon")
}

// A daemon that never comes up fails Connect once the timeout passes, with
// the daemon's log named, since that is where it says why.
func TestConnect_TimesOutNamingTheLog(t *testing.T) {
	dir := globalDir(t)
	calls := stubSpawn(t, func() (<-chan error, error) { return make(chan error), nil })

	start := time.Now()
	_, _, err := Connect(dir, 300*time.Millisecond)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Contains(t, err.Error(), filepath.Join(dir, "logs", "serve.log"))
	assert.Equal(t, int32(1), calls.Load(), "started once, not on every retry")
}

// A daemon that exits before it takes the lock (its nesting guard refused,
// say) fails Connect at once, quoting what it logged since it was started,
// not what earlier daemons did.
func TestConnect_ADaemonThatExitsAtOnceSaysWhy(t *testing.T) {
	dir := globalDir(t)
	logPath := LogPath(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	require.NoError(t, os.WriteFile(logPath, []byte("an earlier daemon's line\n"), 0o644))
	stubSpawn(t, func() (<-chan error, error) {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		_, _ = f.WriteString("level=ERROR msg=serve.refused err=\"refusing to start inside a loom-managed tmux session\"\n")
		_ = f.Close()
		exited := make(chan error, 1)
		exited <- errors.New("exit status 1")
		return exited, nil
	})

	start := time.Now()
	_, _, err := Connect(dir, 30*time.Second)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "at once, not at the timeout")
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "refusing to start inside a loom-managed tmux session")
	assert.NotContains(t, err.Error(), "an earlier daemon's line")
}
