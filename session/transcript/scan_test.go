package transcript

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scanned struct {
	off  int64
	data string
	long bool
}

func scanAll(t *testing.T, r *strings.Reader, from, to, budget int64) ([]scanned, bool) {
	t.Helper()
	var got []scanned
	reached, err := scanBack(r, from, to, budget, func(off int64, data []byte) bool {
		got = append(got, scanned{off, string(data), data == nil})
		return true
	})
	require.NoError(t, err)
	return got, reached
}

func TestScanBack_ReadsLinesAcrossChunks(t *testing.T) {
	lines := []string{strings.Repeat("a", 100<<10), "", "b", strings.Repeat("c", chunk-1), strings.Repeat("d", chunk), "e"}
	var b strings.Builder
	var offs []int64
	for _, l := range lines {
		offs = append(offs, int64(b.Len()))
		b.WriteString(l)
		b.WriteByte('\n')
	}
	r := strings.NewReader(b.String())
	end := int64(b.Len())

	got, reached := scanAll(t, r, 0, end, 1<<30)
	assert.True(t, reached)
	require.Len(t, got, len(lines))
	for i, s := range got {
		j := len(lines) - 1 - i
		assert.Equal(t, scanned{offs[j], lines[j], false}, s, "line %d, newest first", j)
	}

	got, reached = scanAll(t, r, offs[2], end, 1<<30)
	assert.True(t, reached)
	assert.Len(t, got, 4, "from a line's start: that line and the ones after it")

	got, reached = scanAll(t, r, 0, end, 3)
	assert.False(t, reached, "the budget ran out")
	assert.Equal(t, []scanned{{offs[5], "e", false}}, got)
}

func TestScanBack_ALineLongerThanTheLimitIsNil(t *testing.T) {
	old := lineLimit
	lineLimit = 1000
	t.Cleanup(func() { lineLimit = old })
	text := strings.Repeat("x", 5000) + "\nshort\n"
	got, reached := scanAll(t, strings.NewReader(text), 0, int64(len(text)), 1<<30)
	assert.True(t, reached)
	assert.Equal(t, []scanned{{5001, "short", false}, {0, "", true}}, got)
}

func TestLastLineEnd_IgnoresALineBeingWritten(t *testing.T) {
	text := "one\ntwo\nthr"
	end, err := lastLineEnd(strings.NewReader(text), int64(len(text)))
	require.NoError(t, err)
	assert.Equal(t, int64(8), end)
	end, err = lastLineEnd(strings.NewReader("no newline"), 10)
	require.NoError(t, err)
	assert.Zero(t, end)
}
