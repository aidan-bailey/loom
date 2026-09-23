package tmux

import (
	"context"
	"strings"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// siblingCmd runs in the live sibling: 60 lines of scroll-back (so a seed
// capture would pick them up) and a visible marker, then waits.
const siblingCmd = "i=0; while [ $i -lt 60 ]; do echo SIBLING-$i; i=$((i+1)); done; exec sleep 300"

// clientsOn returns the sessions the private server's clients are
// attached to.
func clientsOn(t *testing.T) []string {
	t.Helper()
	out, err := Command(context.Background(), "list-clients", "-F", "#{client_session}").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// TestDeadSessionNeverReachesPrefixSibling_RealTmux is the regression for
// the preview attach landing on another agent. With only loom_api-v2 alive,
// a bare `attach-session -t loom_api` prefix-matched it: the dead api's
// preview PTY attached to api-v2, SeedHistory and the emulator showed
// api-v2's screen, and every keystroke loom then sent "to api" (an initial
// prompt, SendPrompt, inline attach) was typed into api-v2's agent. The
// pane-typed reads (capture-pane, display-message) matched it the same way.
func TestDeadSessionNeverReachesPrefixSibling_RealTmux(t *testing.T) {
	privateTmux(t, "t")
	sibling := ToLoomTmuxName("api-v2")
	newRawSession(t, sibling, siblingCmd)
	require.Eventually(t, func() bool {
		out, _ := Command(context.Background(), "capture-pane", "-p", "-t", PaneTarget(sibling)).Output()
		return strings.Contains(string(out), "SIBLING-59")
	}, 5*time.Second, 50*time.Millisecond, "the sibling never printed its marker")

	dead := NewTmuxSession("api", "sh") // never started: loom_api does not exist
	assert.Equal(t, LivenessDead, dead.SessionLiveness())

	_, err := dead.CapturePaneContent()
	assert.Error(t, err, "capture-pane of a dead session must fail, not read the sibling")
	hist, ok := dead.CaptureHistory()
	assert.False(t, ok, "CaptureHistory of a dead session must fail; got %q", hist)
	assert.False(t, dead.IsAlternateScreen())

	_ = dead.Restore() // pty.Start succeeds; the attach client itself must then fail
	t.Cleanup(func() { _ = dead.PausePreview() })
	assert.NotContains(t, strings.Join(dead.SeedHistory(), "\n"), "SIBLING", "Restore seeded the sibling's history")
	// A client that prefix-matched would appear within a few hundred ms.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		require.NotContains(t, clientsOn(t), sibling, "the dead session's preview PTY attached to %s", sibling)
		if screen, ok := dead.RenderEmulator(); ok {
			require.NotContains(t, screen, "SIBLING", "the dead session's emulator shows the sibling's screen")
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Equal(t, LivenessDead, dead.SessionLiveness())
	assert.Equal(t, []string{sibling}, serverSessions(t), "the sibling must be untouched")
}

// TestFullScreenAttachCmd_TargetsExactly: the full-screen attach hands the
// user's real terminal to this command, so it must name the session
// exactly too.
func TestFullScreenAttachCmd_TargetsExactly(t *testing.T) {
	privateTmux(t, "f")
	sibling := ToLoomTmuxName("api-v2")
	newRawSession(t, sibling, "exec sleep 300")

	cmd := NewTmuxSession("api", "sh").FullScreenAttachCmd()
	out, err := cmd.CombinedOutput()
	assert.Error(t, err, "attaching to the dead session must fail, not attach to %s", sibling)
	assert.Contains(t, string(out), "can't find session")
}

// TestRenameLegacySessions_ExactTarget: rename-session -t claudesquad_api
// renamed claudesquad_api-v2 to loom_api when no claudesquad_api existed,
// so api-v2's live session then answered to api's name.
func TestRenameLegacySessions_ExactTarget(t *testing.T) {
	privateTmux(t, "r")
	legacySibling := ToLegacyTmuxName("api-v2")
	newRawSession(t, legacySibling, "exec sleep 300")
	newRawSession(t, ToLegacyTmuxName("web"), "exec sleep 300")

	RenameLegacySessions([]string{"api", "web"}, internalexec.Default{})

	assert.ElementsMatch(t, []string{legacySibling, ToLoomTmuxName("web")}, serverSessions(t),
		"only web's own legacy session is renamed; api-v2's must be left alone")
}

// TestPaneTargets_WorkOnLiveSession pins PaneTarget's shape against the
// installed tmux: "=name" alone fails pane- and window-typed commands
// ("can't find pane"), so Start's set-options and the captures would all
// silently fail if the trailing ':' were dropped.
func TestPaneTargets_WorkOnLiveSession(t *testing.T) {
	privateTmux(t, "p")
	ts := NewTmuxSession("pane targets", "sh")
	require.NoError(t, ts.Start(t.TempDir()))
	t.Cleanup(func() { _ = ts.Close() })

	name := ToLoomTmuxName("pane targets")
	for option, want := range map[string]string{"history-limit": "10000", "mouse": "on", "status": "off"} {
		out, err := Command(context.Background(), "show-options", "-v", "-t", PaneTarget(name), option).CombinedOutput()
		require.NoError(t, err, "show-options %s: %s", option, out)
		assert.Equal(t, want, strings.TrimSpace(string(out)), "Start's set-option %s", option)
	}
	_, err := ts.CapturePaneContent()
	assert.NoError(t, err)
	_, ok := ts.CaptureHistory()
	assert.True(t, ok)

	// Without the ':' tmux reads "=name" as a pane or window name.
	out, err := Command(context.Background(), "capture-pane", "-p", "-t", SessionTarget(name)).CombinedOutput()
	assert.Error(t, err, "capture-pane -t =name: %s", out)
	out, err = Command(context.Background(), "set-option", "-t", SessionTarget(name), "history-limit", "5").CombinedOutput()
	assert.Error(t, err, "set-option -t =name: %s", out)
}
