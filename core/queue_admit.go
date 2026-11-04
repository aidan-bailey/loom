package core

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
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
	policy, paused := m.refreshPolicy()
	waiting := map[sched.JobID]string{}
	var start []sched.JobID
	if paused != "" {
		for i := range q.store.Jobs {
			if q.store.Jobs[i].State == sched.Queued {
				waiting[q.store.Jobs[i].ID] = "admission paused: " + paused
			}
		}
	} else {
		dec := sched.Admit(q.store.Jobs, policy, now)
		maps.Copy(waiting, dec.Waiting)
		start = dec.Start
	}
	if !maps.Equal(waiting, q.waiting) {
		q.moved()
	}
	q.waiting = waiting
	started := false
	for _, id := range start {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].State == sched.Queued {
			m.startJob(i, policy, now)
			started = true
		}
	}
	if started {
		q.moved()
		m.saveQueueQuiet("start")
	}
}

// tickQueue runs an admission pass from the health tick while jobs wait, at
// most once a queueInterval: a hand edit of the scheduler block applies
// within seconds.
func (m *Model) tickQueue() {
	q := m.queue
	if !q.ready() || !q.hasQueued() || time.Since(q.lastPass) < queueInterval {
		return
	}
	m.admitJobs()
}

// jobStart is a start job's input: copies only.
type jobStart struct {
	dir       string
	id        sched.JobID
	cwd       string
	hint      int // the CPU claim, which becomes the job's parallelism
	runner    sched.Runner
	handshake time.Duration
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
}

// startJob admits job i of the store: it is running from now (its claim is
// taken by the next pass), and a model job starts its shim (spec §5.3).
func (m *Model) startJob(i int, policy sched.Policy, now time.Time) {
	q := m.queue
	rec := &q.store.Jobs[i]
	rec.State, rec.Started, rec.PID = sched.Running, now, 0
	q.starting[rec.ID] = true
	delete(q.waiting, rec.ID)
	m.spawnBackground(runJobStart(jobStart{
		dir: q.jobDir(rec.ID), id: rec.ID, cwd: rec.Cwd,
		hint:      rec.Claim.Need(policy.Caps).CPUs,
		runner:    q.runner,
		handshake: q.handshakeWait(),
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
		spec.HintCPUs = s.hint
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
	i := q.index(r.id)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
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
	q.moved()
	m.saveQueueQuiet("start")
	if rec.CancelReason != "" {
		m.gate(gateQueue).request()
	}
	m.maybeQueue()
}
