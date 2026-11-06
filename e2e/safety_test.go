//go:build e2e

package e2e

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/internal/devsandbox"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionCLI runs `loom session <args> -w <the toy workspace>` and fails
// the test on an error.
func sessionCLI(t *testing.T, sb *devsandbox.Sandbox, args ...string) string {
	t.Helper()
	argv := append([]string{sb.LoomBin(), "session"}, args...)
	out, err := runLoom(t, sb, append(argv, "-w", devsandbox.WorkspaceName)...)
	require.NoError(t, err, out)
	return out
}

// newCLISession starts title with `loom session new` and waits for the
// fake agent's banner in its pane.
func newCLISession(t *testing.T, sb *devsandbox.Sandbox, title string) {
	t.Helper()
	sessionCLI(t, sb, "new", title)
	require.Eventually(t, func() bool {
		return hasSession(sb, title) && strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName(title)), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the fake agent of %s never came up", title)
}

// sessionRow is one row of `loom session list --json`.
type sessionRow struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	Worktree string `json:"worktree"`
	Account  string `json:"account"`
}

// sessionRowOf reads title's row from `loom session list --json`.
func sessionRowOf(t *testing.T, sb *devsandbox.Sandbox, title string) sessionRow {
	t.Helper()
	var rows []sessionRow
	require.NoError(t, json.Unmarshal([]byte(sessionCLI(t, sb, "list", "--json")), &rows))
	for _, r := range rows {
		if r.Title == title {
			return r
		}
	}
	require.Failf(t, "no such session", "%q is not listed: %+v", title, rows)
	return sessionRow{}
}

// worktreeOf is the one worktree of title in the toy workspace.
func worktreeOf(t *testing.T, sb *devsandbox.Sandbox, title string) string {
	t.Helper()
	trees := worktreesOf(t, sb.WorkspaceConfigDir(), title)
	require.Len(t, trees, 1, "worktrees of %s", title)
	return trees[0]
}

// gitOut runs git in dir and returns its output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := internalexec.GitCommand(context.Background(), dir, args...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// work is uncommitted work of every kind git keeps apart: a staged edit, an
// unstaged edit, an untracked file and an ignored one.
var work = map[string]string{
	"README.md": "# toy\n\nedited, then staged\n",
	"main.go":   "package main\n\n// an unstaged edit\nfunc main() {}\n",
	"wip.md":    "untracked\n",
	"build.log": "ignored\n",
}

// leaveWork writes work into tree and returns what `git status` and the
// staged diff say about it, for comparing after.
func leaveWork(t *testing.T, tree string) (status, staged string) {
	t.Helper()
	write := func(rel, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(tree, rel), []byte(body), 0o644))
	}
	write("README.md", work["README.md"])
	gitOut(t, tree, "add", "README.md")
	write("main.go", work["main.go"])
	write("wip.md", work["wip.md"])
	// The worktree's own exclude file keeps the repo's .gitignore untouched.
	exclude := strings.TrimSpace(gitOut(t, tree, "rev-parse", "--git-path", "info/exclude"))
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(tree, exclude)
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(exclude), 0o755))
	require.NoError(t, os.WriteFile(exclude, []byte("*.log\n"), 0o644))
	write("build.log", work["build.log"])
	status = gitOut(t, tree, "status", "--porcelain", "--ignored")
	require.Contains(t, status, "!! build.log", "the log is ignored")
	return status, gitOut(t, tree, "diff", "--cached")
}

// requireWork checks that tree still holds every piece of leaveWork's work,
// staged as it was.
func requireWork(t *testing.T, tree, status, staged, when string) {
	t.Helper()
	for _, rel := range []string{"README.md", "main.go", "wip.md", "build.log"} {
		got, err := os.ReadFile(filepath.Join(tree, rel))
		require.NoError(t, err, "%s: %s is gone", when, rel)
		assert.Equal(t, work[rel], string(got), "%s: %s changed", when, rel)
	}
	assert.Equal(t, status, gitOut(t, tree, "status", "--porcelain", "--ignored"), "%s: git status changed", when)
	assert.Equal(t, staged, gitOut(t, tree, "diff", "--cached"), "%s: the index changed", when)
}

// A pause keeps the worktree as it is and a resume launches in it again:
// staged, unstaged, untracked and ignored work all come through, nothing is
// committed or stashed, and the branch stays where it was.
func TestE2E_Safety_PauseAndResumeKeepUncommittedWork(t *testing.T) {
	sb := newSandbox(t, "")
	newCLISession(t, sb, "keeper")
	tree := worktreeOf(t, sb, "keeper")
	head := gitOut(t, tree, "rev-parse", "HEAD")
	status, staged := leaveWork(t, tree)

	sessionCLI(t, sb, "pause", "keeper")
	assert.False(t, hasSession(sb, "keeper"), "the agent stopped")
	assert.Equal(t, "Paused", sessionRowOf(t, sb, "keeper").Status)
	requireWork(t, tree, status, staged, "after the pause")

	sessionCLI(t, sb, "resume", "keeper")
	require.Eventually(t, func() bool {
		return hasSession(sb, "keeper") && strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName("keeper")), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the agent never came back")
	assert.Equal(t, tree, worktreeOf(t, sb, "keeper"), "it resumed in the same worktree")
	requireWork(t, tree, status, staged, "after the resume")
	assert.Equal(t, head, gitOut(t, tree, "rev-parse", "HEAD"), "nothing was committed")
	assert.Empty(t, strings.TrimSpace(gitOut(t, sb.RepoDir(), "stash", "list")), "nothing was stashed")
}

// claudeScratch points the daemon's Claude temp root and main config dir at
// throwaway dirs (the daemon inherits the test's environment through the
// command that starts it) and returns the temp root.
func claudeScratch(t *testing.T) (tmpRoot, configDir string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv(claudetmp.EnvTmpDir, base)
	configDir = filepath.Join(base, "claude-config")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	tmpRoot = filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()))
	return tmpRoot, configDir
}

// seedClaudeDirs writes, for the session whose worktree is tree, a
// scratchpad note under Claude's temp root and a transcript under its
// projects dir, the way Claude lays them out, and returns both files.
func seedClaudeDirs(t *testing.T, tmpRoot, configDir, tree string) (scratch, transcript string) {
	t.Helper()
	real, err := filepath.EvalSymlinks(tree)
	require.NoError(t, err)
	name, truncated := claudetmp.DirName(real)
	require.False(t, truncated, "the sandbox's worktree path is too long for Claude's encoding")
	scratch = filepath.Join(tmpRoot, name, "e2e-session", "scratchpad", "plan.md")
	transcript = filepath.Join(configDir, "projects", name, "e2e-session.jsonl")
	for path, body := range map[string]string{scratch: "the plan\n", transcript: `{"type":"user"}` + "\n"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	return scratch, transcript
}

// zipHolding waits for a zip under dir whose entries include one ending in
// suffix, and returns its path.
func zipHolding(t *testing.T, dir, suffix string) string {
	t.Helper()
	var found string
	require.Eventually(t, func() bool {
		zips, _ := filepath.Glob(filepath.Join(dir, "*.zip"))
		for _, z := range zips {
			r, err := zip.OpenReader(z)
			if err != nil {
				continue
			}
			for _, f := range r.File {
				if strings.HasSuffix(f.Name, suffix) {
					found = z
				}
			}
			_ = r.Close()
		}
		return found != ""
	}, uiTimeout, 100*time.Millisecond, "no zip in %s holds %s", dir, suffix)
	return found
}

// Nothing of the agent's own is lost across a pause, a resume and a kill:
// the pause parks Claude's scratchpad in the workspace's archive and the
// resume puts it back, and the kill archives the scratchpad for good and
// snapshots the chats before the worktree goes.
func TestE2E_Safety_ScratchpadAndChatsOutliveThePauseAndTheKill(t *testing.T) {
	tmpRoot, configDir := claudeScratch(t)
	sb := newSandbox(t, "")
	newCLISession(t, sb, "keeper")
	tree := worktreeOf(t, sb, "keeper")
	scratch, _ := seedClaudeDirs(t, tmpRoot, configDir, tree)
	archive := filepath.Join(sb.WorkspaceConfigDir(), "archive")

	sessionCLI(t, sb, "pause", "keeper")
	zipHolding(t, filepath.Join(archive, "claude-tmp"), "scratchpad/plan.md")
	_, err := os.Stat(scratch)
	assert.True(t, os.IsNotExist(err), "the pause moved the scratchpad into the archive")
	zipHolding(t, filepath.Join(archive, "chats"), "e2e-session.jsonl")

	sessionCLI(t, sb, "resume", "keeper")
	got, err := os.ReadFile(scratch)
	require.NoError(t, err, "the resume restored the scratchpad before the agent launched")
	assert.Equal(t, "the plan\n", string(got))

	sessionCLI(t, sb, "kill", "keeper", "-y")
	assert.Empty(t, worktreesOf(t, sb.WorkspaceConfigDir(), "keeper"), "the kill removed the worktree")
	zipHolding(t, filepath.Join(archive, "claude-tmp"), "scratchpad/plan.md")
	_, err = os.Stat(scratch)
	assert.True(t, os.IsNotExist(err), "the kill archived the scratchpad")
	zipHolding(t, filepath.Join(archive, "chats"), "e2e-session.jsonl")
}

// tmuxServerPID is the pid of the sandbox's tmux server.
func tmuxServerPID(t *testing.T, sb *devsandbox.Sandbox) int {
	t.Helper()
	out, err := tmux.CommandOnSocket(context.Background(), sb.Socket(), "display-message", "-p", "#{pid}").Output()
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err, "%q", out)
	return pid
}

// freezeTmux stops the sandbox's tmux server with SIGSTOP, so every tmux
// command hangs unanswered as under heavy load, and returns the function
// that thaws it, which the test's cleanup also calls.
func freezeTmux(t *testing.T, server int) (thaw func()) {
	t.Helper()
	thaw = func() { _ = syscall.Kill(server, syscall.SIGCONT) }
	t.Cleanup(thaw)
	require.NoError(t, syscall.Kill(server, syscall.SIGSTOP))
	return thaw
}

// serveLogSince returns a function reading what the sandbox daemon's
// serve.log gained since now.
func serveLogSince(t *testing.T, sb *devsandbox.Sandbox) func() string {
	t.Helper()
	path := filepath.Join(sb.GlobalDir(), "logs", "serve.log")
	var from int64
	if info, err := os.Stat(path); err == nil {
		from = info.Size()
	}
	return func() string {
		b, err := os.ReadFile(path)
		if err != nil || int64(len(b)) < from {
			return ""
		}
		return string(b[from:])
	}
}

// requireUntouched checks that title runs on the same agent, as Running, in
// its worktree with the marker file still there.
func requireUntouched(t *testing.T, sb *devsandbox.Sandbox, title string, agent int, tree, marker string) {
	t.Helper()
	require.True(t, hasSession(sb, title), "the agent's tmux session is gone")
	assert.Equal(t, agent, agentPID(t, sb, title), "the agent was relaunched")
	row := sessionRowOf(t, sb, title)
	assert.NotEqual(t, "Paused", row.Status, "an unanswered probe paused the session")
	assert.Equal(t, resolved(t, tree), resolved(t, row.Worktree))
	got, err := os.ReadFile(filepath.Join(tree, marker))
	require.NoError(t, err, "the worktree lost its uncommitted file")
	assert.Equal(t, "uncommitted\n", string(got))
	assert.Contains(t, gitOut(t, tree, "status", "--porcelain"), marker, "the worktree is still a checkout holding the file")
}

// The 2026-08-21 incident: under load, tmux answers nothing, and a probe
// that times out must change nothing. With the tmux server frozen past
// several health ticks and the probe's timeout, the daemon keeps the
// session Running on the same agent, its worktree and uncommitted work
// untouched, once tmux answers again.
func TestE2E_Safety_AnUnansweredTmuxChangesNothing(t *testing.T) {
	sb := newSandbox(t, "")
	newCLISession(t, sb, "keeper")
	tree := worktreeOf(t, sb, "keeper")
	require.NoError(t, os.WriteFile(filepath.Join(tree, "wip.txt"), []byte("uncommitted\n"), 0o644))
	agent := agentPID(t, sb, "keeper")
	rec := servingDaemon(t, sb)

	logged := serveLogSince(t, sb)
	thaw := freezeTmux(t, tmuxServerPID(t, sb))
	// The daemon probes on its own, with no client asking; its log says
	// when a probe ran into the frozen server and timed out.
	timeouts := func() int { return strings.Count(logged(), "msg=liveness.probe_timeout") }
	require.Eventually(t, func() bool { return timeouts() >= 1 }, 30*time.Second, 200*time.Millisecond,
		"the daemon's probes never timed out on the frozen server, so the test proved nothing")
	// A daemon that took the timeout for a death has paused the session by
	// now; hold the freeze over another tick and timeout regardless.
	time.Sleep(10 * time.Second)
	thaw()

	servingDaemon(t, sb)
	assert.Equal(t, rec.PID, servingDaemon(t, sb).PID, "the same daemon serves")
	requireUntouched(t, sb, "keeper", agent, tree, "wip.txt")
}

// A daemon booting while tmux answers nothing must not take its sessions
// for dead: its reconcile and orphan sweep fail closed, and once tmux
// answers it serves the session on the agent that ran all along.
func TestE2E_Safety_ADaemonBootingIntoAnUnansweredTmuxChangesNothing(t *testing.T) {
	sb := newSandbox(t, "")
	newCLISession(t, sb, "keeper")
	tree := worktreeOf(t, sb, "keeper")
	require.NoError(t, os.WriteFile(filepath.Join(tree, "wip.txt"), []byte("uncommitted\n"), 0o644))
	agent := agentPID(t, sb, "keeper")
	old := servingDaemon(t, sb)
	out, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, out)
	require.True(t, waitExit(old.PID, uiTimeout))

	logged := serveLogSince(t, sb)
	thaw := freezeTmux(t, tmuxServerPID(t, sb))
	// This command's join starts the next daemon, which boots into the
	// frozen server; it may wait on the boot until tmux thaws.
	c := sb.Cmd(sb.LoomBin(), "session", "list", "-w", devsandbox.WorkspaceName)
	require.NoError(t, c.Start())
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	fresh := servingDaemonWithin(t, sb, uiTimeout, old.PID)
	time.Sleep(12 * time.Second) // past the probe's 5s timeout, more than once
	// The boot reconciles keeper only through tmux: had it logged the
	// session's recovery while tmux was frozen, it never asked tmux, and
	// the test proved nothing.
	require.NotContains(t, logged(), "msg=recovery", "the boot reconciled without waiting on the frozen tmux")
	thaw()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		_ = c.Process.Kill()
		t.Fatal("the session list never returned once tmux answered")
	}

	assert.Equal(t, fresh.PID, servingDaemon(t, sb).PID, "the booted daemon serves")
	assert.Contains(t, logged(), "title=keeper action=restore", "the boot, once tmux answered, found keeper healthy")
	requireUntouched(t, sb, "keeper", agent, tree, "wip.txt")
}

// servingDaemonWithin is servingDaemon with its own timeout, for a daemon
// whose boot may be slow; it waits only for the lock record, since a
// daemon booting into a frozen tmux serves only once the boot is done.
func servingDaemonWithin(t *testing.T, sb *devsandbox.Sandbox, timeout time.Duration, not ...int) daemon.Record {
	t.Helper()
	var rec daemon.Record
	require.Eventually(t, func() bool {
		r, held := sb.Daemon()
		rec = r
		for _, pid := range not {
			if r.PID == pid {
				return false
			}
		}
		return held && r.PID > 0
	}, timeout, 50*time.Millisecond, "no new daemon took the lock (last record: %+v)", rec)
	return rec
}
