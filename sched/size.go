package sched

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// The constants of sizing (smart scheduler spec §5.4).
const (
	memoryMargin = 1.15    // on the largest recent peak
	coreHeadroom = 1.25    // on the median of busy cores
	ioThreshold  = 2 << 30 // bytes written that take an I/O slot
	window       = 10      // the runs that count
	kbPerGB      = 1 << 20
)

// Source is where a part of a claim came from.
type Source string

// The sources of a claim's parts.
const (
	SourceExplicit  Source = "explicit"
	SourceLearned   Source = "learned"
	SourceAgent     Source = "agent"
	SourceDefault   Source = "default"
	SourceExclusive Source = "exclusive"
	SourceFamily    Source = "family" // the family pool's runs (family sizing spec §5.3)
)

// ClaimSources are where each part of a claim came from.
type ClaimSources struct {
	CPUs   Source `json:"cpus,omitempty"`
	Memory Source `json:"memory,omitempty"`
	IO     Source `json:"io,omitempty"`
}

// Tag is the short source shown beside a claim: its memory's (learned·N,
// family·N, agent, yours, default); "" for an exclusive claim, and for a
// record from before sizing.
func (s ClaimSources) Tag(learned, familyRuns int) string {
	switch s.Memory {
	case SourceLearned:
		return fmt.Sprintf("learned·%d", learned)
	case SourceFamily:
		return fmt.Sprintf("family·%d", familyRuns)
	case SourceAgent:
		return "agent"
	case SourceExplicit:
		return "yours"
	case SourceDefault:
		return "default"
	}
	return ""
}

// Describe says where a claim's parts came from, for the CLI and the
// belief pager: one source for all three as "learned from 5 runs" (the
// family's as "from 6 runs of its family (<label>)");
// otherwise each source with its parts, in the order cores, memory, I/O:
// "cores and I/O learned from 5 runs, memory yours".
func (s ClaimSources) Describe(learned, familyRuns int, familyLabel string) string {
	type group struct {
		src   Source
		parts []string
	}
	var groups []group
	add := func(src Source, part string) {
		for i := range groups {
			if groups[i].src == src {
				groups[i].parts = append(groups[i].parts, part)
				return
			}
		}
		groups = append(groups, group{src, []string{part}})
	}
	add(s.CPUs, "cores")
	add(s.Memory, "memory")
	add(s.IO, "I/O")
	if len(groups) == 1 {
		return groups[0].src.describe(learned, familyRuns, familyLabel)
	}
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = JoinAnd(g.parts) + " " + g.src.describe(learned, familyRuns, familyLabel)
	}
	return strings.Join(out, ", ")
}

func (src Source) describe(learned, familyRuns int, familyLabel string) string {
	switch src {
	case SourceLearned:
		if learned == 1 {
			return "learned from 1 run"
		}
		return fmt.Sprintf("learned from %d runs", learned)
	case SourceFamily:
		runs := "1 run"
		if familyRuns != 1 {
			runs = fmt.Sprintf("%d runs", familyRuns)
		}
		if familyLabel == "" {
			return "from " + runs + " of its family"
		}
		return fmt.Sprintf("from %s of its family (%s)", runs, familyLabel)
	case SourceAgent:
		return "the agent's estimate"
	case SourceExplicit:
		return "yours"
	case SourceExclusive:
		return "the whole host"
	}
	return "default"
}

// JoinAnd joins words as "a", "a and b", "a, b and c".
func JoinAnd(words []string) string {
	if len(words) <= 1 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// Sized is a claim and where each of its parts came from.
type Sized struct {
	Claim   Claim
	Sources ClaimSources
	Learned int // the runs a learned part came from (the last window); 0 when no part is learned
	// FamilyRuns is the family pool's runs a family part came from; 0 when
	// no part is the family's (family sizing spec §5.3).
	FamilyRuns int
	AboveCap   bool  // memory from measured peaks was held to the memory cap
	BelowKB    int64 // the largest recent peak an explicit memory claim is below; 0 when it isn't
}

// Size is a job's claim (spec §5.4, family sizing spec §5.3), a pure
// function of what it asked for (a 0 part was not claimed), its belief
// (nil when it has none), its family pool (nil for none) and the policy in
// effect, whose caps it must have. Each part takes the first source with a
// value: the flag, the belief's last window runs, the family pool's runs,
// the belief's prior, the default claim. Memory learns from a done run's
// peak with a margin, and a floor only raises it (a run with spare cores
// counts as largestPeak says); only the family's full peaks size memory,
// and its floors neither size nor raise it. Cores learn from busy cores,
// floored at the widest width the fit shows faster (fit spec §4.2), never
// past the default claim's unless the command overruns (job scaling spec
// §5.2), then up to the cap; a family never makes an overrunner. An
// I/O slot comes from bytes written. --cpus with --mem makes the whole
// claim explicit. A part that isn't explicit is held within the policy:
// cores at most the default claim's (an overrunner's at most the cap),
// memory at most the cap.
func Size(asked Claim, b, family *Belief, p Policy) Sized {
	if asked.Exclusive {
		return Sized{Claim: Claim{Exclusive: true}, Sources: ClaimSources{SourceExclusive, SourceExclusive, SourceExclusive}}
	}
	runs, pool := b.recent(), family.recent()
	var prior *Prior
	if b != nil {
		prior = b.Prior
	}
	var s Sized
	learned, fromFamily := false, false

	sc := ScalingOf(b)
	busy, haveBusy := medianBusy(runs)
	famBusy, haveFamBusy := medianBusy(pool)
	switch {
	case asked.CPUs > 0:
		s.Claim.CPUs, s.Sources.CPUs = asked.CPUs, SourceExplicit
	case sc.Overruns:
		// The hint variables don't reach it: count the cores it takes, up
		// to the cap (job scaling spec §5.2).
		s.Claim.CPUs, s.Sources.CPUs = clampInt(int(math.Ceil(coreHeadroom*sc.Busy)), 1, p.Caps.CPUs), SourceLearned
		learned = true
	case haveBusy:
		// The busy rule, floored at the widest width the fit shows a job
		// that waits more than it computes runs faster at (fit spec §4.2).
		cores := clampInt(int(math.Ceil(coreHeadroom*busy)), 1, p.DefaultClaim.CPUs)
		s.Claim.CPUs, s.Sources.CPUs = max(cores, sc.floor(cores, p.DefaultClaim.CPUs)), SourceLearned
		learned = true
	case haveFamBusy:
		s.Claim.CPUs, s.Sources.CPUs = clampInt(int(math.Ceil(coreHeadroom*famBusy)), 1, p.DefaultClaim.CPUs), SourceFamily
		fromFamily = true
	case prior != nil:
		s.Claim.CPUs, s.Sources.CPUs = clampInt(prior.CPUs, 1, p.DefaultClaim.CPUs), SourceAgent
	default:
		s.Claim.CPUs, s.Sources.CPUs = p.DefaultClaim.CPUs, SourceDefault
	}

	peakKB, full := largestPeak(runs, s.Claim.CPUs)
	famKB, famFull := largestPeak(fullPeaks(pool), s.Claim.CPUs)
	evidence := peakKB // the peak a claim above the cap was measured from
	switch {
	case asked.MemoryGB > 0:
		s.Claim.MemoryGB, s.Sources.Memory = asked.MemoryGB, SourceExplicit
		if peakKB > int64(asked.MemoryGB)*kbPerGB {
			s.BelowKB = peakKB
		}
	case full:
		s.Claim.MemoryGB, s.Sources.Memory = withMargin(peakKB), SourceLearned
		learned = true
	case famFull:
		s.Claim.MemoryGB, s.Sources.Memory = withMargin(famKB), SourceFamily
		fromFamily = true
		evidence = max(peakKB, famKB)
		if peakKB > 0 {
			s.Claim.MemoryGB = max(s.Claim.MemoryGB, withMargin(peakKB))
		}
	default:
		s.Claim.MemoryGB, s.Sources.Memory = p.DefaultClaim.MemoryGB, SourceDefault
		if prior != nil {
			s.Claim.MemoryGB, s.Sources.Memory = prior.MemoryGB, SourceAgent
		}
		if peakKB > 0 {
			s.Claim.MemoryGB = max(s.Claim.MemoryGB, withMargin(peakKB))
		}
	}
	if s.Sources.Memory != SourceExplicit && s.Claim.MemoryGB > p.Caps.MemoryGB {
		s.AboveCap = evidence > 0 && withMargin(evidence) > p.Caps.MemoryGB
		s.Claim.MemoryGB = p.Caps.MemoryGB
	}
	s.Claim.MemoryGB = max(s.Claim.MemoryGB, 1)

	writes, haveIO := medianWrites(runs)
	famWrites, haveFamIO := medianWrites(pool)
	switch {
	case asked.IO || (asked.CPUs > 0 && asked.MemoryGB > 0):
		s.Claim.IO, s.Sources.IO = asked.IO, SourceExplicit
	case haveIO:
		s.Claim.IO, s.Sources.IO = writes >= ioThreshold, SourceLearned
		learned = true
	case haveFamIO:
		s.Claim.IO, s.Sources.IO = famWrites >= ioThreshold, SourceFamily
		fromFamily = true
	case prior != nil:
		s.Claim.IO, s.Sources.IO = prior.IO, SourceAgent
	default:
		s.Claim.IO, s.Sources.IO = false, SourceDefault
	}
	if learned {
		s.Learned = len(runs)
	}
	if fromFamily {
		s.FamilyRuns = len(pool)
	}
	return s
}

// fullPeaks is the runs among runs whose peak is full: a family member's
// floor neither sizes nor raises another job's claim (family sizing spec
// §5.3).
func fullPeaks(runs []Run) []Run {
	return slices.DeleteFunc(slices.Clone(runs), func(r Run) bool { return !r.FullPeak() })
}

// withMargin is the memory claim for a peak of kb: the peak plus
// memoryMargin, rounded up to a whole GB, at least 1.
func withMargin(kb int64) int {
	return max(1, int(math.Ceil(float64(kb)*memoryMargin/kbPerGB)))
}

// largestPeak is the memory evidence for a claim of cores (smart scheduler
// spec §5.4, job scaling spec §5.2): the largest peak among runs, floors
// included, and whether any of them is a full peak. A run that had spare
// cores counts as a floor of its peak scaled by cores over the cores it
// had, while runs include a full peak of a run without spare cores; with
// none, its peak counts as it is, since fewer cores need no more memory.
func largestPeak(runs []Run, cores int) (kb int64, full bool) {
	var plain, spared, scaled int64
	var plainFull, sparedFull bool
	for _, r := range runs {
		if r.Spare == 0 || r.Claim.CPUs < 1 {
			plain = max(plain, r.PeakKB)
			plainFull = plainFull || r.FullPeak()
			continue
		}
		spared = max(spared, r.PeakKB)
		sparedFull = sparedFull || r.FullPeak()
		scaled = max(scaled, int64(float64(r.PeakKB)*min(1, float64(cores)/float64(r.Claim.CPUs))))
	}
	if plainFull {
		return max(plain, scaled), true
	}
	return max(plain, spared), sparedFull
}

// medianBusy is the median of the busy cores (CPU time over wall time) of
// the runs that teach cores.
func medianBusy(runs []Run) (float64, bool) {
	var v []float64
	for _, r := range runs {
		if r.teachesCores() {
			v = append(v, r.CPUSeconds/r.WallSeconds)
		}
	}
	return median(v)
}

// medianWrites is the median of the bytes written by the runs that teach
// I/O.
func medianWrites(runs []Run) (float64, bool) {
	var v []float64
	for _, r := range runs {
		if r.teachesIO() {
			v = append(v, float64(*r.WriteBytes))
		}
	}
	return median(v)
}

// median is the middle of v (the mean of the two middle values for an even
// count), and false for an empty v. v is left as it is.
func median(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2], true
	}
	return (s[n/2-1] + s[n/2]) / 2, true
}

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }
