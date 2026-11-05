package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIOCounters(t *testing.T) {
	data := []byte("rchar: 9\nwchar: 8\nsyscr: 1\nsyscw: 1\nread_bytes: 4096\nwrite_bytes: 10000\ncancelled_write_bytes: 1000\n")
	c, ok := parseIOCounters(data)
	assert.True(t, ok)
	assert.Equal(t, ioCounters{read: 4096, write: 10000, cancelled: 1000}, c)
	_, ok = parseIOCounters([]byte("read_bytes: 1\n"))
	assert.False(t, ok, "every counter or none")

	read, written := ioDelta(ioCounters{read: 100, write: 50, cancelled: 10}, ioCounters{read: 4196, write: 10050, cancelled: 1010})
	assert.Equal(t, int64(4096), *read)
	assert.Equal(t, int64(9000), *written, "net of cancelled writes")
	_, written = ioDelta(ioCounters{write: 500}, ioCounters{write: 100})
	assert.Equal(t, int64(0), *written, "never below 0")
}
