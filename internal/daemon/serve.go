package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/log"
)

// ErrRunning is Serve's answer when a daemon already serves the global dir.
var ErrRunning = errors.New("a loom daemon is already running")

// ErrNotRunning is Stop's answer when no daemon serves the global dir.
var ErrNotRunning = errors.New("no loom daemon is running")

// Options configure Serve.
type Options struct {
	// GlobalDir is the global config dir the daemon serves.
	GlobalDir string
	// NewModel builds the model, not yet booted: Serve boots it once it
	// holds the lock, so no two models ever load one global dir.
	NewModel func() (*core.Model, error)
	// Build names this binary in the lock record (rpc.Build).
	Build string
	// Stop is closed to stop the daemon gracefully (on SIGTERM, from
	// `loom serve stop` or a newer loom replacing it).
	Stop <-chan struct{}
	// LockWait is how long to wait for the lock: a client that spawned this
	// daemon may hold it a moment longer. 0 means 5s.
	LockWait time.Duration
	// QuiesceTimeout bounds the wait for in-flight lifecycle jobs when
	// stopping (core.Loop.Quiesce). 0 means 30s.
	QuiesceTimeout time.Duration
	// WatchInterval is how often the daemon checks its socket file still
	// exists (a runtime dir removed at logout, a tmp cleaner) and listens
	// again if not. 0 means 30s.
	WatchInterval time.Duration
	// Serving, when set, is called once the daemon listens: a test seam.
	Serving func(loop *core.Loop, socket string)
}

// Serve runs globalDir's daemon until o.Stop is closed (it then waits for
// in-flight lifecycle jobs, saves every workspace and returns nil) or the
// model is gone (a panic: it returns that error, saving nothing, since the
// model's state is unknown). In order: it takes the lock (ErrRunning when
// another daemon holds it), boots the model, keeps the boot's notices for
// the first client, starts the loop and its tick, listens on its socket
// (SocketPath, mode 0600, recorded in the lock), and serves every
// connection with an rpc.Server. It listens only once booted, so a socket
// that answers is a daemon that is ready.
func Serve(o Options) error {
	if o.LockWait == 0 {
		o.LockWait = 5 * time.Second
	}
	if o.QuiesceTimeout == 0 {
		o.QuiesceTimeout = 30 * time.Second
	}
	if o.WatchInterval == 0 {
		o.WatchInterval = 30 * time.Second
	}
	rec := self(o.Build)
	lock, holder, err := Wait(o.GlobalDir, rec, o.LockWait)
	if errors.Is(err, ErrHeld) {
		if holder.IsDaemon() {
			return fmt.Errorf("%w (%s)", ErrRunning, holder)
		}
		return fmt.Errorf("a loom from before the daemon is running (%s): quit it first", holder)
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	model, err := o.NewModel()
	if err != nil {
		return fmt.Errorf("build the model: %w", err)
	}
	notices := model.Boot()
	loop := core.Start(model)
	srv := rpc.NewServer(loop)
	for _, ev := range notices {
		if n, ok := ev.(core.Notice); ok {
			srv.Keep(n)
		}
	}
	loop.Begin()

	socket, err := SocketPath(o.GlobalDir)
	if err != nil {
		srv.Close()
		loop.Stop()
		return err
	}
	l := &listener{path: socket, srv: srv}
	if err := l.listen(); err != nil {
		srv.Close()
		loop.Stop()
		return err
	}
	rec.Socket = socket
	if err := lock.Write(rec); err != nil {
		log.For("serve").Warn("serve.record_failed", "err", err)
	}
	log.For("serve").Info("serve.listening", "socket", socket, "pid", rec.PID, "build", o.Build)
	if o.Serving != nil {
		o.Serving(loop, socket)
	}

	watch := time.NewTicker(o.WatchInterval)
	defer watch.Stop()
	for {
		select {
		case <-o.Stop:
			log.For("serve").Info("serve.stopping")
			l.close()
			srv.Close()
			if !loop.Quiesce(o.QuiesceTimeout) {
				log.For("serve").Warn("serve.stopped_with_jobs_in_flight")
			}
			if err := saveForStop(loop); err != nil {
				log.For("serve").Error("serve.save_failed", "err", err)
			}
			loop.Stop()
			l.remove()
			log.For("serve").Info("serve.stopped")
			return nil
		case <-srv.Fatal():
			fatal := srv.FatalError()
			log.For("serve").Error("serve.model_gone", "err", fatal)
			l.close()
			srv.Close()
			loop.Stop()
			l.remove()
			return fmt.Errorf("the model failed, and the daemon stops: %w", fatal)
		case <-watch.C:
			l.watch()
		}
	}
}

// saveForStop saves every workspace the model serves, as a stop's last
// step. A failure is returned (and logged), not retried: the daemon is
// going, and the records it could not write keep their last saved state.
func saveForStop(loop *core.Loop) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("save on stop: %v", r)
		}
	}()
	return loop.SaveForQuit()
}

// listener is the daemon's socket and the goroutine accepting on it.
type listener struct {
	path string
	srv  *rpc.Server

	mu sync.Mutex
	ln net.Listener
	// ino is the socket file's inode, to tell it from one put in its place.
	ino uint64
}

// listen binds the socket, replacing a stale file a dead daemon left (the
// lock, which this process holds, proves no live one uses it), makes it
// private (net.Listen follows the umask), and accepts on it.
func (l *listener) listen() error {
	_ = os.Remove(l.path)
	ln, err := net.Listen("unix", l.path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", l.path, err)
	}
	// Closing would unlink the path whatever file is there by then (one put
	// in its place, once the watch has listened again): remove checks the
	// inode first, so it alone deletes.
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(l.path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("make %s private: %w", l.path, err)
	}
	l.mu.Lock()
	l.ln, l.ino = ln, inode(l.path)
	l.mu.Unlock()
	go l.accept(ln)
	return nil
}

// accept serves every connection ln accepts, until ln closes.
func (l *listener) accept(ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		l.srv.Serve(nc)
	}
}

// watch listens again when the socket file is gone or replaced: a runtime
// dir removed at the last logout, or a tmp cleaner, would otherwise leave a
// daemon no client can reach, holding the lock that stops another starting.
func (l *listener) watch() {
	l.mu.Lock()
	ino := l.ino
	l.mu.Unlock()
	if ino != 0 && inode(l.path) == ino {
		return
	}
	log.For("serve").Warn("serve.socket_gone", "socket", l.path)
	l.close()
	if err := privateDir(filepath.Dir(l.path)); err != nil {
		log.For("serve").Error("serve.relisten_failed", "err", err)
		return
	}
	if err := l.listen(); err != nil {
		log.For("serve").Error("serve.relisten_failed", "err", err)
	}
}

// close stops accepting; connections already served stay open.
func (l *listener) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln != nil {
		_ = l.ln.Close()
		l.ln = nil
	}
}

// remove deletes the socket file, if it is still this daemon's.
func (l *listener) remove() {
	l.mu.Lock()
	ino := l.ino
	l.mu.Unlock()
	if ino != 0 && inode(l.path) == ino {
		_ = os.Remove(l.path)
	}
}

// inode is path's inode number, 0 when it can't be read.
func inode(path string) uint64 {
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

// signalStop asks the process pid to stop gracefully: Serve's caller turns
// SIGTERM into a closed Stop. A test seam.
var signalStop = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// Stop stops globalDir's daemon gracefully and waits, up to timeout, until
// it has released its lock. It signals the pid its lock record names rather
// than asking over the socket, so it works whatever protocol the daemon
// speaks (a newer loom replacing an older daemon). ErrNotRunning when no
// daemon holds the lock; a loom from before the daemon holding it is left
// alone.
func Stop(globalDir string, timeout time.Duration) error {
	rec, held := ReadRecord(globalDir)
	if !held {
		return ErrNotRunning
	}
	if !rec.IsDaemon() {
		return fmt.Errorf("the lock is held by a loom from before the daemon (%s): quit it first", rec)
	}
	if rec.PID <= 0 {
		return fmt.Errorf("the daemon's lock record names no process")
	}
	if err := signalStop(rec.PID); err != nil {
		return fmt.Errorf("stop the loom daemon (%s): %w", rec, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, held := ReadRecord(globalDir); !held {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the loom daemon (%s) did not stop within %s: see serve.log", rec, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
