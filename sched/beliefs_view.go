package sched

import (
	"math"
	"slices"
	"time"
)

// BeliefQuery selects beliefs: one by ID, one repo's by its display name,
// or, empty, all of them.
type BeliefQuery struct {
	ID   SignatureID `json:"id,omitempty"`
	Repo string      `json:"repo,omitempty"`
}

// Matches reports whether q selects b.
func (q BeliefQuery) Matches(b Belief) bool {
	return (q.ID == "" || b.ID == q.ID) && (q.Repo == "" || RepoName(b.Repo) == q.Repo)
}

// BeliefView is a belief with what follows from it now: its repo's name,
// the claim a job of it that claims nothing would get, how its command
// scales, its durations at that claim's cores, and the command it runs
// after.
type BeliefView struct {
	Belief         Belief
	RepoName       string
	Sized          Sized
	Scaling        Scaling
	Usual          time.Duration
	P90            time.Duration
	DurationRuns   int
	UsualFromPrior bool
	// UsualFromFamily says Usual and P90 are its family pool's (family
	// sizing spec §6).
	UsualFromFamily bool
	// Family is the label of its family and FamilySize how many beliefs
	// share it, its own included (family sizing spec §6). The caller sets
	// them, since it holds the registry; "" and 0 for a belief with none.
	Family     string
	FamilySize int
	// After is the command of the belief this one's signature runs after
	// (dependent jobs spec §5.6). The caller sets it, since it holds the
	// registry; nil when the signature has no prerequisite part, the
	// prerequisite had no signature, or that belief is gone.
	After []string
	// Explore is the width a quiet host would explore its command at
	// (ExploreTarget), and ExploreFlat says its two widest widths show no
	// gain to look for below the cap (exploration spec §6); both zero with
	// exploring off.
	Explore     int
	ExploreFlat bool
}

// ViewBelief is b's view under p, with family its family pool (nil for
// none); with no caps (admission paused) the claim is left zero.
func ViewBelief(b Belief, family *Belief, p Policy) BeliefView {
	v := BeliefView{Belief: b, RepoName: RepoName(b.Repo), Scaling: ScalingOf(&b)}
	if p.usable() {
		v.Sized = Size(Claim{}, &b, family, p)
		if p.Explore {
			v.Explore = ExploreTarget(&b, p.Caps.CPUs)
			s := v.Scaling
			v.ExploreFlat = v.Explore == 0 && !s.Overruns && len(s.Widths) >= 2 && s.Widest < p.Caps.CPUs
		}
	}
	usual, p90, n, src := Usual(&b, family, v.Sized.Claim.CPUs)
	v.Usual, v.P90 = usual, p90
	switch src {
	case DurationLearned:
		v.DurationRuns = n
	case DurationFamily:
		v.UsualFromFamily = true
	case DurationPrior:
		v.UsualFromPrior = true
	}
	return v
}

// Track is the advisor's track record over the beliefs with a prior: how
// many priors were scored on memory, within 25% of the first full peak or
// under it, and the median duration score.
type Track struct {
	Priors         int
	Scored         int
	MemoryWithin   int
	MemoryUnder    int
	DurationScored int
	DurationMedian float64
}

// TrackRecord is the track record of bs.
func TrackRecord(bs []Belief) Track {
	var t Track
	var durations []float64
	for _, b := range bs {
		if b.Prior == nil {
			continue
		}
		t.Priors++
		if m := b.Prior.Score.Memory; m > 0 {
			t.Scored++
			if math.Abs(m-1) <= 0.25 {
				t.MemoryWithin++
			}
			if m > 1 {
				t.MemoryUnder++
			}
		}
		if d := b.Prior.Score.Duration; d > 0 {
			durations = append(durations, d)
		}
	}
	t.DurationScored = len(durations)
	t.DurationMedian, _ = median(durations)
	return t
}

// BeliefsPage is ReadBeliefs' answer: the beliefs selected, most recently
// seen first, and the advisor's track record over all of them.
type BeliefsPage struct {
	Beliefs []BeliefView
	Track   Track
}

// Clone is b with its own copy of every slice and pointer, for a query to
// hand out.
func (b Belief) Clone() Belief {
	b.Command, b.Env = slices.Clone(b.Command), slices.Clone(b.Env)
	b.Runs = slices.Clone(b.Runs)
	for i := range b.Runs {
		b.Runs[i].ReadBytes, b.Runs[i].WriteBytes = clonePtr(b.Runs[i].ReadBytes), clonePtr(b.Runs[i].WriteBytes)
		if p := b.Runs[i].Profile; p != nil {
			c := *p
			c.Mem.Steps, c.Cores.Steps = slices.Clone(p.Mem.Steps), slices.Clone(p.Cores.Steps)
			b.Runs[i].Profile = &c
		}
	}
	b.Notes = slices.Clone(b.Notes)
	b.Prior = clonePtr(b.Prior)
	return b
}
