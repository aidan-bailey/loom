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
