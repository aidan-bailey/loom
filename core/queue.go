package core

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// queueInterval is gateQueue's cadence: how often the running jobs' locks are
// tested, their usage sampled and their ends settled (spec §5.4). It is also
// the least time between the admission passes the tick starts (tickQueue).
const queueInterval = 3 * time.Second

// jobsDirName is the queue's directory under the global config dir.
const jobsDirName = "jobs"

// silentLimit is how long a failed start's shim stays owed a SIGKILL: a shim
// that has not taken its lock by then is not coming (or its pid is a
// stranger's), and the entry is dropped.
const silentLimit = 10 * time.Minute

// queueGOOS is runtime.GOOS, a variable so a test can stand on Windows, where
// the work queue is paused from Boot on and refuses every submission.
var queueGOOS = runtime.GOOS

// silentShim is a shim whose job failed because it wrote no started.json in
// time, and whose lock never proved it alive, so its group has not been sent
// SIGKILL yet: it may still take its lock and run the command, unaccounted.
type silentShim struct {
	pid   int       // the shim's pid, which leads its group
	since time.Time // when the start failed
	// lastErr is the text of the lock test's last failure that was logged, so
	// a run of the same failure is logged once.
	lastErr string
}

// jobUsage is what a running job's process group uses, as the last sample
// saw it.
type jobUsage struct {
	RSSKB int64
	CPU   float64 // cores
}

// cpuMark is a job's cumulative CPU time at a sample, which the next sample
// turns into cores.
type cpuMark struct {
	cpu time.Duration
	at  time.Time
}

// queueState is the work queue (spec §5): every job record, what admission
// last decided and what the last sample saw. The model owns it and touches
// it only on its loop; the jobs it spawns get snapshots of what they need.
type queueState struct {
	dir      string                   // <globalDir>/jobs
	store    sched.Store              // every record
	loadErr  error                    // jobs.json failed to load: submissions refused, admission paused
	host     sched.Host               // detected once, at boot
	settings config.SchedulerSettings // the scheduler block as last read
	policy   sched.Policy             // in effect after the last pass; zero while paused
	paused   string                   // why admission is paused; "" when it isn't
	waiting  map[sched.JobID]string   // from the last pass
	usage    map[sched.JobID]jobUsage // running jobs, from the last sample
	runner   sched.Runner
	rev      uint64 // bumped by every change to the queue, a job or its usage

	// What follows is private to the model's queue code; no other story reads
	// it.

	// cfgDir is the global config dir, where the scheduler block lives.
	cfgDir string
	// runnerErr is why no runner could be built (sched.NewRunner), or
	// sched.ErrUnsupported on Windows: admission pauses with its text, and
	// ErrUnsupported also refuses every submission.
	runnerErr error
	// starting holds the jobs whose start job is in flight: gateQueue leaves
	// them alone, since their shim may not hold its lock yet.
	starting map[sched.JobID]bool
	// silent holds the shims of jobs whose start failed on the 5 s wait with
	// no proof the shim was alive (a free lock), so no SIGKILL went to their
	// groups. gateQueue keeps testing each: it sends the SIGKILL once the lock
	// shows the shim alive, and drops the entry once the shim is gone (or after
	// silentLimit). In memory only.
	silent map[sched.JobID]silentShim
	// cpuMarks is each running job's CPU time at the last sample.
	cpuMarks map[sched.JobID]cpuMark
	// traces is each running job's samples since its start (past-peak holds
	// spec §5.1), holds where each one's hold stands (sched.HoldState), and
	// holding what each counts at for admission, from the last refreshHolds.
	// In memory only: settledJob drops a job's entries.
	traces  map[sched.JobID]*sched.Trace
	holds   map[sched.JobID]sched.HoldState
	holding map[sched.JobID]sched.Need
	// limits is the accounts a usage limit holds (usage-limit hold spec §2),
	// from the logs of agent jobs that failed on one (noteUsageLimit). In
	// memory only: a restart forgets it, and the next agent to hit the limit
	// records it again.
	limits sched.AccountLimits
	// doomed holds the IDs retention dropped from the store, whose
	// directories go once a write of the shorter jobs.json succeeds.
	doomed []sched.JobID
	// lastPass is when the last admission pass ran, to throttle the tick's.
	lastPass time.Time
	// lastPolicy is when the scheduler block was last read (refreshPolicy),
	// to throttle the tick's read while no job waits.
	lastPolicy time.Time
	// handshake and escalation override the start's 5 s wait for started.json
	// and the 10 s between a cancel's SIGTERM and SIGKILL; 0 keeps them. Tests
	// shorten them.
	handshake, escalation time.Duration
	// saveFailed is set while the last quiet save failed, so a run of failures
	// raises one notice.
	saveFailed bool
	// sampleFailing is set while the last sample failed (ps), so a run of
	// failures is logged once.
	sampleFailing bool
	// unprobed holds the queued records whose lock test failed, with its
	// error: a shim may be behind one, so admission leaves it out and
	// reprobeHeld tests it again before every pass, until the lock answers.
	// In memory only.
	unprobed map[sched.JobID]error
	// lockLogged holds, per running job whose lock test failed, the text of
	// the failure last logged, so the same failure at every pass (3 s) is
	// logged once. An answer, or the job's end, forgets it. In memory only.
	lockLogged map[sched.JobID]string
	// reprobing is set while reprobeHeld runs, so the pass its settles bring
	// does not test again.
	reprobing bool

	// beliefs is the registry of what each signature measured
	// (beliefs.json), loaded with the queue (smart scheduler spec §5.3).
	beliefs sched.Beliefs
	// beliefsErr is why beliefs.json failed to load: learning is off and
	// the file is never written.
	beliefsErr error
	// families memoizes the beliefs' families (family sizing spec §5.2);
	// only the loop reads it.
	families sched.Families
	// beliefsSaveFailed is set while the last save of beliefs.json failed,
	// so a run of failures raises one notice.
	beliefsSaveFailed bool
	// advisor is the scheduler's agent (smart scheduler spec §5.6).
	advisor advisorState

	// Confinement (cgroup spec §5): the user's service manager once a probe
	// reached it, or why it couldn't; whether a probe is in flight and when
	// the last one ran. cgroupRoot is sched.CgroupRoot but in tests. availKB
	// is the host's MemAvailable and slice the jobs slice's reading, from
	// the last pass; cgroupLogged holds, per running job, the cgroup read
	// failure last logged.
	confiner     sched.Confiner
	confineErr   error
	probing      bool
	lastProbe    time.Time
	cgroupRoot   string
	availKB      int64
	slice        *sched.CgroupSample
	cgroupLogged map[sched.JobID]string

	// The ladder (cgroup spec §8): its state between passes, whether an
	// action is in flight, the last failed call (shown until one succeeds),
	// the slice's MemoryHigh as last set and whether a set is in flight, and
	// when the slice's oom_kill last rose.
	ladder       sched.LadderState
	laddering    bool
	ladderErr    string
	sliceHighKB  int64
	sliceSetting bool
	oomAt        time.Time

	// Memory borrowing (spec 2026-11-06 §6): the give-back machine's state,
	// and the borrowers whose freeze failed, which are given back by requeue.
	giveBack sched.GiveBackState
	noLend   map[sched.JobID]bool

	// Exploring (spec 2026-11-08 §4.3): the host's CPU readings of the last
	// 2 × IdleWindow, oldest first, taken on the tick, and whether the last
	// read failed, so a run of failures is logged once. In memory only.
	hostCPU        []sched.CPUReading
	hostCPUFailing bool
}

func newQueueState() *queueState {
	return &queueState{
		waiting:    map[sched.JobID]string{},
		usage:      map[sched.JobID]jobUsage{},
		starting:   map[sched.JobID]bool{},
		silent:     map[sched.JobID]silentShim{},
		cpuMarks:   map[sched.JobID]cpuMark{},
		traces:     map[sched.JobID]*sched.Trace{},
		holds:      map[sched.JobID]sched.HoldState{},
		holding:    map[sched.JobID]sched.Need{},
		limits:     sched.AccountLimits{},
		unprobed:   map[sched.JobID]error{},
		lockLogged: map[sched.JobID]string{},
		noLend:     map[sched.JobID]bool{},

		cgroupRoot:   sched.CgroupRoot,
		cgroupLogged: map[sched.JobID]string{},
	}
}

// ready reports whether the queue has a directory: loadQueue ran.
func (q *queueState) ready() bool { return q != nil && q.dir != "" }

// moved records a change a client would see (QueueChanged follows rev).
func (q *queueState) moved() { q.rev++ }

// index is the position of job id in the store, -1 for none. Positions are
// valid until the store is next appended to or trimmed.
func (q *queueState) index(id sched.JobID) int {
	for i := range q.store.Jobs {
		if q.store.Jobs[i].ID == id {
			return i
		}
	}
	return -1
}

// keyed is the job owner submitted with key that wasn't cancelled: a
// submission's idempotency key (sched.Submit.Key). A cancelled one is no
// answer, since a chain refused partway cancels what it queued and a later
// try must queue again; a finished one is, since it ran while its client
// was away.
func (q *queueState) keyed(key string, owner InstanceID) (sched.Record, bool) {
	for _, rec := range q.store.Jobs {
		if rec.Key == key && rec.Owner == uint64(owner) && rec.State != sched.Cancelled {
			return rec, true
		}
	}
	return sched.Record{}, false
}

// jobDir is job id's directory.
func (q *queueState) jobDir(id sched.JobID) string { return sched.JobDir(q.dir, id) }

// pauseReason is why admission is paused, "" when it isn't: the queue did not
// load, no runner could be built, or the scheduler block is invalid
// (resolveErr, from sched.Resolve). A basic reason hides the ones above it,
// and the queue being unavailable (Windows) hides them all.
func (q *queueState) pauseReason(resolveErr error) string {
	switch {
	case errors.Is(q.runnerErr, sched.ErrUnsupported):
		return q.runnerErr.Error()
	case q.loadErr != nil:
		if errors.Is(q.loadErr, sched.ErrNewerStore) {
			return q.loadErr.Error()
		}
		return "jobs.json could not be loaded: " + q.loadErr.Error()
	case q.runnerErr != nil:
		return q.runnerErr.Error()
	case resolveErr != nil:
		return resolveErr.Error()
	}
	return ""
}

// bootQueue loads the work queue under the global config dir. Boot calls it
// once the workspaces are loaded, so a job's owner can be found.
func (m *Model) bootQueue() {
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		log.For("core").Error("queue.no_global_dir", "err", err)
		m.notifyErr(fmt.Errorf("work queue: the global config dir could not be resolved: %w", err))
		return
	}
	m.loadQueue(dir, sched.DetectHost())
}

// loadQueue loads jobs.json from cfgDir/jobs. A file that fails to load is
// left untouched: submissions are refused, admission pauses, and a notice
// tells the TUI the daemon's caller shows it to (Boot's return).
func (m *Model) loadQueue(cfgDir string, host sched.Host) {
	q := m.queue
	q.cfgDir, q.dir, q.host = cfgDir, filepath.Join(cfgDir, jobsDirName), host
	switch {
	case queueGOOS == "windows":
		// No shim runs there (spec §10): the queue is paused from boot on
		// with sched.ErrUnsupported's text, and submitJob refuses.
		q.runnerErr = sched.ErrUnsupported
	case len(q.runner.Shim) == 0:
		// SetJobRunnerForTest, called before Boot, keeps its runner.
		q.runner, q.runnerErr = sched.NewRunner()
	}
	store, err := sched.LoadStore(q.dir)
	if err != nil {
		q.loadErr = err
		m.refreshPolicy()
		log.For("core").Error("queue.load_failed", "dir", q.dir, "err", err)
		m.notifyErr(fmt.Errorf("work queue: %s; submissions are refused and admission is paused, and jobs.json is left as it is", q.paused))
		return
	}
	q.store = store
	q.moved()
	m.loadBeliefs()
	m.loadAdvisorCalls()
	m.markPendingNotices()
	m.resolveHeld()
	m.probeConfinerAtBoot()
	m.adoptJobs()
	m.admitJobs()
	m.resumeAsks()
}

// errText is err's text, "" for none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// refreshPolicy reads the scheduler block from the global config.json, which
// a hand edit may have changed, and resolves it against the host. It records
// the block, the policy in effect and why admission is paused ("" when it
// isn't; the policy is then zero: no cap is guessed), and returns the last
// two. A config.json that can't be read or parsed pauses admission with the
// error and is left where it is: the block is read with diskSchedulerBlock,
// never config.LoadConfigFrom, which would rename the file aside and fall back
// to the host's defaults.
func (m *Model) refreshPolicy() (sched.Policy, string) {
	q := m.queue
	q.lastPolicy = time.Now()
	var settings config.SchedulerSettings
	var policy sched.Policy
	block, err := diskSchedulerBlock(q.cfgDir)
	if err != nil {
		err = fmt.Errorf("config.json could not be read: %w", err)
	} else {
		if block != nil {
			settings = *block
		}
		policy, err = sched.Resolve(settings, q.host)
	}
	reason := q.pauseReason(err)
	if reason != "" {
		policy = sched.Policy{}
	}
	if !reflect.DeepEqual(settings, q.settings) || policy != q.policy || reason != q.paused {
		q.moved()
	}
	q.settings, q.policy, q.paused = settings, policy, reason
	agent, agentErr := advisor.Resolve(settings.Agent)
	if agent != q.advisor.settings || errText(agentErr) != errText(q.advisor.settingsErr) {
		q.moved()
	}
	q.advisor.settings, q.advisor.settingsErr = agent, agentErr
	return policy, reason
}

// saveQueue writes jobs.json, for a request that a failed write refuses (it
// undoes its change) and as the quiet save's body. A queue that failed to
// load is never written: its file is kept as it is.
func (m *Model) saveQueue() error {
	q := m.queue
	if q.loadErr != nil {
		return fmt.Errorf("jobs.json was not loaded and is left as it is: %w", q.loadErr)
	}
	if err := sched.SaveStore(q.dir, q.store); err != nil {
		return fmt.Errorf("write jobs.json: %w", err)
	}
	q.saveFailed = false
	if len(q.doomed) > 0 {
		// jobs.json no longer lists these jobs: now their directories may go.
		m.spawnBackground(removeJobDirsJob(q.dir, q.doomed))
		q.doomed = nil
	}
	return nil
}

// saveQueueQuiet saves after a change the model made on its own (a start, a
// settle, a notice's fate): when the write fails the change stays in memory,
// a Notice says so once, and the next save writes it (spec §5.9). It reports
// whether jobs.json was written.
func (m *Model) saveQueueQuiet(after string) bool {
	q := m.queue
	if !q.ready() || q.loadErr != nil {
		return false
	}
	err := m.saveQueue()
	if err == nil {
		return true
	}
	log.For("core").Warn("queue.save_failed", "after", after, "err", err)
	if !q.saveFailed {
		q.saveFailed = true
		// The failure belongs to every client, not to the request this save
		// may run inside (a save made while delivering a request's result,
		// causedBy): a Notice carrying the request's cause would reach the
		// requester alone, and saveFailed then suppresses every later one, so
		// no other client would ever hear it.
		m.causedBy(0, func() {
			m.notifyErr(fmt.Errorf("work queue: %w; the change is kept in memory and written with the next save", err))
		})
	}
	return false
}

// retainFinished drops the oldest finished jobs beyond keep_finished from the
// store, oldest end first (spec §5.9). Their directories are queued in doomed
// and removed once a write of the shorter jobs.json succeeds (saveQueue).
// While admission is paused the limit is unknown (the policy is zero) and
// nothing is dropped. The counter never goes back.
func (m *Model) retainFinished() {
	q := m.queue
	keep := q.policy.KeepFinished
	if keep <= 0 {
		return
	}
	var finished []int
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State.Finished() {
			finished = append(finished, i)
		}
	}
	if len(finished) <= keep {
		return
	}
	slices.SortStableFunc(finished, func(a, b int) int {
		return q.store.Jobs[a].Ended.Compare(q.store.Jobs[b].Ended)
	})
	drop := map[sched.JobID]bool{}
	for _, i := range finished[:len(finished)-keep] {
		drop[q.store.Jobs[i].ID] = true
		q.doomed = append(q.doomed, q.store.Jobs[i].ID)
	}
	q.store.Jobs = slices.DeleteFunc(q.store.Jobs, func(r sched.Record) bool { return drop[r.ID] })
	q.moved()
}

// removeJobDirsJob removes the directories of dropped jobs off the loop, each
// through sched.RemoveJobDir, the one removal: it builds the path from the
// job's ID, never from a path read from a file.
func removeJobDirsJob(dir string, ids []sched.JobID) Job {
	ids = slices.Clone(ids)
	return func() any {
		for _, id := range ids {
			if err := sched.RemoveJobDir(dir, id); err != nil {
				log.For("core").Warn("queue.remove_job_dir_failed", "job", int64(id), "err", err)
			}
		}
		return nil
	}
}

// markPendingNotices records as undelivered every notice that was still on its
// way when the last daemon stopped: a finished job that asked for one, has an
// owner and has no fate. Its message may have reached the mod or not, and a
// message the mod may have submitted is never sent again. It runs before
// adoption, whose settled jobs send their own notices.
func (m *Model) markPendingNotices() {
	q := m.queue
	changed := false
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State.Finished() && rec.Notify && rec.Owner != 0 && rec.Notice == sched.NoticeNone {
			rec.Notice, rec.NoticeWhy = sched.NoticeUndelivered, "the daemon stopped before the message was delivered"
			changed = true
		}
	}
	if changed {
		q.moved()
		m.saveQueueQuiet("boot")
	}
}

// reprobeHeld tests again the lock of each queued record whose last test
// failed (queueState.unprobed) and applies the answer as boot adoption does,
// through the same pass: a held lock is adopted as running, a free one with an
// exit.json settles from it, a free one without releases the record to the
// queue as an ordinary job. The test does not block (sched.Probe), so it runs
// here, on the loop. A record whose test fails again stays held, and
// admitJobs leaves it out of its walk.
func (m *Model) reprobeHeld() {
	q := m.queue
	if q.reprobing {
		return
	}
	pass := queuePass{dir: q.dir, now: time.Now(), escalate: q.escalateAfter()}
	for id := range q.unprobed {
		if i := q.index(id); i < 0 || q.store.Jobs[i].State != sched.Queued {
			delete(q.unprobed, id)
			continue
		}
		pass.jobs = append(pass.jobs, passJob{id: id, queued: true})
	}
	if len(pass.jobs) == 0 {
		return
	}
	slices.SortFunc(pass.jobs, func(a, b passJob) int { return cmp.Compare(a.id, b.id) })
	q.reprobing = true
	defer func() { q.reprobing = false }()
	m.deliverQueuePass(runQueuePass(pass))
}

// adoptJobs settles what ran while no daemon was up (spec §5.6): each running
// job through the lock test of a pass, which settles one whose lock is free
// from exit.json (lost when there is none), and each queued job too, since a
// start whose save failed leaves a running shim behind a queued record: one
// whose lock is held is adopted as running, one with an exit.json settles from
// it, and either way it is never started twice. The pass runs here, before the
// loop starts, so the first admission pass counts the jobs adopted. A job's
// peak memory restarts from zero, and it is marked adopted, so its run's
// peak counts only as a floor (smart scheduler spec §5.2).
func (m *Model) adoptJobs() {
	q := m.queue
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State == sched.Running {
			q.store.Jobs[i].PeakRSSKB = 0
			q.store.Jobs[i].Adopted = true
		}
	}
	pass := m.queuePassFor(true)
	if len(pass.jobs) == 0 {
		return
	}
	m.deliverQueuePass(runQueuePass(pass))
}
