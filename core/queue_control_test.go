package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The job controls through the model (spec 2026-11-16-job-controls §11.2).
// The tests call controlJob for its error: ControlJob with ReqID 0 only logs
// a refusal, and TestControlJob_TheReplyCarriesTheRefusal pins the Reply.

func TestControlJob_StartNowStartsAJobTheFitHoldsBack(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2}`)
	l.rec(t, l.ids[0]).Claim = sched.Claim{CPUs: 2, MemoryGB: 12}
	sub := wqJob(t, "sleep", "31")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 12}
	big, _, err := l.m.submitJob(sub)
	require.NoError(t, err)
	wqPass(t, l.m)
	require.Equal(t, sched.Queued, wqRecord(t, l.m, big).State, "12 + 12 GB of claims pass 14")

	require.NoError(t, l.m.controlJob(JobControl{Job: big, Action: JobStart}))
	rec := wqRecord(t, l.m, big)
	assert.Equal(t, sched.Running, rec.State)
	assert.True(t, rec.Forced)
}

func TestControlJob_Refusals(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	run := l.ids[0]
	l.rec(t, run).Locks = []string{"port:4300"}
	q := wqLocked(t, l.m, "port:4300")
	require.Equal(t, sched.Queued, wqRecord(t, l.m, q).State)
	cases := []struct {
		name string
		c    JobControl
		want string
	}{
		{"no such job", JobControl{Job: 99, Action: JobStart}, "there is no job 99"},
		{"another session's job", JobControl{Job: q, Owner: 12345, Action: JobPark}, "is not this session's"},
		{"start a running job", JobControl{Job: run, Action: JobStart}, "is running"},
		{"start past a lock", JobControl{Job: q, Action: JobStart}, "waits for lock port:4300"},
		{"park a running job", JobControl{Job: run, Action: JobPark}, "pause it instead"},
		{"unpark a job not parked", JobControl{Job: q, Action: JobUnpark}, "is not parked"},
		{"pause a queued job", JobControl{Job: q, Action: JobPause}, "is not running"},
		{"resume a job not paused", JobControl{Job: run, Action: JobResume}, "is not paused"},
		{"an unknown action", JobControl{Job: q, Action: "nap"}, "unknown action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := l.m.controlJob(tc.c)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	rec := l.rec(t, run)
	unit := rec.Cgroup
	rec.Cgroup = ""
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "runs uncontained")
	rec.Cgroup, rec.Claim.Exclusive = unit, true
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "is exclusive")
	rec.Claim.Exclusive, rec.Rung = false, sched.RungSqueezed
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "the ladder holds job")
	rec.Rung, rec.LentAt = "", time.Now()
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "is lent")
	rec.LentAt, rec.Kind = time.Time{}, sched.KindAgent
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "agent job")
	rec.Kind = ""
	assert.NoError(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "and with none of those, it pauses")

	// An agent or run job starts by its own rule, which no park would stop.
	for kind, want := range map[string]string{sched.KindAgent: "agent job", sched.KindRun: "flow run"} {
		l.rec(t, q).Kind = kind
		assert.ErrorContains(t, l.m.controlJob(JobControl{Job: q, Action: JobPark}), want)
		assert.False(t, l.rec(t, q).Parked, kind)
	}
}

// wqLocked submits a sleep naming lock, which the test's running job holds,
// so no pass starts it.
func wqLocked(t *testing.T, m *Model, lock string) sched.JobID {
	t.Helper()
	sub := wqJob(t, "sleep", "31")
	sub.Locks = []string{lock}
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqPass(t, m)
	require.Equal(t, sched.Queued, wqRecord(t, m, id).State)
	return id
}

func TestControlJob_ParkAndUnpark(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	l.rec(t, l.ids[0]).Locks = []string{"port:4300"}
	id := wqLocked(t, l.m, "port:4300")
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPark}))
	assert.True(t, wqRecord(t, l.m, id).Parked)
	wqPass(t, l.m)
	assert.Equal(t, sched.WaitParked, l.m.queue.waiting[id])
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: id, Action: JobPark}), "parked already")

	before := time.Now()
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobUnpark}))
	rec := wqRecord(t, l.m, id)
	assert.False(t, rec.Parked)
	assert.False(t, rec.Unparked.Before(before), "the starvation wait starts again")
}

// pausedLadder is the ladder's fixture with one job, sampled once, and swap
// with room for its working set unless swapKB says otherwise.
func pausedLadder(t *testing.T, swapKB int64) (*wqLadder, sched.JobID) {
	t.Helper()
	l := newWQLadder(t, 20, 1)
	prev := readSwapFree
	readSwapFree = func() (int64, error) { return swapKB, nil }
	t.Cleanup(func() { readSwapFree = prev })
	id := l.ids[0]
	l.write(t, id, map[string]string{"memory.reclaim": ""})
	wqPass(t, l.m) // a sample, so its working set is known
	return l, id
}

func TestControlJob_PauseFreezesReclaimsAndResumeThaws(t *testing.T) {
	l, id := pausedLadder(t, 15<<20)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Freeze "+l.rec(t, id).Unit)
	rec := l.rec(t, id)
	require.True(t, rec.Paused())
	assert.True(t, rec.PauseReclaimed)
	assert.True(t, rec.Laddered, "a paused run teaches floors only")
	assert.Empty(t, rec.Control)
	data, err := os.ReadFile(filepath.Join(l.root, rec.Cgroup, "memory.reclaim"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "swappiness=200")

	// The sampler sees it frozen: no rung, so the ladder never thaws it.
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Empty(t, l.rec(t, id).Rung)
	assert.True(t, l.rec(t, id).Paused())

	l.rec(t, id).PausedAt = time.Now().Add(-time.Minute)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobResume}))
	wqPump(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
	assert.False(t, l.rec(t, id).Paused())
	assert.GreaterOrEqual(t, l.rec(t, id).PausedSecs, int64(59), "the pause is summed")
}

func TestControlJob_PauseWithSwapShortIsCPUOnly(t *testing.T) {
	l, id := pausedLadder(t, 1<<10) // 1 MB of swap free
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	require.True(t, l.rec(t, id).Paused())
	assert.False(t, l.rec(t, id).PauseReclaimed)
	data, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "memory.reclaim"))
	require.NoError(t, err)
	assert.Empty(t, string(data), "1 MB of swap can't hold its working set: nothing reclaimed")
}

func TestControlJob_AFailedFreezeChangesNothing(t *testing.T) {
	l, id := pausedLadder(t, 15<<20)
	l.fake.fail = map[string]error{"Freeze": errNoBusForTest}
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	rec := l.rec(t, id)
	assert.False(t, rec.Paused())
	assert.Empty(t, rec.Control)
	assert.Contains(t, rec.ControlErr, "pause failed")

	l.fake.fail = nil
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	assert.True(t, l.rec(t, id).Paused())
	assert.Empty(t, l.rec(t, id).ControlErr, "a success clears the last failure")
}

func TestControlJob_ThawedByHandEndsThePause(t *testing.T) {
	l, id := pausedLadder(t, 15<<20)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	l.rec(t, id).PausedAt = time.Now().Add(-time.Minute)
	wqPass(t, l.m) // the cgroup reads not frozen, in a sample after the pause
	rec := l.rec(t, id)
	assert.False(t, rec.Paused())
	assert.Positive(t, rec.PausedSecs)
}

func TestControlJob_CancelThawsAPausedJob(t *testing.T) {
	l, id := pausedLadder(t, 15<<20)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	require.NoError(t, l.m.cancelJob(id, "from the work view"))
	// The step, before the SIGTERM the gate sends settles the job.
	l.m.stepJobControls()
	wqPump(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
}

func TestControlJob_TheLadderAndTheGiveBackLeaveAPausedJobAlone(t *testing.T) {
	l, id := pausedLadder(t, 15<<20)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	calls := len(l.fake.Calls())
	l.rec(t, id).Borrower = true
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	l.setAvail(2) // short and tight: the ladder and the give-back both run
	wqPass(t, l.m)
	wqPass(t, l.m)
	for _, call := range l.fake.Calls()[calls:] {
		assert.NotContains(t, call, l.rec(t, id).Unit, "no squeeze, freeze, thaw or kill of a paused job")
	}
	assert.True(t, l.rec(t, id).Paused())
	assert.Empty(t, l.rec(t, id).Requeue)
}

func TestControlJob_APauseOutlastsAnAdoption(t *testing.T) {
	l, id := pausedLadder(t, 15<<20)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	// A restart finds the scope still frozen, and its boot adopts the job,
	// sampling it at once.
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	l.m.adoptJobs()
	wqPass(t, l.m)
	assert.True(t, l.rec(t, id).Paused())
	assert.Empty(t, l.rec(t, id).Rung, "still the owner's pause, not the ladder's")
}

func TestControlJob_AParkedDependentStaysParkedWhenReleased(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	first := l.ids[0]
	dep := wqSubmitAfter(t, l.m, first, "sleep", "31")
	require.NoError(t, l.m.controlJob(JobControl{Job: dep.ID, Action: JobPark}))
	ids := l.m.releaseDependents(wqRecord(t, l.m, first), time.Now())
	require.Equal(t, []sched.JobID{dep.ID}, ids)
	rec := wqRecord(t, l.m, dep.ID)
	assert.Equal(t, sched.Queued, rec.State)
	assert.True(t, rec.Parked, "the release leaves the park alone")
	l.m.admitJobs()
	assert.Equal(t, sched.WaitParked, l.m.queue.waiting[dep.ID])
}

func TestControlJob_TheReplyCarriesTheRefusal(t *testing.T) {
	l := newWQLadder(t, 20, 0)
	l.m.ControlJob(JobControl{Job: 99, Action: JobPark}, 7)
	r := requestReply(t, l.m, 7)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.ErrorContains(t, r.Err, "there is no job 99")
}

func TestQueueView_APausedJobsOverdueClockStandsStill(t *testing.T) {
	m, _ := wqGreedyModel(t)
	sub := wqJob(t, "sh", "-c", "sleep 30")
	wqCurved(m, wqSigOf(t, sub.Argv, sub.Cwd), wqCurve(1, 100, 60, 1, 200, 120), 1)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := &m.queue.store.Jobs[m.queue.index(id)]
	base := m.queueJobView(m.queue, *rec, nil).OverdueAfter
	require.Positive(t, base, "its runs give it an overdue mark")
	rec.PausedSecs = 600
	assert.Equal(t, base+10*time.Minute, m.queueJobView(m.queue, *rec, nil).OverdueAfter)
	// Nor does a lent job's (memory borrowing spec §6.4).
	rec.LentSecs = 300
	assert.Equal(t, base+15*time.Minute, m.queueJobView(m.queue, *rec, nil).OverdueAfter)
}
