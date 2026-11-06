package sched

import (
	"slices"
	"time"
)

// Borrowing is what Borrow decided in a pass (memory borrowing spec §5).
type Borrowing struct {
	Start []JobID // queued jobs to start as borrowers, in queue order
	Firm  []JobID // running borrowers that become firm
}

// MayBorrow reports whether queued job r may start as a borrower (memory
// borrowing spec §5.1): it is not --no-requeue, exclusive, an agent job or a run job,
// it has been requeued fewer than MaxRequeues times, and it is not the
// attempt after a give-back requeue.
func MayBorrow(r Record) bool {
	return !r.NoRequeue && !r.Claim.Exclusive && !Unclaimed(r.Kind) && r.Requeues < MaxRequeues && !r.NoBorrow
}

// Borrow is the borrowing pass (memory borrowing spec §5.2–§5.3), a pure
// function run after AdmitGreedy, on its decision dec. Whatever p.Borrow
// says, it names the running unlent borrowers that become firm: those the
// firm fit holds for until their expected end. When p.Borrow is set, memory
// is not tight, the ladder holds nothing off and no job is lent, it also
// names the jobs dec left waiting on memory alone (dec.OnMemory) that start
// as borrowers. It walks them in queue order and starts each that may borrow
// (MayBorrow), names no lock another job holds (a running job, one of dec's
// starts or a borrower before it), passes the CPU and I/O gates and passes
// the borrow fit, counting the running jobs, dec's starts and the borrowers
// before it.
func Borrow(jobs []Record, dec Decision, p Policy, now time.Time, g Greedy) Borrowing {
	var out Borrowing
	all := runningPreds(jobs, g)
	lent := false
	for _, r := range jobs {
		switch {
		case r.State != Running || !r.Borrower:
		case !r.LentAt.IsZero():
			lent = true
		case PeakSum(all, now, runningPred(r, g).End()) <= g.BudgetKB:
			out.Firm = append(out.Firm, r.ID)
		}
	}
	if !p.Borrow || g.Tight || g.Holdoff != "" || lent || len(dec.OnMemory) == 0 {
		return out
	}
	byID := map[JobID]Record{}
	f := &greedyFit{p: p, g: g, now: now}
	for _, r := range jobs {
		byID[r.ID] = r
		if r.State == Running && !Unclaimed(r.Kind) {
			f.count(r)
		}
	}
	firmKB, preds := borrowLoad(jobs, g)
	held := lockHolders(jobs)
	for _, id := range dec.Start {
		r, ok := byID[id]
		if !ok {
			continue
		}
		held.take(r)
		if !Unclaimed(r.Kind) {
			f.take(r)
			preds = append(preds, g.Predict(r, now))
		}
	}
	for _, id := range dec.OnMemory {
		// A job waiting on memory took no lock in AdmitGreedy's walk, so a
		// later start may hold one it names.
		r, ok := byID[id]
		if !ok || !MayBorrow(r) || held.blocks(r) != "" || f.gate(r) != "" {
			continue
		}
		cand := g.Predict(r, now)
		if firmKB+PeakSum(append(slices.Clone(preds), cand), now, cand.End()) > g.BudgetKB {
			continue
		}
		f.take(r)
		held.take(r)
		preds = append(preds, cand)
		out.Start = append(out.Start, id)
	}
	return out
}

// BorrowFits reports whether running borrower id fits the borrow fit from
// now to its prediction's end (memory borrowing spec §6.5): the firm jobs at
// their measured memory and every borrower, it included, at its prediction.
// The give-back machine resumes a lent job on it.
func BorrowFits(jobs []Record, g Greedy, id JobID, now time.Time) bool {
	pred, ok := g.Running[id]
	if !ok {
		return false
	}
	firmKB, preds := borrowLoad(jobs, g)
	return firmKB+PeakSum(preds, now, pred.End()) <= g.BudgetKB
}

// borrowLoad is what the borrow fit counts of the running jobs, agent jobs
// aside as AdmitGreedy leaves them (memory borrowing spec §5.2): a firm job
// at its measured memory plus the margin, or at its prediction before it is
// measured, and a borrower, lent or not, at its prediction.
func borrowLoad(jobs []Record, g Greedy) (firmKB int64, preds []Prediction) {
	for _, r := range jobs {
		if r.State != Running || Unclaimed(r.Kind) {
			continue
		}
		if used, ok := g.Used[r.ID]; ok && used > 0 && !r.Borrower {
			firmKB += WithMarginKB(used)
			continue
		}
		preds = append(preds, runningPred(r, g))
	}
	return firmKB, preds
}

// runningPreds is every running job's prediction but agent and run jobs', as
// AdmitGreedy counts them: the firm fit's terms (memory borrowing spec §5.3).
func runningPreds(jobs []Record, g Greedy) []Prediction {
	var preds []Prediction
	for _, r := range jobs {
		if r.State == Running && !Unclaimed(r.Kind) {
			preds = append(preds, runningPred(r, g))
		}
	}
	return preds
}

// runningPred is running job r's prediction from now, or from its start
// when g has none (greedyFit.count's rule).
func runningPred(r Record, g Greedy) Prediction {
	if pred, ok := g.Running[r.ID]; ok {
		return pred
	}
	return g.Predict(r, r.Started)
}
