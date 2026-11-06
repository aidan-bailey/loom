package flowrun

import (
	"testing"
)

func TestRecord(t *testing.T) {
	st, f, answers := script()
	drive(t, st, f, answers)
	st.Start, st.End = "2026-11-06T10:00:00Z", "2026-11-06T12:30:00Z"
	st.Managers = []string{"m1", "m"}
	st.Roster, st.Profile = "loom flow roster 0123456789ab", "/repo/.claude/flow.toml sha256:abcdefabcdef"
	st.DesignStage = "2 sessions, 40 minutes"
	got := Record(st, RecordInputs{
		Cost:      "COST TABLES\n<!-- agent-costs {} -->",
		Models:    map[string][]string{"m1": {"claude-opus-5-5"}},
		RosterEnd: "loom flow roster ba9876543210",
		Commits:   7,
		Head:      "h7abcdef1234",
	})
	golden(t, "record.golden", got)
}
