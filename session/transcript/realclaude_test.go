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

// runClaude runs one print-mode prompt on haiku in a fresh directory, with
// args before it, and returns the path and session ID of the transcript it
// wrote under the developer's Claude config dir.
func runClaude(t *testing.T, prompt string, args ...string) (path, session string) {
	t.Helper()
	argv := append([]string{"-p", "--model", "haiku", "--output-format", "json"}, args...)
	run := exec.Command("claude", append(argv, prompt)...)
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
	return paths[0], res.SessionID
}

// TestRealClaude_TranscriptReader runs one print-mode prompt that makes one
// Bash call and reads its transcript back: one turn of the prompt, one done
// Bash step whose summary is the command, and Claude's text. A second case
// has Claude launch one subagent and reads the agent step's link and the
// subagent's own page. Claude's transcript format is its own and changes
// without notice, so this is the reader's guard. It spends a few cents of
// haiku per case and leaves the sessions in the developer's Claude config
// dir (projects/), so it runs only with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_TranscriptReader(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	path, session := runClaude(t,
		"Run the shell command `echo loom-transcript-probe` with the Bash tool, then reply with exactly the word DONE.",
		"--allowedTools", "Bash(echo:*)")

	p, err := Read(path, session, Query{})
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
	assert.Empty(t, steps[0].Agent, "a Bash step launches no agent")
	assert.Contains(t, strings.Join(said, " "), "DONE")

	t.Run("subagent", func(t *testing.T) {
		path, session := runClaude(t,
			"Launch exactly one subagent with the Agent tool. Its task: reply with exactly the word SUBPROBE and nothing else. "+
				"Wait for it to finish, then reply with exactly the word DONE.")
		p, err := Read(path, session, Query{})
		require.NoError(t, err)
		var launch *Step
		for _, turn := range p.Turns {
			for _, it := range turn.Items {
				for i := range it.Steps {
					if isAgentTool(it.Steps[i].Tool) {
						launch = &it.Steps[i]
					}
				}
			}
		}
		require.NotNil(t, launch, "the parent holds an Agent step")
		require.NotEmpty(t, launch.Agent, "the step is linked to its subagent")
		assert.Equal(t, StateDone, launch.State)
		assert.True(t, launch.Finished)

		sub, err := Read(path, session, Query{Agent: launch.Agent})
		require.NoError(t, err)
		assert.Equal(t, session, sub.Conversation)
		require.NotNil(t, sub.Agent)
		assert.Equal(t, launch.Agent, sub.Agent.ID)
		assert.False(t, sub.Agent.Missing)
		assert.Equal(t, AgentFinished, sub.Agent.State, "the sidecar's finished key once the agent ends")
		assert.False(t, sub.Agent.Started.IsZero())
		require.NotEmpty(t, sub.Turns)
		assert.Equal(t, FromBrief, sub.Turns[0].From)
		assert.Contains(t, sub.Turns[0].Text, "SUBPROBE")
	})
}
