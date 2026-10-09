package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
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
	// holds the lock, so no two models ever load one global dir. The notices
	// it returns (a registry that would not load, say) are kept for the
	// first client with the boot's.
	NewModel func() (*core.Model, []core.Notice, error)
	// Build names this binary in the lock record (rpc.Build).
	Build string
	// Tmux is the tmux server the model's sessions run on (its socket's
	// path), which the daemon's hello names so its clients use it too. The
	// caller pins it for this process first (tmux.UseServer).
	Tmux string
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
	// again if not. 0 means 2s: a client gives up on a silent daemon after
	// seconds, and a check is one Lstat.
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
		o.WatchInterval = 2 * time.Second
	}
	rec := self(o.Build)
	lock, holder, err := Wait(o.GlobalDir, rec, o.LockWait)
	if errors.Is(err, ErrHeld) {
		if holder.IsPreDaemon() {
			return fmt.Errorf("a loom from before the daemon is running (%s): quit it first", holder)
		}
		// A daemon, maybe still booting: two clients started one each.
		return fmt.Errorf("%w (%s)", ErrRunning, holder)
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	// Where to listen is settled before the boot, which sweeps sessions and
	// relaunches agents: a host with nowhere to put the socket fails here,
	// having touched nothing.
	socket, err := SocketPath(o.GlobalDir)
	if err != nil {
		return err
	}
	model, kept, err := o.NewModel()
	if err != nil {
		return fmt.Errorf("build the model: %w", err)
	}
	notices := model.Boot()
	loop := core.Start(model)
	srv := rpc.NewServer(loop)
	srv.SetTmux(o.Tmux)
	srv.Keep(kept...)
	for _, ev := range notices {
		if n, ok := ev.(core.Notice); ok {
			srv.Keep(n)
		}
	}
	loop.Begin()

	l := &listener{globalDir: o.GlobalDir, path: socket, srv: srv, lock: lock, rec: rec}
	if err := l.listen(); err != nil {
		srv.Close()
		loop.Stop()
		return err
	}
	// The record is how every client finds the daemon: one it can't write
	// leaves a daemon nobody can reach or stop, so it is no daemon at all.
	if err := l.record(); err != nil {
		l.close()
		srv.Close()
		loop.Stop()
		l.remove()
		return err
	}
	log.For("serve").Info("serve.listening", "socket", socket, "pid", rec.PID, "build", o.Build, "tmux", o.Tmux)
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
			// A model that failed but has not been published yet must not be
			// waited on or saved: its state is unknown (stopModel).
			if err := stopModel(loop, o.QuiesceTimeout); err != nil {
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

// stopModel readies the model to stop: it waits for in-flight lifecycle
// jobs (Loop.Quiesce), then saves every workspace the model serves. A
// failure is returned, not retried: the daemon is going, and the records it
// could not write keep their last saved state. A model that already failed
// re-raises its panic on the first call; that is returned too, with nothing
// saved, since its state is unknown.
func stopModel(loop *core.Loop, timeout time.Duration) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the model had failed; nothing saved: %v", r)
		}
	}()
	if !loop.Quiesce(timeout) {
		log.For("serve").Warn("serve.stopped_with_jobs_in_flight")
	}
	return loop.SaveForQuit()
}

// listener is the daemon's socket, the goroutine accepting on it, and the
// lock record that names it.
type listener struct {
	globalDir string
	srv       *rpc.Server
	lock      *Lock
	rec       Record

	mu   sync.Mutex
	path string
	ln   net.Listener
	// file is the socket file as listened on, to tell it from one put in
	// its place (os.SameFile); nil until it listens.
	file os.FileInfo
	// lost is set while the socket can't be listened on again: its failure
	// is logged once, not at every watch.
	lost bool
}

// record writes the lock record naming the socket now listened on.
func (l *listener) record() error {
	l.mu.Lock()
	l.rec.Socket = l.path
	rec := l.rec
	l.mu.Unlock()
	return l.lock.Write(rec)
}

// listen binds the socket, replacing a stale file a dead daemon left (the
// lock, which this process holds, proves no live one uses it), makes it
// private (net.Listen follows the umask), and accepts on it.
func (l *listener) listen() error {
	l.mu.Lock()
	path := l.path
	l.mu.Unlock()
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", path, err)
	}
	// Closing would unlink the path whatever file is there by then (one put
	// in its place, once the watch has listened again): remove checks the
	// inode first, so it alone deletes.
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("make %s private: %w", path, err)
	}
	l.mu.Lock()
	l.ln, l.file = ln, stat(path)
	l.mu.Unlock()
	go l.accept(ln)
	return nil
}

// accept serves every connection ln accepts, until ln closes. Any other
// error (too many open files, say) is waited out: returning would leave a
// daemon that listens but never accepts, which no watch would notice.
func (l *listener) accept(ln net.Listener) {
	backoff := 10 * time.Millisecond
	for {
		nc, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			log.For("serve").Warn("serve.accept_failed", "err", err)
			time.Sleep(backoff)
			backoff = min(2*backoff, time.Second)
			continue
		}
		backoff = 10 * time.Millisecond
		l.srv.Serve(nc)
	}
}

// watch listens again when the socket file is gone or replaced: a runtime
// dir removed at the last logout, or a tmp cleaner, would otherwise leave a
// daemon no client can reach, holding the lock that stops another starting.
//
// The socket goes wherever SocketPath finds a place now, which is another
// one while its dir is gone (a runtime dir removed at logout), and the
// record is rewritten to name it.
func (l *listener) watch() {
	l.mu.Lock()
	file, path, lost := l.file, l.path, l.lost
	l.mu.Unlock()
	if !lost && sameFile(file, path) {
		return
	}
	if !lost {
		log.For("serve").Warn("serve.socket_gone", "socket", path)
	}
	l.close()
	next, err := SocketPath(l.globalDir)
	if err == nil {
		l.mu.Lock()
		l.path = next
		l.mu.Unlock()
		err = l.listen()
	}
	if err == nil {
		err = l.record()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		if !l.lost {
			log.For("serve").Error("serve.relisten_failed", "err", err)
		}
		l.lost = true
		return
	}
	if l.lost || next != path {
		log.For("serve").Info("serve.listening_again", "socket", next)
	}
	l.lost = false
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
	file, path := l.file, l.path
	l.mu.Unlock()
	if sameFile(file, path) {
		_ = os.Remove(path)
	}
}

// stat is path's file info, nil when it can't be read.
func stat(path string) os.FileInfo {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	return info
}

// sameFile reports whether path is still the file listened on, not gone
// or another put in its place.
func sameFile(file os.FileInfo, path string) bool {
	now := stat(path)
	return file != nil && now != nil && os.SameFile(file, now)
}

// signalStop asks the process pid to stop gracefully: Serve's caller turns
// SIGTERM into a closed Stop. pid must be a `loom serve` (servesLoom). A
// test seam.
var signalStop = func(pid int) error {
	if !servesLoom(pid) {
		return fmt.Errorf("process %d is not a loom daemon", pid)
	}
	return terminate(pid)
}

// servesLoom reports whether process pid may be a loom daemon: one whose
// arguments include "serve". A record read at the wrong moment, or left by
// a daemon that died and whose pid was reused, must never get another
// process of the user's signalled. Where the process table can't be read
// (no /proc), it can't tell, and says yes.
func servesLoom(pid int) bool {
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist) || !procMounted()
	}
	return slices.Contains(strings.Split(string(cmdline), "\x00"), "serve")
}

// procMounted reports whether /proc lists processes here.
func procMounted() bool {
	_, err := os.Stat("/proc/self/cmdline")
	return err == nil
}

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
	if rec.IsPreDaemon() {
		return fmt.Errorf("the lock is held by a loom from before the daemon (%s): quit it first", rec)
	}
	if rec.PID <= 0 {
		return fmt.Errorf("the daemon's lock record names no process")
	}
	if host, _ := os.Hostname(); rec.Host != "" && rec.Host != host {
		return fmt.Errorf("the loom daemon runs on %s, not this host: stop it there", rec.Host)
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
