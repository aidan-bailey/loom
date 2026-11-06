package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqRelative seeds a done run of argv in sub's folder: a relative of the
// jobs the tests submit there.
func wqRelative(t *testing.T, m *Model, sub sched.Submit, peakKB int64, argv ...string) sched.SignatureID {
	t.Helper()
	sig := wqSigOf(t, argv, sub.Cwd)
	written := int64(0)
	m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Job: 1, Ended: time.Now(), Outcome: sched.OutcomeDone,
		PeakKB: peakKB, CPUSeconds: 20, WallSeconds: 10, WriteBytes: &written})
	return sig.ID()
}

func TestJobQueue_AJobLearnsFromItsFamily(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "sleep 0 2>&1 | tail -3")
	relative := wqRelative(t, m, sub, 5<<20, "sleep", "0")

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, m, id)
	assert.NotEqual(t, relative, rec.Signature, "a signature of its own")
	assert.Equal(t, sched.ClaimSources{CPUs: sched.SourceFamily, Memory: sched.SourceFamily, IO: sched.SourceFamily}, rec.Sources)
	assert.Equal(t, 6, rec.Claim.MemoryGB, "the relative's 5 GB with its margin")
	assert.Equal(t, 1, rec.FamilyRuns)
	assert.Zero(t, rec.Learned)
	for _, jv := range m.Queue().Jobs {
		if jv.ID == id {
			assert.Equal(t, "sleep 0", jv.Family)
			assert.Equal(t, 1, jv.FamilyRuns)
			assert.True(t, jv.UsualFromFamily)
		}
	}
	wqSettle(t, m, id)
}

func TestJobQueue_AWaitingJobFollowsItsFamily(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job fills the cores
	blocker := wqSubmit(t, m, "sleep", "60")
	require.Equal(t, sched.Running, wqRecord(t, m, blocker).State)
	sub := wqJob(t, "sh", "-c", "cargo build -p a | tail -1")
	relative := wqRelative(t, m, sub, 5<<20, "cargo", "build", "-p", "a")

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Equal(t, sched.Queued, wqRecord(t, m, id).State)
	assert.Equal(t, 6, wqRecord(t, m, id).Claim.MemoryGB)

	m.queue.beliefs.Find(relative).AddRun(sched.Run{Job: 2, Ended: time.Now(), Outcome: sched.OutcomeDone, PeakKB: 10 << 20, WallSeconds: 30})
	m.admitJobs()
	assert.Equal(t, 12, wqRecord(t, m, id).Claim.MemoryGB, "re-sized at the next pass")
}

func TestJobQueue_NoFamilyWhileLearningIsOff(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "sleep 0 | tail -1")
	wqRelative(t, m, sub, 5<<20, "sleep", "0")
	m.queue.beliefsErr = errors.New("beliefs.json is unreadable")
	assert.Nil(t, m.queue.familyPool(wqSigOf(t, sub.Argv, sub.Cwd)),
		"no pool, though the registry in memory holds a relative (the admission pass's re-size would hide a submit's)")

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.SourceDefault, wqRecord(t, m, id).Sources.Memory)
	assert.Zero(t, wqRecord(t, m, id).FamilyRuns)
	wqSettle(t, m, id)
}

func TestAdvisor_AFamilyWithHistoryAsksNothing(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	sub := wqJob(t, "sh", "-c", "sleep 0 | tail -1")
	wqRelative(t, m, sub, 1<<20, "sleep", "0")
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Empty(t, wqRecord(t, m, id).Sizing, "every part from the family")
	wqPump(t, m)
	assert.Empty(t, wqCalls(t, calls), "no call")

	t.Run("a family that lacks a part still asks", func(t *testing.T) {
		m, _ := wqAdvisedModel(t, wqPriorAnswer, 0)
		sub := wqJob(t, "sh", "-c", "sleep 1 | tail -1")
		sig := wqSigOf(t, []string{"sleep", "1"}, sub.Cwd)
		m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Job: 1, Ended: time.Now(), Outcome: sched.OutcomeDone,
			PeakKB: 1 << 20, CPUSeconds: 1, WallSeconds: 1}) // no I/O measured
		id, _, err := m.submitJob(sub)
		require.NoError(t, err)
		assert.Equal(t, sched.SizingAsking, wqRecord(t, m, id).Sizing)
	})
}

func TestAdvisor_KnownBeliefsPutTheFamilyFirst(t *testing.T) {
	m := wqLiveModel(t)
	cwd := t.TempDir()
	now := time.Now()
	ensure := func(age time.Duration, argv ...string) sched.SignatureID {
		sig := wqSigOf(t, argv, cwd)
		m.queue.beliefs.Ensure(sig, now.Add(-age))
		return sig.ID()
	}
	ensure(1*time.Minute, "cargo", "build")                                            // unrelated, the newest
	ensure(2*time.Minute, "cargo", "clippy", "-p", "a")                                // shares a step
	ensure(3*time.Minute, "sh", "-c", "cargo clippy -p a && cargo nextest run -p a y") // the same family, the oldest
	self := ensure(0, "sh", "-c", "cargo nextest run -p a x && cargo clippy -p a")

	var got []string
	for _, k := range m.knownBeliefs(m.queue.beliefs.Find(self), "", now) {
		got = append(got, strings.Join(k.Command, " "))
	}
	assert.Equal(t, []string{"sh -c cargo clippy -p a && cargo nextest run -p a y", "cargo clippy -p a", "cargo build"}, got)
}
