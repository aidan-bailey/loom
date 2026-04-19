package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

	Initialize(dir, false)
	t.Cleanup(Close)

	assert.Equal(t, filepath.Join(dir, logFileName), LogFilePath())
}

func TestInitialize_HonorsEnvLogLevel(t *testing.T) {
	t.Setenv(EnvLogLevel, "debug")
	origLevel := levelVar.Level()
	t.Cleanup(func() { levelVar.Set(origLevel) })

	Initialize(t.TempDir(), false)
	t.Cleanup(Close)

	assert.Equal(t, slog.LevelDebug, levelVar.Level())
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
