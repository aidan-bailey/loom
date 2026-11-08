package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/config"
)

// policyHost is a host with cores cores and ramBytes bytes of RAM.
func policyHost(cores int, ramBytes uint64) Host {
	return Host{Name: "test", Cores: cores, RAMBytes: ramBytes}
}

// policyFresco is the owner's host: 16 cores and 30.2 GB of RAM (a GB is
// 2^30 bytes).
var policyFresco = policyHost(16, 30<<30+(1<<30)/5)

// policyWant is the policy a host's defaults give: 2 I/O slots, a starvation
// wait of 10 minutes, 50 finished jobs kept, holds that may fall, and the
// caps and default claim given.
func policyWant(cpus, memoryGB, claimCPUs, claimMemoryGB int) Policy {
	return Policy{
		Caps:             Caps{CPUs: cpus, MemoryGB: memoryGB, IOJobs: 2},
		DefaultClaim:     Claim{CPUs: claimCPUs, MemoryGB: claimMemoryGB},
		StarveAfter:      10 * time.Minute,
		KeepFinished:     50,
		AgentJobs:        4,
		ReleasePastPeaks: true,
		Contain:          true,
		JobsCPUWeight:    50,
		ReserveGB:        4,
		CPUPressurePct:   25,
		CPUOvercommit:    2,
		Explore:          true,
	}
}

func TestResolve_Defaults(t *testing.T) {
	tests := []struct {
		name string
		host Host
		want Policy
	}{
		{"fresco: 14 cores, 22 GB, three default jobs at once", policyFresco, policyWant(14, 22, 4, 6)},
		{"a laptop", policyHost(8, 16<<30), policyWant(6, 12, 4, 6)},
		{"two cores and 1 GB: at least 1 of each, and a claim within them", policyHost(2, 1<<30), policyWant(1, 1, 1, 1)},
		{"one core", policyHost(1, 8<<30), policyWant(1, 6, 1, 6)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(config.SchedulerSettings{}, tc.host)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolve_Explicit(t *testing.T) {
	tests := []struct {
		name string
		in   config.SchedulerSettings
		want Policy
	}{
		{
			name: "the spec's example block",
			in: config.SchedulerSettings{
				CPUs: 14, MemoryGB: 24, IOJobs: 2,
				DefaultClaim:       &config.ClaimSettings{CPUs: 4, MemoryGB: 6},
				StarveAfterMinutes: 10, KeepFinished: 50,
			},
			want: policyWant(14, 24, 4, 6),
		},
		{
			name: "every field set",
			in: config.SchedulerSettings{
				CPUs: 4, MemoryGB: 8, IOJobs: 1,
				DefaultClaim:       &config.ClaimSettings{CPUs: 2, MemoryGB: 3},
				StarveAfterMinutes: 30, KeepFinished: 5, ReleasePastPeaks: &policyOff,
				Contain: &policyOff, JobsCPUWeight: 30,
				InteractiveReserveGB: 2, CPUPressurePct: 40, CPUOvercommit: 3,
				Explore: &policyOff,
			},
			want: Policy{
				Caps:           Caps{CPUs: 4, MemoryGB: 8, IOJobs: 1},
				DefaultClaim:   Claim{CPUs: 2, MemoryGB: 3},
				StarveAfter:    30 * time.Minute,
				KeepFinished:   5,
				JobsCPUWeight:  30,
				ReserveGB:      2,
				CPUPressurePct: 40,
				CPUOvercommit:  3,
				AgentJobs:      4,
			},
		},
		{
			name: "the largest values",
			in:   config.SchedulerSettings{CPUs: 1024, MemoryGB: 65536, IOJobs: 64, StarveAfterMinutes: 1440, KeepFinished: 10000, JobsCPUWeight: 10000, InteractiveReserveGB: 1024, CPUPressurePct: 100, CPUOvercommit: 8, AgentJobs: 16},
			want: Policy{
				Caps:             Caps{CPUs: 1024, MemoryGB: 65536, IOJobs: 64},
				DefaultClaim:     Claim{CPUs: 4, MemoryGB: 6},
				StarveAfter:      1440 * time.Minute,
				KeepFinished:     10000,
				AgentJobs:        16,
				ReleasePastPeaks: true,
				Contain:          true,
				JobsCPUWeight:    10000,
				ReserveGB:        1024,
				CPUPressurePct:   100,
				CPUOvercommit:    8,
				Explore:          true,
			},
		},
		{
			name: "the smallest values",
			in:   config.SchedulerSettings{CPUs: 1, MemoryGB: 1, IOJobs: 1, StarveAfterMinutes: 1, KeepFinished: 1, JobsCPUWeight: 1, InteractiveReserveGB: 1, CPUPressurePct: 1, CPUOvercommit: 1, AgentJobs: 1},
			want: Policy{
				Caps:             Caps{CPUs: 1, MemoryGB: 1, IOJobs: 1},
				DefaultClaim:     Claim{CPUs: 1, MemoryGB: 1},
				StarveAfter:      time.Minute,
				KeepFinished:     1,
				AgentJobs:        1,
				ReleasePastPeaks: true,
				Contain:          true,
				JobsCPUWeight:    1,
				ReserveGB:        1,
				CPUPressurePct:   1,
				CPUOvercommit:    1,
				Explore:          true,
			},
		},
		{
			name: "half a default claim: the other half takes its default",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: 8}},
			want: policyWant(14, 22, 8, 6),
		},
		{
			name: "a default claim that is all zeros is the default claim",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{}},
			want: policyWant(14, 22, 4, 6),
		},
		{
			name: "the default claim's default follows a small cap",
			in:   config.SchedulerSettings{CPUs: 2, MemoryGB: 3},
			want: policyWant(2, 3, 2, 3),
		},
		{
			name: "a cap set above 75% of the RAM is the owner's call",
			in:   config.SchedulerSettings{MemoryGB: 30},
			want: policyWant(14, 30, 4, 6),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.in, policyFresco)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolve_Bounds(t *testing.T) {
	tests := []struct {
		name string
		host Host // the zero Host is policyFresco
		in   config.SchedulerSettings
		want string
	}{
		{name: "cpus below 1", in: config.SchedulerSettings{CPUs: -1}, want: "scheduler.cpus is -1: it must be 1–1024"},
		{name: "cpus above 1024", in: config.SchedulerSettings{CPUs: 1025}, want: "scheduler.cpus is 1025: it must be 1–1024"},
		{name: "memory_gb below 1", in: config.SchedulerSettings{MemoryGB: -1}, want: "scheduler.memory_gb is -1: it must be 1–65536"},
		{name: "memory_gb above 65536", in: config.SchedulerSettings{MemoryGB: 65537}, want: "scheduler.memory_gb is 65537: it must be 1–65536"},
		{name: "io_jobs below 1", in: config.SchedulerSettings{IOJobs: -2}, want: "scheduler.io_jobs is -2: it must be 1–64"},
		{name: "io_jobs above 64", in: config.SchedulerSettings{IOJobs: 65}, want: "scheduler.io_jobs is 65: it must be 1–64"},
		{name: "starve_after_minutes below 1", in: config.SchedulerSettings{StarveAfterMinutes: -1}, want: "scheduler.starve_after_minutes is -1: it must be 1–1440"},
		{name: "starve_after_minutes above 1440", in: config.SchedulerSettings{StarveAfterMinutes: 1441}, want: "scheduler.starve_after_minutes is 1441: it must be 1–1440"},
		{name: "keep_finished below 1", in: config.SchedulerSettings{KeepFinished: -5}, want: "scheduler.keep_finished is -5: it must be 1–10000"},
		{name: "keep_finished above 10000", in: config.SchedulerSettings{KeepFinished: 10001}, want: "scheduler.keep_finished is 10001: it must be 1–10000"},
		{name: "jobs_cpu_weight below 1", in: config.SchedulerSettings{JobsCPUWeight: -1}, want: "scheduler.jobs_cpu_weight is -1: it must be 1–10000"},
		{name: "jobs_cpu_weight above 10000", in: config.SchedulerSettings{JobsCPUWeight: 10001}, want: "scheduler.jobs_cpu_weight is 10001: it must be 1–10000"},
		{name: "interactive_reserve_gb below 1", in: config.SchedulerSettings{InteractiveReserveGB: -1}, want: "scheduler.interactive_reserve_gb is -1: it must be 1–1024"},
		{name: "interactive_reserve_gb above 1024", in: config.SchedulerSettings{InteractiveReserveGB: 1025}, want: "scheduler.interactive_reserve_gb is 1025: it must be 1–1024"},
		{name: "cpu_pressure_pct below 1", in: config.SchedulerSettings{CPUPressurePct: -1}, want: "scheduler.cpu_pressure_pct is -1: it must be 1–100"},
		{name: "cpu_pressure_pct above 100", in: config.SchedulerSettings{CPUPressurePct: 101}, want: "scheduler.cpu_pressure_pct is 101: it must be 1–100"},
		{name: "cpu_overcommit below 1", in: config.SchedulerSettings{CPUOvercommit: -1}, want: "scheduler.cpu_overcommit is -1: it must be 1–8"},
		{name: "cpu_overcommit above 8", in: config.SchedulerSettings{CPUOvercommit: 9}, want: "scheduler.cpu_overcommit is 9: it must be 1–8"},
		{name: "agent_jobs above 16", in: config.SchedulerSettings{AgentJobs: 17}, want: "scheduler.agent_jobs is 17: it must be 1–16"},
		{name: "agent_jobs below 0", in: config.SchedulerSettings{AgentJobs: -1}, want: "scheduler.agent_jobs is -1: it must be 1–16"},
		{
			name: "a default claim above the default cap",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: 15}},
			want: "scheduler.default_claim.cpus is 15: it must be 1–14",
		},
		{
			name: "a default claim above the cap set",
			in:   config.SchedulerSettings{CPUs: 4, DefaultClaim: &config.ClaimSettings{CPUs: 5}},
			want: "scheduler.default_claim.cpus is 5: it must be 1–4",
		},
		{
			name: "a negative default claim",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: -2}},
			want: "scheduler.default_claim.cpus is -2: it must be 1–14",
		},
		{
			name: "a default claim's memory above the cap",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{MemoryGB: 23}},
			want: "scheduler.default_claim.memory_gb is 23: it must be 1–22",
		},
		{
			name: "a negative default claim memory",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{MemoryGB: -1}},
			want: "scheduler.default_claim.memory_gb is -1: it must be 1–22",
		},
		{
			name: "the first fault is the one reported",
			in:   config.SchedulerSettings{CPUs: -1, MemoryGB: -1},
			want: "scheduler.cpus is -1: it must be 1–1024",
		},
		{
			name: "a host whose default is out of bounds is invalid too: no cap is guessed",
			host: policyHost(2000, 64<<30),
			in:   config.SchedulerSettings{},
			want: "scheduler.cpus is 1998: it must be 1–1024",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host
			if host == (Host{}) {
				host = policyFresco
			}
			got, err := Resolve(tc.in, host)
			assert.EqualError(t, err, tc.want)
			assert.Equal(t, Policy{}, got, "an invalid block yields no policy")
		})
	}
}

func TestResolve_RAMUnreadable(t *testing.T) {
	host := policyHost(8, 0)

	got, err := Resolve(config.SchedulerSettings{}, host)
	assert.EqualError(t, err, "this host's RAM could not be read: set scheduler.memory_gb")
	assert.Equal(t, Policy{}, got)

	got, err = Resolve(config.SchedulerSettings{MemoryGB: 8}, host)
	require.NoError(t, err, "memory_gb set: the RAM is not needed")
	assert.Equal(t, policyWant(6, 8, 4, 6), got)
}

// TestResolve_LeavesTheSettingsAlone: Resolve reads the block it is given and
// changes nothing in it, the default claim included.
func TestResolve_LeavesTheSettingsAlone(t *testing.T) {
	s := config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{}}
	_, err := Resolve(s, policyFresco)
	require.NoError(t, err)
	assert.Equal(t, config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{}}, s)
}

func TestResolve_ReleasePastPeaks(t *testing.T) {
	h := Host{Cores: 16, RAMBytes: 32 << 30}
	p, err := Resolve(config.SchedulerSettings{}, h)
	require.NoError(t, err)
	assert.True(t, p.ReleasePastPeaks, "on when absent")
	off := false
	p, err = Resolve(config.SchedulerSettings{ReleasePastPeaks: &off}, h)
	require.NoError(t, err)
	assert.False(t, p.ReleasePastPeaks)
}

func TestResolve_Contain(t *testing.T) {
	h := Host{Cores: 16, RAMBytes: 32 << 30}
	p, err := Resolve(config.SchedulerSettings{}, h)
	require.NoError(t, err)
	assert.True(t, p.Contain, "on when absent")
	p, err = Resolve(config.SchedulerSettings{Contain: &policyOff}, h)
	require.NoError(t, err)
	assert.False(t, p.Contain)
}

// policyOff is a release_past_peaks (or contain) of false.
var policyOff = false

func TestResolve_Borrow(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{{"absent is off", nil, false}, {"false", &off, false}, {"true", &on, true}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(config.SchedulerSettings{Borrow: tc.in}, policyFresco)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Borrow)
		})
	}
}

func TestResolve_Explore(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{{"absent is on", nil, true}, {"false", &off, false}, {"true", &on, true}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(config.SchedulerSettings{Explore: tc.in}, policyFresco)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Explore)
		})
	}
}
