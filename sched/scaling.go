package sched

import (
	"maps"
	"math"
	"slices"
)

// The constants of scaling (job scaling spec §5.1, fit spec §3).
const (
	overrunRatio  = 1.25 // busy ÷ cores past which a command overruns
	minEfficiency = 0.5  // the efficiency the ideal width stays above
	growthBound   = 2    // the ideal width is at most this × the most cores a run had
	floorGain     = 1.25 // the speed over the busy rule's cores a width needs to floor a claim
)

// The constants of exploring (exploration spec §3).
const (
	exploreFactor  = 2   // an explore's target, as a multiple of the widest width run
	minExploreStep = 1.5 // the narrowest explore, as a multiple of the widest width run
)

// ExploreTarget is the width a quiet host would run a job of belief b (nil
// for none) at to learn how its command scales (exploration spec §3): its
// widest width × exploreFactor, held to capCPUs. 0 when b explores
// nothing: it has no width, its command overruns, the held target is no
// wider than its widest width, or, with 2 widths or more, its two widest
// predict a speed at exploreFactor × the widest under floorGain × the
// speed at the widest. A pure function of its arguments.
func ExploreTarget(b *Belief, capCPUs int) int {
	s := ScalingOf(b)
	if s.Overruns || s.Widest < 1 {
		return 0
	}
	target := min(exploreFactor*s.Widest, capCPUs)
	if target <= s.Widest {
		return 0
	}
	if len(s.Widths) >= 2 {
		top := Scaling{Known: true, Parallel: s.Top}
		if top.factor(s.Widest)/top.factor(exploreFactor*s.Widest) < floorGain {
			return 0
		}
	}
	return target
}

// MinExploreWidth is the narrowest width an explore from widest takes
// (exploration spec §3): ⌈minExploreStep × widest⌉, and at least widest + 1.
// Two runs at one width differ in busy cores by a median 1.10×, so a
// narrower step mostly measures noise.
func MinExploreWidth(widest int) int {
	return max(int(math.Ceil(minExploreStep*float64(widest))), widest+1)
}

// Scaling is how a belief's command scales with cores (job scaling spec
// §5.1). While Known is false (no run with 2 cores or more measured it, or
// the command overruns, whose cores its claim doesn't set), Parallel, Runs
// and Ideal hold nothing.
type Scaling struct {
	Overruns bool    `json:"overruns,omitempty"` // its runs keep more than overrunRatio × their cores busy
	Busy     float64 `json:"busy,omitempty"`     // the median busy cores of its runs
	Known    bool    `json:"known,omitempty"`
	Parallel float64 `json:"parallel,omitempty"` // p: the share of its work that runs in parallel, 0–1
	Runs     int     `json:"runs,omitempty"`     // the runs Parallel came from
	Ideal    int     `json:"ideal,omitempty"`    // the most cores at which its efficiency stays above minEfficiency
	// Widths are the cores its runs had, ascending, when they had 2 widths
	// or more (fit spec §3); Fit is the parallel fraction fitted from their
	// median busy cores, which Parallel never falls below.
	Widths []int   `json:"widths,omitempty"`
	Fit    float64 `json:"fit,omitempty"`
	// Widest is the most cores any of the runs read had (exploration spec
	// §3), the overrunner's too; 0 with none. Top is the pair's p of the
	// two widest of Widths, set only with 2 widths or more.
	Widest int     `json:"widest,omitempty"`
	Top    float64 `json:"top,omitempty"`
}

// ScalingOf is how b's command scales (job scaling spec §5.1, fit spec
// §4.1), a pure function of b (nil for none). It reads the done runs with
// CPU time among b's last window runs, leaving out a run whose claim had no
// cores (an exclusive one). The command overruns when the median of their
// busy cores ÷ cores is above overrunRatio. Otherwise each of them with 2
// cores or more gives Amdahl's parallel fraction, (1 − 1/busy) ÷ (1 −
// 1/cores) held to 0–1, and Parallel is their median; with 2 widths or more
// the fit (fitParallel) raises it when it is higher, since a run's own
// figure assumes 1 core would be kept busy. Ideal is the most cores at
// which the efficiency 1 ÷ (c(1 − p) + p) stays above minEfficiency, at
// least 1 and at most growthBound × the most cores any of these runs had.
func ScalingOf(b *Belief) Scaling {
	var ratios, busies, ps []float64
	byWidth := map[int][]float64{}
	most := 0
	for _, r := range b.recent() {
		c := r.Claim.CPUs
		if !r.teachesCores() || c < 1 {
			continue
		}
		busy := r.CPUSeconds / r.WallSeconds
		ratios = append(ratios, busy/float64(c))
		busies = append(busies, busy)
		byWidth[c] = append(byWidth[c], busy)
		most = max(most, c)
		if c >= 2 {
			ps = append(ps, runParallel(busy, c))
		}
	}
	var s Scaling
	s.Busy, _ = median(busies)
	s.Widest = most
	if ratio, ok := median(ratios); ok && ratio > overrunRatio {
		s.Overruns = true
		return s
	}
	if p, ok := median(ps); ok {
		s.Known, s.Parallel, s.Runs = true, p, len(ps)
	}
	// Two widths include one of 2 cores or more, so Known is already true.
	if len(byWidth) >= 2 {
		s.Widths = slices.Sorted(maps.Keys(byWidth))
		s.Fit = fitParallel(s.Widths, byWidth)
		n := len(s.Widths)
		a, b := s.Widths[n-2], s.Widths[n-1]
		busyA, _ := median(byWidth[a])
		busyB, _ := median(byWidth[b])
		s.Top = pairParallel(a, busyA, b, busyB)
		if s.Fit > s.Parallel {
			s.Parallel, s.Runs = s.Fit, len(busies)
		}
	}
	if s.Known {
		s.Ideal = idealWidth(s.Parallel, growthBound*most)
	}
	return s
}

// fitParallel is the parallel fraction fitted from the busy cores at each
// of widths (2 or more, ascending, each a key of busy): the median of
// pairParallel over every pair of widths, each at its median busy cores
// (fit spec §3).
func fitParallel(widths []int, busy map[int][]float64) float64 {
	at := make([]float64, len(widths))
	for i, w := range widths {
		at[i], _ = median(busy[w])
	}
	var ps []float64
	for i := range widths {
		for j := i + 1; j < len(widths); j++ {
			ps = append(ps, pairParallel(widths[i], at[i], widths[j], at[j]))
		}
	}
	p, _ := median(ps)
	return p
}

// pairParallel is the parallel fraction that takes a command keeping busyA
// cores busy at a cores to busyB at b (a < b, busyA above 0): with R =
// busyB ÷ busyA, 0 when R ≤ 1, else (R − 1) ÷ (R(1 − 1/b) − (1 − 1/a))
// held to 1. The work is the same at both widths, so R is their speedup.
func pairParallel(a int, busyA float64, b int, busyB float64) float64 {
	r := busyB / busyA
	if r <= 1 {
		return 0
	}
	return min((r-1)/(r*(1-1/float64(b))-(1-1/float64(a))), 1)
}

// runParallel is the parallel fraction of a run at cores (2 or more) that
// kept busy cores busy, held to 0–1.
func runParallel(busy float64, cores int) float64 {
	p := (1 - 1/busy) / (1 - 1/float64(cores))
	return min(max(p, 0), 1)
}

// idealWidth is the most cores, from 1 to bound, at which a job of
// parallel fraction p keeps its efficiency above minEfficiency: the last c
// with c(1 − p) + p below 1 ÷ minEfficiency.
func idealWidth(p float64, bound int) int {
	ideal := 1
	for c := 2; c <= bound; c++ {
		if float64(c)*(1-p)+p >= 1/minEfficiency {
			break
		}
		ideal = c
	}
	return ideal
}

// factor is the speed factor at c cores, (1 − p) + p/c: a job's wall time
// at c cores is in proportion to it.
func (s Scaling) factor(c int) float64 { return 1 - s.Parallel + s.Parallel/float64(c) }

// scale is a wall time measured at from cores, at to cores (job scaling
// spec §5.4): wall × factor(to) ÷ factor(from), or wall as it is when
// either count is 0, Parallel is unknown or the command overruns.
func (s Scaling) scale(wall float64, from, to int) float64 {
	if from < 1 || to < 1 || !s.Known || s.Overruns {
		return wall
	}
	return wall * s.factor(to) / s.factor(from)
}

// fitted reports whether Parallel came from the fit (fit spec §5).
func (s Scaling) fitted() bool { return len(s.Widths) >= 2 && s.Parallel == s.Fit }

// floor is the fewest cores the fit lets a job of this scaling claim when
// the busy rule gave cores (fit spec §3): the widest of Widths at most
// limit and at most the ideal width under Fit alone, at which Fit predicts
// a speed at least floorGain × the speed at cores. 0 when none is, and
// with no fit.
func (s Scaling) floor(cores, limit int) int {
	if len(s.Widths) < 2 || cores < 1 {
		return 0
	}
	fit := Scaling{Known: true, Parallel: s.Fit}
	ideal := idealWidth(s.Fit, growthBound*s.Widths[len(s.Widths)-1])
	for _, w := range slices.Backward(s.Widths) {
		if w <= limit && w <= ideal && fit.factor(cores)/fit.factor(w) >= floorGain {
			return w
		}
	}
	return 0
}
