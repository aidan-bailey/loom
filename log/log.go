package log

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	WarningLog *log.Logger
	InfoLog    *log.Logger
	ErrorLog   *log.Logger
)

const (
	logFileName = "claudesquad.log"
	maxLogSize  = 5 * 1024 * 1024 // 5 MB
)

var (
	logFilePath   string
	globalLogFile *os.File
)

// Initialize should be called once at the beginning of the program to set up logging.
// defer Close() after calling this function.
//
// logDir specifies the directory for the log file. If empty, os.TempDir() is used.
// When non-empty, the directory is created if it does not exist.
func Initialize(logDir string, daemon bool) {
	if logDir == "" {
		logDir = os.TempDir()
	} else {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			// Fall back to temp dir if we can't create the log directory.
			logDir = os.TempDir()
		}
	}

	logFilePath = filepath.Join(logDir, logFileName)
	rotateIfNeeded(logFilePath)

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("could not open log file: %s", err))
	}

	fmtS := "%s"
	if daemon {
		fmtS = "[DAEMON] %s"
	}
	InfoLog = log.New(f, fmt.Sprintf(fmtS, "INFO:"), log.Ldate|log.Ltime|log.Lshortfile)
	WarningLog = log.New(f, fmt.Sprintf(fmtS, "WARNING:"), log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLog = log.New(f, fmt.Sprintf(fmtS, "ERROR:"), log.Ldate|log.Ltime|log.Lshortfile)

	globalLogFile = f
}

// Close closes the log file.
func Close() {
	if globalLogFile != nil {
		_ = globalLogFile.Close()
	}
}

// Infof logs at INFO level. No-op if Initialize has not been called.
func Infof(format string, v ...any) {
	if InfoLog != nil {
		InfoLog.Printf(format, v...)
	}
}

// Warnf logs at WARNING level. No-op if Initialize has not been called.
func Warnf(format string, v ...any) {
	if WarningLog != nil {
		WarningLog.Printf(format, v...)
	}
}

// Errorf logs at ERROR level. No-op if Initialize has not been called.
func Errorf(format string, v ...any) {
	if ErrorLog != nil {
		ErrorLog.Printf(format, v...)
	}
}

// rotateIfNeeded renames the log file to .log.1 if it exceeds maxLogSize.
func rotateIfNeeded(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	backup := path + ".1"
	_ = os.Remove(backup)
	_ = os.Rename(path, backup)
}

// Every is used to log at most once every timeout duration. Safe for concurrent
// use; ShouldLog takes a mutex before touching the internal timer.
type Every struct {
	timeout time.Duration
	mu      sync.Mutex
	timer   *time.Timer
}

func NewEvery(timeout time.Duration) *Every {
	return &Every{timeout: timeout}
}

// ShouldLog returns true if the timeout has passed since the last log.
func (e *Every) ShouldLog() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.timer == nil {
		e.timer = time.NewTimer(e.timeout)
		e.timer.Reset(e.timeout)
		return true
	}

	select {
	case <-e.timer.C:
		e.timer.Reset(e.timeout)
		return true
	default:
		return false
	}
}
