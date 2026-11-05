package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// The trace's and the profile's bounds (past-peak holds spec §5.1, §5.2).
const (
	tracePoints  = 256              // a trace halves past this many points
	traceGap     = 30 * time.Second // a longer gap leaves a trace incomplete, and a newest point older than this is stale
	profileSteps = 8                // a staircase's most steps
	ProfiledRuns = 5                // the newest runs of a belief that keep their profiles
)

// TracePoint is one sample of a running job (past-peak holds spec §5.1):
// its progress (X, the CPU-seconds its session has used), its memory, and
// the cores it kept busy since the sample before.
type TracePoint struct {
	X     float64
	RSSKB int64
	Cores float64
}

// Trace is a running job's samples. The model keeps it in memory only.
type Trace struct {
	Points     []TracePoint
	Last       time.Time // when the newest point was taken
	Incomplete bool      // two points were more than traceGap apart
}

// Add appends p, sampled at at. Its progress never falls below the point
// before (a process that left the session took its CPU time with it); a gap
// over traceGap since the last point leaves the trace incomplete; past
// tracePoints points, adjacent pairs merge into one with the later progress
// and the higher memory and cores, so every peak stays.
func (t *Trace) Add(at time.Time, p TracePoint) {
	if n := len(t.Points); n > 0 {
		if at.Sub(t.Last) > traceGap {
			t.Incomplete = true
		}
		p.X = max(p.X, t.Points[n-1].X)
	}
	t.Points = append(t.Points, p)
	t.Last = at
	if len(t.Points) <= tracePoints {
		return
	}
	merged := t.Points[:0]
	for i := 0; i < len(t.Points); i += 2 {
		a := t.Points[i]
		if i+1 < len(t.Points) {
			b := t.Points[i+1]
			a = TracePoint{X: b.X, RSSKB: max(a.RSSKB, b.RSSKB), Cores: max(a.Cores, b.Cores)}
		}
		merged = append(merged, a)
	}
	t.Points = merged
}

// Fresh reports whether t has a point taken within traceGap of now.
func (t *Trace) Fresh(now time.Time) bool {
	return t != nil && len(t.Points) > 0 && now.Sub(t.Last) <= traceGap
}

// x is t's progress: its newest point's.
func (t *Trace) x() float64 { return t.Points[len(t.Points)-1].X }

// Step is one step of a staircase: Level holds for progress below Until. It
// is written as the pair [until, level].
type Step struct {
	Until float64
	Level int64
}

// MarshalJSON writes s as [until, level].
func (s Step) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]float64{s.Until, float64(s.Level)})
}

// UnmarshalJSON reads [until, level].
func (s *Step) UnmarshalJSON(b []byte) error {
	var pair [2]float64
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	s.Until, s.Level = pair[0], int64(pair[1])
	return nil
}

// Staircase is one resource of a profile (spec §5.2): the most a run still
// used from each point of its progress to its end. Its levels fall, so it is
// held as at most profileSteps steps. PeakAt is the progress at which the run
// first reached its peak.
type Staircase struct {
	PeakAt float64 `json:"peak_at"`
	Steps  []Step  `json:"steps"`
}

// Peak is the run's peak: the first step's level, 0 for no steps.
func (s Staircase) Peak() int64 {
	if len(s.Steps) == 0 {
		return 0
	}
	return s.Steps[0].Level
}

// At is the most the run used from progress x to its end, and false once x
// is at or past its end.
func (s Staircase) At(x float64) (int64, bool) {
	for _, st := range s.Steps {
		if x < st.Until {
			return st.Level, true
		}
	}
	return 0, false
}

// Profile is a done run's usage over its progress (spec §5.2): memory in KB,
// cores in milli-cores, and the progress at its end.
type Profile struct {
	End   float64   `json:"end"`
	Mem   Staircase `json:"mem"`
	Cores Staircase `json:"cores"`
}

// BuildProfile is t's profile, and false for no trace, an empty one or an
// incomplete one (spec §5.2).
func BuildProfile(t *Trace) (*Profile, bool) {
	if t == nil || len(t.Points) == 0 || t.Incomplete {
		return nil, false
	}
	return &Profile{
		End:   roundUpX(t.x()),
		Mem:   buildStaircase(t.Points, func(p TracePoint) int64 { return (p.RSSKB + 1023) / 1024 * 1024 }),
		Cores: buildStaircase(t.Points, func(p TracePoint) int64 { return milliCores(p.Cores) }),
	}, true
}

// milliCores is cores in whole milli-cores, rounded up.
func milliCores(cores float64) int64 { return int64(math.Ceil(cores * 1000)) }

// roundUpX is progress x rounded up to a tenth of a CPU-second.
func roundUpX(x float64) float64 { return math.Ceil(x*10) / 10 }

// buildStaircase is the suffix maximum of level over points, one step per
// progress (the first, highest, of equal ones) and per level (the last
// progress of equal ones), merged down to profileSteps by joining, each time,
// the adjacent pair whose merge adds the least area; a merge keeps the higher
// level and the later Until, so the staircase is never below the run.
func buildStaircase(points []TracePoint, level func(TracePoint) int64) Staircase {
	var s Staircase
	peak := int64(-1)
	for _, p := range points {
		if v := level(p); v > peak {
			peak, s.PeakAt = v, roundUpX(p.X)
		}
	}
	raw := make([]Step, len(points))
	most := int64(0)
	for i := len(points) - 1; i >= 0; i-- {
		most = max(most, level(points[i]))
		raw[i] = Step{Until: roundUpX(points[i].X), Level: most}
	}
	for _, st := range raw {
		k := len(s.Steps) - 1
		switch {
		case k >= 0 && s.Steps[k].Until == st.Until:
			// The same progress: the first, highest, level holds.
		case k >= 0 && s.Steps[k].Level == st.Level:
			s.Steps[k].Until = st.Until
		default:
			s.Steps = append(s.Steps, st)
		}
	}
	for len(s.Steps) > profileSteps {
		best, cost := 0, math.Inf(1)
		for i := 0; i+1 < len(s.Steps); i++ {
			c := float64(s.Steps[i].Level-s.Steps[i+1].Level) * (s.Steps[i+1].Until - s.Steps[i].Until)
			if c < cost {
				best, cost = i, c
			}
		}
		s.Steps[best].Until = s.Steps[best+1].Until
		s.Steps = slices.Delete(s.Steps, best+1, best+2)
	}
	return s
}

// Validate says why p can't be trusted, nil when it can: each staircase has
// 1 to profileSteps steps whose progress rises and whose levels fall, nothing
// negative, a peak within its first step, and a last step ending at End.
func (p *Profile) Validate() error {
	if err := p.Mem.validate(p.End); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	if err := p.Cores.validate(p.End); err != nil {
		return fmt.Errorf("cores: %w", err)
	}
	return nil
}

func (s Staircase) validate(end float64) error {
	switch {
	case len(s.Steps) == 0:
		return errors.New("no steps")
	case len(s.Steps) > profileSteps:
		return fmt.Errorf("%d steps, over %d", len(s.Steps), profileSteps)
	}
	for i, st := range s.Steps {
		switch {
		case st.Level < 0 || !(st.Until >= 0):
			return fmt.Errorf("step %d is negative", i+1)
		case i > 0 && st.Until <= s.Steps[i-1].Until:
			return fmt.Errorf("step %d does not move on", i+1)
		case i > 0 && st.Level > s.Steps[i-1].Level:
			return fmt.Errorf("step %d rises", i+1)
		}
	}
	switch {
	case !(s.PeakAt >= 0) || s.PeakAt > s.Steps[0].Until:
		return errors.New("its peak is not in its first step")
	case s.Steps[len(s.Steps)-1].Until != end:
		return errors.New("its last step does not end at the run's end")
	}
	return nil
}
