package overlay

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestSessionLaunchOptionsTogglesRemoteControl(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default"}, false, "")

	_, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, confirmed)
	assert.False(t, lo.Options().RemoteControl)
}

func TestSessionLaunchOptionsCyclesPermissionModeAndModel(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{PermissionMode: "default", Model: "default"}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // row 1: Permission Mode
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "acceptEdits", lo.Options().PermissionMode)

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // row 2: Model
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "sonnet", lo.Options().Model)
}

func TestSessionLaunchOptionsHeadroomProxyExcludesRemoteControl(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	for i := 0; i < 4; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // toggle Headroom Proxy on

	assert.True(t, lo.Options().HeadroomProxy)
	assert.False(t, lo.Options().RemoteControl, "enabling Headroom Proxy must disable Remote Control")
}

func TestSessionLaunchOptionsRemoteControlExcludesHeadroomProxy(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{HeadroomProxy: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // row 0: toggle Remote Control on

	assert.True(t, lo.Options().RemoteControl)
	assert.False(t, lo.Options().HeadroomProxy, "enabling Remote Control must disable Headroom Proxy")
}

func TestSessionLaunchOptionsRowNavigationClamps(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, Effort: "default"}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"}) // up from row 0 stays at row 0
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, lo.Options().RemoteControl)

	for i := 0; i < sessionLaunchOptionsRowCount+2; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // overshoot the bottom
	}
	// If the cursor clamped at the last row, one step up is Cache TTL. If it
	// ran past the end instead, this space lands on nothing.
	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, lo.Options().CacheTTL1h)
}

func TestSessionLaunchOptionsEnterConfirms(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, closed)
	assert.True(t, confirmed)
}

func TestSessionLaunchOptionsEscCancels(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, confirmed)
}

func TestSessionLaunchOptionsShowsBlockedHint(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, true, "not logged in")
	rendered := lo.Render()
	assert.Contains(t, rendered, "not logged in")
}

func TestSessionLaunchOptions_EffortRowCycles(t *testing.T) {
	l := NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
	// Move to row 5 (Effort): down x5 from row 0.
	for i := 0; i < 5; i++ {
		l.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "low", l.Options().Effort)
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "medium", l.Options().Effort)
}

func TestSessionLaunchOptionsRendersAllRows(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "plan", Model: "opus", HeadroomProxy: false, Effort: "high", CacheTTL1h: true, BranchPrefix: "aidanb/"}, false, "")
	rendered := lo.Render()
	assert.Contains(t, rendered, "Remote Control")
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
	assert.Contains(t, rendered, "Headroom Proxy")
	assert.Contains(t, rendered, "Effort")
	assert.Contains(t, rendered, "high")
	assert.Contains(t, rendered, "Cache TTL")
	assert.Contains(t, rendered, "Branch Prefix")
	assert.Contains(t, rendered, "aidanb/")
}

func TestSessionLaunchOptions_CacheTTL1hRowToggles(t *testing.T) {
	l := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	// Move to row 6 (Cache TTL): down x6 from row 0.
	for i := 0; i < 6; i++ {
		l.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, l.Options().CacheTTL1h)
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, l.Options().CacheTTL1h)
}

func TestSessionLaunchOptions_Context1MRow(t *testing.T) {
	t.Run("row count", func(t *testing.T) {
		assert.Equal(t, 8, sessionLaunchOptionsRowCount)
	})

	t.Run("space on row 3 toggles Context1M", func(t *testing.T) {
		l := NewSessionLaunchOptions(LaunchOptions{Model: "sonnet"}, false, "")
		l.cursor = 3
		l.toggleCursor()
		assert.True(t, l.opts.Context1M)
		l.toggleCursor()
		assert.False(t, l.opts.Context1M)
	})

	t.Run("toggling 1M leaves every other option alone", func(t *testing.T) {
		before := LaunchOptions{
			RemoteControl:  true,
			PermissionMode: "plan",
			Model:          "opus",
			HeadroomProxy:  false,
			Effort:         "high",
			CacheTTL1h:     true,
		}
		l := NewSessionLaunchOptions(before, false, "")
		l.cursor = 3
		l.toggleCursor()
		assert.Equal(t, before.RemoteControl, l.opts.RemoteControl)
		assert.Equal(t, before.PermissionMode, l.opts.PermissionMode)
		assert.Equal(t, before.Model, l.opts.Model)
		assert.Equal(t, before.HeadroomProxy, l.opts.HeadroomProxy)
		assert.Equal(t, before.Effort, l.opts.Effort)
		assert.Equal(t, before.CacheTTL1h, l.opts.CacheTTL1h)
	})

	t.Run("rows below Model shifted down by one", func(t *testing.T) {
		l := NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
		l.cursor = 4
		l.toggleCursor()
		assert.True(t, l.opts.HeadroomProxy)

		l = NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
		l.cursor = 5
		l.toggleCursor()
		assert.Equal(t, "low", l.opts.Effort)

		l = NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
		l.cursor = 6
		l.toggleCursor()
		assert.True(t, l.opts.CacheTTL1h)
	})

	t.Run("render shows the row and its state", func(t *testing.T) {
		l := NewSessionLaunchOptions(LaunchOptions{Model: "sonnet", Context1M: true}, false, "")
		assert.Contains(t, l.Render(), "1M Context")
	})
}

// typeInto sends each rune of s to the modal as a key press.
func typeInto(lo *SessionLaunchOptions, s string) {
	for _, r := range s {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// toBranchPrefixRow moves the cursor to the Branch Prefix row (the last one).
func toBranchPrefixRow(lo *SessionLaunchOptions) {
	for i := 0; i < sessionLaunchOptionsRowCount-1; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
}

func TestSessionLaunchOptionsEditsBranchPrefix(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{BranchPrefix: "aidanb/"}, false, "")
	toBranchPrefixRow(lo)

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // enter edit mode
	for i := 0; i < len("aidanb/"); i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeInto(lo, "spike/")
	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.False(t, closed, "enter must commit the edit, not start the session")
	assert.False(t, confirmed)
	assert.Equal(t, "spike/", lo.Options().BranchPrefix)
}

// TestSessionLaunchOptionsEditingSwallowsEnterAndEsc is the regression guard
// for the one genuinely new mechanic here: every other row is a toggle, so
// enter/esc could safely mean confirm/cancel. Once a row accepts text they
// must not reach the modal.
func TestSessionLaunchOptionsEditingSwallowsEnterAndEsc(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{BranchPrefix: "aidanb/"}, false, "")
	toBranchPrefixRow(lo)
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})

	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.False(t, closed, "esc must cancel the edit, not the modal")
	assert.False(t, confirmed)
	assert.Equal(t, "aidanb/", lo.Options().BranchPrefix, "esc must discard the edit")

	// Back on the row list, esc means cancel again.
	closed, confirmed = lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, confirmed)
}

func TestSessionLaunchOptionsAcceptsEmptyBranchPrefix(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{BranchPrefix: "aidanb/"}, false, "")
	toBranchPrefixRow(lo)
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	for i := 0; i < len("aidanb/"); i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, "", lo.Options().BranchPrefix, "clearing the prefix is a legitimate choice")
}

// TestSessionLaunchOptionsBranchPrefixLockedOnRestart pins the restart path:
// the branch already exists, so the row shows it but refuses edits.
func TestSessionLaunchOptionsBranchPrefixLockedOnRestart(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{BranchPrefix: "aidanb/"}, false, "")
	lo.SetBranchPrefixLocked("aidanb/existing-session")
	toBranchPrefixRow(lo)

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	typeInto(lo, "zzz")
	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, "aidanb/", lo.Options().BranchPrefix, "a locked row must not be editable")
	assert.True(t, closed, "enter still starts the session when no edit is in progress")
	assert.True(t, confirmed)
	assert.Contains(t, lo.Render(), "aidanb/existing-session")
}

// TestSessionLaunchOptionsEditingRendersEditorAlone pins the house pattern
// SettingsOverlay.Render uses: the nested TextInputOverlay draws its own
// complete bordered box, so it must replace the modal rather than be nested
// inside the modal's border.
func TestSessionLaunchOptionsEditingRendersEditorAlone(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{BranchPrefix: "aidanb/"}, false, "")
	lo.SetWidth(60)
	toBranchPrefixRow(lo)
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})

	rendered := lo.Render()
	assert.Contains(t, rendered, "Branch Prefix")
	assert.NotContains(t, rendered, "Headroom Proxy", "the row list must not render behind the editor")
	assert.NotContains(t, rendered, "Session Launch Options", "the editor replaces the modal, it is not nested in it")
}
