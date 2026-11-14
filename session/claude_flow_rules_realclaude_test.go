package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rulesProbePlugin is the package-rules probe's plugin: one haiku agent
// that answers from its instructions and reads a file only when told to.
var rulesProbePlugin = map[string]string{
	".claude-plugin/plugin.json": `{"name": "rulesprobe", "version": "0.0.1", "description": "loom package-rules probe"}` + "\n",
	"agents/probe-rules.md": "---\nname: probe-rules\ndescription: Probe agent for loom's package-rules test. Use only when told to.\nmodel: haiku\neffort: low\ntools: Read\n---\n\n" +
		"Answer what you are asked, briefly. Use a tool only when the prompt tells you to.\n",
}

// TestRealClaude_FlowAgentPreloadsPackageRules checks the premise of the
// run engine's package rules
// (docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md
// §1) against the claude on PATH, as the default account: --add-dir with
// CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1 loads the directory's
// CLAUDE.md into an --agent run at startup, without them it doesn't, and a
// later Read under the directory loads it no second time. It spends a few
// cents of haiku, so it only runs with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_FlowAgentPreloadsPackageRules(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	plugin := filepath.Join(root, "rulesprobe")
	for rel, content := range rulesProbePlugin {
		p := filepath.Join(plugin, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	work := filepath.Join(root, "work")
	pkg := filepath.Join(work, "pkg")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "src"), 0o700))
	require.NoError(t, exec.Command("git", "-C", work, "init", "-q").Run())
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "CLAUDE.md"), []byte("# pkg rules\n\nThe codeword is HERON-3.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "src", "a.txt"), []byte("x\n"), 0o600))

	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDE_CONFIG_DIR" || name == "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD" || probeScrubbed(name) {
			continue
		}
		env = append(env, kv)
	}
	run := func(prompt string, preload bool) (string, string) {
		t.Helper()
		args := []string{"-p", prompt, "--agent", "rulesprobe:probe-rules", "--plugin-dir", plugin, "--output-format", "stream-json", "--verbose"}
		cmdEnv := slices.Clone(env)
		if preload {
			args = append(args, "--add-dir", pkg)
			cmdEnv = append(cmdEnv, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1")
		}
		cmd := exec.Command("claude", args...)
		cmd.Dir, cmd.Env = work, cmdEnv
		out, err := cmd.Output()
		require.NoError(t, err, string(out))
		_, result := agentProbeEvents(t, out)
		require.NotNil(t, result, string(out))
		text, _ := result["result"].(string)
		session, _ := result["session_id"].(string)
		return text, session
	}

	ask := "Without using any tool: what codeword do your instructions give? Answer with the codeword only, or NONE."
	got, _ := run(ask, true)
	assert.Contains(t, got, "HERON-3", "--add-dir with the variable loads the package's CLAUDE.md at startup")
	got, _ = run(ask, false)
	assert.NotContains(t, got, "HERON-3", "without them the package's CLAUDE.md waits for a touch")

	_, session := run("Read the file pkg/src/a.txt with the Read tool, then answer DONE.", true)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	found, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", session+".jsonl"))
	require.NoError(t, err)
	require.Len(t, found, 1, "the read's transcript")
	b, err := os.ReadFile(found[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"name":"Read"`, "the agent read under the package")
	for _, line := range strings.Split(string(b), "\n") {
		assert.False(t, strings.Contains(line, `"nested_memory"`) && strings.Contains(line, "pkg/CLAUDE.md"),
			"a Read under a pre-loaded directory loads its CLAUDE.md no second time")
	}
}
