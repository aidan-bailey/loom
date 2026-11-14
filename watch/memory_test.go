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

func TestSaveMemory_ADifferentlyCasedMemoryKeyIsNotKeptAsAnUnknownOne(t *testing.T) {
	// encoding/json matches keys without regard to case, so "PRS" is the
	// memory's own key: kept as an unknown one it would be written back
	// beside the real "prs".
	path := writeMemoryFile(t, `{"version": 1, "PRS": {"3": {"settled": true}}, "Issues": {}, "note": "kept"}`)
	m, err := LoadMemory(path)
	require.NoError(t, err)
	assert.True(t, m.PRs[3].Settled)

	require.NoError(t, SaveMemory(path, m))
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &top))
	assert.NotContains(t, top, "PRS")
	assert.NotContains(t, top, "Issues")
	assert.Contains(t, top, "prs")
	assert.Contains(t, top, "note")
}

func TestMemory_RecordNudge(t *testing.T) {
	at1 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	at2 := at1.Add(time.Hour)
	var m Memory // the zero value works: its maps are nil

	assert.Equal(t, 1, m.RecordNudge(1, "ci", "a", at1))
	assert.Equal(t, 2, m.RecordNudge(1, "ci", "b", at2))
	assert.Equal(t, 1, m.RecordNudge(1, "conflict", "c", at1), "counts are per kind")
	assert.Equal(t, 1, m.RecordNudge(2, "ci", "a", at1), "and per PR")

	assert.Equal(t, NudgeRecord{N: 2, Key: "b", At: at2}, m.PRs[1].Nudges["ci"], "the latest key and time are kept")
	assert.Equal(t, NudgeRecord{N: 1, Key: "c", At: at1}, m.PRs[1].Nudges["conflict"])
	assert.Equal(t, 1, m.PRs[2].Nudges["ci"].N)
}

func TestMemory_Settle(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	var m Memory
	m.Settle(nil)
	assert.Nil(t, m.PRs, "settling nothing creates nothing")

	m.RecordNudge(1, "ci", "a", at)
	m.RecordNudge(2, "ci", "a", at)
	m.Settle([]int{1, 9})
	m.Settle([]int{1})

	assert.True(t, m.PRs[1].Settled)
	assert.Equal(t, 1, m.PRs[1].Nudges["ci"].N, "settling keeps the counts")
	assert.True(t, m.PRs[9].Settled, "a PR the memory had no record of is settled too")
	assert.False(t, m.PRs[2].Settled, "a PR not named is not")
}

func TestMemory_RecordEscalationAndSetSeen(t *testing.T) {
	at1 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	at2 := at1.Add(time.Hour)
	var m Memory

	m.SetSeen(96, "h1:c1")
	m.RecordEscalation(96, "first", at1)
	assert.Equal(t, "h1:c1", m.Issues[96].Seen, "escalating keeps what was seen")
	require.NotNil(t, m.Issues[96].Escalated)
	assert.Equal(t, "first", m.Issues[96].Escalated.Reason)

	m.SetSeen(96, "h2:c2")
	assert.Equal(t, "h2:c2", m.Issues[96].Seen)
	require.NotNil(t, m.Issues[96].Escalated, "seeing keeps the escalation")

	m.RecordEscalation(96, "second", at2)
	assert.Equal(t, "second", m.Issues[96].Escalated.Reason, "a later escalation replaces the record")
	assert.True(t, m.Issues[96].Escalated.At.Equal(at2))

	m.SetSeen(97, "x:")
	assert.Nil(t, m.Issues[97].Escalated)
	assert.Equal(t, "second", m.Issues[96].Escalated.Reason, "another issue is untouched")
}

func TestMemory_ClearCommentedKeepsTheRestOfTheIssue(t *testing.T) {
	var m Memory
	m.ClearCommented(96) // no record: nothing to do, and no panic on the zero Memory
	assert.Empty(t, m.Issues)

	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m.RecordCommented(96, "why")
	m.RecordEscalation(96, "why", at)
	m.SetSeen(96, "abc:1")
	m.RecordCommented(97, "other")
	m.ClearCommented(96)

	assert.Empty(t, m.Issues[96].Commented)
	require.NotNil(t, m.Issues[96].Escalated)
	assert.Equal(t, "abc:1", m.Issues[96].Seen)
	assert.Equal(t, "other", m.Issues[97].Commented, "another issue is untouched")
}

func TestMemory_RecordCommented(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	var m Memory

	m.RecordCommented(96, "first")
	assert.Equal(t, "first", m.Issues[96].Commented, "the zero Memory works")
	assert.Nil(t, m.Issues[96].Escalated, "commenting records no escalation")

	m.RecordEscalation(96, "first", at)
	m.SetSeen(96, "h:c")
	assert.Equal(t, "first", m.Issues[96].Commented, "escalating and seeing keep the commented reason")

	m.RecordCommented(96, "second")
	assert.Equal(t, "second", m.Issues[96].Commented, "a later comment replaces the reason")
	require.NotNil(t, m.Issues[96].Escalated, "commenting keeps the escalation")
	assert.Equal(t, "first", m.Issues[96].Escalated.Reason)
	assert.Equal(t, "h:c", m.Issues[96].Seen, "and what was seen")

	m.RecordCommented(97, "other")
	assert.Equal(t, "second", m.Issues[96].Commented, "another issue is untouched")
}

func TestMemory_RecordsSurviveASaveAndLoad(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	var m Memory
	m.RecordNudge(7, "ci", "sha", at)
	m.Settle([]int{8})
	m.RecordEscalation(96, "why", at)
	m.SetSeen(96, "h:c")
	m.RecordCommented(96, "why")

	path := filepath.Join(t.TempDir(), MemoryFile)
	require.NoError(t, SaveMemory(path, m))
	got, err := LoadMemory(path)
	require.NoError(t, err)

	assert.Equal(t, 1, got.PRs[7].Nudges["ci"].N)
	assert.True(t, got.PRs[8].Settled)
	assert.Equal(t, "why", got.Issues[96].Escalated.Reason)
	assert.Equal(t, "h:c", got.Issues[96].Seen)
	assert.Equal(t, "why", got.Issues[96].Commented)
}

func TestMemory_CloneSharesNothing(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	path := writeMemoryFile(t, `{"version":1,"future":{"a":1},"prs":{},"issues":{}}`)
	orig, err := LoadMemory(path)
	require.NoError(t, err)
	orig.RecordNudge(1, "ci", "a", at)
	orig.RecordEscalation(96, "why", at)
	orig.RecordCommented(96, "why")

	c := orig.Clone()
	assert.Equal(t, "why", c.Issues[96].Commented, "the clone carries the commented reason")
	c.PRs[1].Nudges["ci"] = NudgeRecord{N: 9}
	c.Issues[96].Escalated.Reason = "changed"
	c.RecordNudge(2, "ci", "b", at)
	c.Settle([]int{1})
	c.SetSeen(96, "new")
	c.RecordCommented(96, "other")

	assert.Equal(t, 1, orig.PRs[1].Nudges["ci"].N, "the clone's nudge map is its own")
	assert.Equal(t, "why", orig.Issues[96].Escalated.Reason, "and its escalation")
	assert.NotContains(t, orig.PRs, 2)
	assert.False(t, orig.PRs[1].Settled)
	assert.Empty(t, orig.Issues[96].Seen)
	assert.Equal(t, "why", orig.Issues[96].Commented, "the commented reason is a value, not shared")

	out := filepath.Join(t.TempDir(), MemoryFile)
	require.NoError(t, SaveMemory(out, c))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"future"`, "the clone keeps the unknown top-level keys")
}
