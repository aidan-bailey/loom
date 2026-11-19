package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqLadder is a contained queue (a fake confiner, a temp cgroup root) whose
// host has availGB of memory available, and jobs running sleep, each in its
// own fake scope under /t.slice, the later one started last.
type wqLadder struct {
	m    *Model
	fake *fakeConfiner
	root string
	ids  []sched.JobID
}

func newWQLadder(t *testing.T, availGB int64, jobs int, argv ...string) *wqLadder {
	t.Helper()
	prev := readMemAvailable
	avail := availGB << 20
	readMemAvailable = func() (int64, error) { return avail, nil }
	t.Cleanup(func() { readMemAvailable = prev })
	l := &wqLadder{m: wqLiveModel(t), fake: &fakeConfiner{}, root: t.TempDir()}
	l.m.queue.escalation = 300 * time.Millisecond
	l.m.SetConfinerForTest(l.fake)
	l.m.SetCgroupRootForTest(l.root)
	wqWriteCgroup(t, l.root, "/t.slice", wqCgroupFiles())
	if len(argv) == 0 {
		argv = []string{"sleep", "30"}
	}
	for i := range jobs {
		id := wqLaunch(t, l.m, argv...)
		rec := &l.m.queue.store.Jobs[l.m.queue.index(id)]
		rec.Unit = fmt.Sprintf("loom-ab-job-%d-1.scope", id)
		rec.Cgroup = "/t.slice/" + rec.Unit
		rec.Started = rec.Started.Add(time.Duration(i) * time.Second)
		wqWriteCgroup(t, l.root, rec.Cgroup, wqCgroupFiles())
		l.ids = append(l.ids, id)
	}
	return l
}

// setAvail changes what the host has available, in GB.
func (l *wqLadder) setAvail(gb int64) {
	readMemAvailable = func() (int64, error) { return gb << 20, nil }
}

// write sets files of job id's cgroup.
func (l *wqLadder) write(t *testing.T, id sched.JobID, files map[string]string) {
	t.Helper()
	wqWriteCgroup(t, l.root, wqRecord(t, l.m, id).Cgroup, files)
}

func (l *wqLadder) rec(t *testing.T, id sched.JobID) *sched.Record {
	t.Helper()
	return &l.m.queue.store.Jobs[l.m.queue.index(id)]
}

// wqWorkingBytes is wqCgroupFiles' working set in bytes.
const wqWorkingBytes = (1073741824 - 268435456)

func TestQueueLadder_SqueezesTheMostRecentlyStartedWhenShort(t *testing.T) {
	l := newWQLadder(t, 2, 2)
	a, b := l.ids[0], l.ids[1]
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s %d", l.rec(t, b).Unit, wqWorkingBytes))
	assert.Equal(t, sched.RungSqueezed, l.rec(t, b).Rung)
	assert.True(t, l.rec(t, b).Laddered)
	assert.Empty(t, l.rec(t, a).Rung, "one rung at a time")

	id := wqSubmit(t, l.m, "true")
	assert.Equal(t, fmt.Sprintf("memory is short (job %d squeezed)", b), l.m.queue.waiting[id])
}

// A job with no sample yet (its start landed after the pass now being
// delivered was dispatched) is no victim: squeezed at a working set of 0 it
// would get the 64 MiB floor and be pushed to swap. The sampled job is the
// victim instead.
func TestQueueLadder_AJobWithNoSampleIsNoVictim(t *testing.T) {
	l := newWQLadder(t, 2, 2)
	a, b := l.ids[0], l.ids[1]
	q := l.m.queue
	q.availKB = 2 << 20
	q.usage[a] = jobUsage{RSSKB: wqWorkingBytes >> 10}
	delete(q.usage, b)
	l.m.stepLadder(time.Now())
	wqPump(t, l.m)
	assert.Empty(t, l.rec(t, b).Rung, "the most recently started, but unsampled")
	assert.NotContains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s %d", l.rec(t, b).Unit, 64<<20))
	assert.Contains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s %d", l.rec(t, a).Unit, wqWorkingBytes))
}

// wqFrozen is a frozen scope's cgroup.events, wqThawed a thawed one's.
var (
	wqFrozen = map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"}
	wqThawed = map[string]string{"cgroup.events": "populated 1\nfrozen 0\n", "memory.high": "max"}
)

// Once containment goes (scheduler.contain turned off) neither the ladder
// nor the give-back machine runs, so what they hold (a pause, a lend) is
// restored, one job a pass, through the confiner still connected: a job
// left frozen would hold its claim for good.
func TestQueueLadder_ContainmentOffRestoresWhatTheLadderHeld(t *testing.T) {
	l := newWQLadder(t, 64, 2)
	a, b := l.ids[0], l.ids[1]
	ra := l.rec(t, a)
	ra.Rung, ra.RungAt, ra.Laddered = sched.RungPaused, time.Now().Add(-time.Minute), true
	l.write(t, a, wqFrozen)
	rb := l.rec(t, b)
	rb.Borrower, rb.LentAt, rb.Laddered = true, time.Now().Add(-time.Minute), true
	l.write(t, b, wqFrozen)
	wqConfig(t, l.m, `{"contain": false}`)
	l.m.refreshPolicy()

	wqWait(t, "both thawed", func() bool {
		wqPass(t, l.m)
		for _, id := range l.ids {
			if slices.Contains(l.fake.Calls(), "Thaw "+l.rec(t, id).Unit) {
				l.write(t, id, wqThawed) // the kernel's part
			}
		}
		return l.rec(t, a).Rung == "" && l.rec(t, b).LentAt.IsZero()
	})
	assert.Contains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s 0", l.rec(t, a).Unit))
}

// A confiner dropped after a failed call is probed for again while the
// ladder holds a job, though nothing is queued: only a confiner can thaw it.
func TestQueueLadder_ALostConfinerIsProbedWhileTheLadderHoldsAJob(t *testing.T) {
	l := newWQLadder(t, 64, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Rung, rec.RungAt, rec.Laddered = sched.RungPaused, time.Now().Add(-time.Minute), true
	l.write(t, id, wqFrozen)
	l.m.queue.confiner, l.m.queue.confineErr = nil, errNoBusForTest
	prev := newConfiner
	newConfiner = func(context.Context) (sched.Confiner, error) { return l.fake, nil }
	t.Cleanup(func() { newConfiner = prev })
	require.False(t, l.m.queue.hasQueued())
	l.m.queue.lastProbe = time.Time{} // the last probe was longer ago than probeEvery
	wqPass(t, l.m)
	assert.NotNil(t, l.m.queue.confiner, "probed with nothing queued")
}

func TestQueueLadder_PausesThenRequeues(t *testing.T) {
	l := newWQLadder(t, 2, 1)
	id := l.ids[0]
	wqPass(t, l.m)
	require.Equal(t, sched.RungSqueezed, l.rec(t, id).Rung)
	l.write(t, id, map[string]string{"memory.high": fmt.Sprint(wqWorkingBytes)})

	l.rec(t, id).RungAt = time.Now().Add(-16 * time.Second)
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Freeze "+l.rec(t, id).Unit)
	require.Equal(t, sched.RungPaused, l.rec(t, id).Rung)
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})

	l.rec(t, id).RungAt = time.Now().Add(-31 * time.Second)
	wqPass(t, l.m)
	require.NotEmpty(t, l.rec(t, id).Requeue)
	kill, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "cgroup.kill"))
	require.NoError(t, err)
	assert.Equal(t, "1", string(kill))

	// The kernel's part: the scope's processes die.
	require.NoError(t, sched.SignalGroup(l.rec(t, id).PID, syscall.SIGKILL))
	l.setAvail(64)
	wqWait(t, "the requeue", func() bool { wqPass(t, l.m); return l.rec(t, id).Attempt == 1 })
	rec := l.rec(t, id)
	assert.Equal(t, 1, rec.Requeues)
	assert.Contains(t, rec.LastRequeue, "memory ran short")
	assert.FileExists(t, filepath.Join(l.m.queue.jobDir(id), "attempt-1", sched.LogFile))

	wqWait(t, "the next attempt to start", func() bool {
		wqPump(t, l.m)
		spec, err := sched.ReadSpec(l.m.queue.jobDir(id))
		return err == nil && spec.Scope != nil && spec.Scope.Unit == sched.ScopeName(l.m.queue.dir, id, 1)
	})
}

func TestQueueLadder_NoRequeueStopsAtPause(t *testing.T) {
	l := newWQLadder(t, 2, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.NoRequeue, rec.Rung, rec.RungAt, rec.Laddered = true, sched.RungPaused, time.Now().Add(-time.Minute), true
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Empty(t, l.rec(t, id).Requeue, "a --no-requeue job stays paused")
	kill, _ := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "cgroup.kill"))
	assert.Empty(t, kill)
}

func TestQueueLadder_RecoveryRestoresOldestFirst(t *testing.T) {
	l := newWQLadder(t, 64, 2)
	a, b := l.ids[0], l.ids[1]
	for _, id := range l.ids {
		rec := l.rec(t, id)
		rec.Rung, rec.RungAt, rec.Laddered = sched.RungSqueezed, time.Now().Add(-time.Minute), true
		l.write(t, id, map[string]string{"memory.high": fmt.Sprint(wqWorkingBytes)})
	}
	wqPass(t, l.m)
	assert.NotContains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s 0", l.rec(t, a).Unit), "calm for 30 s first")
	l.m.queue.ladder.CalmSince = time.Now().Add(-31 * time.Second)
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s 0", l.rec(t, a).Unit), "the older job first")
	assert.NotContains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s 0", l.rec(t, b).Unit))
}

func TestQueueLadder_AKernelOOMKillRequeues(t *testing.T) {
	for _, noRequeue := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-requeue %v", noRequeue), func(t *testing.T) {
			l := newWQLadder(t, 64, 1)
			id := l.ids[0]
			l.rec(t, id).NoRequeue = noRequeue
			wqPass(t, l.m)
			require.NotNil(t, l.m.queue.slice)
			require.NoError(t, sched.SignalGroup(l.rec(t, id).PID, syscall.SIGKILL))
			dir := l.m.queue.jobDir(id)
			wqWait(t, "the shim to go", func() bool { alive, _ := sched.Probe(dir); return !alive })
			wqWriteCgroup(t, l.root, "/t.slice", map[string]string{"memory.events": "oom_kill 1\n"})
			// A requeued job may start its next attempt in the same pass.
			wqWait(t, "the settle", func() bool {
				wqPass(t, l.m)
				return l.rec(t, id).Attempt == 1 || l.rec(t, id).State.Finished()
			})
			rec := l.rec(t, id)
			if noRequeue {
				// It ran and was killed, the shim with it: not a job that
				// could not start, and its run teaches a floor.
				assert.Equal(t, sched.Failed, rec.State)
				assert.Equal(t, "the kernel killed it: out of memory", rec.Failure)
				if assert.NotNil(t, rec.Exit) {
					assert.Equal(t, "SIGKILL", rec.Exit.Signal)
				}
				assert.False(t, startFailed(rec.State, rec.Failure, rec.Exit))
				assert.Contains(t, noticeHead(*rec, ""), "was killed by SIGKILL (the kernel killed it: out of memory) after ")
				run, ok := sched.RunOf(*rec)
				assert.True(t, ok, "a kill for memory teaches a floor")
				assert.Equal(t, sched.OutcomeSignal, run.Outcome)
				return
			}
			assert.Equal(t, 1, rec.Attempt)
			assert.Equal(t, "the kernel killed it: out of memory", rec.LastRequeue)
		})
	}
}

// A requeue asked for while the job was ending loses to the exit.json of
// an end of its own: a job that finished before its kill landed is done,
// never run again.
func TestQueueLadder_AnEndOfItsOwnWinsOverARequeue(t *testing.T) {
	l := newWQLadder(t, 64, 1, "sh", "-c", "exit 0")
	id := l.ids[0]
	l.rec(t, id).Requeue = "memory ran short (1.0 GB free)"
	rec := wqSettle(t, l.m, id)
	assert.Equal(t, sched.Done, rec.State)
	assert.Zero(t, rec.Attempt, "not requeued")
}

// A requeued job's starvation wait starts at its requeue: counted from its
// submit it would be a starving head at once, holding every other job back
// behind a job that was just put back for want of memory.
func TestQueueLadder_ARequeuedJobIsNoStarvingHeadAtOnce(t *testing.T) {
	m := wqLiveModel(t)
	busy := wqSubmit(t, m, "sleep", "30") // 4 of the 14 cores
	require.Equal(t, sched.Running, wqRecord(t, m, busy).State)
	long := time.Now().Add(-25 * time.Minute)
	rec := wqEnded(50, 0)
	rec.State, rec.Exit, rec.Ended, rec.Notify = sched.Running, nil, time.Time{}, false
	rec.Submitted, rec.Ready, rec.Started = long, long, long.Add(time.Minute)
	rec.Claim = sched.Claim{CPUs: 12, MemoryGB: 4} // no room beside the busy job
	wqSeed(t, m, rec)
	m.requeueJob(m.queue.index(rec.ID), "memory ran short", time.Now())
	require.Equal(t, sched.Queued, wqRecord(t, m, rec.ID).State)

	small := wqJob(t, "sleep", "30")
	small.Claim = sched.Claim{CPUs: 1, MemoryGB: 1}
	id, _, err := m.submitJob(small)
	require.NoError(t, err)
	assert.Equal(t, sched.Running, wqRecord(t, m, id).State, "the requeued job has waited no 10m: %q", m.queue.waiting[id])
}

// requeueKept, requeueSet and requeueReset place every field of
// sched.Record for a requeue (requeueJob): kept for the next attempt, set by
// the requeue, or reset as a new attempt's. A field missing from all three
// fails TestQueueLadder_ARequeueKeepsOrResetsEveryField until it is placed:
// one dropped by mistake loses what the job is (a flow run's mark lets its
// owner's pause cancel it, a lost key lets a resent gate queue twice).
var (
	requeueKept = []string{"ID", "Order", "Owner", "Agent", "Description", "Argv", "Cwd", "Notify", "Tail", "Submitted",
		"Asked", "Signature", "Checkout", "Unsized", "Sources", "Learned", "FamilyRuns", "AboveCap", "BelowKB", "AgentWhy",
		"After", "Ready", "NoRequeue", "Kind", "AccountKey", "Run", "Locks", "Key"}
	requeueSet   = []string{"Claim", "State", "Attempt", "Requeues", "LastRequeue", "GaveBack", "NoBorrow", "Requeued"}
	requeueReset = []string{"Started", "Ended", "PID", "Exit", "Failure", "PeakRSSKB", "CancelReason", "SignalledAt", "Notice",
		"NoticeWhy", "Sizing", "Adopted", "Spare", "Explored", "HoldBroke", "Unit", "Cgroup", "Uncontained", "Rung", "RungAt",
		"Laddered", "Requeue", "Starved", "Borrower", "LentAt", "LentSecs", "GivingBack", "Forced", "Parked", "Unparked",
		"PausedAt", "PausedSecs", "PauseReclaimed", "Control", "ControlErr"}
)

// wqFill gives every field of v (a struct, through pointers, slices and
// nested structs) a value of its own that is not zero.
func wqFill(v reflect.Value, n *int) {
	*n++
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Date(2026, 1, 1, 0, 0, *n, 0, time.UTC)))
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				wqFill(v.Field(i), n)
			}
		}
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		wqFill(v.Elem(), n)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		wqFill(v.Index(0), n)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		wqFill(k, n)
		wqFill(e, n)
		v.SetMapIndex(k, e)
	case reflect.String:
		v.SetString(fmt.Sprintf("v%d", *n))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64, reflect.Int32:
		v.SetInt(int64(*n))
	case reflect.Uint, reflect.Uint64, reflect.Uint32:
		v.SetUint(uint64(*n))
	case reflect.Float64, reflect.Float32:
		v.SetFloat(float64(*n) + 0.5)
	}
}

func TestQueueLadder_ARequeueKeepsOrResetsEveryField(t *testing.T) {
	placed := map[string]string{}
	for list, names := range map[string][]string{"kept": requeueKept, "set": requeueSet, "reset": requeueReset} {
		for _, name := range names {
			require.Empty(t, placed[name], "%s is both %s and %s", name, placed[name], list)
			placed[name] = list
		}
	}
	typ := reflect.TypeFor[sched.Record]()
	for i := range typ.NumField() {
		assert.NotEmpty(t, placed[typ.Field(i).Name], "sched.Record.%s: is it kept, set or reset by a requeue (requeueJob)?", typ.Field(i).Name)
	}
	for name := range placed {
		_, ok := typ.FieldByName(name)
		assert.True(t, ok, "%s is no field of sched.Record", name)
	}

	m := wqModel(t)
	var before sched.Record
	n := 100
	wqFill(reflect.ValueOf(&before).Elem(), &n)
	wqSeed(t, m, before)
	now := time.Now()
	m.requeueJob(m.queue.index(before.ID), "memory ran short", now)
	after := wqRecord(t, m, before.ID)

	b, a := reflect.ValueOf(before), reflect.ValueOf(after)
	for _, name := range requeueKept {
		if f := b.FieldByName(name); f.IsValid() {
			assert.Equal(t, f.Interface(), a.FieldByName(name).Interface(), "%s is kept", name)
		}
	}
	for _, name := range requeueReset {
		if f := a.FieldByName(name); f.IsValid() {
			assert.True(t, f.IsZero(), "%s is reset, not %v", name, f.Interface())
		}
	}
	// A give-back requeue (GivingBack is set): it counts toward GaveBack.
	claim := before.Claim
	claim.CPUs -= before.Spare.CPUs
	claim.MemoryGB -= before.Spare.MemoryGB
	assert.Equal(t, claim, after.Claim, "the claim less its spare cores and GB")
	assert.Equal(t, sched.Queued, after.State)
	assert.Equal(t, before.Attempt+1, after.Attempt)
	assert.Equal(t, before.Requeues, after.Requeues)
	assert.Equal(t, before.GaveBack+1, after.GaveBack)
	assert.Equal(t, "memory ran short", after.LastRequeue)
	assert.True(t, after.NoBorrow)
	assert.Equal(t, now, after.Requeued, "its starvation wait starts again")
}

func TestQueueLadder_ReconcilesFromTheCgroup(t *testing.T) {
	l := newWQLadder(t, 64, 1)
	id := l.ids[0]
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Equal(t, sched.RungPaused, l.rec(t, id).Rung, "frozen is paused, whoever froze it")
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 0\n", "memory.high": "1073741824\n"})
	wqPass(t, l.m)
	assert.Equal(t, sched.RungSqueezed, l.rec(t, id).Rung)
	l.write(t, id, map[string]string{"memory.high": "max\n"})
	wqPass(t, l.m)
	assert.Empty(t, l.rec(t, id).Rung)
}

func TestQueueLadder_ACancelThawsAPausedJob(t *testing.T) {
	l := newWQLadder(t, 64, 1, "sh", "-c", `trap "" TERM; while :; do sleep 0.1; done`)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Rung, rec.RungAt, rec.Laddered = sched.RungPaused, time.Now(), true
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	require.NoError(t, l.m.cancelJob(id, "with loom job cancel"))
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+rec.Unit)
	assert.Contains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s 0", rec.Unit))
}

func TestQueueLadder_FailedCallsChangeNoRung(t *testing.T) {
	l := newWQLadder(t, 2, 1)
	id := l.ids[0]
	l.fake.fail = map[string]error{"Freeze": fmt.Errorf("no freezer")}
	rec := l.rec(t, id)
	rec.Rung, rec.RungAt, rec.Laddered = sched.RungSqueezed, time.Now().Add(-16*time.Second), true
	l.write(t, id, map[string]string{"memory.high": fmt.Sprint(wqWorkingBytes)})
	wqPass(t, l.m)
	assert.Equal(t, sched.RungSqueezed, l.rec(t, id).Rung)
	assert.Contains(t, l.m.queueView().LadderErr, "no freezer")
}

func TestQueueLadder_TheSliceFollowsTheBudget(t *testing.T) {
	l := newWQLadder(t, 64, 1)
	wqPass(t, l.m)
	budget := sched.Budget(l.m.queue.usage[l.ids[0]].RSSKB, 64<<20, l.m.queue.policy)
	want := fmt.Sprintf("SetMemoryHigh %s %d", sched.SliceName(l.m.queue.dir), uint64(budget)<<10)
	assert.Contains(t, l.fake.Calls(), want)
	n := len(l.fake.Calls())
	wqPass(t, l.m)
	assert.Len(t, l.fake.Calls(), n, "not again until the budget moves 256 MiB")
}

// The slice's MemoryHigh follows the budget the fit uses, so an agent or run
// job's memory, which runs outside the slice and the fit, raises it no more
// than it raises the budget.
func TestQueueLadder_TheSliceLeavesAgentAndRunJobsOut(t *testing.T) {
	l := newWQLadder(t, 64, 1)
	q := l.m.queue
	q.availKB = 10 << 20
	q.usage[l.ids[0]] = jobUsage{RSSKB: 1 << 20}
	wqUnclaimedRunning(t, l.m, 3)
	l.m.maybeSetSliceHigh()
	wqPump(t, l.m)
	budget := sched.Budget(1<<20, 10<<20, q.policy)
	require.Less(t, budget+6<<20, int64(q.policy.Caps.MemoryGB)*kbPerGB, "below the cap, so 6 GB more would show")
	assert.Contains(t, l.fake.Calls(), fmt.Sprintf("SetMemoryHigh %s %d", sched.SliceName(q.dir), uint64(budget)<<10))
}
