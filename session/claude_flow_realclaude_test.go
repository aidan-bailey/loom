package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The probe's two plugins: flowprobe carries an agent that preloads the
// plugin's own skill and runs two Bash calls, the second of which the
// session's PreToolUse hook blocks; flowprobetwo carries a second agent, so
// a run that starts both shows that two --plugin-dir flags both load.
var probePlugins = map[string]map[string]string{
	"flowprobe": {
		".claude-plugin/plugin.json": `{"name": "flowprobe", "version": "0.0.1", "description": "loom flow plugin probe"}` + "\n",
		"agents/probe-dev.md": "---\nname: probe-dev\ndescription: Probe agent for loom's flow plugin test. Use only when told to.\nmodel: haiku\ntools: Bash\nskills:\n  - flowprobe:probe-habits\n---\n\n" +
			"Run these two Bash commands, one call each, in this order: `echo PROBE-ALLOWED`, then `echo PROBE-BLOCKED`. " +
			"Then reply with the codeword your preloaded skill gives you, and what each command returned.\n",
		"skills/probe-habits/SKILL.md": "---\nname: probe-habits\ndescription: Probe skill for loom's flow plugin test.\nuser-invocable: false\n---\n\nThe codeword is QUINCE-4.\n",
	},
	"flowprobetwo": {
		".claude-plugin/plugin.json": `{"name": "flowprobetwo", "version": "0.0.1", "description": "loom flow plugin probe, second plugin"}` + "\n",
		"agents/probe-two.md":        "---\nname: probe-two\ndescription: Second probe agent for loom's flow plugin test. Use only when told to.\nmodel: haiku\ntools: Read\n---\n\nDo not use any tools. Reply with the word DONE.\n",
	},
}

// TestRealClaude_FlowPluginContract checks the four premises the flow
// plugin rests on (flow in loom spec §7) against the claude on PATH, as
// the default account: what agent_type a hook sees inside a plugin agent;
// that a plugin agent preloads its plugin's skill by its namespaced name;
// that a session's PreToolUse hook fires for a plugin agent's calls and
// its exit 2 blocks one; and that two --plugin-dir flags both load. It
// logs the agent type P4 matches on. It spends a few cents of sonnet and
// haiku usage, so it only runs with LOOM_TEST_REAL_CLAUDE=1, never in CI.
func TestRealClaude_FlowPluginContract(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	_, err := hooks.Prepare(hooksDir, "", "")
	require.NoError(t, err)

	// A PreToolUse hook on Bash that keeps each payload and blocks the call
	// holding PROBE-BLOCKED.
	pre := filepath.Join(root, "pretool")
	require.NoError(t, os.MkdirAll(pre, 0o700))
	guard := `f='` + pre + `/'"$(date +%s%N)-$$"'.json'; cat > "$f"; ` +
		`if grep -q PROBE-BLOCKED "$f"; then echo 'blocked by the probe hook' >&2; exit 2; fi; exit 0`
	var doc map[string]any
	b, err := os.ReadFile(hooks.SettingsPath(hooksDir))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &doc))
	doc["hooks"].(map[string]any)["PreToolUse"] = []any{map[string]any{
		"matcher": "Bash",
		"hooks":   []any{map[string]any{"type": "command", "command": guard}},
	}}
	b, err = json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(hooks.SettingsPath(hooksDir), b, 0o600))

	var pluginDirs []string
	for name, files := range probePlugins {
		dir := filepath.Join(root, name)
		for rel, content := range files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		}
		pluginDirs = append(pluginDirs, "--plugin-dir", dir)
	}
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))

	prompt := `We are testing Claude Code plugins. In ONE message, launch two subagents: one with subagent_type "flowprobe:probe-dev" and one with subagent_type "flowprobetwo:probe-two". Give both this task: "Follow your instructions." Wait for both, then reply DONE.`
	args := append([]string{"-p", "--model", "sonnet", "--settings", hooks.SettingsPath(hooksDir)}, pluginDirs...)
	args = append(args, "--allowedTools=Agent,Bash", prompt)
	cmd := exec.Command("claude", args...)
	cmd.Dir = work
	for _, kv := range os.Environ() { // run as the default account (TestMain empties CLAUDE_CONFIG_DIR)
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	starts := map[string]hooks.Event{}
	entries, err := os.ReadDir(hooks.EventsDir(hooksDir))
	require.NoError(t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(hooks.EventsDir(hooksDir), e.Name()))
		require.NoError(t, err)
		if ev, err := hooks.ParseEvent(data); err == nil && ev.Name == hooks.EventSubagentStart {
			starts[ev.AgentType] = ev
		}
	}
	typeEnding := func(suffix string) string {
		for typ := range starts {
			if strings.HasSuffix(typ, suffix) {
				return typ
			}
		}
		return ""
	}
	devType, twoType := typeEnding("probe-dev"), typeEnding("probe-two")
	t.Logf("SubagentStart agent types: %v", func() (ts []string) {
		for typ := range starts {
			ts = append(ts, typ)
		}
		return
	}())
	require.NotEmpty(t, devType, "the probe-dev agent never started:\n%s", out)
	require.NotEmpty(t, twoType, "premise 4: the second --plugin-dir's agent never started:\n%s", out)

	// Premise 1 and 3: PreToolUse fired inside the plugin agent, with its agent_type.
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
	t.Logf("PROBE RESULT: PreToolUse agent_type inside the plugin agent: %v", seen)
	require.NotEmpty(t, seen, "premise 3: no PreToolUse payload from the plugin agent")
	assert.Contains(t, seen, devType, "premise 1: the hook's agent_type matches SubagentStart's")

	ev := starts[devType]
	meta := subagent.MetaPath(ev.TranscriptPath, ev.AgentID)
	require.NotEmpty(t, meta)
	transcript, err := os.ReadFile(strings.TrimSuffix(meta, ".meta.json") + ".jsonl")
	require.NoError(t, err)
	assert.Contains(t, string(transcript), "QUINCE-4", "premise 2: the plugin agent preloaded its plugin's skill")
	assert.Contains(t, string(transcript), "blocked by the probe hook", "premise 3: exit 2 blocked the call")
	assert.Contains(t, string(transcript), "PROBE-ALLOWED", "premise 3: the other call ran")
}
