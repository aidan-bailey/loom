package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func greedyPolicy() Policy {
	return Policy{Caps: Caps{CPUs: 14, MemoryGB: 14, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6},
		StarveAfter: 10 * time.Minute, ReserveGB: 4, CPUPressurePct: 25, CPUOvercommit: 2}
}

func gb(n int64) int64 { return n * kbPerGB }

// flatPredict predicts each job flat at its claim for ten minutes, or at
// the shape in shapes.
func flatPredict(shapes map[JobID]func(start time.Time) Prediction) func(Record, time.Time) Prediction {
	return func(r Record, start time.Time) Prediction {
		if f, ok := shapes[r.ID]; ok {
			return f(start)
		}
		return Flat(gb(int64(r.Claim.MemoryGB)), start, start.Add(10*time.Minute))
	}
}

func running(id JobID, c Claim) Record {
	return Record{ID: id, Order: int64(id), State: Running, Claim: c, Started: t0.Add(-time.Minute)}
}

func queued(id JobID, c Claim) Record {
	return Record{ID: id, Order: int64(id), State: Queued, Claim: c, Submitted: t0, Ready: t0}
}

func TestBudget(t *testing.T) {
	p := greedyPolicy()
	assert.Equal(t, gb(10), Budget(gb(2), gb(12), p), "used + available − reserve")
	assert.Equal(t, gb(14), Budget(gb(10), gb(20), p), "held to the memory cap")
	assert.Equal(t, int64(0), Budget(0, gb(1), p), "never below 0")
}

func TestAdmitGreedy_PeaksThatDontMeetBothStart(t *testing.T) {
	g := Greedy{
		BudgetKB: gb(12),
		Running:  map[JobID]Prediction{1: {{t0, t0.Add(time.Minute), gb(9)}, {t0.Add(time.Minute), t0.Add(10 * time.Minute), gb(1)}}},
		Predict: flatPredict(map[JobID]func(time.Time) Prediction{2: func(s time.Time) Prediction {
			return Prediction{{s, s.Add(5 * time.Minute), gb(1)}, {s.Add(5 * time.Minute), s.Add(8 * time.Minute), gb(9)}}
		}}),
	}
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 10})}
	assert.Equal(t, []JobID{2}, AdmitGreedy(jobs, greedyPolicy(), t0, g).Start)
	assert.Empty(t, Admit(jobs, greedyPolicy(), t0, nil, nil).Start, "claims alone would hold it back")
}

func TestAdmitGreedy_PeaksThatMeetWait(t *testing.T) {
	g := Greedy{BudgetKB: gb(12), Running: map[JobID]Prediction{1: Flat(gb(9), t0, t0.Add(10*time.Minute))}, Predict: flatPredict(nil)}
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 9}), queued(2, Claim{CPUs: 4, MemoryGB: 9})}
	d := AdmitGreedy(jobs, greedyPolicy(), t0, g)
	assert.Empty(t, d.Start)
	assert.Equal(t, "needs ~9 GB at its peak (forecast leaves 3)", d.Waiting[2])
}

func TestAdmitGreedy_JobsStartedInThePassCount(t *testing.T) {
	g := Greedy{BudgetKB: gb(12), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	jobs := []Record{queued(1, Claim{CPUs: 1, MemoryGB: 7}), queued(2, Claim{CPUs: 1, MemoryGB: 7})}
	d := AdmitGreedy(jobs, greedyPolicy(), t0, g)
	assert.Equal(t, []JobID{1}, d.Start)
	assert.Contains(t, d.Waiting[2], "needs ~7 GB")
}

func TestAdmitGreedy_CPU(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(100), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	var jobs []Record
	for i := range 7 {
		jobs = append(jobs, running(JobID(i+1), Claim{CPUs: 4, MemoryGB: 1}))
	}
	for id := range JobID(7) {
		g.Running[id+1] = Flat(1, t0, t0.Add(time.Minute))
	}
	jobs = append(jobs, queued(8, Claim{CPUs: 4, MemoryGB: 1}))
	assert.Equal(t, "CPU overcommitted (32/28 claimed)", AdmitGreedy(jobs, p, t0, g).Waiting[8])

	jobs = []Record{running(1, Claim{CPUs: 4, MemoryGB: 1}), queued(2, Claim{CPUs: 4, MemoryGB: 1})}
	g.Running = map[JobID]Prediction{1: Flat(1, t0, t0.Add(time.Minute))}
	g.CPUPressure = 25
	assert.Equal(t, "CPU busy (pressure 25%)", AdmitGreedy(jobs, p, t0, g).Waiting[2])
	g.CPUPressure = 24.9
	assert.Equal(t, []JobID{2}, AdmitGreedy(jobs, p, t0, g).Start)
	g.CPUPressure = 90
	assert.Equal(t, []JobID{2}, AdmitGreedy([]Record{queued(2, Claim{CPUs: 4, MemoryGB: 1})}, p, t0, g).Start, "pressure counts only while a job runs")
}

func TestAdmitGreedy_ExclusiveIOAndHoldoff(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(100), Running: map[JobID]Prediction{1: Flat(1, t0, t0.Add(time.Minute))}, Predict: flatPredict(nil)}
	ex := queued(2, Claim{Exclusive: true})
	assert.Equal(t, "waits for the host to empty", AdmitGreedy([]Record{running(1, Claim{CPUs: 1, MemoryGB: 1}), ex}, p, t0, g).Waiting[2])
	assert.Equal(t, []JobID{2}, AdmitGreedy([]Record{ex}, p, t0, g).Start)
	busy := running(1, Claim{Exclusive: true})
	assert.Equal(t, "waits for exclusive job 1", AdmitGreedy([]Record{busy, queued(2, Claim{CPUs: 1, MemoryGB: 1})}, p, t0, g).Waiting[2])

	io := []Record{running(1, Claim{CPUs: 1, MemoryGB: 1, IO: true}), running(3, Claim{CPUs: 1, MemoryGB: 1, IO: true}), queued(2, Claim{CPUs: 1, MemoryGB: 1, IO: true})}
	g.Running[3] = Flat(1, t0, t0.Add(time.Minute))
	assert.Equal(t, "needs an I/O slot", AdmitGreedy(io, p, t0, g).Waiting[2])

	g.Holdoff = "memory is short (job 1 paused)"
	assert.Equal(t, g.Holdoff, AdmitGreedy([]Record{queued(2, Claim{CPUs: 1, MemoryGB: 1})}, p, t0, g).Waiting[2])
}

func TestAdmitGreedy_KeepsTheGuards(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(100), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	over := queued(1, Claim{CPUs: 4, MemoryGB: 15})
	assert.Equal(t, waitOver, AdmitGreedy([]Record{over}, p, t0, g).Waiting[1])
	head := queued(2, Claim{CPUs: 4, MemoryGB: 2})
	head.Ready = t0.Add(-11 * time.Minute)
	d := AdmitGreedy([]Record{head, queued(3, Claim{CPUs: 1, MemoryGB: 1})}, p, t0, g)
	assert.Equal(t, []JobID{2}, d.Start)
	assert.Equal(t, JobID(2), d.Starved)
	assert.Contains(t, d.Waiting[3], "waits for job 2")
}
