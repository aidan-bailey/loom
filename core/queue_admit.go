package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

const (
	// defaultHandshake is how long a started shim has to write started.json
	// before the start counts as failed (spec §5.3).
	defaultHandshake = 5 * time.Second
	// startPoll is how often a start job looks for started.json.
	startPoll = 20 * time.Millisecond
	// agentUncontained is why an agent job runs outside the jobs' slice: it
	// claims nothing, so its scope would weigh least of all, and a contained
	// job is the ladder's to squeeze, pause or requeue.
	agentUncontained = "an agent job runs outside the jobs' slice"
)

var (
	// errFolderGone fails a job whose folder was removed while it waited.
	errFolderGone = errors.New("its folder is gone")
	// errRunnerSilent fails a job whose shim wrote no started.json in time.
	errRunnerSilent = errors.New("the job runner did not start")
)

// handshakeWait is the wait for started.json.
func (q *queueState) handshakeWait() time.Duration {
	if q.handshake > 0 {
		return q.handshake
	}
	return defaultHandshake
}

// hasQueued reports whether a job waits.
func (q *queueState) hasQueued() bool {
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State == sched.Queued {
			return true
		}
	}
	return false
}

// admitJobs runs an admission pass (spec §5.2). It rereads the scheduler
// block, so a hand edit applies without a restart, asks sched.Admit which
// queued jobs to start and why each of the others waits, and starts the first
// kind. While admission is paused (an invalid block, a jobs.json that failed
// to load) it starts nothing and says why on every queued job.
func (m *Model) admitJobs() {
	q := m.queue
	if !q.ready() {
		return
	}
	now := time.Now()
	q.lastPass = now
	m.reprobeHeld()
	policy, paused := m.refreshPolicy()
	waiting := map[sched.JobID]string{}
	var start, borrow []sched.JobID
	var spare map[sched.JobID]sched.Claim
	var starved sched.JobID
	var pred func(sched.Record, time.Time) sched.Prediction
	if paused == "" {
		m.maybeProbeConfiner(policy)
	}
	if paused != "" {
		for i := range q.store.Jobs {
			if q.store.Jobs[i].State == sched.Queued {
				waiting[q.store.Jobs[i].ID] = "admission paused: " + paused
			}
		}
	} else {
		// A job that has waited wait_seconds for its prior goes on without
		// it, and claims stay live until a job starts (smart scheduler spec
		// §5.4).
		m.releaseOverdueAsks(now)
		m.resizeQueued(policy)
		m.refreshHolds(policy, now)
		// A queued record whose lock could not be tested stays out of the
		// walk (a shim may be behind it, and it would run the command again
		// after it): it claims nothing and starts nothing.
		jobs := q.store.Jobs
		if len(q.unprobed) > 0 {
			jobs = slices.DeleteFunc(slices.Clone(jobs), func(r sched.Record) bool {
				err, held := q.unprobed[r.ID]
				if held && r.State == sched.Queued {
					waiting[r.ID] = "its lock could not be tested: " + err.Error()
				}
				return held
			})
		}
		var dec sched.Decision
		var bor sched.Borrowing
		g, greedy := m.greedyInputs(policy, now)
		if greedy {
			dec = sched.AdmitGreedy(jobs, policy, now, g)
			bor = sched.Borrow(jobs, dec, policy, now, g)
			m.makeFirm(bor.Firm)
		} else {
			dec = sched.Admit(jobs, policy, now, q.holding)
		}
		maps.Copy(waiting, dec.Waiting)
		granted := dec
		granted.Waiting = maps.Clone(dec.Waiting)
		for _, id := range bor.Start {
			delete(waiting, id)
			delete(granted.Waiting, id)
		}
		start, starved, borrow, pred = dec.Start, dec.Starved, bor.Start, g.Predict
		// Spare cores for the jobs it starts, when nothing else waits (job
		// scaling spec §5.3); a borrower gets none (memory borrowing spec
		// §5.2).
		spare = sched.Grant(granted, jobs, policy, q.holding, func(r sched.Record) *sched.Belief { return q.beliefOf(r.Signature) })
	}
	if !maps.Equal(waiting, q.waiting) {
		q.moved()
	}
	q.waiting = waiting
	started := false
	for _, id := range start {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].State == sched.Queued {
			m.startJob(i, policy, now, spare[id], id == starved)
			started = true
		}
	}
	for _, id := range borrow {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].State == sched.Queued {
			m.startJob(i, policy, now, sched.Claim{}, id == starved)
			m.markBorrower(i, pred, now)
			started = true
		}
	}
	if started {
		q.moved()
		m.saveQueueQuiet("start")
	}
}

// tickQueue keeps the queue current from the health tick, at most once a
// queueInterval: while jobs wait it runs an admission pass, which picks up a
// hand edit of the scheduler block within seconds; with none waiting it only
// rereads the block (refreshPolicy), so the published settings, policy and
// pause reason follow a hand edit on an idle queue too, and a client's caps
// form never opens on a stale block. A reread that changes nothing
// republishes nothing.
func (m *Model) tickQueue() {
	q := m.queue
	if !q.ready() {
		return
	}
	if q.hasQueued() {
		if time.Since(q.lastPass) >= queueInterval {
			m.admitJobs()
		}
		return
	}
	if time.Since(q.lastPolicy) >= queueInterval {
		m.refreshPolicy()
	}
}

// jobStart is a start job's input: copies only.
type jobStart struct {
	dir       string
	id        sched.JobID
	cwd       string
	hint      int // the CPU claim, which becomes the job's parallelism
	runner    sched.Runner
	handshake time.Duration
	// The scope the shim creates (nil: uncontained), the confiner that
	// ensures its slice first, and the slice's properties (cgroup spec §5).
	scope    *sched.ScopeSpec
	confiner sched.Confiner
	slice    sched.SliceProps
}

// jobStarted is a start job's result: the shim's pid and start time, or why
// the job failed to start.
type jobStarted struct {
	id      sched.JobID
	pid     int
	started time.Time
	err     error
	// owed is the pid of a shim the start gave up on (errRunnerSilent) without
	// proof that it was alive, so its group got no SIGKILL: the model keeps it
	// owed one (queueState.silent). 0 when nothing is owed.
	owed int
	// Confinement (cgroup spec §5): the scope and cgroup the shim reported,
	// why the job runs uncontained, and a slice call that failed, which
	// drops the confiner.
	unit, cgroup, uncontained string
	confineErr                error
}

// startJob admits job i of the store with spare added to its claim (job
// scaling spec §5.3; zero for none): it is running from now (its claim is
// taken by the next pass), and a model job starts its shim (spec §5.3).
func (m *Model) startJob(i int, policy sched.Policy, now time.Time, spare sched.Claim, starved bool) {
	q := m.queue
	rec := &q.store.Jobs[i]
	log.For("core").Info("queue.job_started", "job", int64(rec.ID), "signature", string(rec.Signature),
		"waited_s", int64(now.Sub(rec.QueuedSince()).Seconds()))
	rec.State, rec.Started, rec.PID, rec.Starved = sched.Running, now, 0, starved
	if spare != (sched.Claim{}) {
		rec.Claim.CPUs += spare.CPUs
		rec.Claim.MemoryGB += spare.MemoryGB
		rec.Spare = spare
	}
	q.starting[rec.ID] = true
	delete(q.waiting, rec.ID)
	hint := rec.Claim.Need(policy.Caps).CPUs
	var scope *sched.ScopeSpec
	rec.Unit, rec.Cgroup, rec.Uncontained = "", "", ""
	if rec.Kind == sched.KindAgent {
		rec.Uncontained = agentUncontained
	} else if why := q.containWhy(policy); why != "" {
		rec.Uncontained = why
	} else {
		scope = &sched.ScopeSpec{Unit: sched.ScopeName(q.dir, rec.ID, rec.Attempt), Slice: sched.SliceName(q.dir), CPUWeight: sched.CPUWeight(hint)}
	}
	m.spawnBackground(runJobStart(jobStart{
		dir: q.jobDir(rec.ID), id: rec.ID, cwd: rec.Cwd,
		hint:      hint,
		runner:    q.runner,
		handshake: q.handshakeWait(),
		scope:     scope,
		confiner:  q.confiner,
		slice:     sched.SliceProps{CPUWeight: uint64(policy.JobsCPUWeight), MemoryMaxBytes: uint64(policy.Caps.MemoryGB) << 30},
	}))
}

// runJobStart is the start job: it fails a job whose folder is gone, writes
// the CPU hint into spec.json, starts the shim and waits for started.json. A
// shim that wrote nothing in time has its group killed, but only if its lock
// proves it alive (signalAlive). A free lock does not prove the shim gone, since
// a slowed shim may take it later and run the command while its job reads
// failed, so the result hands the shim's pid back (owed) for gateQueue to keep
// testing.
func runJobStart(s jobStart) Job {
	return func() any {
		res := jobStarted{id: s.id}
		if _, err := os.Stat(s.cwd); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				res.err = errFolderGone
			} else {
				res.err = fmt.Errorf("its folder can't be read: %w", err)
			}
			return res
		}
		spec, err := sched.ReadSpec(s.dir)
		if err != nil {
			res.err = fmt.Errorf("its spec could not be read: %w", err)
			return res
		}
		if s.scope != nil {
			ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
			err := s.confiner.EnsureSlice(ctx, s.scope.Slice, s.slice)
			cancel()
			if err != nil {
				res.confineErr = err
				res.uncontained = "its slice could not be set: " + err.Error()
				s.scope = nil
			}
		}
		spec.HintCPUs, spec.Scope = s.hint, s.scope
		if err := sched.WriteSpec(s.dir, spec); err != nil {
			res.err = fmt.Errorf("its spec could not be written: %w", err)
			return res
		}
		pid, err := s.runner.Start(s.dir)
		if err != nil {
			res.err = fmt.Errorf("the job runner could not be started: %w", err)
			return res
		}
		deadline := time.Now().Add(s.handshake)
		for {
			if st, err := sched.ReadStarted(s.dir); err == nil {
				res.pid, res.started = st.PID, st.Started
				res.unit, res.cgroup = st.Unit, st.Cgroup
				if st.Uncontained != "" {
					res.uncontained = st.Uncontained
				}
				return res
			}
			if !time.Now().Before(deadline) {
				break
			}
			time.Sleep(startPoll)
		}
		sent, err := signalAlive(s.dir, pid, syscall.SIGKILL)
		if err != nil {
			log.For("core").Warn("queue.start_kill_failed", "job", int64(s.id), "err", err)
		}
		if !sent && pid > 1 {
			res.owed = pid
		}
		res.err = errRunnerSilent
		return res
	}
}

// deliverJobStarted records a start. A failed one ends the job: failed, or
// cancelled when a cancel was asked for meanwhile; the shim of one that timed
// out unproven stays owed a SIGKILL (queueState.silent), which the tick's
// next pass takes up. A good one records the
// shim's pid and start time, and a cancel asked for meanwhile is carried out
// by the pass this makes due.
func (m *Model) deliverJobStarted(r jobStarted) {
	q := m.queue
	delete(q.starting, r.id)
	if r.owed > 1 {
		// Whatever became of the record, the shim may still come up.
		q.silent[r.id] = silentShim{pid: r.owed, since: time.Now()}
	}
	if r.confineErr != nil {
		m.confinerFailed(r.confineErr)
	}
	i := q.index(r.id)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	if r.uncontained != "" {
		rec.Uncontained = r.uncontained
		if rec.Borrower {
			// Nothing could give it back (memory borrowing spec §10).
			rec.Borrower = false
			log.For("core").Warn("queue.borrowed_uncontained", "job", int64(rec.ID), "why", r.uncontained)
		}
	}
	if r.err != nil {
		rec.Failure = r.err.Error()
		state := sched.Failed
		if rec.CancelReason != "" {
			state = sched.Cancelled
		}
		m.finishJob(i, state, time.Now())
		m.afterSettle()
		return
	}
	rec.PID, rec.Started = r.pid, r.started
	rec.Unit, rec.Cgroup = r.unit, r.cgroup
	q.moved()
	m.saveQueueQuiet("start")
	if rec.CancelReason != "" {
		m.gate(gateQueue).request()
	}
	m.maybeQueue()
}
