package flowrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStepOf(t *testing.T) {
	stories := []Story{{ID: "1"}, {ID: "2"}, {ID: "2-b"}, {ID: "10"}, {ID: "4.1"}}
	for name, want := range map[string]Step{
		"r-premise1":      {Kind: KindPremise, K: 1},
		"r-s1-dev1":       {Story: "1", Kind: KindDev, K: 1},
		"r-s1-dev12":      {Story: "1", Kind: KindDev, K: 12},
		"r-s2-gates3":     {Story: "2", Kind: KindGates, K: 3},
		"r-s2-b-review1":  {Story: "2-b", Kind: KindReview, K: 1},
		"r-s10-fix2":      {Story: "10", Kind: KindFix, K: 2},
		"r-s4.1-dev1":     {Story: "4.1", Kind: KindDev, K: 1},
		"r-final-gates1":  {Final: true, Kind: KindGates, K: 1},
		"r-final-review2": {Final: true, Kind: KindReview, K: 2},
		"r-final-fix4":    {Final: true, Kind: KindFix, K: 4},
		"r-s1-docs1":      {Story: "1", Kind: KindDocs, K: 1},
		"r-s2-b-docs3":    {Story: "2-b", Kind: KindDocs, K: 3},
	} {
		got, ok := StepOf("r", name, stories)
		assert.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}
	for _, name := range []string{"other-s1-dev1", "r-s3-dev1", "r-s1-build1", "r-s1-dev", "r-s1-dev0", "r-s1-dev01", "r-final-dev1", "r-final-docs1", "r-premise", "r-premise0", "r", "r-"} {
		_, ok := StepOf("r", name, stories)
		assert.False(t, ok, name)
	}
}

// TestStepOf_ParsesEveryNameTheEngineSubmits drives scenarios through the
// engine and parses every agent and gate run name it recorded: the naming
// (engine.next, storyPrefix) and the parsing live together here, so a new
// prefix the engine makes fails this test before a board draws it grey.
func TestStepOf_ParsesEveryNameTheEngineSubmits(t *testing.T) {
	failed := GateResult{Ended: "failed", Failing: []GateStep{{Line: "go test ./...", Verdict: "FAIL"}}, Table: "1 FAIL go test"}
	flagged := "plan_premises: p.md against the tree at h0\n" +
		"Story 1 (plan:100): 1 premises, 1 flagged\n  plan:120  missing  a.go: no such file\n" +
		"1 of 1 premises flagged. Not flagged: 0, 0, 0.\n"
	for name, c := range map[string]struct {
		setup   func(f *fakeFX)
		stories []Story
		answers map[string]string
		docs    bool
	}{
		"plain": {stories: []Story{story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full")}},
		"gates fail, are fixed and fail again": {
			setup:   func(f *fakeFX) { f.gates["r-s1-gates1"], f.gates["r-s1-gates2"] = failed, failed },
			stories: []Story{story("1", "intermediate-dev", "vouched")},
			answers: map[string]string{"gates:r-s1-gates2": "Skip the flaky test."},
		},
		"kept minors reach the final round": {
			setup: func(f *fakeFX) {
				f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Minor a.go:3 tidy it\nVERDICT: APPROVED\nSTATUS: DONE"}
			},
			stories: []Story{story("1", "intermediate-dev", "vouched")},
		},
		"premises": {
			setup: func(f *fakeFX) {
				f.premises = flagged
				f.reports["r-premise1"] = Report{Status: "DONE", Text: "plan:120 | MOVED | COSMETIC | a2.go:1 | use a2.go\n"}
			},
			stories: []Story{story("1", "intermediate-dev", "vouched")},
		},
		"a doc pass with a stray decided": {
			setup:   func(f *fakeFX) { f.strays["h2"] = []string{"main.go"} },
			stories: []Story{story("1", "intermediate-dev", "vouched")},
			answers: map[string]string{"strays:r-s1-docs1": "Undo it."},
			docs:    true,
		},
		"final gates fail": {
			setup:   func(f *fakeFX) { f.gates["r-final-gates1"] = failed },
			stories: []Story{story("1", "intermediate-dev", "vouched")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			if c.setup != nil {
				c.setup(f)
			}
			st := newState(c.stories...)
			st.Docs = c.docs
			drive(t, st, f, c.answers)
			names := append(append([]string{}, st.Agents...), st.Gates...)
			assert.NotEmpty(t, names)
			for _, n := range names {
				s, ok := StepOf(st.ID, n, st.Stories)
				assert.True(t, ok, n)
				if !s.Final && s.Kind != KindPremise {
					assert.NotEmpty(t, s.Story, n)
				}
			}
		})
	}
}

func TestQuestionStep(t *testing.T) {
	stories := []Story{{ID: "1"}, {ID: "3"}}
	for _, c := range []struct {
		q    Question
		want Step
		ok   bool
	}{
		{Question{Key: "blocked:r-s3-dev1", Story: "3"}, Step{Story: "3", Kind: KindDev, K: 1}, true},
		{Question{Key: "gates:r-s1-gates2", Story: "1"}, Step{Story: "1", Kind: KindGates, K: 2}, true},
		{Question{Key: "ctx:r-s3-review1:1", Story: "3"}, Step{Story: "3", Kind: KindReview, K: 1}, true},
		{Question{Key: "failed:r-final-fix2", Story: "final"}, Step{Final: true, Kind: KindFix, K: 2}, true},
		{Question{Key: "tree:r-s1-dev1", Story: "1"}, Step{Story: "1", Kind: KindDev, K: 1}, true},
		{Question{Key: "design:3", Story: "3"}, Step{Story: "3", Kind: KindReview}, true},
		{Question{Key: "unresolved:s3:1", Story: "3"}, Step{Story: "3", Kind: KindReview}, true},
		{Question{Key: "plan:3", Story: "3"}, Step{Story: "3", Kind: KindReview}, true},
		{Question{Key: "design:final", Story: "final"}, Step{Final: true, Kind: KindReview}, true},
		{Question{Key: "unresolved:final:2"}, Step{Final: true, Kind: KindReview}, true},
		{Question{Key: "premise-design"}, Step{Kind: KindPremise}, true},
		{Question{Key: "strays:r-s1-docs2", Story: "1"}, Step{Story: "1", Kind: KindDocs, K: 2}, true},
		{Question{Key: "limit:r-s3-review2:1", Story: "3"}, Step{Story: "3", Kind: KindReview, K: 2}, true},
		{Question{Key: "plan:3"}, Step{}, false},
		{Question{Key: "something-new"}, Step{}, false},
	} {
		got, ok := QuestionStep("r", c.q, stories)
		assert.Equal(t, c.ok, ok, c.q.Key)
		assert.Equal(t, c.want, got, c.q.Key)
	}
}
