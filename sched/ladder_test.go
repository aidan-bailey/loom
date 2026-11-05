package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ladderIn(now time.Time, headroomGB int64, pressure float64, jobs ...LadderJob) LadderInput {
	return LadderInput{Now: now, HeadroomKB: gb(headroomGB), ReserveKB: gb(4), MemPressure: pressure, Jobs: jobs}
}

func ljob(id JobID, startedAgo time.Duration) LadderJob {
	return LadderJob{ID: id, Started: t0.Add(-startedAgo), WorkingKB: gb(3)}
}

func TestLadder_Short(t *testing.T) {
	assert.False(t, ladderIn(t0, 4, 9.99).Short(), "headroom at the reserve is not short")
	assert.True(t, ladderIn(t0, 3, 0).Short(), "below it is")
	assert.True(t, ladderIn(t0, 20, 10).Short(), "pressure at 10% is")
}

func TestLadder_SqueezesTheMostRecentlyStartedFirst(t *testing.T) {
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, ljob(1, time.Hour), ljob(2, time.Minute)))
	require.NotNil(t, act)
	assert.Equal(t, Action{Kind: ActSqueeze, Job: 2, MemoryHighKB: gb(3)}, *act)
}

func TestLadder_VictimOrder(t *testing.T) {
	starved := ljob(2, time.Minute)
	starved.Starved = true
	ex := ljob(3, time.Second)
	ex.Exclusive = true
	maxed := ljob(4, 2*time.Second)
	maxed.Requeues = MaxRequeues
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, ljob(1, time.Hour), starved, ex, maxed))
	require.NotNil(t, act)
	assert.Equal(t, JobID(1), act.Job, "a starved head goes last; exclusive and requeued-out jobs never")
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, starved, ex, maxed))
	require.NotNil(t, act)
	assert.Equal(t, JobID(2), act.Job)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, ex, maxed))
	assert.Nil(t, act)
}

func TestLadder_SqueezeThenPauseThenRequeue(t *testing.T) {
	j := ljob(1, time.Minute)
	j.Rung, j.RungAt = RungSqueezed, t0.Add(-14*time.Second)
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	assert.Nil(t, act, "15 s after the squeeze")
	j.RungAt = t0.Add(-15 * time.Second)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	require.NotNil(t, act)
	assert.Equal(t, ActPause, act.Kind)

	j.Rung, j.RungAt = RungPaused, t0.Add(-29*time.Second)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	assert.Nil(t, act, "30 s after the pause")
	j.RungAt = t0.Add(-30 * time.Second)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	require.NotNil(t, act)
	assert.Equal(t, ActRequeue, act.Kind)

	j.NoRequeue = true
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j, ljob(2, time.Hour)))
	require.NotNil(t, act)
	assert.Equal(t, Action{Kind: ActSqueeze, Job: 2, MemoryHighKB: gb(3)}, *act, "a paused --no-requeue job has nothing left: the next one gives")
}

func TestLadder_SqueezeHasAFloor(t *testing.T) {
	j := ljob(1, time.Minute)
	j.WorkingKB = 0
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	require.NotNil(t, act)
	assert.Equal(t, int64(minHighKB), act.MemoryHighKB, "0 would mean no limit")
}

func TestLadder_Recovery(t *testing.T) {
	old, young := ljob(1, time.Hour), ljob(2, time.Minute)
	old.Rung, young.Rung = RungPaused, RungSqueezed
	st, act := Ladder(LadderState{}, ladderIn(t0, 6, 1.9, old, young))
	assert.Nil(t, act)
	assert.Equal(t, t0, st.CalmSince)
	st, act = Ladder(st, ladderIn(t0.Add(29*time.Second), 6, 1.9, old, young))
	assert.Nil(t, act, "calm for 30 s first")
	st, act = Ladder(st, ladderIn(t0.Add(30*time.Second), 6, 1.9, old, young))
	require.NotNil(t, act)
	assert.Equal(t, Action{Kind: ActRestore, Job: 1}, *act, "oldest first")
	old.Rung = ""
	_, act2 := Ladder(st, ladderIn(t0.Add(44*time.Second), 6, 1.9, old, young))
	assert.Nil(t, act2, "one every 15 s")
	_, act2 = Ladder(st, ladderIn(t0.Add(45*time.Second), 6, 1.9, old, young))
	require.NotNil(t, act2)
	assert.Equal(t, JobID(2), act2.Job)

	st, act = Ladder(LadderState{CalmSince: t0}, ladderIn(t0.Add(time.Minute), 5, 1.9, old, young))
	assert.Nil(t, act, "headroom under reserve + 2 GB is not calm")
	assert.True(t, st.CalmSince.IsZero())
	st, _ = Ladder(LadderState{CalmSince: t0}, ladderIn(t0.Add(time.Minute), 6, 2, old, young))
	assert.True(t, st.CalmSince.IsZero(), "pressure at 2% is not calm")
	st, _ = Ladder(LadderState{CalmSince: t0}, ladderIn(t0.Add(time.Minute), 3, 0, old, young))
	assert.True(t, st.CalmSince.IsZero(), "short resets calm")
}

func TestRung_Text(t *testing.T) {
	assert.Equal(t, "squeezed", RungSqueezed.Text())
	assert.Equal(t, "paused (memory)", RungPaused.Text())
	assert.Equal(t, "", Rung("").Text())
}
