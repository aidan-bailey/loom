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
	lines := FormatBelief(ViewBelief(b, nil, szPolicy))
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
		"94% parallel over 1 run · efficient up to 16 cores",
	} {
		assert.Contains(t, text, want)
	}
}

func TestFormatBelief_Family(t *testing.T) {
	v := ViewBelief(Belief{ID: "3f9a1c2b7d10", Signature: Signature{Repo: "/src/loom/.git", Folder: ".", Command: []string{"go", "test", "./sched"}}},
		belief(Run{Outcome: OutcomeDone, PeakKB: 1 << 20, WallSeconds: 120}), szPolicy)
	v.Family, v.FamilySize = "go test ./sched", 3
	assert.True(t, v.UsualFromFamily)
	text := strings.Join(FormatBelief(v), "\n")
	assert.Contains(t, text, "family    go test ./sched (3 beliefs)")
	assert.Contains(t, text, "about     2m (its family's runs)")
	assert.Contains(t, text, "memory from 1 run of its family (go test ./sched)")
}

func TestFormatBelief_Scaling(t *testing.T) {
	line := func(b *Belief) string {
		for _, l := range FormatBelief(ViewBelief(*b, nil, szPolicy)) {
			if strings.HasPrefix(l, "scaling") {
				return l
			}
		}
		return ""
	}
	wasm := at(3, 2.74)
	wasm.WallSeconds, wasm.CPUSeconds = 43, 43*2.74
	assert.Equal(t, "scaling   95% parallel over 1 run · efficient up to 6 cores · ~34s at 4, ~24s at 6", line(belief(wasm)))
	assert.Equal(t, "scaling   ignores the hint variables · keeps 7.8 cores busy, claims 10", line(belief(at(4, 7.81))))
	assert.Equal(t, "scaling   unknown until a run with 2 cores or more", line(belief(at(1, 1.0))))
	waits1, waits2 := at(1, 0.31), at(2, 0.63)
	waits1.WallSeconds, waits1.CPUSeconds = 80, 80*0.31
	waits2.WallSeconds, waits2.CPUSeconds = 40, 40*0.63
	assert.Equal(t, "scaling   100% parallel from runs at 1 and 2 cores · efficient up to 4 cores · ~40s at 2, ~20s at 4", line(belief(waits1, waits2)),
		"the fit gave p: the widths it came from; the floor makes the claim 2 cores")

	explore := szPolicy
	explore.Explore = true
	exploring := func(b *Belief) string {
		for _, l := range FormatBelief(ViewBelief(*b, nil, explore)) {
			if strings.HasPrefix(l, "scaling") {
				return l
			}
		}
		return ""
	}
	assert.Equal(t, "scaling   unknown until a run with 2 cores or more · next quiet run explores 2 cores", exploring(belief(at(1, 1.0))))
	assert.Equal(t, "scaling   0% parallel over 1 run · efficient up to 1 cores · ~2m at 2 · next quiet run explores 4 cores",
		exploring(belief(at(2, 0.97))))
	assert.Equal(t, "scaling   0% parallel from runs at 2 and 4 cores · efficient up to 1 cores · ~2m at 2 · no gain expected past 4 cores, checked again once its 4-core runs leave the window",
		exploring(belief(at(2, 1.0), at(4, 1.0))))
	assert.Equal(t, "scaling   ignores the hint variables · keeps 7.8 cores busy, claims 10", exploring(belief(at(4, 7.81))), "an overrunner explores nothing")
}

func TestFormatBelief_NamesTheCommandItRunsAfter(t *testing.T) {
	sig := NewSignature("/r/.git", ".", []string{"cargo", "test"})
	text := func(after SignatureID, command []string) []string {
		s := sig
		s.After = after
		v := ViewBelief(Belief{ID: s.ID(), Signature: s}, nil, Policy{})
		v.After = command
		return FormatBelief(v)
	}
	assert.Contains(t, text("1a2b3c4d5e6f", []string{"cargo", "test", "--no-run"}), "after     cargo test --no-run")
	assert.Contains(t, text(AfterNone, nil), "after     a job with no signature")
	assert.Contains(t, text("1a2b3c4d5e6f", nil), "after     belief 1a2b3c4d5e6f, since forgotten")
	for _, line := range text("", nil) {
		assert.False(t, strings.HasPrefix(line, "after"), "no prerequisite, no line: %q", line)
	}
	assert.Equal(t, "a, b and c", JoinAnd([]string{"a", "b", "c"}))
}
