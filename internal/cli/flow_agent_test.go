package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAgentClaude is a claude that writes its argv and environment under
// $FAKE_CLAUDE_LOG and answers in stream-json by its prompt, its report
// ending in status lines as a headless agent's does (spec §2a, 9): ASK →
// NEEDS_CONTEXT; CAPPED → the turn cap's error result; BROKEN → no result; FAILING → an error result; BADSTATUS →
// a status no report has; NOSTATUS → none; MUTE → NEEDS_CONTEXT with no
// question; anything else, or a resume → DONE.
const fakeAgentClaude = `#!/bin/sh
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
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-fake"}'
result() { printf '%s\n' '{"type":"result","subtype":"success","is_error":'"$1"',"num_turns":3,"total_cost_usd":0.25,"session_id":"sess-1","result":"'"$2"'"}'; }
if [ -n "$resume" ]; then result false 'answered\nSTATUS: DONE'; exit 0; fi
case "$prompt" in
  *BROKEN*) exit 0;;
  *FAILING*) result true 'boom'; exit 1;;
  *BADSTATUS*) result false 'x\nSTATUS: MAYBE';;
  *NOSTATUS*) result false 'just words';;
  *MUTE*) result false 'x\nSTATUS: NEEDS_CONTEXT';;
  *CAPPED*) printf '%s\n' '{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":3,"total_cost_usd":0.1,"session_id":"sess-1"}'; exit 1;;
  *ASK*) result false 'need one\n**STATUS: NEEDS_CONTEXT**\nQUESTION: ` + "`Which colour?`" + `';;
  *) result false 'all done\nSTATUS: DONE';;
esac
`

// agentFixture serves the test daemon with a fake claude first on PATH, a
// flow root whose agents each preload flow:habits, and the fake
// flowconf's state dir.
func agentFixture(t *testing.T, agents ...string) (*testDaemon, *fakeConf, string) {
	t.Helper()
	d, conf := flowJobsDaemon(t)
	bin, logDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeAgentClaude), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CLAUDE_LOG", logDir)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := fakeFlowRoot(t, nil)
	for _, a := range agents {
		p := filepath.Join(root, "plugin", "agents", a+".md")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte("---\nname: "+a+"\nmodel: sonnet\nskills:\n  - flow:habits\n---\n\nYou build.\n"), 0o600))
	}
	habits := filepath.Join(root, "plugin", "skills", "habits", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(habits), 0o700))
	require.NoError(t, os.WriteFile(habits, []byte("---\nname: habits\ndescription: x\n---\n\nHABIT-TEXT\n"), 0o600))
	t.Setenv(flow.EnvRoot, root)
	return d, conf, logDir
}

// runAgent runs `loom flow agent args…` with stdin.
func runAgent(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := newFlowCmd()
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"agent"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestFlowAgent_RunWaitDone(t *testing.T) {
	d, conf, logDir := agentFixture(t, "intermediate-dev")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "the-parent")
	account := os.Getenv("CLAUDE_CONFIG_DIR")
	require.NoError(t, os.WriteFile(filepath.Join(account, "settings.json"),
		[]byte(`{"enabledPlugins": {"superpowers@official": true, "gone@official": false}}`), 0o600))

	out, _, err := runAgent(t, "Build story 1.", "run", "intermediate-dev", "--effort", "xhigh")
	require.NoError(t, err)
	assert.Equal(t, "started intermediate-dev-1: flow:intermediate-dev, job 1\n", out)
	j := d.job(1)
	assert.Equal(t, sched.KindAgent, j.Kind)

	out, _, err = runAgent(t, "", "wait", "60")
	require.NoError(t, err)
	report := filepath.Join(conf.state, "agents", "intermediate-dev-1", "report.md")
	assert.Equal(t, "done intermediate-dev-1: DONE\nreport: "+report+"\n", out)
	b, err := os.ReadFile(report)
	require.NoError(t, err)
	assert.Equal(t, "all done\nSTATUS: DONE\n", string(b))

	// One argument per line; the prompt holds newlines of its own.
	b, err = os.ReadFile(filepath.Join(logDir, "0.args"))
	require.NoError(t, err)
	args := string(b)
	prompt := rolePreamble("intermediate-dev") + "\n\nYour preloaded skill flow:habits:\n\nHABIT-TEXT\n\nBuild story 1."
	assert.True(t, strings.HasPrefix(args, "-p\n"+prompt+"\n--agent\nflow:intermediate-dev\n"), args)
	assert.Contains(t, args, "--effort\nxhigh\n")
	assert.Contains(t, args, "--output-format\nstream-json\n")
	assert.Contains(t, args, `"superpowers@official":false`, "the account's enabled plugins are turned off")
	assert.NotContains(t, args, "gone@official", "a plugin already off is left alone")
	assert.Contains(t, args, "flow guard", "the git guard")
	assert.NotContains(t, args, "--json-schema", "--agent turns it off (spec §2a, 9)")
	assert.NotContains(t, args, "--resume")
	env, err := os.ReadFile(filepath.Join(logDir, "0.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "LOOM_FLOW_AGENT=intermediate-dev")
	assert.Contains(t, string(env), "CLAUDE_CONFIG_DIR="+account, "the starting session's account")
	assert.NotContains(t, string(env), "the-parent", "the parent session's variables are dropped")

	out, _, err = runAgent(t, "", "show")
	require.NoError(t, err)
	assert.Contains(t, out, "session: sess-1")
	assert.Contains(t, out, "cost: $0.25")
}

func TestFlowAgent_AQuestionIsAnsweredByAResume(t *testing.T) {
	_, _, logDir := agentFixture(t, "intermediate-dev")
	_, _, err := runAgent(t, "ASK me", "run", "intermediate-dev", "--name", "s1")
	require.NoError(t, err)

	out, _, err := runAgent(t, "", "wait", "s1", "60")
	require.NoError(t, err)
	assert.Contains(t, out, "done s1: NEEDS_CONTEXT\nquestion: Which colour?\n")

	_, _, err = runAgent(t, "", "answer", "s1")
	assert.Equal(t, exitUsage, flowExit(err), "an empty answer")
	out, _, err = runAgent(t, "Blue.", "answer", "s1")
	require.NoError(t, err)
	assert.Equal(t, "resumed s1: job 2\n", out)
	out, _, err = runAgent(t, "", "wait", "s1", "60")
	require.NoError(t, err)
	assert.Contains(t, out, "done s1: DONE\n")

	b, err := os.ReadFile(filepath.Join(logDir, "1.args"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "--resume\nsess-1\n")
	assert.True(t, strings.HasPrefix(string(b), "-p\nBlue.\n"), "the answer alone is the prompt: the session holds the rest")

	_, _, err = runAgent(t, "Again.", "answer", "s1")
	assert.Equal(t, exitUsage, flowExit(err), "a run that is DONE takes no answer")
}

func TestFlowAgent_AJobWithoutAUsableReportFails(t *testing.T) {
	agentFixture(t, "intermediate-dev")
	for prompt, why := range map[string]string{
		"BROKEN":    "no result",
		"FAILING":   "an error result",
		"BADSTATUS": `status "MAYBE"`,
		"NOSTATUS":  "no STATUS line",
		"MUTE":      "a question was asked but not given",
	} {
		name := strings.ToLower(prompt)
		_, _, err := runAgent(t, prompt, "run", "intermediate-dev", "--name", name)
		require.NoError(t, err)
		out, _, err := runAgent(t, "", "wait", name, "60")
		assert.Equal(t, 1, flowExit(err), prompt)
		assert.Contains(t, out, "failed "+name+": ", prompt)
		assert.Contains(t, out, why, prompt)
	}
}

func TestFlowAgent_Refusals(t *testing.T) {
	agentFixture(t, "intermediate-dev", "stray")
	stray := filepath.Join(os.Getenv(flow.EnvRoot), "plugin", "agents", "stray.md")
	require.NoError(t, os.WriteFile(stray, []byte("---\nname: stray\nskills: [other:skill]\n---\n"), 0o600))
	for _, c := range []struct {
		stdin string
		args  []string
		code  int
		says  string
	}{
		{"x", []string{"run", "no-such-agent"}, exitUsage, "no agent no-such-agent"},
		{"", []string{"run", "intermediate-dev"}, exitUsage, "the brief is empty"},
		{strings.Repeat("x", 49<<10), []string{"run", "intermediate-dev"}, exitUsage, "at most 48 KiB"},
		{"x", []string{"run", "intermediate-dev", "--name", "bad name"}, exitUsage, "bad name"},
		{"x", []string{"run", "stray"}, exitNotRun, "other:skill"},
		{"", []string{"wait", "never"}, exitNotRun, "never"},
	} {
		_, _, err := runAgent(t, c.stdin, c.args...)
		assert.Equal(t, c.code, flowExit(err), "%q", c.args)
		assert.ErrorContains(t, err, c.says, "%q", c.args)
	}

	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	require.NoError(t, os.WriteFile(settings, []byte("{not json"), 0o600))
	_, _, err := runAgent(t, "x", "run", "intermediate-dev")
	assert.Equal(t, exitNotRun, flowExit(err), "a settings file it can't read refuses the run")
	assert.ErrorContains(t, err, "settings.json")
	require.NoError(t, os.Remove(settings))

	t.Setenv(flow.EnvRoot, "")
	_, _, err = runAgent(t, "x", "run", "intermediate-dev")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "inside a session")
}

func TestFlowAgent_ANameWhoseRunHasntEndedIsRefusedAndCancelEndsIt(t *testing.T) {
	d, _, _ := agentFixture(t, "intermediate-dev")
	_, _, err := runAgent(t, "ASK", "run", "intermediate-dev", "--name", "s1")
	require.NoError(t, err)
	_, _, err = runAgent(t, "ASK", "run", "intermediate-dev", "--name", "s1")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "s1 hasn't ended")

	out, _, err := runAgent(t, "", "cancel", "s1")
	require.NoError(t, err)
	assert.Contains(t, out, "cancelled s1")
	d.waitFinished(t, 1)
	_, _, err = runAgent(t, "x", "run", "intermediate-dev", "--name", "s1")
	require.NoError(t, err, "a cancelled run's name is free again")
}

func TestScrubbedVar(t *testing.T) {
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN", "LOOM_MOD_SOCKET", "CLAUDE_EFFORT"} {
		assert.True(t, scrubbedVar(name), name)
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "PATH", "LOOM_FLOW_ROOT", "LOOM_LAUNCH", "CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_API_KEY"} {
		assert.False(t, scrubbedVar(name), name)
	}
}

func TestStatusLines(t *testing.T) {
	for _, c := range []struct{ report, status, question string }{
		{"all done\nSTATUS: DONE", "DONE", ""},
		{"x\n**STATUS: NEEDS_CONTEXT**\nQUESTION: `Which colour?`", "NEEDS_CONTEXT", "Which colour?"},
		{"STATUS: BLOCKED\nlater\nSTATUS: DONE", "DONE", ""},
		// The order the preamble asks for, and the one real runs wrote
		// (2026-11-06): the question before the status line.
		{"x\nQUESTION: Merge main?\n\nSTATUS: NEEDS_CONTEXT", "NEEDS_CONTEXT", "Merge main?"},
		{"QUESTION: first\nQUESTION: second\nSTATUS: NEEDS_CONTEXT", "NEEDS_CONTEXT", "second"},
		{"STATUS: NEEDS_CONTEXT\nQUESTION: a\nlater\nSTATUS: DONE", "DONE", ""},
		// A bare status word counts only when no STATUS line does.
		{"x\nNEEDS_CONTEXT\nQUESTION: Rewrite them?\nDEVIATION: d", "NEEDS_CONTEXT", "Rewrite them?"},
		{"**DONE**", "DONE", ""},
		{"BLOCKED\nlater\nSTATUS: DONE", "DONE", ""},
		{"we're not DONE yet", "", ""},
		// A bold tag, and a question ending in a code span (feedback
		// thread 18).
		{"**STATUS:** DONE", "DONE", ""},
		{"**QUESTION:** Rename `foo` to `bar`?\n**STATUS:** NEEDS_CONTEXT", "NEEDS_CONTEXT", "Rename `foo` to `bar`?"},
		{"QUESTION: Keep `foo`\nSTATUS: NEEDS_CONTEXT", "NEEDS_CONTEXT", "Keep `foo`"},
		{"no lines at all", "", ""},
	} {
		status, question := statusLines(c.report)
		assert.Equal(t, c.status, status, "%q", c.report)
		assert.Equal(t, c.question, question, "%q", c.report)
	}
}

// A job that stops at its turn cap ends its run TURN_CAP, which answer
// resumes on its session (flow in loom part 3b, spec §6).
func TestFlowAgent_ATurnCapIsResumable(t *testing.T) {
	_, _, logDir := agentFixture(t, "intermediate-dev")
	_, _, err := runAgent(t, "CAPPED work", "run", "intermediate-dev", "--name", "c1")
	require.NoError(t, err)

	out, _, err := runAgent(t, "", "wait", "c1", "60")
	require.NoError(t, err, out)
	assert.True(t, strings.HasPrefix(out, "done c1: TURN_CAP\n"), out)

	out, _, err = runAgent(t, "Continue from where you stopped.", "answer", "c1")
	require.NoError(t, err, out)
	assert.Equal(t, "resumed c1: job 2\n", out)
	out, _, err = runAgent(t, "", "wait", "c1", "60")
	require.NoError(t, err, out)
	assert.True(t, strings.HasPrefix(out, "done c1: DONE\n"), out)
	b, err := os.ReadFile(filepath.Join(logDir, "1.args"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "--resume\nsess-1\n")
}

// Reviewers and developers get their own report lines after the headless
// preamble, and a definition's maxTurns becomes --max-turns (part 3b's probe:
// --agent doesn't apply it).
func TestAgentArgv_RolePreamblesAndMaxTurns(t *testing.T) {
	agentFixture(t)
	root := os.Getenv(flow.EnvRoot)
	write := func(role, front string) {
		p := filepath.Join(root, "plugin", "agents", role+".md")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte("---\nname: "+role+"\n"+front+"---\n\nBody.\n"), 0o600))
	}
	write("story-reviewer", "model: opus\n")
	write("intermediate-dev", "model: sonnet\nmaxTurns: 250\n")
	write("recon", "model: sonnet\n")
	cwd := t.TempDir()
	argvOf := func(role string) string {
		argv, err := agentArgv(root, role, "", cwd, []byte("Go."))
		require.NoError(t, err)
		return strings.Join(argv, "\n")
	}
	review, dev, recon := argvOf("story-reviewer"), argvOf("intermediate-dev"), argvOf("recon")
	assert.Contains(t, review, reviewPreamble)
	assert.NotContains(t, review, developerPreamble)
	assert.Contains(t, dev, developerPreamble)
	assert.NotContains(t, dev, reviewPreamble)
	assert.NotContains(t, recon, reviewPreamble)
	assert.NotContains(t, recon, developerPreamble)
	assert.Contains(t, dev, "\n--max-turns\n250\n")
	assert.NotContains(t, review, "--max-turns", "no maxTurns, no flag")

	_, err := agentArgv(root, "nobody", "", cwd, []byte("Go."))
	assert.Equal(t, exitUsage, exitCode(t, err))
	_, err = agentArgv(root, "recon", "", cwd, []byte("  "))
	assert.Equal(t, exitUsage, exitCode(t, err))
}
