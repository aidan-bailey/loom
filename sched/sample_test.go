//go:build unix

package sched

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requirePS skips the test where ps is not installed, as in the Nix build
// sandbox, whose check inputs hold no procps.
func requirePS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not installed")
	}
}

func TestParsePS(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Proc
	}{
		{
			name: "procps writes hh:mm:ss",
			in:   "    1     1  10240 00:00:03\n 4242  4242   2048 00:10:00\n",
			want: []Proc{
				{PID: 1, PGID: 1, RSSKB: 10240, CPU: 3 * time.Second},
				{PID: 4242, PGID: 4242, RSSKB: 2048, CPU: 10 * time.Minute},
			},
		},
		{
			name: "procps writes days before an hour",
			in:   "77 77 5 2-03:04:05\n",
			want: []Proc{{PID: 77, PGID: 77, RSSKB: 5, CPU: 48*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second}},
		},
		{
			name: "BSD writes minutes and hundredths",
			in:   "  501   501   9216   0:03.45\n  502   501  12288  12:34.56\n",
			want: []Proc{
				{PID: 501, PGID: 501, RSSKB: 9216, CPU: 3*time.Second + 450*time.Millisecond},
				{PID: 502, PGID: 501, RSSKB: 12288, CPU: 12*time.Minute + 34*time.Second + 560*time.Millisecond},
			},
		},
		{
			name: "BSD writes hours when a process has run that long",
			in:   "503 503 1024 1:02:03.45\n",
			want: []Proc{{PID: 503, PGID: 503, RSSKB: 1024, CPU: time.Hour + 2*time.Minute + 3*time.Second + 450*time.Millisecond}},
		},
		{
			name: "a bare number of seconds",
			in:   "9 9 1 42\n",
			want: []Proc{{PID: 9, PGID: 9, RSSKB: 1, CPU: 42 * time.Second}},
		},
		{
			name: "blank lines and no trailing newline",
			in:   "\n9 9 1 0:00\n\n10 9 2 0:01",
			want: []Proc{
				{PID: 9, PGID: 9, RSSKB: 1},
				{PID: 10, PGID: 9, RSSKB: 2, CPU: time.Second},
			},
		},
		{name: "no output", in: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePS([]byte(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParsePS_RefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"too few fields", "1 1 100\n", "ps line 1"},
		{"too many fields", "1 1 100 0:01 extra\n", "ps line 1"},
		{"a pid that is not a number", "x 1 100 0:01\n", "bad pid"},
		{"a negative group", "1 -1 100 0:01\n", "bad pgid"},
		{"a negative size", "1 1 -5 0:01\n", "bad rss"},
		{"a time with four parts", "1 1 100 1:2:3:4\n", "CPU time"},
		{"a time with letters", "1 1 100 ab:cd\n", "CPU time"},
		{"a time with an empty day count", "1 1 100 -01:00:00\n", "CPU time"},
		{"a time with an empty fraction", "1 1 100 0:01.\n", "CPU time"},
		{"a bad line after a good one", "1 1 100 0:01\n2 2 oops 0:01\n", "ps line 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePS([]byte(tc.in))
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.Nil(t, got, "a line it cannot read spoils the whole sample")
		})
	}
}

func TestSnapshot_Group(t *testing.T) {
	snap := Snapshot{Procs: []Proc{
		{PID: 100, PGID: 100, RSSKB: 1000, CPU: 2 * time.Second},
		{PID: 101, PGID: 100, RSSKB: 500, CPU: time.Second},
		{PID: 102, PGID: 100, RSSKB: 250, CPU: 500 * time.Millisecond},
		{PID: 200, PGID: 200, RSSKB: 9000, CPU: time.Hour},
		{PID: 300, PGID: 250, RSSKB: 40, CPU: time.Second},
		{PID: 450, PGID: 900, RSSKB: 7, CPU: time.Minute},
		{PID: 451, PGID: 450, RSSKB: 3, CPU: 2 * time.Minute},
	}}
	tests := []struct {
		name       string
		pgid       int
		wantRSS    int64
		wantCPU    time.Duration
		wantMember int
		wantLeader bool
	}{
		{"a whole group with its leader", 100, 1750, 3500 * time.Millisecond, 3, true},
		{"another group is left out", 200, 9000, time.Hour, 1, true},
		{"members whose leader is gone", 250, 40, time.Second, 1, false},
		{"a leader's pid taken by a process of another group", 450, 3, 2 * time.Minute, 1, true},
		{"no such group", 999, 0, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rss, cpu, members, leaderAlive := snap.Group(tc.pgid)
			assert.Equal(t, tc.wantRSS, rss)
			assert.Equal(t, tc.wantCPU, cpu)
			assert.Equal(t, tc.wantMember, members)
			assert.Equal(t, tc.wantLeader, leaderAlive)
		})
	}
}

func TestSample_ListsThisProcess(t *testing.T) {
	requirePS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before := time.Now()

	snap, err := Sample(ctx)
	require.NoError(t, err)

	assert.False(t, snap.Taken.Before(before), "a snapshot is stamped when ps returned")
	var self *Proc
	for i := range snap.Procs {
		if snap.Procs[i].PID == os.Getpid() {
			self = &snap.Procs[i]
		}
	}
	require.NotNil(t, self, "ps -A lists this test binary")
	assert.Greater(t, self.RSSKB, int64(0))
	assert.Equal(t, syscall.Getpgrp(), self.PGID)
}

func TestSample_FailsOnADoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snap, err := Sample(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, snap.Procs, "a failed sample carries nothing")
}
