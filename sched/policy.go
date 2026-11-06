package sched

import (
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// resolveGB is a GB: 2^30 bytes throughout.
const resolveGB = 1 << 30

// The bounds of a scheduler block, once a 0 has taken its default (spec §5.1).
const (
	maxCapCPUs    = 1024
	maxCapMemory  = 65536
	maxIOJobs     = 64
	maxStarveMins = 1440
	maxKeep       = 10000
	maxCPUWeight  = 10000
	maxReserveGB  = 1024
	maxOvercommit = 8
	maxAgentJobs  = 16
)

// resolveBounded returns v, or def when v is 0, and an error naming the
// scheduler key when the result is outside lo–hi.
func resolveBounded(key string, v, def, lo, hi int) (int, error) {
	if v == 0 {
		v = def
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("scheduler.%s is %d: it must be %d–%d", key, v, lo, hi)
	}
	return v, nil
}

// Resolve applies the host's defaults to the scheduler block and checks its
// bounds. A 0 or absent field takes its default: cpus is max(1, cores − 2);
// memory_gb is max(1, ⌊0.75 × RAM in GB⌋); io_jobs is 2; the default claim is
// {min(4, cpus), min(6, memory_gb)}, each half taking its own default;
// starve_after_minutes is 10; keep_finished is 50; agent_jobs is 4;
// release_past_peaks is true; contain is true; jobs_cpu_weight is 50;
// interactive_reserve_gb is 4; cpu_pressure_pct is 25; cpu_overcommit is
// 2. It fails closed: a value outside its bounds (a negative one
// included), a default claim above a cap, or an unreadable RAM with
// memory_gb unset is an error and no Policy, since no cap is guessed. The
// first fault, in the order cpus, memory_gb, io_jobs, starve_after_minutes,
// keep_finished, agent_jobs, default_claim.cpus, default_claim.memory_gb,
// jobs_cpu_weight, interactive_reserve_gb, cpu_pressure_pct,
// cpu_overcommit, is the one returned.
func Resolve(s config.SchedulerSettings, h Host) (Policy, error) {
	cpus, err := resolveBounded("cpus", s.CPUs, max(1, h.Cores-2), 1, maxCapCPUs)
	if err != nil {
		return Policy{}, err
	}
	memDefault := 0
	if s.MemoryGB == 0 {
		if h.RAMBytes == 0 {
			return Policy{}, errors.New("this host's RAM could not be read: set scheduler.memory_gb")
		}
		memDefault = max(1, int(h.RAMBytes*3/4/resolveGB))
	}
	mem, err := resolveBounded("memory_gb", s.MemoryGB, memDefault, 1, maxCapMemory)
	if err != nil {
		return Policy{}, err
	}
	ioJobs, err := resolveBounded("io_jobs", s.IOJobs, 2, 1, maxIOJobs)
	if err != nil {
		return Policy{}, err
	}
	starve, err := resolveBounded("starve_after_minutes", s.StarveAfterMinutes, 10, 1, maxStarveMins)
	if err != nil {
		return Policy{}, err
	}
	keep, err := resolveBounded("keep_finished", s.KeepFinished, 50, 1, maxKeep)
	if err != nil {
		return Policy{}, err
	}
	agentJobs, err := resolveBounded("agent_jobs", s.AgentJobs, 4, 1, maxAgentJobs)
	if err != nil {
		return Policy{}, err
	}
	var claim config.ClaimSettings
	if s.DefaultClaim != nil {
		claim = *s.DefaultClaim
	}
	claimCPUs, err := resolveBounded("default_claim.cpus", claim.CPUs, min(4, cpus), 1, cpus)
	if err != nil {
		return Policy{}, err
	}
	claimMem, err := resolveBounded("default_claim.memory_gb", claim.MemoryGB, min(6, mem), 1, mem)
	if err != nil {
		return Policy{}, err
	}
	weight, err := resolveBounded("jobs_cpu_weight", s.JobsCPUWeight, 50, 1, maxCPUWeight)
	if err != nil {
		return Policy{}, err
	}
	reserve, err := resolveBounded("interactive_reserve_gb", s.InteractiveReserveGB, 4, 1, maxReserveGB)
	if err != nil {
		return Policy{}, err
	}
	pressure, err := resolveBounded("cpu_pressure_pct", s.CPUPressurePct, 25, 1, 100)
	if err != nil {
		return Policy{}, err
	}
	overcommit, err := resolveBounded("cpu_overcommit", s.CPUOvercommit, 2, 1, maxOvercommit)
	if err != nil {
		return Policy{}, err
	}
	return Policy{
		Caps:             Caps{CPUs: cpus, MemoryGB: mem, IOJobs: ioJobs},
		DefaultClaim:     Claim{CPUs: claimCPUs, MemoryGB: claimMem},
		StarveAfter:      time.Duration(starve) * time.Minute,
		KeepFinished:     keep,
		AgentJobs:        agentJobs,
		ReleasePastPeaks: s.ReleasePastPeaks == nil || *s.ReleasePastPeaks,
		Contain:          s.Contain == nil || *s.Contain,
		JobsCPUWeight:    weight,
		ReserveGB:        reserve,
		CPUPressurePct:   pressure,
		CPUOvercommit:    overcommit,
	}, nil
}
