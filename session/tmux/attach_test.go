package tmux

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// screenRunner is rec's executor with capture-pane answering screen. That
// is the snapshot path's view of the pane, used when there is no emulator.
func screenRunner(rec *argvRecorder, screen string) cmd_test.MockCmdExec {
	e := rec.runner()
	e.OutputFunc = func(c *exec.Cmd) ([]byte, error) {
		rec.mu.Lock()
		rec.runs = append(rec.runs, slices.Clone(c.Args))
		rec.mu.Unlock()
		return []byte(screen), nil
	}
	return e
}

func TestNewAttachClient_TakesTheSessionNameVerbatim(t *testing.T) {
	c := NewAttachClientWithDeps("loom_api", "claude", NewMockPtyFactory(t), cmd_test.MockCmdExec{})
	assert.Equal(t, "loom_api", c.SessionName(), "already a session name: never re-prefixed")
}

func TestNewAttachClient_RestoreAttachesByExactName(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_api", "claude", ptyFactory, rec.runner())

	require.NoError(t, c.Restore())
	t.Cleanup(func() { _ = c.PausePreview() })

	require.Len(t, ptyFactory.cmds, 1)
	assert.Equal(t, []string{"tmux", "attach-session", "-t", "=loom_api"}, ptyFactory.cmds[0].Args)
	assert.Empty(t, rec.ran("new-session"), "a client launches nothing")
	assert.True(t, c.PtmxAlive())
}

func TestDetectStatus_DefaultAdapterOnlyChecksForChange(t *testing.T) {
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_sh", "bash", NewMockPtyFactory(t), screenRunner(rec, "Do you trust the files in this folder?"))

	updated, hasPrompt, err := c.DetectStatus()
	require.NoError(t, err)
	assert.True(t, updated, "the first scan sees new content")
	assert.False(t, hasPrompt)

	updated, _, err = c.DetectStatus()
	require.NoError(t, err)
	assert.False(t, updated, "unchanged content")
	assert.Empty(t, rec.ran("send-keys"), "an agent without patterns gets no trust answer")
}

func TestDetectStatus_AnswersTheTrustPrompt(t *testing.T) {
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_cl", "claude", NewMockPtyFactory(t), screenRunner(rec, "Do you trust the files in this folder?\n❯ 1. Yes, proceed"))

	updated, _, err := c.DetectStatus()

	require.NoError(t, err)
	assert.True(t, updated)
	assert.Equal(t, [][]string{{"tmux", "send-keys", "-t", "=loom_cl:", "Enter"}}, rec.ran("send-keys"))
}

func TestDetectStatus_ReportsThePendingPrompt(t *testing.T) {
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_cl", "claude", NewMockPtyFactory(t), screenRunner(rec, "  3. No, and tell Claude what to do differently"))

	_, hasPrompt, err := c.DetectStatus()

	require.NoError(t, err)
	assert.True(t, hasPrompt)
}

func TestDetectStatus_SurfacesACaptureFailure(t *testing.T) {
	rec := &argvRecorder{}
	e := rec.runner()
	e.OutputFunc = func(*exec.Cmd) ([]byte, error) { return nil, errors.New("no server running") }
	c := NewAttachClientWithDeps("loom_cl", "claude", NewMockPtyFactory(t), e)

	_, _, err := c.DetectStatus()

	require.Error(t, err)
}

// TestAttachClient_RendersASessionItDidNotStart_RealTmux: the TUI's client
// attaches by name to a session that lifecycle launched through a Session.
// No object is shared between the two.
func TestAttachClient_RendersASessionItDidNotStart_RealTmux(t *testing.T) {
	privateTmux(t, "ac")
	if !EmulatorEnabled() {
		t.Skip("emulator disabled (LOOM_PANE_RENDERER=snapshot)")
	}
	s := NewSession("ac", "sh -c 'echo attached-by-name; exec sleep 60'")
	require.NoError(t, s.Start(t.TempDir()))
	t.Cleanup(func() { _ = s.Close() })

	c := NewAttachClient(s.SessionName(), "sh")
	require.NoError(t, c.Restore())
	t.Cleanup(func() { _ = c.PausePreview() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if screen, ok := c.RenderEmulator(); ok && strings.Contains(screen, "attached-by-name") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the client never rendered the session's output")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
