package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobQueue_ALockedJobWaitsForTheHolder: a job submitted with a lock a
// running job holds stays queued, says why in its view, and starts once the
// holder ends, however it ends.
func TestJobQueue_ALockedJobWaitsForTheHolder(t *testing.T) {
	m := wqModel(t)
	holder := wqRunning(t, 1)
	holder.Locks = []string{"port:4300"}
	wqSeed(t, m, holder)

	sub := wqJob(t, "true")
	sub.Locks = []string{"port:4300"}
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqPump(t, m)

	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.Queued, rec.State, "the lock is held")
	assert.Equal(t, []string{"port:4300"}, rec.Locks)
	v, ok := jobOfView(m.Queue(), id)
	require.True(t, ok)
	assert.Equal(t, "waits for lock port:4300 (job 1)", v.Waiting)
	assert.Equal(t, []string{"port:4300"}, v.Locks, "the view carries the locks")

	wqEnd(t, m, holder.ID, sched.Failed, &sched.Exit{Code: 1})
	wqPass(t, m)
	assert.NotEqual(t, sched.Queued, wqRecord(t, m, id).State, "a failed holder frees its lock")
}

// TestJobQueue_ARequeueKeepsTheLocks: a job killed for memory and requeued
// still names its locks on its next attempt.
func TestJobQueue_ARequeueKeepsTheLocks(t *testing.T) {
	m := wqModel(t)
	wqPaused(t, m)
	rec := wqRunning(t, 1)
	rec.Locks = []string{"port:4300", "port:4310"}
	wqSeed(t, m, rec)

	m.requeueJob(m.queue.index(1), "memory ran short", time.Now())

	got := wqRecord(t, m, 1)
	require.Equal(t, sched.Queued, got.State)
	assert.Equal(t, []string{"port:4300", "port:4310"}, got.Locks)
}

func jobOfView(q QueueView, id sched.JobID) (JobView, bool) {
	for _, j := range q.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return JobView{}, false
}
