package daemon

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
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
		o.NewModel = func() (*core.Model, error) { return core.New(core.Options{CmdExec: deadExec()}), nil }
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
		NewModel: func() (*core.Model, error) {
			t.Error("the second daemon built a model")
			return core.New(core.Options{}), nil
		}})
	assert.ErrorIs(t, err, ErrRunning)
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
	r := serve(t, dir, Options{})
	var notices []core.Notice
	for _, ev := range dial(t, r.socket).Sync() {
		if n, ok := ev.(core.Notice); ok {
			notices = append(notices, n)
		}
	}
	require.NotEmpty(t, notices, "the account registry's load error")
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
