package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNeedsPrior(t *testing.T) {
	learned := belief(done(1<<20, 10, 10))
	learned.Runs[0].WriteBytes = int64p(0)
	assert.True(t, NeedsPrior(Claim{}, nil), "an unseen command")
	assert.False(t, NeedsPrior(Claim{Exclusive: true}, nil))
	assert.False(t, NeedsPrior(Claim{CPUs: 2, MemoryGB: 3}, nil), "a whole explicit claim")
	assert.False(t, NeedsPrior(Claim{}, learned), "every part learned")
	assert.True(t, NeedsPrior(Claim{}, belief(done(1<<20, 10, 10))), "no I/O measured (not Linux)")
	assert.False(t, NeedsPrior(Claim{IO: true}, belief(done(1<<20, 10, 10))), "the I/O part is explicit")
	assert.False(t, NeedsPrior(Claim{}, &Belief{Prior: &Prior{CPUs: 1, MemoryGB: 1}}), "a prior already")
	assert.True(t, NeedsPrior(Claim{}, belief(Run{Outcome: OutcomeFailed, PeakKB: 1 << 20, WallSeconds: 5})), "only floors")
}

func TestSurprise(t *testing.T) {
	prev := belief(done(1<<20, 1, 60), done(1<<20, 1, 60), done(1<<20, 1, 60))
	big := done(3<<20, 1, 60)
	big.Claim = Claim{CPUs: 4, MemoryGB: 2}
	killed := Run{Outcome: OutcomeSignal, Signal: "SIGKILL", PeakKB: 1 << 20, Claim: Claim{CPUs: 4, MemoryGB: 6}}
	slow := done(1<<20, 1, 600)
	slow.Claim = Claim{CPUs: 4, MemoryGB: 6}
	assert.Equal(t, "", Surprise(&Belief{}, big, SourceLearned), "no run before it")
	assert.Equal(t, "it peaked at 3.0 GB, above its 2 GB claim", Surprise(prev, big, SourceLearned))
	assert.Equal(t, "", Surprise(prev, big, SourceExplicit), "the claim was the submitter's")
	assert.Equal(t, "it was killed by SIGKILL, which nobody asked for (the out-of-memory killer?)", Surprise(prev, killed, SourceDefault))
	assert.Equal(t, "it took 10m0s, where it usually takes 1m0s", Surprise(prev, slow, SourceLearned))
	assert.Equal(t, "", Surprise(belief(done(1<<20, 1, 60)), slow, SourceLearned), "fewer than three durations")
}

func TestScorePrior(t *testing.T) {
	b := &Belief{Prior: &Prior{MemoryGB: 4, DurationSeconds: 100}}
	ScorePrior(b, Run{Outcome: OutcomeFailed, PeakKB: 8 << 20, WallSeconds: 50})
	assert.Zero(t, b.Prior.Score.Memory, "a failed run's peak is a floor")
	assert.Equal(t, 0.5, b.Prior.Score.Duration)
	ScorePrior(b, done(5<<20, 1, 300))
	assert.Equal(t, 1.25, b.Prior.Score.Memory)
	assert.Equal(t, 0.5, b.Prior.Score.Duration, "only the first duration run scores")
}
