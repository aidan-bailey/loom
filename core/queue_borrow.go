package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// markBorrower makes job i, just started, a borrower (memory borrowing spec
// §5.2), and logs its predicted peak.
func (m *Model) markBorrower(i int, pred func(sched.Record, time.Time) sched.Prediction, now time.Time) {
	rec := &m.queue.store.Jobs[i]
	rec.Borrower = true
	p := pred(*rec, now)
	log.For("core").Info("queue.borrowed", "job", int64(rec.ID), "peak_kb", sched.PeakSum([]sched.Prediction{p}, now, p.End()))
}

// makeFirm makes the running borrowers ids firm (memory borrowing spec §5.3).
func (m *Model) makeFirm(ids []sched.JobID) {
	q := m.queue
	for _, id := range ids {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].Borrower {
			q.store.Jobs[i].Borrower = false
			log.For("core").Info("queue.firm", "job", int64(id))
			q.moved()
		}
	}
}

// giveBackDone is a give-back action's result (memory borrowing spec §6):
// the kill's, freeze's or thaw's error, and after a lend the reclaim's.
type giveBackDone struct {
	act        sched.Action
	at         time.Time
	err        error
	reclaimErr error
}

// stepGiveBack runs one pass of the give-back machine after a sample while
// any borrower runs (memory borrowing spec §6.1), in fallback mode too,
// and one confinement action at a time with the ladder's. A cancelled lent
// job is thawed first, so it can act on its SIGTERM.
func (m *Model) stepGiveBack(now time.Time) {
	q := m.queue
	if q.laddering || q.availKB <= 0 {
		return
	}
	g, greedy := m.greedyInputs(q.policy, now)
	in := sched.GiveBackInput{Now: now, HeadroomKB: q.availKB, ReserveKB: int64(q.policy.ReserveGB) * kbPerGB, CanLend: greedy}
	if kb, err := readSwapFree(); err == nil {
		in.SwapFreeKB = kb
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || !rec.Borrower || rec.Cgroup == "" || rec.Requeue != "" {
			continue
		}
		if rec.CancelReason != "" {
			if !rec.LentAt.IsZero() && greedy {
				m.applyGiveBack(sched.Action{Kind: sched.ActResume, Job: rec.ID}, now)
				return
			}
			continue
		}
		j := sched.BorrowerJob{ID: rec.ID, Started: rec.Started, LentAt: rec.LentAt, LentFor: time.Duration(rec.LentSecs) * time.Second,
			WorkingKB: q.usage[rec.ID].RSSKB, NoLend: q.noLend[rec.ID]}
		if !rec.LentAt.IsZero() && greedy {
			j.Fits = sched.BorrowFits(q.store.Jobs, g, rec.ID, now)
		}
		in.Jobs = append(in.Jobs, j)
	}
	if len(in.Jobs) == 0 {
		return
	}
	var act *sched.Action
	q.giveBack, act = sched.GiveBack(q.giveBack, in)
	if act != nil {
		m.applyGiveBack(*act, now)
	}
}

// applyGiveBack carries out act on a borrower (memory borrowing spec
// §6.3–§6.5). A requeue writes the scope's cgroup.kill once the record asks
// for it, and settleGone then requeues the job as a give-back. A lend
// freezes the scope over D-Bus, then asks the kernel to reclaim its working
// set. A resume thaws it. The record changes only when the call returns.
func (m *Model) applyGiveBack(act sched.Action, now time.Time) {
	q := m.queue
	i := q.index(act.Job)
	if i < 0 {
		return
	}
	rec := &q.store.Jobs[i]
	c, root, unit, cg, kb := q.confiner, q.cgroupRoot, rec.Unit, rec.Cgroup, q.usage[rec.ID].RSSKB
	if act.Kind == sched.ActRequeue {
		rec.Requeue = fmt.Sprintf("it lent its memory back (%.1f GB free)", float64(q.availKB)/kbPerGB)
		rec.GivingBack = true
		q.moved()
		m.saveQueueQuiet("give back")
	}
	q.laddering = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		done := giveBackDone{act: act, at: now}
		switch act.Kind {
		case sched.ActRequeue:
			done.err = sched.CgroupKill(root, cg)
		case sched.ActLend:
			if done.err = c.Freeze(ctx, unit); done.err == nil {
				done.reclaimErr = sched.CgroupReclaim(root, cg, kb)
			}
		case sched.ActResume:
			done.err = c.Thaw(ctx, unit)
		}
		return done
	})
}

// deliverGiveBackDone records a give-back action that returned. A failed
// kill is retried; a failed freeze makes the next give-back of that job a
// requeue; a failed thaw is retried at the next pass.
func (m *Model) deliverGiveBackDone(r giveBackDone) {
	q := m.queue
	q.laddering = false
	i := q.index(r.act.Job)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	if r.err != nil {
		log.For("core").Warn("queue.give_back_failed", "job", int64(rec.ID), "action", string(r.act.Kind), "err", r.err)
		q.ladderErr = fmt.Sprintf("%s job %d: %v", r.act.Kind, rec.ID, r.err)
		switch r.act.Kind {
		case sched.ActRequeue:
			rec.Requeue, rec.GivingBack = "", false // the kill did not go out
		case sched.ActLend:
			q.noLend[rec.ID] = true
		}
		q.moved()
		return
	}
	q.ladderErr = ""
	stake := int64(r.at.Sub(rec.Started).Seconds()) - rec.LentSecs
	switch r.act.Kind {
	case sched.ActRequeue:
		log.For("core").Info("queue.gave_back", "job", int64(rec.ID), "action", "requeue", "headroom_kb", q.availKB,
			"working_kb", q.usage[rec.ID].RSSKB, "stake_s", stake)
	case sched.ActLend:
		// Stamped when the freeze returned, so a sample pass that began
		// later read the frozen cgroup (the reconcile's thawed-by-hand case
		// trusts only those).
		rec.LentAt, rec.Laddered = time.Now(), true
		log.For("core").Info("queue.gave_back", "job", int64(rec.ID), "action", "lend", "headroom_kb", q.availKB,
			"working_kb", q.usage[rec.ID].RSSKB, "stake_s", stake)
		if r.reclaimErr != nil {
			log.For("core").Warn("queue.reclaim_failed", "job", int64(rec.ID), "err", r.reclaimErr)
		}
	case sched.ActResume:
		lent := time.Since(rec.LentAt)
		rec.LentSecs += int64(lent.Seconds())
		rec.LentAt = time.Time{}
		log.For("core").Info("queue.resumed", "job", int64(rec.ID), "lent_s", int64(lent.Seconds()))
	}
	q.moved()
	m.saveQueueQuiet("give back")
}
