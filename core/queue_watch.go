package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

const (
	// sampleTimeout bounds the one `ps` of a pass (spec §5.4).
	sampleTimeout = 5 * time.Second
	// defaultEscalation is how long after a cancel's SIGTERM a job whose shim
	// still holds its lock gets SIGKILL (spec §5.5).
	defaultEscalation = 10 * time.Second
)

// escalateAfter is the time between a cancel's SIGTERM and its SIGKILL.
func (q *queueState) escalateAfter() time.Duration {
	if q.escalation > 0 {
		return q.escalation
	}
	return defaultEscalation
}

// endJob records, on rec itself, that it ended in state at now: the change a
// job's end makes to its record, without its consequences (settledJob), so a
// request can undo it if its write fails.
func endJob(rec *sched.Record, state sched.State, now time.Time) {
	rec.State = state
	if rec.Ended.IsZero() {
		rec.Ended = now
	}
}

// settledJob does what a job's end brings with it: its usage, trace, hold
// and wait reasons go, a client is told (rev), its run is recorded, and the jobs held
// after it are released or cancelled, with its message and theirs
// (settleChain). Job i of the store has already been ended (endJob).
func (m *Model) settledJob(i int) {
	q := m.queue
	rec := q.store.Jobs[i]
	trace := q.traces[rec.ID]
	delete(q.usage, rec.ID)
	delete(q.cpuMarks, rec.ID)
	delete(q.traces, rec.ID)
	delete(q.holds, rec.ID)
	delete(q.holding, rec.ID)
	delete(q.lockLogged, rec.ID)
	delete(q.waiting, rec.ID)
	delete(q.starting, rec.ID)
	q.moved()
	m.recordRun(rec, trace)
	m.settleChain(rec)
}

// finishJob ends job i of the store in state and settles it.
func (m *Model) finishJob(i int, state sched.State, now time.Time) {
	endJob(&m.queue.store.Jobs[i], state, now)
	m.settledJob(i)
}

// afterSettle runs after one or more jobs ended: their claims are free, so an
// admission pass follows; retention trims the finished jobs; and the write
// that follows is quiet (spec §5.9).
func (m *Model) afterSettle() {
	m.admitJobs()
	m.retainFinished()
	m.saveQueueQuiet("settle")
}

// lockHeld reports whether a shim holds jobDir's lock (sched.Probe). A job
// directory with no lock file has had no shim take it.
func lockHeld(jobDir string) (bool, error) {
	alive, err := sched.Probe(jobDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return alive, err
}

// signalAlive sends sig to the process group pgid (the shim's pid: the shim
// leads its group) only when jobDir's lock test proves its shim alive. A pid
// recorded in a file may have been reused by a stranger, and pid 0 and 1
// would name loom's own group and everyone's, so a signal goes only where the
// lock proves the pid is still the shim's. It reports whether it signalled.
func signalAlive(jobDir string, pgid int, sig syscall.Signal) (sent bool, err error) {
	if pgid <= 1 {
		return false, fmt.Errorf("no process group to signal (pid %d)", pgid)
	}
	alive, err := lockHeld(jobDir)
	if err != nil {
		return false, fmt.Errorf("test the job's lock: %w", err)
	}
	if !alive {
		return false, nil
	}
	if err := sched.SignalGroup(pgid, sig); err != nil {
		return false, fmt.Errorf("signal the job's group: %w", err)
	}
	return true, nil
}

// passJob is one record gateQueue looks at, copied from the store.
type passJob struct {
	id          sched.JobID
	pid         int  // 0: not recorded yet
	queued      bool // a queued record, probed at boot only
	cancelled   bool // a cancel was requested
	signalledAt time.Time
	silent      bool // not a record: a failed start's shim, still owed a SIGKILL (queueState.silent)
}

// queuePass is gateQueue's input: copies only, since its job runs off the loop.
type queuePass struct {
	dir      string
	now      time.Time
	escalate time.Duration
	jobs     []passJob
}

// jobObs is what a pass saw of one job.
type jobObs struct {
	id      sched.JobID
	queued  bool
	pid     int            // the shim's, from the record or started.json
	err     error          // the lock test failed: nothing is known
	gone    bool           // the lock is free: the shim is gone
	started *sched.Started // started.json, read when the record had no pid
	exit    *sched.Exit    // exit.json, read once the lock was free
	termAt  time.Time      // SIGTERM went to the group now
	killed  bool           // SIGKILL went to the group
	silent  bool           // a failed start's shim (passJob.silent)
	cleared bool           // silent only: its SIGKILL went, or the shim is gone, so nothing is owed
	// signalErr is why a signal failed.
	signalErr error
}

// queuePassResult is gateQueue's result: what each job's lock and result
// files said, and the one process sample (nil when it failed or timed out).
type queuePassResult struct {
	at        time.Time
	obs       []jobObs
	snap      *sched.Snapshot
	sampleErr error
}

// maybeQueue dispatches gateQueue's pass when it is due: nothing when no job
// runs (or is owed a SIGKILL as a failed start's shim), or its start job is
// still in flight.
func (m *Model) maybeQueue() bool {
	if !m.queue.ready() {
		return false
	}
	return m.dispatchGated(gateQueue, time.Now(), func() Job {
		pass := m.queuePassFor(false)
		if len(pass.jobs) == 0 {
			return nil
		}
		return func() any { return runQueuePass(pass) }
	})
}

// queuePassFor snapshots the records a pass looks at: the running ones whose
// start has landed and, at boot (withQueued), the queued ones too, since a
// start whose save failed leaves a running shim behind a queued record. It
// adds the shims of failed starts that are still owed a SIGKILL, and drops
// those owed for silentLimit.
func (m *Model) queuePassFor(withQueued bool) queuePass {
	q := m.queue
	p := queuePass{dir: q.dir, now: time.Now(), escalate: q.escalateAfter()}
	for id, s := range q.silent {
		if p.now.Sub(s.since) >= silentLimit {
			log.For("core").Warn("queue.silent_shim_given_up", "job", int64(id), "pid", s.pid)
			delete(q.silent, id)
			continue
		}
		p.jobs = append(p.jobs, passJob{id: id, pid: s.pid, silent: true})
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		switch {
		case rec.State == sched.Running && !q.starting[rec.ID]:
			p.jobs = append(p.jobs, passJob{id: rec.ID, pid: rec.PID, cancelled: rec.CancelReason != "", signalledAt: rec.SignalledAt})
		case withQueued && rec.State == sched.Queued:
			p.jobs = append(p.jobs, passJob{id: rec.ID, queued: true})
		}
	}
	return p
}

// runQueuePass is gateQueue's job (and Boot's adoption): for each job it tests
// the lock without blocking, reads the result files of one whose lock is free,
// and carries out a cancel (SIGTERM, then SIGKILL after the escalation) on one
// whose lock is held; then it takes one process sample and kills the group of
// a job whose shim vanished without a result, if the sample proves the group
// is the job's. A failed start's shim still owed a SIGKILL gets its lock
// tested too (watchSilent). It reads no model state.
func runQueuePass(p queuePass) queuePassResult {
	res := queuePassResult{at: p.now}
	running := false
	for _, j := range p.jobs {
		dir := sched.JobDir(p.dir, j.id)
		if j.silent {
			ob := watchSilent(dir, j)
			running = running || ob.gone
			res.obs = append(res.obs, ob)
			continue
		}
		ob := jobObs{id: j.id, queued: j.queued, pid: j.pid}
		alive, err := lockHeld(dir)
		switch {
		case err != nil:
			ob.err = err
		case alive:
			if ob.pid == 0 {
				ob.started, ob.pid = readStarted(dir)
			}
			if j.cancelled && !j.queued {
				tendCancel(&ob, dir, j, p)
			}
		default:
			ob.gone = true
			if exit, err := sched.ReadExit(dir); err == nil {
				ob.exit = &exit
			}
			if ob.pid == 0 {
				ob.started, ob.pid = readStarted(dir)
			}
		}
		if !j.queued && ob.err == nil {
			running = true
		}
		res.obs = append(res.obs, ob)
	}
	if running {
		res.snap, res.sampleErr = sample()
	}
	for i := range res.obs {
		ob := &res.obs[i]
		if ob.silent {
			clearSilent(ob, res.snap)
			continue
		}
		if !ob.gone || ob.exit != nil || ob.queued || res.snap == nil || !orphanedGroup(*res.snap, ob.pid) {
			continue
		}
		if err := sched.SignalGroup(ob.pid, syscall.SIGKILL); err != nil {
			ob.signalErr = err
			continue
		}
		ob.killed = true
	}
	return res
}

// watchSilent tests the lock of a failed start's shim (spec §5.3 step 4: the
// start timed out and the shim's group is to die). A held lock proves the shim
// alive: its group gets SIGKILL now, through signalAlive, and nothing is owed
// any more. A free lock proves nothing yet, since a shim slowed down may not
// have taken it: the entry stays, and clearSilent decides from the sample.
func watchSilent(dir string, j passJob) jobObs {
	ob := jobObs{id: j.id, pid: j.pid, silent: true}
	alive, err := lockHeld(dir)
	switch {
	case err != nil:
		ob.err = err
	case alive:
		ob.killed, ob.signalErr = signalAlive(dir, j.pid, syscall.SIGKILL)
		ob.cleared = ob.killed
	default:
		ob.gone = true
	}
	return ob
}

// clearSilent decides, from the pass's sample, whether a silent shim whose
// lock was free is gone: no process has its pid, so it cannot take the lock
// any more. Members left in its group (a command it started and died on) are
// sent SIGKILL under the lost-job rule (orphanedGroup); an empty group needs
// nothing. A process that still has the pid (the shim not yet up, or a
// stranger that reused it) keeps the entry, and so does a failed sample.
func clearSilent(ob *jobObs, snap *sched.Snapshot) {
	if !ob.gone || snap == nil {
		return
	}
	for _, proc := range snap.Procs {
		if proc.PID == ob.pid {
			return
		}
	}
	if orphanedGroup(*snap, ob.pid) {
		if err := sched.SignalGroup(ob.pid, syscall.SIGKILL); err != nil {
			ob.signalErr = err
			return
		}
		ob.killed = true
	}
	ob.cleared = true
}

// tendCancel carries out the cancel of a job whose lock is held: SIGTERM to
// its group once, and SIGKILL once the escalation has passed since. Each goes
// through signalAlive, which tests the lock again.
func tendCancel(ob *jobObs, dir string, j passJob, p queuePass) {
	switch {
	case j.signalledAt.IsZero():
		sent, err := signalAlive(dir, ob.pid, syscall.SIGTERM)
		ob.signalErr = err
		if sent {
			ob.termAt = p.now
		}
	case p.now.Sub(j.signalledAt) >= p.escalate:
		sent, err := signalAlive(dir, ob.pid, syscall.SIGKILL)
		ob.signalErr = err
		ob.killed = sent
	}
}

// readStarted reads a job's started.json: the record and the shim's pid, nil
// and 0 when there is none.
func readStarted(jobDir string) (*sched.Started, int) {
	st, err := sched.ReadStarted(jobDir)
	if err != nil {
		return nil, 0
	}
	return &st, st.PID
}

// sample takes the one process sample of a pass, within sampleTimeout.
func sample() (*sched.Snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
	defer cancel()
	snap, err := sched.Sample(ctx)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// orphanedGroup reports whether snap proves the process group pgid a dead
// shim's: it has members, and no process has the shim's pid. A live process
// with that pid is the shim itself or a stranger that reused the pid, and its
// group is never signalled; pid 0 and 1 name loom's own group and everyone's.
func orphanedGroup(snap sched.Snapshot, pgid int) bool {
	if pgid <= 1 {
		return false
	}
	for _, p := range snap.Procs {
		if p.PID == pgid {
			return false
		}
	}
	_, _, members, _ := snap.Group(pgid)
	return members > 0
}

// deliverQueuePass applies a pass on the loop: a job whose lock was free is
// settled, one whose lock is held records its pid and its SIGTERM, and usage
// is updated from the sample. An observation of a record that is no longer in
// the state the pass saw it in (a cancel, a settle, a drop since) is ignored.
func (m *Model) deliverQueuePass(r queuePassResult) {
	q := m.queue
	if !q.ready() {
		return
	}
	switch {
	case r.sampleErr != nil:
		// ps failing leaves usage frozen at its last values: say so once per
		// run of failures, not at every pass.
		if !q.sampleFailing {
			log.For("core").Warn("queue.sample_failed", "err", r.sampleErr)
		}
		q.sampleFailing = true
	case r.snap != nil:
		q.sampleFailing = false
	}
	settled, changed := false, false
	for _, ob := range r.obs {
		if ob.silent {
			m.noteSilent(ob)
			continue
		}
		i := q.index(ob.id)
		if i < 0 {
			continue
		}
		want := sched.Running
		if ob.queued {
			want = sched.Queued
		}
		if q.store.Jobs[i].State != want {
			continue
		}
		if ob.signalErr != nil {
			log.For("core").Warn("queue.signal_failed", "job", int64(ob.id), "err", ob.signalErr)
		}
		repeated := false
		if ob.queued {
			// Boot's probe, and reprobeHeld's: a failed one holds the record
			// out of admission, any answer releases it. A failure the record
			// already held with the same text is not logged again: it comes
			// at every pass.
			if ob.err != nil {
				prev, held := q.unprobed[ob.id]
				repeated = held && prev != nil && prev.Error() == ob.err.Error()
				q.unprobed[ob.id] = ob.err
			} else {
				delete(q.unprobed, ob.id)
			}
		}
		if ob.err == nil {
			delete(q.lockLogged, ob.id)
		}
		switch {
		case ob.err != nil:
			if ob.queued && !repeated {
				log.For("core").Warn("queue.lock_test_failed", "job", int64(ob.id), "err", ob.err)
			} else if !ob.queued && q.lockLogged[ob.id] != ob.err.Error() {
				// A running record: the same failure comes at every pass.
				log.For("core").Warn("queue.lock_test_failed", "job", int64(ob.id), "err", ob.err)
				q.lockLogged[ob.id] = ob.err.Error()
			}
		case ob.gone && ob.queued && ob.exit == nil:
			// A queued record whose shim never ran: it stays queued.
		case ob.gone:
			m.settleGone(i, ob, r.at)
			settled = true
		default:
			if m.noteAlive(i, ob, r.at) {
				changed = true
			}
		}
	}
	m.applyUsage(r)
	m.refreshHolds(q.policy, time.Now())
	switch {
	case settled:
		m.afterSettle()
	case changed:
		m.saveQueueQuiet("watch")
	}
}

// noteSilent applies what a pass saw of a failed start's shim: its entry goes
// once its SIGKILL went or the shim is gone, and stays after a lock test or a
// signal that failed.
func (m *Model) noteSilent(ob jobObs) {
	q := m.queue
	if _, ok := q.silent[ob.id]; !ok {
		return
	}
	if ob.signalErr != nil {
		log.For("core").Warn("queue.signal_failed", "job", int64(ob.id), "err", ob.signalErr)
	}
	if ob.err != nil {
		// At every pass until the entry goes: say it when the text is new.
		if sh := q.silent[ob.id]; sh.lastErr != ob.err.Error() {
			log.For("core").Warn("queue.lock_test_failed", "job", int64(ob.id), "err", ob.err)
			sh.lastErr = ob.err.Error()
			q.silent[ob.id] = sh
		}
		return
	}
	if sh := q.silent[ob.id]; sh.lastErr != "" {
		sh.lastErr = "" // the test answered: a later failure is news again
		q.silent[ob.id] = sh
	}
	if ob.killed {
		log.For("core").Warn("queue.silent_shim_killed", "job", int64(ob.id), "pgid", ob.pid)
	}
	if ob.cleared {
		delete(q.silent, ob.id)
	}
}

// noteAlive records what a pass saw of a job whose lock is held: a shim behind
// a queued record (a start whose save failed) makes it running, so it is
// never started twice; a pid and start time it learned from started.json; and
// the moment its group got SIGTERM. It reports whether the record changed.
func (m *Model) noteAlive(i int, ob jobObs, now time.Time) bool {
	q := m.queue
	rec := &q.store.Jobs[i]
	changed := false
	if rec.State == sched.Queued {
		rec.State, rec.Started, changed = sched.Running, now, true
		rec.Adopted = true // its peak is sampled from now on: a floor
		delete(q.waiting, rec.ID)
	}
	if ob.started != nil && rec.PID == 0 {
		rec.PID, rec.Started, changed = ob.started.PID, ob.started.Started, true
	}
	if !ob.termAt.IsZero() && rec.SignalledAt.IsZero() {
		rec.SignalledAt, changed = ob.termAt, true
	}
	if changed {
		q.moved()
	}
	return changed
}

// settleGone settles job i, whose shim is gone, from what the pass read
// (spec §5.4). Cancelled when a cancel was requested, whatever the exit; else
// done for exit 0, failed for another code, a signal or a start error, and
// lost when exit.json is missing or unreadable.
func (m *Model) settleGone(i int, ob jobObs, now time.Time) {
	rec := &m.queue.store.Jobs[i]
	if ob.started != nil && rec.PID == 0 {
		rec.PID, rec.Started = ob.started.PID, ob.started.Started
	}
	cancelled := rec.CancelReason != ""
	state := sched.Lost
	if e := ob.exit; e != nil {
		x := *e
		rec.Exit, rec.Ended = &x, x.Ended
		rec.PeakRSSKB = max(rec.PeakRSSKB, x.MaxRSSKB)
		switch {
		case x.Error != "":
			rec.Failure, state = x.Error, sched.Failed
		case x.Code == 0 && x.Signal == "":
			state = sched.Done
		default:
			state = sched.Failed
		}
	} else if !cancelled {
		rec.Failure = lostWhy
	}
	if cancelled {
		state = sched.Cancelled
	}
	if ob.killed {
		log.For("core").Warn("queue.lost_group_killed", "job", int64(rec.ID), "pgid", ob.pid)
	}
	m.finishJob(i, state, now)
}

// applyUsage turns the pass's sample into each running job's usage: its
// session's memory now (the shim leads the job's session; sccache
// measurement spec §5.2), the peak so far, and the cores used since the last
// sample; adds a point to the trace of a job not adopted, and breaks a hold
// the usage passed (past-peak holds spec §5.1, §5.4). A pass whose sample
// failed leaves the last values.
func (m *Model) applyUsage(r queuePassResult) {
	q := m.queue
	if r.snap == nil {
		return
	}
	for _, ob := range r.obs {
		if ob.gone || ob.err != nil || ob.queued || ob.silent {
			continue
		}
		i := q.index(ob.id)
		if i < 0 {
			continue
		}
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || rec.PID <= 1 {
			continue
		}
		rss, cpu, _ := r.snap.Session(rec.PID)
		cores := 0.0
		if mark, ok := q.cpuMarks[ob.id]; ok {
			if dt := r.snap.Taken.Sub(mark.at); dt > 0 {
				cores = max(0, (cpu-mark.cpu).Seconds()) / dt.Seconds()
			}
		}
		q.cpuMarks[ob.id] = cpuMark{cpu: cpu, at: r.snap.Taken}
		if !rec.Adopted {
			tr := q.traces[ob.id]
			if tr == nil {
				tr = &sched.Trace{}
				q.traces[ob.id] = tr
			}
			tr.Add(r.snap.Taken, sched.TracePoint{X: cpu.Seconds(), RSSKB: rss, Cores: cores})
		}
		if what := q.holds[ob.id].Exceeded(rss, cores); what != "" {
			m.breakHold(rec, what, rss, cores)
		}
		if u := (jobUsage{RSSKB: rss, CPU: cores}); q.usage[ob.id] != u {
			q.usage[ob.id] = u
			q.moved()
		}
		if rss > rec.PeakRSSKB {
			rec.PeakRSSKB = rss
			q.moved()
		}
	}
}
