package subagent

import (
	"bytes"
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
	got, err := SettingsJSON("/cfg/hooks/loom_x/events")
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
			entry("SessionEnd"), entry("Stop"), entry("SubagentStart"),
			entry("SubagentStop"), entry("TeammateIdle"),
		}, ",\n") +
		"\n  }\n}\n"
	assert.Equal(t, want, string(got))
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

	id, err := Prepare(dir)
	require.NoError(t, err)
	assert.Len(t, id, 16)

	settings, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	want, err := SettingsJSON(EventsDir(dir))
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
	first, err := Prepare(dir)
	require.NoError(t, err)
	old := filepath.Join(EventsDir(dir), "1-1.ev")
	require.NoError(t, os.WriteFile(old, []byte("{}"), 0o600))

	second, err := Prepare(dir)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.NoFileExists(t, old)
}

func TestPrepare_RejectsSingleQuote(t *testing.T) {
	_, err := Prepare(filepath.Join(t.TempDir(), "it's"))
	assert.Error(t, err)
}
