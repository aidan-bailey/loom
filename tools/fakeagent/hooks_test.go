package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs(t *testing.T) {
	o := parseArgs([]string{"--append-system-prompt-file", "/c.md", "--settings", "/h/settings.json", "--resume", "abc"})
	assert.Equal(t, launchOptions{settings: "/h/settings.json", resume: "abc"}, o)
	assert.True(t, parseArgs([]string{"agents", "--json"}).agents)
	assert.Equal(t, launchOptions{}, parseArgs([]string{"--settings"}), "a flag with no value is ignored")
}

type recordedHook struct {
	command string
	payload map[string]any
}

func recordingEmitter(t *testing.T, got *[]recordedHook) *hookEmitter {
	t.Helper()
	cmds := map[string][]string{}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd"} {
		cmds[ev] = []string{"hook-" + ev}
	}
	return &hookEmitter{
		commands:   cmds,
		sessionID:  "8c634184-0fe5-4b62-b437-8f364eeeefcc",
		transcript: "/t/8c.jsonl",
		cwd:        "/w",
		run: func(command string, payload []byte) error {
			var m map[string]any
			require.NoError(t, json.Unmarshal(payload, &m))
			*got = append(*got, recordedHook{command: command, payload: m})
			return nil
		},
	}
}

func TestRun_EmitsHooksLikeClaude(t *testing.T) {
	var got []recordedHook
	var out bytes.Buffer
	a := newFakeAgent(personaFor("claude"), strings.NewReader("work 0\nask\ny\nexit\n"), &out, t.TempDir())
	a.sleep = func(time.Duration) {}
	a.hooks = recordingEmitter(t, &got)

	a.run()

	var names []string
	for _, h := range got {
		names = append(names, h.payload["hook_event_name"].(string))
		assert.Equal(t, "8c634184-0fe5-4b62-b437-8f364eeeefcc", h.payload["session_id"])
		assert.Equal(t, "hook-"+h.payload["hook_event_name"].(string), h.command)
	}
	assert.Equal(t, []string{"SessionStart", "UserPromptSubmit", "Stop", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd"}, names)
	assert.Equal(t, "startup", got[0].payload["source"])
	assert.Equal(t, "fakeagent finished: work 0", got[2].payload["last_assistant_message"])
	assert.Equal(t, "Bash", got[4].payload["tool_name"])
	assert.Equal(t, "prompt_input_exit", got[6].payload["reason"])
}

func TestRun_NoHooksWithoutSettings(t *testing.T) {
	_, code, _ := runScript(t, "claude", "work 0\nexit\n")
	assert.Equal(t, 0, code, "a nil emitter fires nothing and breaks nothing")
}

// The emitter's payloads, run through loom's real hook command, come back
// out of loom's scan as the events loom maps to status.
func TestHookEmitter_ThroughLoomsHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook commands need sh")
	}
	t.Setenv("TMPDIR", t.TempDir()) // transcripts go under os.TempDir()
	dir := filepath.Join(t.TempDir(), "hooks", "loom_x")
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	e, err := newHookEmitter(launchOptions{settings: hooks.SettingsPath(dir)}, "/w")
	require.NoError(t, err)

	e.sessionStart()
	e.emit("Stop", map[string]any{"last_assistant_message": "done", "background_tasks": []any{}})

	res, err := hooks.Scan(hooks.Request{Dir: dir}, time.Now())
	require.NoError(t, err)
	require.Len(t, res.Events, 2)
	assert.Equal(t, hooks.EventSessionStart, res.Events[0].Name)
	assert.Equal(t, e.sessionID, res.Events[0].SessionID)
	assert.Equal(t, "startup", res.Events[0].Source)
	assert.Equal(t, "done", res.Events[1].LastAssistantMessage)
	assert.FileExists(t, e.transcript)
}

func TestHookEmitter_ResumeKeepsTheID(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := filepath.Join(t.TempDir(), "hooks", "loom_x")
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"

	e, err := newHookEmitter(launchOptions{settings: hooks.SettingsPath(dir), resume: id}, "/w")
	require.NoError(t, err)

	assert.Equal(t, id, e.sessionID)
	assert.True(t, e.resumed)
}
