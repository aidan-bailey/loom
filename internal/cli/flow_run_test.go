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

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunClaude is a claude for the run engine's tests (flow in loom part
// 3b): it logs its argv and environment under $FAKE_CLAUDE_LOG and answers
// by its prompt. A developer (Build Story, Fix) commits in its cwd before
// it reports DONE, Story 2's developer first asks a question, Story 1's
// first review finds a Critical, any other review approves; with
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
  *"Build Story 1"*) [ -n "$FAKE_SLOW" ] && sleep 4; commit s1; result 'built\nDEVIATION: renamed x\nSTATUS: DONE';;
  *"Build Story 2"*) result 'need one\nSTATUS: NEEDS_CONTEXT\nQUESTION: Which colour?';;
  *"Fix "*) commit fix; result 'fixed\nSTATUS: DONE';;
  *"Review Story 1"*) result 'FINDING: Critical a.go:3 guard it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE';;
  *) result 'VERDICT: APPROVED\nSTATUS: DONE';;
esac
`

// fakeRunTools is runTools with a fixed plan of stories.
type fakeRunTools struct{ stories []flowrun.Story }

func (f fakeRunTools) Doctor() (string, bool, error) { return "OK: everything", true, nil }
func (f fakeRunTools) CheckPlan(string) ([]byte, string, error) {
	b, err := json.Marshal(map[string]any{"spec": "docs/spec.md", "stories": f.stories})
	return b, "", err
}
func (f fakeRunTools) Show() (string, error) {
	return "[supervisor]\nProfile: /repo/.claude/flow.toml sha256:abcdefabcdef\nRecord copy in the repo: none\n\n" +
		"[developer]\nPrefix: none\n\n[verifier]\nServices: none\n\n[reviewer]\nPrefix: none\n", nil
}
func (f fakeRunTools) Services() ([]string, error)             { return nil, nil }
func (f fakeRunTools) Key() (string, error)                    { return "-the-repo", nil }
func (f fakeRunTools) Premises(string, string) (string, error) { return "", nil }
func (f fakeRunTools) Conformance(_, story, _, _ string) (string, error) {
	return "conformance of " + story + "\n", nil
}
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
	d, conf, logDir := agentFixture(t, "junior-dev", "intermediate-dev", "senior-dev", "story-reviewer", "code-quality-reviewer", "premise-check")
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
	return d, conf.state, logDir
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
	assert.Equal(t, []string{id + "-s1-dev1", id + "-s1-review1", id + "-s1-fix1", id + "-s1-review2", id + "-s2-dev1", id + "-s2-review1", id + "-final-review1"}, st.Agents)
	assert.Equal(t, []string{id + "-s1-gates1", id + "-s1-gates2", id + "-s2-gates1", id + "-final-gates1"}, st.Gates, "the fix was gated again")
	assert.Equal(t, []string{"m1"}, st.Managers)
	record, err := os.ReadFile(st.Record)
	require.NoError(t, err)
	text := string(record)
	assert.Contains(t, st.Record, filepath.Join("records", "-the-repo", st.Start[:10]+"-the-plan.md"))
	assert.Contains(t, text, "- Plan: docs/2026-11-06-the-plan.md (2 stories)")
	assert.Contains(t, text, "- Design stage: 1 session, 20 minutes")
	assert.Contains(t, text, "- Manager: claude-fake m1")
	assert.Contains(t, text, "COST of "+id)
	assert.Contains(t, text, "| 1 | intermediate-dev | deep | vouched | 2 | done | 1 | 0 | 0 | 1/0/0 | no |")
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
	env, err := os.ReadFile(filepath.Join(logDir, "0.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "\n"+envFlowRun+"=1\n", "an agent of the run carries the run's job, which a pause spares")
}
