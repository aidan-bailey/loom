package session

import (
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

// TestRealClaude_SubagentBrief repeats the 2026-11-05 probe
// (docs/superpowers/specs/2026-11-05-subagent-loom-context-design.md §4)
// against real Claude, as the default account: a general-purpose subagent
// of a launch prepared with a brief has it in its transcript, and a fork,
// which holds its parent's prompt already, doesn't. It pins the matcher
// Go's regexp can't evaluate. It spends a few cents of sonnet usage and
// leaves entries in ~/.claude/projects, so it only runs with
// LOOM_TEST_REAL_CLAUDE=1 and never in CI.
func TestRealClaude_SubagentBrief(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	const marker = "KUMQUAT-7"
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	_, err := hooks.Prepare(hooksDir, "", "Note for this test session: the probe codeword is "+marker+".")
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))

	// Asking a subagent to repeat its context verbatim trips a safeguard
	// against prompt extraction; the transcript says what it was given.
	prompt := `We are testing Claude Code hooks. In ONE message, launch two subagents: one with subagent_type "fork" and one with subagent_type "general-purpose". Give both this task: "Do not use any tools. Reply with the word DONE." Wait for both, then reply DONE.`
	cmd := exec.Command("claude", "-p", "--model", "sonnet", "--settings", hooks.SettingsPath(hooksDir), "--allowedTools=Agent", prompt)
	cmd.Dir = work
	// TestMain points CLAUDE_CONFIG_DIR at an empty dir, where Claude is
	// logged out; this Claude runs as the default account, under ~/.claude.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	starts := map[string]hooks.Event{} // agent type -> its SubagentStart
	entries, err := os.ReadDir(hooks.EventsDir(hooksDir))
	require.NoError(t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(hooks.EventsDir(hooksDir), e.Name()))
		require.NoError(t, err)
		if ev, err := hooks.ParseEvent(data); err == nil && ev.Name == hooks.EventSubagentStart {
			starts[ev.AgentType] = ev
		}
	}
	transcript := func(typ string) string {
		ev, ok := starts[typ]
		require.True(t, ok, "the model launched no %s subagent:\n%s", typ, out)
		meta := subagent.MetaPath(ev.TranscriptPath, ev.AgentID)
		require.NotEmpty(t, meta)
		b, err := os.ReadFile(strings.TrimSuffix(meta, ".meta.json") + ".jsonl")
		require.NoError(t, err)
		return string(b)
	}
	assert.Contains(t, transcript("general-purpose"), marker)
	assert.NotContains(t, transcript("fork"), marker, "the brief's matcher skips a fork")
}
