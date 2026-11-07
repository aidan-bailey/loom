package attention

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalls_RoundTripKeepsTheLast(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attention")
	calls, err := LoadCalls(dir)
	require.NoError(t, err)
	assert.Empty(t, calls, "no file is no calls")

	stop := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	var many []CallRecord
	for i := range MaxCalls + 5 {
		many = append(many, CallRecord{At: stop, Session: uint64(i + 1), Stop: stop, Outcome: OutcomeAnswered, Verdict: Done})
	}
	require.NoError(t, SaveCalls(dir, many))
	info, err := os.Stat(filepath.Join(dir, CallsFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := LoadCalls(dir)
	require.NoError(t, err)
	require.Len(t, got, MaxCalls)
	assert.Equal(t, uint64(6), got[0].Session, "the oldest are dropped")
	assert.Equal(t, CallVersion, got[0].V)
	assert.True(t, got[0].Stop.Equal(stop))
}

func TestCalls_ANewerOrBadFileIsAnErrorAndLeftAlone(t *testing.T) {
	for _, body := range []string{`{"v":99}` + "\n", "not json\n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, CallsFile)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, err := LoadCalls(dir)
		require.Error(t, err)
		after, _ := os.ReadFile(path)
		assert.Equal(t, body, string(after))
	}
}
