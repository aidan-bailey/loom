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

// filterWithoutMouseAll is filterAll through NewAltScreenFilterWithoutMouse.
func filterWithoutMouseAll(t *testing.T, chunks ...string) string {
	t.Helper()
	var out bytes.Buffer
	f := NewAltScreenFilterWithoutMouse(&out)
	for _, c := range chunks {
		n, err := f.Write([]byte(c))
		require.NoError(t, err)
		require.Equal(t, len(c), n, "filter must report full consumption")
	}
	return out.String()
}

var mouseModes = []string{"1000", "1002", "1003", "1005", "1006", "1015"}

func TestAltScreenFilterWithoutMouse_StripsEachMouseMode(t *testing.T) {
	for _, mode := range mouseModes {
		for _, final := range []string{"h", "l"} {
			t.Run(mode+final, func(t *testing.T) {
				require.Equal(t, "ab", filterWithoutMouseAll(t, "a\x1b[?"+mode+final+"b"))
			})
		}
	}
}

func TestAltScreenFilterWithoutMouse_KeepsTheOtherModesOfASequence(t *testing.T) {
	require.Equal(t, "\x1b[?25h", filterWithoutMouseAll(t, "\x1b[?1000;25;1006h"))
	require.Equal(t, "\x1b[?25l", filterWithoutMouseAll(t, "\x1b[?1049;1000;1002;25;1006;1015l"))
	require.Equal(t, "\x1b[?25;2004h", filterWithoutMouseAll(t, "\x1b[?25;1003;2004h"))
}

func TestAltScreenFilterWithoutMouse_DropsASequenceLeftEmpty(t *testing.T) {
	require.Equal(t, "ab", filterWithoutMouseAll(t, "a\x1b[?1000;1002;1003;1005;1006;1015hb"))
	require.Equal(t, "ab", filterWithoutMouseAll(t, "a\x1b[?1049;1006hb"))
}

func TestAltScreenFilterWithoutMouse_HandlesChunkSplitSequences(t *testing.T) {
	require.Equal(t, "ab", filterWithoutMouseAll(t, "a\x1b[?10", "06hb"))
	require.Equal(t, "a\x1b[?25lb", filterWithoutMouseAll(t, "a\x1b[?25;1", "000lb"))
	require.Equal(t, "ab", filterWithoutMouseAll(t, "a\x1b[?", "1000;1006", "hb"))
}

func TestAltScreenFilterWithoutMouse_StillStripsTheAltScreenModesAndPassesTheRest(t *testing.T) {
	require.Equal(t, "ab", filterWithoutMouseAll(t, "a\x1b[?1049hb"))
	in := "x\x1b[?25l\x1b[?1004h\x1b[?2004h\x1b[?1h\x1b[1000h\x1b[1000;1006H\x1b[?100h\x1b[31mred\x1b[0m\x1b]0;title\x07"
	require.Equal(t, in, filterWithoutMouseAll(t, in))
}

func TestAltScreenFilter_PassesEveryMouseMode(t *testing.T) {
	for _, mode := range mouseModes {
		for _, final := range []string{"h", "l"} {
			in := "a\x1b[?" + mode + final + "b"
			require.Equal(t, in, filterAll(t, in), mode+final)
		}
	}
	require.Equal(t, "\x1b[?1000;1006h", filterAll(t, "\x1b[?1000;1049;1006h"))
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

func TestAltScreenFilter_AbortedCSIStartsFreshSequence(t *testing.T) {
	// An embedded ESC before a CSI final byte aborts that CSI. "\x1b[12"
	// never gets a final byte before the next ESC starts "\x1b[?1049h" —
	// the aborted prefix passes through verbatim, and the well-formed
	// 1049h sequence that follows is still recognized and stripped.
	require.Equal(t, "\x1b[12", filterAll(t, "\x1b[12\x1b[?1049h"))
}

func TestAltScreenFilter_FlushesOversizeSequences(t *testing.T) {
	// A pathological never-terminating "sequence" must not buffer forever.
	long := "\x1b[?" + string(bytes.Repeat([]byte("1;"), 64))
	out := filterAll(t, long)
	require.NotEmpty(t, out, "oversize partial sequences must flush through")
}
