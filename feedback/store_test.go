package feedback

import (
	"os"
	"path/filepath"
	"testing"

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
	body := []byte(`{"version": 2, "shape": "new"}`)
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
