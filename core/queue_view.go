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
	Claimed   sched.Need               // what the running jobs' claims take
	UsedCPU   float64                  // cores the running jobs use, last sampled
	UsedRSSKB int64                    // memory they use, last sampled
	Dir       string                   // <globalDir>/jobs
	Jobs      []JobView                // running (by start), queued (queue order), then finished (newest end first)
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

// jobOwnerOf resolves owner by identity (lookup) to its title, its
// workspace's label and its workspace's ID. The view and the publisher's
// owner signature (queueOwners) both read it, so they can't disagree.
func (m *Model) jobOwnerOf(owner InstanceID) jobOwner {
	inst, ws := m.lookup(owner)
	if inst == nil {
		return jobOwner{}
	}
	return jobOwner{title: inst.Title, workspace: ws.Label(), workspaceID: m.wsIDOf(ws)}
}

// queueOwners is the owner each job of the store resolves to, in store
// order. publishQueue compares it with the last published one, since a
// rename, an unregister or a kill's completion changes it without bumping
// the queue's revision.
func (m *Model) queueOwners() []jobOwner {
	jobs := m.queue.store.Jobs
	owners := make([]jobOwner, len(jobs))
	for i := range jobs {
		owners[i] = m.jobOwnerOf(InstanceID(jobs[i].Owner))
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
	for _, r := range slices.Concat(running, queued, finished) {
		jv := m.queueJobView(q, r)
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

// queueJobView is the view of record r. Only a running job has usage, and
// only a queued one has a reason to wait: a stale entry for a job that has
// moved on is not shown. A queued job the last pass said nothing about,
// while admission is paused, waits for that.
func (m *Model) queueJobView(q *queueState, r sched.Record) JobView {
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
	}
	if r.Exit != nil {
		e := *r.Exit
		jv.Exit = &e
	}
	o := m.jobOwnerOf(jv.Owner)
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
