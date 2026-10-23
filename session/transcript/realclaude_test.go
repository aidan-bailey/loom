package transcript

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_TranscriptReader runs one print-mode prompt that makes one
// Bash call and reads its transcript back: one turn of the prompt, one done
// Bash step whose summary is the command, and Claude's text. Claude's
// transcript format is its own and changes without notice, so this is the
// reader's guard. It spends a few cents of haiku and leaves the session in
// the developer's Claude config dir (projects/), so it runs only with
// LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_TranscriptReader(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	run := exec.Command("claude", "-p", "--model", "haiku", "--output-format", "json", "--allowedTools", "Bash(echo:*)",
		"Run the shell command `echo loom-transcript-probe` with the Bash tool, then reply with exactly the word DONE.")
	run.Dir = t.TempDir()
	out, err := run.Output()
	require.NoError(t, err, "claude -p: %s", out)
	var res struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(out, &res), "claude -p's JSON: %s", out)
	require.NotEmpty(t, res.SessionID)

	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		config = filepath.Join(home, ".claude")
	}
	paths, err := filepath.Glob(filepath.Join(config, "projects", "*", res.SessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1, "the transcript of session %s under %s", res.SessionID, config)

	p, err := Read(paths[0], res.SessionID, Query{})
	require.NoError(t, err)
	assert.Zero(t, p.Skipped)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	assert.Equal(t, FromYou, turn.From)
	assert.Contains(t, turn.Text, "loom-transcript-probe")
	var steps []Step
	var said []string
	for _, it := range turn.Items {
		switch it.Kind {
		case KindSteps:
			steps = append(steps, it.Steps...)
		case KindText:
			said = append(said, it.Text)
		}
	}
	require.Len(t, steps, 1)
	assert.Equal(t, "Bash", steps[0].Tool)
	assert.True(t, strings.HasPrefix(steps[0].Summary, "echo"), "the summary is the command: %q", steps[0].Summary)
	assert.Contains(t, steps[0].Summary, "loom-transcript-probe")
	assert.Equal(t, StateDone, steps[0].State)
	assert.Contains(t, strings.Join(said, " "), "DONE")
}
