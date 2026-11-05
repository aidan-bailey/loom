package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqHeld is a job held after job after, at order: one core, 1 GB, a real
// folder, a command that exits at once.
func wqHeld(t *testing.T, id, after sched.JobID, order int64) sched.Record {
	t.Helper()
	r := wqQueuedRecord(t, id)
	r.State, r.After, r.Order = sched.Held, after, order
	return r
}

// wqRunning is a running job's record with no shim behind it: a test ends
// it with wqEnd.
func wqRunning(t *testing.T, id sched.JobID) sched.Record {
	t.Helper()
	r := wqQueuedRecord(t, id)
	r.State, r.Started = sched.Running, time.Now()
	return r
}

// wqEnd ends job id in state with exit (nil for none) and settles it, as a
// pass does.
func wqEnd(t *testing.T, m *Model, id sched.JobID, state sched.State, exit *sched.Exit) {
	t.Helper()
	i := m.queue.index(id)
	require.GreaterOrEqual(t, i, 0, "job %d", id)
	m.queue.store.Jobs[i].Exit = exit
	m.finishJob(i, state, time.Now())
	m.afterSettle()
}

// wqSubmitAfter submits a job of argv after job after and returns its record.
func wqSubmitAfter(t *testing.T, m *Model, after sched.JobID, argv ...string) sched.Record {
	t.Helper()
	sub := wqJob(t, argv...)
	sub.After = after
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	return wqRecord(t, m, id)
}

// wqPaused pauses admission (an invalid block), so nothing a test seeds
// starts, while settles, releases and cascades go on.
func wqPaused(t *testing.T, m *Model) {
	t.Helper()
	wqConfig(t, m, `{"cpus": -1}`)
	m.refreshPolicy()
	require.NotEmpty(t, m.queue.paused)
}

func TestJobQueue_AfterRefusesWhatCanNeverRun(t *testing.T) {
	m := wqModel(t)
	failed := wqEnded(5, 0)
	failed.State, failed.Exit = sched.Failed, &sched.Exit{Code: 101}
	cancelled := wqEnded(6, 0)
	cancelled.State, cancelled.Exit, cancelled.CancelReason = sched.Cancelled, nil, "with loom job cancel"
	lost := wqEnded(7, 0)
	lost.State, lost.Exit = sched.Lost, nil
	stranger := wqRunning(t, 8)
	stranger.Owner = 77
	for _, r := range []sched.Record{failed, cancelled, lost, stranger} {
		wqSeed(t, m, r)
	}
	next := m.queue.store.NextID

	for after, want := range map[sched.JobID]string{
		99: "there is no job 99",
		5:  "job 5 failed (exit 101), so a job after it would never run",
		6:  "job 6 was cancelled, so a job after it would never run",
		7:  "job 7 was lost, so a job after it would never run",
		8:  "job 8 has another owner: --after takes a job with the same owner as the new one",
	} {
		sub := wqJob(t, "true")
		sub.After = after
		_, _, err := m.submitJob(sub)
		assert.EqualError(t, err, want)
	}
	assert.Equal(t, next, m.queue.store.NextID, "nothing was created")
	assert.Len(t, m.queue.store.Jobs, 4)
}

func TestJobQueue_AJobAfterAWaitingOrRunningOneIsHeldAtItsPlace(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	running := wqSubmit(t, m, "sleep", "30")
	queued := wqSubmit(t, m, "true")
	m.Drain() // the running job's start is never run here

	b := wqSubmitAfter(t, m, running, "true")
	assert.Equal(t, sched.Held, b.State)
	assert.Equal(t, running, b.After)
	assert.Equal(t, int64(1), b.Order, "the running job's place in line")
	assert.True(t, b.Ready.IsZero(), "it hasn't joined the queue")
	c := wqSubmitAfter(t, m, queued, "true")
	assert.Equal(t, sched.Held, c.State)
	assert.Equal(t, int64(2), c.Order)
	d := wqSubmitAfter(t, m, b.ID, "true")
	assert.Equal(t, sched.Held, d.State, "a job after a held one is held too")
	assert.Equal(t, int64(1), d.Order, "a chain shares one place")

	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.Held, saved.Jobs[2].State, "written")
	assert.Equal(t, running, saved.Jobs[2].After)
}

func TestJobQueue_AJobAfterADoneOneQueuesAtOnceAtTheBack(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	wqSeed(t, m, wqEnded(5, 0))
	wqSubmit(t, m, "sleep", "30") // job 6 fills the cores
	m.Drain()

	rec := wqSubmitAfter(t, m, 5, "true")

	assert.Equal(t, sched.Queued, rec.State)
	assert.Equal(t, int64(rec.ID), rec.Order, "its own place: an old finished job's is not borrowed")
	assert.Equal(t, rec.Submitted, rec.Ready)
	assert.Equal(t, sched.JobID(5), rec.After)
}

// The whole path, through real shims: the build ends, its dependent is
// released in the settle at the build's place, and the admission pass that
// follows starts it ahead of a job submitted between the two.
func TestJobQueue_ADoneJobReleasesItsDependentsAtItsPlace(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	gate := filepath.Join(t.TempDir(), "go")
	build := wqSubmit(t, m, wqGated(gate, 0)...)
	wqPump(t, m) // its start: a real shim
	later := wqSubmit(t, m, "true")
	tests := wqSubmitAfter(t, m, build, "true").ID
	require.Equal(t, sched.Held, wqRecord(t, m, tests).State)

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.Equal(t, sched.Done, wqSettle(t, m, build).State)

	rec := wqRecord(t, m, tests)
	assert.Equal(t, sched.Running, rec.State, "released in the settle, and started by the pass after it")
	assert.True(t, rec.Ready.After(rec.Submitted), "it joined the queue at its release")
	assert.Equal(t, int64(build), rec.Order, "at its prerequisite's place")
	assert.Equal(t, sched.Queued, wqRecord(t, m, later).State, "the job submitted between them waits")
	assert.Equal(t, sched.Done, wqSettle(t, m, tests).State)
	assert.Equal(t, sched.Done, wqSettle(t, m, later).State)
}

// A release takes the lower of the dependent's order and its prerequisite's:
// both sides of it, whatever order each had.
func TestJobQueue_AReleaseTakesTheLowerOfTheTwoOrders(t *testing.T) {
	m := wqModel(t)
	wqPaused(t, m)
	build := wqRunning(t, 1)
	build.Order = -3 // moved to the front after its dependents were held
	wqSeed(t, m, build)
	wqSeed(t, m, wqHeld(t, 2, 1, 1))
	wqSeed(t, m, wqHeld(t, 3, 1, -9))

	wqEnd(t, m, 1, sched.Done, &sched.Exit{Code: 0})

	assert.Equal(t, int64(-3), wqRecord(t, m, 2).Order, "its prerequisite's, the lower")
	assert.Equal(t, int64(-9), wqRecord(t, m, 3).Order, "its own, the lower: a held job moved to the front keeps its place")
	assert.Equal(t, sched.Queued, wqRecord(t, m, 3).State)
}

func TestJobQueue_AJobThatDoesNotSucceedCancelsItsChain(t *testing.T) {
	cases := []struct {
		name    string
		state   sched.State
		exit    *sched.Exit
		failure string
		want    string
	}{
		{"an exit code", sched.Failed, &sched.Exit{Code: 101}, "", "because job 1 failed (exit 101)"},
		{"a signal", sched.Failed, &sched.Exit{Code: -1, Signal: "SIGSEGV"}, "", "because job 1 was killed by SIGSEGV"},
		{"a start that failed", sched.Failed, nil, "its folder is gone", "because job 1 could not start"},
		{"a cancel", sched.Cancelled, nil, "", "because job 1 was cancelled"},
		{"a lost runner", sched.Lost, nil, lostWhy, "because job 1 was lost"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			wqPaused(t, m)
			wqSeed(t, m, wqRunning(t, 1))
			wqSeed(t, m, wqHeld(t, 2, 1, 1))
			wqSeed(t, m, wqHeld(t, 3, 2, 1))
			wqSeed(t, m, wqHeld(t, 4, 1, 1))
			wqSeed(t, m, wqQueuedRecord(t, 5))
			m.queue.store.Jobs[0].Failure = c.failure

			wqEnd(t, m, 1, c.state, c.exit)

			for id, reason := range map[sched.JobID]string{2: c.want, 3: "because job 2 was cancelled", 4: c.want} {
				rec := wqRecord(t, m, id)
				assert.Equal(t, sched.Cancelled, rec.State, "job %d", id)
				assert.Equal(t, reason, rec.CancelReason, "job %d", id)
				assert.False(t, rec.Ended.IsZero(), "job %d", id)
			}
			assert.Equal(t, sched.Queued, wqRecord(t, m, 5).State, "a job outside the chain is not touched")
		})
	}
}

func TestJobQueue_CancellingAHeldJobEndsItAndItsChainAtOnce(t *testing.T) {
	m := wqModel(t)
	wqPaused(t, m)
	wqSeed(t, m, wqRunning(t, 1))
	wqSeed(t, m, wqHeld(t, 2, 1, 1))
	wqSeed(t, m, wqHeld(t, 3, 2, 1))

	require.NoError(t, m.cancelJob(2, "with loom job cancel"))

	assert.Equal(t, sched.Cancelled, wqRecord(t, m, 2).State)
	assert.Equal(t, "with loom job cancel", wqRecord(t, m, 2).CancelReason)
	assert.Equal(t, sched.Cancelled, wqRecord(t, m, 3).State)
	assert.Equal(t, "because job 2 was cancelled", wqRecord(t, m, 3).CancelReason)
	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State, "its prerequisite runs on")
	assert.EqualError(t, m.cancelJob(3, "x"), "job 3 has finished")
}

// A pause marks every job of its session before it ends any: the cascade a
// held job's end sets off keeps the pause's reason and sends nothing.
func TestJobQueue_APauseEndsHeldJobsWithItsReasonAndNoMessage(t *testing.T) {
	m := wqModel(t)
	owner := wqOwnerSession(t, m)
	wqPaused(t, m)
	own := func(r sched.Record) sched.Record {
		r.Owner, r.Notify = uint64(owner), true
		return r
	}
	wqSeed(t, m, own(wqRunning(t, 1)))
	wqSeed(t, m, own(wqHeld(t, 2, 1, 1)))
	wqSeed(t, m, own(wqHeld(t, 3, 2, 1)))

	m.Pause(owner, 0)
	m.Drain() // the session's own job and the queue's pass, which this test does not run

	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Cancelled, rec.State, "job %d: a held job ends at once", id)
		assert.Equal(t, "because the session was paused", rec.CancelReason, "job %d keeps the pause's reason", id)
		assert.False(t, rec.Notify, "job %d", id)
		assert.Equal(t, sched.NoticeNone, rec.Notice, "job %d: no message, not even an undelivered one", id)
	}
	root := wqRecord(t, m, 1)
	assert.Equal(t, sched.Running, root.State, "gateQueue ends a running job")
	assert.Equal(t, "because the session was paused", root.CancelReason)

	wqEnd(t, m, 1, sched.Cancelled, nil)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 1).Notice)
}

func TestJobQueue_MoveToFrontCarriesTheJobsHeldAfterIt(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	wqSubmit(t, m, "sleep", "30") // job 1 runs
	m.Drain()
	x := wqSubmit(t, m, "true") // job 2, queued
	y := wqSubmit(t, m, "true") // job 3, queued
	b := wqSubmitAfter(t, m, y, "true").ID
	require.Equal(t, int64(3), wqRecord(t, m, b).Order)

	require.NoError(t, m.moveJobToFront(y))
	assert.Equal(t, int64(1), wqRecord(t, m, y).Order, "one below the lowest waiting order, job 2's")
	assert.Equal(t, int64(1), wqRecord(t, m, b).Order, "the job held after it moves with it")

	require.NoError(t, m.moveJobToFront(b))
	assert.Equal(t, int64(0), wqRecord(t, m, b).Order, "a held job moves to the front too")
	assert.Equal(t, int64(2), wqRecord(t, m, x).Order)
}

func TestJobQueue_ADependentsFirstLineNamesItsPrerequisite(t *testing.T) {
	r := wqEnded(15, 0)
	r.Description, r.After, r.Ready = "run tests", 14, r.Started.Add(-2*time.Second)
	assert.Equal(t, `[loom] job 15 "run tests" finished: exit 0 after 3m10s (2s in the queue, after job 14 "build tests", done in 1m0s).`,
		noticeHead(r, `after job 14 "build tests", done in 1m0s`))
}

func TestJobQueue_ADependentThatPeakedOverTwiceItsClaimIsToldSo(t *testing.T) {
	rec := sched.Record{After: 14, Claim: sched.Claim{CPUs: 2, MemoryGB: 2}, PeakRSSKB: 6_400_000}
	assert.Equal(t, "It peaked at 6.1 GB on a 2 GB claim: if it rebuilt what job 14 built, run job 14's output instead.", overClaimLine(rec))
	rec.PeakRSSKB, rec.Exit = 1_000_000, &sched.Exit{MaxRSSKB: 6_400_000}
	assert.NotEmpty(t, overClaimLine(rec), "the larger of the sampled peak and the max RSS")
	rec.Exit.MaxRSSKB = 4 << 20
	assert.Empty(t, overClaimLine(rec), "twice its claim is not over twice")
	rec.Exit.MaxRSSKB = 6_400_000
	rec.After = 0
	assert.Empty(t, overClaimLine(rec), "only a dependent")
	rec.After, rec.Claim = 14, sched.Claim{Exclusive: true}
	assert.Empty(t, overClaimLine(rec), "an exclusive claim has every GB")
}

// wqChainOwner is a session with a live mod on launch L1, for the chain's
// messages, and seed, which adds a record of that owner.
func wqChainOwner(t *testing.T, m *Model) (seed func(r sched.Record, desc string, notify bool)) {
	t.Helper()
	inst := modInst(t, m, "owner")
	owner := uint64(m.idOf(inst))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	return func(r sched.Record, desc string, notify bool) {
		r.Owner, r.Notify, r.Description = owner, notify, desc
		wqSeed(t, m, r)
	}
}

func TestJobQueue_AChainThatSucceedsSendsOneMessage(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	wqPaused(t, m)
	seed := wqChainOwner(t, m)
	build := wqRunning(t, 1)
	build.Started = time.Now().Add(-190 * time.Second)
	seed(build, "build tests", true)
	seed(wqHeld(t, 2, 1, 1), "run tests", true)

	wqEnd(t, m, 1, sched.Done, &sched.Exit{Code: 0})
	wqPump(t, m)

	got := wqRecord(t, m, 1)
	assert.Equal(t, sched.NoticeCarried, got.Notice, "its message goes with the dependent's")
	assert.Equal(t, "its result goes with job 2's", got.NoticeWhy)
	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	assert.Empty(t, msgs, "no message for the build")

	// The dependent runs, and ends.
	i := m.queue.index(2)
	rec := &m.queue.store.Jobs[i]
	require.Equal(t, sched.Queued, rec.State, "released")
	rec.State, rec.Started = sched.Running, rec.Ready.Add(2*time.Second)
	rec.Ended = rec.Started.Add(9*time.Minute + 12*time.Second)
	wqEnd(t, m, 2, sched.Done, &sched.Exit{Code: 0, Ended: rec.Ended})
	wqPump(t, m)

	msgs, _, err = m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	head, _, _ := strings.Cut(msgs[0].Text, "\n")
	assert.Equal(t, `[loom] job 2 "run tests" finished: exit 0 after 9m12s (2s in the queue, after job 1 "build tests", done in 3m10s).`, head)
}

func TestJobQueue_AFailedJobsMessageNamesTheChainItCancelled(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	wqPaused(t, m)
	seed := wqChainOwner(t, m)
	seed(wqRunning(t, 1), "build tests", true)
	seed(wqHeld(t, 2, 1, 1), "run tests", true)
	seed(wqHeld(t, 3, 2, 1), "bench", true)

	wqEnd(t, m, 1, sched.Failed, &sched.Exit{Code: 101})
	wqPump(t, m)

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1, "one message for the chain")
	lines := strings.Split(msgs[0].Text, "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.True(t, strings.HasPrefix(lines[0], `[loom] job 1 "build tests" finished: exit 101 after `), lines[0])
	assert.Equal(t, `Jobs 2 "run tests" and 3 "bench", which waited on it, were cancelled.`, lines[1])
	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.NoticeCarried, rec.Notice, "job %d", id)
		assert.Equal(t, "its result goes with job 1's", rec.NoticeWhy, "job %d", id)
	}
}

// A build followed with `run` sends no message of its own: the first job
// down each branch that asked for one reports the cascade, with the build's
// log.
func TestJobQueue_AQuietJobsCascadeIsReportedOncePerBranch(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	wqPaused(t, m)
	seed := wqChainOwner(t, m)
	seed(wqRunning(t, 1), "build", false)
	seed(wqHeld(t, 2, 1, 1), "unit", true)
	seed(wqHeld(t, 3, 2, 1), "bench", true)
	seed(wqHeld(t, 4, 1, 1), "lint", false)
	seed(wqHeld(t, 5, 4, 1), "docs", true)
	logPath := filepath.Join(m.queue.jobDir(1), sched.LogFile)
	require.NoError(t, os.WriteFile(logPath, []byte("error[E0308]: mismatched types\n"), 0o600))

	wqEnd(t, m, 1, sched.Failed, &sched.Exit{Code: 101})
	wqPump(t, m)

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 2, "job 2's and job 5's")
	tail := "Log: " + logPath + " (1 line). Last 1 line:\n\nerror[E0308]: mismatched types"
	assert.Equal(t, `[loom] job 2 "unit" was cancelled because job 1 "build" failed (exit 101).`+"\n"+
		`Job 3 "bench", which waited on it, was cancelled.`+"\n"+tail, msgs[0].Text)
	assert.Equal(t, `[loom] job 5 "docs" was cancelled because job 1 "build" failed (exit 101).`+"\n"+tail, msgs[1].Text)
	assert.Equal(t, sched.NoticeCarried, wqRecord(t, m, 3).Notice, "job 2's message reports it")
	assert.Equal(t, "its result goes with job 2's", wqRecord(t, m, 3).NoticeWhy)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 1).Notice, "it asked for no message")
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 4).Notice, "nor did it")
}

func TestJobQueue_ABootResolvesTheHeldJobsAnOlderDaemonLeft(t *testing.T) {
	dir := t.TempDir()
	// Admission paused, so nothing here starts and a release stays queued.
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"scheduler": {"cpus": -1}}`), 0o644))
	held := func(id, after sched.JobID) sched.Record { return wqHeld(t, id, after, int64(id)) }
	failed := wqEnded(3, 0)
	failed.State, failed.Exit = sched.Failed, &sched.Exit{Code: 2}
	paused := held(9, 1)
	paused.CancelReason = "because the session was paused"
	store := sched.Store{Version: sched.StoreVersion, NextID: 13, Jobs: []sched.Record{
		wqEnded(1, 0),
		held(2, 1),
		failed,
		held(4, 3),
		held(5, 4),
		held(6, 0),  // an older loom's save dropped its after
		held(7, 99), // names no record
		paused,
		held(11, 12),
		wqQueuedRecord(t, 12),
	}}
	require.NoError(t, sched.SaveStore(filepath.Join(dir, jobsDirName), store))

	m := wqModelAt(t, dir)

	released := wqRecord(t, m, 2)
	assert.Equal(t, sched.Queued, released.State, "its prerequisite is done")
	assert.False(t, released.Ready.IsZero())
	for id, reason := range map[sched.JobID]string{
		4: "because job 3 failed (exit 2)",
		5: "because job 4 was cancelled",
		6: "because its prerequisite is unknown",
		7: "because its prerequisite is unknown",
		9: "because the session was paused",
	} {
		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Cancelled, rec.State, "job %d", id)
		assert.Equal(t, reason, rec.CancelReason, "job %d", id)
	}
	assert.Equal(t, sched.Held, wqRecord(t, m, 11).State, "its prerequisite still waits")
	saved, err := sched.LoadStore(filepath.Join(dir, jobsDirName))
	require.NoError(t, err)
	assert.Equal(t, sched.Cancelled, saved.Jobs[3].State, "written")
}

func TestJobQueue_TheViewListsHeldJobsAmongTheWaiting(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	build := wqSubmit(t, m, "sleep", "30") // job 1 runs
	m.Drain()
	later := wqSubmit(t, m, "true") // job 2 waits
	tests := wqSubmitAfter(t, m, build, "true").ID

	jobs := m.Queue().Jobs
	require.Len(t, jobs, 3)
	assert.Equal(t, []sched.JobID{build, tests, later}, []sched.JobID{jobs[0].ID, jobs[1].ID, jobs[2].ID},
		"running, then the waiting jobs in queue order: the held one at its chain's place")
	assert.Equal(t, sched.Held, jobs[1].State)
	assert.Equal(t, build, jobs[1].After)
	assert.Equal(t, "after job 1 (running)", jobs[1].Waiting)
	assert.True(t, jobs[1].Ready.IsZero())
	assert.False(t, jobs[1].StartsAt.IsZero(), "the forecast releases it at its prerequisite's expected end")
	assert.Equal(t, jobs[2].Submitted, jobs[2].Ready)
}

func TestJobQueue_ADependentLearnsApartFromTheColdCommand(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	build := wqJob(t, "cargo", "test", "--no-run")
	buildID, _, err := m.submitJob(build)
	require.NoError(t, err)
	m.Drain()
	sub := wqJob(t, "cargo", "test")
	sub.Cwd, sub.After = build.Cwd, buildID
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)

	warm := wqSigOf(t, sub.Argv, sub.Cwd)
	cold := warm.ID()
	warm.After = wqRecord(t, m, buildID).Signature
	rec := wqRecord(t, m, id)
	assert.Equal(t, warm.ID(), rec.Signature, "its own belief")
	assert.NotEqual(t, cold, rec.Signature, "apart from the cold command's")
	b := m.queue.beliefs.Find(rec.Signature)
	require.NotNil(t, b, "the submit made its belief")

	b.AddRun(sched.Run{Outcome: sched.OutcomeDone, PeakKB: 5 << 20, WallSeconds: 30})
	m.admitJobs()
	assert.Equal(t, 6, wqRecord(t, m, id).Claim.MemoryGB, "a held job's claim stays live: 5 GB plus its margin")

	page, err := m.readBeliefs(sched.BeliefQuery{ID: rec.Signature})
	require.NoError(t, err)
	require.Len(t, page.Beliefs, 1)
	assert.Equal(t, []string{"cargo", "test", "--no-run"}, page.Beliefs[0].After)
}

func TestAdvisor_ADependentsRequestNamesItsPrerequisite(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	build := wqJob(t, "sleep", "30")
	build.Description = "build tests"
	buildID, _, err := m.submitJob(build)
	require.NoError(t, err)
	sub := wqJob(t, "cargo", "test")
	sub.Cwd, sub.After = build.Cwd, buildID
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Equal(t, sched.Held, wqRecord(t, m, id).State)
	require.Equal(t, sched.SizingAsking, wqRecord(t, m, id).Sizing, "an unseen command asks while it is held")

	wqWait(t, "both priors", func() bool { wqPump(t, m); return len(wqCalls(t, calls)) >= 2 })
	assert.Contains(t, wqCalls(t, calls)[1], `"after":{"command":["sleep","30"],"description":"build tests"}`)
	assert.NotContains(t, wqCalls(t, calls)[0], `"after"`, "the build has none")
}

func TestJobQueue_EndedHowNamesEachEnd(t *testing.T) {
	r := wqEnded(14, 0)
	for _, c := range []struct {
		edit func(*sched.Record)
		want string
	}{
		{func(r *sched.Record) { r.State, r.Exit = sched.Failed, &sched.Exit{Code: 101} }, "failed (exit 101)"},
		{func(r *sched.Record) { r.State, r.Exit = sched.Failed, &sched.Exit{Code: -1, Signal: "SIGKILL"} }, "was killed by SIGKILL"},
		{func(r *sched.Record) { r.State, r.Exit, r.Failure = sched.Failed, nil, "its folder is gone" }, "could not start"},
		{func(r *sched.Record) { r.State = sched.Cancelled }, "was cancelled"},
		{func(r *sched.Record) { r.State, r.Exit = sched.Lost, nil }, "was lost"},
	} {
		x := r
		c.edit(&x)
		assert.Equal(t, c.want, endedHow(x), fmt.Sprint(c.want))
	}
}
