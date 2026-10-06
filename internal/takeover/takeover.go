// Package takeover keeps one loom TUI per global config dir, and lets a
// new one take over from the one already running.
//
// Two looms on the same workspaces overwrite each other's sessions:
// Storage rewrites a workspace's whole instance list from the writer's own
// memory, so each loom's saves drop the sessions only the other one knows,
// which then come back as orphans. Both would also attach a client to
// every agent session and fight over its window size. So the TUI holds an
// flock on <dir>/loom.lock for its lifetime (the OS releases it when the
// process exits, crash included) and serves takeover requests on
// <dir>/loom.sock. A newcomer that finds the lock held asks the holder to
// save and quit (Request), then waits for the lock (Wait) before it reads
// any state, so it loads what the holder saved last.
//
// No app, ui or session imports.
package takeover

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	lockFile = "loom.lock"
	sockFile = "loom.sock"
)

// ErrHeld is TryAcquire's (and Wait's) answer while another process holds
// the lock.
var ErrHeld = errors.New("another loom is running")

// Holder identifies a loom process: the one holding the lock (recorded in
// the lock file) or the one asking to take over.
type Holder struct {
	PID     int       `json:"pid"`
	TTY     string    `json:"tty,omitempty"`
	Started time.Time `json:"started"`
}

// Self describes this process.
func Self() Holder {
	return Holder{PID: os.Getpid(), TTY: ownTTY(), Started: time.Now()}
}

// ownTTY is the terminal on this process's stdin, "" when unknown (it is
// read from /proc, so Linux only).
func ownTTY() string {
	tty, err := os.Readlink("/proc/self/fd/0")
	if err != nil || !strings.HasPrefix(tty, "/dev/") {
		return ""
	}
	return tty
}

// String describes h for a message: "pid 3713275 on /dev/pts/2, since 06:23".
func (h Holder) String() string { return h.describe(time.Now()) }

func (h Holder) describe(now time.Time) string {
	s := fmt.Sprintf("pid %d", h.PID)
	if h.TTY != "" {
		s += " on " + h.TTY
	}
	if !h.Started.IsZero() {
		layout := "Jan 2 15:04"
		if y, m, d := h.Started.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
			layout = "15:04"
		}
		s += ", since " + h.Started.Format(layout)
	}
	return s
}

// Lock is a held lock. Production holds it until the process exits.
type Lock struct {
	dir string
	fl  *flock.Flock
	ln  net.Listener
}

// TryAcquire takes the lock in dir and records self in it. While another
// process holds it, it returns ErrHeld and that process's Holder (zero
// when its record can't be read).
func TryAcquire(dir string, self Holder) (*Lock, Holder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, Holder{}, err
	}
	path := filepath.Join(dir, lockFile)
	fl := flock.New(path)
	ok, err := fl.TryLock()
	if err != nil {
		_ = fl.Close()
		return nil, Holder{}, fmt.Errorf("lock %s: %w", path, err)
	}
	if !ok {
		_ = fl.Close()
		return nil, readHolder(path), ErrHeld
	}
	data, err := json.Marshal(self)
	if err == nil {
		err = os.WriteFile(path, data, 0o644)
	}
	if err != nil {
		_ = fl.Close()
		return nil, Holder{}, fmt.Errorf("record lock holder: %w", err)
	}
	return &Lock{dir: dir, fl: fl}, Holder{}, nil
}

func readHolder(path string) Holder {
	var h Holder
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &h)
	}
	return h
}

// Wait polls for the lock until it is free or timeout passes (ErrHeld).
func Wait(dir string, self Holder, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, _, err := TryAcquire(dir, self)
		if !errors.Is(err, ErrHeld) || !time.Now().Before(deadline) {
			return l, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Listen serves takeover requests: fn runs, on the listener's goroutine,
// with the requester each time one arrives. A socket file left by a
// holder that crashed is replaced; holding the lock proves nothing else
// is serving it.
func (l *Lock) Listen(fn func(by Holder)) error {
	path := filepath.Join(l.dir, sockFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	l.ln = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // closed
			}
			serve(conn, fn)
		}
	}()
	return nil
}

func serve(conn net.Conn, fn func(Holder)) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var by Holder
	if json.Unmarshal(line, &by) != nil {
		return
	}
	fn(by)
	_, _ = conn.Write([]byte("ok\n"))
}

// Request asks the loom holding the lock in dir to save and quit. It
// returns once the holder has the request, not once it has quit: Wait
// for the lock after it.
func Request(dir string, by Holder, timeout time.Duration) error {
	conn, err := net.DialTimeout("unix", filepath.Join(dir, sockFile), timeout)
	if err != nil {
		return fmt.Errorf("it isn't taking requests (an older loom, or a stuck one): %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	data, err := json.Marshal(by)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return err
	}
	ack, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("no answer: %w", err)
	}
	if strings.TrimSpace(ack) != "ok" {
		return fmt.Errorf("unexpected answer %q", ack)
	}
	return nil
}

// Close stops serving requests and releases the lock. The lock file
// stays: removing it while another process waits on it would let the two
// lock different files.
func (l *Lock) Close() error {
	if l.ln != nil {
		_ = l.ln.Close()
		_ = os.Remove(filepath.Join(l.dir, sockFile))
	}
	return l.fl.Close()
}
