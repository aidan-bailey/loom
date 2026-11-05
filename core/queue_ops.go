package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// errQueueNotLoaded refuses a request made where the queue has no directory:
// before Boot, or where the global config dir could not be resolved.
var errQueueNotLoaded = errors.New("the work queue isn't loaded")

// jobOwner resolves a submitter's $LOOM_LAUNCH to the session whose current
// launch it is (spec §6.2): that session's ID, or 0 for a plain shell (no
// launch) and for a launch no served session holds, which is also reported
// (unknown) so the caller can warn.
func (m *Model) jobOwner(launch string) (owner InstanceID, unknown bool) {
	if launch == "" {
		return 0, false
	}
	inst := m.instByLaunch(launch)
	if inst == nil {
		return 0, true
	}
	return m.idOf(inst), false
}

// submitJob queues a job (spec §6.1, §5.9): it refuses first, on Windows, with
// sched.ErrUnsupported; then it checks the submission against the bounds,
// the argv check (sched.CheckCommand) and the caps, sizes its claim from its
// signature's belief (smart scheduler spec §5.4), writes spec.json and
// jobs.json, marks the belief seen, and runs an admission pass. It replies only once both files are
// written; a failed write refuses the request with the error and changes
// nothing. warning is the unknown launch's text.
func (m *Model) submitJob(s sched.Submit) (id sched.JobID, warning string, err error) {
	q := m.queue
	switch {
	case queueGOOS == "windows" || (q != nil && errors.Is(q.runnerErr, sched.ErrUnsupported)):
		// First of all: no shim runs there, so nothing else is worth saying.
		return 0, "", sched.ErrUnsupported
	case !q.ready():
		return 0, "", errQueueNotLoaded
	}
	if err := s.Validate(); err != nil {
		return 0, "", err
	}
	if _, err := os.Stat(s.Cwd); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, "", fmt.Errorf("the folder %s does not exist", s.Cwd)
		}
		return 0, "", fmt.Errorf("the folder %s can't be read: %w", s.Cwd, err)
	}
	if err := sched.CheckCommand(s.Argv, s.Env, s.Cwd); err != nil {
		return 0, "", err
	}
	policy, paused := m.refreshPolicy()
	if paused != "" {
		return 0, "", fmt.Errorf("admission paused: %s", paused)
	}
	now := time.Now()
	sig, checkout, unsized := jobSignature(s.Argv, s.Cwd)
	rec := sched.Record{
		Description: strings.TrimSpace(s.Description), Argv: slices.Clone(s.Argv), Cwd: s.Cwd,
		Notify: s.Notify, Tail: s.Tail, State: sched.Queued, Submitted: now,
		Asked: s.Claim, Unsized: unsized, Checkout: checkout,
	}
	if sig != nil {
		rec.Signature = sig.ID()
	}
	q.sizeRecord(&rec, policy)
	if err := sched.CheckClaim(rec.Claim, policy); err != nil {
		return 0, "", err
	}
	if sig != nil && m.askForPrior(s.Claim, q.beliefOf(rec.Signature), now) {
		rec.Sizing = sched.SizingAsking
	}

	owner, unknown := m.jobOwner(s.Launch)
	call := m.attribution(s.Launch, s.Argv)
	id = max(q.store.NextID, 1)
	rec.ID, rec.Order, rec.Owner = id, int64(id), uint64(owner)
	if call != nil {
		rec.Agent = call.Agent
	}
	prev := q.store
	// The directory is made exclusively: NextID is past every directory the
	// load saw, but a directory that appeared since (or a counter that went
	// wrong) must never take a new job's spec, or be removed below as ours.
	if err := sched.CreateJobDir(q.dir, id); err != nil {
		if errors.Is(err, fs.ErrExist) {
			// The counter only goes up: move it past the directory so the
			// next submit tries the next ID, not this one again until a
			// restart. Not saved here; LoadStore raises it past the
			// directory on the next load anyway.
			q.store.NextID = id + 1
			return 0, "", fmt.Errorf("job %d's directory already exists, so nothing was submitted (it is left as it is): %w", id, err)
		}
		return 0, "", err
	}
	if err := sched.WriteSpec(q.jobDir(id), sched.Spec{ID: id, Argv: rec.Argv, Cwd: s.Cwd, Env: slices.Clone(s.Env)}); err != nil {
		// Taking back the directory is safe: this submit just created it, and
		// id was never in a written jobs.json, so no shim can own it.
		_ = sched.RemoveJobDir(q.dir, id)
		return 0, "", fmt.Errorf("write the job's spec: %w", err)
	}
	q.store.NextID = id + 1
	q.store.Jobs = append(q.store.Jobs, rec)
	if err := m.saveQueue(); err != nil {
		q.store = prev
		// As above: this submit created the directory, and id was never in a
		// written jobs.json, so no shim can own it.
		_ = sched.RemoveJobDir(q.dir, id)
		return 0, "", err
	}
	q.moved()
	if call != nil {
		call.uses-- // spent only by a job that was saved
	}
	if sig != nil && q.beliefsErr == nil {
		q.beliefs.Ensure(*sig, now)
		m.saveBeliefsQuiet("submit")
	}
	if rec.Sizing != "" {
		m.queueAdvice(advisorCall{kind: advisePrior, sig: rec.Signature, job: id, queued: now})
	}
	if unknown {
		warning = fmt.Sprintf("this shell's loom session (launch %s) isn't one the daemon serves: job %d has no owner, so no message will come", s.Launch, id)
	}
	m.admitJobs()
	return id, warning, nil
}

// cancelJob cancels job id (spec §5.5) for reason, which the notice and the
// follower's line give after "was cancelled": a queued job is cancelled at
// once; a running job's request is recorded and gateQueue carries it out
// (SIGTERM to its group, SIGKILL if the lock is still held 10 s later). A
// finished job is refused, and a failed write refuses the request.
func (m *Model) cancelJob(id sched.JobID, reason string) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case q.store.Jobs[i].State.Finished():
		return fmt.Errorf("job %d has finished", id)
	}
	rec := &q.store.Jobs[i]
	prev := *rec
	queued := rec.State == sched.Queued
	switch {
	case queued:
		rec.CancelReason = reason
		endJob(rec, sched.Cancelled, time.Now())
	case rec.CancelReason != "":
		return nil // asked for already: the first request stands
	default:
		rec.CancelReason = reason
	}
	if err := m.saveQueue(); err != nil {
		*rec = prev
		return err
	}
	q.moved()
	if queued {
		m.settledJob(i)
		m.afterSettle()
		return nil
	}
	m.gate(gateQueue).request()
	m.maybeQueue()
	return nil
}

// moveJobToFront puts queued job id ahead of every other queued job: its order
// becomes one below the lowest queued order. Anything but a queued job is
// refused.
func (m *Model) moveJobToFront(id sched.JobID) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case q.store.Jobs[i].State != sched.Queued:
		return fmt.Errorf("job %d isn't waiting", id)
	}
	prev := q.store.Jobs[i].Order
	lowest := prev
	for _, rec := range q.store.Jobs {
		if rec.State == sched.Queued && rec.Order < lowest {
			lowest = rec.Order
		}
	}
	q.store.Jobs[i].Order = lowest - 1
	if err := m.saveQueue(); err != nil {
		q.store.Jobs[i].Order = prev
		return err
	}
	q.moved()
	m.admitJobs()
	return nil
}

// notifyJob sets the notify flag of job id, which has not ended, is not being
// cancelled and has an owner: a message will come when it ends (what an
// interrupted `loom job run` asks for).
func (m *Model) notifyJob(id sched.JobID) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case q.store.Jobs[i].State.Finished():
		return fmt.Errorf("job %d has finished", id)
	case q.store.Jobs[i].Owner == 0:
		return fmt.Errorf("job %d has no owner", id)
	case q.store.Jobs[i].CancelReason != "":
		// cancelOwnerJobs cleared the flag for a pause, kill or reset: setting
		// it again would send a notice nobody asked for.
		return fmt.Errorf("job %d is being cancelled", id)
	case q.store.Jobs[i].Notify:
		return nil
	}
	q.store.Jobs[i].Notify = true
	if err := m.saveQueue(); err != nil {
		q.store.Jobs[i].Notify = false
		return err
	}
	q.moved()
	return nil
}

// setSchedulerCaps replaces the scheduler block of the global config.json with
// s (a field left 0 takes the host's default), after resolving it against the
// host: an invalid block is refused, with sched.Resolve's text, and writes
// nothing. The rest of config.json is kept, and the global workspace's own
// copy follows. An admission pass applies it at once.
func (m *Model) setSchedulerCaps(s config.SchedulerSettings) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	if _, err := sched.Resolve(s, q.host); err != nil {
		return err
	}
	cfg, err := readGlobalConfig(q.cfgDir)
	if err != nil {
		return fmt.Errorf("config.json could not be read, so the caps were not saved: %w", err)
	}
	// The caps form never edits the agent block: keep the file's (smart
	// scheduler spec §6).
	s.Agent = cfg.GetScheduler().Agent
	cfg.SetScheduler(s)
	if err := config.SaveConfigTo(cfg, q.cfgDir); err != nil {
		return fmt.Errorf("write the caps to config.json: %w", err)
	}
	if ws := m.wsByConfigDir(q.cfgDir); ws != nil && ws.cfg != nil {
		ws.cfg.SetScheduler(s)
	}
	m.admitJobs()
	return nil
}

// readGlobalConfig decodes dir's config.json in full over the defaults, as
// config.LoadConfigFrom does for a file that decodes (a missing file is the
// defaults too). It differs where LoadConfigFrom would rename a file that
// fails to decode and return the defaults: a file that can't be read or
// decoded is an error, and is left as it is, since saving defaults over it
// would drop every other global setting.
func readGlobalConfig(dir string) (*config.Config, error) {
	path := filepath.Join(dir, config.ConfigFileName)
	cfg := config.DefaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// cancelOwnerJobs cancels every job of the sessions in owners that has not
// ended, for reason ("because the session was paused", "…killed" or
// "…reset"): the queued ones at once, the running ones by a request
// gateQueue carries out. Pause and Kill call it with a set of one before
// they act, and a reset with all its sessions, so that one walk ends every
// queued job before the single admission pass: ending them one session at a
// time let the first's settle start a later session's job, only to cancel it.
// Neither waits for the jobs to exit. The owners are sent no notice for them
// (the flag is cleared), and the jobs of other owners and of none (owner 0,
// whether or not the set holds it) are not touched.
func (m *Model) cancelOwnerJobs(owners map[InstanceID]bool, reason string) {
	q := m.queue
	if q == nil || !q.ready() || len(owners) == 0 {
		return
	}
	now := time.Now()
	found, settled := false, false
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.Owner == 0 || !owners[InstanceID(rec.Owner)] || rec.State.Finished() {
			continue
		}
		found = true
		rec.Notify = false
		switch {
		case rec.State == sched.Queued:
			rec.CancelReason = reason
			endJob(rec, sched.Cancelled, now)
			m.settledJob(i)
			settled = true
		case rec.CancelReason == "":
			rec.CancelReason = reason
		}
	}
	if !found {
		return
	}
	q.moved()
	if settled {
		m.afterSettle()
	} else {
		m.saveQueueQuiet("cancel")
	}
	m.gate(gateQueue).request()
	m.maybeQueue()
}
