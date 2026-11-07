package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentProbePlugin is the probe's plugin: an agent with pinned model and
// tools that preloads the plugin's skill, runs a Bash call the probe's
// PreToolUse hook allows and one it blocks, and reports in the schema.
var agentProbePlugin = map[string]string{
	".claude-plugin/plugin.json": `{"name": "flowprobe", "version": "0.0.1", "description": "loom agent-run probe"}` + "\n",
	"agents/probe-dev.md": "---\nname: probe-dev\ndescription: Probe agent for loom's agent-run test. Use only when told to.\nmodel: haiku\neffort: low\ntools: Bash\nskills:\n  - flowprobe:probe-habits\n---\n\n" +
		"Run these Bash commands, one call each, in this order: `git status --short`, `echo PROBE-ALLOWED`, `echo PROBE-BLOCKED`. " +
		"Put in your report the codeword your preloaded skill gives you and what each command returned. " +
		"If nobody has told you a colour yet, end with the status NEEDS_CONTEXT and the question `Which colour?`. " +
		"Once you have been told a colour, report DONE and say which colour you were told.\n",
	"skills/probe-habits/SKILL.md": "---\nname: probe-habits\ndescription: Probe skill for loom's agent-run test.\nuser-invocable: false\n---\n\nThe codeword is QUINCE-7.\n",
}

// agentProbePreamble mirrors cli.headlessPreamble: --agent turns
// --json-schema off (premise 4, 2026-11-06), so a report ends in status lines.
const agentProbePreamble = "You run headless as a loom agent job: nobody can message you, and SendMessage reaches no one. " +
	"Your final answer is your report. Its last line is its status: `STATUS: DONE`, `STATUS: DONE_WITH_CONCERNS`, `STATUS: BLOCKED` " +
	"or `STATUS: NEEDS_CONTEXT`. With NEEDS_CONTEXT, put a line `QUESTION: <your question>` just before it; the answer resumes you."

// statusLines is a report's last STATUS line's value and its last QUESTION
// line, wherever it stands, as cli.statusLines reads them.
func statusLines(report string) (status, question string) {
	for _, line := range strings.Split(report, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "*`")
		if v, ok := strings.CutPrefix(line, "STATUS:"); ok {
			status = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "QUESTION:"); ok {
			question = strings.TrimSpace(v)
		}
	}
	return status, question
}

// agentProbeEvents splits a stream-json log into its init and result events.
func agentProbeEvents(t *testing.T, out []byte) (initEv, result map[string]any) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev["type"] == "system" && ev["subtype"] == "init":
			initEv = ev
		case ev["type"] == "result":
			result = ev
		}
	}
	return initEv, result
}

// TestRealClaude_FlowAgentRun checks the eight premises of flow in loom
// part 3a (spec §5.1, plan amendment 4) against the claude on PATH, as the
// default account. It spends a few cents of haiku, so it only runs with
// LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_FlowAgentRun(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "git", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	plugin := filepath.Join(root, "flowprobe")
	for rel, content := range agentProbePlugin {
		p := filepath.Join(plugin, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	pre := filepath.Join(root, "pretool")
	require.NoError(t, os.MkdirAll(pre, 0o700))
	hook := `f='` + pre + `/'"$(date +%s%N)-$$"'.json'; cat > "$f"; ` +
		`if grep -q PROBE-BLOCKED "$f"; then echo 'blocked by the probe hook' >&2; exit 2; fi; exit 0`
	// The account's enabled plugins are turned off (owner, 2026-11-06), as
	// an agent job's are: their SessionStart hooks inject what no subagent
	// gets.
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
	settings, err := json.Marshal(map[string]any{"enabledPlugins": off, "hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "Bash|Write|Edit", "hooks": []any{map[string]any{"type": "command", "command": hook}},
	}}}})
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	require.NoError(t, exec.Command("git", "-C", work, "init", "-q").Run())

	// The probe's own shell is a loom session's, inside Claude Code: the
	// environment is scrubbed as an agent job's is (plan amendment 4), and
	// the default account is used.
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDE_CONFIG_DIR" || probeScrubbed(name) {
			continue
		}
		env = append(env, kv)
	}
	run := func(prompt string, extra ...string) []byte {
		args := append([]string{"-p", prompt, "--agent", "flowprobe:probe-dev", "--plugin-dir", plugin,
			"--settings", string(settings), "--output-format", "stream-json", "--verbose"}, extra...)
		cmd := exec.Command("claude", args...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.Output()
		require.NoError(t, err, string(out))
		return out
	}

	// --agent's main thread doesn't preload the definition's skills
	// (premise 3, 2026-11-06), so the skill's body goes in the prompt, as
	// an agent job's does.
	out := run(agentProbePreamble + "\n\nYour preloaded skill flowprobe:probe-habits:\n\nThe codeword is QUINCE-7.\n\nFollow your instructions.")
	initEv, result := agentProbeEvents(t, out)
	require.NotNil(t, initEv, "no init event:\n%s", out)
	require.NotNil(t, result, "no result event:\n%s", out)
	t.Logf("PROBE init: model=%v tools=%v session=%v", initEv["model"], initEv["tools"], initEv["session_id"])
	t.Logf("PROBE result: subtype=%v is_error=%v turns=%v cost=%v denials=%v", result["subtype"], result["is_error"], result["num_turns"], result["total_cost_usd"], result["permission_denials"])

	// Premise 1: the plugin agent is the main agent, with its pins.
	assert.Contains(t, initEv["model"], "haiku", "premise 1: the agent's pinned model")
	// Premise 8: a scrubbed run from inside a Claude session is a session of its own.
	assert.NotEqual(t, os.Getenv("CLAUDE_CODE_SESSION_ID"), initEv["session_id"], "premise 8")

	// Premise 2: the hook fired headless, exit 2 blocked, and what agent_type it saw.
	var seen []string
	payloads, err := os.ReadDir(pre)
	require.NoError(t, err)
	for _, p := range payloads {
		data, err := os.ReadFile(filepath.Join(pre, p.Name()))
		require.NoError(t, err)
		var in struct {
			AgentType string `json:"agent_type"`
		}
		require.NoError(t, json.Unmarshal(data, &in))
		seen = append(seen, in.AgentType)
	}
	t.Logf("PROBE RESULT: PreToolUse agent_type for a --agent main thread: %q", seen)
	require.NotEmpty(t, seen, "premise 2: the hook never fired")
	assert.Contains(t, string(out), "blocked by the probe hook", "premise 2: exit 2 blocked the call")

	// The account's plugins are off: no SessionStart hook ran.
	assert.NotContains(t, string(out), `"hook_event":"SessionStart"`, "the account's plugins are off")
	// Premises 3, 4 and 6: the skill's codeword, the status lines, a routine command ran.
	report, _ := result["result"].(string)
	t.Logf("PROBE report:\n%s", report)
	status, question := statusLines(report)
	assert.Equal(t, "NEEDS_CONTEXT", status, "premise 4: the status line")
	assert.Equal(t, "Which colour?", strings.Trim(question, "`\""), "premise 4: the question line")
	assert.Contains(t, report, "QUINCE-7", "premise 3: the preloaded skill")
	assert.Contains(t, report, "PROBE-ALLOWED", "premise 6: a routine command ran headless")

	// Premise 7: the transcript persists.
	session, _ := initEv["session_id"].(string)
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", session+".jsonl"))
	assert.NotEmpty(t, matches, "premise 7: no transcript for session %s", session)

	// Premise 5: --resume continues with the agent's context.
	out = run("The colour is blue. Finish, and say which colour you were told and the codeword.", "--resume", session)
	initEv, result = agentProbeEvents(t, out)
	require.NotNil(t, result, "premise 5: no result on resume:\n%s", out)
	report, _ = result["result"].(string)
	t.Logf("PROBE resumed report:\n%s", report)
	status, _ = statusLines(report)
	assert.Equal(t, "DONE", status, "premise 5")
	assert.Contains(t, strings.ToLower(report), "blue", "premise 5: the answer reached it")
	assert.Contains(t, report, "QUINCE-7", "premise 5: its context survived")
	assert.Equal(t, session, initEv["session_id"], "premise 5: the same session")
}

// probeScrubbed mirrors cli.scrubbedVar (plan amendment 4): the parent
// session's variables an agent job drops.
func probeScrubbed(name string) bool {
	switch name {
	case "CLAUDECODE", "AI_AGENT", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_JOB_DIR", "LOOM_MOD_SOCKET",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_EXECPATH":
		return true
	}
	return false
}
