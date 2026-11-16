package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// JobAction is what a ControlJob does to its job (job controls spec §5).
type JobAction string

// The job controls.
const (
	JobStart  JobAction = "start"  // start a queued job now, past the fit
	JobPark   JobAction = "park"   // keep a waiting job from starting
	JobUnpark JobAction = "unpark" // let a parked job start again
	JobPause  JobAction = "pause"  // freeze a running job, its memory to swap when that has room
	JobResume JobAction = "resume" // thaw a paused job
)

// JobControl is one ControlJob: the job, the session that must own it (0
// for any: the work view covers the host), and the action.
type JobControl struct {
	Job    sched.JobID
	Owner  InstanceID
	Action JobAction
}

// ControlJob carries out a job control (job controls spec §5), answered at
// once; a pause or resume is accepted then, and the job shows how it went.
func (m *Model) ControlJob(c JobControl, req ReqID) {
	m.answerQueueRequest(req, m.controlJob(c))
}

// controlJob checks c against its job and changes the record: start, park
// and unpark at once, then an admission pass; pause and resume as an intent
// the control step carries out. A failed save puts the record back and
// refuses.
func (m *Model) controlJob(c JobControl) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(c.Job)
	if i < 0 {
		return fmt.Errorf("there is no job %d", c.Job)
	}
	rec := &q.store.Jobs[i]
	if c.Owner != 0 && InstanceID(rec.Owner) != c.Owner {
		return fmt.Errorf("job %d is not this session's", c.Job)
	}
	prev := *rec
	var err error
	switch c.Action {
	case JobStart:
		err = m.forceJob(rec)
	case JobPark:
		err = parkJob(rec)
	case JobUnpark:
		err = unparkJob(rec, time.Now())
	case JobPause:
		err = m.pauseJob(rec)
	case JobResume:
		err = m.resumeJob(rec)
	default:
		return fmt.Errorf("job %d: unknown action %q", c.Job, c.Action)
	}
	if err != nil {
		return err
	}
	if err := m.saveQueue(); err != nil {
		*rec = prev
		return err
	}
	q.moved()
	log.For("core").Info("queue.controlled", "job", int64(c.Job), "action", string(c.Action), "owner_given", c.Owner != 0)
	switch c.Action {
	case JobStart, JobUnpark:
		m.admitJobs()
	case JobPause, JobResume:
		m.stepJobControls()
	}
	return nil
}

// unclaimedRefusal is why an agent or run job takes no control: the caps
// never hold it, nor does a scope of its own contain it. nil for any other
// job.
func unclaimedRefusal(rec *sched.Record) error {
	switch rec.Kind {
	case sched.KindAgent:
		return fmt.Errorf("job %d is an agent job: it starts by its account's agent slots", rec.ID)
	case sched.KindRun:
		return fmt.Errorf("job %d is a flow run: it starts at once", rec.ID)
	}
	return nil
}

// forceJob marks queued job rec forced (job controls spec §5.1), clearing a
// park; the pass after it starts it.
func (m *Model) forceJob(rec *sched.Record) error {
	switch {
	case rec.State == sched.Running:
		return fmt.Errorf("job %d is running", rec.ID)
	case rec.State == sched.Held:
		return fmt.Errorf("job %d waits for job %d to end", rec.ID, rec.After)
	case rec.State != sched.Queued:
		return fmt.Errorf("job %d has ended", rec.ID)
	}
	if err := unclaimedRefusal(rec); err != nil {
		return err
	}
	if _, paused := m.refreshPolicy(); paused != "" {
		return fmt.Errorf("admission paused: %s", paused)
	}
	if why := sched.LockBlock(m.queue.store.Jobs, *rec); why != "" {
		return fmt.Errorf("job %d %s", rec.ID, why)
	}
	rec.Forced, rec.Parked = true, false
	return nil
}

// parkJob parks queued or held job rec (job controls spec §5.2).
func parkJob(rec *sched.Record) error {
	switch {
	case rec.State == sched.Running:
		return fmt.Errorf("job %d is running: pause it instead", rec.ID)
	case !rec.State.Waiting():
		return fmt.Errorf("job %d has ended", rec.ID)
	case rec.Parked:
		return fmt.Errorf("job %d is parked already", rec.ID)
	}
	rec.Parked = true
	return nil
}

// unparkJob unparks rec at now, which restarts its starvation wait.
func unparkJob(rec *sched.Record, now time.Time) error {
	if !rec.Parked {
		return fmt.Errorf("job %d is not parked", rec.ID)
	}
	rec.Parked, rec.Unparked = false, now
	return nil
}

// pauseJob asks the control step to pause running job rec (job controls
// spec §5.3), refusing a job it can't freeze, one a pause would free
// nothing of, and one the ladder, the give-back or a cancel holds.
func (m *Model) pauseJob(rec *sched.Record) error {
	if rec.State != sched.Running {
		return fmt.Errorf("job %d is not running", rec.ID)
	}
	if err := unclaimedRefusal(rec); err != nil {
		return err
	}
	switch {
	case rec.Cgroup == "":
		return fmt.Errorf("job %d runs uncontained: there is no scope to freeze", rec.ID)
	case m.queue.confiner == nil:
		return fmt.Errorf("job %d can't be paused: the queue has no systemd bus", rec.ID)
	case rec.Claim.Exclusive:
		return fmt.Errorf("job %d is exclusive: it holds the host until it ends", rec.ID)
	case rec.Rung != "":
		return fmt.Errorf("the ladder holds job %d (%s)", rec.ID, rec.Rung)
	case !rec.LentAt.IsZero():
		return fmt.Errorf("job %d is lent to give memory back", rec.ID)
	case rec.Requeue != "":
		return fmt.Errorf("job %d is being requeued", rec.ID)
	case rec.CancelReason != "":
		return fmt.Errorf("job %d is being cancelled", rec.ID)
	case rec.Control == sched.ControlPause:
		return fmt.Errorf("job %d is pausing", rec.ID)
	case rec.Control == sched.ControlResume:
		return fmt.Errorf("job %d is resuming", rec.ID)
	case rec.Paused():
		return fmt.Errorf("job %d is already paused", rec.ID)
	}
	rec.Control, rec.ControlErr = sched.ControlPause, ""
	return nil
}

// resumeJob asks the control step to resume paused job rec.
func (m *Model) resumeJob(rec *sched.Record) error {
	switch {
	case rec.Control == sched.ControlResume:
		return fmt.Errorf("job %d is resuming", rec.ID)
	case rec.State != sched.Running || !rec.Paused():
		return fmt.Errorf("job %d is not paused", rec.ID)
	case m.queue.confiner == nil:
		return fmt.Errorf("job %d can't be resumed: the queue has no systemd bus", rec.ID)
	}
	rec.Control, rec.ControlErr = sched.ControlResume, ""
	return nil
}

// jobControlDone is a control step's call that returned: the freeze's or
// thaw's error, and after a freeze whether the reclaim ran and its error.
type jobControlDone struct {
	job        sched.JobID
	kind       sched.ControlKind
	err        error
	reclaimed  bool
	reclaimKB  int64
	reclaimErr error
}

// stepJobControls carries out one pause or resume (job controls spec §6.2),
// one confinement call at a time with the ladder's and the give-back's
// (q.laddering): the first running job with a Control, or a cancelled
// paused job, which is thawed so it can act on its SIGTERM.
func (m *Model) stepJobControls() {
	q := m.queue
	if q.laddering || q.confiner == nil {
		return
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || rec.Cgroup == "" {
			continue
		}
		kind := rec.Control
		if kind == "" && rec.Paused() && rec.CancelReason != "" {
			kind = sched.ControlResume
		}
		if kind != "" {
			m.applyJobControl(i, kind)
			return
		}
	}
}

// applyJobControl spawns job i's call: a pause freezes the scope and, when
// SwapFree has room for its working set (the give-back's check), reclaims
// it; a resume thaws it. The record changes only when the call returns.
func (m *Model) applyJobControl(i int, kind sched.ControlKind) {
	q := m.queue
	rec := q.store.Jobs[i]
	c, root, unit, cg, kb := q.confiner, q.cgroupRoot, rec.Unit, rec.Cgroup, q.usage[rec.ID].RSSKB
	swapKB, swapErr := readSwapFree()
	reclaim := kind == sched.ControlPause && swapErr == nil && kb > 0 && swapKB >= kb
	q.laddering = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		done := jobControlDone{job: rec.ID, kind: kind}
		switch kind {
		case sched.ControlPause:
			if done.err = c.Freeze(ctx, unit); done.err == nil && reclaim {
				done.reclaimErr = sched.CgroupReclaim(root, cg, kb)
				done.reclaimed, done.reclaimKB = done.reclaimErr == nil, kb
			}
		case sched.ControlResume:
			done.err = c.Thaw(ctx, unit)
		}
		return done
	})
}

// deliverJobControlDone records a control call that returned. A failure
// leaves the job as it was, with ControlErr on its row; a pause stamps
// PausedAt when the freeze returned, so the reconcile's thawed-by-hand case
// trusts only samples taken after it.
func (m *Model) deliverJobControlDone(r jobControlDone) {
	q := m.queue
	q.laddering = false
	i := q.index(r.job)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	rec.Control = ""
	if r.err != nil {
		rec.ControlErr = fmt.Sprintf("%s failed: %v", r.kind, r.err)
		log.For("core").Warn("queue.control_failed", "job", int64(rec.ID), "action", string(r.kind), "err", r.err)
		q.moved()
		m.saveQueueQuiet("control")
		return
	}
	rec.ControlErr = ""
	switch r.kind {
	case sched.ControlPause:
		rec.PausedAt, rec.PauseReclaimed, rec.Laddered = time.Now(), r.reclaimed, true
		log.For("core").Info("queue.paused", "job", int64(rec.ID), "reclaimed", r.reclaimed, "reclaimed_kb", r.reclaimKB)
		if r.reclaimErr != nil {
			log.For("core").Warn("queue.reclaim_failed", "job", int64(rec.ID), "err", r.reclaimErr)
		}
	case sched.ControlResume:
		paused := time.Since(rec.PausedAt)
		rec.PausedSecs += int64(paused.Seconds())
		rec.PausedAt, rec.PauseReclaimed = time.Time{}, false
		log.For("core").Info("queue.unpaused", "job", int64(rec.ID), "paused_s", int64(paused.Seconds()))
	}
	q.moved()
	m.saveQueueQuiet("control")
	if q.hasQueued() {
		m.admitJobs() // a pause makes room
	}
}
