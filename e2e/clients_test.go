//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/internal/devsandbox"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The subcommands as clients of the daemon, and the daemon's own duties
// with no TUI (daemon stage 3C+4): `loom workspace` and `loom reset` change
// what the daemon serves, and an open TUI follows; an agent that stops at a
// trust prompt is answered by the daemon alone.

// addWorkspace makes a git repository in the sandbox's dir and registers it
// as the workspace name with `loom workspace add`, as a user does from a
// shell. It returns the repository's path.
func addWorkspace(t *testing.T, sb *devsandbox.Sandbox, name string) string {
	t.Helper()
	// Inside the sandbox dir, which Down removes after it has stopped the
	// daemon: a temp dir would go first, under the daemon's feet.
	repo := filepath.Join(sb.Dir, "ws-"+name)
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".loom/\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "loom e2e"},
		{"config", "user.email", "e2e@example.invalid"},
		{"config", "commit.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-q", "--no-verify", "-m", "chore: initial commit"},
	} {
		out, err := internalexec.GitCommand(context.Background(), repo, args...).CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	// Without the sandbox's config the workspace would run the real claude.
	require.NoError(t, sb.SeedWorkspace(repo))
	out, err := runLoom(t, sb, sb.LoomBin(), "workspace", "add", repo, "--name", name)
	require.NoError(t, err, out)
	assert.Contains(t, out, fmt.Sprintf("Workspace %q added at %s", name, repo))
	return repo
}

// openTabs opens the registered workspaces names as tabs in the TUI that
// shows a workspace in its classic slot, through the workspace picker (W):
// space toggles a row and esc applies.
func openTabs(t *testing.T, sb *devsandbox.Sandbox, rows ...int) {
	t.Helper()
	require.NoError(t, sb.SendKeys("W"))
	require.NoError(t, sb.WaitFor("Toggle Workspaces", uiTimeout))
	at := 0
	for _, row := range rows {
		for ; at < row; at++ {
			require.NoError(t, sb.SendKeys("j"))
		}
		require.NoError(t, sb.SendKeys("Space"))
	}
	require.NoError(t, sb.SendKeys("Escape"))
	waitGone(t, sb, "Toggle Workspaces")
}

// tabBar is the TUI's tab bar: the first three lines of its screen.
func tabBar(t *testing.T, sb *devsandbox.Sandbox) string {
	t.Helper()
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	lines := strings.Split(screen, "\n")
	return strings.Join(lines[:min(3, len(lines))], "\n")
}

// waitTabs waits until the tab bar names every workspace of want and none
// of gone.
func waitTabs(t *testing.T, sb *devsandbox.Sandbox, want, gone []string) {
	t.Helper()
	var bar string
	require.Eventually(t, func() bool {
		bar = tabBar(t, sb)
		for _, name := range want {
			if !strings.Contains(bar, name) {
				return false
			}
		}
		for _, name := range gone {
			if strings.Contains(bar, name) {
				return false
			}
		}
		return true
	}, uiTimeout, 100*time.Millisecond, "the tab bar should show %v and not %v:\n%s", want, gone, bar)
}

// A workspace removed from the registry while it is open as a tab is
// unloaded by the daemon, and the TUI closes the tab; the other tab and the
// daemon carry on.
func TestE2E_Clients_WorkspaceRemoveClosesTheTab(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	addWorkspace(t, sb, "second")
	rec := servingDaemon(t, sb)
	openTabs(t, sb, 0, 1)
	waitTabs(t, sb, []string{devsandbox.WorkspaceName, "second"}, nil)

	out, err := runLoom(t, sb, sb.LoomBin(), "workspace", "remove", "second")
	require.NoError(t, err, out)
	assert.Contains(t, out, `Workspace "second" removed`)

	require.NoError(t, sb.WaitFor("second is no longer registered; closed its tab", uiTimeout))
	waitTabs(t, sb, []string{devsandbox.WorkspaceName}, []string{"second"})
	list, err := runLoom(t, sb, sb.LoomBin(), "workspace", "list")
	require.NoError(t, err, list)
	assert.NotRegexp(t, `(?m)^second\s`, list)
	assert.Regexp(t, `(?m)^`+devsandbox.WorkspaceName+`\s`, list)
	now, held := sb.Daemon()
	assert.True(t, held)
	assert.Equal(t, rec.PID, now.PID, "the removal needed no new daemon")
	assert.True(t, sb.DriverRunning(), "the TUI did not exit")
}

// A workspace renamed while it is open as a tab keeps its tab, its rail and
// its agents: the daemon renames it in place, keeping its ID, so the TUI
// follows by ID and shows the new name.
func TestE2E_Clients_WorkspaceRenameKeepsTheTab(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	repo := addWorkspace(t, sb, "second")
	openTabs(t, sb, 0, 1)
	waitTabs(t, sb, []string{devsandbox.WorkspaceName, "second"}, nil)
	// Focus the second tab (its rail shows its terminal, "0. second") and
	// start an agent there.
	require.NoError(t, sb.SendText("}"))
	require.NoError(t, sb.WaitFor("0. second", uiTimeout))
	createSession(t, sb, "mover")
	agent := agentPID(t, sb, "mover")
	require.Len(t, worktreesOf(t, filepath.Join(repo, ".loom"), "mover"), 1, "mover runs in the second workspace")
	rec := servingDaemon(t, sb)

	out, err := runLoom(t, sb, sb.LoomBin(), "workspace", "rename", "second", "renamed")
	require.NoError(t, err, out)
	assert.Contains(t, out, `Workspace "second" renamed to "renamed"`)

	waitTabs(t, sb, []string{devsandbox.WorkspaceName, "renamed"}, []string{"second"})
	require.NoError(t, sb.WaitFor("RENAMED", uiTimeout), "the rail's header follows the name")
	requireKept(t, sb, "mover", agent)
	list, err := runLoom(t, sb, sb.LoomBin(), "workspace", "list")
	require.NoError(t, err, list)
	assert.Regexp(t, `(?m)^renamed\s`, list)
	assert.NotRegexp(t, `(?m)^second\s`, list)
	now, held := sb.Daemon()
	assert.True(t, held)
	assert.Equal(t, rec.PID, now.PID, "the rename needed no new daemon")
	assert.True(t, sb.DriverRunning(), "the TUI did not exit")
}

// paneText is what tmux shows in the pane of session name on the sandbox's
// server.
func paneText(t *testing.T, sb *devsandbox.Sandbox, name string) string {
	t.Helper()
	out, err := tmux.CommandOnSocket(context.Background(), sb.Socket(),
		"capture-pane", "-p", "-t", tmux.PaneTarget(name)).Output()
	require.NoError(t, err, "no pane for %s", name)
	return string(out)
}

// hasSession reports whether the tmux session titled title runs.
func hasSession(sb *devsandbox.Sandbox, title string) bool {
	return tmux.CommandOnSocket(context.Background(), sb.Socket(),
		"has-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(title))).Run() == nil
}

// worktreesOf lists the worktrees of the session titled title in the
// workspace whose config dir is configDir.
func worktreesOf(t *testing.T, configDir, title string) []string {
	t.Helper()
	trees, err := filepath.Glob(filepath.Join(configDir, "worktrees", "*", title+"_*"))
	require.NoError(t, err)
	var dirs []string
	for _, tree := range trees {
		if info, err := os.Stat(tree); err == nil && info.IsDir() {
			dirs = append(dirs, tree)
		}
	}
	return dirs
}

// `loom reset --force` while the daemon runs is the daemon's to make: the
// workspace's sessions leave an open TUI's list at once, their tmux
// sessions and worktrees go, the workspace keeps a terminal, and another
// workspace's agent runs on untouched.
func TestE2E_Clients_ResetEmptiesTheWorkspaceAndSparesTheOthers(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "doomed")
	require.Len(t, worktreesOf(t, sb.WorkspaceConfigDir(), "doomed"), 1)
	terminal := panePID(t, sb, tmux.ToLoomTmuxName(devsandbox.WorkspaceName))

	// Another workspace, shown by a second TUI on the same daemon.
	repo := addWorkspace(t, sb, "other")
	second, err := sb.WithDriver("dev-driver-2")
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			screen, _ := second.Screen(false)
			t.Logf("second TUI's last screen:\n%s", screen)
		}
	})
	require.NoError(t, second.Start(devsandbox.StartOptions{Command: []string{sb.LoomBin(), "--workspace", "other"}}))
	require.NoError(t, second.WaitFor("0. other", uiTimeout))
	createSession(t, second, "survivor")
	survivor := agentPID(t, sb, "survivor")
	rec := servingDaemon(t, sb)

	out, err := runLoom(t, sb, sb.LoomBin(), "reset", "--workspace", devsandbox.WorkspaceName, "--force")
	require.NoError(t, err, out)
	assert.Contains(t, out, "The loom daemon reset "+devsandbox.WorkspaceName)

	waitGone(t, sb, "doomed")
	assert.False(t, hasSession(sb, "doomed"), "the session's tmux session is gone")
	assert.Empty(t, worktreesOf(t, sb.WorkspaceConfigDir(), "doomed"), "its worktree is gone")
	require.NoError(t, sb.WaitFor("0. "+devsandbox.WorkspaceName, uiTimeout), "the workspace keeps its terminal")
	require.Eventually(t, func() bool {
		pid, err := tmux.CommandOnSocket(context.Background(), sb.Socket(),
			"display-message", "-p", "-t", tmux.PaneTarget(tmux.ToLoomTmuxName(devsandbox.WorkspaceName)), "#{pane_pid}").Output()
		return err == nil && strings.TrimSpace(string(pid)) != "" && strings.TrimSpace(string(pid)) != fmt.Sprint(terminal)
	}, uiTimeout, 100*time.Millisecond, "the terminal is a new process: the reset swept the old one")

	assert.True(t, hasSession(sb, "survivor"), "another workspace's session survives")
	assert.Equal(t, survivor, agentPID(t, sb, "survivor"), "its agent was not relaunched")
	assert.Len(t, worktreesOf(t, filepath.Join(repo, ".loom"), "survivor"), 1, "its worktree survives")
	require.NoError(t, second.WaitFor("survivor", uiTimeout))
	now, held := sb.Daemon()
	assert.True(t, held)
	assert.Equal(t, rec.PID, now.PID, "the reset needed no new daemon")
	assert.True(t, sb.DriverRunning(), "the TUI did not exit")

	// The records went with the sessions: a TUI started afresh lists none.
	require.NoError(t, sb.StopDriver(10*time.Second))
	startLoom(t, sb)
	require.NoError(t, sb.WaitFor("0. "+devsandbox.WorkspaceName, uiTimeout))
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "doomed")
}

// A subcommand is a client of the daemon, as the TUI is: with none running
// it starts one (the build it is), and the next command or TUI joins that
// one. What it changes the daemon serves at once.
func TestE2E_Clients_SubcommandsStartTheDaemon(t *testing.T) {
	sb := newSandbox(t, "")
	_, held := sb.Daemon()
	require.False(t, held, "no daemon runs before the first command")

	out, err := runLoom(t, sb, sb.LoomBin(), "workspace", "list")
	require.NoError(t, err, out)
	assert.Contains(t, out, devsandbox.WorkspaceName)
	rec := servingDaemon(t, sb)
	if exe := exeOf(rec.PID); exe != "" {
		assert.Equal(t, resolved(t, sb.LoomBin()), exe, "the command starts its own build as the daemon")
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", rec.PID))
	if err == nil {
		assert.True(t, strings.HasSuffix(string(cmdline), "\x00serve\x00"), "the daemon is `loom serve`: %q", cmdline)
	}

	addWorkspace(t, sb, "second")
	status, err := runLoom(t, sb, sb.LoomBin(), "workspace", "status", "second")
	require.NoError(t, err, status)
	assert.Contains(t, status, `Workspace "second"`)
	assert.Contains(t, status, "0 instances", "the daemon serves a workspace at once when it is added")
	now, held := sb.Daemon()
	require.True(t, held)
	assert.Equal(t, rec.PID, now.PID, "the next command joins the daemon rather than start another")

	startLoom(t, sb)
	again, _ := sb.Daemon()
	assert.Equal(t, rec.PID, again.PID, "so does the TUI")
}

// An agent that stops at a trust prompt on launch is answered by the daemon
// alone: with the daemon stopped and the agent's tmux session killed under
// it, the next daemon's boot relaunches the agent (a crash restart), which
// stops at the prompt, and the daemon's launch watch answers it with no TUI
// open. A TUI opened afterwards finds the agent past it.
func TestE2E_Clients_TrustPromptAnsweredWithNoTUI(t *testing.T) {
	sb := newSandbox(t, "fake-claude-trust")
	startLoom(t, sb)
	// The start's own launch stops at the prompt too: the banner shows once
	// the daemon has answered it.
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	old := servingDaemon(t, sb)

	require.NoError(t, sb.StopDriver(10*time.Second))
	require.NoError(t, sb.StopDaemon())
	require.True(t, waitExit(old.PID, uiTimeout))
	require.NoError(t, tmux.CommandOnSocket(context.Background(), sb.Socket(),
		"kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName("keeper"))).Run())
	require.True(t, waitExit(agent, uiTimeout), "the agent ended with its session")

	// Any command starts the daemon, whose boot finds the session dead with
	// its worktree intact and relaunches the agent.
	out, err := runLoom(t, sb, sb.LoomBin(), "workspace", "list")
	require.NoError(t, err, out)
	fresh := servingDaemon(t, sb, old.PID)
	require.Eventually(t, func() bool {
		return hasSession(sb, "keeper") && strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName("keeper")), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the relaunched agent never got past its trust prompt:\n%s", func() string {
		if !hasSession(sb, "keeper") {
			return "(no session)"
		}
		return paneText(t, sb, tmux.ToLoomTmuxName("keeper"))
	}())
	relaunched := agentPID(t, sb, "keeper")
	assert.NotEqual(t, agent, relaunched, "the agent was relaunched")
	assert.NotContains(t, paneText(t, sb, tmux.ToLoomTmuxName("keeper")), "Do you trust the files in this folder?")
	assert.False(t, sb.DriverRunning(), "no TUI was open to answer it")
	serveLog, err := os.ReadFile(daemon.LogPath(sb.GlobalDir()))
	require.NoError(t, err)
	assert.Contains(t, string(serveLog), "launch_watch.trust_prompt_answered", "the daemon answered it")

	startLoom(t, sb)
	assert.Equal(t, fresh.PID, servingDaemon(t, sb).PID, "the TUI joins the daemon that answered it")
	requireReattached(t, sb, "keeper", relaunched)
}
