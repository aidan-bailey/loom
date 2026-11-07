package sched

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadLogEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")

	data, partial, err := ReadLogEnd(path, 100)
	require.NoError(t, err, "a job with no log yet")
	assert.Empty(t, data)
	assert.False(t, partial)

	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o600))
	data, partial, err = ReadLogEnd(path, 100)
	require.NoError(t, err)
	assert.Equal(t, "0123456789", string(data))
	assert.False(t, partial)

	data, partial, err = ReadLogEnd(path, 4)
	require.NoError(t, err)
	assert.Equal(t, "6789", string(data), "the end of the log")
	assert.True(t, partial, "the head was cut")

	_, _, err = ReadLogEnd(filepath.Join(path, "below"), 4)
	assert.Error(t, err, "a path that cannot be a log is an error, not an empty log")
}

func writeLog(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), LogFile)
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	return path
}

func TestReadLog_AMissingFileIsAnEmptyRead(t *testing.T) {
	r, err := ReadLog(filepath.Join(t.TempDir(), LogFile), -1, 100)
	require.NoError(t, err)
	assert.Equal(t, LogRead{}, r)
}

func TestReadLog_ASmallFilesTailIsTheWholeFile(t *testing.T) {
	r, err := ReadLog(writeLog(t, "one\ntwo\n"), -1, 100)
	require.NoError(t, err)
	assert.Equal(t, LogRead{Data: []byte("one\ntwo\n"), Start: 0, End: 8, Size: 8}, r)
}

func TestReadLog_ALongFilesTailStartsOnALine(t *testing.T) {
	// 10 lines of 4 bytes; the last 10 bytes start mid-line ("7\nl8\nl9\n"
	// would be cut), so the read moves past the first newline.
	text := ""
	for i := range 10 {
		text += "l" + string(rune('0'+i)) + "\n\n"
	}
	r, err := ReadLog(writeLog(t, text), -1, 10)
	require.NoError(t, err)
	assert.True(t, r.Cut)
	assert.Equal(t, int64(len(text)), r.Size)
	assert.Equal(t, r.Size, r.End)
	assert.Equal(t, text[r.Start:], string(r.Data))
	assert.Equal(t, byte('\n'), text[r.Start-1], "the read starts right after a newline")
	assert.LessOrEqual(t, len(r.Data), 10)
}

func TestReadLog_ATailWithNoNewlineKeepsItsBytes(t *testing.T) {
	text := strings.Repeat("x", 50)
	r, err := ReadLog(writeLog(t, text), -1, 10)
	require.NoError(t, err)
	assert.True(t, r.Cut)
	assert.Equal(t, LogRead{Data: []byte(text[40:]), Start: 40, End: 50, Size: 50, Cut: true}, r)
}

func TestReadLog_FromAnOffsetReadsWhatFollows(t *testing.T) {
	path := writeLog(t, "one\ntwo\nthree\n")

	r, err := ReadLog(path, 4, 100)
	require.NoError(t, err)
	assert.Equal(t, LogRead{Data: []byte("two\nthree\n"), Start: 4, End: 14, Size: 14}, r)

	r, err = ReadLog(path, 14, 100)
	require.NoError(t, err)
	assert.Equal(t, LogRead{Data: []byte{}, Start: 14, End: 14, Size: 14}, r, "at the end there is nothing new")
}

func TestReadLog_AnOffsetReadStopsAtTheLimitAndContinuesFromItsEnd(t *testing.T) {
	path := writeLog(t, "0123456789")

	r, err := ReadLog(path, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, "234", string(r.Data))
	assert.Equal(t, int64(5), r.End)

	r, err = ReadLog(path, r.End, 3)
	require.NoError(t, err)
	assert.Equal(t, "567", string(r.Data))
}

func TestReadLog_AnOffsetPastTheEndIsAFreshTail(t *testing.T) {
	r, err := ReadLog(writeLog(t, "new\n"), 400, 100)
	require.NoError(t, err)
	assert.Equal(t, LogRead{Data: []byte("new\n"), Start: 0, End: 4, Size: 4, Reset: true}, r)
}
