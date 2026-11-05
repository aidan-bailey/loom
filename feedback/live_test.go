//go:build feedback_live

package feedback

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/sched/advisor"
	"github.com/stretchr/testify/require"
)

// TestLive_GroupsTwoNotesTogether runs the installed claude with the
// grouper's prompt, schema and the advisor's lockdown on three notes: two
// about one fault, worded differently, and one about another. The contract
// the fakes stand in for. Opt-in (go test -tags feedback_live ./feedback),
// since it spends plan usage. TestMain isolates CLAUDE_CONFIG_DIR, so the
// call names the real one: FEEDBACK_LIVE_CLAUDE_CONFIG_DIR, else ~/.claude.
func TestLive_GroupsTwoNotesTogether(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude on PATH")
	}
	configDir := os.Getenv("FEEDBACK_LIVE_CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		configDir = filepath.Join(home, ".claude")
	}
	settings, err := Resolve(nil)
	require.NoError(t, err)
	work := t.TempDir()
	in := Empty()
	file := func(sess uint64, kind Kind, title, text string) ThreadID {
		n, _ := in.Note(in.Add(Note{Kind: kind, Title: title, Text: text, From: Session{ID: sess}}))
		req, offered := BuildRequest(in, n)
		msg, err := json.Marshal(req)
		require.NoError(t, err)
		res, err := advisor.Run(advisor.Call{Program: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + configDir}, Dir: work,
			Model: settings.Model, Effort: settings.Effort, Schema: Schema, SystemPrompt: SystemPrompt, Message: msg})
		require.NoError(t, err)
		a, err := ParseAnswer(res.Answer)
		require.NoError(t, err, string(res.Answer))
		th, err := Apply(&in, n.ID, a, offered)
		require.NoError(t, err)
		t.Logf("note %d → thread %d: %s; usage %+v", n.ID, th, res.Answer, res.Usage)
		return th
	}
	first := file(1, KindBug, "loom job run: --after N says no such job for another session's job",
		"I ran `loom job run --after 14 -d tests -- go test ./...` where job 14 was submitted by session builder. It refused: no such job 14. loom job list shows job 14 running.")
	second := file(2, KindBug, "--after can't see job 14 submitted from session x",
		"`loom job submit --after 14 -- make test` from session tester: refused with 'no such job 14', though the job exists (another session queued it).")
	third := file(3, KindFriction, "loom session send --wait times out on long turns",
		"`loom session send reviewer --from me --text 'review' --wait` gave up after my Bash tool's 2 minute timeout while the reviewer was still working.")
	require.Equal(t, first, second, "the same fault, worded differently")
	require.NotEqual(t, first, third, "another fault")
}
