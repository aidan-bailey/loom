package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runJob is a flow run engine's job (flow in loom part 3b) in state.
func runJob(id JobID, state State, waited time.Duration) Record {
	r := Record{ID: id, Order: int64(id), State: state, Kind: KindRun, Submitted: admitNow.Add(-waited)}
	if state == Running {
		r.Started = admitNow.Add(-time.Minute)
	}
	return r
}

func TestAdmit_RunJobs(t *testing.T) {
	agents := func(p *Policy) { p.AgentJobs = 4 }
	runAdmitCases(t, []admitCase{
		{
			name: "a run job starts on a full host with its account's agent slots full, and a build waits",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 14, MemoryGB: 22}),
				agentJob(2, "", Running, time.Hour),
				agentJob(3, "", Running, time.Hour),
				agentJob(4, "", Running, time.Hour),
				agentJob(5, "", Running, time.Hour),
				runJob(6, Queued, time.Minute),
				queuedJob(7, admitSmall, time.Minute),
			},
			edit:    agents,
			start:   []JobID{6},
			waiting: map[JobID]string{7: "needs 2 cores (0 free)"},
		},
		{
			name: "running run jobs take nothing from the caps",
			jobs: []Record{
				runJob(1, Running, time.Hour),
				queuedJob(2, Claim{CPUs: 14, MemoryGB: 22}, time.Minute),
			},
			edit:  agents,
			start: []JobID{2},
		},
		{
			name: "a starving run job is never the head",
			jobs: []Record{
				runJob(1, Queued, time.Hour),
				queuedJob(2, admitDefault, time.Minute),
				queuedJob(3, admitDefault, time.Minute),
			},
			edit:  agents,
			start: []JobID{1, 2, 3},
		},
		{
			name: "with no usable policy a run job neither starts nor gets a reason",
			jobs: []Record{runJob(1, Queued, time.Minute)},
			edit: func(p *Policy) { *p = Policy{AgentJobs: 4} },
		},
	})
}

func TestGrant_PassesRunJobsOver(t *testing.T) {
	p := szPolicy
	p.StarveAfter, p.AgentJobs = 10*time.Minute, 1
	wide := belief(at(4, 3.9))
	now := admitNow
	build := Record{ID: 1, Order: 1, State: Queued, Submitted: now, Claim: Claim{CPUs: 4, MemoryGB: 2},
		Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	jobs := []Record{build, runJob(2, Running, time.Hour), runJob(3, Queued, time.Minute)}
	dec := Admit(jobs, p, now, nil, nil)
	require.Contains(t, dec.Start, JobID(3))
	got := Grant(dec, jobs, p, nil, func(r Record) *Belief {
		if r.Kind == KindRun {
			return nil
		}
		return wide
	}, HostIdle{}).Spare
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, got, "a starting run job gets no grant and stops none")
}

func TestStore_RoundTripsARunJob(t *testing.T) {
	dir := t.TempDir()
	rec := runJob(3, Queued, time.Minute)
	build := queuedJob(4, admitSmall, time.Minute)
	build.Run = 3
	require.NoError(t, SaveStore(dir, Store{NextID: 5, Jobs: []Record{rec, build}}))
	got, err := LoadStore(dir)
	require.NoError(t, err)
	require.Len(t, got.Jobs, 2)
	assert.Equal(t, KindRun, got.Jobs[0].Kind)
	assert.Equal(t, JobID(3), got.Jobs[1].Run)
}

func TestUnclaimed(t *testing.T) {
	assert.True(t, Unclaimed(KindAgent))
	assert.True(t, Unclaimed(KindRun))
	assert.False(t, Unclaimed(""))
}
