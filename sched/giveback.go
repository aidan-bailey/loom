package sched

import (
	"cmp"
	"slices"
	"time"
)

// The give-back machine's thresholds (memory borrowing spec §6, §9).
const (
	cheapStake    = 2 * time.Minute  // a borrower that has run less is requeued, not lent
	giveBackEvery = 10 * time.Second // the least time between give-backs
	lendToResume  = 30 * time.Second // the least time a job stays lent
)

// The give-back machine's actions; a give-back requeue is ActRequeue.
const (
	ActLend   ActionKind = "lend"   // freeze it, then push its memory to swap
	ActResume ActionKind = "resume" // thaw a lent job
)

// BorrowerJob is a running borrower as GiveBack sees it.
type BorrowerJob struct {
	ID        JobID
	Started   time.Time
	LentAt    time.Time     // zero unless lent
	LentFor   time.Duration // time lent in this attempt before LentAt
	WorkingKB int64
	NoLend    bool // a freeze of it failed: it is given back by requeue
	Fits      bool // lent: the borrow fit holds for it now (BorrowFits)
}

// GiveBackInput is one pass's reading (memory borrowing spec §6.1).
type GiveBackInput struct {
	Now        time.Time
	HeadroomKB int64 // the host's MemAvailable
	ReserveKB  int64 // interactive_reserve_gb
	SwapFreeKB int64 // the host's SwapFree
	CanLend    bool  // the user bus answers (greedy mode): a freeze or thaw can be made
	Jobs       []BorrowerJob
}

// GiveBackState is what the machine keeps between passes.
type GiveBackState struct {
	Last time.Time // the last give-back
}

// Tight reports whether memory is tight: headroom below the reserve plus
// the ladder's calm margin (memory borrowing spec §4).
func Tight(headroomKB, reserveKB int64) bool { return headroomKB < reserveKB+calmMarginKB }

// GiveBack is one pass of the give-back machine (memory borrowing spec §6),
// a pure function: at most one action. While memory is tight it gives back
// the most recently started unlent borrower, at most one every
// giveBackEvery. That is a requeue when the borrower has run under
// cheapStake (time lent aside), a freeze of it failed, there is no bus, or
// swap can't take its working set, and a lend otherwise. While memory is not
// tight, and the bus is up, it resumes the lent job lent earliest whose
// borrow fit holds, once it has been lent lendToResume.
func GiveBack(st GiveBackState, in GiveBackInput) (GiveBackState, *Action) {
	if Tight(in.HeadroomKB, in.ReserveKB) {
		if !st.Last.IsZero() && in.Now.Sub(st.Last) < giveBackEvery {
			return st, nil
		}
		var v *BorrowerJob
		for i := range in.Jobs {
			j := &in.Jobs[i]
			if j.LentAt.IsZero() && (v == nil || j.Started.After(v.Started) || (j.Started.Equal(v.Started) && j.ID > v.ID)) {
				v = j
			}
		}
		if v == nil {
			return st, nil
		}
		st.Last = in.Now
		if stake := in.Now.Sub(v.Started) - v.LentFor; stake < cheapStake || v.NoLend || !in.CanLend || in.SwapFreeKB < v.WorkingKB {
			return st, &Action{Kind: ActRequeue, Job: v.ID}
		}
		return st, &Action{Kind: ActLend, Job: v.ID}
	}
	if !in.CanLend {
		return st, nil
	}
	var ready []*BorrowerJob
	for i := range in.Jobs {
		if j := &in.Jobs[i]; !j.LentAt.IsZero() && j.Fits && in.Now.Sub(j.LentAt) >= lendToResume {
			ready = append(ready, j)
		}
	}
	if len(ready) == 0 {
		return st, nil
	}
	j := slices.MinFunc(ready, func(a, b *BorrowerJob) int { return cmp.Or(a.LentAt.Compare(b.LentAt), cmp.Compare(a.ID, b.ID)) })
	return st, &Action{Kind: ActResume, Job: j.ID}
}
