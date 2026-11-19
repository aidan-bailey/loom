package sched

import (
	"math"
	"slices"
	"sort"
	"time"
)

// The prediction's margins (cgroup spec §7.2).
const (
	widenMin   = 30 * time.Second // the least a predicted edge moves
	widenShare = 0.25             // of how far ahead of its anchor the edge is
)

// Span is a stretch of time in which a job is expected to use KB of memory
// at most, and KB less ExcessKB in a usual run (usual plus one worst spec).
// ExcessKB 0, the zero value, says nothing is known below the worst.
type Span struct {
	From, Until time.Time
	KB          int64
	ExcessKB    int64
}

// usualKB is what a usual run uses in s.
func (s Span) usualKB() int64 { return s.KB - s.ExcessKB }

// Prediction is a job's expected memory over time (cgroup spec §7.2): spans
// in time order that never overlap, nothing outside them.
type Prediction []Span

// At is the most p expects at t: a span holds its From and not its Until.
func (p Prediction) At(t time.Time) int64 { return p.spanAt(t).KB }

// spanAt is p's span holding t, the zero span for none.
func (p Prediction) spanAt(t time.Time) Span {
	i := sort.Search(len(p), func(i int) bool { return p[i].Until.After(t) })
	if i < len(p) && !t.Before(p[i].From) {
		return p[i]
	}
	return Span{}
}

// UsualAtMost is a copy of p whose usual level is at most kb in each span,
// and never below 0: a flat claim's, from its belief's usual peak.
func (p Prediction) UsualAtMost(kb int64) Prediction {
	out := slices.Clone(p)
	for i := range out {
		out[i].ExcessKB = out[i].KB - min(out[i].usualKB(), max(kb, 0))
	}
	return out
}

// UsualAtLeast is a copy of p whose usual level is at least kb in each span,
// and never above its worst: a running job's, from its measured use.
func (p Prediction) UsualAtLeast(kb int64) Prediction {
	out := slices.Clone(p)
	for i := range out {
		out[i].ExcessKB = out[i].KB - min(out[i].KB, max(out[i].usualKB(), kb))
	}
	return out
}

// End is when p's last span ends, zero for none.
func (p Prediction) End() time.Time {
	if len(p) == 0 {
		return time.Time{}
	}
	return p[len(p)-1].Until
}

// Flat is kb from anchor to until, or for a minute when until has passed.
func Flat(kb int64, anchor, until time.Time) Prediction {
	if !until.After(anchor) {
		until = anchor.Add(time.Minute)
	}
	return Prediction{{From: anchor, Until: until, KB: kb}}
}

// WithMarginKB is kb with sizing's 15% margin, rounded up.
func WithMarginKB(kb int64) int64 { return int64(math.Ceil(float64(kb) * memoryMargin)) }

// envelope is the most of runs' spans at each moment, and the median run
// (usual plus one worst spec): between each pair of adjacent edges, each
// run's level is the highest of its spans holding that stretch, or 0; the
// stretch's KB is the highest run's level and its usual level the median
// run's (the upper one for an even count). Equal neighbours merge, and
// stretches at 0 drop.
func envelope(runs [][]Span) Prediction {
	var edges []time.Time
	for _, spans := range runs {
		for _, s := range spans {
			edges = append(edges, s.From, s.Until)
		}
	}
	slices.SortFunc(edges, func(a, b time.Time) int { return a.Compare(b) })
	edges = slices.CompactFunc(edges, func(a, b time.Time) bool { return a.Equal(b) })
	var out Prediction
	levels := make([]int64, len(runs))
	for i := 0; i+1 < len(edges); i++ {
		from, until := edges[i], edges[i+1]
		for j, spans := range runs {
			levels[j] = 0
			for _, s := range spans {
				if !s.From.After(from) && s.Until.After(from) {
					levels[j] = max(levels[j], s.KB)
				}
			}
		}
		slices.Sort(levels)
		kb := levels[len(levels)-1]
		excess := kb - levels[len(levels)/2]
		switch n := len(out); {
		case kb == 0:
		case n > 0 && out[n-1].KB == kb && out[n-1].ExcessKB == excess && out[n-1].Until.Equal(from):
			out[n-1].Until = until
		default:
			out = append(out, Span{From: from, Until: until, KB: kb, ExcessKB: excess})
		}
	}
	return out
}

// widen is how far a predicted edge at t moves from anchor's view: 30 s, or
// a quarter of how far ahead it is, whichever is more.
func widen(anchor, t time.Time) time.Duration {
	return max(widenMin, time.Duration(widenShare*float64(t.Sub(anchor))))
}

// wallAt is the wall time c's run had been going when its progress reached
// x, interpolated within the memory step holding x; past its end, its last.
func (c *Curve) wallAt(x float64) float64 {
	prevUntil, prevWall := 0.0, 0.0
	for _, st := range c.Mem {
		if x < st.Until {
			return prevWall + (st.Wall-prevWall)*(x-prevUntil)/(st.Until-prevUntil)
		}
		prevUntil, prevWall = st.Until, st.Wall
	}
	return prevWall
}

// runSpans is c's memory from progress x on, from anchor: each step ending
// past x lasts from the wall time the run reached its start (or x, for the
// step holding x) to the wall time it reached its end, less the run's wall
// time at x; each level carries the margin, and each edge is widened but
// never moved before anchor.
func runSpans(c *Curve, x float64, anchor time.Time) []Span {
	wallX := c.wallAt(x)
	var out []Span
	prevWall := 0.0
	for _, st := range c.Mem {
		if st.Until > x {
			from := anchor.Add(seconds(max(prevWall, wallX) - wallX))
			until := anchor.Add(seconds(st.Wall - wallX))
			from, until = from.Add(-widen(anchor, from)), until.Add(widen(anchor, until))
			if from.Before(anchor) {
				from = anchor
			}
			out = append(out, Span{From: from, Until: until, KB: WithMarginKB(st.Level)})
		}
		prevWall = st.Wall
	}
	return out
}

// Predict is a job's prediction from its belief's curves (cgroup spec
// §7.2): from progress x (0 for a job not started) and anchored at anchor
// (now, or the start), the most of the spans of every curve whose run lasted
// past x, with the median of them as the usual level (usual plus one worst
// spec). It is false when fewer than holdRuns runs did.
func Predict(curves []*Curve, x float64, anchor time.Time) (Prediction, bool) {
	var runs [][]Span
	for _, c := range curves {
		if c.End > x {
			runs = append(runs, runSpans(c, x, anchor))
		}
	}
	if len(runs) < holdRuns {
		return nil, false
	}
	return envelope(runs), true
}

// UsualPeakKB is the usual peak of b's own runs (usual plus one worst spec):
// the median of the full peaks among its newest window runs (the upper one
// for an even count), with sizing's margin, and false with fewer than
// holdRuns of them. A run with no peak (one that ended before a sample) is
// no evidence, and a family's runs never count: they mix filtered and full
// runs.
func UsualPeakKB(b *Belief) (int64, bool) {
	if b == nil {
		return 0, false
	}
	var peaks []int64
	for _, r := range b.Runs[:min(len(b.Runs), window)] {
		if r.FullPeak() && r.PeakKB > 0 {
			peaks = append(peaks, r.PeakKB)
		}
	}
	if len(peaks) < holdRuns {
		return 0, false
	}
	slices.Sort(peaks)
	return WithMarginKB(peaks[len(peaks)/2]), true
}

// PeakSum is the most preds use together in [from, to), at from and at the
// start of every span within it, where a sum can only rise (usual plus one
// worst spec): each prediction at its usual level, plus the largest excess
// of any one of them over its usual level, since one job at its worst at a
// time is what history shows and the ladder takes the rarer coincidence.
// A single prediction's sum is its worst.
func PeakSum(preds []Prediction, from, to time.Time) int64 {
	points := []time.Time{from}
	for _, p := range preds {
		for _, s := range p {
			if s.From.After(from) && s.From.Before(to) {
				points = append(points, s.From)
			}
		}
	}
	var peak int64
	for _, t := range points {
		var sum, excess int64
		for _, p := range preds {
			s := p.spanAt(t)
			sum += s.usualKB()
			excess = max(excess, s.ExcessKB)
		}
		peak = max(peak, sum+excess)
	}
	return peak
}
