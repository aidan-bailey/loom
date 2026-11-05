package core

import (
	"fmt"
	"os"
	"path/filepath"
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
				assert.Equal(t, sched.Failed, rec.State)
				assert.Equal(t, "killed: the kernel killed it: out of memory", rec.Failure)
				return
			}
			assert.Equal(t, 1, rec.Attempt)
			assert.Equal(t, "the kernel killed it: out of memory", rec.LastRequeue)
		})
	}
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
