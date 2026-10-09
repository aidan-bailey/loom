package daemon

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// LogPath is globalDir's daemon log, which a client's errors point at.
func LogPath(globalDir string) string {
	return filepath.Join(globalDir, "logs", log.ServeLogFileName)
}

// spawn starts `loom serve` (this executable) detached (detach), so it
// outlives the terminal and the client that started it,
// with no stdio and this process's environment, from which it resolves the
// same global dir. It returns a channel that receives the daemon's exit.
// A test seam: tests serve in process instead.
var spawn = func() (<-chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("start the loom daemon: %w", err)
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("start the loom daemon: %w", err)
	}
	defer null.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the loom daemon: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

// Connect dials globalDir's daemon, starting one when none runs, and
// returns the connection and the daemon's lock record. It reads the lock:
// a daemon holding it is dialed at the socket it recorded; a daemon still
// booting (it listens only once booted) is waited for; a loom TUI from
// before the daemon holding it is refused; and with no holder it starts
// one (spawn) and waits for it. It retries, backing off, until a daemon
// answers or timeout passes. The spawner does not take the lock: two
// clients starting a daemon each is harmless, since the second daemon
// finds the first's lock and exits, and both clients dial the first. A
// daemon that exits before it takes the lock (its nesting guard refused,
// say) fails Connect at once, quoting what it logged.
func Connect(globalDir string, timeout time.Duration) (net.Conn, Record, error) {
	logPath := LogPath(globalDir)
	logFrom := fileSize(logPath)
	deadline := time.Now().Add(timeout)
	var (
		spawned bool
		exited  <-chan error
		gone    bool  // the daemon this call started has exited
		exitErr error // and how
		last    error // why the last attempt did not connect
	)
	for wait := 10 * time.Millisecond; ; wait = min(2*wait, 250*time.Millisecond) {
		select {
		case exitErr = <-exited:
			gone, exited = true, nil
		default:
		}
		rec, held := ReadRecord(globalDir)
		switch {
		case held && rec.IsDaemon():
			nc, err := net.Dial("unix", rec.Socket)
			if err == nil {
				return nc, rec, nil
			}
			last = fmt.Errorf("the loom daemon (%s) does not answer on %s: %w", rec, rec.Socket, err)
		case held && rec.IsPreDaemon():
			return nil, rec, fmt.Errorf("a loom from before the daemon is running (%s): quit it first", rec)
		case held:
			last = fmt.Errorf("the loom daemon (%s) is still starting", rec)
		case gone:
			return nil, Record{}, fmt.Errorf("the loom daemon did not start: %s%s", exitText(exitErr), logTail(logPath, logFrom))
		case !spawned:
			ch, err := spawn()
			if err != nil {
				return nil, Record{}, err
			}
			spawned, exited = true, ch
			last = errors.New("the loom daemon is starting")
		}
		if !time.Now().Before(deadline) {
			return nil, Record{}, fmt.Errorf("%w; gave up after %s: see %s", last, timeout, logPath)
		}
		time.Sleep(wait)
	}
}

// exitText says how a daemon process ended (its Wait's error).
func exitText(err error) string {
	if err == nil {
		return "it exited"
	}
	return fmt.Sprintf("it exited (%v)", err)
}

// fileSize is path's size, 0 when it can't be read.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// logTailLines bounds the lines of serve.log a failed start quotes.
const logTailLines = 5

// logTail quotes the last lines written to the log at path since it was
// from bytes long (from the start when it is shorter now: rotated), as a
// suffix for an error; when there are none, it names the log instead.
func logTail(path string, from int64) string {
	f, err := os.Open(path)
	if err != nil {
		return "; see " + path
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() < from {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return "; see " + path
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "; see " + path
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, "  "+l)
		}
	}
	if len(lines) == 0 {
		return "; see " + path
	}
	if len(lines) > logTailLines {
		lines = lines[len(lines)-logTailLines:]
	}
	return fmt.Sprintf("; %s ends:\n%s", path, strings.Join(lines, "\n"))
}
