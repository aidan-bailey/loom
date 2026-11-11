package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestForecast(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 8, MemoryGB: 16, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}, StarveAfter: time.Hour}
	four := Claim{CPUs: 4, MemoryGB: 4}
	jobs := []Record{
		{ID: 1, State: Running, Claim: four, Started: now.Add(-time.Minute)},                 // usual 3m: ends at +2m
		{ID: 2, State: Running, Claim: four, Started: now},                                   // usual 10m: ends at +10m
		{ID: 3, Order: 3, State: Queued, Claim: four, Submitted: now},                        // usual 5m: starts at +2m, ends at +7m
		{ID: 4, Order: 4, State: Queued, Claim: Claim{CPUs: 8, MemoryGB: 4}, Submitted: now}, // needs both: starts at +10m
		{ID: 5, Order: 5, State: Queued, Claim: four, Sizing: SizingAsking, Submitted: now},
		{ID: 6, Order: 6, State: Queued, Claim: Claim{CPUs: 9, MemoryGB: 1}, Submitted: now}, // over the caps: never
	}
	usual := map[JobID]time.Duration{1: 3 * time.Minute, 2: 10 * time.Minute, 3: 5 * time.Minute}
	got := Forecast(jobs, p, now, nil, nil, func(r Record) (time.Duration, bool) { d, ok := usual[r.ID]; return d, ok }, time.Hour)
	assert.Equal(t, map[JobID]time.Time{3: now.Add(2 * time.Minute), 4: now.Add(10 * time.Minute)}, got)

	t.Run("a job past its usual end ends a minute from now", func(t *testing.T) {
		jobs := []Record{
			{ID: 1, State: Running, Claim: Claim{CPUs: 8, MemoryGB: 4}, Started: now.Add(-time.Hour)},
			{ID: 2, Order: 2, State: Queued, Claim: four, Submitted: now},
		}
		got := Forecast(jobs, p, now, nil, nil, func(Record) (time.Duration, bool) { return time.Minute, true }, time.Minute)
		assert.Equal(t, now.Add(time.Minute), got[2])
	})
	t.Run("nothing past the horizon", func(t *testing.T) {
		jobs := []Record{
			{ID: 1, State: Running, Claim: Claim{CPUs: 8, MemoryGB: 4}, Started: now},
			{ID: 2, Order: 2, State: Queued, Claim: four, Submitted: now},
		}
		got := Forecast(jobs, p, now, nil, nil, func(Record) (time.Duration, bool) { return 48 * time.Hour, true }, time.Minute)
		assert.Empty(t, got)
	})
}

func TestForecast_ReleasesHeldJobs(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 8, MemoryGB: 16, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}, StarveAfter: time.Hour}
	four := Claim{CPUs: 4, MemoryGB: 4}
	jobs := []Record{
		{ID: 1, Order: 1, State: Running, Claim: four, Started: now.Add(-time.Minute)}, // usual 3m: ends at +2m
		{ID: 3, Order: 3, State: Running, Claim: four, Started: now},                   // usual 10m: ends at +10m
		{ID: 7, Order: 7, State: Queued, Claim: four, Submitted: now},                  // behind 9 and 10, which keep job 1's place: starts at +10m
		{ID: 9, Order: 1, State: Held, After: 1, Claim: four, Submitted: now},          // released at +2m and starts; usual 5m: ends at +7m
		{ID: 10, Order: 1, State: Held, After: 9, Claim: four, Submitted: now},         // released at +7m and starts
	}
	usual := map[JobID]time.Duration{1: 3 * time.Minute, 3: 10 * time.Minute, 7: 5 * time.Minute, 9: 5 * time.Minute, 10: 5 * time.Minute}
	got := Forecast(jobs, p, now, nil, nil, func(r Record) (time.Duration, bool) { d, ok := usual[r.ID]; return d, ok }, time.Hour)
	assert.Equal(t, map[JobID]time.Time{
		9:  now.Add(2 * time.Minute),
		10: now.Add(7 * time.Minute),
		7:  now.Add(10 * time.Minute),
	}, got)

	t.Run("a job held after one being sized gets no start", func(t *testing.T) {
		jobs := []Record{
			{ID: 1, Order: 1, State: Queued, Claim: four, Sizing: SizingAsking, Submitted: now},
			{ID: 2, Order: 1, State: Held, After: 1, Claim: four, Submitted: now},
		}
		assert.Empty(t, Forecast(jobs, p, now, nil, nil, func(Record) (time.Duration, bool) { return time.Minute, true }, time.Minute))
	})
}

func TestForecast_CountsRunningJobsAtTheirHolds(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 4, MemoryGB: 8, IOJobs: 2}, StarveAfter: time.Hour}
	jobs := []Record{
		{ID: 1, Order: 1, State: Running, Started: now, Claim: Claim{CPUs: 4, MemoryGB: 8}},
		{ID: 2, Order: 2, State: Queued, Submitted: now, Claim: Claim{CPUs: 2, MemoryGB: 4}},
	}
	usual := func(Record) (time.Duration, bool) { return 10 * time.Minute, true }
	assert.Equal(t, now.Add(10*time.Minute), Forecast(jobs, p, now, nil, nil, usual, time.Minute)[2])
	assert.Equal(t, now, Forecast(jobs, p, now, map[JobID]Need{1: {CPUs: 2, MemoryGB: 2}}, nil, usual, time.Minute)[2])
}

// TestForecastGreedy: a job the forecast's peaks leave no room for starts
// when the running job is expected to end, whatever the ladder holds.
func TestForecastGreedy(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	run := Record{ID: 1, Order: 1, State: Running, Claim: Claim{CPUs: 4, MemoryGB: 9}, Started: now.Add(-time.Minute)}
	wait := Record{ID: 2, Order: 2, State: Queued, Claim: Claim{CPUs: 4, MemoryGB: 9}, Submitted: now, Ready: now}
	p := Policy{Caps: Caps{CPUs: 14, MemoryGB: 14, IOJobs: 2}, StarveAfter: 10 * time.Minute, ReserveGB: 4, CPUPressurePct: 25, CPUOvercommit: 2}
	g := Greedy{
		BudgetKB: 12 * kbPerGB,
		Running:  map[JobID]Prediction{1: Flat(9*kbPerGB, now, now.Add(4*time.Minute))},
		Predict: func(r Record, start time.Time) Prediction {
			return Flat(int64(r.Claim.MemoryGB)*kbPerGB, start, start.Add(5*time.Minute))
		},
	}
	usual := func(Record) (time.Duration, bool) { return 5 * time.Minute, true }
	starts := ForecastGreedy([]Record{run, wait}, p, now, g, usual, time.Minute)
	assert.Equal(t, run.Started.Add(5*time.Minute), starts[2])
	g.Holdoff = "memory is short (job 1 paused)"
	assert.Equal(t, run.Started.Add(5*time.Minute), ForecastGreedy([]Record{run, wait}, p, now, g, usual, time.Minute)[2], "the hold-off passes")
}
