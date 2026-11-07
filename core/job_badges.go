package core

import (
	"fmt"

	"github.com/aidan-bailey/loom/sched"
)

// JobBadge is one session's work-queue badge: Label is "⚙ job 14" for its
// lowest running job, with " +N" for N more running, or "⚙ queued" when it
// has only queued or held ones; Focus is the job the label names, 0 for
// "⚙ queued". The TUI's cards and the web layer's fleet both show it.
type JobBadge struct {
	Label string
	Focus sched.JobID
}

// ownerJobs is what one session's jobs add up to for its badge.
type ownerJobs struct {
	lowest  sched.JobID // the lowest running ID; meaningful when running > 0
	running int
	queued  bool
}

// JobBadges is every owning session's badge, keyed by its InstanceID. A
// session with no running, queued or held job, a finished job, and a job
// with no owner get no entry. It reads the view only, so it is safe
// wherever the view is.
func JobBadges(q QueueView) map[InstanceID]JobBadge {
	by := map[InstanceID]*ownerJobs{}
	for _, j := range q.Jobs {
		if j.Owner == 0 {
			continue
		}
		o := by[j.Owner]
		if o == nil {
			o = &ownerJobs{}
			by[j.Owner] = o
		}
		switch j.State {
		case sched.Running:
			if o.running == 0 || j.ID < o.lowest {
				o.lowest = j.ID
			}
			o.running++
		case sched.Queued, sched.Held:
			o.queued = true
		}
	}
	out := make(map[InstanceID]JobBadge, len(by))
	for id, o := range by {
		switch {
		case o.running > 0:
			label := fmt.Sprintf("⚙ job %d", o.lowest)
			if o.running > 1 {
				label += fmt.Sprintf(" +%d", o.running-1)
			}
			out[id] = JobBadge{Label: label, Focus: o.lowest}
		case o.queued:
			out[id] = JobBadge{Label: "⚙ queued"}
		}
	}
	return out
}
