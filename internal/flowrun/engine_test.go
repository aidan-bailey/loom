package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
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
	strays     map[string][]string // DocsCheck's answer, by head
	commitsOn  map[string]bool     // a run that commits though it doesn't report done
	serviceErr error
	calls      []string
	saves      int
	failAt     int
	saved      *State
	dirs       map[string]string // each agent run's working dir
	mu         sync.Mutex
}

func newFake() *fakeFX {
	return &fakeFX{reports: map[string]Report{}, resumes: map[string][]Report{}, gates: map[string]GateResult{},
		noCommit: map[string]bool{}, started: map[string]bool{}, roles: map[string]string{}, briefs: map[string]string{},
		answered: map[string]string{}, answerRep: map[string]Report{}, strays: map[string][]string{}, commitsOn: map[string]bool{},
		dirs: map[string]string{}}
}

func isDev(role string) bool {
	return role == "junior-dev" || role == "intermediate-dev" || role == "senior-dev"
}

// commits says whether role's done report moves HEAD: a developer's, and
// a doc writer's.
func commits(role string) bool { return isDev(role) || role == "doc-writer" }

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

func (f *fakeFX) AgentStarted(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[name], nil
}

func (f *fakeFX) Agent(dir, name, role, effort, brief string) (Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.report(name, role)
	if f.started[name] {
		f.calls = append(f.calls, "follow "+name)
		return r, nil
	}
	f.started[name], f.roles[name], f.briefs[name], f.dirs[name] = true, role, brief, dir
	f.calls = append(f.calls, strings.TrimSpace(fmt.Sprintf("agent %s %s %s", name, role, effort)))
	if commits(role) && (r.done() || f.commitsOn[name]) && !f.noCommit[name] {
		f.head++
	}
	return r, nil
}

func (f *fakeFX) Resume(name, text string) (Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	if commits(f.roles[name]) && r.done() {
		f.head++
	}
	f.answered[name], f.answerRep[name] = text, r
	return r, nil
}

func (f *fakeFX) GatesStarted(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started["gates "+name], nil
}

func (f *fakeFX) Services([]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "services")
	return f.serviceErr
}

func (f *fakeFX) Gates(name string, _ []string) (GateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeFX) Head(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "h" + strconv.Itoa(f.head), nil
}

func (f *fakeFX) Clean(string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dirty, nil
}

func (f *fakeFX) Commits(base, head string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := strconv.Atoi(strings.TrimPrefix(base, "h"))
	h, _ := strconv.Atoi(strings.TrimPrefix(head, "h"))
	var cs []string
	for i := b + 1; i <= h; i++ {
		cs = append(cs, "c"+strconv.Itoa(i))
	}
	return cs, nil
}

func (f *fakeFX) Conformance(story, _, head string) (Conformance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Conformance{Path: "/conformance/" + story + "-" + head, Markers: f.markers}, nil
}

func (f *fakeFX) Premises(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.premises, nil
}

func (f *fakeFX) DocsCheck(base, head string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "docs-check "+base+".."+head)
	return f.strays[head], nil
}

func (f *fakeFX) Save(st *State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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

// runSettled is Run, waiting after its outcome for every track's goroutine,
// so a test's next Run never shares the fake with a track still waking.
func runSettled(st *State, fx Effects) error {
	e := newEngine(st, fx)
	err := e.start()
	e.wg.Wait()
	return err
}

// drive runs st, answering each question by its key from answers, until it
// ends; a question answers lacks fails the test.
func drive(t *testing.T, st *State, f *fakeFX, answers map[string]string) {
	t.Helper()
	for range 50 {
		require.NoError(t, runSettled(st, f))
		if st.Status != Parked {
			return
		}
		a, ok := answers[st.Question.Key]
		require.True(t, ok, "an unexpected question %s (%s): %s", st.Question.Key, st.Question.Kind, st.Question.Text)
		_, err := CheckAnswer(st.Question.Kind, a)
		require.NoError(t, err)
		require.NoError(t, st.Answer(a, "m", FromSpec, ""))
	}
	t.Fatal("the run never ended")
}

// submits is f's submitted agent and gate runs.
func (f *fakeFX) submits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	require.NoError(t, runSettled(st, f))
	require.Equal(t, Parked, st.Status)
	assert.Equal(t, Question{Key: "ctx:r-s1-dev1:1", Kind: KindNeedsContext, Story: "1", Text: "Which colour?", Paths: []string{"/reports/r-s1-dev1"}, At: "h0"}, *st.Question)
	require.NoError(t, st.Answer("blue", "m2", FromSpec, ""))
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
	require.NoError(t, runSettled(st, f))
	require.Equal(t, Parked, st.Status)
	assert.Equal(t, KindAgentFailed, st.Question.Kind)
	assert.Contains(t, st.Question.Text, "reported done with no commits")
	require.NoError(t, st.Answer("retry", "m", FromSpec, ""))
	drive(t, st, f, nil)
	assert.Contains(t, f.submits(), "agent r-s1-dev2 intermediate-dev")
}

// An agent-failed question takes a decision: a retry with it in the next
// brief (feedback thread 12).
func TestRun_AFailedAgentRetriesWithTheDecision(t *testing.T) {
	const decision = "Merge main first."
	failed := Report{Status: "failed", Why: "a question was asked but not given"}
	t.Run("a developer", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = failed
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-dev1": decision})
		assert.Equal(t, Done, st.Status)
		assert.Contains(t, f.submits(), "agent r-s1-dev2 intermediate-dev", "a decision is a retry of the same role")
		assert.Contains(t, f.briefs["r-s1-dev2"], decision)
		assert.NotContains(t, f.briefs["r-s1-dev1"], decision)
	})
	t.Run("a developer done with no commits", func(t *testing.T) {
		f := newFake()
		f.noCommit["r-s1-dev1"] = true
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-dev1": decision})
		assert.Contains(t, f.briefs["r-s1-dev2"], decision)
	})
	t.Run("a plain retry carries no decision", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = failed
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-dev1": "retry"})
		assert.Equal(t, f.briefs["r-s1-dev1"], f.briefs["r-s1-dev2"])
	})
	t.Run("a reviewer", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = failed
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-review1": decision})
		assert.Contains(t, f.briefs["r-s1-review2"], decision)
	})
	t.Run("a premise-check", func(t *testing.T) {
		f := newFake()
		f.premises = "plan_premises: p.md against the tree at h0\nStory 1 (plan:100): 1 premises, 1 flagged\n  plan:120  missing  a.go: no such file\n"
		f.reports["r-premise1"] = failed
		f.reports["r-premise2"] = Report{Status: "DONE", Text: "plan:120 | HOLDS | - | a.go:1 |\n"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-premise1": decision})
		assert.Contains(t, f.briefs["r-premise2"], decision)
	})
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
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gates:r-s1-gates1", st.Question.Key)
		assert.Equal(t, KindGateFailure, st.Question.Kind)
	})
	t.Run("a story parked at its gates counts its commits", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = Report{Status: "DONE"}
		f.gates["r-s1-gates1"] = GateResult{Ended: "infra", Table: "FAIL (infra)"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gating", st.Stories[0].Row.Status)
		assert.Equal(t, 1, st.Stories[0].Row.Commits, "show prints the commits a gating story's gates run over")
	})
	t.Run("a failing service is infra, and the gates aren't started", func(t *testing.T) {
		f := newFake()
		f.serviceErr = errors.New("db: exit 1")
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gates:r-s1-gates1", st.Question.Key)
		assert.Contains(t, st.Question.Text, "a service failed: db: exit 1")
		assert.NotContains(t, f.calls, "gates r-s1-gates1")
	})
}

// TestRun_AnAcceptedGateFailure pins thread 20: once the manager accepts a
// story's failing steps, a later gate run of the story that fails only on
// them asks again rather than dispatching a fixer, and one failing on any
// other step still gets a fixer. Each question ends a Run, so the next Run
// knows the accepted steps only by replaying the earlier gate run.
func TestRun_AnAcceptedGateFailure(t *testing.T) {
	step := func(line string) GateStep { return GateStep{Line: line, Verdict: "FAIL", Log: "/logs/" + line} }
	accepted := GateResult{Ended: "failed", Failing: []GateStep{step("npm test"), step("npm run lint")}, Table: "FAIL npm test\nFAIL npm run lint"}
	// gates1 and gates2 fail, the manager accepts at gates2; the review's fix
	// round (r-s1-fix2) then gates again at gates3.
	setup := func(gates3 GateResult) (*State, *fakeFX) {
		f := newFake()
		f.gates["r-s1-gates1"], f.gates["r-s1-gates2"], f.gates["r-s1-gates3"] = accepted, accepted, gates3
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Important a.go:3 fix\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		return newState(story("1", "intermediate-dev", "vouched")), f
	}
	t.Run("failing only on accepted steps asks", func(t *testing.T) {
		st, f := setup(GateResult{Ended: "failed", Failing: []GateStep{step("npm run lint")}, Table: "FAIL npm run lint"})
		require.NoError(t, runSettled(st, f))
		require.Equal(t, "gates:r-s1-gates2", st.Question.Key)
		require.NoError(t, st.Answer("accept", "m", FromSpec, ""))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "gates:r-s1-gates3", st.Question.Key, "no fixer before the question")
		assert.Equal(t, KindGateFailure, st.Question.Kind)
		assert.Contains(t, st.Question.Text, "accepted at r-s1-gates2")
		assert.Equal(t, []string{"/logs/npm run lint"}, st.Question.Paths)
		assert.NotContains(t, f.submits(), "agent r-s1-fix3 intermediate-dev")
		drive(t, st, f, map[string]string{"gates:r-s1-gates3": "accept"})
		assert.Equal(t, Done, st.Status)
		assert.NotContains(t, f.submits(), "agent r-s1-fix3 intermediate-dev")
	})
	t.Run("failing on a step not accepted gets a fixer", func(t *testing.T) {
		st, f := setup(GateResult{Ended: "failed", Failing: []GateStep{step("npm run lint"), step("go test ./...")}, Table: "FAIL npm run lint\nFAIL go test"})
		drive(t, st, f, map[string]string{"gates:r-s1-gates2": "accept"}) // gates3 asking fails the test
		assert.Equal(t, Done, st.Status)
		assert.Contains(t, f.submits(), "agent r-s1-fix3 intermediate-dev")
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
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, KindAgentFailed, st.Question.Kind)
	})
}

// A plan edit the manager makes while the run is parked moves the stories'
// ranges (State.Restory), the next briefs carry the new ones, and the
// premises, flagged against the base's plan, keep their stories (feedback
// thread 14).
func TestRun_RestoriedRangesReachTheNextBriefs(t *testing.T) {
	f := newFake()
	f.premises = "plan_premises: p.md against the tree at h0\nStory 3 (plan:300): 1 premises, 1 flagged\n  plan:310  missing  a.go: no such file\n"
	f.reports["r-premise1"] = Report{Status: "DONE", Text: "plan:310 | MOVED | COSMETIC | a2.go:1 | use a2.go\n"}
	f.reports["r-s2-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which?"}
	st := newState(story("1", "intermediate-dev", "vouched"), story("2", "intermediate-dev", "vouched"), story("3", "intermediate-dev", "vouched"))
	require.NoError(t, runSettled(st, f))
	require.Equal(t, "ctx:r-s2-dev1:1", st.Question.Key)
	assert.Contains(t, f.briefs["r-s1-review1"], "plan lines 100-199")

	fresh := []Story{story("1", "intermediate-dev", "vouched"), story("2", "intermediate-dev", "vouched"), story("3", "intermediate-dev", "vouched")}
	fresh[1].End += 34
	fresh[2].Line, fresh[2].End = fresh[2].Line+34, fresh[2].End+34
	_, err := st.Restory(fresh)
	require.NoError(t, err)
	require.NoError(t, st.Answer("That one.", "m", FromSpec, ""))
	drive(t, st, f, nil)
	assert.Equal(t, Done, st.Status)
	assert.Contains(t, f.briefs["r-s2-review1"], "plan lines 200-333")
	assert.Contains(t, f.briefs["r-s3-dev1"], "plan lines 334-433")
	assert.Contains(t, f.briefs["r-s3-dev1"], "use a2.go", "plan:310 was Story 3's at the base")
	assert.Empty(t, st.Stories[1].Row.Corrections)
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

// The plan question names the reports its PLAN lines came from, and says
// what Minors the story's reviews kept for the final round, so a manager
// doesn't read them as lost (feedback thread 18).
func TestRun_ThePlanQuestionNamesItsReports(t *testing.T) {
	t.Run("one review, and the Minor it kept", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Minor a.go:3 tidy it\nPLAN: its loop never ends\nVERDICT: APPROVED\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		q := st.Question
		assert.Equal(t, "plan:1", q.Key)
		assert.Equal(t, []string{"/reports/r-s1-review1"}, q.Paths)
		assert.Contains(t, q.Text, "\n- its loop never ends")
		assert.Contains(t, q.Text, "1 Minor finding, which the final round fixes")
	})
	t.Run("two Minors kept", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Minor a.go:3 tidy it\nFINDING: Minor b.go:4 and this\nPLAN: its loop never ends\nVERDICT: APPROVED\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Contains(t, st.Question.Text, "They also kept 2 Minor findings, which the final round fixes; their reports have the FINDING lines.")
	})
	t.Run("a fix round's re-review adds its report", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 guard it\nPLAN: its loop never ends\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		f.reports["r-s1-review2"] = Report{Status: "DONE", Text: "PLAN: another one\nVERDICT: APPROVED\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		q := st.Question
		assert.Equal(t, "plan:1", q.Key)
		assert.Equal(t, []string{"/reports/r-s1-review1", "/reports/r-s1-review2"}, q.Paths)
		assert.Contains(t, q.Text, "\n- its loop never ends\n- another one")
		assert.NotContains(t, q.Text, "Minor", "the Critical's review kept nothing: its Minors go to the fix round")
	})
	t.Run("a re-review with no PLAN line adds no report", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 guard it\nPLAN: its loop never ends\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, []string{"/reports/r-s1-review1"}, st.Question.Paths)
	})
}

// An unresolved question lists the Critical and Important findings it asks
// about; its report has the rest (feedback thread 18).
func TestRun_AnUnresolvedQuestionListsItsFindings(t *testing.T) {
	critical := Report{Status: "DONE", Text: "FINDING: Critical a.go:3 guard it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	still := Report{Status: "DONE", Text: "FINDING: Important b.go:4 still races\nFINDING: Critical design c.go:9 pick one owner\nFINDING: Minor d.go:1 tidy it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	for _, c := range []struct{ name, first, second, key string }{
		{"a story's", "r-s1-review1", "r-s1-review2", "unresolved:s1:1"},
		{"the final round's", "r-final-review1", "r-final-review2", "unresolved:final:1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			f.reports[c.first], f.reports[c.second] = critical, still
			st := newState(story("1", "intermediate-dev", "vouched"))
			require.NoError(t, runSettled(st, f))
			require.Equal(t, Parked, st.Status)
			q := st.Question
			require.Equal(t, c.key, q.Key)
			assert.Equal(t, []string{"/reports/" + c.second}, q.Paths)
			assert.Contains(t, q.Text, "still finds Critical or Important issues:\n- Important b.go:4 still races\n- Critical (design) c.go:9 pick one owner")
			assert.NotContains(t, q.Text, "tidy it", "a Minor isn't what it asks about")
		})
	}
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
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, KindPremiseDesign, st.Question.Kind)
		assert.Equal(t, []string{"agent r-premise1 premise-check"}, f.submits())
		require.NoError(t, st.Answer("Use the new type.", "m", FromSpec, ""))
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
			require.NoError(t, runSettled(st, f))
			require.Equal(t, Parked, st.Status)
			assert.Equal(t, Question{Key: "tree:r-s1-dev1", Kind: KindTreeMoved, Story: "1", At: "h0",
				Text: "HEAD is h3, and the engine last recorded h0, before it starts r-s1-dev1"}, *st.Question)
			require.NoError(t, st.Answer(c.answer, "m", FromSpec, ""))
			drive(t, st, f, nil)
			assert.Equal(t, c.status, st.Status)
		})
	}
	t.Run("a dirty tree", func(t *testing.T) {
		f := newFake()
		f.dirty = true
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Contains(t, st.Question.Text, "uncommitted changes")
	})
}

// An answer can carry the manager's own commits to the spec and plan, so
// the next dispatch doesn't park on them as tree-moved (the first real
// runs, 2026-11-06, parked four times so).
func TestRun_AnAnswerTakesTheManagersCommits(t *testing.T) {
	parked := func(t *testing.T) (*State, *fakeFX) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Important design a.go:3 fix\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		require.Equal(t, KindDesignFinding, st.Question.Kind)
		assert.Equal(t, "h1", st.Question.At, "the head the engine recorded when it parked")
		f.head++ // the manager commits its decision to the spec
		return st, f
	}
	t.Run("an answer with the manager's head", func(t *testing.T) {
		st, f := parked(t)
		require.NoError(t, st.Answer("Fix it so.", "m", FromSpec, "h2"))
		drive(t, st, f, nil) // a tree-moved question fails the test
		require.Equal(t, Done, st.Status)
		assert.Contains(t, f.submits(), "agent r-s1-fix1 intermediate-dev")
		assert.Contains(t, Record(st, RecordInputs{}), "Answered from the spec: Fix it so. (the run took HEAD h2 with it)")
	})
	t.Run("an answer without it", func(t *testing.T) {
		st, f := parked(t)
		require.NoError(t, st.Answer("Fix it so.", "m", FromSpec, ""))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, KindTreeMoved, st.Question.Kind)
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

func TestRun_FinalRoundBatchesWholeFiles(t *testing.T) {
	f := newFake()
	// Ten findings: a.go has two, b.go one, then seven files of one each.
	text := "FINDING: Important a.go:1 one\nFINDING: Minor b.go:2 two\nFINDING: Important a.go:9 three\n"
	for i := range 7 {
		text += fmt.Sprintf("FINDING: Minor f%d.go:1 tidy %d\n", i, i)
	}
	f.reports["r-final-review1"] = Report{Status: "DONE", Text: text + "VERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, nil)
	first, second := f.briefs["r-final-fix1"], f.briefs["r-final-fix2"]
	for _, in := range []string{"a.go:1 one", "a.go:9 three", "b.go:2 two", "f0.go:1 tidy 0", "f4.go:1 tidy 4"} {
		assert.Contains(t, first, in, "one developer takes files until it holds eight findings")
	}
	assert.NotContains(t, first, "f5.go")
	assert.Contains(t, second, "f5.go:1 tidy 5")
	assert.Contains(t, second, "f6.go:1 tidy 6")
	assert.NotContains(t, f.calls, "agent r-final-fix3 intermediate-dev", "ten findings are two developers, not nine")
	assert.Contains(t, f.calls, "gates r-final-gates2", "the round is gated")
	assert.Contains(t, f.calls, "agent r-final-review2 story-reviewer", "and re-reviewed")
}

func TestFixBatches(t *testing.T) {
	at := func(file string, n int) []Finding {
		out := make([]Finding, n)
		for i := range out {
			out[i] = Finding{File: file, Line: i + 1}
		}
		return out
	}
	files := func(batches [][]Finding) [][]string {
		var out [][]string
		for _, b := range batches {
			var names []string
			for _, f := range b {
				names = append(names, f.File)
			}
			out = append(out, names)
		}
		return out
	}
	assert.Empty(t, fixBatches(nil, 3))
	// A file's findings stay together, in first-appearance order, and a
	// batch closes before the file that would take it past the limit.
	mixed := []Finding{{File: "a"}, {File: "b"}, {File: "a"}, {File: "c"}, {File: "c"}, {File: "d"}}
	assert.Equal(t, [][]string{{"a", "a", "b"}, {"c", "c", "d"}}, files(fixBatches(mixed, 3)))
	// A file over the limit is a batch of its own, never split.
	assert.Equal(t, [][]string{{"a"}, {"big", "big", "big", "big"}, {"z"}}, files(fixBatches(slices.Concat(at("a", 1), at("big", 4), at("z", 1)), 3)))
	// Exactly the limit fits.
	assert.Len(t, fixBatches(slices.Concat(at("a", 2), at("b", 1)), 3), 1)
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

// docState is a run with the doc pass (the doc pass's spec, 2026-11-10).
func docState(stories ...Story) *State {
	st := newState(stories...)
	st.Docs, st.Blocks.DocWriter = true, "Doc paths (change only files these match): `*.md`"
	return st
}

func TestRun_TheDocPass(t *testing.T) {
	t.Run("a pass with commits is checked, then gated", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "DOC: README.md the flag\n**DOC:** USAGE.md its key\nLESSON: docs/claude/x.md a trap\nSTATUS: DONE"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Equal(t, []string{
			"agent r-s1-dev1 intermediate-dev", "services", "gates r-s1-gates1", "agent r-s1-review1 story-reviewer",
			"agent r-s1-docs1 doc-writer", "docs-check h1..h2", "services", "gates r-s1-gates2",
			"services", "gates r-final-gates1", "agent r-final-review1 code-quality-reviewer",
		}, f.calls)
		assert.Equal(t, Row{Status: "done", Developer: "intermediate-dev", Base: "h0", Head: "h2", Commits: 2, Docs: 2, Lessons: 1}, st.Stories[0].Row)
		brief := f.briefs["r-s1-docs1"]
		assert.Contains(t, brief, "Update the docs for Story 1 of the plan p.md: its section is plan lines 100-199. The spec is s.md.")
		assert.Contains(t, brief, "The story landed as h0..h1")
		assert.Contains(t, brief, "/reports/r-s1-dev1")
		assert.Contains(t, brief, "Its reviews sent no Critical or Important finding to a fix round.")
		assert.Contains(t, brief, "[doc-writer]\nDoc paths")
	})
	t.Run("a pass with no commits runs neither", func(t *testing.T) {
		f := newFake()
		f.noCommit["r-s1-docs1"] = true
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.NotContains(t, strings.Join(f.calls, "\n"), "docs-check")
		assert.NotContains(t, f.calls, "gates r-s1-gates2")
		assert.Contains(t, st.Notes, "Story 1: the doc pass changed no docs")
		assert.Equal(t, "h1", st.Stories[0].Row.Head)
	})
	t.Run("the findings its fix rounds took reach its brief", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 guard it\nFINDING: Minor a.go:5 tidy\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		brief := f.briefs["r-s1-docs1"]
		assert.Contains(t, brief, "- Critical a.go:3 guard it (/reports/r-s1-review1)")
		assert.NotContains(t, brief, "tidy", "a Minor is no lesson")
		assert.Contains(t, brief, "The story landed as h0..h2", "after its fix round")
	})
	t.Run("a stray asks, and accept gates it", func(t *testing.T) {
		f := newFake()
		f.strays["h2"] = []string{"main.go", "go.sum"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, Question{Key: "strays:r-s1-docs1", Kind: KindDocsStrayed, Story: "1", At: "h2",
			Text: "r-s1-docs1 changed paths that aren't doc paths:\n- main.go\n- go.sum", Paths: []string{"/reports/r-s1-docs1"}}, *st.Question)
		require.NoError(t, st.Answer("accept", "m", FromSpec, ""))
		drive(t, st, f, nil)
		assert.Contains(t, f.submits(), "gates r-s1-gates2")
		assert.Contains(t, st.Notes, "Story 1: the manager kept the doc pass's changes to main.go, go.sum")
	})
	t.Run("a decision on a stray runs a fresh pass to undo it", func(t *testing.T) {
		f := newFake()
		f.strays["h2"] = []string{"main.go"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"strays:r-s1-docs1": "Keep the docs, undo the code."})
		brief := f.briefs["r-s1-docs2"]
		assert.Contains(t, brief, "/reports/r-s1-docs1")
		assert.Contains(t, brief, "Keep the docs, undo the code.")
		assert.Contains(t, brief, "undo its changes to them with inverse edits, and commit that:\n- main.go")
		assert.Contains(t, f.calls, "docs-check h1..h3", "over the whole pass")
		assert.Contains(t, f.submits(), "gates r-s1-gates2")
	})
	t.Run("a finding joins the kept ones for the final round", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "FINDING: Important design a.go:7 the spec says three retries\nSTATUS: DONE"}
		f.noCommit["r-s1-docs1"] = true
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Contains(t, f.briefs["r-final-fix1"], "Important a.go:7 the spec says three retries")
		assert.NotContains(t, f.briefs["r-final-fix1"], "(design)", "the spec already decided it")
		assert.Contains(t, st.Notes, "Story 1: the doc pass kept 1 finding for the final round")
	})
	t.Run("a FINDING it can't read fails the pass, and its retry goes on from its commits", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "FINDING: Severe a.go:7 no\nSTATUS: DONE"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "failed:r-s1-docs1", st.Question.Key)
		assert.Contains(t, st.Question.Text, "a FINDING line it can't read")
		require.NoError(t, st.Answer("retry", "m", FromSpec, ""))
		drive(t, st, f, nil) // a tree-moved question fails the test
		assert.Contains(t, f.briefs["r-s1-docs2"], "/reports/r-s1-docs1")
		assert.Contains(t, f.calls, "docs-check h1..h3")
	})
	t.Run("a pass that commits and hits its turn cap is retried from its commits", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "TURN_CAP"}
		f.commitsOn["r-s1-docs1"] = true
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-docs1": "retry"}) // a tree-moved question fails the test
		assert.Contains(t, f.briefs["r-s1-docs2"], "/reports/r-s1-docs1")
		assert.Contains(t, f.calls, "docs-check h1..h3")
	})
	t.Run("a retry's brief names every earlier attempt", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "FINDING: Severe a.go:7 no\nSTATUS: DONE"}
		f.strays["h3"] = []string{"main.go"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"failed:r-s1-docs1": "retry", "strays:r-s1-docs2": "Undo it."})
		brief := f.briefs["r-s1-docs3"]
		assert.Contains(t, brief, "/reports/r-s1-docs1")
		assert.Contains(t, brief, "/reports/r-s1-docs2")
		assert.Contains(t, f.calls, "docs-check h1..h4")
	})
	t.Run("a run without Docs has no pass", func(t *testing.T) {
		f := newFake()
		st := newState(story("1", "intermediate-dev", "vouched"))
		st.Blocks.DocWriter = "Doc paths: `*.md`"
		drive(t, st, f, nil)
		assert.NotContains(t, strings.Join(f.calls, "\n"), "doc-writer")
		assert.Contains(t, Record(st, RecordInputs{}), "| no | — | — |")
	})
}

// script is a two-story run with a question, a fix round, a kept Minor and
// doc passes: Story 1's keeps a finding, and Story 2's stray is accepted.
func script() (*State, *fakeFX, map[string]string) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which?"}
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 fix\nFINDING: Minor a.go:5 tidy\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "DOC: README.md the flag\nLESSON: docs/claude/x.md a trap\nFINDING: Important b.go:2 the spec says two\nSTATUS: DONE"}
	f.reports["r-s2-dev1"] = Report{Status: "BLOCKED"}
	f.gates["r-s2-gates1"] = GateResult{Ended: "failed", Free: 9 << 30, Failing: []GateStep{
		{Line: "go vet", Verdict: "FAIL"},
		{Line: "bash -c 'go   test ./... && " + strings.Repeat("x", 120) + "'", Verdict: "FAIL (killed)"},
	}}
	f.gates["r-final-gates2"] = GateResult{Ended: "passed", Free: 120 << 30}
	f.reports["r-s2-review1"] = Report{Status: "failed", Why: "it ended with no result"}
	f.strays["h6"] = []string{"main.go"}
	st := newState(story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"))
	st.Docs, st.Blocks.DocWriter = true, "Doc paths (change only files these match): `*.md`"
	return st, f, map[string]string{"ctx:r-s1-dev1:1": "That one.", "failed:r-s2-review1": "Read the conformance first.", "strays:r-s2-docs1": "accept"}
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
			err := runSettled(st, f)
			for err != nil || st.Status == Parked {
				if err != nil {
					require.ErrorIs(t, err, errCrash)
					st = clone(f.saved) // the engine restarts from the file
					if f.saved == nil {
						st, _, _ = script()
					}
				} else {
					require.NoError(t, st.Answer(answers[st.Question.Key], "m", FromSpec, ""))
				}
				err = runSettled(st, f)
			}
			require.Equal(t, Done, st.Status)
			assert.Contains(t, f.briefs["r-s2-review2"], "Read the conformance first.", "a retry's decision survives the restart")
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
	require.NoError(t, runSettled(st, f)) // a pass over a finished run replays every step
	assert.Equal(t, once.Stories, st.Stories)
	assert.Equal(t, once.Notes, st.Notes)
	assert.Equal(t, once.Kept, st.Kept)
}

// limitAt is a usage-limit report of window, hit at t0 and resetting after
// in; in 0 leaves the reset unknown.
func limitAt(window string, in time.Duration) Report {
	t0 := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	l := &Limit{Window: window, At: t0}
	if in > 0 {
		l.Resets = t0.Add(in)
	}
	return Report{Status: AgentUsageLimit, Why: "it stopped on a usage limit", Limit: l}
}

// resumesOf is f's resume calls of name.
func (f *fakeFX) resumesOf(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "resume "+name+" ") {
			out = append(out, c)
		}
	}
	return out
}

func TestRun_AUsageLimit(t *testing.T) {
	t.Run("a reset within six hours retries with no question", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = limitAt("five_hour", 2*time.Hour)
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Equal(t, Done, st.Status)
		require.Len(t, f.resumesOf("r-s1-dev1"), 1, "the same run is resumed, and the queue holds it")
		assert.NotContains(t, f.submits(), "agent r-s1-dev2 intermediate-dev", "no fresh attempt")
		l := f.reports["r-s1-dev1"].Limit
		assert.Contains(t, st.Notes, "r-s1-dev1: usage limit (5-hour), waiting for the reset at "+sched.ResetText(l.Resets, l.At))
		assert.Empty(t, st.Answers, "nothing was asked")
	})
	t.Run("exactly six hours still waits", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = limitAt("five_hour", LimitWaitWithin)
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Equal(t, Done, st.Status)
	})
	for _, tc := range []struct {
		name, window string
		in           time.Duration
		says         string
	}{
		{"a reset further away parks", "seven_day", 3 * 24 * time.Hour, "7-day limit, resets "},
		{"an unknown reset parks", "", 0, "an unknown time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.reports["r-s1-review1"] = limitAt(tc.window, tc.in)
			st := newState(story("1", "intermediate-dev", "vouched"))
			require.NoError(t, runSettled(st, f))
			require.Equal(t, Parked, st.Status)
			assert.Equal(t, "limit:r-s1-review1:1", st.Question.Key)
			assert.Equal(t, KindUsageLimit, st.Question.Kind)
			assert.Contains(t, st.Question.Text, tc.says)
			assert.Empty(t, f.resumesOf("r-s1-review1"), "nothing is resubmitted before the answer")

			drive(t, st, f, map[string]string{"limit:r-s1-review1:1": "wait"})
			assert.Equal(t, Done, st.Status)
			assert.Len(t, f.resumesOf("r-s1-review1"), 1, "wait resubmits")
		})
	}
	t.Run("stop stops", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = limitAt("seven_day", 3*24*time.Hour)
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"limit:r-s1-dev1:1": "stop"})
		assert.Equal(t, Stopped, st.Status)
		assert.Empty(t, f.resumesOf("r-s1-dev1"))
	})
	t.Run("the same reset again asks: the queue did not hold the retry", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = limitAt("five_hour", time.Hour)
		f.resumes["r-s1-dev1"] = []Report{limitAt("five_hour", time.Hour)}
		st := newState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "limit:r-s1-dev1:2", st.Question.Key)
		assert.Contains(t, st.Question.Text, "again before that reset")
		drive(t, st, f, map[string]string{"limit:r-s1-dev1:2": "wait"})
		assert.Equal(t, Done, st.Status)
	})
	t.Run("a limit on a resume, twice, retries each time", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which?"}
		f.resumes["r-s1-dev1"] = []Report{limitAt("five_hour", time.Hour), limitAt("five_hour", 5*time.Hour)}
		st := newState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"ctx:r-s1-dev1:1": "That one."})
		assert.Equal(t, Done, st.Status)
		got := f.resumesOf("r-s1-dev1")
		require.Len(t, got, 3, "the answer, then a retry after each limit")
		assert.NotEqual(t, got[1], got[2], "each retry's text is its own, so a follow never takes one for another")
	})
}

// limitScript is a run whose developer stops on a 5-hour limit, its
// resume on another, and its reviewer on a 7-day one the manager waits for.
func limitScript() (*State, *fakeFX, map[string]string) {
	f := newFake()
	f.reports["r-s1-dev1"] = limitAt("five_hour", time.Hour)
	f.resumes["r-s1-dev1"] = []Report{limitAt("five_hour", 2*time.Hour), {Status: "DONE", Text: "STATUS: DONE"}}
	f.reports["r-s1-review1"] = limitAt("seven_day", 48*time.Hour)
	st := newState(story("1", "intermediate-dev", "vouched"))
	return st, f, map[string]string{"limit:r-s1-review1:1": "wait"}
}

func TestRun_AUsageLimitReplaysTheSameWayAfterARestartAtEverySave(t *testing.T) {
	st, f, answers := limitScript()
	drive(t, st, f, answers)
	require.Equal(t, Done, st.Status)
	want := slices.Concat(f.submits(), f.resumesOf("r-s1-dev1"), f.resumesOf("r-s1-review1"))
	require.Len(t, f.resumesOf("r-s1-dev1"), 2)
	notes := st.Notes
	for k := 1; k <= f.saves; k++ {
		t.Run(fmt.Sprintf("a crash at save %d", k), func(t *testing.T) {
			st, f, answers := limitScript()
			f.failAt = k
			err := runSettled(st, f)
			for err != nil || st.Status == Parked {
				if err != nil {
					require.ErrorIs(t, err, errCrash)
					st = clone(f.saved)
					if f.saved == nil {
						st, _, _ = limitScript()
					}
				} else {
					require.NoError(t, st.Answer(answers[st.Question.Key], "m", FromSpec, ""))
				}
				err = runSettled(st, f)
			}
			require.Equal(t, Done, st.Status)
			assert.Equal(t, want, slices.Concat(f.submits(), f.resumesOf("r-s1-dev1"), f.resumesOf("r-s1-review1")), "nothing submitted or resumed twice")
			assert.Equal(t, notes, st.Notes)
		})
	}
}
