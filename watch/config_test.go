package watch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/config"
)

// baseConfig is the smallest valid loom-watch.json: only the required keys.
func baseConfig() map[string]any {
	m := map[string]any{}
	m["version"] = 1
	m["label"] = "schema-drift"
	m["escalate_label"] = "needs-human"
	m["title"] = "heal {spec} {issue} {row}"
	m["session"] = map[string]any{"prompt": ".loom-watch/start.md"}
	return m
}

func setSession(m map[string]any, key string, v any) {
	m["session"].(map[string]any)[key] = v
}

func parseMap(t *testing.T, m map[string]any) (Config, error) {
	t.Helper()
	data, err := json.Marshal(m)
	require.NoError(t, err)
	return ParseConfig(data)
}

func words(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "x"
	}
	return out
}

func TestParseConfig_Full(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
  "version": 1,
  "label": "schema-drift",
  "escalate_label": "needs-human",
  "title": "heal {spec} {issue} {row}",
  "cap": 5,
  "stuck": "3h",
  "nudge_limit": 4,
  "nudge_gap": "30m",
  "issue_changed": false,
  "log_tail": 50,
  "session": {
    "model": "opus",
    "effort": "high",
    "permission_mode": "auto",
    "account": "work",
    "prompt": ".loom-watch/start.md",
    "resume_prompt": ".loom-watch/resume.md"
  },
  "hook": { "command": ["python3", "scripts/heal_drift.py", "hook"], "timeout": "90s" }
}`))
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Version)
	assert.Equal(t, "schema-drift", cfg.Label)
	assert.Equal(t, "needs-human", cfg.EscalateLabel)
	assert.Equal(t, "heal {spec} {issue} {row}", cfg.Title)
	assert.Equal(t, 5, cfg.Cap)
	assert.Equal(t, 3*time.Hour, cfg.Stuck)
	assert.Equal(t, 4, cfg.NudgeLimit)
	assert.Equal(t, 30*time.Minute, cfg.NudgeGap)
	assert.False(t, cfg.IssueChanged)
	assert.Equal(t, 50, cfg.LogTail)
	assert.Equal(t, SessionConfig{Model: "opus", Effort: "high", PermissionMode: "auto", Account: "work", Prompt: ".loom-watch/start.md", ResumePrompt: ".loom-watch/resume.md"}, cfg.Session)
	require.NotNil(t, cfg.Hook)
	assert.Equal(t, HookConfig{Command: []string{"python3", "scripts/heal_drift.py", "hook"}, Timeout: 90 * time.Second}, *cfg.Hook)
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := parseMap(t, baseConfig())
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.Cap)
	assert.Equal(t, 2*time.Hour, cfg.Stuck)
	assert.Equal(t, 3, cfg.NudgeLimit)
	assert.Equal(t, time.Hour, cfg.NudgeGap)
	assert.True(t, cfg.IssueChanged)
	assert.Equal(t, 200, cfg.LogTail)
	assert.Equal(t, ".loom-watch/start.md", cfg.Session.Prompt)
	assert.Equal(t, ".loom-watch/start.md", cfg.Session.ResumePrompt, "resume_prompt defaults to prompt")
	assert.Empty(t, cfg.Session.Account)
	assert.Nil(t, cfg.Hook)
}

func TestParseConfig_HookTimeoutDefaultsToAMinute(t *testing.T) {
	m := baseConfig()
	m["hook"] = map[string]any{"command": []string{"hook.sh"}}
	cfg, err := parseMap(t, m)
	require.NoError(t, err)
	require.NotNil(t, cfg.Hook)
	assert.Equal(t, time.Minute, cfg.Hook.Timeout)
	assert.Equal(t, []string{"hook.sh"}, cfg.Hook.Command)
}

func TestParseConfig_Required(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		mutate func(m map[string]any)
	}{
		{"no version", "version", func(m map[string]any) { delete(m, "version") }},
		{"no label", "label", func(m map[string]any) { delete(m, "label") }},
		{"empty label", "label", func(m map[string]any) { m["label"] = "" }},
		{"no escalate_label", "escalate_label", func(m map[string]any) { delete(m, "escalate_label") }},
		{"empty escalate_label", "escalate_label", func(m map[string]any) { m["escalate_label"] = "" }},
		{"no title", "title", func(m map[string]any) { delete(m, "title") }},
		{"empty title", "title", func(m map[string]any) { m["title"] = "" }},
		{"no session", "session.prompt", func(m map[string]any) { delete(m, "session") }},
		{"no prompt", "session.prompt", func(m map[string]any) { m["session"] = map[string]any{"model": "opus"} }},
		{"empty prompt", "session.prompt", func(m map[string]any) { setSession(m, "prompt", "") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := baseConfig()
			tc.mutate(m)
			_, err := parseMap(t, m)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.key)
		})
	}
}

func TestParseConfig_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"unknown top-level key", func(m map[string]any) { m["extra"] = 1 }, "extra"},
		{"unknown session key", func(m map[string]any) { setSession(m, "colour", "red") }, "colour"},
		{"unknown hook key", func(m map[string]any) {
			m["hook"] = map[string]any{"command": []string{"x"}, "shell": true}
		}, "shell"},
		{"version 2", func(m map[string]any) { m["version"] = 2 }, "version"},
		{"version 0", func(m map[string]any) { m["version"] = 0 }, "version"},
		{"cap as a string", func(m map[string]any) { m["cap"] = "2" }, "cap"},
		{"stuck not a duration", func(m map[string]any) { m["stuck"] = "soon" }, "stuck"},
		{"stuck as a number", func(m map[string]any) { m["stuck"] = 7200 }, "stuck"},
		{"nudge_gap not a duration", func(m map[string]any) { m["nudge_gap"] = "1 hour" }, "nudge_gap"},
		{"hook timeout not a duration", func(m map[string]any) {
			m["hook"] = map[string]any{"command": []string{"x"}, "timeout": "slow"}
		}, "hook.timeout"},
		{"escalate_label equals label", func(m map[string]any) { m["escalate_label"] = "schema-drift" }, "escalate_label"},
		{"label with a leading space", func(m map[string]any) { m["label"] = " schema-drift" }, "label"},
		{"escalate_label with a newline", func(m map[string]any) { m["escalate_label"] = "needs\nhuman" }, "escalate_label"},
		{"title without {issue}", func(m map[string]any) { m["title"] = "heal {spec}" }, "title"},
		{"title with a bad variable", func(m map[string]any) { m["title"] = "heal {Spec} {issue}" }, "title"},
		{"title that can never fit", func(m map[string]any) {
			m["title"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa {issue}"
		}, "title"},
		{"unknown model", func(m map[string]any) { setSession(m, "model", "gpt-9") }, "session.model"},
		{"unknown effort", func(m map[string]any) { setSession(m, "effort", "huge") }, "session.effort"},
		{"unknown permission mode", func(m map[string]any) { setSession(m, "permission_mode", "yolo") }, "session.permission_mode"},
		{"hook without a command", func(m map[string]any) { m["hook"] = map[string]any{} }, "hook.command"},
		{"hook command with a NUL", func(m map[string]any) {
			m["hook"] = map[string]any{"command": []string{"a\x00b"}}
		}, "hook.command"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := baseConfig()
			tc.mutate(m)
			cfg, err := parseMap(t, m)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.Equal(t, Config{}, cfg, "a fault returns no partly defaulted config")
		})
	}
}

func TestParseConfig_AcceptsEveryValueTheSessionCommandOffers(t *testing.T) {
	for _, model := range config.ClaudeModelAliases() {
		m := baseConfig()
		setSession(m, "model", model)
		_, err := parseMap(t, m)
		assert.NoError(t, err, "model %q", model)
	}
	for _, effort := range config.ClaudeEfforts {
		m := baseConfig()
		setSession(m, "effort", effort)
		_, err := parseMap(t, m)
		assert.NoError(t, err, "effort %q", effort)
	}
	for _, mode := range config.ClaudePermissionModes {
		m := baseConfig()
		setSession(m, "permission_mode", mode)
		_, err := parseMap(t, m)
		assert.NoError(t, err, "permission_mode %q", mode)
	}
}

func TestParseConfig_Bounds(t *testing.T) {
	setHookTimeout := func(m map[string]any, v any) {
		m["hook"] = map[string]any{"command": []string{"x"}, "timeout": v}
	}
	tests := []struct {
		key string
		set func(m map[string]any, v any)
		ok  []any
		bad []any
	}{
		{"cap", func(m map[string]any, v any) { m["cap"] = v }, []any{1, 10}, []any{0, 11, -1}},
		{"stuck", func(m map[string]any, v any) { m["stuck"] = v }, []any{"10m", "48h"}, []any{"9m59s", "48h1s", "0s", "-1h"}},
		{"nudge_limit", func(m map[string]any, v any) { m["nudge_limit"] = v }, []any{1, 10}, []any{0, 11}},
		{"nudge_gap", func(m map[string]any, v any) { m["nudge_gap"] = v }, []any{"1m", "24h"}, []any{"59s", "24h1s"}},
		{"log_tail", func(m map[string]any, v any) { m["log_tail"] = v }, []any{1, 1000}, []any{0, 1001}},
		{"hook.timeout", setHookTimeout, []any{"1s", "300s"}, []any{"999ms", "301s", "0s"}},
	}
	for _, tc := range tests {
		for _, v := range tc.ok {
			t.Run(fmt.Sprintf("%s=%v accepted", tc.key, v), func(t *testing.T) {
				m := baseConfig()
				tc.set(m, v)
				_, err := parseMap(t, m)
				assert.NoError(t, err)
			})
		}
		for _, v := range tc.bad {
			t.Run(fmt.Sprintf("%s=%v refused", tc.key, v), func(t *testing.T) {
				m := baseConfig()
				tc.set(m, v)
				_, err := parseMap(t, m)
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.key)
			})
		}
	}
}

func TestParseConfig_HookCommandBounds(t *testing.T) {
	for name, command := range map[string][]string{"one string": words(1), "32 strings": words(32), "empty arguments after the program": {"x", "", ""}} {
		t.Run(name+" accepted", func(t *testing.T) {
			m := baseConfig()
			m["hook"] = map[string]any{"command": command}
			_, err := parseMap(t, m)
			assert.NoError(t, err)
		})
	}
	for name, command := range map[string][]string{"no strings": {}, "33 strings": words(33), "empty program": {""}, "empty program with arguments": {"", "x"}} {
		t.Run(name+" refused", func(t *testing.T) {
			m := baseConfig()
			m["hook"] = map[string]any{"command": command}
			_, err := parseMap(t, m)
			require.Error(t, err)
			assert.ErrorContains(t, err, "hook.command")
		})
	}
}

func TestParseConfig_PromptPaths(t *testing.T) {
	for _, p := range []string{".loom-watch/start.md", "start.md", "a/b/c.md"} {
		t.Run(p+" accepted", func(t *testing.T) {
			m := baseConfig()
			setSession(m, "prompt", p)
			setSession(m, "resume_prompt", p)
			_, err := parseMap(t, m)
			assert.NoError(t, err)
		})
	}
	for _, p := range []string{"/etc/passwd", "../start.md", "a/../b.md", "..", ".", "a/", "./start.md", "a\nb.md"} {
		t.Run(fmt.Sprintf("%q refused as prompt", p), func(t *testing.T) {
			m := baseConfig()
			setSession(m, "prompt", p)
			_, err := parseMap(t, m)
			require.Error(t, err)
			assert.ErrorContains(t, err, "session.prompt")
		})
		t.Run(fmt.Sprintf("%q refused as resume_prompt", p), func(t *testing.T) {
			m := baseConfig()
			setSession(m, "resume_prompt", p)
			_, err := parseMap(t, m)
			require.Error(t, err)
			assert.ErrorContains(t, err, "session.resume_prompt")
		})
	}
}

func TestParseConfig_BadDocuments(t *testing.T) {
	valid, err := json.Marshal(baseConfig())
	require.NoError(t, err)

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "empty"},
		{"only whitespace", []byte(" \n\t"), "empty"},
		{"not JSON", []byte("{"), ConfigFile},
		{"an array", []byte("[]"), ConfigFile},
		{"a second object", append(append([]byte{}, valid...), []byte(`{}`)...), "unexpected data"},
		{"trailing text", append(append([]byte{}, valid...), []byte(` x`)...), "unexpected data"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfig(tc.data)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.Equal(t, Config{}, cfg)
		})
	}
}

func TestParseConfig_SizeLimit(t *testing.T) {
	valid, err := json.Marshal(baseConfig())
	require.NoError(t, err)

	atLimit := append(append([]byte{}, valid...), bytes.Repeat([]byte(" "), maxConfigBytes-len(valid))...)
	require.Len(t, atLimit, maxConfigBytes)
	_, err = ParseConfig(atLimit)
	assert.NoError(t, err, "exactly 64 KiB is allowed")

	_, err = ParseConfig(append(atLimit, ' '))
	require.Error(t, err)
	assert.ErrorContains(t, err, "bytes")
}
