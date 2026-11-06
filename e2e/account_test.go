//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envOf is process pid's environment variable name, and whether it is set.
func envOf(pid int, name string) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return "", false
	}
	for _, kv := range bytes.Split(b, []byte{0}) {
		if k, v, ok := strings.Cut(string(kv), "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// childrenOf lists the pids whose parent is pid.
func childrenOf(pid int) []int {
	var out []int
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	for _, s := range stats {
		b, err := os.ReadFile(s)
		if err != nil {
			continue
		}
		end := bytes.LastIndexByte(b, ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(b[end+1:]))
		if len(fields) < 2 {
			continue
		}
		if ppid, err := strconv.Atoi(fields[1]); err == nil && ppid == pid {
			if child, err := strconv.Atoi(filepath.Base(filepath.Dir(s))); err == nil {
				out = append(out, child)
			}
		}
	}
	return out
}

// agentConfigDir is the CLAUDE_CONFIG_DIR the agent of title runs with:
// that of the first process, from its pane's down, that sets one. A launch
// may wrap the agent in a shell, which then carries it only to its child.
func agentConfigDir(t *testing.T, sb *devsandbox.Sandbox, title string) (string, bool) {
	t.Helper()
	queue := []int{agentPID(t, sb, title)}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if v, ok := envOf(pid, "CLAUDE_CONFIG_DIR"); ok {
			return v, true
		}
		queue = append(queue, childrenOf(pid)...)
	}
	return "", false
}

// accountDir is the config dir the sandbox's registry records for name.
func accountDir(t *testing.T, sb *devsandbox.Sandbox, name string) string {
	t.Helper()
	acct, ok := account.LoadRegistry(sb.GlobalDir()).Get(name)
	require.True(t, ok, "no account %q", name)
	return acct.Dir
}

// A session on an account launches its Claude with that account's config
// dir, and one on the default account with the daemon's own; when the
// account's dir is gone, a resume refuses rather than launch on the
// default account, which would bill the wrong subscription, and goes ahead
// once the dir is back.
func TestE2E_Account_ASessionRunsOnItsAccountAndFailsClosedWithoutIt(t *testing.T) {
	_, mainDir := claudeScratch(t) // a throwaway main dir: never link into the real ~/.claude
	sb := newSandbox(t, "fake-claude")
	out, err := runLoom(t, sb, sb.LoomBin(), "account", "add", "work", "--no-login")
	require.NoError(t, err, out)
	dir := accountDir(t, sb, "work")
	require.True(t, strings.HasPrefix(resolved(t, dir), resolved(t, sb.GlobalDir())+string(filepath.Separator)),
		"the account lives in the sandbox, not the host: %s", dir)

	sessionCLI(t, sb, "new", "onwork", "--account", "work")
	sessionCLI(t, sb, "new", "ondefault")
	for _, title := range []string{"onwork", "ondefault"} {
		require.Eventually(t, func() bool {
			return hasSession(sb, title) && strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName(title)), "commands: work N")
		}, uiTimeout, 100*time.Millisecond, "the agent of %s never came up", title)
	}
	assert.Equal(t, "work", sessionRowOf(t, sb, "onwork").Account)

	got, ok := agentConfigDir(t, sb, "onwork")
	require.True(t, ok, "the account's agent runs with no CLAUDE_CONFIG_DIR")
	assert.Equal(t, resolved(t, dir), resolved(t, got), "the account's agent runs in the account's dir")
	if got, ok := agentConfigDir(t, sb, "ondefault"); ok {
		assert.Equal(t, resolved(t, mainDir), resolved(t, got), "the default account's agent runs in the main dir")
	}

	sessionCLI(t, sb, "pause", "onwork")
	aside := dir + ".aside"
	require.NoError(t, os.Rename(dir, aside))
	argv := []string{sb.LoomBin(), "session", "resume", "onwork", "-w", devsandbox.WorkspaceName}
	out, err = runLoom(t, sb, argv...)
	require.Error(t, err, "a resume with the account's dir gone must fail: %s", out)
	assert.Contains(t, out, "is missing", out)
	assert.False(t, hasSession(sb, "onwork"), "nothing launched, on any account")
	assert.Equal(t, "Paused", sessionRowOf(t, sb, "onwork").Status)

	require.NoError(t, os.Rename(aside, dir))
	sessionCLI(t, sb, "resume", "onwork")
	require.Eventually(t, func() bool { return hasSession(sb, "onwork") }, uiTimeout, 100*time.Millisecond)
	got, ok = agentConfigDir(t, sb, "onwork")
	require.True(t, ok)
	assert.Equal(t, resolved(t, dir), resolved(t, got), "the resumed agent runs on its account again")
}
