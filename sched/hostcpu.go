package sched

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// IdleWindow is how far back the host's idle cores are measured
// (exploration spec §4.3).
const IdleWindow = 30 * time.Second

// CPUTimes is /proc/stat's aggregate cpu line: the host's idle and total
// CPU time in clock ticks since boot, and its logical CPUs (its cpuN
// lines).
type CPUTimes struct {
	Idle  uint64 // idle + iowait
	Total uint64 // user, nice, system, idle, iowait, irq, softirq and steal
	CPUs  int
}

// CPUReading is the host's CPU times, taken at At.
type CPUReading struct {
	At    time.Time
	Times CPUTimes
}

// errNoAggregate is a /proc/stat whose first line is not the aggregate
// cpu line with its 8 counted fields.
var errNoAggregate = errors.New("/proc/stat: no aggregate cpu line")

// parseHostStat reads data, a /proc/stat, into the host's CPU times: an
// error when its first line is not the aggregate cpu line with at least 8
// numeric fields, or no cpuN line follows. guest and guest_nice are left
// out of the total, since user and nice already count them.
func parseHostStat(data []byte) (CPUTimes, error) {
	lines := bytes.Split(data, []byte("\n"))
	fields := bytes.Fields(lines[0])
	if len(fields) < 9 || string(fields[0]) != "cpu" {
		return CPUTimes{}, errNoAggregate
	}
	var t CPUTimes
	for i, f := range fields[1:9] {
		v, err := strconv.ParseUint(string(f), 10, 64)
		if err != nil {
			return CPUTimes{}, fmt.Errorf("/proc/stat: cpu field %d: %w", i+1, err)
		}
		t.Total += v
		if i == 3 || i == 4 { // idle, iowait
			t.Idle += v
		}
	}
	for _, l := range lines[1:] {
		if f := bytes.Fields(l); len(f) > 0 && len(f[0]) > 3 && bytes.HasPrefix(f[0], []byte("cpu")) {
			t.CPUs++
		}
	}
	if t.CPUs == 0 {
		return CPUTimes{}, errors.New("/proc/stat: no cpuN line")
	}
	return t, nil
}

// IdleCores is the host's mean idle logical CPUs between the newest of
// readings (oldest first) and the newest reading at least IdleWindow older
// than it (exploration spec §4.3): the idle share of the CPU time counted
// between them, times the logical CPUs. Unknown when no reading is that
// old, the CPU count changed, a counter went backwards, or no time was
// counted. A pure function of its argument.
func IdleCores(readings []CPUReading) HostIdle {
	if len(readings) < 2 {
		return HostIdle{}
	}
	last := readings[len(readings)-1]
	for i := len(readings) - 2; i >= 0; i-- {
		first := readings[i]
		if last.At.Sub(first.At) < IdleWindow {
			continue
		}
		a, b := first.Times, last.Times
		if a.CPUs != b.CPUs || b.Idle < a.Idle || b.Total <= a.Total || b.Idle-a.Idle > b.Total-a.Total {
			return HostIdle{}
		}
		share := float64(b.Idle-a.Idle) / float64(b.Total-a.Total)
		return HostIdle{Cores: share * float64(b.CPUs), Known: true}
	}
	return HostIdle{}
}

// KeepReadings is readings (oldest first) with r added and every reading
// more than 2 × IdleWindow older than r dropped (exploration spec §4.3).
func KeepReadings(readings []CPUReading, r CPUReading) []CPUReading {
	cut := r.At.Add(-2 * IdleWindow)
	i := 0
	for i < len(readings) && readings[i].At.Before(cut) {
		i++
	}
	return append(readings[i:], r)
}
