package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqHoldProfile is a run that peaked at 6 GB and 4 cores before progress
// 10, then used 1 GB and 1 core to its end at 100.
func wqHoldProfile() *sched.Profile {
	return &sched.Profile{End: 100,
		Mem:   sched.Staircase{PeakAt: 10, Steps: []sched.Step{{Until: 10, Level: 6 << 20}, {Until: 100, Level: 1 << 20}}},
		Cores: sched.Staircase{PeakAt: 5, Steps: []sched.Step{{Until: 10, Level: 4000}, {Until: 100, Level: 1000}}}}
}

// wqHoldJob launches a real shim (sleep 30) on caps of 4 cores and 8 GB,
// gives it a 4c/8G learned claim and a belief with three profiled runs, and
// returns it with the model.
func wqHoldJob(t *testing.T) (*Model, sched.JobID) {
	t.Helper()
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4, "memory_gb": 8}`)
	id := wqLaunch(t, m, "sleep", "30")
	rec := &m.queue.store.Jobs[m.queue.index(id)]
	sig := wqSigOf(t, rec.Argv, rec.Cwd)
	b := m.queue.beliefs.Ensure(sig, time.Now())
	for range 3 {
		b.AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 8}, PeakKB: 6 << 20,
			CPUSeconds: 100, WallSeconds: 50, Profile: wqHoldProfile()})
	}
	rec.Signature, rec.Claim = sig.ID(), sched.Claim{CPUs: 4, MemoryGB: 8}
	rec.Sources = sched.ClaimSources{CPUs: sched.SourceLearned, Memory: sched.SourceLearned, IO: sched.SourceLearned}
	return m, id
}

// wqSample delivers a pass whose sample shows job id's session at rss KB and
// cpu CPU-seconds, taken at.
func wqSample(m *Model, id sched.JobID, at time.Time, rss int64, cpu float64) {
	pid := m.queue.store.Jobs[m.queue.index(id)].PID
	snap := wqSnap(at, pid, rss, time.Duration(cpu*float64(time.Second)))
	m.Deliver(queuePassResult{at: at, obs: []jobObs{{id: id, pid: pid}}, snap: snap})
}

// wqPastPeak samples job id through its peak and two samples past it: at
// progress 13 its hold is 2c/2G.
func wqPastPeak(m *Model, id sched.JobID) {
	t0 := time.Now().Add(-12 * time.Second)
	wqSample(m, id, t0, 5<<20, 2)
	wqSample(m, id, t0.Add(3*time.Second), 6<<20, 12.8) // 3.6 cores: the peak
	wqSample(m, id, t0.Add(6*time.Second), 900<<10, 15.5)
	wqSample(m, id, t0.Add(9*time.Second), 800<<10, 18)
}

func TestJobQueue_AJobPastItsPeaksLetsTheNextOneIn(t *testing.T) {
	m, id := wqHoldJob(t)
	sub := wqJob(t, "sleep", "1")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 4}
	next, _, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Equal(t, sched.Queued, wqRecord(t, m, next).State, "the claim leaves no room")

	wqPastPeak(m, id)
	assert.Equal(t, sched.HoldState{CPUs: 2, MemoryGB: 2}, m.queue.holds[id])
	v := m.queueView()
	require.Equal(t, id, v.Jobs[0].ID)
	assert.Equal(t, sched.Need{CPUs: 2, MemoryGB: 2}, v.Jobs[0].Holds)
	assert.Equal(t, sched.Need{CPUs: 2, MemoryGB: 2}, v.Held)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, v.Claimed)

	m.admitJobs()
	assert.Equal(t, sched.Running, wqRecord(t, m, next).State, "the hold leaves 2c/6G")
}

func TestJobQueue_AReleasedJobThatGrowsBreaksItsHold(t *testing.T) {
	m, id := wqHoldJob(t)
	wqPastPeak(m, id)
	require.NotZero(t, m.queue.holds[id])

	wqSample(m, id, time.Now(), 3<<20, 19)
	assert.True(t, wqRecord(t, m, id).HoldBroke)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id], "back to its claim")
	assert.Zero(t, m.queueView().Jobs[0].Holds)

	wqSample(m, id, time.Now(), 800<<10, 19.5)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id], "for the rest of its run")
}

func TestJobQueue_AnAdoptedJobHoldsItsClaim(t *testing.T) {
	m, id := wqHoldJob(t)
	m.queue.store.Jobs[m.queue.index(id)].Adopted = true
	wqPastPeak(m, id)
	assert.Nil(t, m.queue.traces[id], "no trace for an adopted job")
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id])
}

func TestJobQueue_TheSwitchOffHoldsEveryClaim(t *testing.T) {
	m, id := wqHoldJob(t)
	wqConfig(t, m, `{"cpus": 4, "memory_gb": 8, "release_past_peaks": false}`)
	m.admitJobs() // rereads the block
	wqPastPeak(m, id)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id])
}

func TestJobQueue_ADoneRunsTraceBecomesItsProfile(t *testing.T) {
	const pid = 4242
	m := wqModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID, rec.Notify = sched.Running, nil, time.Time{}, pid, false
	sig := sched.NewSignature("/repo", ".", rec.Argv)
	rec.Signature = sig.ID()
	m.queue.beliefs.Ensure(sig, time.Now())
	wqSeed(t, m, rec)
	t0 := time.Now().Add(-6 * time.Second)
	for i, s := range []struct {
		rss int64
		cpu float64
	}{{4 << 20, 1}, {6 << 20, 7}, {1 << 20, 8}} {
		at := t0.Add(time.Duration(i) * 3 * time.Second)
		m.Deliver(queuePassResult{at: at, obs: []jobObs{{id: 1, pid: pid}}, snap: wqSnap(at, pid, s.rss, time.Duration(s.cpu*float64(time.Second)))})
	}
	end := time.Now()
	m.Deliver(queuePassResult{at: end, obs: []jobObs{{id: 1, pid: pid, gone: true, exit: &sched.Exit{Code: 0, Ended: end}}}})

	b := m.queue.beliefs.Find(sig.ID())
	require.Len(t, b.Runs, 1)
	p := b.Runs[0].Profile
	require.NotNil(t, p, "a done run's trace is its profile")
	assert.Equal(t, 8.0, p.End)
	assert.Equal(t, int64(6<<20), p.Mem.Peak())
	assert.Equal(t, 7.0, p.Mem.PeakAt)
	assert.Nil(t, m.queue.traces[1], "the settle drops the trace")
}

func TestJobQueue_AnAdoptedRunGetsNoProfile(t *testing.T) {
	const pid = 4242
	m := wqModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID, rec.Notify, rec.Adopted = sched.Running, nil, time.Time{}, pid, false, true
	sig := sched.NewSignature("/repo", ".", rec.Argv)
	rec.Signature = sig.ID()
	m.queue.beliefs.Ensure(sig, time.Now())
	wqSeed(t, m, rec)
	t0 := time.Now().Add(-6 * time.Second)
	for i, cpu := range []float64{1, 7, 8} {
		at := t0.Add(time.Duration(i) * 3 * time.Second)
		m.Deliver(queuePassResult{at: at, obs: []jobObs{{id: 1, pid: pid}}, snap: wqSnap(at, pid, 1<<20, time.Duration(cpu*float64(time.Second)))})
	}
	end := time.Now()
	m.Deliver(queuePassResult{at: end, obs: []jobObs{{id: 1, pid: pid, gone: true, exit: &sched.Exit{Code: 0, Ended: end}}}})

	b := m.queue.beliefs.Find(sig.ID())
	require.Len(t, b.Runs, 1)
	assert.Nil(t, b.Runs[0].Profile, "an adopted run's trace began partway through")
}

// The caps form never edits release_past_peaks: a caps edit keeps the
// file's, as it keeps the agent block.
func TestJobQueue_ACapsEditKeepsTheReleaseSwitch(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4, "release_past_peaks": false, "contain": false, "jobs_cpu_weight": 30}`)
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 6}))
	got := config.LoadConfigFrom(m.queue.cfgDir).GetScheduler()
	require.NotNil(t, got.ReleasePastPeaks, "the switch is still in the file")
	assert.False(t, *got.ReleasePastPeaks)
	assert.Equal(t, 6, got.CPUs)
	assert.False(t, m.queue.policy.ReleasePastPeaks, "and in effect")
	require.NotNil(t, got.Contain, "the cgroup keys too (cgroup spec §10)")
	assert.False(t, *got.Contain)
	assert.Equal(t, 30, got.JobsCPUWeight)
}
