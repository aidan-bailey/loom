package advisor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePrior(t *testing.T) {
	known := func(id sched.SignatureID) bool { return id == "aaaaaaaaaaaa" }
	a, err := ParsePrior([]byte(`{"like":"aaaaaaaaaaaa","cpus":4,"memory_gb":12,"io":true,"duration_seconds":300,"note":"`+strings.Repeat("n", 400)+`"}`), 4, 22, known)
	require.NoError(t, err)
	assert.Equal(t, sched.SignatureID("aaaaaaaaaaaa"), a.Like)
	assert.Equal(t, 12, a.MemoryGB)
	assert.Len(t, []rune(a.Note), MaxNote, "cut")
	bad := []string{
		`{"like":"","cpus":5,"memory_gb":1,"io":false,"duration_seconds":1,"note":""}`,
		`{"like":"","cpus":0,"memory_gb":1,"io":false,"duration_seconds":1,"note":""}`,
		`{"like":"","cpus":1,"memory_gb":23,"io":false,"duration_seconds":1,"note":""}`,
		`{"like":"","cpus":1,"memory_gb":1,"io":false,"duration_seconds":86401,"note":""}`,
		`{"like":"bbbbbbbbbbbb","cpus":1,"memory_gb":1,"io":false,"duration_seconds":1,"note":""}`,
		`not json`,
	}
	for _, raw := range bad {
		_, err := ParsePrior([]byte(raw), 4, 22, known)
		assert.Error(t, err, raw)
	}
}

// TestParsePrior_QuotesAroundALikeAreNotPartOfIt pins what the 2026-11-05
// replay found: Haiku writes "no like" as the literal string "" in a third of
// its answers, and Opus once wrote a lone quote. Quotes and spaces around a
// like are dropped; what is left is still checked against the beliefs given.
func TestParsePrior_QuotesAroundALikeAreNotPartOfIt(t *testing.T) {
	known := func(id sched.SignatureID) bool { return id == "aaaaaaaaaaaa" }
	answer := func(like string) []byte {
		raw, err := json.Marshal(map[string]any{"like": like, "cpus": 2, "memory_gb": 3, "io": false, "duration_seconds": 60, "note": "n"})
		require.NoError(t, err)
		return raw
	}
	for _, like := range []string{`""`, `"`, ` `, `" "`} {
		a, err := ParsePrior(answer(like), 4, 22, known)
		require.NoError(t, err, "like %q", like)
		assert.Empty(t, a.Like, "like %q is no like", like)
	}
	a, err := ParsePrior(answer(` "aaaaaaaaaaaa" `), 4, 22, known)
	require.NoError(t, err)
	assert.Equal(t, sched.SignatureID("aaaaaaaaaaaa"), a.Like)
	for _, like := range []string{"e", `"bbbbbbbbbbbb"`} {
		_, err := ParsePrior(answer(like), 4, 22, known)
		assert.Error(t, err, "like %q names no belief given", like)
	}
}

func TestParseNote(t *testing.T) {
	note, err := ParseNote([]byte(`{"note":"  a cold build after the sweep  "}`))
	require.NoError(t, err)
	assert.Equal(t, "a cold build after the sweep", note)
	_, err = ParseNote([]byte(`{"note":"   "}`))
	assert.Error(t, err)
}
