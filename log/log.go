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
const EnvLogFormat = "LOOM_LOG_FORMAT"

// EnvLogLevel gates the minimum log level for both the Structured
// logger and the legacy *log.Logger vars (InfoLog / WarningLog /
// ErrorLog). Values: "debug", "info" (default), "warn", "error".
// Legacy records below the gate are silently dropped at the writer
// layer so no change to the ~90 *.Printf call sites is required.
// `SetLevel` lets tests and future runtime toggles update the gate
// after Initialize.
const EnvLogLevel = "LOOM_LOG_LEVEL"

// Legacy env var names from the claude-squad era. Still honored as
// fallbacks with a one-time deprecation warning so in-flight shell
// configs keep working across the rename.
const (
	legacyEnvLogFormat = "CLAUDE_SQUAD_LOG_FORMAT"
	legacyEnvLogLevel  = "CLAUDE_SQUAD_LOG_LEVEL"
)

// legacyWarnOnce tracks whether we've already emitted the deprecation
// notice for a given legacy env name, keyed by new-name to avoid
// spamming stderr when the same value is read from multiple places.
var (
	legacyWarnOnceMu sync.Mutex
	legacyWarnedFor  = map[string]bool{}
)

// GetEnvWithLegacy returns os.Getenv(current); if empty it falls back
// to os.Getenv(legacy) and prints a one-time deprecation notice to
// stderr naming the new var to set.
func GetEnvWithLegacy(current, legacy string) string {
	if v := os.Getenv(current); v != "" {
		return v
	}
	v := os.Getenv(legacy)
	if v == "" {
		return ""
	}
	legacyWarnOnceMu.Lock()
	warned := legacyWarnedFor[current]
	if !warned {
		legacyWarnedFor[current] = true
	}
	legacyWarnOnceMu.Unlock()
	if !warned {
		fmt.Fprintf(os.Stderr, "loom: %s is deprecated; please use %s instead.\n", legacy, current)
	}
	return v
}

// WarningLog, InfoLog, and ErrorLog are the legacy *log.Logger
// package-level vars used by pre-Structured call sites (about 90
// Printf invocations). They share the log file with Structured and
// are gated by LOOM_LOG_LEVEL via the levelWriter shim so a legacy
// Printf at INFO tier is silently dropped under LOOM_LOG_LEVEL=warn.
// Nil until Initialize has been called.
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

const logFileName = "loom.log"

// maxLogSize is the rotation threshold in bytes. Declared as var (not
// const) so tests can shrink it to trigger rotation without writing
// megabytes of fixture data.
var maxLogSize int64 = 5 * 1024 * 1024 // 5 MB

var (
	logFilePath   string
	globalRotator *rotatingWriter
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
	levelVar.Set(parseLevel(GetEnvWithLegacy(EnvLogLevel, legacyEnvLogLevel)))

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
		globalRotator = nil
		return fmt.Errorf("could not open log file %q: %w", logFilePath, err)
	}

	// Track bytes already in the file so mid-run rotation fires at the
	// right threshold even when we appended to a pre-existing log.
	var initialSize int64
	if info, statErr := f.Stat(); statErr == nil {
		initialSize = info.Size()
	}
	rw := newRotatingWriter(logFilePath, f, initialSize, maxLogSize)

	// Wrap the rotator in a per-level filter so legacy *log.Logger
	// writes drop below the level gate, matching slog's behaviour.
	InfoLog = log.New(&levelWriter{w: rw, level: slog.LevelInfo}, prefix+"INFO:", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLog = log.New(&levelWriter{w: rw, level: slog.LevelWarn}, prefix+"WARNING:", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLog = log.New(&levelWriter{w: rw, level: slog.LevelError}, prefix+"ERROR:", log.Ldate|log.Ltime|log.Lshortfile)

	// Structured logs flow through the rotator too — slog's handler
	// already respects levelVar, so no extra level wrapper is needed.
	Structured = newStructured(rw, daemon)
	globalRotator = rw
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
// JSON when LOOM_LOG_FORMAT=json is set (machine-readable for
// log-shipping pipelines), and human-readable text otherwise so that
// ad-hoc `grep` workflows keep working.
func newStructured(w io.Writer, daemon bool) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: levelVar}
	if GetEnvWithLegacy(EnvLogFormat, legacyEnvLogFormat) == "json" {
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
	if globalRotator != nil {
		_ = globalRotator.Close()
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
// logger. Gated by LOOM_LOG_LEVEL=debug (slog short-circuits
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
// Called once at startup to catch log files left oversized by a prior
// crash; runtime rotation (rotatingWriter) handles the steady-state case.
// Runs before the structured logger is wired up, so failures go to
// stderr — the alternative (silent swallow) let the log grow past its
// cap without any operator signal.
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
		fmt.Fprintf(os.Stderr, "loom: log rotation could not remove %s: %v\n", backup, err)
	}
	if err := os.Rename(path, backup); err != nil {
		fmt.Fprintf(os.Stderr, "loom: log rotation failed (%s → %s): %v\n", path, backup, err)
	}
}

// rotatingWriter is an io.Writer that keeps a bounded single-file log
// with one `.1` backup. When a write would push the file past `max`,
// the current file is closed, renamed to path+".1", and a fresh file
// is opened for subsequent writes. All operations are serialized by
// `mu`, so concurrent legacy-logger and slog writes observe a
// consistent file-size counter and never race on the rename.
type rotatingWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
	max  int64
}

func newRotatingWriter(path string, file *os.File, initial int64, max int64) *rotatingWriter {
	return &rotatingWriter{path: path, file: file, size: initial, max: max}
}

func (rw *rotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file != nil && rw.size+int64(len(p)) > rw.max {
		rw.rotateLocked()
	}
	if rw.file == nil {
		// Rotation reopen failed; discard silently to keep long-lived
		// daemons running. Disk-full errors surface elsewhere.
		return len(p), nil
	}
	n, err := rw.file.Write(p)
	rw.size += int64(n)
	return n, err
}

func (rw *rotatingWriter) rotateLocked() {
	_ = rw.file.Close()
	backup := rw.path + ".1"
	_ = os.Remove(backup)
	_ = os.Rename(rw.path, backup)
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		rw.file = nil
		return
	}
	rw.file = f
	rw.size = 0
}

// Close closes the underlying log file, if any. Safe to call multiple
// times; subsequent calls are no-ops.
func (rw *rotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file == nil {
		return nil
	}
	err := rw.file.Close()
	rw.file = nil
	return err
}

// levelWriter drops writes whose fixed tier sits below the package
// `levelVar` gate. Wrapping the legacy *log.Logger writers in this
// shim makes LOOM_LOG_LEVEL apply uniformly without touching
// the ~90 .Printf call sites. Returning len(p) on a dropped write
// keeps *log.Logger from treating the drop as a short-write error.
type levelWriter struct {
	w     io.Writer
	level slog.Level
}

func (lw *levelWriter) Write(p []byte) (int, error) {
	if lw.level < levelVar.Level() {
		return len(p), nil
	}
	return lw.w.Write(p)
}

// Every is used to log at most once every timeout duration. Safe for concurrent
// use; ShouldLog takes a mutex before touching the internal timestamp.
type Every struct {
	timeout time.Duration
	mu      sync.Mutex
	lastAt  time.Time
}

// NewEvery constructs an Every rate-limiter with the given minimum
// spacing between ShouldLog() approvals. Use at the call site that
// would otherwise flood the log — e.g. a tick-based status sampler.
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
