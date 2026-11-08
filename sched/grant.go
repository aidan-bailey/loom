package sched

import "math"

// SpareWidth is the width a grant could widen queued job r of belief b
// (nil for none) to, or 0 when r can't be granted spare cores (job scaling
// spec §5.3): it isn't grantable, b's parallel fraction is unknown (an
// overrunner's always is), or b's ideal width is no wider than r's cores.
func SpareWidth(r Record, b *Belief) int {
	if !grantable(r) {
		return 0
	}
	s := ScalingOf(b)
	if !s.Known || s.Ideal <= r.Claim.CPUs {
		return 0
	}
	return s.Ideal
}

// grantable reports whether queued job r's claim may be widened past its
// base (job scaling spec §5.3): its cores learned from its own runs (not
// explicit, exclusive, the agent's, the family's or the default), and its
// memory neither explicit nor held to the cap.
func grantable(r Record) bool {
	return !r.Claim.Exclusive && r.Sources.CPUs == SourceLearned && r.Sources.Memory != SourceExplicit && !r.AboveCap
}

// ExploreWidth is the width a quiet host could explore queued job r of
// belief b at (exploration spec §3): ExploreTarget(b, capCPUs) for a job a
// grant could widen, 0 for any other.
func ExploreWidth(r Record, b *Belief, capCPUs int) int {
	if !grantable(r) {
		return 0
	}
	return ExploreTarget(b, capCPUs)
}

// HostIdle is the host's mean idle logical CPUs over the last IdleWindow
// (exploration spec §4.3), and whether they are known.
type HostIdle struct {
	Cores float64
	Known bool
}

// Grants is what Grant lends: each widened job's spare claim, and the jobs
// among them whose width is an explore (exploration spec §4.2). Both are
// nil when nothing is lent.
type Grants struct {
	Spare    map[JobID]Claim
	Explored map[JobID]bool
}

// add lends c to job id, marked an explore when explored.
func (g *Grants) add(id JobID, c Claim, explored bool) {
	if g.Spare == nil {
		g.Spare = map[JobID]Claim{}
	}
	g.Spare[id] = c
	if explored {
		if g.Explored == nil {
			g.Explored = map[JobID]bool{}
		}
		g.Explored[id] = true
	}
}

// exploreStep is the width an explore takes queued job r of belief b to
// (exploration spec §4.2), and the memory it adds: from ExploreWidth down
// to the larger of MinExploreWidth of its widest width and its base cores
// + 1, at most limit, the first whose memory (spareMemory) adds at most
// memory; false when none does.
func exploreStep(r Record, b *Belief, p Policy, limit, memory int) (width, extra int, ok bool) {
	base := r.Claim.CPUs
	target := ExploreWidth(r, b, p.Caps.CPUs)
	if target <= base {
		return 0, 0, false
	}
	low := max(MinExploreWidth(ScalingOf(b).Widest), base+1)
	for t := min(target, limit); t >= low; t-- {
		if extra := spareMemory(r, b, t, p) - r.Claim.MemoryGB; extra <= memory {
			return t, extra, true
		}
	}
	return 0, 0, false
}

// Grant is what each job dec starts gets past its base claim (job scaling
// spec §5.3, exploration spec §4.2), a pure function of its arguments: dec
// is Admit's decision over jobs under p, belief gives a record's belief
// (nil for none), and idle is the host's idle cores. It grants nothing
// while a queued job waits for anything but the caps; agent and run jobs
// are passed over, their waits stopping nothing and their starts granted
// nothing (flow in loom parts 3a and 3b). The pool is the caps less the
// running jobs' holds (holds, as Admit counts them), the starting jobs'
// base claims and a reserve of the default claim. In dec's order, each
// job explores when p.Explore is on and idle is known: exploreStep, its
// extra cores within the pool and within the idle cores less the reserve's
// cores and less what earlier explores took. Otherwise each job SpareWidth
// can widen takes the most cores up to that width whose memory
// (spareMemory) fits what the pool has left. The pool shrinks by what each
// took. A job that gets nothing has no entry.
func Grant(dec Decision, jobs []Record, p Policy, holds map[JobID]Need, belief func(Record) *Belief, idle HostIdle) Grants {
	var g Grants
	if len(dec.Start) == 0 || !p.usable() {
		return g
	}
	agent := map[JobID]bool{}
	for _, r := range jobs {
		if Unclaimed(r.Kind) {
			agent[r.ID] = true
		}
	}
	for id, why := range dec.Waiting {
		if why != waitOver && !agent[id] {
			return g
		}
	}
	starting := make(map[JobID]Record, len(dec.Start))
	for _, id := range dec.Start {
		starting[id] = Record{}
	}
	var used Need
	for _, r := range jobs {
		if agent[r.ID] {
			continue
		}
		if _, ok := starting[r.ID]; ok {
			starting[r.ID] = r
			used = used.plus(r.Claim.Need(p.Caps))
		} else if r.State == Running {
			used = used.plus(runningNeed(r, p, holds))
		}
	}
	reserve := p.DefaultClaim.Need(p.Caps)
	cores := p.Caps.CPUs - used.CPUs - reserve.CPUs
	memory := p.Caps.MemoryGB - used.MemoryGB - reserve.MemoryGB
	// An explore's extra cores must also be idle now, past the reserve's
	// (exploration spec §3).
	explore, idleRoom := p.Explore && idle.Known, 0
	if explore {
		idleRoom = int(math.Floor(idle.Cores)) - reserve.CPUs
	}
	for _, id := range dec.Start {
		if agent[id] {
			continue
		}
		r := starting[id]
		b := belief(r)
		base := r.Claim.CPUs
		if explore {
			if t, extra, ok := exploreStep(r, b, p, base+min(cores, idleRoom), memory); ok {
				g.add(id, Claim{CPUs: t - base, MemoryGB: extra}, true)
				cores, idleRoom, memory = cores-(t-base), idleRoom-(t-base), memory-extra
				continue
			}
		}
		for t := min(SpareWidth(r, b), base+cores); t > base; t-- {
			extra := spareMemory(r, b, t, p) - r.Claim.MemoryGB
			if extra > memory {
				continue
			}
			g.add(id, Claim{CPUs: t - base, MemoryGB: extra}, false)
			cores -= t - base
			memory -= extra
			break
		}
	}
	return g
}

// spareMemory is job r's memory at t cores (job scaling spec §5.3): its
// claim's memory grown in proportion to t over its claim's cores, lowered
// to the largest full peak, with its margin, of b's last runs that had t
// cores or more, but never below its claim's memory nor above the cap.
func spareMemory(r Record, b *Belief, t int, p Policy) int {
	m := int(math.Ceil(float64(r.Claim.MemoryGB) * float64(t) / float64(r.Claim.CPUs)))
	var peak int64
	for _, run := range b.recent() {
		if run.FullPeak() && run.Claim.CPUs >= t {
			peak = max(peak, run.PeakKB)
		}
	}
	if peak > 0 {
		m = min(m, withMargin(peak))
	}
	return min(max(m, r.Claim.MemoryGB), p.Caps.MemoryGB)
}
