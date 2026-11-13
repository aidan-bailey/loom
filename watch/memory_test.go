package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeMemoryFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), MemoryFile)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestMemory_RoundTrip(t *testing.T) {
	at1 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	at2 := at1.Add(time.Hour)
	m := newMemory()
	m.PRs[123] = PRMemory{Nudges: map[string]NudgeRecord{
		"ci":       {N: 2, Key: "sha2", At: at2},
		"conflict": {N: 1, Key: "sha2+base", At: at1},
	}}
	m.PRs[5] = PRMemory{Settled: true}
	m.Issues[96] = IssueMemory{Escalated: &Escalation{At: at1, Reason: "stuck"}, Seen: "abc:77", Commented: "stuck"}
	m.Issues[97] = IssueMemory{Seen: "def:"}

	path := filepath.Join(t.TempDir(), MemoryFile)
	require.NoError(t, SaveMemory(path, m))
	got, err := LoadMemory(path)
	require.NoError(t, err)

	assert.Equal(t, 1, got.Version)
	ci := got.PRs[123].Nudges["ci"]
	assert.Equal(t, 2, ci.N)
	assert.Equal(t, "sha2", ci.Key)
	assert.True(t, ci.At.Equal(at2))
	assert.Equal(t, 1, got.PRs[123].Nudges["conflict"].N)
	assert.False(t, got.PRs[123].Settled)
	assert.True(t, got.PRs[5].Settled)
	require.NotNil(t, got.Issues[96].Escalated)
	assert.Equal(t, "stuck", got.Issues[96].Escalated.Reason)
	assert.True(t, got.Issues[96].Escalated.At.Equal(at1))
	assert.Equal(t, "abc:77", got.Issues[96].Seen)
	assert.Equal(t, "stuck", got.Issues[96].Commented)
	assert.Nil(t, got.Issues[97].Escalated)
	assert.Equal(t, "def:", got.Issues[97].Seen)
	assert.Empty(t, got.Issues[97].Commented)
}

// TestSaveMemory_WritesTheSpecsFormat pins the file's keys, which other
// builds and the owner's own editor read.
func TestSaveMemory_WritesTheSpecsFormat(t *testing.T) {
	at := time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC)
	m := newMemory()
	m.PRs[123] = PRMemory{Nudges: map[string]NudgeRecord{"ci": {N: 2, Key: "sha2", At: at}}}
	m.Issues[96] = IssueMemory{Escalated: &Escalation{At: at, Reason: "stuck"}, Seen: "abc:77", Commented: "stuck"}
	m.Issues[97] = IssueMemory{Seen: "def:"}

	path := filepath.Join(t.TempDir(), MemoryFile)
	require.NoError(t, SaveMemory(path, m))
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.EqualValues(t, 1, raw["version"])
	pr := raw["prs"].(map[string]any)["123"].(map[string]any)
	ci := pr["nudges"].(map[string]any)["ci"].(map[string]any)
	assert.EqualValues(t, 2, ci["n"])
	assert.Equal(t, "sha2", ci["key"])
	assert.Equal(t, "2026-11-13T11:00:00Z", ci["at"])
	assert.Equal(t, false, pr["settled"])
	issue := raw["issues"].(map[string]any)["96"].(map[string]any)
	assert.Equal(t, "abc:77", issue["seen"])
	assert.Equal(t, "stuck", issue["escalated"].(map[string]any)["reason"])
	assert.Equal(t, "stuck", issue["commented"], "the reason of the escalation whose comment was posted")
	assert.NotContains(t, raw["issues"].(map[string]any)["97"], "commented", "an empty commented is omitted")
}

func TestSaveMemory_ZeroMemoryWritesEmptyObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), MemoryFile)
	require.NoError(t, SaveMemory(path, Memory{}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.EqualValues(t, 1, raw["version"], "the version is stamped")
	assert.Equal(t, map[string]any{}, raw["prs"], "nil maps are written as {}, not null")
	assert.Equal(t, map[string]any{}, raw["issues"])
}

func TestSaveMemory_CreatesTheDirAndLeavesOnlyTheFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	path := filepath.Join(dir, MemoryFile)
	require.NoError(t, SaveMemory(path, newMemory()))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp file is left behind")
	assert.Equal(t, MemoryFile, entries[0].Name())
}

func TestSaveMemory_FailsWhenTheDirCannotBeMade(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	assert.Error(t, SaveMemory(filepath.Join(blocker, MemoryFile), newMemory()))
}

func TestLoadMemory_MissingFileIsEmptyMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), MemoryFile)
	m, err := LoadMemory(path)
	require.NoError(t, err)
	assert.Equal(t, 1, m.Version)
	assert.NotNil(t, m.PRs)
	assert.Empty(t, m.PRs)
	assert.NotNil(t, m.Issues)
	assert.Empty(t, m.Issues)
	assert.NoFileExists(t, path, "loading writes nothing")
}

// TestLoadMemory_RefusesAndKeepsTheFile: a file that can't be trusted stops
// the watcher for the workspace and stays exactly as it is.
func TestLoadMemory_RefusesAndKeepsTheFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"not JSON", `{nope`},
		{"empty file", ``},
		{"null", `null`},
		{"prs of the wrong type", `{"version":1,"prs":[]}`},
		{"a PR number that is not a number", `{"version":1,"prs":{"abc":{}}}`},
		{"no version", `{"prs":{}}`},
		{"version zero", `{"version":0,"prs":{}}`},
		{"version as a string", `{"version":"1"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMemoryFile(t, tc.content)
			_, err := LoadMemory(path)
			require.Error(t, err)
			assert.NotErrorIs(t, err, errMemoryNewer)
			data, rerr := os.ReadFile(path)
			require.NoError(t, rerr)
			assert.Equal(t, tc.content, string(data), "the file is left alone")
		})
	}

	t.Run("unreadable", func(t *testing.T) {
		_, err := LoadMemory(t.TempDir())
		assert.Error(t, err, "a directory where the file should be")
	})
}

func TestLoadMemory_NewerVersionIsRefusedBeforeItsShapeIsRead(t *testing.T) {
	const content = `{"version":2,"prs":"a shape this build does not know"}`
	path := writeMemoryFile(t, content)

	_, err := LoadMemory(path)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMemoryNewer)
	assert.ErrorContains(t, err, "version 2")
	data, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	assert.Equal(t, content, string(data), "the file is left alone")
}

func TestMemory_UnknownTopLevelKeysSurviveASave(t *testing.T) {
	path := writeMemoryFile(t, `{
  "version": 1,
  "future": {"a": [1, 2]},
  "prs": {"3": {"nudges": {"ci": {"n": 1, "key": "k", "at": "2026-01-02T03:04:05Z", "extra": 1}}, "settled": false, "mystery": true}},
  "issues": {},
  "note": "kept"
}`)
	m, err := LoadMemory(path)
	require.NoError(t, err)
	assert.Equal(t, 1, m.PRs[3].Nudges["ci"].N)

	require.NoError(t, SaveMemory(path, m))
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &top))
	assert.JSONEq(t, `{"a": [1, 2]}`, string(top["future"]))
	assert.JSONEq(t, `"kept"`, string(top["note"]))
	assert.NotContains(t, string(data), "mystery", "an unknown key inside a PR record is not kept")
	assert.NotContains(t, string(data), `"extra"`, "nor one inside a nudge record")

	again, err := LoadMemory(path)
	require.NoError(t, err)
	assert.Equal(t, 1, again.PRs[3].Nudges["ci"].N)
}
