package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// szPolicy is fresco's: 14 cores, 22 GB, 2 I/O jobs; 4 cores and 6 GB by default.
var szPolicy = Policy{Caps: Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}}

func done(peakKB int64, cpu, wall float64) Run {
	return Run{Outcome: OutcomeDone, PeakKB: peakKB, CPUSeconds: cpu, WallSeconds: wall}
}

func belief(runs ...Run) *Belief { return &Belief{Runs: runs} }

func all(s Source) ClaimSources { return ClaimSources{CPUs: s, Memory: s, IO: s} }

func TestSize(t *testing.T) {
	const peak138 = 14_422_836 // 13.8 GB
	failed := Run{Outcome: OutcomeFailed, PeakKB: peak138, WallSeconds: 30}
	io3 := done(1<<20, 10, 10)
	io3.WriteBytes = int64p(3 << 30)
	io1 := done(1<<20, 10, 10)
	io1.WriteBytes = int64p(1 << 30)
	cases := []struct {
		name    string
		asked   Claim
		b       *Belief
		policy  Policy
		want    Claim
		sources ClaimSources
		learned int
		above   bool
		below   int64
	}{
		{"nothing known", Claim{}, nil, szPolicy, Claim{CPUs: 4, MemoryGB: 6}, all(SourceDefault), 0, false, 0},
		{"exclusive", Claim{Exclusive: true}, belief(done(peak138, 1, 1)), szPolicy, Claim{Exclusive: true}, all(SourceExclusive), 0, false, 0},
		{"fully explicit", Claim{CPUs: 2, MemoryGB: 3}, belief(io3), szPolicy, Claim{CPUs: 2, MemoryGB: 3}, all(SourceExplicit), 0, false, 0},
		{"a done peak, with its margin", Claim{}, belief(done(peak138, 0, 93)), szPolicy, Claim{CPUs: 4, MemoryGB: 16},
			ClaimSources{SourceDefault, SourceLearned, SourceDefault}, 1, false, 0},
		{"a small peak rounds up", Claim{}, belief(done(1_153_434, 0, 5)), szPolicy, Claim{CPUs: 4, MemoryGB: 2},
			ClaimSources{SourceDefault, SourceLearned, SourceDefault}, 1, false, 0},
		{"a failed run's peak only raises", Claim{}, belief(failed), szPolicy, Claim{CPUs: 4, MemoryGB: 16}, all(SourceDefault), 0, false, 0},
		{"a learned peak above the cap", Claim{}, belief(done(peak138, 0, 93)), Policy{Caps: Caps{CPUs: 14, MemoryGB: 14, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}},
			Claim{CPUs: 4, MemoryGB: 14}, ClaimSources{SourceDefault, SourceLearned, SourceDefault}, 1, true, 0},
		{"busy cores shrink the claim", Claim{}, belief(done(1<<20, 12, 10)), szPolicy, Claim{CPUs: 2, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 1, false, 0},
		{"busy cores never pass the default", Claim{}, belief(done(1<<20, 39, 10)), szPolicy, Claim{CPUs: 4, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 1, false, 0},
		{"the median of busy cores", Claim{}, belief(done(1<<20, 10, 10), done(1<<20, 12, 10), done(1<<20, 40, 10)), szPolicy, Claim{CPUs: 2, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 3, false, 0},
		{"heavy writes take a slot", Claim{}, belief(io3), szPolicy, Claim{CPUs: 2, MemoryGB: 2, IO: true}, all(SourceLearned), 1, false, 0},
		{"light writes take none", Claim{}, belief(io1), szPolicy, Claim{CPUs: 2, MemoryGB: 2}, all(SourceLearned), 1, false, 0},
		{"a prior", Claim{}, &Belief{Prior: &Prior{CPUs: 6, MemoryGB: 12, IO: true}}, szPolicy, Claim{CPUs: 4, MemoryGB: 12, IO: true}, all(SourceAgent), 0, false, 0},
		{"a prior raised by a floor", Claim{}, &Belief{Prior: &Prior{CPUs: 2, MemoryGB: 6}, Runs: []Run{failed}}, szPolicy, Claim{CPUs: 2, MemoryGB: 16}, all(SourceAgent), 0, false, 0},
		{"an explicit memory claim below a peak", Claim{MemoryGB: 12}, belief(done(peak138, 0, 93)), szPolicy, Claim{CPUs: 4, MemoryGB: 12},
			ClaimSources{SourceDefault, SourceExplicit, SourceDefault}, 0, false, peak138},
		{"an explicit I/O slot", Claim{IO: true}, belief(done(peak138, 0, 93)), szPolicy, Claim{CPUs: 4, MemoryGB: 16, IO: true},
			ClaimSources{SourceDefault, SourceLearned, SourceExplicit}, 1, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Size(c.asked, c.b, c.policy)
			assert.Equal(t, c.want, s.Claim)
			assert.Equal(t, c.sources, s.Sources)
			assert.Equal(t, c.learned, s.Learned)
			assert.Equal(t, c.above, s.AboveCap)
			assert.Equal(t, c.below, s.BelowKB)
		})
	}

	t.Run("only the last ten runs count", func(t *testing.T) {
		runs := []Run{}
		for range 10 {
			runs = append(runs, done(1<<20, 0, 10))
		}
		runs = append(runs, done(20<<20, 0, 10)) // the eleventh, oldest
		assert.Equal(t, 2, Size(Claim{}, belief(runs...), szPolicy).Claim.MemoryGB)
	})
}

func TestClaimSources_Describe(t *testing.T) {
	L, A, D, E := SourceLearned, SourceAgent, SourceDefault, SourceExplicit
	assert.Equal(t, "learned from 5 runs", ClaimSources{L, L, L}.Describe(5))
	assert.Equal(t, "learned from 1 run", ClaimSources{L, L, L}.Describe(1))
	assert.Equal(t, "the agent's estimate", ClaimSources{A, A, A}.Describe(0))
	assert.Equal(t, "default", ClaimSources{D, D, D}.Describe(0))
	assert.Equal(t, "cores and I/O learned from 1 run, memory yours", ClaimSources{L, E, L}.Describe(1))
	assert.Equal(t, "cores yours, memory learned from 2 runs, I/O default", ClaimSources{E, L, D}.Describe(2))
}

func TestClaimSources_Tag(t *testing.T) {
	assert.Equal(t, "learned·5", ClaimSources{Memory: SourceLearned}.Tag(5))
	assert.Equal(t, "agent", ClaimSources{Memory: SourceAgent}.Tag(0))
	assert.Equal(t, "yours", ClaimSources{Memory: SourceExplicit}.Tag(0))
	assert.Equal(t, "default", ClaimSources{Memory: SourceDefault}.Tag(0))
	assert.Equal(t, "", ClaimSources{Memory: SourceExclusive}.Tag(0))
	assert.Equal(t, "", ClaimSources{}.Tag(0), "a record from before sizing")
}

func TestUsualAndOverdueAfter(t *testing.T) {
	sec := func(s float64) Run { return Run{Outcome: OutcomeDone, WallSeconds: s} }
	b := belief(sec(60), sec(120), sec(90), Run{Outcome: OutcomeCancelled, WallSeconds: 900}, Run{Outcome: OutcomeFailed, WallSeconds: 100})
	usual, p90, n, fromPrior := Usual(b)
	assert.Equal(t, 95*time.Second, usual, "the median of 60, 90, 100 and 120")
	assert.Equal(t, 120*time.Second, p90)
	assert.Equal(t, 4, n)
	assert.False(t, fromPrior)
	assert.Equal(t, 7*time.Minute, OverdueAfter(b), "max(2 × 2m, 2m + 5m)")

	few := belief(sec(60), sec(60))
	assert.Zero(t, OverdueAfter(few), "fewer than three duration runs")

	prior := &Belief{Prior: &Prior{DurationSeconds: 300}}
	usual, p90, n, fromPrior = Usual(prior)
	assert.Equal(t, 5*time.Minute, usual)
	assert.Equal(t, 5*time.Minute, p90)
	assert.Zero(t, n)
	assert.True(t, fromPrior)

	usual, _, n, fromPrior = Usual(nil)
	assert.Zero(t, usual)
	assert.Zero(t, n)
	assert.False(t, fromPrior)
}
