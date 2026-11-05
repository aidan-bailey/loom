package sched

import "math"

// The hold's rules (past-peak holds spec §5.3).
const (
	holdRuns  = 3   // the profiled runs a belief needs
	holdSeen  = 0.9 // the share of the lowest profiled peak a run must reach
	holdDwell = 2   // the newest points that must be within the envelope now
)

// HoldState is where a running job's hold stands between passes (spec
// §5.3): for each resource, the hold it was released to, 0 while it holds
// its claim. A released hold only falls.
type HoldState struct {
	CPUs     int
	MemoryGB int
}

// Exceeded names the released resource whose usage, rssKB of memory and
// cores busy, is above its hold, "" for none (spec §5.4).
func (h HoldState) Exceeded(rssKB int64, cores float64) string {
	switch {
	case h.MemoryGB > 0 && rssKB > int64(h.MemoryGB)*kbPerGB:
		return "memory"
	case h.CPUs > 0 && cores > float64(h.CPUs):
		return "cores"
	}
	return ""
}

// holdResource is a resource a hold can release: its staircase in a
// profile, a trace point's usage in the staircase's unit, and a level, with
// its margin, in the claim's unit (at least 1).
type holdResource struct {
	stair func(*Profile) Staircase
	usage func(TracePoint) int64
	hold  func(level int64) int
}

var (
	holdMemory = holdResource{
		stair: func(p *Profile) Staircase { return p.Mem },
		usage: func(pt TracePoint) int64 { return pt.RSSKB },
		hold:  withMargin,
	}
	holdCores = holdResource{
		stair: func(p *Profile) Staircase { return p.Cores },
		usage: func(pt TracePoint) int64 { return milliCores(pt.Cores) },
		hold:  func(milli int64) int { return max(1, int(math.Ceil(float64(milli)*coreHeadroom/1000))) },
	}
)

// Hold is what running job rec takes from p's caps now, and where its hold
// stands after this pass (past-peak holds spec §5.3), a pure function of its
// arguments: b is rec's belief (nil for none, or while learning is off), t
// its trace (nil for none), fresh whether the latest sample succeeded and t
// is fresh, and prev where the last pass left it. The need is the claim's
// but for a released resource; the I/O slot is always the claim's. When
// rule 1, 2 or 4 fails the need is the claim's and prev is kept.
func Hold(rec Record, b *Belief, t *Trace, fresh bool, prev HoldState, p Policy) (HoldState, Need) {
	claim := rec.Claim.Need(p.Caps)
	runs := profiledRuns(b)
	if rec.HoldBroke || !holdable(rec, t, fresh, p) || len(runs) < holdRuns || brokeRecently(b) {
		return prev, claim
	}
	x := t.x()
	if len(aliveAt(runs, x)) == 0 {
		return prev, claim
	}
	next := HoldState{
		CPUs:     holdCores.released(prev.CPUs, claim.CPUs, t, runs, x),
		MemoryGB: holdMemory.released(prev.MemoryGB, claim.MemoryGB, t, runs, x),
	}
	need := claim
	if next.CPUs > 0 {
		need.CPUs = next.CPUs
	}
	if next.MemoryGB > 0 {
		need.MemoryGB = next.MemoryGB
	}
	return next, need
}

// holdable is rule 2: a complete, fresh trace of a job that was not
// adopted, with the switch on and a claim neither exclusive nor fully
// explicit.
func holdable(rec Record, t *Trace, fresh bool, p Policy) bool {
	return p.ReleasePastPeaks && fresh && t != nil && len(t.Points) > 0 && !t.Incomplete && !rec.Adopted &&
		!rec.Claim.Exclusive && !(rec.Sources.CPUs == SourceExplicit && rec.Sources.Memory == SourceExplicit)
}

// profiledRuns is the valid profiles among b's newest ProfiledRuns runs.
func profiledRuns(b *Belief) []*Profile {
	if b == nil {
		return nil
	}
	var out []*Profile
	for _, r := range b.Runs[:min(len(b.Runs), ProfiledRuns)] {
		if r.Profile != nil && r.Profile.Validate() == nil {
			out = append(out, r.Profile)
		}
	}
	return out
}

// brokeRecently reports whether one of b's last window runs broke its hold.
func brokeRecently(b *Belief) bool {
	for _, r := range b.recent() {
		if r.HoldBroke {
			return true
		}
	}
	return false
}

// aliveAt is the runs whose end is past progress x.
func aliveAt(runs []*Profile, x float64) []*Profile {
	var out []*Profile
	for _, p := range runs {
		if p.End > x {
			out = append(out, p)
		}
	}
	return out
}

// envelope is the most any of runs still used from progress x (rule 4).
func (r holdResource) envelope(runs []*Profile, x float64) int64 {
	var most int64
	for _, p := range runs {
		if v, ok := r.stair(p).At(x); ok {
			most = max(most, v)
		}
	}
	return most
}

// released is the resource's hold after this pass, 0 while it holds its
// claim: a released one (prev > 0) takes the lower of prev and the
// candidate; an unreleased one is released to the candidate when it has
// seen its peak (rule 3), tracks the envelope (rule 5) and the candidate is
// below the claim.
func (r holdResource) released(prev, claim int, t *Trace, runs []*Profile, x float64) int {
	alive := aliveAt(runs, x)
	now := r.envelope(alive, x)
	cand := min(r.hold(now), claim)
	if prev > 0 {
		return min(prev, cand)
	}
	lowest := int64(math.MaxInt64)
	for _, p := range runs {
		lowest = min(lowest, r.stair(p).Peak())
	}
	var seen int64
	for _, pt := range t.Points {
		seen = max(seen, r.usage(pt))
	}
	if float64(seen) < holdSeen*float64(lowest) {
		return 0
	}
	for _, p := range alive {
		if x <= r.stair(p).PeakAt {
			return 0
		}
	}
	if len(t.Points) < holdDwell {
		return 0
	}
	for _, pt := range t.Points[len(t.Points)-holdDwell:] {
		if r.usage(pt) > now {
			return 0
		}
	}
	if cand >= claim {
		return 0
	}
	return cand
}
