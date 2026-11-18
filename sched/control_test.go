package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The pure rules of the job controls (spec 2026-11-16-job-controls).

func TestQueuedSince_TakesTheLatestOfSubmitReadyAndUnpark(t *testing.T) {
	r := Record{Submitted: t0}
	assert.Equal(t, t0, r.QueuedSince())
	r.Ready = t0.Add(time.Minute)
	assert.Equal(t, t0.Add(time.Minute), r.QueuedSince())
	r.Unparked = t0.Add(30 * time.Second)
	assert.Equal(t, t0.Add(time.Minute), r.QueuedSince(), "an unpark before the release counts from the release")
	r.Unparked = t0.Add(2 * time.Minute)
	assert.Equal(t, t0.Add(2*time.Minute), r.QueuedSince())
	assert.Equal(t, t0.Add(2*time.Minute), Record{Submitted: t0, Unparked: t0.Add(2 * time.Minute)}.QueuedSince())
}

func TestQueuedSince_ARequeueStartsTheWaitAgain(t *testing.T) {
	r := Record{Submitted: t0, Ready: t0.Add(time.Minute), Requeued: t0.Add(30 * time.Second)}
	assert.Equal(t, t0.Add(time.Minute), r.QueuedSince(), "a requeue before the release counts from the release")
	r.Requeued = t0.Add(25 * time.Minute)
	assert.Equal(t, t0.Add(25*time.Minute), r.QueuedSince())
	r.Unparked = t0.Add(30 * time.Minute)
	assert.Equal(t, t0.Add(30*time.Minute), r.QueuedSince(), "the latest of the two")
}

func TestPausedFor(t *testing.T) {
	r := Record{PausedSecs: 30}
	assert.Equal(t, 30*time.Second, r.PausedFor(t0))
	assert.False(t, r.Paused())
	r.PausedAt = t0.Add(-time.Minute)
	assert.True(t, r.Paused())
	assert.Equal(t, 90*time.Second, r.PausedFor(t0))
}

// starvingFor is queued job id with claim c, queued waited ago.
func starvingFor(id JobID, c Claim, waited time.Duration) Record {
	r := queued(id, c)
	r.Submitted, r.Ready = t0.Add(-waited), t0.Add(-waited)
	return r
}

func TestAdmitGreedy_AForcedJobStartsPastEveryFit(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(4), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	forced := queued(1, Claim{CPUs: 4, MemoryGB: 12}) // its 12 GB pass the 4 GB budget
	forced.Forced = true
	small := queued(2, Claim{CPUs: 1, MemoryGB: 1})
	d := AdmitGreedy([]Record{forced, small}, p, t0, g)
	assert.Equal(t, []JobID{1}, d.Start, "the forced job starts, and the small one sees its 12 GB")
	assert.Contains(t, d.Waiting[2], "at its peak")

	over := forced
	over.Claim.CPUs = 32
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{over}, p, t0, g).Start, "over the cores cap")

	g.Holdoff = "memory is short (job 9 squeezed)"
	d = AdmitGreedy([]Record{forced, small}, p, t0, g)
	assert.Equal(t, []JobID{1}, d.Start, "past the ladder's holdoff")
	assert.Equal(t, g.Holdoff, d.Waiting[2])

	g.Holdoff = ""
	sizing := forced
	sizing.Sizing = SizingAsking
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{sizing}, p, t0, g).Start, "while it is being sized")

	busy := running(5, Claim{Exclusive: true})
	g.Running = map[JobID]Prediction{5: Flat(0, t0, t0.Add(time.Hour))}
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{busy, forced}, p, t0, g).Start, "beside an exclusive job")

	head := starvingFor(3, Claim{CPUs: 1, MemoryGB: 1}, time.Hour)
	g.Running = map[JobID]Prediction{}
	g.BudgetKB = gb(14)
	d = AdmitGreedy([]Record{head, forced}, p, t0, g)
	assert.Equal(t, []JobID{1, 3}, d.Start, "past the guard, which then starts its head alone")
	assert.Equal(t, JobID(3), d.Starved)
}

func TestAdmit_AForcedJobWaitsOnARunningJobsLock(t *testing.T) {
	p := greedyPolicy()
	holder := running(5, Claim{CPUs: 1, MemoryGB: 1})
	holder.Locks = []string{"port:4300"}
	forced := queued(1, Claim{CPUs: 1, MemoryGB: 1})
	forced.Forced, forced.Locks = true, []string{"port:4300"}
	d := Admit([]Record{holder, forced}, p, t0, nil, nil)
	assert.Empty(t, d.Start)
	assert.Equal(t, "waits for lock port:4300 (job 5)", d.Waiting[1])
	assert.Equal(t, "waits for lock port:4300 (job 5)", LockBlock([]Record{holder, forced}, forced))
	assert.Empty(t, LockBlock([]Record{forced}, forced), "no running job holds it")

	// A queued job naming the lock a forced job takes waits for it, and is
	// never a starving head the guard would start beside it.
	free := forced
	free.Locks = []string{"port:4301"}
	other := starvingFor(2, Claim{CPUs: 1, MemoryGB: 1}, 11*time.Minute)
	other.Locks = []string{"port:4301"}
	d = Admit([]Record{free, other}, p, t0, nil, nil)
	assert.Equal(t, []JobID{1}, d.Start)
	assert.Equal(t, "waits for lock port:4301 (job 1)", d.Waiting[2])

	assert.Empty(t, Admit([]Record{free}, Policy{}, t0, nil, nil).Start, "a zero policy starts nothing, forced or not")
}

func TestAdmit_AParkedJobIsSkippedAndNeverTheHead(t *testing.T) {
	p := greedyPolicy()
	parked := starvingFor(1, Claim{CPUs: 1, MemoryGB: 1}, time.Hour)
	parked.Parked = true
	next := queued(2, Claim{CPUs: 1, MemoryGB: 1})
	d := Admit([]Record{parked, next}, p, t0, nil, nil)
	assert.Equal(t, []JobID{2}, d.Start)
	assert.Equal(t, WaitParked, d.Waiting[1])
	assert.Equal(t, JobID(0), d.Starved)

	parked.Parked = false
	d = Admit([]Record{parked, next}, p, t0, nil, nil)
	assert.Equal(t, JobID(1), d.Starved, "unparked, it is the starving head again")
}

func TestAdmit_APausedJobCountsMemoryOnly(t *testing.T) {
	p := greedyPolicy() // 14 cores, 14 GB, 2 I/O slots
	paused := running(5, Claim{CPUs: 14, MemoryGB: 6, IO: true})
	io := running(6, Claim{CPUs: 1, MemoryGB: 1, IO: true})
	next := queued(1, Claim{CPUs: 8, MemoryGB: 6, IO: true})
	assert.Empty(t, Admit([]Record{paused, io, next}, p, t0, nil, nil).Start, "running, its cores and slot count")
	paused.PausedAt = t0.Add(-time.Minute)
	assert.Equal(t, []JobID{1}, Admit([]Record{paused, io, next}, p, t0, nil, nil).Start, "paused, only its memory counts")
	next.Claim.MemoryGB = 8
	assert.Contains(t, Admit([]Record{paused, io, next}, p, t0, nil, nil).Waiting[1], "GB", "6 + 1 + 8 GB pass the 14 GB cap")

	g := Greedy{BudgetKB: gb(14), Running: map[JobID]Prediction{5: PausedPrediction(gb(2), t0), 6: Flat(gb(1), t0, t0.Add(time.Minute))}, Predict: flatPredict(nil)}
	next.Claim = Claim{CPUs: 14, MemoryGB: 11, IO: true}
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{paused, io, next}, p, t0, g).Start, "its 2 GB used, no cores, no slot")
	next.Claim.MemoryGB = 12
	assert.Contains(t, AdmitGreedy([]Record{paused, io, next}, p, t0, g).Waiting[1], "at its peak")
}

// A head that only a paused job keeps out fits once the owner resumes it,
// which no running job's end brings: like an over-cap job it is never the
// head, in either fit, and an exclusive job, which waits for an empty host,
// is never the head while a job is paused. A running job's end does make
// room, and the guard holds the rest back for the head as before.
func TestAdmit_AJobOnlyAPausedJobKeepsOutIsNeverTheHead(t *testing.T) {
	p := greedyPolicy() // 14 cores, 14 GB
	paused := running(5, Claim{CPUs: 2, MemoryGB: 8})
	paused.PausedAt = t0.Add(-time.Minute)
	big := starvingFor(1, Claim{CPUs: 4, MemoryGB: 10}, time.Hour) // 8 + 10 GB pass the 14 GB cap
	excl := starvingFor(3, Claim{CPUs: 1, MemoryGB: 1, Exclusive: true}, time.Hour)
	excl.Order = 1 // ahead of the small job, as big is
	small := queued(2, Claim{CPUs: 1, MemoryGB: 1})
	g := Greedy{BudgetKB: gb(14), Running: map[JobID]Prediction{5: PausedPrediction(gb(8), t0)}, Predict: flatPredict(nil)}

	for _, head := range []Record{big, excl} {
		jobs := []Record{paused, head, small}
		for name, d := range map[string]Decision{
			"claims": Admit(jobs, p, t0, nil, nil),
			"greedy": AdmitGreedy(jobs, p, t0, g),
		} {
			assert.Equal(t, []JobID{2}, d.Start, "%s, job %d: the small job starts", name, head.ID)
			assert.Zero(t, d.Starved, name)
			assert.Contains(t, d.Waiting[head.ID], "paused", name)
		}
	}

	busy := running(5, Claim{CPUs: 2, MemoryGB: 8})
	g.Running = map[JobID]Prediction{5: Flat(gb(8), t0, t0.Add(10*time.Minute))}
	jobs := []Record{busy, big, small}
	for name, d := range map[string]Decision{
		"claims": Admit(jobs, p, t0, nil, nil),
		"greedy": AdmitGreedy(jobs, p, t0, g),
	} {
		assert.Empty(t, d.Start, name)
		assert.Equal(t, "waits for job 1, which has waited past 10m", d.Waiting[2], name)
	}
}

func TestPausedPrediction_OutlastsAnyCandidate(t *testing.T) {
	pred := PausedPrediction(gb(3), t0)
	assert.Equal(t, gb(3), PeakSum([]Prediction{pred}, t0, t0.Add(24*time.Hour)))
}

func TestGrant_ParkedWaitsAndForcedStarts(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the default claim, the reserve, 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	wide := belief(at(4, 3.9)) // efficient up to 8 cores
	learned := func(id JobID, cores int) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: Claim{CPUs: cores, MemoryGB: 2},
			Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	}
	grant := func(jobs ...Record) map[JobID]Claim {
		return Grant(Admit(jobs, p, now, nil, nil), jobs, p, nil, func(Record) *Belief { return wide }, HostIdle{}).Spare
	}
	parked := learned(2, 11) // it would wait for cores
	parked.Parked = true
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, grant(learned(1, 4), parked), "a parked job's wait stops no grant")
	parked.Parked = false
	assert.Nil(t, grant(learned(1, 4), parked), "unparked, its wait for cores does")

	forced := learned(1, 4)
	forced.Forced = true
	assert.Nil(t, grant(forced), "a forced start gets its claim and no more")
}

func TestForecast_APausedJobNeverEnds(t *testing.T) {
	p := greedyPolicy()
	paused := running(5, Claim{CPUs: 1, MemoryGB: 10})
	paused.PausedAt = t0.Add(-time.Minute)
	next := queued(1, Claim{CPUs: 1, MemoryGB: 10})
	usual := func(Record) (time.Duration, bool) { return time.Minute, true }
	starts := Forecast([]Record{paused, next}, p, t0, nil, nil, usual, time.Minute)
	assert.NotContains(t, starts, JobID(1), "10 + 10 GB pass the cap until the owner resumes it")
	paused.PausedAt = time.Time{}
	assert.Contains(t, Forecast([]Record{paused, next}, p, t0, nil, nil, usual, time.Minute), JobID(1))
}

func TestLadder_ForcedJobsGoLast(t *testing.T) {
	in := LadderInput{Now: t0, HeadroomKB: 0, ReserveKB: gb(4), Jobs: []LadderJob{
		{ID: 1, Started: t0.Add(-time.Minute), WorkingKB: gb(1)},
		{ID: 2, Started: t0.Add(-time.Second), WorkingKB: gb(1), Forced: true},
	}}
	_, act := Ladder(LadderState{}, in)
	if assert.NotNil(t, act) {
		assert.Equal(t, JobID(1), act.Job, "the most recent start is forced: the older one goes first")
	}
	in.Jobs[1].Forced = false
	_, act = Ladder(LadderState{}, in)
	if assert.NotNil(t, act) {
		assert.Equal(t, JobID(2), act.Job)
	}
}
