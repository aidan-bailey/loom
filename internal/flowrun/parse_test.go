package flowrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReview(t *testing.T) {
	r, err := ParseReview("Strengths.\n\n**FINDING: Critical a/b.go:12 guard the nil**\n`FINDING: Important design c.go:3-9 decide the contract`\n" +
		"FINDING: Minor [design] d.go:1 a note\nPLAN: the plan's loop never ends\nPLAN:\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE\n")
	require.NoError(t, err)
	assert.Equal(t, "CHANGES_REQUESTED", r.Verdict)
	assert.Equal(t, []Finding{
		{Severity: "Critical", File: "a/b.go", Line: 12, Fix: "guard the nil"},
		{Severity: "Important", Design: true, File: "c.go", Line: 3, Fix: "decide the contract"},
		{Severity: "Minor", Design: true, File: "d.go", Line: 1, Fix: "a note"},
	}, r.Findings)
	assert.Equal(t, []string{"the plan's loop never ends"}, r.Plan)
	assert.True(t, r.Blocking())
	assert.Equal(t, [3]int{1, 1, 1}, r.Counts())
	assert.Len(t, r.Minors(), 1)

	r, err = ParseReview("VERDICT: APPROVED\nSTATUS: DONE")
	require.NoError(t, err)
	assert.False(t, r.Blocking())

	_, err = ParseReview("FINDING: Critical somewhere\nVERDICT: APPROVED")
	assert.ErrorContains(t, err, "a FINDING line it can't read")
	_, err = ParseReview("FINDING: Minor a.go:1 x\nSTATUS: DONE")
	assert.ErrorContains(t, err, "no VERDICT line")
	_, err = ParseReview("VERDICT: MAYBE")
	assert.Error(t, err)

	// Bold tags, and values that start or end in a code span (feedback
	// thread 18: a PLAN line lost its closing backtick).
	r, err = ParseReview("**FINDING:** Minor a.go:1 rename `foo`\n**PLAN:** `snapshot` redraws the cell under `cursor`\n**VERDICT:** APPROVED")
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", r.Verdict)
	assert.Equal(t, []Finding{{Severity: "Minor", File: "a.go", Line: 1, Fix: "rename `foo`"}}, r.Findings)
	assert.Equal(t, []string{"`snapshot` redraws the cell under `cursor`"}, r.Plan)
}

// TagLine takes a markdown wrapper off a tagged line, and never a code
// span of its value (feedback thread 18).
func TestTagLine(t *testing.T) {
	for _, c := range []struct{ line, value string }{
		{"PLAN: the loop", "the loop"},
		{"  PLAN:   the loop  ", "the loop"},
		{"PLAN:", ""},
		{"PLAN: fails the plan's own `a_row_too_wide`", "fails the plan's own `a_row_too_wide`"},
		{"PLAN: `snapshot` builds the footer", "`snapshot` builds the footer"},
		{"PLAN: `a` and `b`", "`a` and `b`"},
		{"**PLAN:** the loop", "the loop"},
		{"`PLAN:` the loop", "the loop"},
		{"**`PLAN:`** the loop", "the loop"},
		{"**PLAN: the loop**", "the loop"},
		{"`PLAN: the loop`", "the loop"},
		{"**PLAN: uses `foo`**", "uses `foo`"},
		{"PLAN: `the whole value`", "the whole value"},
		{"PLAN: **the whole value**", "the whole value"},
		{"PLAN: **", "**"},
		{"PLAN: `", "`"},
		{"**PLAN:**", ""},
	} {
		v, ok := TagLine(c.line, "PLAN:")
		assert.True(t, ok, "%q", c.line)
		assert.Equal(t, c.value, v, "%q", c.line)
	}
	for _, line := range []string{"the PLAN: here", "PLANS: x", "- PLAN: a list item", ""} {
		_, ok := TagLine(line, "PLAN:")
		assert.False(t, ok, "%q", line)
	}
}

func TestDeviations(t *testing.T) {
	assert.Equal(t, 2, Deviations("did it\nDEVIATION: renamed x\n**DEVIATION: skipped y**\nDEVIATION:\nSTATUS: DONE"))
	assert.Zero(t, Deviations("STATUS: DONE"))
}

func TestPremiseGroups(t *testing.T) {
	out := "plan_premises: p.md against the tree at abc\n" +
		"Preamble (plan:1): 2 premises, 1 flagged\n  plan:3  missing  x\n  ok held\n" +
		"Story 1 (plan:10): 4 premises, 0 flagged\n  ok a\n" +
		"Story 2 (plan:20): 4 premises, 1 flagged\n  plan:22  stale  y\n" +
		"Story 3 (plan:30): 4 premises, 1 flagged\n  plan:31  clash  z\n" +
		"Story 4 (plan:40): 4 premises, 1 flagged\n  plan:41  missing  w\n" +
		"Story 5 (plan:50): 4 premises, 2 flagged\n  plan:51  missing  v\n  plan:52  missing  u\n" +
		"7 of 22 premises flagged. Not flagged: 0.\nSend the flagged lines to premise-check.\n"
	groups := PremiseGroups(out, 3)
	require.Len(t, groups, 2)
	assert.Equal(t, []string{"preamble", "2", "3"}, groups[0].Stories)
	assert.Equal(t, "Preamble (plan:1): 2 premises, 1 flagged\n  plan:3  missing  x\nStory 2 (plan:20): 4 premises, 1 flagged\n  plan:22  stale  y\n"+
		"Story 3 (plan:30): 4 premises, 1 flagged\n  plan:31  clash  z\n", groups[0].Text)
	assert.Equal(t, []string{"4", "5"}, groups[1].Stories)
	assert.Contains(t, groups[1].Text, "plan:52  missing  u")
	assert.NotContains(t, groups[1].Text, "premises flagged")
	assert.Empty(t, PremiseGroups("", 3))
}

func TestPremiseVerdicts(t *testing.T) {
	got := PremiseVerdicts("plan:22 | STALE | DESIGN | y.go:4 | the type changed\n`plan:31 | HOLDS | - | z.go:1 |`\nnoise\n4 flags: 1 hold\n")
	require.Len(t, got, 2)
	assert.Equal(t, PremiseVerdict{Line: 22, Verdict: "STALE", Class: "DESIGN", Fix: "the type changed", Text: "plan:22 | STALE | DESIGN | y.go:4 | the type changed"}, got[0])
	assert.Equal(t, "HOLDS", got[1].Verdict)
	assert.Equal(t, "-", got[1].Class)
}
