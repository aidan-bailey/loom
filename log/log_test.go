package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStructured_JSONFormat(t *testing.T) {
	t.Setenv(EnvLogFormat, "json")
	var buf bytes.Buffer
	logger := newStructured(&buf, false)
	logger.Info("hello", "key", "value")

	line := strings.TrimSpace(buf.String())
	assert.NotEmpty(t, line)

	var decoded map[string]any
	assert.NoError(t, json.Unmarshal([]byte(line), &decoded))
	assert.Equal(t, "hello", decoded["msg"])
	assert.Equal(t, "value", decoded["key"])
}

func TestNewStructured_TextFormat(t *testing.T) {
	t.Setenv(EnvLogFormat, "")
	var buf bytes.Buffer
	logger := newStructured(&buf, false)
	logger.Info("hello", "key", "value")

	out := buf.String()
	assert.Contains(t, out, `msg=hello`)
	assert.Contains(t, out, `key=value`)
	// Text handler prefixes with time=..., never starts with '{'.
	assert.False(t, strings.HasPrefix(strings.TrimSpace(out), "{"), "text handler should not emit JSON")
}

func TestNewStructured_DaemonTagsComponent(t *testing.T) {
	t.Setenv(EnvLogFormat, "json")
	var buf bytes.Buffer
	logger := newStructured(&buf, true)
	logger.Info("m")

	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &decoded))
	assert.Equal(t, "daemon", decoded["component"])
}

func TestKVHelpers_NoopBeforeInitialize(t *testing.T) {
	// Structured is nil before Initialize; helpers must not panic.
	Structured = nil
	assert.NotPanics(t, func() { InfoKV("m", "k", "v") })
	assert.NotPanics(t, func() { WarnKV("m", "k", "v") })
	assert.NotPanics(t, func() { ErrorKV("m", "k", "v") })
	assert.NotPanics(t, func() { DebugKV("m", "k", "v") })
	assert.NotPanics(t, func() { Debugf("m %d", 1) })
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		"  info ": slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"trace":   slog.LevelInfo, // unknown → default
	}
	for in, want := range cases {
		assert.Equalf(t, want, parseLevel(in), "parseLevel(%q)", in)
	}
}

func TestLevelVar_GatesDebug(t *testing.T) {
	t.Setenv(EnvLogFormat, "")
	origLevel := levelVar.Level()
	t.Cleanup(func() { levelVar.Set(origLevel) })

	var buf bytes.Buffer
	levelVar.Set(slog.LevelInfo)
	logger := newStructured(&buf, false)
	logger.Debug("should-not-appear")
	assert.Empty(t, strings.TrimSpace(buf.String()), "debug must be filtered at LevelInfo")

	buf.Reset()
	levelVar.Set(slog.LevelDebug)
	logger.Debug("should-appear", "k", "v")
	out := buf.String()
	assert.Contains(t, out, "should-appear")
	assert.Contains(t, out, "k=v")
}

func TestSetLevel_RuntimeToggle(t *testing.T) {
	origLevel := levelVar.Level()
	t.Cleanup(func() { levelVar.Set(origLevel) })

	SetLevel(slog.LevelError)
	assert.Equal(t, slog.LevelError, levelVar.Level())
	SetLevel(slog.LevelDebug)
	assert.Equal(t, slog.LevelDebug, levelVar.Level())
}

func TestFor_InheritsComponent(t *testing.T) {
	t.Setenv(EnvLogFormat, "json")
	origStructured := Structured
	origLevel := levelVar.Level()
	t.Cleanup(func() {
		Structured = origStructured
		levelVar.Set(origLevel)
	})

	var buf bytes.Buffer
	levelVar.Set(slog.LevelInfo)
	Structured = newStructured(&buf, true) // daemon=true → component=daemon baked in

	sub := For("tmux", "instance", "demo")
	sub.Info("hello")

	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &decoded))
	assert.Equal(t, "daemon", decoded["component"], "For must inherit component from base")
	assert.Equal(t, "tmux", decoded["subsystem"])
	assert.Equal(t, "demo", decoded["instance"])
}

func TestFor_SafeBeforeInitialize(t *testing.T) {
	origStructured := Structured
	Structured = nil
	t.Cleanup(func() { Structured = origStructured })

	logger := For("whatever", "k", "v")
	assert.NotNil(t, logger)
	assert.NotPanics(t, func() { logger.Info("no-op ok") })
}

func TestInitialize_PopulatesLogFilePath(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, Initialize(dir, false))
	t.Cleanup(Close)

	assert.Equal(t, filepath.Join(dir, logFileName), LogFilePath())
}

func TestInitialize_HonorsEnvLogLevel(t *testing.T) {
	t.Setenv(EnvLogLevel, "debug")
	origLevel := levelVar.Level()
	t.Cleanup(func() { levelVar.Set(origLevel) })

	require.NoError(t, Initialize(t.TempDir(), false))
	t.Cleanup(Close)

	assert.Equal(t, slog.LevelDebug, levelVar.Level())
}

// TestInitialize_ReturnsErrorWhenLogFileUnopenable guards the
// degraded-but-functional path: if the log file can't be opened
// (here: path is an existing directory, not a file), Initialize
// must return a non-nil error AND still wire up working loggers on
// stderr so the caller can run degraded. The prior implementation
// panicked, killing the app before any UI rendered.
func TestInitialize_ReturnsErrorWhenLogFileUnopenable(t *testing.T) {
	dir := t.TempDir()
	// Put a directory where the log file should be — OpenFile(O_WRONLY)
	// will fail with EISDIR, which is the failure mode we care about.
	blocker := filepath.Join(dir, logFileName)
	require.NoError(t, os.Mkdir(blocker, 0755))

	err := Initialize(dir, false)
	t.Cleanup(Close)

	require.Error(t, err, "Initialize must report the open failure instead of panicking")
	assert.Contains(t, err.Error(), logFileName, "error should name the unopenable path")

	// Degraded loggers must still be usable.
	assert.NotNil(t, InfoLog, "InfoLog must be wired to fallback sink")
	assert.NotNil(t, WarningLog)
	assert.NotNil(t, ErrorLog)
	assert.NotNil(t, Structured, "Structured logger must be wired to fallback sink")

	assert.NotPanics(t, func() { InfoLog.Printf("degraded info") })
	assert.NotPanics(t, func() { Structured.Info("degraded structured") })
}

// TestInitialize_DaemonFallbackUsesDiscard guards that the daemon
// child never tries to write to stderr on log-open failure — its
// stdio is nil'd by the parent (daemon.LaunchDaemon), so stderr
// would be a closed fd. io.Discard is the safe sink.
func TestInitialize_DaemonFallbackUsesDiscard(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, logFileName)
	require.NoError(t, os.Mkdir(blocker, 0755))

	err := Initialize(dir, true)
	t.Cleanup(Close)

	require.Error(t, err)
	// Writes must not panic even though the daemon parent may have
	// closed stdio.
	assert.NotPanics(t, func() { InfoLog.Printf("daemon degraded info") })
	assert.NotPanics(t, func() { Structured.Info("daemon degraded structured") })
}

// TestEvery_RateLimits exercises the ShouldLog rate limiter: the first
// call must return true, every follow-up call within the window must
// return false, and the next call after the window must return true
// again. Prior implementation used time.NewTimer + Reset which is
// racy per the Go docs on non-stopped timers.
func TestEvery_RateLimits(t *testing.T) {
	e := NewEvery(50 * time.Millisecond)

	assert.True(t, e.ShouldLog(), "first call must always emit")
	// Ten rapid calls inside the window — none should emit.
	for i := 0; i < 10; i++ {
		assert.False(t, e.ShouldLog(), "call %d inside window must be suppressed", i)
	}

	time.Sleep(60 * time.Millisecond)
	assert.True(t, e.ShouldLog(), "first call after window must emit again")
	assert.False(t, e.ShouldLog(), "subsequent call inside new window must be suppressed")
}

func TestDebugKV_EmitsWhenEnabled(t *testing.T) {
	t.Setenv(EnvLogFormat, "")
	origStructured := Structured
	origLevel := levelVar.Level()
	t.Cleanup(func() {
		Structured = origStructured
		levelVar.Set(origLevel)
	})

	var buf bytes.Buffer
	levelVar.Set(slog.LevelDebug)
	Structured = newStructured(&buf, false)

	DebugKV("marker", "k", 42)
	out := buf.String()
	assert.Contains(t, out, "marker")
	assert.Contains(t, out, "k=42")
}

// TestLevelGateSilencesLegacyLoggers guards the core contract of this
// change: LOOM_LOG_LEVEL=error must drop INFO and WARNING
// records coming through the legacy *log.Logger vars, while ERROR
// still writes. Regression catches a dropped level check in
// levelWriter.Write.
func TestLevelGateSilencesLegacyLoggers(t *testing.T) {
	dir := t.TempDir()
	origLevel := levelVar.Level()
	t.Cleanup(func() { levelVar.Set(origLevel) })

	Initialize(dir, false)
	t.Cleanup(Close)

	SetLevel(slog.LevelError)
	InfoLog.Print("info-line-should-be-dropped")
	WarningLog.Print("warn-line-should-be-dropped")
	ErrorLog.Print("error-line-should-appear")

	// Close so buffered writes hit disk before we read the file.
	Close()
	// Re-set globalRotator to nil manually since the subsequent
	// t.Cleanup(Close) expects it non-nil. Re-opening for read only
	// is fine because we never touch the rotator again in this test.
	globalRotator = nil

	contents, err := os.ReadFile(filepath.Join(dir, logFileName))
	require.NoError(t, err)
	text := string(contents)

	assert.NotContains(t, text, "info-line-should-be-dropped", "INFO records must be filtered at LevelError")
	assert.NotContains(t, text, "warn-line-should-be-dropped", "WARNING records must be filtered at LevelError")
	assert.Contains(t, text, "error-line-should-appear", "ERROR records must still write at LevelError")
}

// TestRotationHappensMidRun shrinks maxLogSize so a small number of
// writes crosses the threshold, then verifies the rename-in-place
// produced a .log.1 with the old bytes and a fresh .log with the
// subsequent writes. Without runtime rotation this test would find
// everything concatenated in a single .log.
func TestRotationHappensMidRun(t *testing.T) {
	dir := t.TempDir()
	origMax := maxLogSize
	origLevel := levelVar.Level()
	t.Cleanup(func() {
		maxLogSize = origMax
		levelVar.Set(origLevel)
	})

	// Threshold sized for exactly one rotation: ~70-byte prefixed
	// lines × 4 pre-rotation records ≈ 280 bytes > 200. Single
	// post-rotation record lands in the fresh file.
	maxLogSize = 200

	Initialize(dir, false)
	t.Cleanup(Close)

	SetLevel(slog.LevelInfo)
	// Use distinct markers per line so we can reason about where each
	// record landed without guessing prefix byte counts. With ~62-byte
	// prefixed lines and max=200: markers A, B, C fill .log (~186 B),
	// marker D overflows → rotation → D lands in fresh .log along with
	// POST. A, B, C survive in .log.1.
	InfoLog.Print("MARK-A")
	InfoLog.Print("MARK-B")
	InfoLog.Print("MARK-C")
	InfoLog.Print("MARK-D")
	InfoLog.Print("MARK-POST")

	// Flush + cleanly release handles so readers see the final bytes.
	Close()
	globalRotator = nil

	logPath := filepath.Join(dir, logFileName)
	backupPath := logPath + ".1"

	backup, err := os.ReadFile(backupPath)
	require.NoError(t, err, ".log.1 must exist after mid-run rotation")
	current, err := os.ReadFile(logPath)
	require.NoError(t, err, ".log must exist after mid-run rotation")

	// The pre-rotation writes (A, B, C) must all be in .log.1 and
	// absent from .log. The post-rotation write must be in .log.
	// Marker D straddles the boundary: deliberately unasserted so a
	// later prefix-width change (e.g. extended filename) doesn't
	// break the test for a benign reason.
	assert.Contains(t, string(backup), "MARK-A", "MARK-A must be in .log.1 (pre-rotation)")
	assert.Contains(t, string(backup), "MARK-B", "MARK-B must be in .log.1 (pre-rotation)")
	assert.Contains(t, string(backup), "MARK-C", "MARK-C must be in .log.1 (pre-rotation)")
	assert.NotContains(t, string(backup), "MARK-POST", ".log.1 must not contain post-rotation data")
	assert.Contains(t, string(current), "MARK-POST", "MARK-POST must be in .log (post-rotation)")
	assert.NotContains(t, string(current), "MARK-A", ".log must not contain MARK-A (written before rotation)")
}

// TestRotationConcurrentWrites hammers the rotator from many
// goroutines and verifies that (a) all writes complete without error
// and (b) no individual line is torn across a rotation boundary. We
// size max so exactly one rotation fires over the total volume,
// which keeps all records recoverable in .log + .log.1 (single-
// backup rotation overwrites .log.1 on each subsequent rotation, so
// multi-rotation scenarios would legitimately lose records).
func TestRotationConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	origMax := maxLogSize
	origLevel := levelVar.Level()
	t.Cleanup(func() {
		maxLogSize = origMax
		levelVar.Set(origLevel)
	})

	const goroutines = 10
	const perGoroutine = 50
	// ~70 bytes per line × 500 lines ≈ 35 KB total. max=20 KB → exactly
	// one rotation: the first ~20 KB land in .log.1, the remainder in
	// the fresh .log. Both files are inspected so no records are lost
	// to single-backup overwrite.
	maxLogSize = 20000

	Initialize(dir, false)
	t.Cleanup(Close)

	SetLevel(slog.LevelInfo)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				InfoLog.Printf("MARK g=%02d i=%03d END", g, i)
			}
		}(g)
	}
	wg.Wait()

	Close()
	globalRotator = nil

	current, err := os.ReadFile(filepath.Join(dir, logFileName))
	require.NoError(t, err)
	var combined strings.Builder
	combined.Write(current)
	if backup, rerr := os.ReadFile(filepath.Join(dir, logFileName+".1")); rerr == nil {
		combined.Write(backup)
	}
	text := combined.String()

	markCount := strings.Count(text, "MARK g=")
	endCount := strings.Count(text, " END")
	assert.Equal(t, goroutines*perGoroutine, markCount, "every record must appear exactly once across .log + .log.1")
	assert.Equal(t, markCount, endCount, "every record's MARK must be paired with END — a mismatch indicates a torn write across the rotation boundary")
}

// TestRotateIfNeeded_RotatesOversizedFileAtStartup simulates the case
// where a prior process crashed leaving an oversized log on disk. The
// startup hook must rename it to .log.1 before the fresh file is opened
// so the steady-state rotator starts from a clean baseline.
func TestRotateIfNeeded_RotatesOversizedFileAtStartup(t *testing.T) {
	dir := t.TempDir()
	origMax := maxLogSize
	t.Cleanup(func() { maxLogSize = origMax })

	maxLogSize = 100
	path := filepath.Join(dir, logFileName)

	// Pre-seed a log that exceeds the threshold — the kind of file a
	// crashed prior run would leave behind.
	stale := bytes.Repeat([]byte("A"), 250)
	require.NoError(t, os.WriteFile(path, stale, 0644))

	rotateIfNeeded(path)

	backup, err := os.ReadFile(path + ".1")
	require.NoError(t, err, "oversized log must be renamed to .log.1")
	assert.Equal(t, stale, backup, "backup must contain the pre-rotation bytes verbatim")

	if _, err := os.Stat(path); err == nil {
		t.Fatalf(".log must not exist post-rotation (gets created fresh by OpenFile later); stat returned nil err")
	}
}

// TestRotateIfNeeded_SkipsSmallFile guards against spurious rotation
// of a healthy log. A file under the threshold must be left alone.
func TestRotateIfNeeded_SkipsSmallFile(t *testing.T) {
	dir := t.TempDir()
	origMax := maxLogSize
	t.Cleanup(func() { maxLogSize = origMax })

	maxLogSize = 1000
	path := filepath.Join(dir, logFileName)
	content := []byte("small log")
	require.NoError(t, os.WriteFile(path, content, 0644))

	rotateIfNeeded(path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got, "small log must be left untouched")
	_, err = os.Stat(path + ".1")
	assert.True(t, os.IsNotExist(err), ".log.1 must not be created when below threshold")
}

// TestRotateIfNeeded_NoOpWhenMissing ensures the startup hook is a
// no-op when no log exists yet (first run). It must not create empty
// files or panic.
func TestRotateIfNeeded_NoOpWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, logFileName)

	assert.NotPanics(t, func() { rotateIfNeeded(path) })

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "startup rotate on a missing file must not create one")
}

// TestEvery_ShouldLog_RateLimits verifies Every lets exactly one call
// through within a timeout window. Subsequent calls must return false
// until the timer fires, then true once. Covers the rate-limiting path
// used by the `git.cmd.ok` debug stream.
func TestEvery_ShouldLog_RateLimits(t *testing.T) {
	e := NewEvery(50 * time.Millisecond)

	// First call initializes the timer and must pass.
	assert.True(t, e.ShouldLog(), "first call must log — primes the rate limiter")

	// A flurry of immediate follow-ups must all drop.
	for i := 0; i < 5; i++ {
		assert.False(t, e.ShouldLog(), "follow-up call %d within window must be rate-limited", i)
	}

	// Wait past the timeout and the next call must pass again.
	time.Sleep(75 * time.Millisecond)
	assert.True(t, e.ShouldLog(), "call after timeout must log")
	assert.False(t, e.ShouldLog(), "immediate follow-up after post-timeout log must drop")
}

// TestEvery_ShouldLog_ConcurrentSafe hammers ShouldLog from many
// goroutines. Under a small timeout exactly one goroutine per window
// should win the race; the total permitted count must be bounded and
// the mutex must prevent any data races (run with -race).
func TestEvery_ShouldLog_ConcurrentSafe(t *testing.T) {
	e := NewEvery(20 * time.Millisecond)

	var allowed int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	const goroutines = 50
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if e.ShouldLog() {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Under the tight 20ms window and the first-call-primes semantics,
	// at most a handful of goroutines will have won the drain — but at
	// least one must (the primer). Assert sanity bounds rather than an
	// exact count because scheduler jitter can let a second call
	// through if the goroutines straddle the first timer fire.
	assert.GreaterOrEqual(t, allowed, int64(1), "at least one concurrent call must win the rate limit")
	assert.Less(t, allowed, int64(goroutines), "rate limiter must drop most concurrent calls within the window")
}
