package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_AccountMemoryDir pins what accountMemoryDir assumes,
// against a real interactive Claude in a loom-style worktree of a
// repository reached through a symlinked parent, on an account whose
// projects/ links into the main config dir. Run it with CLAUDE_CONFIG_DIR
// set to such an account (a loom account dir). Without loom's settings,
// Claude asks before writing a memory, under the key loom computes; with
// them, the memory lands in the resolved dir unasked. It spends a few
// cents of haiku usage and leaves trust entries in the account's
// .claude.json (it removes the projects/ dirs it creates), so it only runs with LOOM_TEST_REAL_CLAUDE=1 and never in
// CI.
func TestRealClaude_AccountMemoryDir(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if fi, err := os.Lstat(filepath.Join(configDir, "projects")); configDir == "" || err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Skip("set CLAUDE_CONFIG_DIR to a loom account dir, whose projects/ is a link")
	}
	projects, err := filepath.EvalSymlinks(filepath.Join(configDir, "projects"))
	require.NoError(t, err)

	ctx := context.Background()
	git := func(dir string, args ...string) {
		t.Helper()
		out, err := internalexec.GitCommand(ctx, dir, args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	real := filepath.Join(t.TempDir(), "real")
	require.NoError(t, os.MkdirAll(real, 0o700))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	repo := filepath.Join(link, "repo") // the path loom would store
	git("", "init", "-q", repo)
	git(repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(repo, ".loom", "worktrees", "wt_18be000000000002")
	git(repo, "worktree", "add", "-q", "-b", "wt", wt)

	memDir, why := accountMemoryDir(configDir, repo)
	require.Empty(t, why)
	physRepo, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	key, _ := claudetmp.DirName(physRepo)
	physWt, err := filepath.EvalSymlinks(wt)
	require.NoError(t, err)
	wtKey, _ := claudetmp.DirName(physWt)
	var created []string // only what this test creates
	for _, k := range []string{key, wtKey} {
		p := filepath.Join(projects, k)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			created = append(created, p)
		}
	}

	sock := fmt.Sprintf("loommem-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() {
		_ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run()
		time.Sleep(3 * time.Second) // Claude writes its transcript as it exits
		for _, p := range created {
			_ = os.RemoveAll(p)
		}
	})

	// start runs claude with extra flags in a new session and returns its
	// screen, wrapped lines joined, and a way to send it a prompt.
	start := func(name, flags string) (func() string, func(string)) {
		target := tmux.PaneTarget(name)
		screen := func() string { return tm("capture-pane", "-p", "-J", "-t", target) }
		tm("new-session", "-d", "-s", name, "-x", "250", "-y", "50", "-c", wt, "-e", "CLAUDE_CONFIG_DIR="+configDir)
		tm("send-keys", "-t", target, "claude --model haiku --permission-mode default "+flags, "Enter")
		const trustDialog = "trust this folder"
		waitForRealClaude(t, screen, 60*time.Second, func() bool {
			s := screen()
			return strings.Contains(s, trustDialog) || strings.Contains(s, "Claude Code")
		})
		if s := screen(); strings.Contains(s, trustDialog) {
			if strings.Contains(s, "❯ No, exit") {
				tm("send-keys", "-t", target, "Down")
			}
			tm("send-keys", "-t", target, "Enter")
		}
		waitForRealClaude(t, screen, 60*time.Second, func() bool {
			s := screen()
			return strings.Contains(s, "Claude Code") && !strings.Contains(s, trustDialog)
		})
		time.Sleep(2 * time.Second) // let the input box take focus
		return screen, func(text string) {
			tm("send-keys", "-t", target, "-l", text)
			time.Sleep(300 * time.Millisecond)
			tm("send-keys", "-t", target, "Enter")
		}
	}
	const ask = "Do you want to"

	screen, send := start("plain", "")
	send("Automated test. Use the Write tool to save an auto-memory file named probe-a.md in your memory directory, then reply DONE.")
	waitForRealClaude(t, screen, 2*time.Minute, func() bool { return strings.Contains(screen(), ask) })
	assert.Contains(t, screen(), "/projects/"+key+"/memory/", "Claude keys the worktree's memory on its repository's physical root")
	tm("send-keys", "-t", tmux.PaneTarget("plain"), "Escape")
	tm("kill-session", "-t", tmux.SessionTarget("plain"))

	hooksDir := filepath.Join(t.TempDir(), "hooks")
	_, err = hooks.Prepare(hooksDir, memDir)
	require.NoError(t, err)
	screen, send = start("loom", "--settings '"+hooks.SettingsPath(hooksDir)+"'")
	send("Automated test. Use the Write tool to save an auto-memory file named probe-b.md in your memory directory, then reply DONE.")
	probe := filepath.Join(memDir, "probe-b.md")
	waitForRealClaude(t, screen, 2*time.Minute, func() bool {
		_, err := os.Stat(probe)
		return err == nil || strings.Contains(screen(), ask)
	})
	assert.NotContains(t, screen(), ask, "a memory write in the resolved dir needs no permission")
	assert.FileExists(t, probe)
}
