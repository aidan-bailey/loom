package core

import (
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
)

func TestJobBadges(t *testing.T) {
	q := QueueView{Jobs: []JobView{
		// Owner 1: three running (listed out of order) and a queued one. The
		// lowest running ID names the badge, and the others count in "+N".
		{ID: 16, State: sched.Running, Owner: 1},
		{ID: 14, State: sched.Running, Owner: 1},
		{ID: 15, State: sched.Running, Owner: 1},
		{ID: 17, State: sched.Queued, Owner: 1},
		// Owner 2: queued only, and a held one counts as queued.
		{ID: 18, State: sched.Queued, Owner: 2},
		{ID: 19, State: sched.Held, Owner: 2},
		// Owner 3: one running job.
		{ID: 20, State: sched.Running, Owner: 3},
		// Owner 4: only finished jobs of every kind: no badge.
		{ID: 21, State: sched.Done, Owner: 4},
		{ID: 22, State: sched.Failed, Owner: 4},
		{ID: 23, State: sched.Cancelled, Owner: 4},
		{ID: 24, State: sched.Lost, Owner: 4},
		// No owner: no session to badge.
		{ID: 25, State: sched.Running, Owner: 0},
		{ID: 26, State: sched.Queued, Owner: 0},
	}}
	assert.Equal(t, map[InstanceID]JobBadge{
		1: {Label: "⚙ job 14 +2", Focus: 14},
		2: {Label: "⚙ queued"},
		3: {Label: "⚙ job 20", Focus: 20},
	}, JobBadges(q))
}

func TestJobBadges_EmptyQueue(t *testing.T) {
	assert.Empty(t, JobBadges(QueueView{}))
}
