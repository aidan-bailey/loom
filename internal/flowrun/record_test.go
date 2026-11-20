package flowrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecord(t *testing.T) {
	st, f, answers := script()
	drive(t, st, f, answers)
	st.Start, st.End = "2026-11-06T10:00:00Z", "2026-11-06T12:30:00Z"
	st.Managers = []string{"m1", "m"}
	st.Roster, st.Profile = "loom flow roster 0123456789ab", "/repo/.claude/flow.toml sha256:abcdefabcdef"
	st.DesignStage = "2 sessions, 40 minutes"
	st.Part = "2 of 3"
	st.Budget = "draft $30; run $25 (estimate 2026-11-06)"
	st.Stories[1].Form = "mechanical"
	st.Answers = append(st.Answers, Answer{Question: Question{Kind: KindBlocked, Story: "2", Text: "Which store?"}, Answer: "The cache.", From: FromOwner})
	got := Record(st, RecordInputs{
		Cost:      "COST TABLES\n<!-- agent-costs {} -->",
		Models:    map[string][]string{"m1": {"claude-opus-5-5"}},
		RosterEnd: "loom flow roster ba9876543210",
		Commits:   7,
		Head:      "h7abcdef1234",
	})
	golden(t, "record.golden", got)
}

// The Package rules line (package rules spec §2.3) follows the Engine
// line: absent when the record knows no agent run's rules.
func TestRecord_PackageRulesLine(t *testing.T) {
	st := newState(story("1", "intermediate-dev"))
	st.Status = Done
	assert.NotContains(t, Record(st, RecordInputs{}), "Package rules")
	assert.Contains(t, Record(st, RecordInputs{Rules: []AgentRules{{Name: "r-s1-dev1", Role: "intermediate-dev"}}}),
		"\n- Package rules: none pre-loaded\n\n## Cost")
	got := Record(st, RecordInputs{Rules: []AgentRules{
		{Name: "r-s1-dev1", Role: "intermediate-dev", Bytes: 2048},
		{Name: "r-s1-review1", Role: "code-quality-reviewer", Bytes: 4096},
		{Name: "r-s1-docs1", Role: "doc-writer"},
	}})
	assert.Contains(t, got, "- Engine: loom flow run (flow in loom part 3b)\n"+
		"- Package rules: 2 of 3 agent runs pre-loaded rules, 3.0 KB a run, most 4.0 KB (r-s1-review1, code-quality-reviewer)\n\n## Cost")
}
