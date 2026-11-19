package core

import (
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errNoBusForTest stands for a probe that failed.
var errNoBusForTest = errors.New("no bus in this test")

// wqCurve is a run's curve: memory gb1 GB until progress x1 (wall w1 s),
// then gb2 GB to its end at x2 (wall w2 s).
func wqCurve(gb1 int64, x1, w1 float64, gb2 int64, x2, w2 float64) *sched.Curve {
	return &sched.Curve{End: x2,
		Mem:   []sched.CurveStep{{Until: x1, Level: gb1 << 20, Wall: w1}, {Until: x2, Level: gb2 << 20, Wall: w2}},
		Cores: []sched.CurveStep{{Until: x2, Level: 1000, Wall: w2}}}
}

// wqCurved gives sig's belief three done runs of curve c, peaking at peakGB.
func wqCurved(m *Model, sig sched.Signature, c *sched.Curve, peakGB int64) {
	b := m.queue.beliefs.Ensure(sig, time.Now())
	for range 3 {
		b.AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 2, MemoryGB: int(peakGB)}, PeakKB: peakGB << 20,
			CPUSeconds: c.End, WallSeconds: c.Mem[len(c.Mem)-1].Wall, Curve: c})
	}
}

// wqGreedyModel is a contained queue (a fake confiner) on a 14 GB cap with
// 20 GB available, and job A running in a cgroup past an early 10 GB peak:
// its curve says 1 GB from here on.
func wqGreedyModel(t *testing.T) (*Model, sched.JobID) {
	t.Helper()
	prev := readMemAvailable
	readMemAvailable = func() (int64, error) { return 20 << 20, nil }
	t.Cleanup(func() { readMemAvailable = prev })
	m := wqLiveModel(t)
	m.SetConfinerForTest(&fakeConfiner{})
	wqConfig(t, m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2}`)
	id := wqLaunch(t, m, "sleep", "30")
	rec := &m.queue.store.Jobs[m.queue.index(id)]
	sig := wqSigOf(t, rec.Argv, rec.Cwd)
	wqCurved(m, sig, wqCurve(10, 50, 30, 1, 1000, 600), 10)
	rec.Signature, rec.Cgroup, rec.Claim = sig.ID(), "/t.slice/loom-ab-job-1-1.scope", sched.Claim{CPUs: 2, MemoryGB: 12}
	now := time.Now()
	m.queue.traces[id] = &sched.Trace{Points: []sched.TracePoint{{X: 100, RSSKB: 1 << 20, At: now}}, Last: now}
	m.queue.usage[id] = jobUsage{RSSKB: 1 << 20}
	return m, id
}

func TestQueueGreedy_AdmitsWhatClaimsWouldHoldBack(t *testing.T) {
	m, _ := wqGreedyModel(t)
	sub := wqJob(t, "sh", "-c", "sleep 30")
	// Its command uses 1 GB for 5 minutes, then 10 GB: its peak comes long
	// after job A's.
	wqCurved(m, wqSigOf(t, sub.Argv, sub.Cwd), wqCurve(1, 100, 300, 10, 200, 400), 10)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.Running, rec.State, "12 + 12 GB of claims on a 14 GB cap, but the peaks don't meet: %q", m.queue.waiting[id])
}

func TestQueueGreedy_WaitsWithTheForecastsReason(t *testing.T) {
	m, a := wqGreedyModel(t)
	m.queue.traces[a].Points[0].X = 10 // before its 10 GB peak
	sub := wqJob(t, "sh", "-c", "sleep 30")
	wqCurved(m, wqSigOf(t, sub.Argv, sub.Cwd), wqCurve(10, 100, 60, 1, 200, 120), 10)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, m, id).State)
	assert.Contains(t, m.queue.waiting[id], "at its peak (forecast leaves")
}

func TestQueueGreedy_AJobNoEmptyQueueCouldTakeHoldsNoOneBack(t *testing.T) {
	m, _ := wqGreedyModel(t)
	// 10 GB available: job A's 1 GB plus 10, less the 4 GB reserve, is a
	// 7 GB budget, which job B's 10 GB peak passes whatever A does.
	prev := readMemAvailable
	readMemAvailable = func() (int64, error) { return 10 << 20, nil }
	t.Cleanup(func() { readMemAvailable = prev })
	sub := wqJob(t, "sh", "-c", "sleep 30")
	wqCurved(m, wqSigOf(t, sub.Argv, sub.Cwd), wqCurve(10, 100, 60, 1, 200, 120), 10)
	b, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := &m.queue.store.Jobs[m.queue.index(b)]
	require.Equal(t, sched.Queued, rec.State)
	rec.Submitted, rec.Ready = time.Now().Add(-11*time.Minute), time.Now().Add(-11*time.Minute)

	small := wqJob(t, "sh", "-c", "sleep 31")
	small.Claim = sched.Claim{CPUs: 1, MemoryGB: 1}
	c, _, err := m.submitJob(small)
	require.NoError(t, err)
	assert.Equal(t, sched.Running, wqRecord(t, m, c).State, "job B has starved, but no job's end could make room for it: %q", m.queue.waiting[c])
	assert.Contains(t, m.queue.waiting[b], "with no job running")
}

// wqUnclaimedRunning seeds a running agent job and a running run job, each
// sampled at gb GB: the jobs the fit never counts (sched.Unclaimed).
func wqUnclaimedRunning(t *testing.T, m *Model, gb int64) {
	t.Helper()
	for i, kind := range []string{sched.KindAgent, sched.KindRun} {
		id := m.queue.store.NextID
		wqSeed(t, m, sched.Record{ID: id, Order: int64(id), Kind: kind, Description: kind, Argv: []string{"claude"}, Cwd: "/work",
			State: sched.Running, Submitted: time.Now(), Started: time.Now(), PID: 5000 + i})
		m.queue.usage[id] = jobUsage{RSSKB: gb << 20}
	}
}

// The fit counts no agent or run job (sched.Unclaimed), so their memory is
// no room either: the budget is the running jobs the fit counts plus what
// the host has available, never what an agent holds.
func TestQueueGreedy_AgentAndRunJobsMemoryIsNoRoom(t *testing.T) {
	m, _ := wqGreedyModel(t)
	// Job A's 1 GB plus 10 GB available, less the reserve: below the 14 GB
	// cap, so 6 GB more would show.
	prev := readMemAvailable
	readMemAvailable = func() (int64, error) { return 10 << 20, nil }
	t.Cleanup(func() { readMemAvailable = prev })
	g, ok := m.greedyInputs(m.queue.policy, time.Now())
	require.True(t, ok)
	want := g.BudgetKB
	require.Less(t, want+6<<20, int64(m.queue.policy.Caps.MemoryGB)*kbPerGB)

	wqUnclaimedRunning(t, m, 3)
	g, ok = m.greedyInputs(m.queue.policy, time.Now())
	require.True(t, ok)
	assert.Equal(t, want, g.BudgetKB, "the agents' 6 GB is taken, not free")
}

func TestQueueGreedy_FallbackAdmitsByClaims(t *testing.T) {
	m, _ := wqGreedyModel(t)
	m.queue.confiner = nil
	m.queue.confineErr = errNoBusForTest
	sub := wqJob(t, "sh", "-c", "sleep 30")
	wqCurved(m, wqSigOf(t, sub.Argv, sub.Cwd), wqCurve(1, 100, 300, 10, 200, 400), 10)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, m, id).State)
	assert.Contains(t, m.queue.waiting[id], "GB (2 free)", "claims: 12 of 14 GB taken")
}

func TestQueueGreedy_ARunningJobAboveItsPredictionCountsAtItsUse(t *testing.T) {
	m, a := wqGreedyModel(t)
	m.queue.usage[a] = jobUsage{RSSKB: 9 << 20} // far above the 1 GB its curve says
	sub := wqJob(t, "sh", "-c", "sleep 30")
	wqCurved(m, wqSigOf(t, sub.Argv, sub.Cwd), wqCurve(1, 100, 300, 10, 200, 400), 10)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, m, id).State, "9 GB × 1.15 measured plus its 11.5 GB peak passes the budget")
}

// wqPeaks gives sig's belief one done run per peak, in GB, with no curves.
func wqPeaks(m *Model, sig sched.Signature, peaksGB ...int64) {
	b := m.queue.beliefs.Ensure(sig, time.Now())
	for _, p := range peaksGB {
		b.AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 2, MemoryGB: 8}, PeakKB: p << 20, CPUSeconds: 60, WallSeconds: 60})
	}
}

// wqUsualModel is wqGreedyModel (a 14 GB budget) with job A on an 8 GB
// claim and no curves, whose command usually peaks at 1 GB, and a submit
// whose command peaks at 7 GB but usually at 2 (a 9 GB claim).
func wqUsualModel(t *testing.T) (*Model, sched.JobID, sched.Submit) {
	t.Helper()
	m, a := wqGreedyModel(t)
	rec := &m.queue.store.Jobs[m.queue.index(a)]
	sig := wqSigOf(t, []string{"sleep", "31"}, rec.Cwd)
	wqPeaks(m, sig, 1, 7, 1)
	rec.Signature, rec.Claim = sig.ID(), sched.Claim{CPUs: 2, MemoryGB: 8}
	delete(m.queue.traces, a)
	sub := wqJob(t, "sh", "-c", "sleep 30")
	wqPeaks(m, wqSigOf(t, sub.Argv, sub.Cwd), 2, 7, 2)
	return m, a, sub
}

func TestQueueGreedy_CountsTheUsualRunsPlusOneWorst(t *testing.T) {
	m, _, sub := wqUsualModel(t)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, m, id)
	assert.Equal(t, 9, rec.Claim.MemoryGB)
	assert.Equal(t, sched.Running, rec.State, "8 + 9 GB at their worst, but 1.15 + 2.3 usual plus one 6.85 GB excess fits 14: %q", m.queue.waiting[id])
}

func TestQueueGreedy_ARunningJobsUseRaisesItsUsualLevel(t *testing.T) {
	m, a, sub := wqUsualModel(t)
	m.queue.usage[a] = jobUsage{RSSKB: 6 << 20} // below its 8 GB claim, far above its usual 1 GB
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, m, id).State, "6 × 1.15 measured, 2.3 usual and a 6.7 GB excess pass 14")
}

func TestQueueGreedy_NoUsualPeakForALadderedOrTwiceRequeuedJob(t *testing.T) {
	m, a, _ := wqUsualModel(t)
	rec := m.queue.store.Jobs[m.queue.index(a)]
	now := time.Now()
	expect := func(sched.Record) time.Duration { return 10 * time.Minute }
	require.Positive(t, m.predictRunning(rec, now, expect)[0].ExcessKB, "an ordinary job counts usually at its usual peak")
	laddered := rec
	laddered.Rung = sched.RungSqueezed
	assert.Zero(t, m.predictRunning(laddered, now, expect)[0].ExcessKB, "the ladder holds it")
	requeued := rec
	requeued.Requeues = sched.MaxRequeues
	assert.Zero(t, m.predictRunning(requeued, now, expect)[0].ExcessKB, "it runs at its full claim")
	assert.Zero(t, m.predictNew(requeued, now, expect)[0].ExcessKB, "and starts at it")
	assert.Positive(t, m.predictNew(rec, now, expect)[0].ExcessKB)
}
