package account

import (
	"os"
	"os/exec"
	"testing"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_UsageProbe probes the installed CLI's default account, so
// a change to the experimental get_usage response shows up on upgrade. It
// costs nothing (no model call) but needs a logged-in claude.ai account:
//
//	LOOM_TEST_REAL_CLAUDE=1 go test ./account -run TestRealClaude
func TestRealClaude_UsageProbe(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to probe the installed claude CLI")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	id, err := AuthStatus(bin, nil, internalexec.Default{})
	require.NoError(t, err)
	if !id.LoggedIn {
		t.Skip("the default account is not logged in")
	}

	u, err := ProbeUsage(bin, nil, id.ConfigDir, internalexec.Default{})

	require.NoError(t, err)
	if !u.Available {
		t.Skipf("no plan rate limits for auth method %q", id.AuthMethod)
	}
	assert.NotEmpty(t, u.Plan)
	require.True(t, u.FiveHour != nil || u.SevenDay != nil, "a subscriber reports at least one window")
	for _, w := range []*Window{u.FiveHour, u.SevenDay} {
		if w != nil {
			assert.GreaterOrEqual(t, w.Pct, 0.0)
		}
	}
}
