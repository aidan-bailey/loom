package sched

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// borrowPolicy is greedyPolicy with borrowing on.
func borrowPolicy() Policy {
	p := greedyPolicy()
	p.Borrow = true
	return p
}

// borrowGreedy is a 14 GB budget whose running jobs are predicted flat at
// their claims for ten minutes and measured at used.
func borrowGreedy(jobs []Record, used map[JobID]int64) Greedy {
	g := Greedy{BudgetKB: gb(14), Running: map[JobID]Prediction{}, Used: used, Predict: flatPredict(nil)}
	for _, r := range jobs {
		if r.State == Running {
			g.Running[r.ID] = Flat(gb(int64(r.Claim.MemoryGB)), t0, t0.Add(10*time.Minute))
		}
	}
	return g
}

// borrowed is a running borrower.
func borrowed(id JobID, c Claim) Record {
	r := running(id, c)
	r.Borrower = true
	return r
}

func TestAdmitGreedy_OnMemoryNamesOnlyTheMemoryWaits(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 20, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6}), queued(3, Claim{CPUs: 9, MemoryGB: 1})}
	d := AdmitGreedy(jobs, greedyPolicy(), t0, borrowGreedy(jobs, nil))
	assert.Equal(t, []JobID{2}, d.OnMemory)
	assert.Contains(t, d.Waiting[3], "CPU overcommitted")
}

func TestBorrow_StartsAgainstWhatFirmJobsUseNow(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
	dec := AdmitGreedy(jobs, borrowPolicy(), t0, g)
	require.Empty(t, dec.Start, "10 + 6 GB of forecasts pass the 14 GB budget")
	require.Equal(t, []JobID{2}, dec.OnMemory)
	assert.Equal(t, []JobID{2}, Borrow(jobs, dec, borrowPolicy(), t0, g).Start, "4 GB used × 1.15 + 6 GB fits")
	assert.Empty(t, Borrow(jobs, dec, greedyPolicy(), t0, g).Start, "borrow off")
}

func TestBorrow_BorrowersLendToNoOne(t *testing.T) {
	jobs := []Record{borrowed(1, Claim{CPUs: 4, MemoryGB: 9}), queued(2, Claim{CPUs: 4, MemoryGB: 6})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(1)})
	assert.Empty(t, Borrow(jobs, Decision{OnMemory: []JobID{2}}, borrowPolicy(), t0, g).Start,
		"the borrower counts at its 9 GB prediction, not its 1 GB use")
}

func TestBorrow_AFirmJobNotYetMeasuredCountsAtItsPrediction(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6})}
	g := borrowGreedy(jobs, map[JobID]int64{})
	assert.Empty(t, Borrow(jobs, Decision{OnMemory: []JobID{2}}, borrowPolicy(), t0, g).Start)
}

func TestBorrow_WhoMayBorrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Record)
		may  bool
	}{
		{"an ordinary job", func(*Record) {}, true},
		{"--no-requeue", func(r *Record) { r.NoRequeue = true }, false},
		{"requeued MaxRequeues times", func(r *Record) { r.Requeues = MaxRequeues }, false},
		{"one requeue below", func(r *Record) { r.Requeues = MaxRequeues - 1 }, true},
		{"the attempt after a give-back", func(r *Record) { r.NoBorrow = true }, false},
		{"exclusive", func(r *Record) { r.Claim.Exclusive = true }, false},
		{"an agent job", func(r *Record) { r.Kind = KindAgent }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := queued(2, Claim{CPUs: 4, MemoryGB: 6})
			tc.edit(&r)
			assert.Equal(t, tc.may, MayBorrow(r))
			jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), r}
			g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
			assert.Equal(t, tc.may, len(Borrow(jobs, Decision{OnMemory: []JobID{2}}, borrowPolicy(), t0, g).Start) == 1)
		})
	}
}

func TestBorrow_HoldsBack(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6}), borrowed(3, Claim{CPUs: 1, MemoryGB: 1})}
	used := map[JobID]int64{1: gb(4), 3: gb(1)}
	dec := Decision{OnMemory: []JobID{2}}
	g := borrowGreedy(jobs, used)
	require.Equal(t, []JobID{2}, Borrow(jobs, dec, borrowPolicy(), t0, g).Start, "4.6 + 1 + 6 GB fits: the case each guard spares")

	tight := g
	tight.Tight = true
	assert.Empty(t, Borrow(jobs, dec, borrowPolicy(), t0, tight).Start, "memory is tight")
	held := g
	held.Holdoff = "memory is short (job 1 paused)"
	assert.Empty(t, Borrow(jobs, dec, borrowPolicy(), t0, held).Start, "the ladder holds admission off")
	lent := slices.Clone(jobs)
	lent[2].LentAt = t0.Add(-time.Minute)
	assert.Empty(t, Borrow(lent, dec, borrowPolicy(), t0, g).Start, "a job is lent")
}

func TestBorrow_ThePassStartsCount(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 2, MemoryGB: 5}), queued(3, Claim{CPUs: 2, MemoryGB: 5})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
	assert.Equal(t, []JobID{2}, Borrow(jobs, Decision{OnMemory: []JobID{2, 3}}, borrowPolicy(), t0, g).Start,
		"4.6 + 5 + 5 GB passes 14: the second borrower waits")

	dec := Decision{Start: []JobID{3}, OnMemory: []JobID{2}}
	assert.Empty(t, Borrow(jobs, dec, borrowPolicy(), t0, g).Start, "AdmitGreedy's start counts at its 5 GB prediction")
}

func TestBorrow_TheCPUGateCountsThePassStarts(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 14, MemoryGB: 10}), queued(2, Claim{CPUs: 8, MemoryGB: 1}), queued(3, Claim{CPUs: 8, MemoryGB: 1})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
	assert.Equal(t, []JobID{2}, Borrow(jobs, Decision{OnMemory: []JobID{2, 3}}, borrowPolicy(), t0, g).Start,
		"14 + 8 + 8 cores pass the 28 allowed")
}

func TestBorrow_ABorrowerBecomesFirmWhenTheFirmFitHolds(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 6}), borrowed(2, Claim{CPUs: 4, MemoryGB: 8})}
	used := map[JobID]int64{1: gb(2), 2: gb(2)}
	assert.Equal(t, []JobID{2}, Borrow(jobs, Decision{}, greedyPolicy(), t0, borrowGreedy(jobs, used)).Firm,
		"6 + 8 GB of forecasts fit 14, with borrowing off too")
	jobs[1].Claim.MemoryGB = 9
	assert.Empty(t, Borrow(jobs, Decision{}, greedyPolicy(), t0, borrowGreedy(jobs, used)).Firm, "6 + 9 GB don't")
	jobs[1].Claim.MemoryGB = 8
	jobs[1].LentAt = t0
	assert.Empty(t, Borrow(jobs, Decision{}, greedyPolicy(), t0, borrowGreedy(jobs, used)).Firm, "a lent job resumes first")
}

func TestBorrowFits(t *testing.T) {
	lent := borrowed(2, Claim{CPUs: 4, MemoryGB: 8})
	lent.LentAt = t0.Add(-time.Minute)
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 12}), lent}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(5), 2: 0})
	assert.True(t, BorrowFits(jobs, g, 2, t0), "5 × 1.15 + 8 = 13.75 GB")
	g.Used[1] = gb(6)
	assert.False(t, BorrowFits(jobs, g, 2, t0), "6 × 1.15 + 8 = 14.9 GB")
	assert.False(t, BorrowFits(jobs, g, 9, t0), "no such job")
}
