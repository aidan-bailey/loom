// Package daemon runs `loom serve`, the process that owns a global config
// dir's sessions (daemon stage 3B), and finds it for its clients.
//
// One daemon serves one global dir. It holds an flock on <globalDir>/loom.lock
// for its whole life (the OS releases it when the process exits, crash
// included) and records in it who it is and where it listens (Record):
// clients read the socket's path from there rather than computing it, since
// their environment may differ from the daemon's. The lock lives in the
// global dir, not beside the socket, so two processes whose environments
// would put the socket in different places still exclude each other; and it
// is the path the TUI's own takeover lock used, so a loom from before the
// daemon and a daemon exclude each other too.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const lockFile = "loom.lock"

// ErrHeld is TryAcquire's answer while another process holds the lock.
var ErrHeld = errors.New("another loom process holds the lock")

// Record is what the lock file says about the process holding it. A daemon
// fills Build when it takes the lock and Socket once it listens; a loom TUI
// from before the daemon wrote only the first three, in the same JSON, so a
// record with neither is such a TUI (IsPreDaemon).
type Record struct {
	PID     int       `json:"pid"`
	TTY     string    `json:"tty,omitempty"`
	Started time.Time `json:"started"`
	// Socket is the unix socket the daemon listens on.
	Socket string `json:"socket,omitempty"`
	// Build names the daemon's binary (rpc.Build).
	Build string `json:"build,omitempty"`
	// Host is the machine the daemon runs on: a global dir shared over a
	// network filesystem must not have a pid signalled on the wrong host.
	Host string `json:"host,omitempty"`
	// Tmux is the tmux server the daemon's sessions run on (its socket's
	// path), which the next daemon keeps while it runs (TmuxServer).
	Tmux string `json:"tmux,omitempty"`
}

// IsDaemon reports whether r is the record of a daemon that listens. One
// still booting has written its build but no socket yet.
func (r Record) IsDaemon() bool { return r.Socket != "" }

// IsPreDaemon reports whether r is the record of a loom TUI from before the
// daemon: it names a process, but neither a socket nor a build. A record
// read mid-write names no process, and is neither this nor a daemon's.
func (r Record) IsPreDaemon() bool { return r.PID != 0 && r.Socket == "" && r.Build == "" }

// String describes r for a message: "pid 3713275 on /dev/pts/2, since 06:23".
func (r Record) String() string { return r.describe(time.Now()) }

func (r Record) describe(now time.Time) string {
	s := fmt.Sprintf("pid %d", r.PID)
	if r.TTY != "" {
		s += " on " + r.TTY
	}
	if !r.Started.IsZero() {
		layout := "Jan 2 15:04"
		if y, m, d := r.Started.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
			layout = "15:04"
		}
		s += ", since " + r.Started.Format(layout)
	}
	return s
}

// self is this process's record, with no socket yet. Its build is never
// empty, since a record with none is a pre-daemon TUI's (IsPreDaemon).
func self(build string) Record {
	if build == "" {
		build = "unknown"
	}
	host, _ := os.Hostname()
	return Record{PID: os.Getpid(), TTY: ownTTY(), Started: time.Now(), Build: build, Host: host}
}

// ownTTY is the terminal on this process's stdin, "" when unknown (it is
// read from /proc, so Linux only).
func ownTTY() string {
	tty, err := os.Readlink("/proc/self/fd/0")
	if err != nil || !strings.HasPrefix(tty, "/dev/") || tty == os.DevNull {
		return ""
	}
	return tty
}

// LockPath is the lock file of globalDir's daemon.
func LockPath(globalDir string) string { return filepath.Join(globalDir, lockFile) }

// Lock is a held lock.
type Lock struct {
	record string // where Write puts the record (recordPath)
	fl     *flock.Flock
}

// TryAcquire takes globalDir's lock and records rec in it. While another
// process holds it, it returns ErrHeld and that process's record (zero when
// it can't be read).
func TryAcquire(globalDir string, rec Record) (*Lock, Record, error) {
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return nil, Record{}, err
	}
	path := LockPath(globalDir)
	fl := flock.New(path)
	ok, err := fl.TryLock()
	if err != nil {
		_ = fl.Close()
		return nil, Record{}, fmt.Errorf("lock %s: %w", path, err)
	}
	if !ok {
		_ = fl.Close()
		holder, _ := readRecordOK(recordPath(globalDir))
		return nil, holder, ErrHeld
	}
	l := &Lock{record: recordPath(globalDir), fl: fl}
	if err := l.Write(rec); err != nil {
		_ = fl.Close()
		return nil, Record{}, err
	}
	return l, Record{}, nil
}

// acquire takes globalDir's lock for a daemon, waiting up to wait while it
// is held by no live loom process: a client's ReadRecord probing it, or a
// daemon that has taken it and not yet written its record. A live holder
// ends the wait at once (ErrHeld, with its record): a daemon started while
// another runs, or while another stops, must not take over once that one
// has gone, when nobody is waiting for it.
func acquire(globalDir string, rec Record, wait time.Duration) (*Lock, Record, error) {
	deadline := time.Now().Add(wait)
	for {
		l, holder, err := TryAcquire(globalDir, rec)
		if !errors.Is(err, ErrHeld) || liveHolder(holder) || !time.Now().Before(deadline) {
			return l, holder, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// liveHolder reports whether rec names a live holder of the lock: a loom
// TUI from before the daemon, or a `loom serve`.
func liveHolder(rec Record) bool {
	if rec.PID <= 0 || !alive(rec.PID) {
		return false
	}
	return rec.IsPreDaemon() || servesLoom(rec.PID)
}

// Write replaces the record the held lock carries.
func (l *Lock) Write(rec Record) error {
	data, err := json.Marshal(rec)
	if err == nil {
		err = os.WriteFile(l.record, data, 0o644)
	}
	if err != nil {
		return fmt.Errorf("record lock holder: %w", err)
	}
	return nil
}

// Close releases the lock. The file stays: a contender waiting on its
// inode and a holder of a recreated one would both "hold" it.
func (l *Lock) Close() error { return l.fl.Close() }

// ReadRecord reads globalDir's lock record, whoever holds it (zero when it
// can't be read). Held reports whether a process holds the lock now.
//
// A record is rewritten in place (a rename would give the lock file a new
// inode, which the lock is on), so a reader can meet it half-written: a held
// lock whose record won't parse is read again for a moment.
func ReadRecord(globalDir string) (rec Record, held bool) {
	path := recordPath(globalDir)
	fl := flock.New(LockPath(globalDir))
	ok, err := fl.TryLock()
	if err == nil && ok {
		_ = fl.Close()
		rec, _ = readRecordOK(path)
		return rec, false
	}
	_ = fl.Close()
	held = err == nil
	for range 20 {
		var parsed bool
		if rec, parsed = readRecordOK(path); parsed || !held {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return rec, held
}

// readRecordOK reads the record at path, and whether it parsed.
func readRecordOK(path string) (Record, bool) {
	var r Record
	data, err := os.ReadFile(path)
	if err != nil {
		return r, false
	}
	return r, json.Unmarshal(data, &r) == nil
}
