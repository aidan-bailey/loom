package daemon

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deadExec is a machine with no live tmux session and no claude: every
// command fails, every output is empty.
func deadExec() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, &exec.ExitError{} },
	}
}

// globalDir is a fresh global config dir, which the global workspace
// resolves (config.GlobalWorkspaceContext), and a short runtime dir, so
// the socket lands there.
func globalDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	rt, err := os.MkdirTemp("", "rt")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)
	return dir
}

// running is a daemon serving on its own goroutine.
type running struct {
	socket string
	loop   *core.Loop
	stop   chan struct{}
	done   chan error
}

// shutdown stops the daemon gracefully and returns what Serve returned.
func (r *running) shutdown(t *testing.T) error {
	t.Helper()
	close(r.stop)
	select {
	case err := <-r.done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon did not stop")
		return nil
	}
}

// serve runs a daemon for dir with o's settings (NewModel, Stop and Serving
// filled in) and waits until it listens. It is stopped when the test ends
// if the test did not.
func serve(t *testing.T, dir string, o Options) *running {
	t.Helper()
	r := &running{stop: make(chan struct{}), done: make(chan error, 1)}
	ready := make(chan struct{})
	o.GlobalDir = dir
	o.Stop = r.stop
	if o.NewModel == nil {
		o.NewModel = func() (*core.Model, []core.Notice, error) {
			return core.New(core.Options{CmdExec: deadExec()}), nil, nil
		}
	}
	o.Serving = func(loop *core.Loop, socket string) {
		r.loop, r.socket = loop, socket
		close(ready)
	}
	go func() { r.done <- Serve(o) }()
	select {
	case <-ready:
	case err := <-r.done:
		t.Fatalf("Serve returned before serving: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon never listened")
	}
	t.Cleanup(func() {
		select {
		case <-r.stop:
		default:
			close(r.stop)
			<-r.done
		}
	})
	return r
}

// dial connects a client to the daemon's socket.
func dial(t *testing.T, socket string) *rpc.Client {
	t.Helper()
	nc, err := net.Dial("unix", socket)
	require.NoError(t, err)
	c, err := rpc.Dial(nc)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// A daemon serves its global dir's model on a private socket it records in
// its lock, and on stop saves every workspace, removes the socket and
// releases the lock.
func TestServe_ServesItsGlobalDirUntilStopped(t *testing.T) {
	dir := globalDir(t)
	r := serve(t, dir, Options{Build: "test build"})

	rec, held := ReadRecord(dir)
	require.True(t, held)
	assert.Equal(t, os.Getpid(), rec.PID)
	assert.Equal(t, r.socket, rec.Socket)
	assert.Equal(t, "test build", rec.Build)
	assert.True(t, strings.HasPrefix(r.socket, os.Getenv("XDG_RUNTIME_DIR")), "in the runtime dir: %s", r.socket)
	info, err := os.Stat(r.socket)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "only this user may connect")

	c := dial(t, r.socket)
	served := c.Workspaces()
	require.Len(t, served, 1)
	assert.Empty(t, served[0].Name, "the global workspace")

	state := filepath.Join(dir, config.StateFileName)
	_ = os.Remove(state)
	require.NoError(t, r.shutdown(t))
	assert.FileExists(t, state, "every workspace is saved on stop")
	assert.NoFileExists(t, r.socket)
	_, held = ReadRecord(dir)
	assert.False(t, held, "the lock is released")
}

// Two daemons on one global dir would both load and write its sessions:
// the second refuses while the first holds the lock.
func TestServe_RefusesASecondDaemon(t *testing.T) {
	dir := globalDir(t)
	serve(t, dir, Options{})
	err := Serve(Options{GlobalDir: dir, LockWait: 100 * time.Millisecond, Stop: make(chan struct{}),
		NewModel: func() (*core.Model, []core.Notice, error) {
			t.Error("the second daemon built a model")
			return core.New(core.Options{}), nil, nil
		}})
	assert.ErrorIs(t, err, ErrRunning)
}

// Two clients starting a daemon each is harmless: the second daemon finds
// the first's lock, even while the first still boots (its record has a
// build but no socket yet), and stops as already running. A loom from
// before the daemon holding the lock is named as such.
func TestServe_TheLockHoldersRecordSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		holder Record
		check  func(t *testing.T, err error)
	}{
		{"a daemon still booting", Record{PID: 4242, Build: "v0.13.1"}, func(t *testing.T, err error) {
			assert.ErrorIs(t, err, ErrRunning)
		}},
		{"a loom from before the daemon", Record{PID: 3713275, TTY: "/dev/pts/2"}, func(t *testing.T, err error) {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "a loom from before the daemon is running")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := globalDir(t)
			l, _, err := TryAcquire(dir, tc.holder)
			require.NoError(t, err)
			t.Cleanup(func() { _ = l.Close() })
			tc.check(t, Serve(Options{GlobalDir: dir, LockWait: 100 * time.Millisecond, Stop: make(chan struct{}),
				NewModel: func() (*core.Model, []core.Notice, error) {
					t.Error("built a model without the lock")
					return core.New(core.Options{}), nil, nil
				}}))
		})
	}
}

// A socket file removed under a running daemon (a runtime dir cleared at
// logout, a tmp cleaner) would leave a daemon no client can reach, holding
// the lock that stops another starting: it listens again.
func TestServe_ARemovedSocketIsListenedAgain(t *testing.T) {
	dir := globalDir(t)
	r := serve(t, dir, Options{WatchInterval: 20 * time.Millisecond})
	require.NoError(t, os.Remove(r.socket))
	require.Eventually(t, func() bool {
		nc, err := net.Dial("unix", r.socket)
		if err != nil {
			return false
		}
		_ = nc.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)
	dial(t, r.socket).Workspaces()
}

// The boot raises its notices before any client can connect: the first one
// is shown them.
func TestServe_TheBootsNoticesReachTheFirstClient(t *testing.T) {
	dir := globalDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "accounts.json"), []byte("{not json"), 0o600))
	r := serve(t, dir, Options{NewModel: func() (*core.Model, []core.Notice, error) {
		return core.New(core.Options{CmdExec: deadExec()}), []core.Notice{{Info: "the registry would not load"}}, nil
	}})
	var infos []string
	var errs int
	for _, ev := range dial(t, r.socket).Sync() {
		if n, ok := ev.(core.Notice); ok {
			if n.Err != nil {
				errs++
			}
			infos = append(infos, n.Info)
		}
	}
	assert.Positive(t, errs, "the account registry's load error")
	assert.Contains(t, infos, "the registry would not load", "what building the model raised")
}

// A model that panicked is as good as gone: the daemon exits, saving
// nothing, since the model's state is unknown, and gives up its socket and
// lock so the next loom starts a fresh one.
func TestServe_StopsWhenTheModelIsGone(t *testing.T) {
	dir := globalDir(t)
	r := serve(t, dir, Options{})
	c := dial(t, r.socket)
	func() {
		defer func() { _ = recover() }()
		r.loop.DeliverForTest(core.StartResult{}) // a nil instance panics the model
	}()
	func() {
		defer func() { _ = recover() }()
		_ = c.FlushForTest()
	}()
	select {
	case err := <-r.done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the model failed")
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon kept running over a failed model")
	}
	close(r.stop)
	assert.NoFileExists(t, r.socket)
	_, held := ReadRecord(dir)
	assert.False(t, held)
}

// Stop stops a running daemon gracefully (here: by closing its Stop, as the
// SIGTERM handler does) and waits until it has released its lock; it leaves
// a loom from before the daemon alone, and says when nothing runs.
func TestStop(t *testing.T) {
	t.Run("nothing running", func(t *testing.T) {
		assert.ErrorIs(t, Stop(globalDir(t), time.Second), ErrNotRunning)
	})

	t.Run("a loom from before the daemon", func(t *testing.T) {
		dir := globalDir(t)
		l, _, err := TryAcquire(dir, Record{PID: 12345})
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		prev := signalStop
		signalStop = func(int) error { t.Error("signalled a TUI"); return nil }
		t.Cleanup(func() { signalStop = prev })
		err = Stop(dir, time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "before the daemon")
	})

	t.Run("a daemon on another host", func(t *testing.T) {
		dir := globalDir(t)
		l, _, err := TryAcquire(dir, Record{PID: 12345, Build: "b", Socket: "/run/x.sock", Host: "elsewhere.invalid"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		prev := signalStop
		signalStop = func(int) error { t.Error("signalled a pid of another host"); return nil }
		t.Cleanup(func() { signalStop = prev })
		err = Stop(dir, time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "elsewhere.invalid")
	})

	t.Run("a daemon", func(t *testing.T) {
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
		require.NoError(t, Stop(dir, 30*time.Second))
		assert.Equal(t, os.Getpid(), signalled)
		require.NoError(t, <-r.done)
		_, held := ReadRecord(dir)
		assert.False(t, held)
	})
}

// The socket goes where a client of any environment can find it through the
// lock record: the runtime dir, else the global dir when the path fits a
// socket address, else a private dir in /tmp; always in a dir only this
// user can enter.
func TestSocketPath(t *testing.T) {
	t.Run("the runtime dir", func(t *testing.T) {
		dir := globalDir(t)
		p, err := SocketPath(dir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "loom", globalHash(dir)+".sock"), p)
		info, err := os.Stat(filepath.Dir(p))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	})

	t.Run("no runtime dir: the global dir", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "g")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		t.Setenv("XDG_RUNTIME_DIR", "")
		p, err := SocketPath(dir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "run", "serve.sock"), p)
	})

	t.Run("a global dir too deep for a socket address: /tmp", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
		t.Setenv("XDG_RUNTIME_DIR", "")
		p, err := SocketPath(dir)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(p), maxSocketPath)
		assert.Contains(t, p, "loom-")
		assert.True(t, strings.HasSuffix(p, globalHash(dir)+".sock"))
	})

	t.Run("a symlinked runtime dir is not trusted", func(t *testing.T) {
		dir := globalDir(t)
		rt := os.Getenv("XDG_RUNTIME_DIR")
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(rt, "loom")))
		p, err := SocketPath(dir)
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(p, rt), "fell back: %s", p)
	})

	t.Run("two spellings of one global dir share a daemon", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(dir, link))
		assert.Equal(t, globalHash(dir), globalHash(link))
	})
}

// A stop waits for the lifecycle jobs in flight (a pause, a kill), so none
// is cut off mid-step, and saves once they have landed.
func TestServe_AStopWaitsForJobsInFlight(t *testing.T) {
	dir := globalDir(t)
	r := serve(t, dir, Options{QuiesceTimeout: 10 * time.Second})
	release := make(chan struct{})
	r.loop.SpawnForTest(func() any { <-release; return nil }, false)
	close(r.stop)
	select {
	case err := <-r.done:
		t.Fatalf("stopped with a job in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-r.done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon never stopped")
	}
}

// heldGH is a gh whose `issue view` blocks until release is closed (and
// says so on started), then answers with issue 5; anything else fails.
func heldGH(started chan<- struct{}, release <-chan struct{}) cmd_test.MockCmdExec {
	view := func(c *exec.Cmd) ([]byte, error) {
		if !slices.Contains(c.Args, "issue") || !slices.Contains(c.Args, "view") {
			return nil, errors.New("no gh here")
		}
		started <- struct{}{}
		<-release
		return []byte(`{"number":5,"title":"held","state":"OPEN"}`), nil
	}
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return errors.New("no gh here") },
		OutputFunc: view,
	}
}

// logSince is what the test log gained since it was from bytes long.
func logSince(t *testing.T, from int64) string {
	t.Helper()
	data, err := os.ReadFile(log.LogFilePath())
	require.NoError(t, err)
	if int64(len(data)) < from {
		return string(data)
	}
	return string(data[from:])
}

// A graceful stop says bye to every client first and takes nothing new,
// but the request in flight when it came still gets its Reply: its job
// finishes and publishes to the connections still open, the workspaces
// are saved, and only then do the connections close, which the client
// reads as a stop, not a crash.
func TestServe_StopSaysByeAndAnswersInFlightRequests(t *testing.T) {
	dir := globalDir(t)
	logFrom := fileSize(log.LogFilePath())
	started, release := make(chan struct{}, 1), make(chan struct{})
	var once bool
	releaseGH := func() {
		if !once {
			once = true
			close(release)
		}
	}
	r := serve(t, dir, Options{QuiesceTimeout: 20 * time.Second, NewModel: func() (*core.Model, []core.Notice, error) {
		return core.New(core.Options{CmdExec: deadExec(), GHExec: heldGH(started, release)}), nil, nil
	}})
	t.Cleanup(releaseGH) // runs before the daemon's own stop: a failing test must not hold it
	c := dial(t, r.socket)

	c.FetchIssue(dir, 5, 9)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the request's job never ran")
	}
	close(r.stop)
	require.Eventually(t, c.Stopping, 10*time.Second, 5*time.Millisecond, "the client heard the bye")
	assert.NoError(t, c.Err(), "the connection stays open while the job runs")
	_, err := c.Open(1)
	assert.ErrorIs(t, err, core.ErrUnavailable, "a new request is refused")
	select {
	case err := <-r.done:
		t.Fatalf("stopped with a request in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	releaseGH()
	select {
	case err := <-r.done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the daemon never stopped")
	}
	require.Eventually(t, func() bool { return c.Err() != nil }, 10*time.Second, 5*time.Millisecond, "the connection closed")
	var replies []core.Reply
	for _, ev := range c.Sync() {
		if rep, ok := ev.(core.Reply); ok {
			replies = append(replies, rep)
		}
	}
	require.Len(t, replies, 1, "the in-flight request's Reply came before the close")
	assert.Equal(t, core.ReqID(9), replies[0].Req)
	assert.NoError(t, replies[0].Err)
	assert.Equal(t, 5, replies[0].Issue.Number)
	assert.ErrorIs(t, c.Err(), core.ErrUnavailable, "the loss is a stop, not a crash")

	logged := logSince(t, logFrom)
	assert.Contains(t, logged, "serve.stopped")
	assert.NotContains(t, logged, "serve.stopped_with_jobs_in_flight")
}

// A stop that comes before a model's failure is published must neither
// wait on nor save a model whose state is unknown, nor crash the daemon
// re-raising its panic: it reports it, with nothing saved.
func TestStopModel_AFailedModelIsNotSaved(t *testing.T) {
	loop := core.Start(core.New(core.Options{CmdExec: deadExec()}))
	t.Cleanup(loop.Stop)
	func() {
		defer func() { _ = recover() }()
		loop.DeliverForTest(core.StartResult{}) // a nil instance panics the model
	}()
	var err error
	require.NotPanics(t, func() { err = stopModel(loop, time.Second) })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing saved")
}

// A runtime dir removed under a running daemon (the last logout) and not
// made again: the daemon listens wherever SocketPath finds a place now, and
// rewrites its record so clients find it there.
func TestServe_ASocketWhoseDirIsGoneMovesElsewhere(t *testing.T) {
	dir := globalDir(t)
	rt := os.Getenv("XDG_RUNTIME_DIR")
	r := serve(t, dir, Options{WatchInterval: 20 * time.Millisecond})
	require.True(t, strings.HasPrefix(r.socket, rt))
	require.NoError(t, os.RemoveAll(rt))
	require.NoError(t, os.WriteFile(rt, nil, 0o600), "a file where the runtime dir was: it can't be made again")

	var rec Record
	require.Eventually(t, func() bool {
		rec, _ = ReadRecord(dir)
		return rec.Socket != "" && rec.Socket != r.socket
	}, 5*time.Second, 10*time.Millisecond, "the record still names the gone socket")
	assert.False(t, strings.HasPrefix(rec.Socket, rt))
	dial(t, rec.Socket).Workspaces()
}

// A host with nowhere to put the socket fails before the boot, which
// sweeps sessions and relaunches agents, not after it.
func TestServe_FindsAPlaceToListenBeforeItBoots(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", maxSocketPath))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	t.Setenv(config.EnvGlobalDir, dir)
	t.Setenv("XDG_RUNTIME_DIR", blocked)
	t.Setenv("TMPDIR", blocked)

	err := Serve(Options{GlobalDir: dir, Stop: make(chan struct{}),
		NewModel: func() (*core.Model, []core.Notice, error) {
			t.Error("booted with nowhere to listen")
			return core.New(core.Options{CmdExec: deadExec()}), nil, nil
		}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no place for the loom daemon's socket")
	_, held := ReadRecord(dir)
	assert.False(t, held, "the lock is released")
}

// flakyListener fails its first Accept as a full file table would, then
// hands out what conns sends, and is closed when conns is.
type flakyListener struct {
	net.Listener // only Accept is called
	conns        chan net.Conn
	failed       bool
}

func (f *flakyListener) Accept() (net.Conn, error) {
	if !f.failed {
		f.failed = true
		return nil, syscall.EMFILE
	}
	c, ok := <-f.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

// A failed Accept (too many open files) is waited out: a daemon that
// listened but never accepted again would hold the lock unreachable.
func TestListener_AFailedAcceptIsWaitedOut(t *testing.T) {
	loop := core.Start(core.New(core.Options{CmdExec: deadExec()}))
	srv := rpc.NewServer(loop)
	t.Cleanup(func() {
		srv.Close()
		loop.Stop()
	})
	fl := &flakyListener{conns: make(chan net.Conn, 1)}
	l := &listener{srv: srv}
	accepting := make(chan struct{})
	go func() {
		defer close(accepting)
		l.accept(fl)
	}()

	a, b := net.Pipe()
	fl.conns <- a
	dialed := make(chan error, 1)
	go func() {
		c, err := rpc.Dial(b)
		if err == nil {
			c.Close()
		}
		dialed <- err
	}()
	select {
	case err := <-dialed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the connection after a failed accept was never served")
	}
	close(fl.conns)
	select {
	case <-accepting:
	case <-time.After(5 * time.Second):
		t.Fatal("accept outlived its listener")
	}
}

// A lock record is rewritten in place, so a reader can meet it torn: a held
// lock whose record does not parse yet is read again, not taken for a
// holder that names nothing.
func TestReadRecord_WaitsOutATornRecord(t *testing.T) {
	dir := globalDir(t)
	want := Record{PID: 4242, Build: "b", Socket: "/run/x.sock"}
	l, _, err := TryAcquire(dir, want)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	require.NoError(t, os.WriteFile(LockPath(dir), []byte(`{"pid":42`), 0o644))
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = l.Write(want)
	}()
	rec, held := ReadRecord(dir)
	require.True(t, held)
	assert.Equal(t, want.Socket, rec.Socket)
	assert.Equal(t, want.PID, rec.PID)
}

// Only a process that may be a loom daemon is signalled: never one whose
// arguments don't say serve, which a stale or torn record could name.
func TestServesLoom(t *testing.T) {
	if !procMounted() {
		t.Skip("no /proc")
	}
	assert.False(t, servesLoom(os.Getpid()), "this test binary")
	cmd := exec.Command("sh", "-c", "sleep 30; true", "serve")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	assert.Eventually(t, func() bool { return servesLoom(cmd.Process.Pid) }, 5*time.Second, 10*time.Millisecond)
}
