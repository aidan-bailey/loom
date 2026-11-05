package sched

import "fmt"

// NeedsPrior reports whether a job that asked for asked, of belief b (nil
// for a signature with none yet), would take some part it didn't claim from
// the default where an advisor's prior could give it (smart scheduler spec
// §5.4): b has no prior, and a part not explicit has no learned value.
func NeedsPrior(asked Claim, b *Belief) bool {
	if asked.Exclusive || (b != nil && b.Prior != nil) {
		return false
	}
	runs := b.recent()
	_, busy := medianBusy(runs)
	_, full := largestPeak(runs)
	_, io := medianWrites(runs)
	explicitIO := asked.IO || (asked.CPUs > 0 && asked.MemoryGB > 0)
	return (asked.CPUs == 0 && !busy) || (asked.MemoryGB == 0 && !full) || (!explicitIO && !io)
}

// Surprise says how run surprises b, which has not recorded it yet (spec
// §5.6), or "" when it doesn't or b had no run before it: a peak above its
// memory claim when memory (the claim's memory source) was learned, the
// agent's or the default; a SIGKILL nobody requested; or, with three
// duration runs before it, a wall time over three times or under a third of
// their median.
func Surprise(b *Belief, run Run, memory Source) string {
	if b == nil || len(b.Runs) == 0 {
		return ""
	}
	if (memory == SourceLearned || memory == SourceAgent || memory == SourceDefault) && run.PeakKB > int64(run.Claim.MemoryGB)*kbPerGB {
		return fmt.Sprintf("it peaked at %.1f GB, above its %d GB claim", float64(run.PeakKB)/kbPerGB, run.Claim.MemoryGB)
	}
	if run.Outcome == OutcomeSignal && run.Signal == "SIGKILL" {
		return "it was killed by SIGKILL, which nobody asked for (the out-of-memory killer?)"
	}
	if run.TeachesDuration() {
		var walls []float64
		for _, r := range b.recent() {
			if r.TeachesDuration() {
				walls = append(walls, r.WallSeconds)
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
