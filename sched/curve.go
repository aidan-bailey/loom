package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// curveSteps is a curve's most steps per resource (cgroup spec §6.1).
const curveSteps = 16

// CurveStep is one step of a curve: Level holds for progress from the step
// before's Until (0 for the first) up to Until, and the run had been going
// Wall seconds when its progress reached Until. It is written as
// [until, level, wall].
type CurveStep struct {
	Until float64
	Level int64
	Wall  float64
}

// MarshalJSON writes s as [until, level, wall].
func (s CurveStep) MarshalJSON() ([]byte, error) {
	return json.Marshal([3]float64{s.Until, float64(s.Level), s.Wall})
}

// UnmarshalJSON reads [until, level, wall].
func (s *CurveStep) UnmarshalJSON(b []byte) error {
	var v [3]float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.Until, s.Level, s.Wall = v[0], int64(v[1]), v[2]
	return nil
}

// Curve is a done run's usage over its progress, rising and falling
// (cgroup spec §6.1): memory in KB rounded up to MB, cores in milli-cores,
// and the progress at its end.
type Curve struct {
	End   float64     `json:"end"`
	Mem   []CurveStep `json:"mem"`
	Cores []CurveStep `json:"cores"`
}

// BuildCurve is t's curve for a run that started at started, and false for
// no trace, an empty or incomplete one, or no start.
func BuildCurve(t *Trace, started time.Time) (*Curve, bool) {
	if t == nil || len(t.Points) == 0 || t.Incomplete || started.IsZero() {
		return nil, false
	}
	return &Curve{
		End:   roundUpX(t.x()),
		Mem:   buildCurveSteps(t.Points, started, func(p TracePoint) int64 { return (p.RSSKB + 1023) / 1024 * 1024 }),
		Cores: buildCurveSteps(t.Points, started, func(p TracePoint) int64 { return milliCores(p.Cores) }),
	}, true
}

// buildCurveSteps is one step per point (the highest level of points at one
// progress, and one step for a run of equal levels), merged down to
// curveSteps by joining, each time, the adjacent pair whose merge adds the
// least area; a merge keeps the higher level and the later Until and Wall,
// so the curve is never below the run.
func buildCurveSteps(points []TracePoint, started time.Time, level func(TracePoint) int64) []CurveStep {
	var steps []CurveStep
	for _, p := range points {
		st := CurveStep{Until: roundUpX(p.X), Level: level(p), Wall: max(0, p.At.Sub(started).Seconds())}
		k := len(steps) - 1
		switch {
		case k >= 0 && steps[k].Until == st.Until:
			steps[k].Level, steps[k].Wall = max(steps[k].Level, st.Level), st.Wall
		case k >= 0 && steps[k].Level == st.Level:
			steps[k].Until, steps[k].Wall = st.Until, st.Wall
		default:
			steps = append(steps, st)
		}
	}
	from := func(i int) float64 {
		if i == 0 {
			return 0
		}
		return steps[i-1].Until
	}
	for len(steps) > curveSteps {
		best, cost := 0, math.Inf(1)
		for i := 0; i+1 < len(steps); i++ {
			top := max(steps[i].Level, steps[i+1].Level)
			c := float64(top-steps[i].Level)*(steps[i].Until-from(i)) + float64(top-steps[i+1].Level)*(steps[i+1].Until-steps[i].Until)
			if c < cost {
				best, cost = i, c
			}
		}
		next := steps[best+1]
		steps[best] = CurveStep{Until: next.Until, Level: max(steps[best].Level, next.Level), Wall: next.Wall}
		steps = slices.Delete(steps, best+1, best+2)
	}
	return steps
}

// Validate says why c can't be trusted, nil when it can: each resource has 1
// to curveSteps steps whose progress rises, whose wall times never fall,
// nothing negative, and a last step ending at End.
func (c *Curve) Validate() error {
	if err := validateCurve(c.Mem, c.End); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	if err := validateCurve(c.Cores, c.End); err != nil {
		return fmt.Errorf("cores: %w", err)
	}
	return nil
}

func validateCurve(steps []CurveStep, end float64) error {
	switch {
	case len(steps) == 0:
		return errors.New("no steps")
	case len(steps) > curveSteps:
		return fmt.Errorf("%d steps, over %d", len(steps), curveSteps)
	}
	for i, st := range steps {
		switch {
		case st.Level < 0 || !(st.Until >= 0) || !(st.Wall >= 0):
			return fmt.Errorf("step %d is negative", i+1)
		case i > 0 && st.Until <= steps[i-1].Until:
			return fmt.Errorf("step %d does not move on", i+1)
		case i > 0 && st.Wall < steps[i-1].Wall:
			return fmt.Errorf("step %d goes back in time", i+1)
		}
	}
	if steps[len(steps)-1].Until != end {
		return errors.New("its last step does not end at the run's end")
	}
	return nil
}

// CurvedRuns is the valid curves among b's newest ProfiledRuns runs.
func CurvedRuns(b *Belief) []*Curve {
	if b == nil {
		return nil
	}
	var out []*Curve
	for _, r := range b.Runs[:min(len(b.Runs), ProfiledRuns)] {
		if r.Curve != nil && r.Curve.Validate() == nil {
			out = append(out, r.Curve)
		}
	}
	return out
}
