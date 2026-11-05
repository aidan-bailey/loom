package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// at is a done run at cores that kept busy cores busy for 100 s and peaked
// at 1 GB.
func at(cores int, busy float64) Run {
	r := done(1<<20, busy*100, 100)
	r.Claim = Claim{CPUs: cores, MemoryGB: 2}
	return r
}

func TestScalingOf(t *testing.T) {
	assert.Equal(t, Scaling{}, ScalingOf(nil), "no belief")
	assert.Equal(t, Scaling{}, ScalingOf(belief(done(1<<20, 10, 10))), "a run whose claim had no cores (exclusive) teaches nothing")

	one := ScalingOf(belief(at(1, 1.0)))
	assert.False(t, one.Known, "a 1-core run can't show scaling")
	assert.Equal(t, 1.0, one.Busy)

	race := ScalingOf(belief(at(4, 2.17)))
	assert.True(t, race.Known)
	assert.InDelta(t, 0.719, race.Parallel, 0.001, "(1 − 1/2.17) ÷ (1 − 1/4)")
	assert.Equal(t, 1, race.Runs)
	assert.Equal(t, 4, race.Ideal, "efficiency 0.54 at 4 cores, 0.47 at 5")

	wasm := ScalingOf(belief(at(3, 2.74)))
	assert.InDelta(t, 0.952, wasm.Parallel, 0.001)
	assert.Equal(t, 6, wasm.Ideal, "22 at 50%, held to twice the 3 cores it had")

	nextest := ScalingOf(belief(at(2, 1.68), at(4, 1.18)))
	assert.InDelta(t, 0.507, nextest.Parallel, 0.001, "the median of 0.81 and 0.20")
	assert.Equal(t, 2, nextest.Runs)
	assert.Equal(t, 3, nextest.Ideal)

	e2e := ScalingOf(belief(at(3, 0.48)))
	assert.Zero(t, e2e.Parallel, "never busier than 1 core: held to 0")
	assert.Equal(t, 1, e2e.Ideal)

	full := ScalingOf(belief(at(4, 4.0)))
	assert.Equal(t, 1.0, full.Parallel, "a saturated run")
	assert.Equal(t, 8, full.Ideal, "p of 1: the bound alone")

	vitest := ScalingOf(belief(at(4, 7.81)))
	assert.True(t, vitest.Overruns)
	assert.InDelta(t, 7.81, vitest.Busy, 0.001)
	assert.False(t, vitest.Known, "an overrunner's p means nothing")

	assert.False(t, ScalingOf(belief(at(4, 5.0))).Overruns, "1.25 × its cores is not over")
	assert.True(t, ScalingOf(belief(at(4, 7.81), at(4, 7.0), at(4, 2.0))).Overruns, "by the median")

	failed := at(4, 7.81)
	failed.Outcome = OutcomeFailed
	assert.Equal(t, Scaling{}, ScalingOf(belief(failed)), "only done runs teach")
}

func TestScaling_Scale(t *testing.T) {
	s := ScalingOf(belief(at(3, 2.74))) // p ≈ 0.953
	assert.InDelta(t, 33.6, s.scale(43, 3, 4), 0.1)
	assert.InDelta(t, 24.3, s.scale(43, 3, 6), 0.1)
	assert.Equal(t, 43.0, s.scale(43, 0, 4), "a run with no cores")
	assert.Equal(t, 43.0, s.scale(43, 3, 0), "no cores asked")
	assert.Equal(t, 43.0, Scaling{}.scale(43, 3, 4), "p unknown")
	assert.Equal(t, 43.0, Scaling{Overruns: true, Known: true, Parallel: 1}.scale(43, 3, 4), "an overrunner")
}
