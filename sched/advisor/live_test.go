//go:build advisor_live

package advisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/require"
)

// TestLive_TheRealCLIAnswersAPrior runs the installed claude with the
// advisor's flags on one small prior: the contract the fakes stand in for.
// Opt-in (go test -tags advisor_live ./sched/advisor), since it spends plan
// usage. TestMain isolates CLAUDE_CONFIG_DIR, so the call names the real
// one: ADVISOR_LIVE_CLAUDE_CONFIG_DIR, else ~/.claude.
func TestLive_TheRealCLIAnswersAPrior(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)
	configDir := os.Getenv("ADVISOR_LIVE_CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		configDir = filepath.Join(home, ".claude")
	}
	msg, err := json.Marshal(PriorRequest{
		Task: "prior",
		Job:  Job{Argv: []string{"go", "test", "./sched/..."}, Command: []string{"go", "test", "./sched/..."}, Description: "sched tests", Repo: "loom", Folder: "."},
		Host: Host{Cores: 16, RAMGB: 30, Caps: sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}, DefaultClaim: sched.Claim{CPUs: 4, MemoryGB: 6}},
	})
	require.NoError(t, err)
	res, err := Run(Call{Program: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + configDir}, Dir: wd,
		Model: DefaultModel, Effort: DefaultEffort, Schema: PriorSchema, Message: msg})
	require.NoError(t, err)
	_, err = ParsePrior(res.Answer, 4, 22, func(sched.SignatureID) bool { return false })
	require.NoError(t, err, string(res.Answer))
	require.NotEmpty(t, res.Usage.Model, "the CLI names the model that answered")
	t.Logf("prior: %s; usage: %+v", res.Answer, res.Usage)
}
