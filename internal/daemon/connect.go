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

// CrashLogPath is where globalDir's daemon writes a runtime crash (a fatal
// error, an unrecovered panic on a goroutine), which no log line records.
func CrashLogPath(globalDir string) string {
	return filepath.Join(globalDir, "logs", "serve-crash.log")
}

// spawn starts `loom serve` (this executable) for globalDir detached
// (detach), so it outlives the terminal and the client that started it,
// with no stdio and this process's environment, from which it resolves the
// same global dir. It starts in the global dir, never the client's working
// directory, which it would otherwise keep for its whole life (a worktree
// removed later, a disk unmounted). It returns a channel that receives the
// daemon's exit. A test seam: tests serve in process instead.
var spawn = func(globalDir string) (<-chan error, error) {
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
	cmd.Dir = globalDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the loom daemon: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

// bootTimeout bounds Connect's wait for a live daemon that is still
// booting, past its own timeout: a boot relaunches every agent whose session
// died (after a reboot, all of them) before the daemon listens.
const bootTimeout = 3 * time.Minute

// maxSpawns bounds how many daemons one Connect starts: another only after
// the last stood down (exited cleanly, as Serve does on ErrRunning) for a
// daemon that has gone since.
const maxSpawns = 3

// Connect dials globalDir's daemon, starting one when none runs, and
// returns the connection and the daemon's lock record. It reads the lock:
// a daemon holding it is dialed at the socket it recorded; a daemon still
// booting (it listens only once booted) is waited for, a live one up to
// bootTimeout; a live loom TUI from before the daemon holding it is
// refused; and with no holder it starts one (spawn) and waits for it. It
// retries, backing off, until a daemon answers or timeout passes. The
// spawner does not take the lock: two clients starting a daemon each is
// harmless, since the second daemon finds the first's lock and stands down,
// and both clients dial the first. A daemon that fails before it takes the
// lock (its nesting guard refused, say) fails Connect at once, quoting what
// it logged. It makes the global dir first, which the daemon starts in: a
// first launch with a new LOOM_GLOBAL_DIR, or LOOM_HOME and no ~/.loom yet,
// would otherwise fail to start any.
func Connect(globalDir string, timeout time.Duration) (net.Conn, Record, error) {
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return nil, Record{}, fmt.Errorf("make the loom config dir: %w", err)
	}
	logPath, crashPath := LogPath(globalDir), CrashLogPath(globalDir)
	logFrom, crashFrom := fileSize(logPath), fileSize(crashPath)
	start := time.Now()
	var (
		spawns  int
		exited  <-chan error // the daemon this call started last
		gone    bool         // has exited
		exitErr error        // and how
		last    error        // why the last attempt did not connect
	)
	for wait := 10 * time.Millisecond; ; wait = min(2*wait, 250*time.Millisecond) {
		select {
		case exitErr = <-exited:
			gone, exited = true, nil
		default:
		}
		booting := false
		rec, held := ReadRecord(globalDir)
		switch {
		case held && rec.IsDaemon():
			nc, err := net.Dial("unix", rec.Socket)
			if err == nil {
				return nc, rec, nil
			}
			last = fmt.Errorf("the loom daemon (%s) does not answer on %s: %w", rec, rec.Socket, err)
		case held && rec.IsPreDaemon() && alive(rec.PID):
			return nil, rec, fmt.Errorf("a loom from before the daemon is running (%s): quit it first", rec)
		case held:
			// A daemon booting, or one that took the lock and has not yet
			// written its record over the last holder's.
			last = fmt.Errorf("the loom daemon (%s) is still starting", rec)
			booting = liveHolder(rec)
		case exited != nil:
			last = errors.New("the loom daemon is starting")
		case gone && (exitErr != nil || spawns == maxSpawns):
			return nil, Record{}, fmt.Errorf("the loom daemon did not start: %s%s%s",
				exitText(exitErr), logTail(logPath, logFrom), crashTail(crashPath, crashFrom))
		default:
			ch, err := spawn(globalDir)
			if err != nil {
				return nil, Record{}, err
			}
			spawns, exited, gone = spawns+1, ch, false
			last = errors.New("the loom daemon is starting")
		}
		limit := start.Add(timeout)
		if booting {
			limit = start.Add(max(timeout, bootTimeout))
		}
		if !time.Now().Before(limit) {
			return nil, Record{}, fmt.Errorf("%w; gave up after %s: see %s%s",
				last, time.Since(start).Round(time.Second), logPath, crashTail(crashPath, crashFrom))
		}
		time.Sleep(wait)
	}
}

// crashTail quotes what the daemon's crash log gained since it was from
// bytes long, as a suffix for an error; "" when it gained nothing.
func crashTail(path string, from int64) string {
	if fileSize(path) <= from {
		return ""
	}
	return logTail(path, from)
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
