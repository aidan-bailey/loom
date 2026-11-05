package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
)

// ownerJobs is what one session's jobs add up to for its badge.
type ownerJobs struct {
	lowest  sched.JobID // the lowest running ID; meaningful when running > 0
	running int
	queued  bool
}

// ownerJobBadges is every session's work-queue badge, keyed by the owner's
// InstanceID: "⚙ job 14" for its lowest running job, with " +N" for N more
// running, or "⚙ queued" when it has only queued ones. A session with
// neither, a finished job, and a job with no owner get no entry. It reads
// the view only, so it is safe wherever the view is.
func ownerJobBadges(q core.QueueView) map[core.InstanceID]string {
	by := map[core.InstanceID]*ownerJobs{}
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
	out := make(map[core.InstanceID]string, len(by))
	for id, o := range by {
		switch {
		case o.running > 0:
			label := fmt.Sprintf("⚙ job %d", o.lowest)
			if o.running > 1 {
				label += fmt.Sprintf(" +%d", o.running-1)
			}
			out[id] = label
		case o.queued:
			out[id] = "⚙ queued"
		}
	}
	return out
}

// syncJobBadges publishes the badges of home.queue to the cards. Call it
// wherever home.queue changes.
func (m *home) syncJobBadges() {
	ui.SetJobBadges(ownerJobBadges(m.queue))
}
