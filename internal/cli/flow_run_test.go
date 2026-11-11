package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunClaude is a claude for the run engine's tests (flow in loom part
// 3b): it logs its argv and environment under $FAKE_CLAUDE_LOG and answers
// by its prompt. A developer (Build Story, Fix) commits in its cwd before
// it reports DONE, Story 2's developer first asks a question, Story 3's
// developer ends with no result (no report), any other story's developer
// commits and reports DONE, Story 1's
// first review finds a Critical, any other review approves; Story 1's doc
// writer commits a doc, any other doc writer changes nothing; with
// $FAKE_SLOW set, Story 1's developer takes a few seconds.
const fakeRunClaude = `#!/bin/sh
n=$(ls "$FAKE_CLAUDE_LOG" | grep -c '\.args$')
printf '%s\n' "$@" > "$FAKE_CLAUDE_LOG/$n.args"
env > "$FAKE_CLAUDE_LOG/$n.env"
prompt=""; resume=""
while [ $# -gt 0 ]; do
  case "$1" in
    -p) prompt="$2"; shift 2;;
    --resume) resume="$2"; shift 2;;
    *) shift;;
  esac
done
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-'$n'","model":"claude-fake"}'
result() { printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"total_cost_usd":0.01,"session_id":"sess-'$n'","result":"'"$1"'"}'; }
commit() { git -c user.name=f -c user.email=f@f commit -q --allow-empty -m "$1" >/dev/null 2>&1; }
if [ -n "$resume" ]; then commit resumed; result 'resumed\nSTATUS: DONE'; exit 0; fi
case "$prompt" in
  *"Update the docs for Story 1 "*) commit docs; result 'DOC: README.md the new flag\nSTATUS: DONE';;
  *"Update the docs for Story"*) result 'nothing to change\nSTATUS: DONE';;
  *"Build Story 1"*) [ -n "$FAKE_SLOW" ] && sleep 4; commit s1; result 'built\nDEVIATION: renamed x\nSTATUS: DONE';;
  *"Build Story 2"*) result 'need one\nSTATUS: NEEDS_CONTEXT\nQUESTION: Which colour?';;
  *"Build Story 3"*) ;;
  *"Build Story"*) commit dev; result 'built\nSTATUS: DONE';;
  *"Fix "*) commit fix; result 'fixed\nSTATUS: DONE';;
  *"Review Story 1"*) result 'FINDING: Critical a.go:3 guard it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE';;
  *) result 'VERDICT: APPROVED\nSTATUS: DONE';;
esac
`

// fakeRunTools is runTools with a fixed plan of stories, whose check-plan
// fails with fails when it is set.
type fakeRunTools struct {
	stories   []flowrun.Story
	fails     string
	generated string // the supervisor block's Generated line's list, when set
}

func (f fakeRunTools) Doctor() (string, bool, error) { return "OK: everything", true, nil }
func (f fakeRunTools) CheckPlan(string) ([]byte, string, error) {
	if f.fails != "" {
		return nil, f.fails, nil
	}
	b, err := json.Marshal(map[string]any{"spec": "docs/spec.md", "stories": f.stories})
	return b, "", err
}
func (f fakeRunTools) Show() (string, error) {
	generated := ""
	if f.generated != "" {
		generated = "Generated files: " + f.generated + "\n"
	}
	return "[supervisor]\nProfile: /repo/.claude/flow.toml sha256:abcdefabcdef\nRecord copy in the repo: none\n" + generated + "\n" +
		"[developer]\nPrefix: none\n\n[verifier]\nServices: none\n\n[reviewer]\nPrefix: none\n\n[doc-writer]\nPrefix for every check command: none\n", nil
}
func (f fakeRunTools) Services() ([]string, error)             { return nil, nil }
func (f fakeRunTools) Key() (string, error)                    { return "-the-repo", nil }
func (f fakeRunTools) Premises(string, string) (string, error) { return "", nil }
func (f fakeRunTools) Conformance(_, story, _, _ string) (string, error) {
	return "conformance of " + story + "\n", nil
}
func (f fakeRunTools) DocsCheck(string, string) ([]string, error) { return nil, nil }
func (f fakeRunTools) Costs(run string) (string, error) {
	return "COST of " + run + "\n<!-- agent-costs {} -->\n<!-- agent-costs-managers {\"m1\": [\"claude-fake\"]} -->", nil
}

func runStory(id, developer string) flowrun.Story {
	return flowrun.Story{ID: id, Line: 10, End: 20, Developer: developer, Tier: "deep", Review: "vouched", Verify: "true"}
}

// runFixture is agentFixture with the run engine's fakes: the roster's
// agents, a fake claude that commits, the stories' tools, gates of `true`,
// a git repo as the working directory, and an engine job that exits at
// once (each test runs the engine in-process). It returns the state dir
// and the fake claude's log dir.
func runFixture(t *testing.T, stories ...flowrun.Story) (*testDaemon, string, string) {
	t.Helper()
	d, conf, logDir := agentFixture(t, "junior-dev", "intermediate-dev", "senior-dev", "story-reviewer", "code-quality-reviewer", "premise-check", "doc-writer")
	bin := filepath.Dir(firstOnPath(t, "claude"))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeRunClaude), 0o700))
	conf.gates = []string{"RUN true"}
	oldTools, oldEngine := newRunTools, engineArgv
	newRunTools = func(string) runTools { return fakeRunTools{stories: stories} }
	engineArgv = func(string) ([]string, error) { return []string{"true"}, nil }
	t.Cleanup(func() { newRunTools, engineArgv = oldTools, oldEngine })
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o700))
	for _, name := range []string{"plan.md", "2026-11-06-the-plan.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", name), []byte("# A plan\n\n**Design stage:** 1 session, 20 minutes\n"), 0o600))
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "docs"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base"}} {
		require.NoError(t, exec.Command("git", append([]string{"-C", repo}, args...)...).Run())
	}
	t.Chdir(repo)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "m1")
	t.Setenv(sched.EnvJob, "")
	t.Setenv(envFlowRun, "")
	setPermissionMode(t, "auto")
	return d, conf.state, logDir
}

// setPermissionMode writes mode as the global config's Claude permission
// mode, which a run's headless agents run in.
func setPermissionMode(t *testing.T, mode string) {
	t.Helper()
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.ClaudePermissionMode = &mode
	require.NoError(t, config.SaveConfigTo(cfg, dir))
}

func firstOnPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	require.NoError(t, err)
	return p
}

// runRun runs `loom flow run args…` with stdin.
func runRun(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	return runRunCtx(t, context.Background(), stdin, args...)
}

func runRunCtx(t *testing.T, ctx context.Context, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := newFlowCmd()
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"run"}, args...))
	err := cmd.ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

func TestFlowRun_EndToEnd(t *testing.T) {
	d, state, _ := runFixture(t, runStory("1", "intermediate-dev"), runStory("2", "intermediate-dev"))
	out, _, err := runRun(t, "", "start", "docs/2026-11-06-the-plan.md")
	require.NoError(t, err, out)
	assert.Equal(t, "started 2026-11-06-the-plan, job 1\n", out)
	j := d.job(1)
	assert.Equal(t, sched.KindRun, j.Kind, "the engine is a run job")
	id := "2026-11-06-the-plan"

	out, _, err = runRun(t, "", "engine", id)
	require.NoError(t, err, out)
	out, _, err = runRun(t, "", "wait", id)
	require.NoError(t, err, out)
	assert.Contains(t, out, "parked "+id+": needs-context (story 2)\nWhich colour?\n")
	assert.Contains(t, out, "answer with: an answer (loom flow run answer "+id+" --from spec|owner < answer)")

	out, _, err = runRun(t, "Blue.", "answer", id, "--from", "owner")
	require.NoError(t, err, out)
	assert.Regexp(t, `^answered `+id+`: engine job \d+\n$`, out)

	out, _, err = runRun(t, "", "engine", id)
	require.NoError(t, err, out)
	out, _, err = runRun(t, "", "wait", id)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ended "+id+": done\nstories: 2 of 2\nrecord: ")

	st, err := flowrun.Load(filepath.Join(state, "runs", id+".json"))
	require.NoError(t, err)
	assert.Equal(t, []string{id + "-s1-dev1", id + "-s1-review1", id + "-s1-fix1", id + "-s1-review2", id + "-s1-docs1",
		id + "-s2-dev1", id + "-s2-review1", id + "-s2-docs1", id + "-final-review1"}, st.Agents)
	assert.Equal(t, []string{id + "-s1-gates1", id + "-s1-gates2", id + "-s1-gates3", id + "-s2-gates1", id + "-final-gates1"}, st.Gates,
		"the fix and the doc pass were gated again")
	assert.True(t, st.Docs, "start switches the doc pass on when the profile prints its block")
	assert.Equal(t, []string{"m1"}, st.Managers)
	record, err := os.ReadFile(st.Record)
	require.NoError(t, err)
	text := string(record)
	assert.Contains(t, st.Record, filepath.Join("records", "-the-repo", st.Start[:10]+"-the-plan.md"))
	assert.Contains(t, text, "- Plan: docs/2026-11-06-the-plan.md (2 stories)")
	assert.Contains(t, text, "- Design stage: 1 session, 20 minutes")
	assert.Contains(t, text, "- Manager: claude-fake m1")
	assert.Contains(t, text, "COST of "+id)
	assert.Contains(t, text, "| 1 | intermediate-dev | deep | vouched | 3 | done | 1 | 0 | 0 | 1/0/0 | no | 1 | 0 |")
	assert.Contains(t, text, "- Questions: 1; answered from the spec: 0; by the owner: 1.")
	assert.Contains(t, text, "- Asked (needs-context, Story 2): Which colour?\n  Answered by the owner: Blue.")
	require.Len(t, st.Answers, 1)
	assert.Equal(t, flowrun.FromOwner, st.Answers[0].From)
}

// gitIn runs git args in dir, failing the test on an error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

// The manager's commit to the plan while it answers rides with the answer,
// so the next dispatch doesn't park on it as tree-moved (2026-11-06).
func TestFlowRun_AnAnswerTakesTheManagersPlanCommit(t *testing.T) {
	_, _, _ = runFixture(t, runStory("1", "intermediate-dev"), runStory("2", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/2026-11-06-the-plan.md")
	require.NoError(t, err)
	id := "2026-11-06-the-plan"
	_, _, err = runRun(t, "", "engine", id)
	require.NoError(t, err)
	out, _, err := runRun(t, "", "wait", id)
	require.NoError(t, err, out)
	require.Contains(t, out, "parked "+id+": needs-context (story 2)")

	require.NoError(t, os.WriteFile(filepath.Join("docs", "2026-11-06-the-plan.md"), []byte("# A plan, amended\n"), 0o600))
	gitIn(t, ".", "commit", "-q", "-am", "docs: the manager's answer")
	head := gitIn(t, ".", "rev-parse", "HEAD")
	out, _, err = runRun(t, "Blue.", "answer", id, "--from", "spec")
	require.NoError(t, err, out)
	assert.Contains(t, out, "the run takes HEAD "+head[:9]+" with the answer")

	_, _, err = runRun(t, "", "engine", id)
	require.NoError(t, err)
	out, _, err = runRun(t, "", "wait", id)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ended "+id+": done", "no tree-moved question")
}

// An agent that wrote no report parks with its job's output.log to read,
// and the manager's decision reaches the retried agent's brief (feedback
// thread 12).
func TestFlowRun_AFailedAgentPointsAtItsLogAndRetriesWithTheDecision(t *testing.T) {
	_, state, logDir := runFixture(t, runStory("3", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	st, err := flowrun.Load(filepath.Join(state, "runs", "plan.json"))
	require.NoError(t, err)
	require.Equal(t, flowrun.Parked, st.Status)
	require.Equal(t, flowrun.KindAgentFailed, st.Question.Kind)
	require.Len(t, st.Question.Paths, 1)
	assert.Equal(t, "output.log", filepath.Base(st.Question.Paths[0]), "no report.md was written")
	assert.FileExists(t, st.Question.Paths[0])

	out, _, err := runRun(t, "Merge main first.", "answer", "plan", "--from", "owner")
	require.NoError(t, err, out)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	args, err := os.ReadFile(filepath.Join(logDir, "1.args"))
	require.NoError(t, err)
	assert.Contains(t, string(args), "Merge main first.", "the retried developer's brief")
}

// planEditFixture is a run of Stories 1, 2 and 4 parked on Story 2's
// question, whose check-plan the test changes through the tools it returns.
func planEditFixture(t *testing.T) (*fakeRunTools, string) {
	t.Helper()
	stories := []flowrun.Story{runStory("1", "intermediate-dev"), runStory("2", "intermediate-dev"), runStory("4", "intermediate-dev")}
	stories[1].Line, stories[1].End, stories[2].Line, stories[2].End = 30, 40, 50, 60
	_, state, _ := runFixture(t, stories...)
	tools := &fakeRunTools{stories: stories}
	newRunTools = func(string) runTools { return *tools } // runFixture's cleanup restores it
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	st, err := flowrun.Load(filepath.Join(state, "runs", "plan.json"))
	require.NoError(t, err)
	require.Equal(t, "needs-context", st.Question.Kind)
	return tools, filepath.Join(state, "runs", "plan.json")
}

// editPlan commits an edit to the plan, and to code.go too when code is set.
func editPlan(t *testing.T, code bool) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join("docs", "plan.md"), []byte("# A plan, amended\n"), 0o600))
	gitIn(t, ".", "add", "docs/plan.md")
	if code {
		require.NoError(t, os.WriteFile("code.go", []byte("package x\n"), 0o600))
		gitIn(t, ".", "add", "code.go")
	}
	gitIn(t, ".", "commit", "-q", "-m", "docs: the manager's answer")
}

func storyRanges(st *flowrun.State) [][2]int {
	var out [][2]int
	for _, s := range st.Stories {
		out = append(out, [2]int{s.Line, s.End})
	}
	return out
}

// A plan edit inside Story 2, taken with the answer, moves Story 2's range
// and Story 4's, and the next briefs carry them (feedback thread 14).
func TestFlowRun_AnAnswerRereadsTheStoriesOfAnEditedPlan(t *testing.T) {
	tools, path := planEditFixture(t)
	logDir := os.Getenv("FAKE_CLAUDE_LOG")
	editPlan(t, false)
	tools.stories[1].End, tools.stories[2].Line, tools.stories[2].End = 45, 55, 65
	out, _, err := runRun(t, "Blue.", "answer", "plan", "--from", "spec")
	require.NoError(t, err, out)
	assert.Contains(t, out, "the plan changed since the question: its stories' plan lines are now Story 1 10-20, Story 2 30-45, Story 4 55-65\n")
	st, err := flowrun.Load(path)
	require.NoError(t, err)
	assert.Equal(t, [][2]int{{10, 20}, {30, 45}, {55, 65}}, storyRanges(st))

	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	st, err = flowrun.Load(path)
	require.NoError(t, err)
	require.Equal(t, flowrun.Done, st.Status)
	prompts := map[string]string{}
	argFiles, err := filepath.Glob(filepath.Join(logDir, "*.args"))
	require.NoError(t, err)
	for _, f := range argFiles {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, who := range []string{"Review Story 2", "Build Story 4"} {
			if strings.Contains(string(b), who) {
				prompts[who] = string(b)
			}
		}
	}
	assert.Contains(t, prompts["Review Story 2"], "plan lines 30-45")
	assert.Contains(t, prompts["Build Story 4"], "plan lines 55-65")
}

// A plan edit check-plan fails on, or one that changes the stories, is
// refused, and the run stays parked as it was; a tree-moved continue
// re-reads the stories too.
func TestFlowRun_AnAnswerRefusesAPlanItCantReread(t *testing.T) {
	tools, path := planEditFixture(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	editPlan(t, false)

	tools.stories = tools.stories[:2]
	out, _, err := runRun(t, "Blue.", "answer", "plan", "--from", "spec")
	assert.Equal(t, exitUsage, exitCode(t, err), out)
	assert.ErrorContains(t, err, "the plan's stories are now stories 1, 2, and the run's are stories 1, 2, 4")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "nothing saved")

	tools.stories = []flowrun.Story{runStory("1", "intermediate-dev"), runStory("2", "intermediate-dev"), runStory("4", "intermediate-dev")}
	tools.fails = "FAIL: Story 4 has no Verify line"
	_, _, err = runRun(t, "Blue.", "answer", "plan", "--from", "spec")
	assert.Equal(t, exitUsage, exitCode(t, err))
	assert.ErrorContains(t, err, "FAIL: Story 4 has no Verify line")
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "nothing saved")

	tools.fails = ""
	st, err := flowrun.Load(path)
	require.NoError(t, err)
	st.Question = &flowrun.Question{Key: "tree:plan-s4-dev1", Kind: flowrun.KindTreeMoved, Text: "moved", At: st.Question.At}
	require.NoError(t, flowrun.Save(path, st))
	editPlan(t, true) // a code commit too, which the answer doesn't take
	tools.stories[1].Line, tools.stories[1].End, tools.stories[2].Line, tools.stories[2].End = 30, 41, 51, 61
	out, _, err = runRun(t, "continue", "answer", "plan", "--from", "spec")
	require.NoError(t, err, out)
	st, err = flowrun.Load(path)
	require.NoError(t, err)
	assert.Equal(t, [][2]int{{10, 20}, {30, 41}, {51, 61}}, storyRanges(st))
}

func TestTakenHead(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	for _, f := range []string{"plan.md", "spec.md", "code.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, f), []byte("v1\n"), 0o600))
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	at := gitIn(t, repo, "rev-parse", "HEAD")
	st := &flowrun.State{Plan: "plan.md", Spec: "spec.md", Question: &flowrun.Question{At: at}}
	fx := &runFX{cwd: repo}
	edit := func(f string) {
		require.NoError(t, os.WriteFile(filepath.Join(repo, f), []byte("v2\n"), 0o600))
		gitIn(t, repo, "commit", "-q", "-am", "edit "+f)
	}

	assert.Empty(t, takenHead(fx, st), "HEAD where the engine left it")
	edit("spec.md")
	edit("plan.md")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	assert.Equal(t, head, takenHead(fx, st), "the manager's spec and plan commits")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "code.go"), []byte("dirty\n"), 0o600))
	assert.Empty(t, takenHead(fx, st), "a dirty tree")
	gitIn(t, repo, "checkout", "-q", "code.go")

	edit("code.go")
	assert.Empty(t, takenHead(fx, st), "a commit beyond the spec and plan")

	gitIn(t, repo, "reset", "-q", "--hard", at)
	gitIn(t, repo, "commit", "-q", "--amend", "-m", "base, rewritten")
	edit("plan.md")
	assert.Empty(t, takenHead(fx, st), "a HEAD that doesn't descend from the recorded one")

	assert.Empty(t, takenHead(fx, &flowrun.State{Plan: "plan.md", Question: &flowrun.Question{}}), "a question parked before At was recorded")
}

func TestFlowRun_ARestartedEngineFollowsWhatItQueued(t *testing.T) {
	d, state, _ := runFixture(t, runStory("1", "intermediate-dev"))
	t.Setenv("FAKE_SLOW", "1")
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := runRunCtx(t, ctx, "", "engine", "plan")
		done <- err
	}()
	record := filepath.Join(state, "agents", "plan-s1-dev1.json")
	require.Eventually(t, func() bool { _, err := os.Stat(record); return err == nil }, 30*time.Second, 20*time.Millisecond)
	cancel()
	err = <-done
	assert.Equal(t, exitNotRun, exitCode(t, err), "an engine whose wait ends stops, leaving the run running")

	out, _, err := runRun(t, "", "engine", "plan")
	require.NoError(t, err, out)
	n := 0
	for _, j := range d.jobs() {
		if strings.HasPrefix(j.Description, "agent plan-s1-dev1:") {
			n++
		}
	}
	assert.Equal(t, 1, n, "the restarted engine followed the agent job it had queued")
	st, err := flowrun.Load(filepath.Join(state, "runs", "plan.json"))
	require.NoError(t, err)
	assert.Equal(t, flowrun.Done, st.Status)
}

func TestFlowRun_Refusals(t *testing.T) {
	_, state, _ := runFixture(t, runStory("2", "intermediate-dev"))

	_, _, err := runRun(t, "x", "answer", "plan", "--from", "spec")
	assert.Equal(t, exitNotRun, exitCode(t, err), "no run of that name")
	_, _, err = runRun(t, "x", "answer", "plan")
	assert.Equal(t, exitUsage, exitCode(t, err), "an answer without --from")
	_, _, err = runRun(t, "x", "answer", "plan", "--from", "manager")
	assert.Equal(t, exitUsage, exitCode(t, err), "--from takes spec or owner")

	require.NoError(t, os.WriteFile("dirt.txt", []byte("x"), 0o600))
	_, _, err = runRun(t, "", "start", "docs/plan.md")
	assert.Equal(t, exitUsage, exitCode(t, err), "a dirty tree")
	require.NoError(t, os.Remove("dirt.txt"))

	// Headless agents can write only in auto or bypassPermissions (thread 7).
	for _, mode := range []string{"default", "acceptEdits", "plan", "dontAsk", "manual"} {
		setPermissionMode(t, mode)
		_, _, err = runRun(t, "", "start", "docs/plan.md")
		assert.Equal(t, exitUsage, exitCode(t, err), "permission mode %s", mode)
		assert.ErrorContains(t, err, "claude_permission_mode")
		assert.NoFileExists(t, filepath.Join(state, "runs", "plan.json"), "nothing written or queued")
	}
	setPermissionMode(t, "bypassPermissions")
	_, _, err = runRun(t, "", "cancel", "plan")
	assert.Equal(t, exitNotRun, exitCode(t, err), "still no run")
	setPermissionMode(t, "auto")

	_, _, err = runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "start", "docs/plan.md")
	assert.Equal(t, exitUsage, exitCode(t, err), "a run not ended")
	_, _, err = runRun(t, "Blue.", "answer", "plan", "--from", "spec")
	assert.Equal(t, exitUsage, exitCode(t, err), "an answer to a running run")

	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	_, _, err = runRun(t, "accept", "answer", "plan", "--from", "spec")
	assert.Equal(t, exitUsage, exitCode(t, err), "a word a needs-context question doesn't take")
	_, _, err = runRun(t, "", "resume", "plan")
	assert.Equal(t, exitUsage, exitCode(t, err), "resume of a parked run")

	path := filepath.Join(state, "runs", "plan.json")
	st, err := flowrun.Load(path)
	require.NoError(t, err)
	st.Question = &flowrun.Question{Key: "tree:x", Kind: flowrun.KindTreeMoved, Text: "moved"}
	require.NoError(t, flowrun.Save(path, st))
	require.NoError(t, os.WriteFile("dirt.txt", []byte("x"), 0o600))
	_, _, err = runRun(t, "continue", "answer", "plan", "--from", "spec")
	assert.Equal(t, exitUsage, exitCode(t, err), "continue with a dirty tree")
	require.NoError(t, os.Remove("dirt.txt"))

	t.Setenv("LOOM_FLOW_ROOT", "")
	_, _, err = runRun(t, "", "start", "docs/plan.md")
	assert.Equal(t, exitNotRun, exitCode(t, err), "outside a session")
}

func TestFlowRun_CancelEndsTheRunAndWritesItsRecord(t *testing.T) {
	_, state, _ := runFixture(t, runStory("2", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)

	out, _, err := runRun(t, "", "cancel", "plan")
	require.NoError(t, err, out)
	assert.True(t, strings.HasPrefix(out, "cancelled plan\nrecord: "), out)
	st, err := flowrun.Load(filepath.Join(state, "runs", "plan.json"))
	require.NoError(t, err)
	assert.Equal(t, flowrun.Cancelled, st.Status)
	record, err := os.ReadFile(st.Record)
	require.NoError(t, err)
	assert.Contains(t, string(record), "- Ended: cancelled")

	out, _, err = runRun(t, "", "wait", "plan")
	assert.Equal(t, 1, exitCode(t, err), out)
	assert.Contains(t, out, "ended plan: cancelled")
}

func TestFlowRun_LostEngineSaysResume(t *testing.T) {
	runFixture(t, runStory("1", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)

	out, _, err := runRun(t, "", "wait", "plan")
	assert.Equal(t, 1, exitCode(t, err), out)
	assert.Contains(t, out, "lost plan: the engine (job 1) ended done without parking; loom flow run resume plan carries on")

	out, _, err = runRun(t, "", "resume", "plan")
	require.NoError(t, err, out)
	assert.Equal(t, "resumed plan: engine job 2\n", out)
}

func TestFlowRun_TheEngineMarksItsJobs(t *testing.T) {
	_, _, logDir := runFixture(t, runStory("2", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	t.Setenv(sched.EnvJob, "1") // as the shim sets it for the engine's job
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	assert.Equal(t, "1", jobEnv(t, logDir, "0.env")[envFlowRun], "an agent of the run carries the run's job, which a pause spares")
}

func TestPartLine(t *testing.T) {
	assert.Equal(t, "2 of 3", partLine("# P\n\n**Spec:** `s.md`\n**Part:** 2 of 3\n\n## Stories\n"))
	assert.Equal(t, "2 of 3", partLine("**Part:** 2 of 3.\n"))
	assert.Empty(t, partLine("# P\n\n**Spec:** `s.md`\n"))
	assert.Empty(t, partLine("# P\n\n## Story 1: x\n\n**Part:** 2 of 3\n"), "a line inside a story is not the plan's")
}
