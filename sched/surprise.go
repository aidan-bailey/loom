package sched

import "fmt"

// NeedsPrior reports whether a job that asked for asked, of belief b (nil
// for a signature with none yet) and family pool family (nil for none),
// would take some part it didn't claim from the default where an advisor's
// prior could give it (smart scheduler spec §5.4, family sizing spec
// §5.3): b has no prior, and a part not explicit has no learned value and
// none from the family.
func NeedsPrior(asked Claim, b, family *Belief) bool {
	if asked.Exclusive || (b != nil && b.Prior != nil) {
		return false
	}
	runs, pool := b.recent(), family.recent()
	_, busy := medianBusy(runs)
	_, famBusy := medianBusy(pool)
	_, full := largestPeak(runs, 0) // whether a peak is full doesn't depend on the cores
	_, famFull := largestPeak(fullPeaks(pool), 0)
	_, io := medianWrites(runs)
	_, famIO := medianWrites(pool)
	explicitIO := asked.IO || (asked.CPUs > 0 && asked.MemoryGB > 0)
	return (asked.CPUs == 0 && !busy && !famBusy) || (asked.MemoryGB == 0 && !full && !famFull) || (!explicitIO && !io && !famIO)
}

// Surprise says how run surprises b, which has not recorded it yet (smart
// scheduler spec §5.6, job scaling spec §5.5), or "" when it doesn't: a
// peak above its memory claim when memory (the claim's memory source) was
// learned, the family's, the agent's or the default; a SIGKILL nobody
// requested; more
// than overrunRatio × its claim's cores kept busy while b doesn't overrun;
// or, with three duration runs before it, a wall time over three times or
// under a third of their median, each scaled to the run's cores. All but
// the overrun need a run before it.
func Surprise(b *Belief, run Run, memory Source) string {
	if b == nil {
		return ""
	}
	before := len(b.Runs) > 0
	if before && (memory == SourceLearned || memory == SourceFamily || memory == SourceAgent || memory == SourceDefault) && run.PeakKB > int64(run.Claim.MemoryGB)*kbPerGB {
		return fmt.Sprintf("it peaked at %.1f GB, above its %d GB claim", float64(run.PeakKB)/kbPerGB, run.Claim.MemoryGB)
	}
	if before && run.Outcome == OutcomeSignal && run.Signal == "SIGKILL" {
		return "it was killed by SIGKILL, which nobody asked for (the out-of-memory killer?)"
	}
	s := ScalingOf(b)
	if c := run.Claim.CPUs; run.teachesCores() && c > 0 && !s.Overruns {
		if busy := run.CPUSeconds / run.WallSeconds; busy > overrunRatio*float64(c) {
			return fmt.Sprintf("it kept %.1f cores busy on a %d-core claim: the hint variables don't reach it", busy, c)
		}
	}
	if before && run.TeachesDuration() {
		var walls []float64
		for _, r := range b.recent() {
			if r.TeachesDuration() {
				walls = append(walls, s.scale(r.WallSeconds, r.Claim.CPUs, run.Claim.CPUs))
			}
		}
		if m, ok := median(walls); ok && len(walls) >= overdueRuns && (run.WallSeconds > 3*m || run.WallSeconds < m/3) {
			return fmt.Sprintf("it took %s, where it usually takes %s", seconds(run.WallSeconds), seconds(m))
		}
	}
	return ""
}

// ScorePrior scores b's prior against run, the run b records next: its
// first full peak over the prior's memory, its first duration run's wall
// time over the prior's duration (spec §5.6). Each scores once.
func ScorePrior(b *Belief, run Run) {
	if b == nil || b.Prior == nil {
		return
	}
	p := b.Prior
	if p.Score.Memory == 0 && run.FullPeak() && p.MemoryGB > 0 {
		p.Score.Memory = float64(run.PeakKB) / float64(int64(p.MemoryGB)*kbPerGB)
	}
	if p.Score.Duration == 0 && run.TeachesDuration() && p.DurationSeconds > 0 {
		p.Score.Duration = run.WallSeconds / float64(p.DurationSeconds)
	}
}
