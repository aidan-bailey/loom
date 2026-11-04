package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var qvEpoch = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// qvBlock is the scheduler block these tests configure: 8 cores, 16 GB, 2
// I/O jobs and a default claim of 2 cores and 4 GB. The policy it resolves
// to (qvPolicy) does not depend on the host the tests run on.
const qvBlock = `{"cpus": 8, "memory_gb": 16, "io_jobs": 2, "default_claim": {"cpus": 2, "memory_gb": 4}}`

// qvPolicy is the policy qvBlock resolves to.
var qvPolicy = sched.Policy{
	Caps:         sched.Caps{CPUs: 8, MemoryGB: 16, IOJobs: 2},
	DefaultClaim: sched.Claim{CPUs: 2, MemoryGB: 4},
	StarveAfter:  10 * time.Minute,
	KeepFinished: 50,
}

// qvRecord is a record of the given state, ordered by its ID, with the
// default claim of qvBlock. Its submission time is now, so a queued job has
// not yet waited long enough to starve the others.
func qvRecord(id sched.JobID, state sched.State) sched.Record {
	return sched.Record{
		ID:          id,
		Order:       int64(id),
		Description: fmt.Sprintf("job %d", id),
		Argv:        []string{"sleep", "1"},
		Cwd:         "/work",
		Claim:       sched.Claim{CPUs: 2, MemoryGB: 4},
		Tail:        20,
		State:       state,
		Submitted:   time.Now(),
	}
}

// qvFinished is a finished record that ended at ended: a done job exits 0, a
// failed one exits 2 after a peak of 512 KB, a cancelled one was cancelled
// from the work view.
func qvFinished(id sched.JobID, state sched.State, ended time.Time) sched.Record {
	r := qvRecord(id, state)
	r.Started, r.Ended = ended.Add(-time.Minute), ended
	switch state {
	case sched.Done:
		r.Exit = &sched.Exit{Code: 0, Ended: ended}
	case sched.Failed:
		r.Exit = &sched.Exit{Code: 2, Ended: ended, MaxRSSKB: 512}
		r.Failure = "exit 2"
	case sched.Cancelled:
		r.CancelReason = "from the work view"
	}
	return r
}

// qvSeed is a global dir that holds the scheduler block block (none when ""),
// and a jobs dir with a spec.json for each of jobs and a jobs.json listing
// them. The next ID is the greater of next and one past the highest ID.
func qvSeed(t *testing.T, block string, next sched.JobID, jobs ...sched.Record) string {
	t.Helper()
	dir := t.TempDir()
	if block != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"scheduler": `+block+`}`), 0o644))
	}
	jobsDir := filepath.Join(dir, "jobs")
	next = max(next, 1)
	for _, r := range jobs {
		require.NoError(t, sched.WriteSpec(sched.JobDir(jobsDir, r.ID), sched.Spec{ID: r.ID, Argv: r.Argv, Cwd: r.Cwd}))
		next = max(next, r.ID+1)
	}
	require.NoError(t, sched.SaveStore(jobsDir, sched.Store{Version: sched.StoreVersion, NextID: next, Jobs: jobs}))
	return dir
}

// qvLive is wqModelAt over dir for a test that runs real shims: when the test
// ends every job still unfinished is cancelled and settled, so no process
// group outlives it.
func qvLive(t *testing.T, dir string) *Model {
	t.Helper()
	m := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, m) })
	return m
}

func jobIDs(v QueueView) []sched.JobID {
	var ids []sched.JobID
	for _, j := range v.Jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func jobOf(t *testing.T, v QueueView, id sched.JobID) JobView {
	t.Helper()
	for _, j := range v.Jobs {
		if j.ID == id {
			return j
		}
	}
	require.Failf(t, "no such job", "job %d is not in the view", id)
	return JobView{}
}

// The view lists the running jobs by start, the queued ones in queue order
// and the finished ones newest end first; it totals what the running jobs
// claim and use, takes an exclusive claim's need at the caps in effect, and
// shows usage only for a running job and a wait only for a queued one. Two
// real shims run; the one launched first is made the later to start, so the
// order is by start and not by ID.
func TestQueueView_OrdersJobsAndTotals(t *testing.T) {
	queuedExcl := qvRecord(5, sched.Queued)
	queuedExcl.Claim = sched.Claim{Exclusive: true}
	queuedFront := qvRecord(6, sched.Queued)
	queuedFront.Order = 2 // moved to the front
	queuedFront.Claim = sched.Claim{CPUs: 6, MemoryGB: 4}
	// The block is invalid at the load, which pauses admission: a load admits
	// what fits, and with no job running yet the queued ones would start
	// (and fail, their folder being gone). The valid block goes in once the
	// two shims run.
	dir := qvSeed(t, `{"cpus": 5000}`, 0,
		qvFinished(4, sched.Failed, qvEpoch.Add(90*time.Second)), queuedExcl,
		qvFinished(2, sched.Done, qvEpoch.Add(30*time.Second)),
		qvFinished(8, sched.Cancelled, qvEpoch.Add(60*time.Second)), queuedFront)
	m := qvLive(t, dir)
	first := wqLaunch(t, m, "sh", "-c", "sleep 30")
	second := wqLaunch(t, m, "sh", "-c", "sleep 30")
	require.Equal(t, []sched.JobID{9, 10}, []sched.JobID{first, second}, "fixture: the IDs follow the seeded ones")
	m.queue.store.Jobs[m.queue.index(first)].Started = m.queue.store.Jobs[m.queue.index(second)].Started.Add(time.Second)
	wqConfig(t, m, qvBlock)
	wqPass(t, m)
	wqPass(t, m)
	m.queue.usage[2] = jobUsage{RSSKB: 999} // a finished job's stale usage
	m.admitJobs()
	m.queue.waiting[first] = "stale: it is running"

	v := m.Queue()

	assert.Equal(t, []sched.JobID{second, first, 6, 5, 4, 8, 2}, jobIDs(v), "running by start, queued in queue order, finished newest first")
	assert.Equal(t, wqHost, v.Host)
	assert.Equal(t, config.SchedulerSettings{CPUs: 8, MemoryGB: 16, IOJobs: 2, DefaultClaim: &config.ClaimSettings{CPUs: 2, MemoryGB: 4}}, v.Settings)
	assert.Equal(t, qvPolicy, v.Policy)
	assert.Equal(t, filepath.Join(dir, "jobs"), v.Dir)
	assert.Empty(t, v.Paused)
	assert.Equal(t, sched.Need{CPUs: 8, MemoryGB: 12}, v.Claimed, "what the two running jobs' claims take")

	run1, run2 := jobOf(t, v, first), jobOf(t, v, second)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 6}, run1.Need)
	assert.Equal(t, filepath.Join(sched.JobDir(v.Dir, first), sched.LogFile), run1.LogPath)
	assert.Empty(t, run1.Waiting, "a running job waits for nothing")
	assert.Greater(t, run1.RSSKB, int64(0), "its sampled usage")
	assert.Equal(t, m.queue.usage[first].RSSKB, run1.RSSKB)
	assert.Equal(t, run1.RSSKB+run2.RSSKB, v.UsedRSSKB, "the finished job's stale usage is not counted")
	assert.InDelta(t, run1.CPU+run2.CPU, v.UsedCPU, 1e-9)

	excl := jobOf(t, v, 5)
	assert.Equal(t, sched.Need{CPUs: 8, MemoryGB: 16, IOSlots: 2}, excl.Need, "an exclusive claim takes the caps as they stand")
	assert.Equal(t, "needs 8 cores (0 free)", excl.Waiting)
	front := jobOf(t, v, 6)
	assert.Equal(t, sched.Need{CPUs: 6, MemoryGB: 4}, front.Need)
	assert.Equal(t, "needs 6 cores (0 free)", front.Waiting)

	done := jobOf(t, v, 2)
	assert.Zero(t, done.RSSKB, "a finished job shows no usage")
	require.NotNil(t, done.Exit)
	assert.Zero(t, done.Exit.Code)
	assert.True(t, done.Exit.Ended.Equal(qvEpoch.Add(30*time.Second)))
	failed := jobOf(t, v, 4)
	assert.Equal(t, "exit 2", failed.Failure)
	assert.Equal(t, int64(512), failed.Exit.MaxRSSKB)
	assert.Equal(t, "from the work view", jobOf(t, v, 8).CancelReason)
}

// While admission is paused the policy is zero, so an exclusive claim needs
// nothing, and a queued job the last pass said nothing about waits for the
// reason; one it did speak of keeps its own. The block's invalid cores pause
// admission, so neither job starts.
func TestQueueView_PausedQueueSaysWhy(t *testing.T) {
	const why = "scheduler.cpus is 5000: it must be 1–1024"
	exclusive := qvRecord(2, sched.Queued)
	exclusive.Claim = sched.Claim{Exclusive: true}
	m := wqModelAt(t, qvSeed(t, `{"cpus": 5000}`, 0, qvRecord(1, sched.Queued), exclusive))

	v := m.Queue()

	assert.Equal(t, why, v.Paused)
	assert.Equal(t, sched.Policy{}, v.Policy)
	assert.Equal(t, 5000, v.Settings.CPUs, "the block as it was read")
	assert.Equal(t, "admission paused: "+why, jobOf(t, v, 1).Waiting)
	assert.Equal(t, "admission paused: "+why, jobOf(t, v, 2).Waiting, "no pass has spoken of it")
	assert.Equal(t, sched.Need{}, jobOf(t, v, 2).Need)

	m.queue.waiting = map[sched.JobID]string{2: "over this host's caps"}
	v = m.Queue()
	assert.Equal(t, "admission paused: "+why, jobOf(t, v, 1).Waiting)
	assert.Equal(t, "over this host's caps", jobOf(t, v, 2).Waiting, "a reason a pass gave is kept")
}

// An owner resolves by identity to its title, its workspace's label and its
// workspace's ID; one no served workspace holds keeps its ID with none of
// them, and a job without one stays 0. The owner's job is a real one,
// submitted from the session's launch and run to its end.
func TestQueueView_ResolvesTheOwnerByIdentity(t *testing.T) {
	orphan := qvFinished(1, sched.Done, qvEpoch)
	orphan.Owner = 12345
	m := qvLive(t, qvSeed(t, qvBlock, 0, orphan, qvFinished(2, sched.Done, qvEpoch)))
	owner := wqOwnerSession(t, m)
	sub := wqJob(t, "true")
	sub.Launch = "L1"
	id, warning, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Empty(t, warning)
	wqSettle(t, m, id)
	_, ws := m.lookup(owner)
	require.NotNil(t, ws, "fixture: the owner is served")

	v := m.Queue()

	owned := jobOf(t, v, id)
	assert.Equal(t, owner, owned.Owner)
	assert.Equal(t, "owner", owned.OwnerTitle)
	assert.Equal(t, "owners", owned.OwnerWorkspace)
	assert.Equal(t, ws.Label(), owned.OwnerWorkspace)
	assert.Equal(t, m.wsIDOf(ws), owned.OwnerWorkspaceID)
	assert.NotZero(t, owned.OwnerWorkspaceID)
	gone := jobOf(t, v, 1)
	assert.Equal(t, InstanceID(12345), gone.Owner)
	assert.Empty(t, gone.OwnerTitle, "no served session holds it")
	assert.Empty(t, gone.OwnerWorkspace)
	assert.Zero(t, gone.OwnerWorkspaceID)
	plain := jobOf(t, v, 2)
	assert.Zero(t, plain.Owner)
	assert.Zero(t, plain.OwnerWorkspaceID)
}

// Nothing a caller does to a returned view reaches the model's records, and
// Clone shares nothing with its source.
func TestQueueView_HandsOutCopies(t *testing.T) {
	m := wqModelAt(t, qvSeed(t, qvBlock, 0, qvRecord(1, sched.Queued), qvFinished(2, sched.Failed, qvEpoch)))

	v := m.Queue()
	jobOf(t, v, 1).Argv[0] = "edited"
	jobOf(t, v, 2).Exit.Code = 99
	v.Settings.DefaultClaim.CPUs = 99
	again := m.Queue()
	assert.Equal(t, "sleep", jobOf(t, again, 1).Argv[0], "argv")
	assert.Equal(t, 2, jobOf(t, again, 2).Exit.Code, "exit")
	assert.Equal(t, 2, again.Settings.DefaultClaim.CPUs, "default claim")

	c := again.Clone()
	jobOf(t, again, 1).Argv[0] = "edited"
	jobOf(t, again, 2).Exit.Code = 99
	again.Settings.DefaultClaim.CPUs = 99
	assert.Equal(t, "sleep", jobOf(t, c, 1).Argv[0], "clone argv")
	assert.Equal(t, 2, jobOf(t, c, 2).Exit.Code, "clone exit")
	assert.Equal(t, 2, c.Settings.DefaultClaim.CPUs, "clone default claim")
}

func queueEvents(events []Event) []QueueChanged {
	var out []QueueChanged
	for _, ev := range events {
		if q, ok := ev.(QueueChanged); ok {
			out = append(out, q)
		}
	}
	return out
}

// The queue publishes on the first Sync, then only when its revision moves
// or a different queue replaces it, and the snapshot a late client gets
// carries it without taking the next publish away.
func TestPublishQueue_OnceThenOnChange(t *testing.T) {
	m := wqModelAt(t, qvSeed(t, qvBlock, 0, qvFinished(1, sched.Done, qvEpoch), qvFinished(2, sched.Failed, qvEpoch.Add(time.Minute))))

	first := queueEvents(m.Sync().Events)
	require.Len(t, first, 1, "published the first time")
	assert.Equal(t, m.Queue(), first[0].View)
	assert.Empty(t, queueEvents(m.Sync().Events), "nothing changed")

	m.queue.store.Jobs[0].Description = "renamed"
	assert.Empty(t, queueEvents(m.Sync().Events), "a change that bumps no revision is not seen")

	m.queue.rev++
	moved := queueEvents(m.Sync().Events)
	require.Len(t, moved, 1, "a moved revision")
	assert.Equal(t, "renamed", jobOf(t, moved[0].View, m.queue.store.Jobs[0].ID).Description)

	m.queue = &queueState{dir: m.queue.dir, rev: m.queue.rev}
	replaced := queueEvents(m.Sync().Events)
	require.Len(t, replaced, 1, "a different queue at the same revision, as a boot installs one")
	assert.Empty(t, replaced[0].View.Jobs)

	m.queue.rev++
	snap := queueEvents(m.Snapshot())
	require.Len(t, snap, 1, "the snapshot holds the queue")
	assert.Equal(t, m.Queue(), snap[0].View, "as it is now, not as last published")
	assert.Len(t, queueEvents(m.Sync().Events), 1, "and is no publish: the change still goes out at the next Sync")
	assert.Empty(t, queueEvents(m.Sync().Events), "once")
}
