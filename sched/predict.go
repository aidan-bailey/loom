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

// Span is a stretch of time in which a job is expected to use KB of memory.
type Span struct {
	From, Until time.Time
	KB          int64
}

// Prediction is a job's expected memory over time (cgroup spec §7.2): spans
// in time order that never overlap, nothing outside them.
type Prediction []Span

// At is what p expects at t: a span holds its From and not its Until.
func (p Prediction) At(t time.Time) int64 {
	i := sort.Search(len(p), func(i int) bool { return p[i].Until.After(t) })
	if i < len(p) && !t.Before(p[i].From) {
		return p[i].KB
	}
	return 0
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

// envelope is the most of spans at each moment: between each pair of
// adjacent edges, the highest level of the spans holding that stretch;
// equal neighbours merge, and stretches at 0 drop.
func envelope(spans []Span) Prediction {
	var edges []time.Time
	for _, s := range spans {
		edges = append(edges, s.From, s.Until)
	}
	slices.SortFunc(edges, func(a, b time.Time) int { return a.Compare(b) })
	edges = slices.CompactFunc(edges, func(a, b time.Time) bool { return a.Equal(b) })
	var out Prediction
	for i := 0; i+1 < len(edges); i++ {
		from, until := edges[i], edges[i+1]
		var kb int64
		for _, s := range spans {
			if !s.From.After(from) && s.Until.After(from) {
				kb = max(kb, s.KB)
			}
		}
		switch n := len(out); {
		case kb == 0:
		case n > 0 && out[n-1].KB == kb && out[n-1].Until.Equal(from):
			out[n-1].Until = until
		default:
			out = append(out, Span{From: from, Until: until, KB: kb})
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
// past x. It is false when fewer than holdRuns runs did.
func Predict(curves []*Curve, x float64, anchor time.Time) (Prediction, bool) {
	var spans []Span
	n := 0
	for _, c := range curves {
		if c.End > x {
			spans = append(spans, runSpans(c, x, anchor)...)
			n++
		}
	}
	if n < holdRuns {
		return nil, false
	}
	return envelope(spans), true
}

// PeakSum is the most preds use together in [from, to): their sum at from
// and at the start of every span within it, where a sum can only rise.
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
		var sum int64
		for _, p := range preds {
			sum += p.At(t)
		}
		peak = max(peak, sum)
	}
	return peak
}
