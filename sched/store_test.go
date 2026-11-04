package sched

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeRecord is a finished job with every field of a Record set, so that a
// field the codec drops shows.
func storeRecord(id JobID) Record {
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	return Record{
		ID:           id,
		Order:        int64(id),
		Owner:        7,
		Description:  "polyoxide tests",
		Argv:         []string{"cargo", "test"},
		Cwd:          "/work/tree",
		Claim:        Claim{CPUs: 4, MemoryGB: 6, IO: true},
		Notify:       true,
		Tail:         20,
		State:        Failed,
		Submitted:    t0,
		Started:      t0.Add(time.Minute),
		Ended:        t0.Add(5 * time.Minute),
		PID:          4242,
		Exit:         &Exit{Code: 101, Ended: t0.Add(5 * time.Minute), MaxRSSKB: 2048},
		Failure:      "the job runner did not start",
		PeakRSSKB:    4096,
		CancelReason: "from the work view",
		SignalledAt:  t0.Add(4 * time.Minute),
		Notice:       NoticeUndelivered,
		NoticeWhy:    "the session is not running",
	}
}

// storeKeys returns m's keys, sorted.
func storeKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// writeStoreFile writes content as jobs.json in a fresh dir and returns the
// dir.
func writeStoreFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, StoreFile), []byte(content), 0o600))
	return dir
}

func TestStore_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	in := Store{Version: StoreVersion, NextID: 4, Jobs: []Record{storeRecord(1), storeRecord(2), storeRecord(3)}}
	require.NoError(t, SaveStore(dir, in))
	got, err := LoadStore(dir)
	require.NoError(t, err)
	assert.Equal(t, in, got)

	next := Store{Version: StoreVersion, NextID: 5, Jobs: []Record{storeRecord(4)}}
	require.NoError(t, SaveStore(dir, next))
	got, err = LoadStore(dir)
	require.NoError(t, err)
	assert.Equal(t, next, got, "a save replaces the file")
}

// TestSaveStore_ModesAndFormat: the file is 0600 in a 0700 directory, made
// along with its parents, stamped with the version, and a queued record omits
// what it has not got yet.
func TestSaveStore_ModesAndFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "global", "jobs")
	queued := Record{
		ID: 1, Order: 1, Description: "d", Argv: []string{"true"}, Cwd: "/w",
		Claim: Claim{CPUs: 1, MemoryGB: 1}, Tail: 20, State: Queued,
		Submitted: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC),
	}
	require.NoError(t, SaveStore(dir, Store{NextID: 2, Jobs: []Record{queued}}))
	assertJobFileMode(t, dir, 0o700)
	assertJobFileMode(t, filepath.Join(dir, StoreFile), 0o600)

	raw, err := os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, err)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))
	assert.Equal(t, []string{"jobs", "next_id", "version"}, storeKeys(top))
	assert.JSONEq(t, `1`, string(top["version"]), "SaveStore stamps StoreVersion")
	var jobs []map[string]any
	require.NoError(t, json.Unmarshal(top["jobs"], &jobs))
	require.Len(t, jobs, 1)
	assert.Equal(t,
		[]string{"argv", "claim", "cwd", "description", "id", "order", "state", "submitted", "tail"},
		storeKeys(jobs[0]), "a queued record omits what it has not got yet")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the atomic write leaves no temp file")

	require.NoError(t, SaveStore(dir, Store{NextID: 1}))
	raw, err = os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &top))
	assert.JSONEq(t, `[]`, string(top["jobs"]), "no jobs is written as an empty list")
}

func TestLoadStore_NoFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	got, err := LoadStore(dir)
	require.NoError(t, err)
	assert.Equal(t, Store{Version: StoreVersion, NextID: 1}, got)
	assert.NoDirExists(t, dir, "loading creates nothing")
}

// TestLoadStore_NewerVersionIsLeftUntouched: a jobs.json from a newer loom
// is reported as ErrNewerStore even when its shape is one this build cannot
// parse, and the file is not touched.
func TestLoadStore_NewerVersionIsLeftUntouched(t *testing.T) {
	content := `{"version": 2, "next_id": 9, "jobs": "a shape this loom does not know", "extra": true}`
	dir := writeStoreFile(t, content)

	got, err := LoadStore(dir)
	assert.ErrorIs(t, err, ErrNewerStore)
	assert.Equal(t, Store{}, got)

	after, readErr := os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, readErr)
	assert.Equal(t, content, string(after), "the file is as it was")
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Len(t, entries, 1, "nothing else was written beside it")
}

func TestLoadStore_InvalidFilesAreLeftUntouched(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string // a part of the error
	}{
		{"truncated", `{"version": 1, "next_id": 3, "jobs": [`, "parse"},
		{"not an object", `[]`, "parse"},
		{"no version", `{"next_id": 1, "jobs": []}`, "version 0"},
		{"a negative version", `{"version": -1}`, "version -1"},
		{"an ID below 1", `{"version": 1, "next_id": 3, "jobs": [{"id": 0}]}`, "job ID 0"},
		{"a repeated ID", `{"version": 1, "next_id": 3, "jobs": [{"id": 2}, {"id": 2}]}`, "job 2 appears twice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeStoreFile(t, tc.content)
			got, err := LoadStore(dir)
			assert.ErrorContains(t, err, tc.want)
			assert.NotErrorIs(t, err, ErrNewerStore)
			assert.Equal(t, Store{}, got)

			after, readErr := os.ReadFile(filepath.Join(dir, StoreFile))
			require.NoError(t, readErr)
			assert.Equal(t, tc.content, string(after), "the file is as it was")
		})
	}
}

// TestLoadStore_RaisesNextIDPastEveryJob: a job directory is named by its ID,
// so a counter at or below an ID on disk would reuse it; the load raises it,
// and never lowers it.
func TestLoadStore_RaisesNextIDPastEveryJob(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    JobID
	}{
		{"a counter behind the jobs", `{"version": 1, "next_id": 2, "jobs": [{"id": 5}, {"id": 3}]}`, 6},
		{"a counter at the highest ID", `{"version": 1, "next_id": 5, "jobs": [{"id": 5}]}`, 6},
		{"a counter ahead of the jobs", `{"version": 1, "next_id": 9, "jobs": [{"id": 3}]}`, 9},
		{"no counter and no jobs", `{"version": 1, "jobs": []}`, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadStore(writeStoreFile(t, tc.content))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.NextID)
		})
	}
}
