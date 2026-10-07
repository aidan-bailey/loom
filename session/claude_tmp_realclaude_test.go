package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_TempDirLayout pins the layout session/claudetmp assumes,
// Claude's temp root and its encoding of the physical cwd, against a real
// interactive Claude session on a private tmux server, started in a cwd
// reached through a symlinked parent (the path loom stores). It has to be
// interactive: a headless `claude -p` is told of no scratchpad and creates
// no per-cwd directory. It spends a few cents of haiku usage and leaves
// entries in ~/.claude/projects, so it only runs with
// LOOM_TEST_REAL_CLAUDE=1 and never in CI.
func TestRealClaude_TempDirLayout(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	tmp := t.TempDir() // Claude's temp root's parent, never the real one
	real := filepath.Join(t.TempDir(), "real")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "repo", "wt_18be000000000001"), 0o700))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	cwd := filepath.Join(link, "repo", "wt_18be000000000001") // the path loom would store
	physical, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	t.Setenv(claudetmp.EnvTmpDir, tmp)

	ctx := context.Background()
	sock := fmt.Sprintf("loomclaudetmp-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })
	target := tmux.PaneTarget("probe")
	screen := func() string { return tm("capture-pane", "-p", "-t", target) }
	send := func(text string) {
		tm("send-keys", "-t", target, "-l", text)
		time.Sleep(300 * time.Millisecond)
		tm("send-keys", "-t", target, "Enter")
	}

	// -e hands Claude the variable as a loom pane would get it.
	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", cwd, "-e", claudetmp.EnvTmpDir+"="+tmp)
	tm("send-keys", "-t", target, "claude --model haiku", "Enter")
	const trustDialog = "trust this folder"
	waitForRealClaude(t, screen, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, trustDialog) || strings.Contains(s, "Claude Code")
	})
	if s := screen(); strings.Contains(s, trustDialog) {
		// "No, exit" is the default for folders under a temp dir.
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

	send("Automated test. Write the word ok to a file named probe.txt in your scratchpad directory, then reply DONE.")
	var dir string
	var matches []string
	waitForRealClaude(t, screen, 2*time.Minute, func() bool {
		root, ok := claudetmp.Root()
		if !ok {
			return false
		}
		d, ok := claudetmp.Locate(root, cwd)
		if !ok {
			return false
		}
		dir = d
		matches, _ = filepath.Glob(filepath.Join(dir, "*", "scratchpad", "probe.txt"))
		return len(matches) == 1
	})

	want, _ := claudetmp.DirName(physical)
	assert.Equal(t, want, filepath.Base(dir), "Claude names the dir after its physical cwd")
	assert.Len(t, matches, 1, "the scratchpad sits at <dir>/<session id>/scratchpad")
}
