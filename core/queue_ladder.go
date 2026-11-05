package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// sliceHighStep is how far the budget moves before the slice's MemoryHigh
// follows it (cgroup spec §8.1).
const sliceHighStep = 256 << 10 // KB

// ladderDone is a ladder action's result.
type ladderDone struct {
	act sched.Action
	at  time.Time
	err error
}

// sliceHighSet is a set of the slice's MemoryHigh.
type sliceHighSet struct {
	kb  int64
	err error
}

// stepLadder runs one pass of the ladder after a sample (cgroup spec §8),
// in greedy mode only and one action at a time. A cancelled job that is
// paused is restored first, so it can act on its SIGTERM.
func (m *Model) stepLadder(now time.Time) {
	q := m.queue
	if q.containWhy(q.policy) != "" || q.availKB <= 0 || q.laddering {
		return
	}
	in := sched.LadderInput{Now: now, HeadroomKB: q.availKB, ReserveKB: int64(q.policy.ReserveGB) * kbPerGB}
	if q.slice != nil {
		in.MemPressure = q.slice.MemPressure
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || rec.Cgroup == "" || rec.Requeue != "" {
			continue
		}
		if rec.CancelReason != "" {
			if rec.Rung != "" {
				m.applyLadder(sched.Action{Kind: sched.ActRestore, Job: rec.ID}, now)
				return
			}
			continue
		}
		in.Jobs = append(in.Jobs, sched.LadderJob{
			ID: rec.ID, Started: rec.Started, WorkingKB: q.usage[rec.ID].RSSKB, Rung: rec.Rung, RungAt: rec.RungAt,
			NoRequeue: rec.NoRequeue, Exclusive: rec.Claim.Exclusive, Starved: rec.Starved, Requeues: rec.Requeues,
		})
	}
	var act *sched.Action
	q.ladder, act = sched.Ladder(q.ladder, in)
	if act != nil {
		m.applyLadder(*act, now)
		return
	}
	m.maybeSetSliceHigh()
}

// applyLadder carries out act from a job: a squeeze, pause or restore over
// D-Bus, a requeue by writing the scope's cgroup.kill once the record asks
// for it (settleGone then requeues the job). The rung changes only when the
// call returns (deliverLadderDone).
func (m *Model) applyLadder(act sched.Action, now time.Time) {
	q := m.queue
	i := q.index(act.Job)
	if i < 0 {
		return
	}
	rec := q.store.Jobs[i]
	c, root := q.confiner, q.cgroupRoot
	if act.Kind == sched.ActRequeue {
		q.store.Jobs[i].Requeue = fmt.Sprintf("memory ran short (%.1f GB free)", float64(q.availKB)/kbPerGB)
		q.moved()
		m.saveQueueQuiet("ladder")
	}
	q.laddering = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		var err error
		switch act.Kind {
		case sched.ActSqueeze:
			err = c.SetMemoryHigh(ctx, rec.Unit, uint64(act.MemoryHighKB)<<10)
		case sched.ActPause:
			err = c.Freeze(ctx, rec.Unit)
		case sched.ActRestore:
			if rec.Rung == sched.RungPaused {
				err = c.Thaw(ctx, rec.Unit)
			}
			if err == nil {
				err = c.SetMemoryHigh(ctx, rec.Unit, 0)
			}
		case sched.ActRequeue:
			err = sched.CgroupKill(root, rec.Cgroup)
		}
		return ladderDone{act: act, at: now, err: err}
	})
}

// deliverLadderDone records a ladder action that returned: the job's rung,
// or, for a failed call, nothing but the error, retried at the next pass.
func (m *Model) deliverLadderDone(r ladderDone) {
	q := m.queue
	q.laddering = false
	i := q.index(r.act.Job)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	if r.err != nil {
		log.For("core").Warn("queue.ladder_failed", "job", int64(rec.ID), "action", string(r.act.Kind), "err", r.err)
		q.ladderErr = fmt.Sprintf("%s job %d: %v", r.act.Kind, rec.ID, r.err)
		if r.act.Kind == sched.ActRequeue {
			rec.Requeue = "" // the kill did not go out: the ladder tries again
		}
		q.moved()
		return
	}
	q.ladderErr = ""
	switch r.act.Kind {
	case sched.ActSqueeze:
		rec.Rung, rec.RungAt, rec.Laddered = sched.RungSqueezed, r.at, true
	case sched.ActPause:
		rec.Rung, rec.RungAt = sched.RungPaused, r.at
	case sched.ActRestore:
		rec.Rung, rec.RungAt = "", time.Time{}
	}
	log.For("core").Info("queue.ladder", "job", int64(rec.ID), "action", string(r.act.Kind),
		"headroom_kb", q.availKB, "working_kb", q.usage[rec.ID].RSSKB)
	q.moved()
	m.saveQueueQuiet("ladder")
}

// maybeSetSliceHigh keeps the slice's MemoryHigh at the budget (rung 0,
// cgroup spec §8.1), at least 1 GB, once it has moved by sliceHighStep.
func (m *Model) maybeSetSliceHigh() {
	q := m.queue
	if q.sliceSetting || q.confiner == nil {
		return
	}
	var used int64
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State == sched.Running {
			used += q.usage[q.store.Jobs[i].ID].RSSKB
		}
	}
	kb := max(sched.Budget(used, q.availKB, q.policy), kbPerGB)
	if d := kb - q.sliceHighKB; d < sliceHighStep && -d < sliceHighStep {
		return
	}
	c, slice := q.confiner, sched.SliceName(q.dir)
	q.sliceSetting = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		return sliceHighSet{kb: kb, err: c.SetMemoryHigh(ctx, slice, uint64(kb)<<10)}
	})
}

// deliverSliceHighSet records the slice's MemoryHigh, or logs why it failed.
func (m *Model) deliverSliceHighSet(r sliceHighSet) {
	q := m.queue
	q.sliceSetting = false
	if r.err != nil {
		log.For("core").Warn("queue.slice_high_failed", "err", r.err)
		return
	}
	q.sliceHighKB = r.kb
}

// ladderHoldoff is why nothing is admitted while the ladder holds a job
// (cgroup spec §8.3), "" when it holds none: the latest laddered job.
func (m *Model) ladderHoldoff() string {
	q := m.queue
	var last *sched.Record
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State == sched.Running && rec.Rung != "" && (last == nil || rec.RungAt.After(last.RungAt)) {
			last = rec
		}
	}
	if last == nil {
		return ""
	}
	return fmt.Sprintf("memory is short (job %d %s)", last.ID, last.Rung)
}

// requeueWhy is why job rec, whose shim is gone, ran out of memory: the
// ladder's requeue, or a kernel OOM kill of a contained job (cgroup spec
// §15, 12): it ended by a SIGKILL nobody asked for, or with no result at
// all since OOMPolicy=kill takes the shim too, while the slice's oom_kill
// rose within two passes; "" for neither.
func (m *Model) requeueWhy(rec sched.Record, now time.Time) string {
	q := m.queue
	switch {
	case rec.Requeue != "":
		return rec.Requeue
	case rec.Cgroup != "" && rec.CancelReason == "" && (rec.Exit == nil || rec.Exit.Signal == "SIGKILL") &&
		!q.oomAt.IsZero() && now.Sub(q.oomAt) <= 2*queueInterval:
		return "the kernel killed it: out of memory"
	}
	return ""
}

// requeueJob puts job i, killed for memory, back in the queue as its next
// attempt (cgroup spec §8.4): its run is a floor, its files go to
// attempt-<n>, and it keeps its ID, owner, order and place; its owner is
// told.
func (m *Model) requeueJob(i int, why string, now time.Time) {
	q := m.queue
	rec := &q.store.Jobs[i]
	if err := sched.ArchiveAttempt(q.jobDir(rec.ID), rec.Attempt); err != nil {
		log.For("core").Warn("queue.requeue_failed", "job", int64(rec.ID), "err", err)
		rec.Failure = "killed: " + why + "; its requeue failed: " + err.Error()
		m.finishJob(i, sched.Failed, now)
		return
	}
	floor := *rec
	floor.State, floor.Ended = sched.Cancelled, now
	q.forgetRunning(rec.ID)
	m.recordRun(floor, nil)
	claim := rec.Claim
	claim.CPUs -= rec.Spare.CPUs
	claim.MemoryGB -= rec.Spare.MemoryGB
	*rec = sched.Record{
		ID: rec.ID, Order: rec.Order, Owner: rec.Owner, Agent: rec.Agent, Description: rec.Description,
		Argv: rec.Argv, Cwd: rec.Cwd, Claim: claim, Notify: rec.Notify, Tail: rec.Tail, State: sched.Queued,
		Submitted: rec.Submitted, Asked: rec.Asked, Signature: rec.Signature, Checkout: rec.Checkout,
		Unsized: rec.Unsized, Sources: rec.Sources, Learned: rec.Learned, AboveCap: rec.AboveCap,
		BelowKB: rec.BelowKB, AgentWhy: rec.AgentWhy, After: rec.After, Ready: rec.Ready,
		Attempt: rec.Attempt + 1, Requeues: rec.Requeues + 1, LastRequeue: why, NoRequeue: rec.NoRequeue,
	}
	log.For("core").Warn("queue.requeued", "job", int64(rec.ID), "attempt", rec.Attempt+1, "why", why)
	q.moved()
	m.sendRequeueNotice(*rec, why)
}
