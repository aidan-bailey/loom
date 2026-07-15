package vt

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func filterAll(t *testing.T, chunks ...string) string {
	t.Helper()
	var out bytes.Buffer
	f := NewAltScreenFilter(&out)
	for _, c := range chunks {
		n, err := f.Write([]byte(c))
		require.NoError(t, err)
		require.Equal(t, len(c), n, "filter must report full consumption")
	}
	return out.String()
}

func TestAltScreenFilter_StripsEnterExit(t *testing.T) {
	require.Equal(t, "ab", filterAll(t, "a\x1b[?1049hb"))
	require.Equal(t, "ab", filterAll(t, "a\x1b[?1049lb"))
	require.Equal(t, "ab", filterAll(t, "a\x1b[?1047hb"))
	require.Equal(t, "ab", filterAll(t, "a\x1b[?47hb"))
}

func TestAltScreenFilter_RewritesCombinedParams(t *testing.T) {
	// tmux may set several private modes in one sequence; only the
	// alt-screen ones are removed.
	require.Equal(t, "\x1b[?1000h", filterAll(t, "\x1b[?1000;1049h"))
	require.Equal(t, "\x1b[?1000;1002h", filterAll(t, "\x1b[?1000;1049;1002h"))
}

func TestAltScreenFilter_PassesUnrelatedSequences(t *testing.T) {
	in := "x\x1b[31mred\x1b[0m\x1b[?25l\x1b]0;title\x07\x1b[2Jy"
	require.Equal(t, in, filterAll(t, in))
}

func TestAltScreenFilter_HandlesChunkSplitSequences(t *testing.T) {
	// The 1049h sequence arrives split across three Writes.
	require.Equal(t, "ab", filterAll(t, "a\x1b[?", "104", "9hb"))
	// A split SGR must survive untouched.
	require.Equal(t, "a\x1b[3;1mb", filterAll(t, "a\x1b[3;", "1mb"))
}

func TestAltScreenFilter_FlushesOversizeSequences(t *testing.T) {
	// A pathological never-terminating "sequence" must not buffer forever.
	long := "\x1b[?" + string(bytes.Repeat([]byte("1;"), 64))
	out := filterAll(t, long)
	require.NotEmpty(t, out, "oversize partial sequences must flush through")
}
