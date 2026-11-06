package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gbIn is a reading at now with headroomGB of headroom, a 4 GB reserve,
// 15 GB of swap free and the bus up.
func gbIn(now time.Time, headroomGB int64, jobs ...BorrowerJob) GiveBackInput {
	return GiveBackInput{Now: now, HeadroomKB: gb(headroomGB), ReserveKB: gb(4), SwapFreeKB: gb(15), CanLend: true, Jobs: jobs}
}

func TestTight(t *testing.T) {
	assert.True(t, Tight(gb(6)-1, gb(4)), "below the reserve plus 2 GB")
	assert.False(t, Tight(gb(6), gb(4)), "at it")
}

func TestGiveBack_OnlyWhileTight(t *testing.T) {
	in := gbIn(t0, 6, BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(2)})
	_, act := GiveBack(GiveBackState{}, in)
	assert.Nil(t, act)
	in.HeadroomKB--
	_, act = GiveBack(GiveBackState{}, in)
	assert.Equal(t, &Action{Kind: ActLend, Job: 1}, act)
}

func TestGiveBack_TheYoungestUnlentBorrowerGoesFirst(t *testing.T) {
	old := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(1)}
	young := BorrowerJob{ID: 2, Started: t0.Add(-10 * time.Minute), WorkingKB: gb(1)}
	lent := BorrowerJob{ID: 3, Started: t0.Add(-5 * time.Minute), LentAt: t0.Add(-time.Minute), WorkingKB: gb(1)}
	_, act := GiveBack(GiveBackState{}, gbIn(t0, 3, old, young, lent))
	require.NotNil(t, act)
	assert.Equal(t, JobID(2), act.Job)
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 3, lent))
	assert.Nil(t, act, "a lent job has given back already")
}

func TestGiveBack_TheStakeDecidesRequeueOrLend(t *testing.T) {
	long := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(2)}
	for _, tc := range []struct {
		name string
		job  BorrowerJob
		edit func(*GiveBackInput)
		want ActionKind
	}{
		{"just under 2 minutes", BorrowerJob{ID: 1, Started: t0.Add(-2*time.Minute + time.Second), WorkingKB: gb(2)}, nil, ActRequeue},
		{"2 minutes", BorrowerJob{ID: 1, Started: t0.Add(-2 * time.Minute), WorkingKB: gb(2)}, nil, ActLend},
		{"time lent is no stake", BorrowerJob{ID: 1, Started: t0.Add(-3 * time.Minute), LentFor: 61 * time.Second, WorkingKB: gb(2)}, nil, ActRequeue},
		{"a freeze of it failed", BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(2), NoLend: true}, nil, ActRequeue},
		{"swap can't take it", long, func(in *GiveBackInput) { in.SwapFreeKB = gb(2) - 1 }, ActRequeue},
		{"swap takes it exactly", long, func(in *GiveBackInput) { in.SwapFreeKB = gb(2) }, ActLend},
		{"no bus", long, func(in *GiveBackInput) { in.CanLend = false }, ActRequeue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := gbIn(t0, 3, tc.job)
			if tc.edit != nil {
				tc.edit(&in)
			}
			_, act := GiveBack(GiveBackState{}, in)
			require.NotNil(t, act)
			assert.Equal(t, tc.want, act.Kind)
		})
	}
}

func TestGiveBack_TenSecondsBetweenGiveBacks(t *testing.T) {
	a := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(1)}
	b := BorrowerJob{ID: 2, Started: t0.Add(-2 * time.Hour), WorkingKB: gb(1)}
	st, act := GiveBack(GiveBackState{}, gbIn(t0, 3, a, b))
	require.Equal(t, &Action{Kind: ActLend, Job: 1}, act)
	a.LentAt = t0
	_, act = GiveBack(st, gbIn(t0.Add(10*time.Second-time.Millisecond), 3, a, b))
	assert.Nil(t, act)
	_, act = GiveBack(st, gbIn(t0.Add(10*time.Second), 3, a, b))
	assert.Equal(t, &Action{Kind: ActLend, Job: 2}, act)
}

func TestGiveBack_Resume(t *testing.T) {
	first := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), LentAt: t0.Add(-31 * time.Second), Fits: true}
	second := BorrowerJob{ID: 2, Started: t0.Add(-2 * time.Hour), LentAt: t0.Add(-30 * time.Second), Fits: true}
	_, act := GiveBack(GiveBackState{}, gbIn(t0, 64, second, first))
	assert.Equal(t, &Action{Kind: ActResume, Job: 1}, act, "the earliest lent first, one per pass")

	soon := second
	soon.LentAt = t0.Add(-30*time.Second + time.Millisecond)
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 64, soon))
	assert.Nil(t, act, "lent under 30 s")
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 64, second))
	assert.Equal(t, &Action{Kind: ActResume, Job: 2}, act, "lent 30 s")

	unfit := second
	unfit.Fits = false
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 64, unfit))
	assert.Nil(t, act, "its fit doesn't hold")
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 3, second))
	assert.Nil(t, act, "memory is tight")
	noBus := gbIn(t0, 64, second)
	noBus.CanLend = false
	_, act = GiveBack(GiveBackState{}, noBus)
	assert.Nil(t, act, "no bus to thaw it")
}
