package advisor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallLog_RoundTripAndCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	none, err := LoadCalls(dir)
	require.NoError(t, err, "no file is an empty log")
	assert.Empty(t, none)

	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	var calls []CallRecord
	for i := range MaxCalls + 5 {
		calls = append(calls, CallRecord{At: t0.Add(time.Duration(i) * time.Minute), Kind: KindPrior, Job: 7, Outcome: OutcomeAnswered,
			Answer: `{"cpus":2}`, Usage: Usage{Model: "claude-opus-5-5", DurationMS: 3000, CostUSD: 0.01}})
	}
	require.NoError(t, SaveCalls(dir, calls))
	info, err := os.Stat(filepath.Join(dir, CallsFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := LoadCalls(dir)
	require.NoError(t, err)
	require.Len(t, got, MaxCalls, "only the last MaxCalls are kept")
	assert.Equal(t, t0.Add(5*time.Minute), got[0].At, "the oldest kept")
	assert.Equal(t, CallVersion, got[0].V)
	assert.Equal(t, `{"cpus":2}`, got[0].Answer)
}

func TestCallLog_AFileThatFailsToLoadIsLeftAsItIs(t *testing.T) {
	for name, body := range map[string]string{
		"garbled": "{nope\n",
		"newer":   fmt.Sprintf(`{"v": %d, "kind": "prior"}`+"\n", CallVersion+1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, CallsFile)
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := LoadCalls(dir)
			require.Error(t, err)
			left, _ := os.ReadFile(path)
			assert.Equal(t, body, string(left))
		})
	}
}
