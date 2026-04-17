package log

import (
	"bytes"
	"encoding/json"
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
}
