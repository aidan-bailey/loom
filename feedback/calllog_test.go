package feedback

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendCall_AppendsLines(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, AppendCall(dir, CallRecord{At: t0, Note: 1, Model: "haiku", Effort: "low", Thread: 1}))
	require.NoError(t, AppendCall(dir, CallRecord{At: t0, Note: 2, Model: "haiku", Effort: "low", Err: "boom"}))
	f, err := os.Open(filepath.Join(dir, CallLogFile))
	require.NoError(t, err)
	defer f.Close()
	var notes []NoteID
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var r CallRecord
		require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
		notes = append(notes, r.Note)
	}
	assert.Equal(t, []NoteID{1, 2}, notes)
	st, _ := os.Stat(filepath.Join(dir, CallLogFile))
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}
