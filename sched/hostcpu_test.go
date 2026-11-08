package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHostStat(t *testing.T) {
	sample := "cpu  1000 50 300 8000 200 10 20 5 7 0\n" +
		"cpu0 500 25 150 4000 100 5 10 2 3 0\n" +
		"cpu1 500 25 150 4000 100 5 10 3 4 0\n" +
		"intr 12345\nctxt 999\n"
	got, err := parseHostStat([]byte(sample))
	require.NoError(t, err)
	assert.Equal(t, CPUTimes{Idle: 8200, Total: 9585, CPUs: 2}, got,
		"idle + iowait; the first 8 fields, guest left out since user already counts it")
	for name, bad := range map[string]string{
		"empty":                       "",
		"no aggregate line":           "cpu0 1 2 3 4 5 6 7 8\n",
		"too few fields":              "cpu 1 2 3 4 5 6 7\ncpu0 1 2 3 4 5 6 7\n",
		"a field that isn't a number": "cpu 1 2 x 4 5 6 7 8\ncpu0 1 2 3 4 5 6 7 8\n",
		"no cpuN line":                "cpu 1 2 3 4 5 6 7 8\nintr 1\n",
	} {
		_, err := parseHostStat([]byte(bad))
		assert.Error(t, err, name)
	}
}

func TestIdleCores(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	reading := func(s int, idle, total uint64) CPUReading {
		return CPUReading{At: t0.Add(time.Duration(s) * time.Second), Times: CPUTimes{Idle: idle, Total: total, CPUs: 16}}
	}
	// 35 s of 16 CPUs at 100 ticks a second is 56,000 ticks: 42,000 idle is 12 idle cores.
	got := IdleCores([]CPUReading{reading(0, 0, 0), reading(5, 1000, 10000), reading(40, 43000, 66000)})
	assert.True(t, got.Known)
	assert.InDelta(t, 12, got.Cores, 1e-9, "from the newest reading at least 30 s older (5 s), not the oldest")

	assert.Equal(t, HostIdle{}, IdleCores(nil), "no readings")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(0, 0, 0)}), "one reading")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(0, 0, 0), reading(20, 100, 1000)}), "none 30 s old")
	changed := reading(40, 43000, 66000)
	changed.Times.CPUs = 8
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(5, 1000, 10000), changed}), "the CPU count changed")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(5, 1000, 10000), reading(40, 900, 66000)}), "idle went backwards")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(5, 1000, 10000), reading(40, 1000, 10000)}), "no time counted")
}

func TestKeepReadings(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	var rs []CPUReading
	for s := 0; s <= 90; s += 3 {
		rs = KeepReadings(rs, CPUReading{At: t0.Add(time.Duration(s) * time.Second)})
	}
	assert.Equal(t, t0.Add(30*time.Second), rs[0].At, "2 × IdleWindow back from the newest")
	assert.Equal(t, t0.Add(90*time.Second), rs[len(rs)-1].At)
	assert.Len(t, rs, 21)
}
