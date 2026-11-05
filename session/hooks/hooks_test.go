package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsJSON_Golden(t *testing.T) {
	got, err := SettingsJSON("/cfg/hooks/loom_x/events", "", "", "")
	require.NoError(t, err)

	cmd := `f='/cfg/hooks/loom_x/events/'\"$(date +%s%N)-$$\"; { cat > \"$f.tmp\" && mv \"$f.tmp\" \"$f.json\"; } 2>/dev/null || cat >/dev/null`
	entry := func(name string) string {
		return `    "` + name + `": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "` + cmd + `"
          }
        ]
      }
    ]`
	}
	want := "{\n  \"hooks\": {\n" +
		strings.Join([]string{
			entry("Notification"), entry("PermissionRequest"), entry("SessionEnd"),
			entry("SessionStart"), entry("Stop"), entry("SubagentStart"),
			entry("SubagentStop"), entry("TeammateIdle"), entry("UserPromptSubmit"),
		}, ",\n") +
		"\n  }\n}\n"
	assert.Equal(t, want, string(got))
}

// An account session's memory dir rides in the same file, so it needs no
// second --settings flag; without one the file is the hooks alone.
func TestSettingsJSON_AutoMemoryDirectory(t *testing.T) {
	got, err := SettingsJSON("/e", "/home/u/.claude/projects/-r/memory", "", "")
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(got, &doc))
	assert.Equal(t, "/home/u/.claude/projects/-r/memory", doc["autoMemoryDirectory"])
	assert.Contains(t, doc, "hooks")

	got, err = SettingsJSON("/e", "", "", "")
	require.NoError(t, err)
	assert.NotContains(t, string(got), "autoMemoryDirectory")
}

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook commands need sh")
	}
}

func runHook(t *testing.T, eventsDir string, stdin []byte) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", HookCommand(eventsDir))
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.Run()
}

func TestHookCommand_WritesPayloadAtomically(t *testing.T) {
	requireShell(t)
	dir := filepath.Join(t.TempDir(), "with space", "events")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	require.NoError(t, runHook(t, dir, []byte(`{"hook_event_name":"Stop"}`)))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.HasSuffix(entries[0].Name(), ".json"))
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	assert.Equal(t, `{"hook_event_name":"Stop"}`, string(data))
}

// When the folder is gone the hook must still exit 0 and read all of its
// input, or Claude would report a hook error or fail writing the payload.
func TestHookCommand_MissingFolderExitsZeroAndDrains(t *testing.T) {
	requireShell(t)
	missing := filepath.Join(t.TempDir(), "gone", "events")

	r, w, err := os.Pipe()
	require.NoError(t, err)
	cmd := exec.Command("sh", "-c", HookCommand(missing))
	cmd.Stdin = r
	require.NoError(t, cmd.Start())
	require.NoError(t, r.Close())

	big := bytes.Repeat([]byte("x"), 1<<20) // larger than a pipe buffer
	_, writeErr := w.Write(big)
	require.NoError(t, w.Close())

	assert.NoError(t, cmd.Wait())
	assert.NoError(t, writeErr, "the hook must consume its whole input")
}

func TestSafePath(t *testing.T) {
	assert.True(t, SafePath("/home/u/.loom/hooks/loom_a b"))
	assert.False(t, SafePath("/home/u/it's/hooks"))
}

func TestPrepare_CreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks", "loom_x")

	id, err := Prepare(dir, "", "")
	require.NoError(t, err)
	assert.Len(t, id, 16)

	settings, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	want, err := SettingsJSON(EventsDir(dir), "", "", "")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(settings))

	stored, err := os.ReadFile(launchIDPath(dir))
	require.NoError(t, err)
	assert.Equal(t, id, string(stored))

	info, err := os.Stat(EventsDir(dir))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
}

func TestPrepare_ClearsPreviousLaunch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom_x")
	first, err := Prepare(dir, "", "")
	require.NoError(t, err)
	old := filepath.Join(EventsDir(dir), "1-1.ev")
	require.NoError(t, os.WriteFile(old, []byte("{}"), 0o600))

	second, err := Prepare(dir, "", "")
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.NoFileExists(t, old)
}

func TestPrepare_RejectsSingleQuote(t *testing.T) {
	_, err := Prepare(filepath.Join(t.TempDir(), "it's"), "", "")
	assert.Error(t, err)
}

// A brief adds a second SubagentStart entry, for every agent type but a
// fork, and leaves the recorder's entry as it was; without one the
// settings are the recorder's alone (TestSettingsJSON_Golden).
func TestSettingsJSON_BriefsSubagentsButNotForks(t *testing.T) {
	got, err := SettingsJSON("/e", "", "/h/subagent-context.json", "")
	require.NoError(t, err)
	var doc settingsDoc
	require.NoError(t, json.Unmarshal(got, &doc))
	start := doc.Hooks[EventSubagentStart]
	require.Len(t, start, 2)
	assert.Equal(t, hookMatcher{Hooks: []hookCommand{{Type: "command", Command: HookCommand("/e")}}}, start[0],
		"the recorder still sees every agent, forks included")
	assert.Equal(t, hookMatcher{Matcher: "^(?!fork$)", Hooks: []hookCommand{{Type: "command", Command: briefCommand("/h/subagent-context.json")}}}, start[1])
	for _, name := range HookEvents {
		if name != EventSubagentStart {
			assert.Len(t, doc.Hooks[name], 1, name)
		}
	}

	got, err = SettingsJSON("/e", "", "", "")
	require.NoError(t, err)
	var bare settingsDoc
	require.NoError(t, json.Unmarshal(got, &bare))
	assert.Len(t, bare.Hooks[EventSubagentStart], 1)
	assert.NotContains(t, string(got), "matcher")
}

// The brief command prints the hook output loom wrote, and with the file
// gone prints nothing. Either way it reads its whole input and exits 0,
// as HookCommand does: Claude pipes the payload to it.
func TestBriefCommand_ReadsItsInputAndExitsZero(t *testing.T) {
	requireShell(t)
	dir := filepath.Join(t.TempDir(), "with space")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	present := filepath.Join(dir, "subagent-context.json")
	require.NoError(t, os.WriteFile(present, []byte(`{"hookSpecificOutput":{}}`), 0o600))

	for path, want := range map[string]string{present: `{"hookSpecificOutput":{}}`, filepath.Join(dir, "gone.json"): ""} {
		r, w, err := os.Pipe()
		require.NoError(t, err)
		var out bytes.Buffer
		cmd := exec.Command("sh", "-c", briefCommand(path))
		cmd.Stdin, cmd.Stdout = r, &out
		require.NoError(t, cmd.Start())
		require.NoError(t, r.Close())
		_, writeErr := w.Write(bytes.Repeat([]byte("x"), 1<<20)) // larger than a pipe buffer
		require.NoError(t, w.Close())

		assert.NoError(t, cmd.Wait(), path)
		assert.NoError(t, writeErr, "the command must consume its whole input")
		assert.Equal(t, want, out.String(), path)
	}
}

// Prepare writes a brief as the SubagentStart output the brief command
// prints, and registers the command; with no brief it writes neither.
func TestPrepare_WritesTheSubagentBrief(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom_x")
	brief := "Commit by path <always> & keep scratch files out."
	_, err := Prepare(dir, "", brief)
	require.NoError(t, err)

	b, err := os.ReadFile(BriefPath(dir))
	require.NoError(t, err)
	var out map[string]map[string]string
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, map[string]string{"hookEventName": "SubagentStart", "additionalContext": brief}, out["hookSpecificOutput"])
	settings, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	want, err := SettingsJSON(EventsDir(dir), "", BriefPath(dir), "")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(settings))

	_, err = Prepare(dir, "", "")
	require.NoError(t, err)
	assert.NoFileExists(t, BriefPath(dir))
	settings, err = os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	assert.NotContains(t, string(settings), "subagent-context.json")
}

func TestSettingsJSON_GuardsFlowsAgentsOnlyWhenAsked(t *testing.T) {
	without, err := SettingsJSON("/e", "", "", "")
	require.NoError(t, err)
	assert.NotContains(t, string(without), "PreToolUse")

	with, err := SettingsJSON("/e", "", "", "'/bin/loom' flow guard")
	require.NoError(t, err)
	var doc settingsDoc
	require.NoError(t, json.Unmarshal(with, &doc))
	require.Len(t, doc.Hooks["PreToolUse"], 1)
	entry := doc.Hooks["PreToolUse"][0]
	assert.Equal(t, "Bash|Write|Edit", entry.Matcher)
	assert.Equal(t, []hookCommand{{Type: "command", Command: "'/bin/loom' flow guard", Timeout: 10}}, entry.Hooks)
	for _, name := range HookEvents {
		assert.Empty(t, doc.Hooks[name][0].Matcher, "the recorder's entries keep no matcher: %s", name)
	}
}

func TestPrepareGuarded_WritesTheGuardIntoTheSettings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom_x")
	_, err := PrepareGuarded(dir, "", "", "'/bin/loom' flow guard")
	require.NoError(t, err)
	got, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	want, err := SettingsJSON(EventsDir(dir), "", "", "'/bin/loom' flow guard")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}
