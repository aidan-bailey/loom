package tmux

import (
	"testing"

	"github.com/aidan-bailey/loom/session/vt"
	"github.com/stretchr/testify/require"
)

// TestStatusContent_PrefersEmulator: with an emulator wired, status content
// comes from the in-process screen — no capture-pane subprocess.
func TestStatusContent_PrefersEmulator(t *testing.T) {
	ts := NewTmuxSession("status-emu", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	_, _ = ts.emu.Write([]byte("Do you want to proceed?"))

	content, err := ts.statusContent()
	require.NoError(t, err)
	require.Contains(t, content, "Do you want to proceed?")
}

// TestStatusContent_HasUpdatedParity: HasUpdated's change detection behaves
// identically on emulator content — first call hashes (updated), unchanged
// content is not an update, new content is.
func TestStatusContent_HasUpdatedParity(t *testing.T) {
	ts := NewTmuxSession("status-hash", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))

	_, _ = ts.emu.Write([]byte("line one"))
	updated, _ := ts.HasUpdated()
	require.True(t, updated, "first capture is always an update")

	updated, _ = ts.HasUpdated()
	require.False(t, updated, "unchanged screen is not an update")

	_, _ = ts.emu.Write([]byte("\r\nline two"))
	updated, _ = ts.HasUpdated()
	require.True(t, updated, "new output is an update")
}

// TestStatusContent_PromptDetectionParity: pendingPrompt patterns must match
// against emulator-rendered content (which carries SGR escapes, same as
// capture-pane -e output did).
func TestStatusContent_PromptDetectionParity(t *testing.T) {
	ts := NewTmuxSession("status-prompt", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))

	// Feed the claude adapter's pending-prompt marker with SGR styling
	// wrapped around it, as a real agent screen would.
	_, _ = ts.emu.Write([]byte("\x1b[1mNo, and tell Claude what to do differently\x1b[0m"))
	_, hasPrompt := ts.HasUpdated()
	require.True(t, hasPrompt, "prompt marker must be detected in emulator content")
}

func TestCursorState(t *testing.T) {
	ts := NewTmuxSession("cursor", "claude")
	_, ok := ts.CursorState()
	require.False(t, ok, "no emulator → no cursor state")

	ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	_, _ = ts.emu.Write([]byte("\x1b[3;7H"))
	c, ok := ts.CursorState()
	require.True(t, ok)
	require.Equal(t, 6, c.X)
	require.Equal(t, 2, c.Y)
	require.True(t, c.Visible)
}

func TestPaneTitle_PassesValidTitle(t *testing.T) {
	ts := NewTmuxSession("title-valid", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	_, _ = ts.emu.Write([]byte("\x1b]2;claude - working\x07"))
	title, ok := ts.PaneTitle()
	require.True(t, ok)
	require.Equal(t, "claude - working", title)
}

func TestPaneTitle_RejectsInvalidUTF8(t *testing.T) {
	ts := NewTmuxSession("title-invalid", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	// Triggers the vendored x/vt OSC parser's non-ASCII truncation bug (see
	// TestTitle_NonASCIITruncatesInVendoredParser) — PaneTitle must not
	// forward the resulting mangled bytes.
	_, _ = ts.emu.Write([]byte("\x1b]2;✳ claude\x07"))
	_, ok := ts.PaneTitle()
	require.False(t, ok, "invalid UTF-8 title must be rejected, not forwarded")
}
