package sched

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemTotal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want uint64
	}{
		{"a real /proc/meminfo head", "MemTotal:       31701924 kB\nMemFree:         1204448 kB\n", 31701924 * 1024},
		{"MemTotal after other lines", "MemFree: 5 kB\nMemTotal: 2048 kB\nBuffers: 1 kB\n", 2048 * 1024},
		{"another key that starts the same", "MemTotalFoo: 5 kB\nMemTotal: 7 kB\n", 7 * 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMemTotal([]byte(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseMemTotal_RefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"no MemTotal line", "MemFree: 5 kB\n"},
		{"empty", ""},
		{"no unit", "MemTotal: 2048\n"},
		{"another unit", "MemTotal: 2 MB\n"},
		{"not a number", "MemTotal: lots kB\n"},
		{"a negative number", "MemTotal: -5 kB\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseMemTotal([]byte(tc.in))
			assert.Error(t, err)
		})
	}
}

func TestDetectHost(t *testing.T) {
	h := DetectHost()

	assert.NotEmpty(t, h.Name)
	assert.Equal(t, runtime.NumCPU(), h.Cores)
	switch runtime.GOOS {
	case "linux", "darwin":
		assert.Greater(t, h.RAMBytes, uint64(0), "this platform can read its RAM")
	default:
		assert.Zero(t, h.RAMBytes, "no reader here, so the RAM stays unknown")
	}
}
