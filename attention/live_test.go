//go:build attention_live

package attention

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/sched/advisor"
	"github.com/stretchr/testify/require"
)

// evalCase is one labelled turn of testdata/eval.json.
type evalCase struct {
	Name    string  `json:"name"`
	Verdict string  `json:"verdict"`
	Request Request `json:"request"`
}

// TestLive_Eval runs the installed claude with the classifier's prompt,
// schema and lockdown on every labelled turn, logs accuracy and cost, and
// fails on any needs_you read as done or stopped: a missed ask is the
// failure this classifier exists to prevent. Opt-in (go test -tags
// attention_live ./attention -run TestLive -v), since it spends plan usage.
// TestMain isolates CLAUDE_CONFIG_DIR, so the call names the real one:
// ATTENTION_LIVE_CLAUDE_CONFIG_DIR, else ~/.claude.
func TestLive_Eval(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude on PATH")
	}
	configDir := os.Getenv("ATTENTION_LIVE_CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		configDir = filepath.Join(home, ".claude")
	}
	data, err := os.ReadFile(filepath.Join("testdata", "eval.json"))
	require.NoError(t, err)
	var cases []evalCase
	require.NoError(t, json.Unmarshal(data, &cases))
	settings, err := Resolve(nil)
	require.NoError(t, err)
	work := t.TempDir()
	right, cost := 0, 0.0
	for _, c := range cases {
		msg, err := json.Marshal(c.Request)
		require.NoError(t, err)
		res, err := advisor.Run(advisor.Call{Program: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + configDir}, Dir: work,
			Model: settings.Model, Effort: settings.Effort, Schema: Schema, SystemPrompt: SystemPrompt, NoTools: true, Message: msg})
		require.NoError(t, err, c.Name)
		a, err := ParseVerdict(res.Answer)
		require.NoError(t, err, "%s: %s", c.Name, res.Answer)
		cost += res.Usage.CostUSD
		if a.Verdict == c.Verdict {
			right++
		}
		t.Logf("%-20s want %-9s got %-9s %q (%d ms)", c.Name, c.Verdict, a.Verdict, a.Gist, res.Usage.DurationMS)
		if c.Verdict == NeedsYou && (a.Verdict == Done || a.Verdict == Stopped) {
			t.Errorf("%s: an ask read as %s", c.Name, a.Verdict)
		}
	}
	t.Logf("accuracy %d/%d, cost $%.4f", right, len(cases), cost)
}
