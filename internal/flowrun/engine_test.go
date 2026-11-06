package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errCrash = errors.New("a crash at this save")

// fakeFX is Effects scripted per agent and gate run: reports[name] is the
// report an agent run ends with (by role when absent), resumes[name] the
// reports of its resumes in order, gates[name] a gate run's result (passed
// when absent). A developer that reports done moves HEAD by one commit,
// unless noCommit names it. It logs a submit as `agent <name> <role>
// <effort>` or `gates <name>`, and a call to a run that already exists as
// `follow <name>`. Save fails at save number failAt, as a crash, and keeps
// the last state it saved.
type fakeFX struct {
	reports    map[string]Report
	resumes    map[string][]Report
	gates      map[string]GateResult
	noCommit   map[string]bool
	started    map[string]bool
	roles      map[string]string
	briefs     map[string]string
	answered   map[string]string // an agent run's last answer
	answerRep  map[string]Report // and the report it got
	head       int
	dirty      bool
	premises   string
	markers    []string
	serviceErr error
	calls      []string
	saves      int
	failAt     int
	saved      *State
}

func newFake() *fakeFX {
	return &fakeFX{reports: map[string]Report{}, resumes: map[string][]Report{}, gates: map[string]GateResult{},
		noCommit: map[string]bool{}, started: map[string]bool{}, roles: map[string]string{}, briefs: map[string]string{},
		answered: map[string]string{}, answerRep: map[string]Report{}}
}

func isDev(role string) bool {
	return role == "junior-dev" || role == "intermediate-dev" || role == "senior-dev"
}

func defaultReport(name, role string) Report {
	r := Report{Status: "DONE", Text: "STATUS: DONE", Path: "/reports/" + name}
	if strings.HasSuffix(role, "reviewer") {
		r.Text = "VERDICT: APPROVED\nSTATUS: DONE"
	}
	return r
}

func (f *fakeFX) report(name, role string) Report {
	r, ok := f.reports[name]
	if !ok {
		return defaultReport(name, role)
	}
	if r.Path == "" {
		r.Path = "/reports/" + name
	}
	return r
}

func (f *fakeFX) AgentStarted(name string) (bool, error) { return f.started[name], nil }

func (f *fakeFX) Agent(name, role, effort, brief string) (Report, error) {
	r := f.report(name, role)
	if f.started[name] {
		f.calls = append(f.calls, "follow "+name)
		return r, nil
	}
	f.started[name], f.roles[name], f.briefs[name] = true, role, brief
	f.calls = append(f.calls, strings.TrimSpace(fmt.Sprintf("agent %s %s %s", name, role, effort)))
	if isDev(role) && r.done() && !f.noCommit[name] {
		f.head++
	}
	return r, nil
}

func (f *fakeFX) Resume(name, text string) (Report, error) {
	if a, ok := f.answered[name]; ok && a == text {
		f.calls = append(f.calls, "follow "+name)
		return f.answerRep[name], nil
	}
	f.calls = append(f.calls, "resume "+name+" "+text)
	var r Report
	if rs := f.resumes[name]; len(rs) > 0 {
		r, f.resumes[name] = rs[0], rs[1:]
	} else {
		r = defaultReport(name, f.roles[name])
	}
	if r.Path == "" {
		r.Path = "/reports/" + name
	}
	if isDev(f.roles[name]) && r.done() {
		f.head++
	}
	f.answered[name], f.answerRep[name] = text, r
	return r, nil
}

func (f *fakeFX) GatesStarted(name string) (bool, error) { return f.started["gates "+name], nil }

func (f *fakeFX) Services([]string) error {
	f.calls = append(f.calls, "services")
	return f.serviceErr
}

func (f *fakeFX) Gates(name string, _ []string) (GateResult, error) {
	if f.started["gates "+name] {
		f.calls = append(f.calls, "follow "+name)
	} else {
		f.started["gates "+name] = true
		f.calls = append(f.calls, "gates "+name)
	}
	if r, ok := f.gates[name]; ok {
		return r, nil
	}
	return GateResult{Ended: "passed"}, nil
}

func (f *fakeFX) Head() (string, error) { return "h" + strconv.Itoa(f.head), nil }
func (f *fakeFX) Clean() (bool, error)  { return !f.dirty, nil }

func (f *fakeFX) Commits(base, head string) ([]string, error) {
	b, _ := strconv.Atoi(strings.TrimPrefix(base, "h"))
	h, _ := strconv.Atoi(strings.TrimPrefix(head, "h"))
	var cs []string
	for i := b + 1; i <= h; i++ {
		cs = append(cs, "c"+strconv.Itoa(i))
	}
	return cs, nil
}

func (f *fakeFX) Conformance(story, _, head string) (Conformance, error) {
	return Conformance{Path: "/conformance/" + story + "-" + head, Markers: f.markers}, nil
}

func (f *fakeFX) Premises(string) (string, error) { return f.premises, nil }

func (f *fakeFX) Save(st *State) error {
	f.saves++
	if f.failAt > 0 && f.saves == f.failAt {
		return errCrash
	}
	f.saved = clone(st)
	return nil
}

func clone(st *State) *State {
	b, err := json.Marshal(st)
	if err != nil {
		panic(err)
	}
	var out State
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return &out
}

// story is a plan's story with its plan lines n*100 to n*100+99.
func story(id, developer, review string) Story {
	n, _ := strconv.Atoi(id)
	return Story{ID: id, Line: n * 100, End: n*100 + 99, Developer: developer, Tier: "deep", Review: review, Verify: "go test ./x"}
}

func newState(stories ...Story) *State {
	return &State{Schema: Schema, ID: "r", Status: Running, Plan: "p.md", Spec: "s.md", Base: "h0",
		Blocks:  Blocks{Developer: "Prefix: none\nLandmine notes (deep stories only):\n    a note", Reviewer: "rev"},
		Stories: stories}
}

// drive runs st, answering each question by its key from answers, until it
// ends; a question answers lacks fails the test.
func drive(t *testing.T, st *State, f *fakeFX, answers map[string]string) {
	t.Helper()
	for range 50 {
		require.NoError(t, Run(st, f))
		if st.Status != Parked {
			return
		}
		a, ok := answers[st.Question.Key]
		require.True(t, ok, "an unexpected question %s (%s): %s", st.Question.Key, st.Question.Kind, st.Question.Text)
		_, err := CheckAnswer(st.Question.Kind, a)
		require.NoError(t, err)
		require.NoError(t, st.Answer(a, "m", FromSpec))
	}
	t.Fatal("the run never ended")
}

// submits is f's submitted agent and gate runs.
func (f *fakeFX) submits() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "agent ") || strings.HasPrefix(c, "gates ") {
			out = append(out, c)
		}
	}
	return out
}

func TestRun_AStoryRunsDeveloperGatesReview(t *testing.T) {
	f := newFake()
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, nil)
	assert.Equal(t, Done, st.Status)
	assert.Equal(t, []string{
		"agent r-s1-dev1 intermediate-dev", "services", "gates r-s1-gates1", "agent r-s1-review1 story-reviewer",
		"services", "gates r-final-gates1", "agent r-final-review1 code-quality-reviewer",
	}, f.calls)
	row := st.Stories[0].Row
	assert.Equal(t, Row{Status: "done", Developer: "intermediate-dev", Base: "h0", Head: "h1", Commits: 1}, row)
	assert.NotEmpty(t, st.End)
	assert.Equal(t, []string{"r-s1-dev1", "r-s1-review1", "r-final-review1"}, st.Agents)
}

func TestRun_KeptMinorsReachTheFinalRound(t *testing.T) {
	f := newFake()
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Minor a.go:3 tidy it\nVERDICT: APPROVED\nSTATUS: DONE"}
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, nil)
	assert.Contains(t, f.calls, "agent r-final-fix1 intermediate-dev")
	assert.Contains(t, f.briefs["r-final-fix1"], "Minor a.go:3 tidy it")
	assert.Contains(t, f.calls, "agent r-final-review2 story-reviewer", "the round is re-reviewed")
	assert.Equal(t, [3]int{0, 0, 1}, st.Stories[0].Row.Findings)
}

func TestRun_NeedsContextParksAndTheAnswerResumes(t *testing.T) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which colour?"}
	st := newState(story("1", "intermediate-dev", "vouched"))
	require.NoError(t, Run(st, f))
	require.Equal(t, Parked, st.Status)
	assert.Equal(t, Question{Key: "ctx:r-s1-dev1:1", Kind: KindNeedsContext, Story: "1", Text: "Which colour?", Paths: []string{"/reports/r-s1-dev1"}}, *st.Question)
	require.NoError(t, st.Answer("blue", "m2", FromSpec))
	drive(t, st, f, nil)
	assert.Contains(t, f.calls, "resume r-s1-dev1 blue")
	assert.Contains(t, st.Managers, "m2")
	assert.Equal(t, 1, st.Stories[0].Row.Escalated)
}

func TestRun_JuniorBlockedOrCappedGoesToAnIntermediate(t *testing.T) {
	for _, status := range []string{"BLOCKED", "TURN_CAP"} {
		t.Run(status, func(t *testing.T) {
			f := newFake()
			f.reports["r-s1-dev1"] = Report{Status: status}
			st := newState(story("1", "junior-dev", "vouched"))
			drive(t, st, f, nil)
			assert.Equal(t, []string{"agent r-s1-dev1 junior-dev", "agent r-s1-dev2 intermediate-dev"}, f.submits()[:2])
			assert.Contains(t, f.briefs["r-s1-dev2"], "/reports/r-s1-dev1", "the junior's report")
			row := st.Stories[0].Row
			assert.Equal(t, 1, row.UpATier)
			assert.Equal(t, status == "TURN_CAP", row.TurnCap)
			assert.Equal(t, "intermediate-dev", row.Developer)
			assert.Zero(t, row.Escalated, "no question")
		})
	}
}

func TestRun_IntermediateTurnCapResumesOnceThenAsks(t *testing.T) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "TURN_CAP"}
	f.resumes["r-s1-dev1"] = []Report{{Status: "TURN_CAP"}}
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, map[string]string{"failed:r-s1-dev1": "retry"})
	assert.Contains(t, f.calls, "resume r-s1-dev1 Continue from where you stopped.")
	assert.Contains(t, f.submits(), "agent r-s1-dev2 intermediate-dev", "a retry is a fresh agent of the same role")
	assert.True(t, st.Stories[0].Row.TurnCap)
}

func TestRun_IntermediateBlockedAsksThenDecidesThenSenior(t *testing.T) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "BLOCKED"}
	f.reports["r-s1-dev2"] = Report{Status: "BLOCKED"}
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, map[string]string{"blocked:r-s1-dev1": "Use the cache."})
	sub := f.submits()
	assert.Equal(t, []string{"agent r-s1-dev1 intermediate-dev", "agent r-s1-dev2 intermediate-dev", "agent r-s1-dev3 senior-dev"}, sub[:3])
	assert.Contains(t, f.briefs["r-s1-dev2"], "Use the cache.")
	assert.Contains(t, f.briefs["r-s1-dev3"], "Use the cache.")
	assert.Contains(t, f.briefs["r-s1-dev3"], "/reports/r-s1-dev1")
	assert.Contains(t, f.briefs["r-s1-dev3"], "/reports/r-s1-dev2")
	assert.Equal(t, 1, st.Stories[0].Row.UpATier)
	assert.Equal(t, 1, st.Stories[0].Row.Escalated, "the senior took it without a question")
	assert.Equal(t, "senior-dev", st.Stories[0].Row.Developer)
}

func TestRun_DoneWithNoCommitsIsAFailure(t *testing.T) {
	f := newFake()
	f.noCommit["r-s1-dev1"] = true
	st := newState(story("1", "intermediate-dev", "vouched"))
	require.NoError(t, Run(st, f))
	require.Equal(t, Parked, st.Status)
	assert.Equal(t, KindAgentFailed, st.Question.Kind)
	assert.Contains(t, st.Question.Text, "reported done with no commits")
	require.NoError(t, st.Answer("retry", "m", FromSpec))
	drive(t, st, f, nil)
	assert.Contains(t, f.submits(), "agent r-s1-dev2 intermediate-dev")
}

func TestRun_AFixerDoneWithNoCommitsIsNoted(t *testing.T) {
	t.Run("in a story's fix round", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Important a.go:3 fix\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		f.noCommit["r-s1-fix1"] = true
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil) // any question fails the test
		require.Equal(t, Done, st.Status)
		assert.NotContains(t, f.submits(), "agent r-s1-fix2 intermediate-dev")
		assert.Contains(t, f.submits(), "agent r-s1-review2 story-reviewer", "the re-review judges the findings")
		assert.Contains(t, strings.Join(st.Notes, "\n"), "r-s1-fix1 reported done with no commits")
	})
	t.Run("in the final round, a kept Minor already fixed", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Minor b.go:2 two\nVERDICT: APPROVED\nSTATUS: DONE"}
		f.noCommit["r-final-fix1"] = true
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		require.Equal(t, Done, st.Status)
		assert.Contains(t, f.submits(), "gates r-final-gates2", "the round is still gated")
		assert.Contains(t, f.submits(), "agent r-final-review2 story-reviewer", "and re-reviewed")
		assert.Contains(t, strings.Join(st.Notes, "\n"), "r-final-fix1 reported done with no commits")
	})
}

func TestRun_LandminesRunAtXHighAndAFixAtItsPin(t *testing.T) {
	f := newFake()
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 fix\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	s := story("1", "intermediate-dev", "full")
	s.Landmines = []string{"os.RemoveAll("}
	st := newState(s)
	drive(t, st, f, nil)
	assert.Contains(t, f.calls, "agent r-s1-dev1 intermediate-dev xhigh")
	assert.Contains(t, f.calls, "agent r-s1-fix1 intermediate-dev")
}

func TestRun_GateFailures(t *testing.T) {
	failed := GateResult{Ended: "failed", Failing: []GateStep{{Line: "go test ./...", Verdict: "FAIL", Log: "/logs/1"}}, Table: "1 FAIL go test"}
	t.Run("a failure is fixed and the gates run again", func(t *testing.T) {
		f := newFake()
		f.gates["r-s1-gates1"] = failed
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		sub := f.submits()
		assert.Equal(t, []string{"agent r-s1-dev1 intermediate-dev", "gates r-s1-gates1", "agent r-s1-fix1 intermediate-dev", "gates r-s1-gates2"}, sub[:4])
		assert.Contains(t, f.briefs["r-s1-fix1"], "FAIL: go test ./... (log: /logs/1)")
	})
	for _, c := range []struct{ answer, want string }{
		{"retry", "gates r-s1-gates3"},
		{"accept", "agent r-s1-review1 story-reviewer"},
		{"Skip the flaky test.", "agent r-s1-fix2 intermediate-dev"},
	} {
		t.Run("a second failure asks, answered "+c.answer, func(t *testing.T) {
			f := newFake()
			f.gates["r-s1-gates1"], f.gates["r-s1-gates2"] = failed, failed
			st := newState(story("1", "intermediate-dev", "vouched"))
			drive(t, st, f, map[string]string{"gates:r-s1-gates2": c.answer})
			sub := f.submits()
			assert.Equal(t, c.want, sub[4], "%v", sub)
		})
	}
	t.Run("an infra failure asks at once", func(t *testing.T) {
		f := newFake()
		f.gates["r-s1-gates1"] = GateResult{Ended: "infra", Table: "FAIL (infra)"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gates:r-s1-gates1", st.Question.Key)
		assert.Equal(t, KindGateFailure, st.Question.Kind)
	})
	t.Run("a story parked at its gates counts its commits", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = Report{Status: "DONE"}
		f.gates["r-s1-gates1"] = GateResult{Ended: "infra", Table: "FAIL (infra)"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gating", st.Stories[0].Row.Status)
		assert.Equal(t, 1, st.Stories[0].Row.Commits, "show prints the commits a gating story's gates run over")
	})
	t.Run("a failing service is infra, and the gates aren't started", func(t *testing.T) {
		f := newFake()
		f.serviceErr = errors.New("db: exit 1")
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gates:r-s1-gates1", st.Question.Key)
		assert.Contains(t, st.Question.Text, "a service failed: db: exit 1")
		assert.NotContains(t, f.calls, "gates r-s1-gates1")
	})
}

func TestRun_ReviewerChoice(t *testing.T) {
	for _, c := range []struct {
		name, review, devStatus string
		markers                 []string
		want                    string
	}{
		{"vouched", "vouched", "DONE", nil, "story-reviewer"},
		{"full", "full", "DONE", nil, "code-quality-reviewer"},
		{"vouched, done with concerns", "vouched", "DONE_WITH_CONCERNS", nil, "code-quality-reviewer"},
		{"vouched, a marker in uncovered lines", "vouched", "DONE", []string{"os.RemoveAll("}, "code-quality-reviewer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			f.markers = c.markers
			f.reports["r-s1-dev1"] = Report{Status: c.devStatus, Text: "STATUS: " + c.devStatus}
			st := newState(story("1", "intermediate-dev", c.review))
			drive(t, st, f, nil)
			assert.Contains(t, f.calls, "agent r-s1-review1 "+c.want)
		})
	}
}

func TestRun_FindingsRoutes(t *testing.T) {
	critical := Report{Status: "DONE", Text: "FINDING: Critical a.go:3 guard it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	t.Run("a Critical is fixed, gated and re-reviewed", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = critical
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		sub := f.submits()
		assert.Equal(t, []string{"agent r-s1-fix1 intermediate-dev", "gates r-s1-gates2", "agent r-s1-review2 story-reviewer"}, sub[3:6])
		assert.Contains(t, f.briefs["r-s1-fix1"], "Critical a.go:3 guard it")
		assert.Contains(t, f.briefs["r-s1-review2"], "Re-review a fix round")
		assert.Equal(t, [3]int{1, 0, 0}, st.Stories[0].Row.Findings)
		assert.Equal(t, "intermediate-dev", st.Stories[0].Row.Developer)
	})
	t.Run("a fixer is not the story's developer", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = critical
		st := newState(story("1", "junior-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Equal(t, "junior-dev", st.Stories[0].Row.Developer)
	})
	t.Run("a design finding asks first", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Important design a.go:3 decide it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"design:1": "Return an error."})
		assert.Contains(t, f.briefs["r-s1-fix1"], "Return an error.")
	})
	t.Run("still blocking after the fix asks, and accept moves on", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"], f.reports["r-s1-review2"] = critical, critical
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"unresolved:s1:1": "accept"})
		assert.Equal(t, Done, st.Status)
		assert.Contains(t, strings.Join(st.Notes, "\n"), "accepted 1 findings left open")
	})
	t.Run("a review without a verdict is a failure", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "looks fine\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-review1": "retry"})
		assert.Contains(t, f.submits(), "agent r-s1-review2 story-reviewer")
	})
	t.Run("a non-developer's BLOCKED is a failure", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "BLOCKED"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, KindAgentFailed, st.Question.Kind)
	})
}

func TestRun_PlanLinesAreAskedOnce(t *testing.T) {
	f := newFake()
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "PLAN: its loop never ends\nVERDICT: APPROVED\nSTATUS: DONE"}
	f.reports["r-s1-review2"] = Report{Status: "DONE", Text: "PLAN: another one\nVERDICT: APPROVED\nSTATUS: DONE"}
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, map[string]string{"plan:1": "Bound the loop at ten."})
	assert.Contains(t, f.briefs["r-s1-fix1"], "Plan: its loop never ends")
	assert.Contains(t, f.briefs["r-s1-fix1"], "Bound the loop at ten.")
	assert.Contains(t, strings.Join(st.Notes, "\n"), "not asked again: another one")
}

func TestRun_Premises(t *testing.T) {
	flagged := "plan_premises: p.md against the tree at h0\n" +
		"Story 1 (plan:100): 3 premises, 2 flagged\n  plan:120  missing  a.go: no such file\n  plan:130  stale    b.go: changed\n" +
		"2 of 3 premises flagged. Not flagged: 0, 0, 0.\n"
	t.Run("a cosmetic verdict reaches the story's brief", func(t *testing.T) {
		f := newFake()
		f.premises = flagged
		f.reports["r-premise1"] = Report{Status: "DONE", Text: "plan:120 | MOVED | COSMETIC | a2.go:1 | use a2.go\nplan:130 | HOLDS | - | b.go:4 |\n"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Equal(t, "agent r-premise1 premise-check", f.submits()[0])
		assert.Contains(t, f.briefs["r-premise1"], "plan:120  missing")
		assert.Contains(t, f.briefs["r-s1-dev1"], "plan:120 | MOVED | COSMETIC | a2.go:1 | use a2.go")
		assert.NotContains(t, f.briefs["r-s1-dev1"], "plan:130")
	})
	t.Run("a design verdict asks before any story", func(t *testing.T) {
		f := newFake()
		f.premises = flagged
		f.reports["r-premise1"] = Report{Status: "DONE", Text: "plan:130 | STALE | DESIGN | b.go:4 | the type changed\n"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, KindPremiseDesign, st.Question.Kind)
		assert.Equal(t, []string{"agent r-premise1 premise-check"}, f.submits())
		require.NoError(t, st.Answer("Use the new type.", "m", FromSpec))
		drive(t, st, f, nil)
		assert.Contains(t, f.briefs["r-s1-dev1"], "the manager: Use the new type.")
	})
}

func TestRun_TreeMoved(t *testing.T) {
	for _, c := range []struct{ answer, status string }{{"continue", Done}, {"stop", Stopped}} {
		t.Run(c.answer, func(t *testing.T) {
			f := newFake()
			f.head = 3 // someone committed before the run's first dispatch
			st := newState(story("1", "intermediate-dev", "vouched"))
			require.NoError(t, Run(st, f))
			require.Equal(t, Parked, st.Status)
			assert.Equal(t, Question{Key: "tree:r-s1-dev1", Kind: KindTreeMoved, Story: "1",
				Text: "HEAD is h3, and the engine last recorded h0, before it starts r-s1-dev1"}, *st.Question)
			require.NoError(t, st.Answer(c.answer, "m", FromSpec))
			drive(t, st, f, nil)
			assert.Equal(t, c.status, st.Status)
		})
	}
	t.Run("a dirty tree", func(t *testing.T) {
		f := newFake()
		f.dirty = true
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Contains(t, st.Question.Text, "uncommitted changes")
	})
}

func TestRun_AFollowedAgentSkipsTheTreeCheck(t *testing.T) {
	f := newFake()
	f.started["r-s1-dev1"], f.roles["r-s1-dev1"], f.head = true, "intermediate-dev", 1 // its own commit moved HEAD
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, nil)
	assert.Equal(t, "follow r-s1-dev1", f.calls[0])
	assert.Zero(t, st.Stories[0].Row.Escalated)
}

func TestRun_FinalRoundGroupsByFile(t *testing.T) {
	f := newFake()
	f.reports["r-final-review1"] = Report{Status: "DONE", Text: "FINDING: Important a.go:1 one\nFINDING: Minor b.go:2 two\nFINDING: Important a.go:9 three\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, nil)
	assert.Contains(t, f.briefs["r-final-fix1"], "a.go:1 one")
	assert.Contains(t, f.briefs["r-final-fix1"], "a.go:9 three")
	assert.NotContains(t, f.briefs["r-final-fix1"], "b.go")
	assert.Contains(t, f.briefs["r-final-fix2"], "b.go:2 two")
	assert.Contains(t, f.calls, "gates r-final-gates2", "the round is gated")
	assert.Contains(t, f.calls, "agent r-final-review2 story-reviewer", "and re-reviewed")
}

func TestRun_StopEndsStopped(t *testing.T) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "BLOCKED"}
	st := newState(story("1", "intermediate-dev", "vouched"), story("2", "intermediate-dev", "vouched"))
	drive(t, st, f, map[string]string{"blocked:r-s1-dev1": "stop"})
	assert.Equal(t, Stopped, st.Status)
	assert.NotEmpty(t, st.End)
	assert.Nil(t, st.Question)
	assert.NotContains(t, strings.Join(f.calls, "\n"), "r-s2-dev1")
}

// script is a two-story run with a question, a fix round and a kept Minor.
func script() (*State, *fakeFX, map[string]string) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which?"}
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 fix\nFINDING: Minor a.go:5 tidy\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	f.reports["r-s2-dev1"] = Report{Status: "BLOCKED"}
	f.gates["r-s2-gates1"] = GateResult{Ended: "failed", Failing: []GateStep{{Line: "go vet", Verdict: "FAIL"}}}
	st := newState(story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"))
	return st, f, map[string]string{"ctx:r-s1-dev1:1": "That one."}
}

func TestRun_ARestartAtEverySavePointSubmitsNothingTwice(t *testing.T) {
	st, f, answers := script()
	drive(t, st, f, answers)
	require.Equal(t, Done, st.Status)
	want := f.submits()
	slices.Sort(want)
	saves := f.saves
	for k := 1; k <= saves; k++ {
		t.Run(fmt.Sprintf("a crash at save %d", k), func(t *testing.T) {
			st, f, answers := script()
			f.failAt = k
			err := Run(st, f)
			for err != nil || st.Status == Parked {
				if err != nil {
					require.ErrorIs(t, err, errCrash)
					st = clone(f.saved) // the engine restarts from the file
					if f.saved == nil {
						st, _, _ = script()
					}
				} else {
					require.NoError(t, st.Answer(answers[st.Question.Key], "m", FromSpec))
				}
				err = Run(st, f)
			}
			require.Equal(t, Done, st.Status)
			got := f.submits()
			slices.Sort(got)
			assert.Equal(t, want, got)
		})
	}
}

func TestRun_ARestartRebuildsTheRowsOnce(t *testing.T) {
	st, f, answers := script()
	drive(t, st, f, answers)
	once := clone(st)
	st.Status, st.End = Running, ""
	require.NoError(t, Run(st, f)) // a pass over a finished run replays every step
	assert.Equal(t, once.Stories, st.Stories)
	assert.Equal(t, once.Notes, st.Notes)
	assert.Equal(t, once.Kept, st.Kept)
}
