package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var curveStart = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

// traceOf is a complete trace with one point every 3 s at the given
// progresses and memory (KB), each point 2 cores busy.
func traceOf(xs []float64, kb []int64) *Trace {
	var tr Trace
	for i := range xs {
		tr.Add(curveStart.Add(time.Duration(i+1)*3*time.Second), TracePoint{X: xs[i], RSSKB: kb[i], Cores: 2})
	}
	return &tr
}

func TestBuildCurve_KeepsTheRise(t *testing.T) {
	tr := traceOf([]float64{10, 20, 30, 40, 50}, []int64{1 << 20, 1 << 20, 8 << 20, 8 << 20, 2 << 20})
	c, ok := BuildCurve(tr, curveStart)
	require.True(t, ok)
	assert.Equal(t, 50.0, c.End)
	assert.Equal(t, []CurveStep{
		{Until: 20, Level: 1 << 20, Wall: 6},
		{Until: 40, Level: 8 << 20, Wall: 12},
		{Until: 50, Level: 2 << 20, Wall: 15},
	}, c.Mem)
	assert.Equal(t, []CurveStep{{Until: 50, Level: 2000, Wall: 15}}, c.Cores)
	require.NoError(t, c.Validate())
}

func TestBuildCurve_NeverBelowTheRunAndCompressed(t *testing.T) {
	var xs []float64
	var kb []int64
	for i := range 200 {
		xs = append(xs, float64(i+1)*1.5)
		kb = append(kb, int64((i*7919)%97+1)<<14)
	}
	tr := traceOf(xs, kb)
	c, ok := BuildCurve(tr, curveStart)
	require.True(t, ok)
	require.NoError(t, c.Validate())
	assert.LessOrEqual(t, len(c.Mem), curveSteps)
	for _, p := range tr.Points {
		level := int64(-1)
		for _, st := range c.Mem {
			if p.X <= st.Until {
				level = st.Level
				break
			}
		}
		assert.GreaterOrEqual(t, level, (p.RSSKB+1023)/1024*1024, "at progress %.1f", p.X)
	}
}

func TestBuildCurve_Refuses(t *testing.T) {
	_, ok := BuildCurve(nil, curveStart)
	assert.False(t, ok)
	_, ok = BuildCurve(&Trace{}, curveStart)
	assert.False(t, ok)
	tr := traceOf([]float64{1}, []int64{1})
	tr.Incomplete = true
	_, ok = BuildCurve(tr, curveStart)
	assert.False(t, ok)
	_, ok = BuildCurve(traceOf([]float64{1}, []int64{1}), time.Time{})
	assert.False(t, ok, "no start, no wall times")
}

func TestCurveStep_JSON(t *testing.T) {
	data, err := json.Marshal(CurveStep{Until: 12.5, Level: 1048576, Wall: 3.5})
	require.NoError(t, err)
	assert.JSONEq(t, `[12.5, 1048576, 3.5]`, string(data))
	var st CurveStep
	require.NoError(t, json.Unmarshal(data, &st))
	assert.Equal(t, CurveStep{Until: 12.5, Level: 1048576, Wall: 3.5}, st)
}

func TestCurve_Validate(t *testing.T) {
	good := Curve{End: 20, Mem: []CurveStep{{10, 1, 1}, {20, 2, 2}}, Cores: []CurveStep{{20, 1, 2}}}
	require.NoError(t, good.Validate())
	for name, c := range map[string]Curve{
		"no steps":      {End: 20, Mem: nil, Cores: good.Cores},
		"not moving on": {End: 20, Mem: []CurveStep{{10, 1, 1}, {10, 2, 2}}, Cores: good.Cores},
		"wall back":     {End: 20, Mem: []CurveStep{{10, 1, 3}, {20, 2, 2}}, Cores: good.Cores},
		"negative":      {End: 20, Mem: []CurveStep{{10, -1, 1}, {20, 2, 2}}, Cores: good.Cores},
		"short of end":  {End: 30, Mem: good.Mem, Cores: good.Cores},
	} {
		assert.Error(t, c.Validate(), name)
	}
}

func TestBelief_CurvesOnTheNewestRunsOnly(t *testing.T) {
	var b Belief
	for i := range ProfiledRuns + 2 {
		b.AddRun(Run{Job: JobID(i + 1), Outcome: OutcomeDone, Curve: &Curve{End: 1, Mem: []CurveStep{{1, 1, 1}}, Cores: []CurveStep{{1, 1, 1}}}})
	}
	for i, r := range b.Runs {
		assert.Equal(t, i < ProfiledRuns, r.Curve != nil, "run %d", i)
	}
	b.Runs[1].Curve.End = 0 // invalid
	assert.Len(t, CurvedRuns(&b), ProfiledRuns-1)
	assert.Empty(t, CurvedRuns(nil))
}
