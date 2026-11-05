package sched

import "math"

// SpareWidth is the width a grant could widen queued job r of belief b
// (nil for none) to, or 0 when r can't be granted spare cores (job scaling
// spec §5.3): its cores aren't learned (they are explicit, exclusive, the
// agent's or the default), its memory is explicit or held to the cap, b's
// parallel fraction is unknown (an overrunner's always is), or b's ideal
// width is no wider than r's cores.
func SpareWidth(r Record, b *Belief) int {
	if r.Claim.Exclusive || r.Sources.CPUs != SourceLearned || r.Sources.Memory == SourceExplicit || r.AboveCap {
		return 0
	}
	s := ScalingOf(b)
	if !s.Known || s.Ideal <= r.Claim.CPUs {
		return 0
	}
	return s.Ideal
}

// Grant is what each job dec starts gets past its base claim (job scaling
// spec §5.3), a pure function of its arguments: dec is Admit's decision
// over jobs under p, and belief gives a record's belief (nil for none). It
// grants nothing while a queued job waits for anything but the caps. The
// pool is the caps less the running jobs' holds (holds, as Admit counts
// them), the starting jobs' base
// claims and a reserve of the default claim. In dec's order, each job
// SpareWidth can widen takes the most cores up to that width whose memory
// (spareMemory) fits what the pool has left, and the pool shrinks by what
// it took. A job that gets nothing has no entry; nil when none gets
// anything.
func Grant(dec Decision, jobs []Record, p Policy, holds map[JobID]Need, belief func(Record) *Belief) map[JobID]Claim {
	if len(dec.Start) == 0 || !p.usable() {
		return nil
	}
	for _, why := range dec.Waiting {
		if why != waitOver {
			return nil
		}
	}
	starting := make(map[JobID]Record, len(dec.Start))
	for _, id := range dec.Start {
		starting[id] = Record{}
	}
	var used Need
	for _, r := range jobs {
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
	var out map[JobID]Claim
	for _, id := range dec.Start {
		r := starting[id]
		b := belief(r)
		for t := min(SpareWidth(r, b), r.Claim.CPUs+cores); t > r.Claim.CPUs; t-- {
			extra := spareMemory(r, b, t, p) - r.Claim.MemoryGB
			if extra > memory {
				continue
			}
			if out == nil {
				out = map[JobID]Claim{}
			}
			out[id] = Claim{CPUs: t - r.Claim.CPUs, MemoryGB: extra}
			cores -= t - r.Claim.CPUs
			memory -= extra
			break
		}
	}
	return out
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
