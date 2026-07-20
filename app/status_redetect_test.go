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

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// startedInstanceWithProgram returns a started instance running an arbitrary
// program whose mocked capture-pane always returns the same content. Static
// content is the shape of the Running-latch regression: the first status
// detection after a burst hashes new content (updated=true) and every later
// one hashes the same content (updated=false).
func startedInstanceWithProgram(t *testing.T, title, program, content string) *session.Instance {
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
		Title:     title,
		Path:      workdir,
		Program:   program,
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
			if strings.Contains(cmd.String(), "capture-pane") {
				return []byte(content), nil
			}
			return []byte(""), nil
		},
	}
	ts := tmux.NewTmuxSessionWithDeps(title, program, runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec)
	inst.SetTmuxSession(ts)
	require.NoError(t, inst.Start(true))
	return inst
}

// runDetection drives one quiet-triggered (or redetect-triggered) detection
// round the way the Bubble Tea runtime would: Update(trigger) must yield a
// command producing a statusDetectedMsg, which is fed back through Update.
// Returns the follow-up command from the statusDetectedMsg handler.
func runDetection(t *testing.T, m *home, trigger tea.Msg) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(trigger)
	require.NotNil(t, cmd, "trigger %T must schedule status detection", trigger)
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "detection cmd must return statusDetectedMsg, got %T", msg)
	require.NoError(t, detected.err)
	_, follow := m.Update(detected)
	return follow
}

// TestStatusDetectionConvergesToReadyAfterSettle is the regression guard for
// the Running-latch bug: the single quiet event after an output burst hashes
// changed content (updated=true → Running), and with no further output no
// event would ever re-derive the status — an idle agent showed "Running"
// forever. The updated→Running conclusion must schedule a follow-up detection
// that settles the instance to Ready once content stops changing.
func TestStatusDetectionConvergesToReadyAfterSettle(t *testing.T) {
	inst := startedInstanceWithProgram(t, "settle", "bash", "$ done\n$ ")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	// First quiet after the burst: content changed since the previous sample,
	// so detection concludes Running — and must arm a re-detection.
	_, cmd := m.Update(paneQuietMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd)
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "expected statusDetectedMsg, got %T", msg)
	require.True(t, detected.updated, "first sample after a burst hashes new content")
	_, follow := m.Update(detected)
	require.Equal(t, session.Running, inst.GetStatus())
	require.NotNil(t, follow, "updated→Running must schedule a re-detection, not latch")

	// While a re-detection is pending, another updated result must not stack
	// a second chain for the same session.
	_, dup := m.Update(statusDetectedMsg{instance: inst, updated: true})
	require.Nil(t, dup, "re-detection must be deduped per session")

	// The armed re-detection fires (tea.Tick waits out the delay), runs a
	// second detection against now-static content, and settles to Ready.
	redetect := follow()
	_ = runDetection(t, m, redetect)
	require.Equal(t, session.Ready, inst.GetStatus(),
		"a silent pane must settle to Ready on the follow-up detection")
}

// TestStatusDetectionSurfacesPromptAfterSettle: a permission prompt arrives
// as pane output, so the first quiet sample reports updated=true AND
// hasPrompt=true — the updated branch used to win and mask the prompt as
// "Running". The follow-up detection must surface Prompting.
func TestStatusDetectionSurfacesPromptAfterSettle(t *testing.T) {
	content := "Do you want to make this edit?\n❯ 1. Yes\n  3. No, and tell Claude what to do differently"
	inst := startedInstanceWithProgram(t, "promptmask", "claude", content)
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	_, cmd := m.Update(paneQuietMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd)
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "expected statusDetectedMsg, got %T", msg)
	require.True(t, detected.updated)
	require.True(t, detected.hasPrompt, "claude adapter must detect the permission prompt")
	_, follow := m.Update(detected)
	require.Equal(t, session.Running, inst.GetStatus())
	require.NotNil(t, follow, "prompt masked by updated=true must trigger re-detection")

	redetect := follow()
	_ = runDetection(t, m, redetect)
	require.Equal(t, session.Prompting, inst.GetStatus(),
		"the follow-up detection must surface the waiting permission prompt")
}

// TestDirtyDoesNotDemotePrompting: pane output is not proof the agent is
// working — Loom forwards focus-in/out sequences on host focus and selection
// changes, and agents repaint in response, so a dirty event used to flip a
// Prompting instance to Running just because the user clicked the window.
// Prompting must survive dirty events; quiet-time detection owns the demotion.
func TestDirtyDoesNotDemotePrompting(t *testing.T) {
	content := "Do you want to make this edit?\n❯ 1. Yes\n  3. No, and tell Claude what to do differently"
	inst := startedInstanceWithProgram(t, "dirtykeep", "claude", content)
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Prompting))

	_, _ = m.Update(paneDirtyMsg{session: inst.TmuxSessionName()})
	require.Equal(t, session.Prompting, inst.GetStatus(),
		"a focus/selection repaint must not relabel a waiting prompt as Running")
}

// TestDirtyPromotesReadyToRunning pins the promotion that must survive the
// Prompting fix: output on a Ready instance still means the agent started
// doing something.
func TestDirtyPromotesReadyToRunning(t *testing.T) {
	inst := startedInstanceWithProgram(t, "dirtypromote", "bash", "$ ")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Ready))

	_, _ = m.Update(paneDirtyMsg{session: inst.TmuxSessionName()})
	require.Equal(t, session.Running, inst.GetStatus())
}

// TestQuietDuringLoadingSchedulesRedetect: a quiet event that lands while the
// instance is still Loading used to be dropped by statusEligible and never
// re-fired — an agent that finished drawing during startup was latched on the
// unconditional Running set by Start. The quiet must instead arm a delayed
// re-check that runs detection once the start flow resolves.
func TestQuietDuringLoadingSchedulesRedetect(t *testing.T) {
	inst := startedInstanceWithProgram(t, "loadquiet", "bash", "$ ")
	t.Setenv("LOOM_PANE_RENDERER", "")
	require.NoError(t, inst.TransitionTo(session.Loading))

	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	_, cmd := m.Update(paneQuietMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd, "quiet during Loading must arm a re-check, not drop")

	// Start flow completes while the re-check is pending.
	require.NoError(t, inst.TransitionTo(session.Running))

	redetect := cmd()
	_, detectCmd := m.Update(redetect)
	require.NotNil(t, detectCmd, "re-check after Loading resolves must run detection")
	msg := detectCmd()
	_, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "expected statusDetectedMsg, got %T", msg)
}
