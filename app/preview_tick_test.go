package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/require"
)

// runningPtyFactory is a PtyFactory that runs the spawned command through the
// mock cmdExec (so `tmux new-session` flips the mock's sessionCreated flag),
// then hands back a /dev/null handle. Without running the command the post-create
// has-session poll never succeeds and Start times out.
type runningPtyFactory struct {
	t       *testing.T
	cmdExec cmd_test.MockCmdExec
}

func (f runningPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	h, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		f.t.Fatalf("runningPtyFactory: /dev/null: %v", err)
	}
	_ = f.cmdExec.Run(cmd)
	return h, nil
}

func (f runningPtyFactory) Close() {}

// startedInstanceWithHistory returns a started instance backed by a mock tmux
// whose capture-pane returns 200 lines of history for the `-S` (CaptureHistory)
// form and the bottom 24 for the plain visible capture. *historyCaptures counts
// the `-S` captures so a test can observe whether the agent pane re-rendered.
func startedInstanceWithHistory(t *testing.T, historyCaptures *int) *session.Instance {
	t.Helper()

	workdir := t.TempDir()
	runGit := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = workdir
		require.NoError(t, c.Run(), "git %v", args)
	}
	runGit("init")
	runGit("config", "--local", "user.email", "t@t.com")
	runGit("config", "--local", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "f.txt"), []byte("x"), 0644))
	runGit("add", ".")
	runGit("commit", "-m", "init")

	// Drive content through the mocked capture-pane, not a live emulator.
	t.Setenv("LOOM_PANE_RENDERER", "snapshot")

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:     "scroll",
		Path:      workdir,
		Program:   "bash",
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)

	sessionCreated := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				sessionCreated = true
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			s := cmd.String()
			if strings.Contains(s, "capture-pane") {
				// Only CaptureHistory uses `-S` (full scrollback).
				if strings.Contains(s, "-S") {
					*historyCaptures++
					var b strings.Builder
					for i := 1; i <= 200; i++ {
						fmt.Fprintf(&b, "histline%d\n", i)
					}
					return []byte(strings.TrimRight(b.String(), "\n")), nil
				}
				var b strings.Builder
				for i := 177; i <= 200; i++ {
					fmt.Fprintf(&b, "histline%d\n", i)
				}
				return []byte(strings.TrimRight(b.String(), "\n")), nil
			}
			return []byte(""), nil
		},
	}
	ts := tmux.NewTmuxSessionWithDeps("scroll", "bash", runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec)
	inst.SetTmuxSession(ts)
	require.NoError(t, inst.Start(true)) // creates the worktree and marks started
	return inst
}

// TestPreviewTickRerendersScrolledAgent is the regression guard for the second
// half of the scroll fix: the preview tick short-circuits when the agent's live
// content hash is unchanged, but scrolling changes the WINDOW, not the live
// hash. Before the fix the short-circuit skipped UpdateAgent, freezing the
// scroll; the tick must still re-render (capture-pane -S) a scrolled agent pane.
func TestPreviewTickRerendersScrolledAgent(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)

	m := homeWithAppState(t)
	_ = m.list.AddInstance(inst) // first add is auto-selected
	require.Same(t, inst, m.list.GetSelectedInstance())

	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)

	// Prime at the live tail, then scroll up into history.
	require.NoError(t, m.splitPane.UpdateAgent(inst))
	for i := 0; i < 30; i++ {
		m.splitPane.ScrollAgentUp()
	}
	require.NoError(t, m.splitPane.UpdateAgent(inst))
	require.True(t, m.splitPane.IsAgentInScrollMode(), "agent pane should be scrolled into history")

	// Make the preview-tick hash short-circuit fire: populate the content hash,
	// then pin lastPreview* to the current title + hash so the tick treats the
	// live content as unchanged.
	_, _ = inst.HasUpdated()
	m.lastPreviewTitle = inst.Title
	m.lastPreviewHash = inst.GetContentHash()
	require.NotNil(t, m.lastPreviewHash, "need a non-nil content hash to hit the short-circuit branch")

	before := historyCaptures
	_, _ = m.Update(previewTickMsg{})

	require.Greater(t, historyCaptures, before,
		"a scrolled agent pane must be re-rendered (capture-pane -S) on the preview tick even when the live content hash is unchanged")
	require.True(t, m.splitPane.IsAgentInScrollMode(), "agent pane stays scrolled after the tick")
}
