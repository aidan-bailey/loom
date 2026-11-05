package sched

import (
	"strconv"
	"strings"
)

// ioCounters are the storage counters of /proc/<pid>/io.
type ioCounters struct{ read, write, cancelled int64 }

// parseIOCounters reads read_bytes, write_bytes and cancelled_write_bytes
// out of /proc/<pid>/io's text; false unless all three are there.
func parseIOCounters(data []byte) (ioCounters, bool) {
	var c ioCounters
	seen := 0
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "read_bytes":
			c.read, seen = n, seen|1
		case "write_bytes":
			c.write, seen = n, seen|2
		case "cancelled_write_bytes":
			c.cancelled, seen = n, seen|4
		}
	}
	return c, seen == 7
}

// ioDelta is what was read and written between two readings, writes net of
// cancelled ones, never below 0.
func ioDelta(before, after ioCounters) (read, written *int64) {
	r := max(0, after.read-before.read)
	w := max(0, (after.write-after.cancelled)-(before.write-before.cancelled))
	return &r, &w
}
