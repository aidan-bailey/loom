package core

import (
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobQueue_ASubmitWithAKeyTheQueueHoldsIsThatJob: a client that lost
// the daemon mid-submit sends the same submission again; with a key, the
// job the first one queued is the answer, and nothing is queued twice.
func TestJobQueue_ASubmitWithAKeyTheQueueHoldsIsThatJob(t *testing.T) {
	dir := t.TempDir()
	m := wqModelAt(t, dir)

	sub := wqJob(t, "true")
	sub.Key = "gates:abc:1:0"
	first, _, err := m.submitJob(sub)
	require.NoError(t, err)
	again, warning, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Empty(t, warning)
	assert.Equal(t, first, again, "the key's job is the answer")
	assert.Len(t, m.queue.store.Jobs, 1, "nothing was queued twice")
	assert.Equal(t, "gates:abc:1:0", wqRecord(t, m, first).Key)

	other := wqJob(t, "true")
	other.Key = "gates:abc:2:0"
	id, _, err := m.submitJob(other)
	require.NoError(t, err)
	assert.NotEqual(t, first, id, "another key is another job")
	keyless := wqJob(t, "true")
	a, _, err := m.submitJob(keyless)
	require.NoError(t, err)
	b, _, err := m.submitJob(keyless)
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "with no key every submit queues a job")

	restarted := wqModelAt(t, dir)
	after, _, err := restarted.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, first, after, "the key survives a restart of the daemon")
}

// TestJobQueue_AKeyMatchesNoCancelledJobAndNoOtherOwnersJob: a cancelled job
// is no answer (a chain refused partway cancels what it queued, and a later
// try must queue again), a finished one is (it ran while the daemon was
// away), and a key matches only its owner's jobs.
func TestJobQueue_AKeyMatchesNoCancelledJobAndNoOtherOwnersJob(t *testing.T) {
	m := wqModel(t)
	done := wqEnded(1, 0)
	done.Key = "done"
	cancelled := wqEnded(2, 0)
	cancelled.State, cancelled.Exit, cancelled.Key = sched.Cancelled, nil, "cancelled"
	theirs := wqEnded(3, 7)
	theirs.Key = "theirs"
	wqSeed(t, m, done)
	wqSeed(t, m, cancelled)
	wqSeed(t, m, theirs)

	for key, want := range map[string]bool{"done": true, "cancelled": false, "theirs": false} {
		sub := wqJob(t, "true")
		sub.Key = key
		id, _, err := m.submitJob(sub)
		require.NoError(t, err, key)
		matched := map[string]sched.JobID{"done": 1, "cancelled": 2, "theirs": 3}[key] == id
		assert.Equal(t, want, matched, key)
	}
}
