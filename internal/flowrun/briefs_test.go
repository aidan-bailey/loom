package flowrun

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/name, or rewrites it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test ./internal/flowrun -update to write it")
	assert.Equal(t, string(want), got)
}

func briefState() (*State, *Story) {
	st := newState(story("2", "intermediate-dev", "vouched"))
	st.Blocks.Developer = "Prefix for every build and test command: none\nServices: none\nLandmine notes (deep stories only):\n    Never drop a stash.\n    Never run loom."
	st.Blocks.Reviewer = "Prefix: none\nLandmine notes:\n    Never drop a stash."
	s := &st.Stories[0]
	s.Interfaces = "exposes `Run`"
	s.Row.Corrections = []string{"plan:212 | MOVED | COSMETIC | a.go:4 | it is in a.go now"}
	return st, s
}

func TestBriefs(t *testing.T) {
	st, s := briefState()
	findings := []Finding{{Severity: "Critical", File: "a.go", Line: 3, Fix: "guard it"}, {Severity: "Minor", Design: true, File: "b.go", Line: 9, Fix: "rename"}}
	failing := GateResult{Ended: "failed", Failing: []GateStep{{Line: "go test ./...", Verdict: "FAIL", Log: "/state/gates/r-s2-gates1/2.log"}}}
	golden(t, "developer.golden", DeveloperBrief(st, s))
	golden(t, "escalated.golden", withEarlier(DeveloperBrief(st, s), []string{"/state/agents/r-s2-dev1/report.md"}, "Use the cache."))
	golden(t, "fix-gates.golden", FixGatesBrief(st, s, "h1", "h4", failing, "Skip the flaky test."))
	golden(t, "fix-findings.golden", FixFindingsBrief(st, s, "h1", "h4", findings, []string{"the loop never ends"}, ""))
	golden(t, "review.golden", ReviewBrief(st, s, "h1", "h4", "/state/runs/r/conformance-2-h4.txt", "/state/agents/r-s2-dev1/report.md"))
	golden(t, "re-review.golden", ReReviewBrief(st, s, "h4", "h6", findings, nil))
	golden(t, "anti-seam.golden", AntiSeamBrief(st, "h9"))
	golden(t, "premise.golden", PremiseBrief(st, PremiseGroup{Stories: []string{"2"}, Text: "Story 2 (plan:200): 3 premises, 1 flagged\n  plan:212  missing  a.go\n"}))
	final := Story{ID: finalStory}
	assert.Contains(t, FixFindingsBrief(st, &final, "h0", "h9", findings, nil, ""), "Fix these review findings in the whole implementation.")
}

func TestDeveloperBlock_ALightStoryLosesItsLandmineNotes(t *testing.T) {
	st, s := briefState()
	assert.Contains(t, developerBlock(st, s), "Never drop a stash.", "a deep story keeps them")
	s.Tier = "light"
	got := developerBlock(st, s)
	assert.Equal(t, "Prefix for every build and test command: none\nServices: none\nLandmine notes (deep stories only): none (a light story)", got)
}
