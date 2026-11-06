package sched

import (
	"math"
	"slices"
	"time"
)

// overdueRuns is how many duration runs a belief needs before a job of it
// can be overdue.
const overdueRuns = 3

// DurationSource is where a usual duration came from (family sizing spec
// §5.4).
type DurationSource string

// The sources of a usual duration.
const (
	DurationNone    DurationSource = ""
	DurationLearned DurationSource = "learned" // the belief's own runs
	DurationFamily  DurationSource = "family"  // its family pool's runs, unscaled
	DurationPrior   DurationSource = "agent"   // the advisor's prior
)

// Usual is a belief's usual duration and its P90 at cores (smart scheduler
// spec §5.5, job scaling spec §5.4, family sizing spec §5.4): the median
// and the 90th percentile (nearest rank) of the wall times of its duration
// runs among its last window, each scaled from the cores it had to cores
// (Scaling.scale: as measured when cores is 0, b's parallel fraction is
// unknown or it overruns); n is how many there were. With none, its family
// pool's duration runs stand in, unscaled (a pool mixes members whose
// scaling differs); with none of those either, the prior's duration stands
// in for both, unscaled, with n 0; with no prior, all are zero. src says
// which.
func Usual(b, family *Belief, cores int) (usual, p90 time.Duration, n int, src DurationSource) {
	s := ScalingOf(b)
	var walls []float64
	for _, r := range b.recent() {
		if r.TeachesDuration() {
			walls = append(walls, s.scale(r.WallSeconds, r.Claim.CPUs, cores))
		}
	}
	src = DurationLearned
	if len(walls) == 0 {
		for _, r := range family.recent() {
			if r.TeachesDuration() {
				walls = append(walls, r.WallSeconds)
			}
		}
		src = DurationFamily
	}
	if len(walls) == 0 {
		if b != nil && b.Prior != nil && b.Prior.DurationSeconds > 0 {
			d := time.Duration(b.Prior.DurationSeconds) * time.Second
			return d, d, 0, DurationPrior
		}
		return 0, 0, 0, DurationNone
	}
	m, _ := median(walls)
	slices.Sort(walls)
	rank := max(0, int(math.Ceil(0.9*float64(len(walls))))-1)
	return seconds(m), seconds(walls[rank]), len(walls), src
}

// OverdueAfter is how long a job of b at cores may run before it is
// overdue: max(2 × P90, P90 + 5 minutes) of Usual over b's own runs at
// cores once b has overdueRuns duration runs, else 0 (never). A family's
// runs never make a job overdue (family sizing spec §5.4).
func OverdueAfter(b *Belief, cores int) time.Duration {
	_, p90, n, _ := Usual(b, nil, cores)
	if n < overdueRuns {
		return 0
	}
	return max(2*p90, p90+5*time.Minute)
}

// seconds is s seconds as a duration, to the second.
func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second)).Round(time.Second)
}
