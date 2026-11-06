package core

import (
	"strconv"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobQueue_ARunJobIsNeverSized: a flow run engine's job (flow in loom
// part 3b) keeps no claim, signature or sizing, asks the advisor nothing,
// starts at once and runs uncontained, and its view shows its kind.
func TestJobQueue_ARunJobIsNeverSized(t *testing.T) {
	m := wqModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	sub.Kind = sched.KindRun

	id, _, err := m.submitJob(sub)

	require.NoError(t, err)
	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.KindRun, rec.Kind)
	assert.Equal(t, sched.Claim{}, rec.Claim, "no claim, not even the default")
	assert.Empty(t, rec.Signature)
	assert.Empty(t, rec.Sizing)
	assert.Equal(t, "a run job is never sized", rec.Unsized)
	assert.Empty(t, rec.AccountKey, "an account is an agent job's")
	assert.Empty(t, m.queue.advisor.pending, "the advisor is asked nothing")
	assert.Equal(t, sched.Running, rec.State, "started at once")
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, sched.KindRun, spec.Kind, "the shim gives it AgentHints")
	assert.Equal(t, sched.KindRun, m.queueJobView(m.queue, rec, nil).Kind)
}

// A job submitted under a run (its environment's LOOM_FLOW_RUN names a run
// job) records the run; any other value is ignored (part 3b, planning
// amendment 1).
func TestJobQueue_AJobOfARunRecordsItsRun(t *testing.T) {
	m := wqModel(t)
	sub := wqJob(t, "sleep", "30")
	sub.Kind = sched.KindRun
	run, _, err := m.submitJob(sub)
	require.NoError(t, err)
	build := wqSubmit(t, m, "sh", "-c", "exit 0")

	for _, c := range []struct {
		name, value string
		want        sched.JobID
	}{
		{"a run job", strconv.FormatInt(int64(run), 10), run},
		{"a build job", strconv.FormatInt(int64(build), 10), 0},
		{"a missing job", "999", 0},
		{"not a number", "x", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			sub := wqJob(t, "sh", "-c", "exit 0")
			sub.Env = append(sub.Env, "LOOM_FLOW_RUN="+c.value)
			id, _, err := m.submitJob(sub)
			require.NoError(t, err)
			assert.Equal(t, c.want, wqRecord(t, m, id).Run)
		})
	}
}

// A pause spares a flow run's jobs, which go on while their manager is
// paused, and a kill (which removes the worktree) cancels them (part 3b,
// planning amendment 1).
func TestJobQueue_APauseSparesARunsJobsAndAKillDoesNot(t *testing.T) {
	cases := []struct {
		name  string
		act   func(*Model, InstanceID)
		spare bool
	}{
		{"pause", func(m *Model, id InstanceID) { m.Pause(id, 0) }, true},
		{"kill", func(m *Model, id InstanceID) { m.Kill(id, 0) }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqLiveModel(t)
			owner := wqOwnerSession(t, m)
			submit := func(kind string, env ...string) sched.JobID {
				sub := wqJob(t, "sleep", "30")
				sub.Launch, sub.Kind = "L1", kind
				sub.Env = append(sub.Env, env...)
				id, _, err := m.submitJob(sub)
				require.NoError(t, err)
				return id
			}
			run := submit(sched.KindRun)
			ofRun := submit("", "LOOM_FLOW_RUN="+strconv.FormatInt(int64(run), 10))
			plain := submit("")
			require.Equal(t, run, wqRecord(t, m, ofRun).Run)
			wqPump(t, m) // the running jobs' starts: real shims

			c.act(m, owner)
			m.Drain()
			m.gate(gateQueue).inFlight = false

			assert.NotEmpty(t, wqRecord(t, m, plain).CancelReason, "a plain job of the owner is cancelled")
			for _, id := range []sched.JobID{run, ofRun} {
				if c.spare {
					assert.Empty(t, wqRecord(t, m, id).CancelReason, "job %d of the run is spared", id)
				} else {
					assert.NotEmpty(t, wqRecord(t, m, id).CancelReason, "job %d of the run is cancelled", id)
				}
			}
		})
	}
}
