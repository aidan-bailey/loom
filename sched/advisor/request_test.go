package advisor

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
)

func TestRecentRuns(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	wrote := int64(1536 << 20)
	runs := []sched.Run{ // newest first, as a belief keeps them
		{Ended: now.Add(-90 * time.Second), Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Spare: 2,
			WallSeconds: 12.6, PeakKB: 2_936_013, WriteBytes: &wrote, Checkout: "/src/wt"},
		{Ended: now.Add(-time.Hour), Outcome: sched.OutcomeFailed, Claim: sched.Claim{CPUs: 2, MemoryGB: 4},
			WallSeconds: 2, PeakKB: 512_000, Checkout: "/src/main"},
		{Ended: now.Add(-2 * time.Hour), Outcome: sched.OutcomeDone, Claim: sched.Claim{Exclusive: true}, WallSeconds: 700, PeakKB: 8 << 20},
		{Ended: now.Add(-3 * time.Hour), Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, WallSeconds: 8},
	}
	yes, no, mb := true, false, int64(1536)
	assert.Equal(t, []KnownRun{
		{Outcome: sched.OutcomeDone, MinutesAgo: 1, Cores: 6, WallSeconds: 13, PeakGB: 2.8, WroteMB: &mb, Here: &yes},
		{Outcome: sched.OutcomeFailed, MinutesAgo: 60, Cores: 2, WallSeconds: 2, PeakGB: 0.5, Here: &no},
		{Outcome: sched.OutcomeDone, MinutesAgo: 120, WallSeconds: 700, PeakGB: 8},
	}, RecentRuns(runs, "/src/wt", now), "the newest three; no cores for an exclusive claim, no here for a run with no checkout")

	for _, r := range RecentRuns(runs, "", now) {
		assert.Nil(t, r.Here, "no here for a job with no checkout")
	}
	assert.Empty(t, RecentRuns(nil, "/src/wt", now))
	ahead := []sched.Run{{Ended: now.Add(time.Minute), Outcome: sched.OutcomeDone}}
	assert.Equal(t, 0, RecentRuns(ahead, "", now)[0].MinutesAgo, "a run ending after now is 0 minutes ago, not negative")
}
