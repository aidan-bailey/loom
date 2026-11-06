package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runProbePlugin is the run engine probe's plugin (flow in loom part 3b,
// plan P1): an agent capped at two turns with work for four, and a reviewer.
var runProbePlugin = map[string]string{
	".claude-plugin/plugin.json": `{"name": "flowrunprobe", "version": "0.0.1", "description": "loom run-engine probe"}` + "\n",
	"agents/probe-capped.md": "---\nname: probe-capped\ndescription: Probe agent for loom's run-engine test. Use only when told to.\nmodel: haiku\neffort: low\nmaxTurns: 2\ntools: Bash\n---\n\n" +
		"Run `ls`, then `ls -a`, then `ls -l`, then `pwd`, each as its own Bash call, one at a time, waiting for each before the next. Then report what each returned.\n",
	"agents/probe-reviewer.md": "---\nname: probe-reviewer\ndescription: Probe reviewer for loom's run-engine test. Use only when told to.\nmodel: haiku\neffort: low\ntools: Bash, Read\n---\n\n" +
		"You review a change. Read `x.go`, find the problem in it, and report it.\n",
}

// runProbeReview mirrors cli.reviewPreamble (part 3b, spec §5.6).
const runProbeReview = "Before your STATUS line, end your report with one line `FINDING: <severity> <file>:<line> <the fix>` per issue, " +
	"the severity being Critical, Important or Minor, followed by the word design when the fix needs a decision the spec doesn't make " +
	"(`FINDING: Important design a.go:12 <the fix>`), one line `PLAN: <the problem>` " +
	"per problem in the plan's own code, and then `VERDICT: APPROVED` or `VERDICT: CHANGES_REQUESTED`. Write no FINDING or PLAN line when there is none."

var runProbeFinding = regexp.MustCompile(`(?m)^[*` + "`" + `]*FINDING: (Critical|Important|Minor) (\[?design\]? )?\S+:\d+ .+`)

// TestRealClaude_FlowRunPremises checks part 3b's four premises (plan P1)
// against the claude on PATH, as the default account: whether --agent applies
// a definition's maxTurns, --max-turns as the fallback, a resume of a capped
// run, and a reviewer's FINDING and VERDICT lines. A few cents of haiku, so
// it only runs with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_FlowRunPremises(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	plugin := filepath.Join(root, "flowrunprobe")
	for rel, content := range runProbePlugin {
		p := filepath.Join(plugin, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	off := map[string]bool{}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	if b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
		var user struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		require.NoError(t, json.Unmarshal(b, &user))
		for name, on := range user.EnabledPlugins {
			if on {
				off[name] = false
			}
		}
	}
	settings, err := json.Marshal(map[string]any{"enabledPlugins": off})
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(work, "x.go"), []byte("package x\n\nfunc Div(a, b int) int { return a / b }\n"), 0o600))
	for _, args := range [][]string{{"init", "-q"}, {"add", "x.go"}, {"-c", "user.name=p", "-c", "user.email=p@p", "commit", "-qm", "x"}} {
		require.NoError(t, exec.Command("git", append([]string{"-C", work}, args...)...).Run())
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDE_CONFIG_DIR" || probeScrubbed(name) {
			continue
		}
		env = append(env, kv)
	}
	// run returns the stream-json output whatever the exit: a capped run may
	// exit non-zero.
	run := func(agent, prompt string, extra ...string) []byte {
		args := append([]string{"-p", prompt, "--agent", "flowrunprobe:" + agent, "--plugin-dir", plugin,
			"--settings", string(settings), "--output-format", "stream-json", "--verbose"}, extra...)
		cmd := exec.Command("claude", args...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.Output()
		t.Logf("PROBE %s %v: exit %v", agent, extra, err)
		return out
	}
	logResult := func(label string, result map[string]any) {
		report, _ := result["result"].(string)
		t.Logf("PROBE %s: subtype=%v is_error=%v turns=%v has_result_text=%v", label, result["subtype"], result["is_error"], result["num_turns"], report != "")
	}

	// Premise 1: --agent applies the definition's maxTurns.
	out := run("probe-capped", agentProbePreamble+"\n\nGo.")
	initEv, result := agentProbeEvents(t, out)
	require.NotNil(t, result, "premise 1: no result:\n%s", out)
	logResult("premise 1 (definition maxTurns)", result)
	// On Claude Code 2.1.318 it doesn't (2026-11-06: 5 turns against a cap
	// of 2), so agent jobs pass --max-turns from the definition (premise 2);
	// the probe records which holds.
	capped := result["subtype"] == "error_max_turns"
	t.Logf("PROBE RESULT: --agent applies the definition's maxTurns: %v", capped)

	// Premise 2: --max-turns caps it, the fallback for premise 1.
	out = run("probe-capped", agentProbePreamble+"\n\nGo.", "--max-turns", "2")
	_, result2 := agentProbeEvents(t, out)
	require.NotNil(t, result2, "premise 2: no result:\n%s", out)
	logResult("premise 2 (--max-turns 2)", result2)
	assert.Equal(t, "error_max_turns", result2["subtype"], "premise 2")

	// Premise 3: a resume of a capped run continues its session.
	session, _ := initEv["session_id"].(string)
	if !capped {
		_, r2 := agentProbeEvents(t, out)
		session, _ = r2["session_id"].(string)
	}
	out = run("probe-capped", "Continue from where you stopped.", "--resume", session)
	initEv3, result3 := agentProbeEvents(t, out)
	require.NotNil(t, result3, "premise 3: no result on resume:\n%s", out)
	logResult("premise 3 (resume)", result3)
	assert.Equal(t, session, initEv3["session_id"], "premise 3: the same session")

	// Premise 4: a reviewer under the review preamble ends with FINDING,
	// VERDICT and STATUS lines.
	out = run("probe-reviewer", agentProbePreamble+" "+runProbeReview+"\n\nReview x.go.")
	_, result4 := agentProbeEvents(t, out)
	require.NotNil(t, result4, "premise 4: no result:\n%s", out)
	report, _ := result4["result"].(string)
	t.Logf("PROBE premise 4 report:\n%s", report)
	assert.Regexp(t, runProbeFinding, report, "premise 4: a FINDING line")
	assert.Regexp(t, `(?m)^[*`+"`"+`]*VERDICT: (APPROVED|CHANGES_REQUESTED)`, report, "premise 4: a VERDICT line")
	status, _ := statusLines(report)
	assert.Contains(t, []string{"DONE", "DONE_WITH_CONCERNS"}, status, "premise 4: a STATUS line")
}
