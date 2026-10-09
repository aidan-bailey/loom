package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envExec fakes a live tmux server: has-session succeeds, and
// show-environment answers with out/err.
func envExec(out string, err error, gotArgs *[]string) cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if gotArgs != nil {
				*gotArgs = append([]string(nil), c.Args...)
			}
			return []byte(out), err
		},
	}
}

func publishAccountDirs(t *testing.T, dirs map[string]string) {
	t.Helper()
	SetAccountDirs(dirs, nil)
	t.Cleanup(func() { SetAccountDirs(nil, nil) })
}

func TestLiveSessionAccount_MapsConfigDirToAccount(t *testing.T) {
	publishAccountDirs(t, map[string]string{"alt": "/g/accounts/alt", "work": "/g/accounts/work"})
	var args []string

	got := liveSessionAccount("three", envExec("CLAUDE_CONFIG_DIR=/g/accounts/alt\n", nil, &args))

	assert.Equal(t, "alt", got)
	assert.Equal(t, []string{"tmux", "-u", "show-environment", "-t", "=loom_three", "CLAUDE_CONFIG_DIR"}, args,
		"asks the session's own environment, by exact name")
}

// A session launched on the default account has no CLAUDE_CONFIG_DIR in
// its environment; tmux answers "unknown variable" and exits 1.
func TestLiveSessionAccount_DefaultWhenUnset(t *testing.T) {
	publishAccountDirs(t, map[string]string{"alt": "/g/accounts/alt"})

	assert.Equal(t, "", liveSessionAccount("three", envExec("", &exec.ExitError{}, nil)))
	assert.Equal(t, "", liveSessionAccount("three", envExec("-CLAUDE_CONFIG_DIR\n", nil, nil)),
		"a variable removed from the session environment is the default account too")
}

// A dir no registered account owns can't be named: no opinion, which is
// the default account, as before.
func TestLiveSessionAccount_UnregisteredDirIsDefault(t *testing.T) {
	publishAccountDirs(t, map[string]string{"alt": "/g/accounts/alt"})

	assert.Equal(t, "", liveSessionAccount("three", envExec("CLAUDE_CONFIG_DIR=/g/accounts/gone\n", nil, nil)))
}

func TestLiveSessionAccount_ProbeFailureIsDefault(t *testing.T) {
	publishAccountDirs(t, map[string]string{"alt": "/g/accounts/alt"})

	assert.Equal(t, "", liveSessionAccount("three", envExec("", errors.New("tmux: timeout"), nil)))
}

func TestInstanceDataFromOrphan_CarriesAccount(t *testing.T) {
	data := InstanceDataFromOrphan(OrphanCandidate{Title: "three", Account: "alt"}, "claude")
	assert.Equal(t, "alt", data.Account)
}

// The regression: a live session whose record was lost (another loom
// overwrote state.json) came back on the default account — its badge
// said @default and r saved it that way — while its agent still ran on
// the account it was launched on.
func TestDiscoverOrphans_LiveOrphanKeepsItsAccount(t *testing.T) {
	withStubProbe(t)
	publishAccountDirs(t, map[string]string{"alt": "/g/accounts/alt"})
	cfgDir := t.TempDir()
	wt := filepath.Join(cfgDir, "worktrees", "aidanb", "three_18acb35cb8ad6e5a")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(wt+".loom-title", []byte("three"), 0o644))

	got, err := DiscoverOrphans(cfgDir, nil, envExec("CLAUDE_CONFIG_DIR=/g/accounts/alt\n", nil, nil))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].HasLiveTmux)
	assert.Equal(t, "alt", got[0].Account)
}

// A dead orphan has no environment to read; nothing is probed.
func TestDiscoverOrphans_DeadOrphanSkipsAccountProbe(t *testing.T) {
	withStubProbe(t)
	publishAccountDirs(t, map[string]string{"alt": "/g/accounts/alt"})
	cfgDir := t.TempDir()
	wt := filepath.Join(cfgDir, "worktrees", "aidanb", "three_18acb35cb8ad6e5a")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	probed := false
	dead := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) {
			probed = true
			return nil, errors.New("unexpected")
		},
	}

	got, err := DiscoverOrphans(cfgDir, nil, dead)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].HasLiveTmux)
	assert.Equal(t, "", got[0].Account)
	assert.False(t, probed)
}
