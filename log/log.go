package log

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EnvLogFormat selects the output format for the structured logger.
// "json" → JSON lines; anything else → plain text. Legacy *log.Logger
// vars (InfoLog/WarningLog/ErrorLog) always emit plain text for
// source-compatibility with the ~117 existing .Printf call sites.
const EnvLogFormat = "CLAUDE_SQUAD_LOG_FORMAT"

// EnvLogLevel gates the Structured logger's minimum level. Values:
// "debug", "info" (default), "warn", "error". Legacy *log.Logger vars
// are unaffected — they always write. `SetLevel` lets tests and
// future runtime toggles update the gate after Initialize.
const EnvLogLevel = "CLAUDE_SQUAD_LOG_LEVEL"

var (
	WarningLog *log.Logger
	InfoLog    *log.Logger
	ErrorLog   *log.Logger

	// Structured is the slog-based logger preferred for new call sites
	// (see InfoKV/WarnKV/ErrorKV). It shares the log file with the
	// legacy loggers. Nil until Initialize has been called.
	Structured *slog.Logger

	// levelVar is the mutable level gate shared by every slog handler
	// created via newStructured. Using a single LevelVar keeps the
	// handler zero-alloc when the level filters out a record.
	levelVar = new(slog.LevelVar)
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
//
// Returns a non-nil error when the log file could not be opened. Callers
// may still use the package-level loggers after an error — Initialize
// falls back to stderr (or io.Discard in the daemon child, whose parent
// has nil'd stdio), so the app stays functional in a degraded mode
// instead of crashing via panic like the previous implementation.
func Initialize(logDir string, daemon bool) error {
	if logDir == "" {
		logDir = os.TempDir()
	} else {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			logDir = os.TempDir()
		}
	}

	logFilePath = filepath.Join(logDir, logFileName)
	rotateIfNeeded(logFilePath)

	prefix := ""
	if daemon {
		prefix = "[DAEMON] "
	}
	levelVar.Set(parseLevel(os.Getenv(EnvLogLevel)))

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		// Degraded path: pick a sink the process can actually write to.
		// Daemon's stdio is nil'd by the parent (see daemon.LaunchDaemon),
		// so stderr would write to a closed fd — discard instead.
		var sink io.Writer = os.Stderr
		if daemon {
			sink = io.Discard
		}
		InfoLog = log.New(sink, prefix+"INFO:", log.Ldate|log.Ltime|log.Lshortfile)
		WarningLog = log.New(sink, prefix+"WARNING:", log.Ldate|log.Ltime|log.Lshortfile)
		ErrorLog = log.New(sink, prefix+"ERROR:", log.Ldate|log.Ltime|log.Lshortfile)
		Structured = newStructured(sink, daemon)
		globalLogFile = nil
		return fmt.Errorf("could not open log file %q: %w", logFilePath, err)
	}

	InfoLog = log.New(f, prefix+"INFO:", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLog = log.New(f, prefix+"WARNING:", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLog = log.New(f, prefix+"ERROR:", log.Ldate|log.Ltime|log.Lshortfile)
	Structured = newStructured(f, daemon)
	globalLogFile = f
	return nil
}

// parseLevel maps a case-insensitive env value to slog.Level. An
// unknown or empty value yields LevelInfo — the pre-debug-tier
// default, so unset-env runs behave exactly like before.
func parseLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetLevel updates the Structured logger's minimum level at runtime.
// Safe to call before Initialize; the value is applied whenever the
// handler is first created.
func SetLevel(l slog.Level) { levelVar.Set(l) }

// newStructured returns a slog.Logger writing to w. Output format is
// JSON when CLAUDE_SQUAD_LOG_FORMAT=json is set (machine-readable for
// log-shipping pipelines), and human-readable text otherwise so that
// ad-hoc `grep` workflows keep working.
func newStructured(w io.Writer, daemon bool) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: levelVar}
	if os.Getenv(EnvLogFormat) == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	logger := slog.New(handler)
	if daemon {
		logger = logger.With("component", "daemon")
	}
	return logger
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

// InfoKV emits a structured INFO record. Prefer this over Infof for
// new call sites — kv pairs survive log-shipping.
func InfoKV(msg string, kv ...any) {
	if Structured != nil {
		Structured.Info(msg, kv...)
	}
}

// WarnKV emits a structured WARNING record.
func WarnKV(msg string, kv ...any) {
	if Structured != nil {
		Structured.Warn(msg, kv...)
	}
}

// ErrorKV emits a structured ERROR record.
func ErrorKV(msg string, kv ...any) {
	if Structured != nil {
		Structured.Error(msg, kv...)
	}
}

// Debugf emits a printf-style DEBUG record via the Structured
// logger. Gated by CLAUDE_SQUAD_LOG_LEVEL=debug (slog short-circuits
// before formatting when the level is below the handler's gate, so
// the `fmt.Sprintf` is paid only when debug is enabled).
func Debugf(format string, v ...any) {
	if Structured != nil && Structured.Enabled(context.Background(), slog.LevelDebug) {
		Structured.Debug(fmt.Sprintf(format, v...))
	}
}

// DebugKV emits a structured DEBUG record. Preferred for new call
// sites — the KV pairs survive log-shipping and stay grep-friendly.
func DebugKV(msg string, kv ...any) {
	if Structured != nil {
		Structured.Debug(msg, kv...)
	}
}

// For returns a Structured child logger tagged with subsystem=... plus
// any additional KV pairs. Designed to be cached on a long-lived owner
// (e.g. *session.Instance) so every log from that owner carries the
// same identifying attributes without each call site repeating them.
// Returns a no-op logger if Initialize has not run yet, so callers at
// package init time are safe.
func For(subsystem string, kv ...any) *slog.Logger {
	base := Structured
	if base == nil {
		base = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: levelVar}))
	}
	attrs := append([]any{"subsystem", subsystem}, kv...)
	return base.With(attrs...)
}

// LogFilePath returns the absolute path of the log file opened by
// Initialize. Empty string before Initialize runs. Exposed so the
// `debug` subcommand can tell users where to `tail` their logs.
func LogFilePath() string { return logFilePath }

// rotateIfNeeded renames the log file to .log.1 if it exceeds maxLogSize.
// Rotation happens before the structured logger is wired up, so failures
// go to stderr — the alternative (silent swallow) let the log grow past
// its cap without any operator signal.
func rotateIfNeeded(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	backup := path + ".1"
	// Remove is best-effort: "not exist" on first rotation is expected.
	// Only a non-ENOENT failure is worth surfacing, since Rename will
	// then fail too and we want the operator to see the root cause.
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "claude-squad: log rotation could not remove %s: %v\n", backup, err)
	}
	if err := os.Rename(path, backup); err != nil {
		fmt.Fprintf(os.Stderr, "claude-squad: log rotation failed (%s → %s): %v\n", path, backup, err)
	}
}

// Every is used to log at most once every timeout duration. Safe for concurrent
// use; ShouldLog takes a mutex before touching the internal timestamp.
type Every struct {
	timeout time.Duration
	mu      sync.Mutex
	lastAt  time.Time
}

func NewEvery(timeout time.Duration) *Every {
	return &Every{timeout: timeout}
}

// ShouldLog returns true if the timeout has passed since the last log. The
// first call always returns true (zero lastAt predates any real time, so
// Sub >= timeout holds trivially).
func (e *Every) ShouldLog() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	if now.Sub(e.lastAt) < e.timeout {
		return false
	}
	e.lastAt = now
	return true
}
