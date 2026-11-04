package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qvBlockedModel is a model over a seeded global dir whose caps are 4 cores
// and 8 GB (so a default claim is 4 cores and 6 GB), and the dir. It holds a
// running job (5, a real shim) that takes all 4 cores, two queued jobs (2 and
// 3) and a finished one (4). The next ID is 6.
func qvBlockedModel(t *testing.T) (*Model, string) {
	t.Helper()
	q2, q3 := qvRecord(2, sched.Queued), qvRecord(3, sched.Queued)
	q2.Claim, q3.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}, sched.Claim{CPUs: 4, MemoryGB: 6}
	// The block is invalid at the load, which pauses admission: a load admits
	// what fits, and with no job running yet a queued one would start (and
	// fail, its folder being gone). The real block goes in once the blocker runs.
	dir := qvSeed(t, `{"cpus": 5000}`, 5, q2, q3, qvFinished(4, sched.Done, qvEpoch.Add(time.Minute)))
	m := qvLive(t, dir)
	require.Equal(t, sched.JobID(5), wqLaunch(t, m, "sh", "-c", "sleep 30"), "fixture: the blocker follows the seeded IDs")
	wqConfig(t, m, `{"cpus": 4, "memory_gb": 8}`)
	m.admitJobs()
	require.Empty(t, m.queue.paused, "fixture: the pass applied the block")
	return m, dir
}

// qvSubmit is a submission the model accepts.
func qvSubmit(t *testing.T) sched.Submit {
	t.Helper()
	return sched.Submit{
		Argv:        []string{"cargo", "test"},
		Cwd:         t.TempDir(),
		Env:         []string{"PATH=/usr/bin"},
		Description: "tests",
		Notify:      true,
		Tail:        20,
	}
}

// requestReply is the one Reply to req the model produced.
func requestReply(t *testing.T, m *Model, req ReqID) Reply {
	t.Helper()
	r := replyTo(m.Drain().Events, req)
	require.NotNil(t, r, "a Reply to request %d", req)
	return *r
}

func queueOrder(m *Model) []sched.JobID {
	return jobIDs(m.Queue())
}

// SubmitJob replies with the new ID in Reply.Job and says nothing more for a
// submission from a plain shell; from a launch no served session holds it
// replies with the warning in Reply.Notice, and the job is queued either way.
func TestSubmitJob_RepliesWithTheIDAndTheOwnerWarning(t *testing.T) {
	m, _ := qvBlockedModel(t)

	m.SubmitJob(qvSubmit(t), 3)
	r := requestReply(t, m, 3)
	require.NoError(t, r.Err)
	assert.Equal(t, sched.JobID(6), r.Job)
	assert.NoError(t, r.Notice, "a plain shell has no owner and no warning")

	ghost := qvSubmit(t)
	ghost.Launch = "ghost"
	m.SubmitJob(ghost, 4)
	r = requestReply(t, m, 4)
	require.NoError(t, r.Err)
	assert.Equal(t, sched.JobID(7), r.Job)
	require.Error(t, r.Notice)
	assert.Equal(t, "this shell's loom session (launch ghost) isn't one the daemon serves: job 7 has no owner, so no message will come", r.Notice.Error())

	queued := jobOf(t, m.Queue(), 7)
	assert.Equal(t, sched.Queued, queued.State)
	assert.Zero(t, queued.Owner)
	assert.Equal(t, "needs 4 cores (0 free)", queued.Waiting, "it waits behind the job that takes every core")
}

// The environment is stored for the shim and never published: not in the
// view, so not in anything a client or the web layer is sent.
func TestSubmitJob_NeverPublishesTheEnvironment(t *testing.T) {
	m, _ := qvBlockedModel(t)
	spec := qvSubmit(t)
	spec.Env = append(spec.Env, "LOOM_SECRET_PROBE=hunter2")

	m.SubmitJob(spec, 3)
	r := requestReply(t, m, 3)
	require.NoError(t, r.Err)

	stored, err := sched.ReadSpec(sched.JobDir(m.Queue().Dir, r.Job))
	require.NoError(t, err)
	assert.Contains(t, stored.Env, "LOOM_SECRET_PROBE=hunter2", "the shim gets it")
	for name, v := range map[string]any{"Queue": m.Queue(), "QueueChanged": QueueChanged{View: m.Queue()}} {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		assert.NotContains(t, string(b), "hunter2", name)
	}
}

// A refused submission replies with an error that matches ErrRefused and
// names its bound, adds nothing to the queue, and with ReqID 0 replies to
// no one.
func TestSubmitJob_RefusalsAreReplies(t *testing.T) {
	m, _ := qvBlockedModel(t)
	before := queueOrder(m)

	noDesc := qvSubmit(t)
	noDesc.Description = " "
	overCap := qvSubmit(t)
	overCap.Claim = sched.Claim{CPUs: 99}
	for i, tc := range []struct {
		name string
		spec sched.Submit
		want string
	}{
		{"an empty description", noDesc, "the description is empty"},
		{"a claim over a cap", overCap, "claims 99 cores; this host's cap is 4"},
	} {
		req := ReqID(10 + i)
		m.SubmitJob(tc.spec, req)
		r := requestReply(t, m, req)
		require.Error(t, r.Err, tc.name)
		assert.ErrorIs(t, r.Err, ErrRefused, tc.name)
		assert.Contains(t, r.Err.Error(), tc.want, tc.name)
		assert.Zero(t, r.Job, tc.name)
	}
	m.SubmitJob(noDesc, 0)
	assert.Empty(t, replies(m.Drain()), "no Reply was asked for")
	assert.Equal(t, before, queueOrder(m), "nothing was queued")
}

// While admission is paused, here by a scheduler block with an invalid
// number of cores, a submission is refused with the reason.
func TestSubmitJob_RefusedWhileAdmissionIsPaused(t *testing.T) {
	m := wqModelAt(t, qvSeed(t, `{"cpus": 5000}`, 0))

	m.SubmitJob(qvSubmit(t), 3)

	r := requestReply(t, m, 3)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.EqualError(t, r.Err, "admission paused: scheduler.cpus is 5000: it must be 1–1024")
	assert.Empty(t, m.Queue().Jobs)
}

// CancelJob maps its source to the reason the notice words, cancels a
// queued job at once, and refuses an unknown source, a finished job and a
// job the queue doesn't hold.
func TestCancelJob_MapsItsSourceAndRefuses(t *testing.T) {
	m, _ := qvBlockedModel(t)

	m.CancelJob(2, CancelFromView, 3)
	require.NoError(t, requestReply(t, m, 3).Err)
	j := jobOf(t, m.Queue(), 2)
	assert.Equal(t, sched.Cancelled, j.State)
	assert.Equal(t, "from the work view", j.CancelReason)

	m.CancelJob(3, CancelFromCLI, 4)
	require.NoError(t, requestReply(t, m, 4).Err)
	j = jobOf(t, m.Queue(), 3)
	assert.Equal(t, sched.Cancelled, j.State)
	assert.Equal(t, "with loom job cancel", j.CancelReason)

	for _, tc := range []struct {
		name string
		id   sched.JobID
		from CancelFrom
		want string
	}{
		{"an unknown source", 5, CancelFrom("web"), `unknown source "web"`},
		{"an empty source", 5, CancelFrom(""), `unknown source ""`},
		{"a finished job", 4, CancelFromCLI, "job 4 has finished"},
		{"a job the queue doesn't hold", 99, CancelFromView, "there is no job 99"},
	} {
		m.CancelJob(tc.id, tc.from, 20)
		r := requestReply(t, m, 20)
		require.Error(t, r.Err, tc.name)
		assert.ErrorIs(t, r.Err, ErrRefused, tc.name)
		assert.Contains(t, r.Err.Error(), tc.want, tc.name)
	}
	running := jobOf(t, m.Queue(), 5)
	assert.Equal(t, sched.Running, running.State, "the refused cancels touched nothing")
	assert.Empty(t, running.CancelReason)
}

// MoveJobToFront puts a queued job first among the queued ones, and refuses
// a job that isn't waiting and one the queue doesn't hold.
func TestMoveJobToFront_MovesAndRefuses(t *testing.T) {
	m, _ := qvBlockedModel(t)
	require.Equal(t, []sched.JobID{5, 2, 3, 4}, queueOrder(m))

	m.MoveJobToFront(3, 3)
	require.NoError(t, requestReply(t, m, 3).Err)
	assert.Equal(t, []sched.JobID{5, 3, 2, 4}, queueOrder(m))

	m.MoveJobToFront(5, 4)
	r := requestReply(t, m, 4)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.EqualError(t, r.Err, "job 5 isn't waiting")
	m.MoveJobToFront(99, 5)
	assert.EqualError(t, requestReply(t, m, 5).Err, "there is no job 99")
	assert.Equal(t, []sched.JobID{5, 3, 2, 4}, queueOrder(m))
}

// NotifyJob refuses a job with no owner and one that has ended, saying which.
func TestNotifyJob_RefusesWhatCannotBeNotified(t *testing.T) {
	m, _ := qvBlockedModel(t)

	for i, tc := range []struct {
		id   sched.JobID
		want string
	}{
		{2, "job 2 has no owner"},
		{4, "job 4 has finished"},
		{99, "there is no job 99"},
	} {
		req := ReqID(3 + i)
		m.NotifyJob(tc.id, req)
		r := requestReply(t, m, req)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.EqualError(t, r.Err, tc.want)
	}
}

// SetSchedulerCaps applies a valid block at once (the view shows it and the
// caps in effect) and writes it to the global config; an invalid one is
// refused with the bound it broke and changes neither. The raised caps leave
// the queued jobs waiting, so no shim starts.
func TestSetSchedulerCaps_AppliesOrRefuses(t *testing.T) {
	m, dir := qvBlockedModel(t)

	m.SetSchedulerCaps(config.SchedulerSettings{CPUs: -1}, 3)
	r := requestReply(t, m, 3)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Contains(t, r.Err.Error(), "scheduler.cpus is -1")
	assert.Equal(t, 4, config.LoadConfigFrom(dir).GetScheduler().CPUs, "nothing was written")
	assert.Equal(t, 4, m.Queue().Settings.CPUs)

	m.SetSchedulerCaps(config.SchedulerSettings{CPUs: 6, MemoryGB: 8}, 4)
	require.NoError(t, requestReply(t, m, 4).Err)
	v := m.Queue()
	assert.Equal(t, 6, v.Settings.CPUs)
	assert.Equal(t, 6, v.Policy.Caps.CPUs, "the pass it ran applied it")
	assert.Equal(t, 8, v.Policy.Caps.MemoryGB)
	assert.Equal(t, 6, config.LoadConfigFrom(dir).GetScheduler().CPUs, "written to the global config")
	assert.Equal(t, sched.Queued, jobOf(t, v, 2).State, "a 4-core job still does not fit beside the running one")
}
