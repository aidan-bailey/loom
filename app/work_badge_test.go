package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestOwnerJobBadges(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		// Owner 1: three running (listed out of order) and a queued one. The
		// lowest running ID names the badge, and the others count in "+N".
		{ID: 16, State: sched.Running, Owner: 1},
		{ID: 14, State: sched.Running, Owner: 1},
		{ID: 15, State: sched.Running, Owner: 1},
		{ID: 17, State: sched.Queued, Owner: 1},
		// Owner 2: queued only.
		{ID: 18, State: sched.Queued, Owner: 2},
		{ID: 19, State: sched.Queued, Owner: 2},
		// Owner 3: one running job.
		{ID: 20, State: sched.Running, Owner: 3},
		// Owner 4: only finished jobs of every kind: no badge.
		{ID: 21, State: sched.Done, Owner: 4},
		{ID: 22, State: sched.Failed, Owner: 4},
		{ID: 23, State: sched.Cancelled, Owner: 4},
		{ID: 24, State: sched.Lost, Owner: 4},
		// No owner: no card to badge.
		{ID: 25, State: sched.Running, Owner: 0},
		{ID: 26, State: sched.Queued, Owner: 0},
	}}
	assert.Equal(t, map[core.InstanceID]string{
		1: "⚙ job 14 +2",
		2: "⚙ queued",
		3: "⚙ job 20",
	}, ownerJobBadges(q))
}

func TestOwnerJobBadges_EmptyQueue(t *testing.T) {
	assert.Empty(t, ownerJobBadges(core.QueueView{}))
}

// TestQueueChanged_PublishesTheJobBadges runs the TUI's applier, not the
// helper: the event moves the badges to ui, and the next event, with the job
// done, takes them away again.
func TestQueueChanged_PublishesTheJobBadges(t *testing.T) {
	m := newTestHome(t)
	t.Cleanup(func() { ui.SetJobBadges(nil) })

	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{Jobs: []core.JobView{
		{ID: 14, State: sched.Running, Owner: 9},
		{ID: 15, State: sched.Queued, Owner: 10},
	}}})
	assert.Equal(t, "⚙ job 14", ui.JobBadge(9))
	assert.Equal(t, "⚙ queued", ui.JobBadge(10))
	assert.Equal(t, "", ui.JobBadge(11), "a session with no job has no badge")

	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{Jobs: []core.JobView{
		{ID: 14, State: sched.Done, Owner: 9},
		{ID: 15, State: sched.Running, Owner: 10},
	}}})
	assert.Equal(t, "", ui.JobBadge(9), "a finished job's badge is gone")
	assert.Equal(t, "⚙ job 15", ui.JobBadge(10), "a started job's badge changes")
}
