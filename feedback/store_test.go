package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "feedback")
	in := Empty()
	th, _ := in.OpenThread(in.Add(note(1, "a", 0)), "a thread")
	require.NoError(t, in.SetFix(th, Fix{ID: 5, Title: "fb-1-a", Workspace: "loom", At: t0}))
	in.Fail(in.Add(note(2, "b", 3)), "boom", t0.Add(600e9))
	require.NoError(t, Save(dir, in))

	got, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, in, got)

	st, err := os.Stat(filepath.Join(dir, StoreFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	st, err = os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
}

func TestStore_MissingIsEmpty(t *testing.T) {
	got, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, Empty(), got)
}

func TestStore_NewerIsLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StoreFile)
	body := []byte(`{"version": 3, "shape": "new"}`)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	_, err := Load(dir)
	assert.ErrorIs(t, err, ErrNewer)
	after, _ := os.ReadFile(path)
	assert.Equal(t, body, after)
}

func TestStore_InvalidIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":        `{`,
		"version 0":       `{"version": 0}`,
		"a repeated note": `{"version":1,"notes":[{"id":1},{"id":1}]}`,
		"thread ID 0":     `{"version":1,"threads":[{"id":0}]}`,
	} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, StoreFile), []byte(body), 0o600))
		_, err := Load(dir)
		assert.Error(t, err, name)
	}
}

func TestStore_RaisesCountersPastTheIDs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, StoreFile),
		[]byte(`{"version":1,"next_note":1,"next_thread":0,"notes":[{"id":7}],"threads":[{"id":3}]}`), 0o600))
	got, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, NoteID(8), got.NextNote)
	assert.Equal(t, ThreadID(4), got.NextThread)
}

func TestStore_RoundTripKeepsTheLinkAndTheClosure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "feedback")
	in := Empty()
	linked, ids := threadOf(t, &in, 2)
	require.NoError(t, in.Publish(linked, loomRepo, 57, ids[0], t0))
	closed, _ := threadOf(t, &in, 1)
	require.NoError(t, in.Close(closed, "no fix: expected", t0.Add(time.Hour)))
	require.NoError(t, Save(dir, in))

	got, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, in, got)
	th, _ := got.Thread(linked)
	assert.Equal(t, Published, th.Status)
	assert.Equal(t, Issue{Repo: loomRepo, Number: 57, URL: IssueURL(loomRepo, 57), At: t0, Posted: ids[0]}, th.Issue)
	th, _ = got.Thread(closed)
	assert.Equal(t, Closure{At: t0.Add(time.Hour), Reason: "no fix: expected"}, th.Closed)

	data, err := os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), `"issue":`), "only the linked thread writes a link")
	assert.Equal(t, 1, strings.Count(string(data), `"closed":`), "only the closed thread writes a closure")
	assert.Contains(t, string(data), `"version": 2`)
}

func TestStore_VersionOneLoadsAndSavesAsVersionTwo(t *testing.T) {
	assert.Equal(t, 2, Version)
	dir := t.TempDir()
	path := filepath.Join(dir, StoreFile)
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"next_note":2,"next_thread":2,"notes":[{"id":1,"kind":"bug","title":"a","text":"x","from":{"id":1,"title":"s","workspace":"loom"},"at":"2026-11-05T12:00:00Z","thread":1,"grouping":"grouped"}],"threads":[{"id":1,"title":"a","kind":"bug","status":"open","notes":[1],"first":"2026-11-05T12:00:00Z","last":"2026-11-05T12:00:00Z"}]}`), 0o600))
	got, err := Load(dir)
	require.NoError(t, err)
	th, ok := got.Thread(1)
	require.True(t, ok)
	assert.Equal(t, Open, th.Status)
	assert.Zero(t, th.Issue)
	assert.Zero(t, th.Closed)

	require.NoError(t, Save(dir, got))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"version": 2`, "a save stamps this build's version")
	again, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, got.Threads, again.Threads)
}

func TestStore_VersionTwoLoads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, StoreFile),
		[]byte(`{"version":2,"threads":[{"id":1,"status":"published","notes":[3],"issue":{"repo":"aidan-bailey/loom","number":57,"posted":3}}]}`), 0o600))
	got, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, Published, got.Threads[0].Status)
	assert.Equal(t, 57, got.Threads[0].Issue.Number)
	assert.Equal(t, NoteID(3), got.Threads[0].Issue.Posted)
}
