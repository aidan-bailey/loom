package sched

// The constants of scaling (job scaling spec §5.1).
const (
	overrunRatio  = 1.25 // busy ÷ cores past which a command overruns
	minEfficiency = 0.5  // the efficiency the ideal width stays above
	growthBound   = 2    // the ideal width is at most this × the most cores a run had
)

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
}

// ScalingOf is how b's command scales (job scaling spec §5.1), a pure
// function of b (nil for none). It reads the done runs with CPU time among
// b's last window runs, leaving out a run whose claim had no cores (an
// exclusive one). The command overruns when the median of their busy
// cores ÷ cores is above overrunRatio. Otherwise each of them with 2 cores
// or more gives Amdahl's parallel fraction, (1 − 1/busy) ÷ (1 − 1/cores)
// held to 0–1, and Parallel is their median; Ideal is the most cores at
// which the efficiency 1 ÷ (c(1 − p) + p) stays above minEfficiency, at
// least 1 and at most growthBound × the most cores any of these runs had.
func ScalingOf(b *Belief) Scaling {
	var ratios, busies, ps []float64
	most := 0
	for _, r := range b.recent() {
		c := r.Claim.CPUs
		if !r.teachesCores() || c < 1 {
			continue
		}
		busy := r.CPUSeconds / r.WallSeconds
		ratios = append(ratios, busy/float64(c))
		busies = append(busies, busy)
		most = max(most, c)
		if c >= 2 {
			ps = append(ps, runParallel(busy, c))
		}
	}
	var s Scaling
	s.Busy, _ = median(busies)
	if ratio, ok := median(ratios); ok && ratio > overrunRatio {
		s.Overruns = true
		return s
	}
	if p, ok := median(ps); ok {
		s.Known, s.Parallel, s.Runs = true, p, len(ps)
		s.Ideal = idealWidth(p, growthBound*most)
	}
	return s
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
