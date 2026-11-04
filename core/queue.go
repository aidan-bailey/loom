package core

import (
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
	// doomed holds the IDs retention dropped from the store, whose
	// directories go once a write of the shorter jobs.json succeeds.
	doomed []sched.JobID
	// lastPass is when the last admission pass ran, to throttle the tick's.
	lastPass time.Time
	// handshake and escalation override the start's 5 s wait for started.json
	// and the 10 s between a cancel's SIGTERM and SIGKILL; 0 keeps them. Tests
	// shorten them.
	handshake, escalation time.Duration
	// saveFailed is set while the last quiet save failed, so a run of failures
	// raises one notice.
	saveFailed bool
}

func newQueueState() *queueState {
	return &queueState{
		waiting:  map[sched.JobID]string{},
		usage:    map[sched.JobID]jobUsage{},
		starting: map[sched.JobID]bool{},
		silent:   map[sched.JobID]silentShim{},
		cpuMarks: map[sched.JobID]cpuMark{},
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
	m.admitJobs()
}

// refreshPolicy reads the scheduler block from the global config.json, which
// a hand edit may have changed, and resolves it against the host. It records
// the block, the policy in effect and why admission is paused ("" when it
// isn't; the policy is then zero: no cap is guessed), and returns the last
// two.
func (m *Model) refreshPolicy() (sched.Policy, string) {
	q := m.queue
	settings := config.LoadConfigFrom(q.cfgDir).GetScheduler()
	policy, err := sched.Resolve(settings, q.host)
	reason := q.pauseReason(err)
	if reason != "" {
		policy = sched.Policy{}
	}
	if !reflect.DeepEqual(settings, q.settings) || policy != q.policy || reason != q.paused {
		q.moved()
	}
	q.settings, q.policy, q.paused = settings, policy, reason
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
		m.notifyErr(fmt.Errorf("work queue: %w; the change is kept in memory and written with the next save", err))
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
