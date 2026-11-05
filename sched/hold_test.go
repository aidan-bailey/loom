package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// holdProfile is a run that peaked at 6 GB and 4 cores before progress 10,
// then used 1 GB and 1 core to its end at 100.
func holdProfile() *Profile {
	return &Profile{End: 100,
		Mem:   Staircase{PeakAt: 10, Steps: []Step{{Until: 10, Level: 6 << 20}, {Until: 100, Level: 1 << 20}}},
		Cores: Staircase{PeakAt: 5, Steps: []Step{{Until: 10, Level: 4000}, {Until: 100, Level: 1000}}}}
}

// holdFixture is a job of claim 4c/8G io, its belief with three profiled
// runs, a trace that peaked and has tracked the profile since, and a policy
// that releases: every rule passes, and the hold is 2c/2G.
type holdFixture struct {
	rec   Record
	b     *Belief
	t     *Trace
	fresh bool
	prev  HoldState
	p     Policy
}

func newHoldFixture() *holdFixture {
	b := &Belief{}
	for range 3 {
		b.AddRun(Run{Outcome: OutcomeDone, Profile: holdProfile()})
	}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	return &holdFixture{
		rec: Record{ID: 1, State: Running, Claim: Claim{CPUs: 4, MemoryGB: 8, IO: true},
			Sources: ClaimSources{CPUs: SourceLearned, Memory: SourceLearned}},
		b: b,
		t: tracePts(t0,
			TracePoint{X: 2, RSSKB: 5 << 20, Cores: 0},
			TracePoint{X: 8, RSSKB: 6 << 20, Cores: 3.6},
			TracePoint{X: 11, RSSKB: 900 << 10, Cores: 0.9},
			TracePoint{X: 13, RSSKB: 800 << 10, Cores: 0.8}),
		fresh: true,
		p:     Policy{Caps: Caps{CPUs: 14, MemoryGB: 24, IOJobs: 2}, ReleasePastPeaks: true},
	}
}

func (f *holdFixture) hold() (HoldState, Need) { return Hold(f.rec, f.b, f.t, f.fresh, f.prev, f.p) }

var (
	claimNeed    = Need{CPUs: 4, MemoryGB: 8, IOSlots: 1}
	releasedNeed = Need{CPUs: 2, MemoryGB: 2, IOSlots: 1}
)

func TestHold_ReleasesAJobPastItsPeaks(t *testing.T) {
	state, need := newHoldFixture().hold()
	assert.Equal(t, HoldState{CPUs: 2, MemoryGB: 2}, state, "1 GB + 15% is 2 GB, 1 core + 25% is 2")
	assert.Equal(t, releasedNeed, need, "the I/O slot is held to the end")
}

// Each rule, just failing: the job holds its claim.
func TestHold_EachRuleHoldsTheClaim(t *testing.T) {
	for name, spoil := range map[string]func(f *holdFixture){
		"1: two profiled runs":       func(f *holdFixture) { f.b.Runs = f.b.Runs[:2] },
		"1: an invalid profile":      func(f *holdFixture) { f.b.Runs[0].Profile = &Profile{End: 1} },
		"1: no belief":               func(f *holdFixture) { f.b = nil },
		"1: a recent broken hold":    func(f *holdFixture) { f.b.Runs[2].HoldBroke = true },
		"2: adopted":                 func(f *holdFixture) { f.rec.Adopted = true },
		"2: no trace":                func(f *holdFixture) { f.t = nil },
		"2: an incomplete trace":     func(f *holdFixture) { f.t.Incomplete = true },
		"2: a stale sample":          func(f *holdFixture) { f.fresh = false },
		"2: switched off":            func(f *holdFixture) { f.p.ReleasePastPeaks = false },
		"2: exclusive":               func(f *holdFixture) { f.rec.Claim.Exclusive = true },
		"2: an explicit claim":       func(f *holdFixture) { f.rec.Sources = ClaimSources{CPUs: SourceExplicit, Memory: SourceExplicit} },
		"2: this job broke its hold": func(f *holdFixture) { f.rec.HoldBroke = true },
		"4: outlasting every run":    func(f *holdFixture) { f.t.Points[3].X = 100 },
	} {
		f := newHoldFixture()
		spoil(f)
		_, need := f.hold()
		want := claimNeed
		if f.rec.Claim.Exclusive {
			want = f.rec.Claim.Need(f.p.Caps)
		}
		assert.Equal(t, want, need, name)
	}
}

// Rules 2's explicit claim and 1's history count only in full.
func TestHold_TheOtherSideOfEachRule(t *testing.T) {
	for name, adjust := range map[string]func(f *holdFixture){
		"explicit cores alone":  func(f *holdFixture) { f.rec.Sources.CPUs = SourceExplicit },
		"explicit memory alone": func(f *holdFixture) { f.rec.Sources.Memory = SourceExplicit },
		"a broken hold 11 runs back": func(f *holdFixture) {
			f.b.Runs[2].HoldBroke = true
			for range window - 2 {
				f.b.AddRun(Run{Outcome: OutcomeDone, Profile: holdProfile()})
			}
		},
		"an older run with no profile": func(f *holdFixture) { f.b.Runs = append(f.b.Runs, Run{Outcome: OutcomeDone}) },
		"a shorter run drops out": func(f *holdFixture) {
			short := holdProfile()
			short.End, short.Mem.Steps[1].Until, short.Cores.Steps[1].Until = 12, 12, 12
			f.b.AddRun(Run{Outcome: OutcomeDone, Profile: short})
		},
	} {
		f := newHoldFixture()
		adjust(f)
		_, need := f.hold()
		assert.Equal(t, releasedNeed, need, name)
	}
}

// Rules 3 and 5 decide each resource on its own.
func TestHold_PerResourceRules(t *testing.T) {
	cases := map[string]struct {
		spoil func(f *holdFixture)
		want  Need
	}{
		"3: memory never reached 90% of the lowest peak": {func(f *holdFixture) { f.t.Points[1].RSSKB = 5 << 20 }, Need{CPUs: 2, MemoryGB: 8, IOSlots: 1}},
		"3: memory at 90% is enough":                     {func(f *holdFixture) { f.t.Points[1].RSSKB = 6<<20*9/10 + 1 }, releasedNeed},
		"3: not past a run's memory peak": {func(f *holdFixture) {
			late := holdProfile()
			late.Mem.PeakAt, late.Mem.Steps[0].Until = 13, 14
			f.b.AddRun(Run{Outcome: OutcomeDone, Profile: late})
		}, Need{CPUs: 2, MemoryGB: 8, IOSlots: 1}},
		"5: memory above the envelope one point back": {func(f *holdFixture) { f.t.Points[2].RSSKB = 1<<20 + 1 }, Need{CPUs: 2, MemoryGB: 8, IOSlots: 1}},
		"5: memory at the envelope tracks":            {func(f *holdFixture) { f.t.Points[2].RSSKB = 1 << 20 }, releasedNeed},
		"5: cores above the envelope now":             {func(f *holdFixture) { f.t.Points[3].Cores = 1.01 }, Need{CPUs: 4, MemoryGB: 2, IOSlots: 1}},
		"a hold that allows no less than the claim":   {func(f *holdFixture) { f.rec.Claim = Claim{CPUs: 2, MemoryGB: 2} }, Need{CPUs: 2, MemoryGB: 2}},
	}
	for name, c := range cases {
		f := newHoldFixture()
		c.spoil(f)
		state, need := f.hold()
		assert.Equal(t, c.want, need, name)
		assert.Equal(t, need.CPUs < f.rec.Claim.CPUs, state.CPUs > 0, name)
	}
}

// A released hold only falls; after release rules 3 and 5 stop counting,
// and a failing rule 1, 2 or 4 counts the claim but keeps the state.
func TestHold_AfterRelease(t *testing.T) {
	f := newHoldFixture()
	f.prev = HoldState{CPUs: 3, MemoryGB: 1}
	f.t.Points[3].RSSKB = 2 << 20 // above the envelope: rule 5 would fail
	state, need := f.hold()
	assert.Equal(t, HoldState{CPUs: 2, MemoryGB: 1}, state, "the lower of the stored hold and the candidate")
	assert.Equal(t, Need{CPUs: 2, MemoryGB: 1, IOSlots: 1}, need)

	f.fresh = false
	state, need = f.hold()
	assert.Equal(t, f.prev, state, "a failed sample keeps the state")
	assert.Equal(t, claimNeed, need, "and counts the claim")

	f = newHoldFixture()
	f.prev = HoldState{CPUs: 2, MemoryGB: 2}
	f.t.Add(f.t.Last.Add(time.Second), TracePoint{X: 100, RSSKB: 800 << 10, Cores: 0.8})
	state, need = f.hold()
	assert.Equal(t, f.prev, state, "a released job outlasting every run keeps its state")
	assert.Equal(t, claimNeed, need, "and counts its claim: no history covers what is left")
}

func TestHoldState_Exceeded(t *testing.T) {
	h := HoldState{CPUs: 2, MemoryGB: 2}
	assert.Equal(t, "", h.Exceeded(2<<20, 2))
	assert.Equal(t, "memory", h.Exceeded(2<<20+1, 0))
	assert.Equal(t, "cores", h.Exceeded(0, 2.01))
	assert.Equal(t, "", HoldState{}.Exceeded(1<<30, 64), "nothing released, nothing exceeded")
	assert.Equal(t, "", HoldState{MemoryGB: 2}.Exceeded(0, 64), "cores not released")
}
