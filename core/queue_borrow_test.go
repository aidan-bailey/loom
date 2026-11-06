package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWQBorrow is newWQLadder's contained queue with 20 GB available, 15 GB
// of swap free, an 8-core 14 GB cap and borrowing on, its jobs claiming
// 12 GB each and using wqCgroupFiles' 768 MB.
func newWQBorrow(t *testing.T, jobs int) *wqLadder {
	t.Helper()
	l := newWQLadder(t, 20, jobs)
	prev := readSwapFree
	readSwapFree = func() (int64, error) { return 15 << 20, nil }
	t.Cleanup(func() { readSwapFree = prev })
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2, "borrow": true}`)
	for _, id := range l.ids {
		l.rec(t, id).Claim = sched.Claim{CPUs: 2, MemoryGB: 12}
	}
	wqPass(t, l.m)
	return l
}

func TestQueueBorrow_AJobBorrowsWhatAFirmJobLeaves(t *testing.T) {
	l := newWQBorrow(t, 1)
	sub := wqJob(t, "sleep", "30")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 6}
	id, _, err := l.m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, l.m, id)
	assert.Equal(t, sched.Running, rec.State, "12 + 6 GB of claims pass 14, but 0.9 + 6 GB used fits: %q", l.m.queue.waiting[id])
	assert.True(t, rec.Borrower)

	v := l.m.queueView()
	assert.Equal(t, int64(6<<20), v.BorrowedKB, "its 6 GB prediction is borrowed (memory borrowing spec §8)")
	for _, j := range v.Jobs {
		assert.Equal(t, j.ID == id, j.Borrower, "job %d", j.ID)
	}
}

func TestQueueBorrow_OffByDefault(t *testing.T) {
	l := newWQBorrow(t, 1)
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2}`)
	sub := wqJob(t, "sleep", "30")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 6}
	id, _, err := l.m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, l.m, id).State)
	assert.Contains(t, l.m.queue.waiting[id], "at its peak")
}

func TestQueueBorrow_TightnessRequeuesAYoungBorrowerQuietly(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	l.rec(t, id).Borrower = true
	l.setAvail(5) // under the 4 GB reserve plus 2 GB: tight
	wqPass(t, l.m)
	require.True(t, l.rec(t, id).GivingBack)
	kill, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "cgroup.kill"))
	require.NoError(t, err)
	assert.Equal(t, "1", string(kill))

	// The kernel's part: the scope's processes die.
	require.NoError(t, sched.SignalGroup(l.rec(t, id).PID, syscall.SIGKILL))
	l.setAvail(64)
	wqWait(t, "the give-back requeue", func() bool { wqPass(t, l.m); return l.rec(t, id).Attempt == 1 })
	rec := l.rec(t, id)
	assert.Equal(t, 0, rec.Requeues, "a give-back is no requeue toward MaxRequeues")
	assert.Equal(t, 1, rec.GaveBack)
	assert.True(t, rec.NoBorrow, "its next attempt runs firm only")
	assert.False(t, rec.Borrower)
	assert.Contains(t, rec.LastRequeue, "lent its memory back")
}

func TestQueueBorrow_TightnessLendsAnOlderBorrowerAndItResumes(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.Started = true, time.Now().Add(-10*time.Minute)
	l.write(t, id, map[string]string{"memory.reclaim": ""})
	l.setAvail(5)
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Freeze "+l.rec(t, id).Unit)
	require.False(t, l.rec(t, id).LentAt.IsZero())
	assert.True(t, l.rec(t, id).Laddered, "a lent run teaches floors only")
	data, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "memory.reclaim"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "swappiness=200")

	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Empty(t, l.rec(t, id).Rung, "lent, not the ladder's pause")
	assert.Empty(t, l.m.ladderHoldoff(), "a lent job holds no admission off")
	l.setAvail(3) // short: the ladder runs
	wqPass(t, l.m)
	for _, call := range l.fake.Calls() {
		assert.NotContains(t, call, "SetMemoryHigh "+l.rec(t, id).Unit, "the ladder leaves a lent job alone")
	}

	l.setAvail(64)
	l.rec(t, id).LentAt = time.Now().Add(-31 * time.Second)
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
	assert.True(t, l.rec(t, id).LentAt.IsZero())
	assert.Positive(t, l.rec(t, id).LentSecs)
}

func TestQueueBorrow_AFailedFreezeGivesBackByRequeue(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.Started = true, time.Now().Add(-10*time.Minute)
	l.fake.fail = map[string]error{"Freeze": errNoBusForTest}
	l.setAvail(5)
	wqPass(t, l.m)
	require.True(t, l.rec(t, id).LentAt.IsZero())
	assert.NotEmpty(t, l.m.queue.ladderErr)
	l.m.queue.giveBack.Last = time.Now().Add(-11 * time.Second)
	wqPass(t, l.m)
	assert.True(t, l.rec(t, id).GivingBack, "the next give-back is a requeue")
}

func TestQueueBorrow_ACancelThawsALentJob(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.LentAt, rec.CancelReason = true, time.Now().Add(-time.Minute), "by the test"
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
}

func TestQueueBorrow_ALentJobThawedByHandRunsAgain(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.LentAt = true, time.Now().Add(-time.Minute)
	wqPass(t, l.m) // its cgroup.events says it is not frozen
	assert.True(t, l.rec(t, id).LentAt.IsZero())
	assert.Positive(t, l.rec(t, id).LentSecs)
	assert.True(t, l.rec(t, id).Borrower)
}

func TestQueueBorrow_ABorrowerBecomesFirm(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.Claim = true, sched.Claim{CPUs: 2, MemoryGB: 6}
	wqSubmit(t, l.m, "sleep", "30") // a pass runs: 6 GB alone fits 14
	assert.False(t, l.rec(t, id).Borrower)
}

// TestConfigureBorrow_SetsOnlyThatKeyOfTheGlobalConfig: the request writes
// scheduler.borrow into the queue's global config.json, keeps every other
// key, and the policy follows (borrow setting spec).
func TestConfigureBorrow_SetsOnlyThatKeyOfTheGlobalConfig(t *testing.T) {
	m := wqModel(t)
	path := filepath.Join(m.queue.cfgDir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(`{"theme": "nord", "scheduler": {"cpus": 4, "cpu_overcommit": 3}}`), 0o644))

	require.NoError(t, m.ConfigureBorrow(true))
	cfg, err := config.ReadConfigFrom(m.queue.cfgDir)
	require.NoError(t, err)
	s := cfg.GetScheduler()
	require.NotNil(t, s.Borrow)
	assert.True(t, *s.Borrow)
	assert.Equal(t, 4, s.CPUs)
	assert.Equal(t, 3, s.CPUOvercommit)
	assert.Equal(t, "nord", cfg.GetTheme())
	assert.True(t, m.queue.policy.Borrow, "the policy follows")
	v := m.queueView().Settings.Borrow
	require.NotNil(t, v, "and the view")
	assert.True(t, *v)

	require.NoError(t, m.ConfigureBorrow(false))
	assert.False(t, m.queue.policy.Borrow)
}

func TestConfigureBorrow_RefusesAnUnreadableGlobalConfig(t *testing.T) {
	m := wqModel(t)
	path := filepath.Join(m.queue.cfgDir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	assert.ErrorContains(t, m.ConfigureBorrow(true), "configure borrowing")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data), "left byte for byte")
}

func TestConfigureBorrow_RefusesAQueueNotLoaded(t *testing.T) {
	m := NewForTest(Options{})
	assert.ErrorIs(t, m.ConfigureBorrow(true), errQueueNotLoaded)
}
