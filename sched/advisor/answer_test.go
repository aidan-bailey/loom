package advisor

import (
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

func TestParseNote(t *testing.T) {
	note, err := ParseNote([]byte(`{"note":"  a cold build after the sweep  "}`))
	require.NoError(t, err)
	assert.Equal(t, "a cold build after the sweep", note)
	_, err = ParseNote([]byte(`{"note":"   "}`))
	assert.Error(t, err)
}
