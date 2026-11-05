package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentJob is an agent job (flow in loom part 3a) of account in state.
func agentJob(id JobID, account string, state State, waited time.Duration) Record {
	r := Record{ID: id, Order: int64(id), State: state, Kind: KindAgent, AccountKey: account, Submitted: admitNow.Add(-waited)}
	if state == Running {
		r.Started = admitNow.Add(-time.Minute)
	}
	return r
}

func TestAdmit_AgentJobs(t *testing.T) {
	agents := func(p *Policy) { p.AgentJobs = 4 }
	runAdmitCases(t, []admitCase{
		{
			name: "an agent starts on a host whose caps are full, and a build waits",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 14, MemoryGB: 22}),
				agentJob(2, "/acc/a", Queued, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			edit:    agents,
			start:   []JobID{2},
			waiting: map[JobID]string{3: "needs 2 cores (0 free)"},
		},
		{
			name: "running agents take nothing from the caps",
			jobs: []Record{
				agentJob(1, "/acc/a", Running, time.Hour),
				agentJob(2, "/acc/a", Running, time.Hour),
				queuedJob(3, Claim{CPUs: 14, MemoryGB: 22}, time.Minute),
			},
			edit:  agents,
			start: []JobID{3},
		},
		{
			name: "an account's limit holds its next agent back, and another account's starts",
			jobs: []Record{
				agentJob(1, "/acc/a", Running, time.Hour),
				agentJob(2, "/acc/a", Running, time.Hour),
				agentJob(3, "/acc/a", Running, time.Hour),
				agentJob(4, "/acc/a", Running, time.Hour),
				agentJob(5, "/acc/a", Queued, time.Minute),
				agentJob(6, "/acc/b", Queued, time.Minute),
			},
			edit:    agents,
			start:   []JobID{6},
			waiting: map[JobID]string{5: "waits for an agent slot (4 of 4 running on this account)"},
		},
		{
			name: "agents started in one pass count toward the limit",
			jobs: []Record{
				agentJob(1, "", Queued, time.Minute),
				agentJob(2, "", Queued, time.Minute),
				agentJob(3, "", Queued, time.Minute),
			},
			edit:    func(p *Policy) { p.AgentJobs = 2 },
			start:   []JobID{1, 2},
			waiting: map[JobID]string{3: "waits for an agent slot (2 of 2 running on this account)"},
		},
		{
			name: "a starving agent is never the head",
			jobs: []Record{
				agentJob(1, "/acc/a", Queued, time.Hour),
				queuedJob(2, admitDefault, time.Minute),
				queuedJob(3, admitDefault, time.Minute),
			},
			edit:  agents,
			start: []JobID{1, 2, 3},
		},
		{
			name: "with no usable policy an agent neither starts nor gets a reason",
			jobs: []Record{agentJob(1, "/acc/a", Queued, time.Minute)},
			edit: func(p *Policy) { *p = Policy{AgentJobs: 4} },
		},
	})
}

func TestGrant_PassesAgentJobsOver(t *testing.T) {
	p := szPolicy
	p.StarveAfter, p.AgentJobs = 10*time.Minute, 1
	wide := belief(at(4, 3.9))
	now := admitNow
	build := Record{ID: 1, Order: 1, State: Queued, Submitted: now, Claim: Claim{CPUs: 4, MemoryGB: 2},
		Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	jobs := []Record{
		build,
		{ID: 2, Order: 2, State: Running, Kind: KindAgent, AccountKey: "a", Started: now},
		{ID: 3, Order: 3, State: Queued, Kind: KindAgent, AccountKey: "a", Submitted: now},
		{ID: 4, Order: 4, State: Queued, Kind: KindAgent, AccountKey: "b", Submitted: now},
	}
	dec := Admit(jobs, p, now, nil)
	require.Equal(t, "waits for an agent slot (1 of 1 running on this account)", dec.Waiting[3])
	require.Contains(t, dec.Start, JobID(4))
	got := Grant(dec, jobs, p, nil, func(r Record) *Belief {
		if r.Kind == KindAgent {
			return nil
		}
		return wide
	})
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, got, "an agent waiting for a slot stops no grant, and a starting one gets none")
}

func TestAgentHints_OnlyTheJobAndSccache(t *testing.T) {
	got := AgentHints([]string{"A=1", "LOOM_JOB=9", "B=2"}, 7)
	assert.Equal(t, []string{"A=1", "B=2", "SCCACHE_CLIENT_SIDE=1", "LOOM_JOB=7"}, got)
	for _, e := range got {
		assert.False(t, strings.HasPrefix(e, "CARGO_BUILD_JOBS="), "no parallelism hint")
	}
	got = AgentHints([]string{"SCCACHE_CLIENT_SIDE=0"}, 8)
	assert.Equal(t, []string{"SCCACHE_CLIENT_SIDE=0", "LOOM_JOB=8"}, got, "the submitter's own setting stays")
}

func TestResolve_AgentJobs(t *testing.T) {
	h := Host{Cores: 16, RAMBytes: 32 << 30}
	p, err := Resolve(config.SchedulerSettings{}, h)
	require.NoError(t, err)
	assert.Equal(t, 4, p.AgentJobs, "the default")
	for _, n := range []int{1, 16} {
		p, err := Resolve(config.SchedulerSettings{AgentJobs: n}, h)
		require.NoError(t, err)
		assert.Equal(t, n, p.AgentJobs)
	}
	for _, n := range []int{17, -1} {
		_, err := Resolve(config.SchedulerSettings{AgentJobs: n}, h)
		assert.ErrorContains(t, err, "scheduler.agent_jobs is ")
		assert.ErrorContains(t, err, "it must be 1–16")
	}
}

func TestStore_RoundTripsAnAgentJob(t *testing.T) {
	dir := t.TempDir()
	rec := agentJob(3, "/acc/a", Queued, time.Minute)
	require.NoError(t, SaveStore(dir, Store{NextID: 4, Jobs: []Record{rec}}))
	got, err := LoadStore(dir)
	require.NoError(t, err)
	require.Len(t, got.Jobs, 1)
	assert.Equal(t, KindAgent, got.Jobs[0].Kind)
	assert.Equal(t, "/acc/a", got.Jobs[0].AccountKey)
}
