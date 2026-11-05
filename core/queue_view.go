package core

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// QueueView is the work queue as a client sees it (published as
// QueueChanged, and what Core.Queue answers from a replica). It holds no
// job's environment: that crosses the wire only inside SubmitJob.
type QueueView struct {
	Host      sched.Host
	Settings  config.SchedulerSettings // as configured; a 0 field shows the default
	Policy    sched.Policy             // in effect; zero while Paused != ""
	Paused    string                   // why admission is paused; "" when it isn't
	Learning  string                   // why learning is off; "" while it is on (smart scheduler spec §5.3)
	Agent     AgentView                // the advisor (smart scheduler spec §5.6)
	Claimed   sched.Need               // what the running jobs' claims take
	UsedCPU   float64                  // cores the running jobs use, last sampled
	UsedRSSKB int64                    // memory they use, last sampled
	Dir       string                   // <globalDir>/jobs
	Jobs      []JobView                // running (by start), queued (queue order), then finished (newest end first)
}

// AgentView is the work queue's advisor as a client sees it (smart
// scheduler spec §5.6).
type AgentView struct {
	On     bool
	Model  string
	Effort string
	Wait   time.Duration
	Why    string // why it is off or paused; "" while on
}

// JobView is one job of the queue as a client sees it.
type JobView struct {
	ID               sched.JobID
	State            sched.State
	Description      string
	Argv             []string
	Cwd              string
	Owner            InstanceID  // 0: none
	OwnerTitle       string      // "" when the owner isn't served
	OwnerWorkspace   string      // the owner's workspace's WorkspaceView.Label; "" when the owner isn't served
	OwnerWorkspaceID WorkspaceID // the owner's workspace, which `o` opens (openTab); 0 when the owner isn't served
	Claim            sched.Claim
	Need             sched.Need // an exclusive claim's at the current caps
	Submitted        time.Time
	Started          time.Time
	Ended            time.Time
	Exit             *sched.Exit
	Failure          string
	CancelReason     string
	PeakRSSKB        int64
	RSSKB            int64
	CPU              float64
	LogPath          string // JobDir(Dir, ID)/output.log
	Waiting          string
	Notice           sched.Notice
	NoticeWhy        string
	// The smart scheduler's sizing (spec 2026-11-05 §5.4).
	Signature sched.SignatureID
	Sizing    sched.Sizing
	Sources   sched.ClaimSources
	Learned   int
	AboveCap  bool
	BelowKB   int64
	Unsized   string
	AgentWhy  string
	// Durations (smart scheduler spec §5.5): the belief's usual duration
	// and P90 (0 for none; UsualFromPrior when they are the agent's
	// estimate), how long the job may run before it is overdue (0 for
	// never), and a queued job's forecast start (zero for none).
	Usual          time.Duration
	P90            time.Duration
	UsualFromPrior bool
	OverdueAfter   time.Duration
	StartsAt       time.Time
}

// StartFailed reports whether the job failed without ever running its command:
// its state is Failed and the daemon gave a reason (Failure), or its exit
// record is missing or says the exec failed (Exit.Error). A job that ran and
// exited non-zero or by a signal, and a job in any other state, is not.
func (j JobView) StartFailed() bool { return startFailed(j.State, j.Failure, j.Exit) }

// startFailed is StartFailed over the fields a job's record and its view
// share, so the notice (which has a record) and the view agree.
func startFailed(state sched.State, failure string, exit *sched.Exit) bool {
	return state == sched.Failed && (failure != "" || exit == nil || exit.Error != "")
}

// Clone is v with its own copy of everything a caller could edit in place:
// the scheduler block's default claim, and each job's argv and exit.
func (v QueueView) Clone() QueueView {
	v.Settings = cloneSchedulerSettings(v.Settings)
	v.Jobs = cloneJobViews(v.Jobs)
	return v
}

func cloneSchedulerSettings(s config.SchedulerSettings) config.SchedulerSettings {
	if s.DefaultClaim != nil {
		c := *s.DefaultClaim
		s.DefaultClaim = &c
	}
	if s.Agent != nil {
		a := *s.Agent
		if a.Enabled != nil {
			on := *a.Enabled
			a.Enabled = &on
		}
		s.Agent = &a
	}
	return s
}

func cloneJobViews(jobs []JobView) []JobView {
	out := slices.Clone(jobs)
	for i := range out {
		out[i].Argv = slices.Clone(out[i].Argv)
		if e := out[i].Exit; e != nil {
			c := *e
			out[i].Exit = &c
		}
	}
	return out
}

// Queue is the work queue as it stands now (Core.Queue; a client's replica
// answers it from the last QueueChanged).
func (m *Model) Queue() QueueView { return m.queueView() }

// jobOwner is what a job's owner resolves to: the fields of its JobView that
// come from the model's workspaces and sessions, not from the queue. All zero
// when no served session holds the owner.
type jobOwner struct {
	title       string
	workspace   string
	workspaceID WorkspaceID
}

// jobOwners resolves the owners of jobs by identity, as lookup does (the
// first served session holding an ID, by the ID idOf gives it: a workspace
// registered again is resolved in the Sync that serves it, before its views
// have named its sessions), to each one's title, its workspace's
// label and its workspace's ID. It builds one map for the whole call, not a
// walk of every workspace per job, and none (nil, which every lookup answers
// with the zero jobOwner) when no job has an owner. The view (queueView) and
// the publisher's owner signature (queueOwners) both read it, so they can't
// disagree.
func (m *Model) jobOwners(jobs []sched.Record) map[InstanceID]jobOwner {
	owned := false
	for i := range jobs {
		if jobs[i].Owner != 0 {
			owned = true
			break
		}
	}
	if !owned {
		return nil
	}
	owners := map[InstanceID]jobOwner{}
	for _, ws := range m.workspaces {
		label, wsID := ws.Label(), m.wsIDOf(ws)
		for _, inst := range ws.insts {
			id := m.idOf(inst) // what a client's view of the session names it, assigned if the views have not yet
			if _, held := owners[id]; id == 0 || held {
				continue
			}
			owners[id] = jobOwner{title: inst.Title, workspace: label, workspaceID: wsID}
		}
	}
	return owners
}

// queueOwners is the owner each job of the store resolves to, in store
// order. publishQueue compares it with the last published one, since a
// rename, an unregister or a kill's completion changes it without bumping
// the queue's revision.
func (m *Model) queueOwners() []jobOwner {
	jobs := m.queue.store.Jobs
	byID := m.jobOwners(jobs)
	owners := make([]jobOwner, len(jobs))
	for i := range jobs {
		owners[i] = byID[InstanceID(jobs[i].Owner)]
	}
	return owners
}

// queueRev is the revision of the queue as last changed.
func (m *Model) queueRev() uint64 { return m.queue.rev }

// queueView builds the published view of m.queue: the running jobs by
// start, the queued ones in queue order, then the finished ones, newest end
// first. It copies everything it holds, and never the environment, which no
// record keeps.
func (m *Model) queueView() QueueView {
	q := m.queue
	v := QueueView{
		Host:     q.host,
		Settings: cloneSchedulerSettings(q.settings),
		Policy:   q.policy,
		Paused:   q.paused,
		Learning: q.learningOff(),
		Agent:    m.agentView(time.Now()),
		Dir:      q.dir,
	}
	var running, queued, finished []sched.Record
	for _, r := range q.store.Jobs {
		switch r.State {
		case sched.Running:
			running = append(running, r)
		case sched.Queued:
			queued = append(queued, r)
		default:
			finished = append(finished, r)
		}
	}
	slices.SortStableFunc(running, func(a, b sched.Record) int {
		return cmp.Or(a.Started.Compare(b.Started), cmp.Compare(a.ID, b.ID))
	})
	slices.SortStableFunc(queued, func(a, b sched.Record) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID))
	})
	slices.SortStableFunc(finished, func(a, b sched.Record) int {
		return cmp.Or(b.Ended.Compare(a.Ended), cmp.Compare(b.ID, a.ID))
	})
	byID := m.jobOwners(q.store.Jobs)
	usualOf := func(r sched.Record) (time.Duration, bool) {
		u, _, n, fromPrior := sched.Usual(q.beliefOf(r.Signature))
		return u, n > 0 || fromPrior
	}
	starts := sched.Forecast(q.store.Jobs, q.policy, time.Now(), usualOf, m.hostUsual())
	for _, r := range slices.Concat(running, queued, finished) {
		jv := m.queueJobView(q, r, byID)
		if r.State == sched.Queued {
			jv.StartsAt = starts[r.ID]
		}
		if r.State == sched.Running {
			v.Claimed.CPUs += jv.Need.CPUs
			v.Claimed.MemoryGB += jv.Need.MemoryGB
			v.Claimed.IOSlots += jv.Need.IOSlots
			v.UsedCPU += jv.CPU
			v.UsedRSSKB += jv.RSSKB
		}
		v.Jobs = append(v.Jobs, jv)
	}
	return v
}

// hostUsual is the forecast's duration for a job with none: the median of
// every belief's usual duration on this host, or 5 minutes with none.
func (m *Model) hostUsual() time.Duration {
	var all []time.Duration
	for i := range m.queue.beliefs.Beliefs {
		if u, _, n, _ := sched.Usual(&m.queue.beliefs.Beliefs[i]); n > 0 {
			all = append(all, u)
		}
	}
	if len(all) == 0 {
		return 5 * time.Minute
	}
	slices.Sort(all)
	return all[len(all)/2]
}

// queueJobView is the view of record r. Only a running job has usage, and
// only a queued one has a reason to wait: a stale entry for a job that has
// moved on is not shown. A queued job the last pass said nothing about,
// while admission is paused, waits for that. owners is jobOwners' answer.
func (m *Model) queueJobView(q *queueState, r sched.Record, owners map[InstanceID]jobOwner) JobView {
	jv := JobView{
		ID:           r.ID,
		State:        r.State,
		Description:  r.Description,
		Argv:         slices.Clone(r.Argv),
		Cwd:          r.Cwd,
		Owner:        InstanceID(r.Owner),
		Claim:        r.Claim,
		Need:         r.Claim.Need(q.policy.Caps),
		Submitted:    r.Submitted,
		Started:      r.Started,
		Ended:        r.Ended,
		Failure:      r.Failure,
		CancelReason: r.CancelReason,
		PeakRSSKB:    r.PeakRSSKB,
		LogPath:      filepath.Join(sched.JobDir(q.dir, r.ID), sched.LogFile),
		Notice:       r.Notice,
		NoticeWhy:    r.NoticeWhy,
		Signature:    r.Signature,
		Sizing:       r.Sizing,
		Sources:      r.Sources,
		Learned:      r.Learned,
		AboveCap:     r.AboveCap,
		BelowKB:      r.BelowKB,
		Unsized:      r.Unsized,
		AgentWhy:     r.AgentWhy,
	}
	if r.Exit != nil {
		e := *r.Exit
		jv.Exit = &e
	}
	b := q.beliefOf(r.Signature)
	jv.Usual, jv.P90, _, jv.UsualFromPrior = sched.Usual(b)
	jv.OverdueAfter = sched.OverdueAfter(b)
	o := owners[jv.Owner]
	jv.OwnerTitle, jv.OwnerWorkspace, jv.OwnerWorkspaceID = o.title, o.workspace, o.workspaceID
	switch r.State {
	case sched.Running:
		u := q.usage[r.ID]
		jv.RSSKB, jv.CPU = u.RSSKB, u.CPU
	case sched.Queued:
		jv.Waiting = q.waiting[r.ID]
		if jv.Waiting == "" && q.paused != "" {
			jv.Waiting = "admission paused: " + q.paused
		}
	}
	return jv
}
