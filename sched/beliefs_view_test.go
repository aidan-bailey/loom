package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackRecord(t *testing.T) {
	bs := []Belief{
		{Prior: &Prior{Score: PriorScore{Memory: 1.1, Duration: 0.5}}},
		{Prior: &Prior{Score: PriorScore{Memory: 2.0, Duration: 1.5}}},
		{Prior: &Prior{}},
		{},
	}
	assert.Equal(t, Track{Priors: 3, Scored: 2, MemoryWithin: 1, MemoryUnder: 2, DurationScored: 2, DurationMedian: 1.0}, TrackRecord(bs))
}

func TestBeliefQuery_Matches(t *testing.T) {
	b := Belief{ID: "abc", Signature: Signature{Repo: "/src/polyoxide/.git"}}
	assert.True(t, BeliefQuery{}.Matches(b))
	assert.True(t, BeliefQuery{Repo: "polyoxide"}.Matches(b))
	assert.False(t, BeliefQuery{Repo: "loom"}.Matches(b))
	assert.True(t, BeliefQuery{ID: "abc"}.Matches(b))
	assert.False(t, BeliefQuery{ID: "abd"}.Matches(b))
}

func TestBelief_CloneSharesNothing(t *testing.T) {
	b := Belief{Signature: Signature{Command: []string{"x"}}, Runs: []Run{{WriteBytes: int64p(1)}}, Notes: []Note{{Text: "n"}}, Prior: &Prior{CPUs: 1}}
	c := b.Clone()
	c.Command[0], c.Runs[0].Job, c.Notes[0].Text, c.Prior.CPUs = "y", 9, "m", 2
	*c.Runs[0].WriteBytes = 7
	assert.Equal(t, "x", b.Command[0])
	assert.Zero(t, b.Runs[0].Job)
	assert.Equal(t, "n", b.Notes[0].Text)
	assert.Equal(t, 1, b.Prior.CPUs)
	assert.Equal(t, int64(1), *b.Runs[0].WriteBytes)
}

func TestRoughDuration(t *testing.T) {
	assert.Equal(t, "40s", RoughDuration(40*time.Second))
	assert.Equal(t, "2m", RoughDuration(93*time.Second))
	assert.Equal(t, "1h05m", RoughDuration(65*time.Minute))
}

func TestFormatBelief(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 9, 9, 0, 0, time.UTC)
	b := Belief{ID: "3f9a1c2b7d10", Signature: Signature{Repo: "/src/polyoxide/.git", Folder: ".", Command: []string{"bash", "scripts/web-smoke.sh"}, Env: []string{"CARGO_INCREMENTAL=0"}}}
	run := done(14_422_836, 512, 93)
	run.Job, run.Ended, run.Claim, run.WriteBytes = 6, t0, Claim{CPUs: 8, MemoryGB: 12, IO: true}, int64p(3<<30)
	b.AddRun(run)
	b.Prior = &Prior{CPUs: 4, MemoryGB: 12, IO: true, DurationSeconds: 300, Model: "opus", Effort: "low", At: t0.Add(-time.Hour), Note: "builds wasm, runs chromium", Score: PriorScore{Memory: 1.15}}
	b.AddNote(Note{At: t0, Job: 6, Text: "the wasm build and chromium overlap"})
	lines := FormatBelief(ViewBelief(b, szPolicy))
	text := strings.Join(lines, "\n")
	require.NotEmpty(t, lines)
	for _, want := range []string{
		"belief 3f9a1c2b7d10 · polyoxide · .",
		"bash scripts/web-smoke.sh  (env CARGO_INCREMENTAL=0)",
		"4c/16G io (learned from 1 run)",
		"opus, low",
		"memory ×1.15",
		"the wasm build and chromium overlap",
		"13.8G",
	} {
		assert.Contains(t, text, want)
	}
}
