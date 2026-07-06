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

func TestSessionLaunchOptionsHeadroomWrapExcludesRemoteControl(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	for i := 0; i < 3; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // toggle Headroom Wrap on

	assert.True(t, lo.Options().HeadroomWrap)
	assert.False(t, lo.Options().RemoteControl, "enabling Headroom Wrap must disable Remote Control")
}

func TestSessionLaunchOptionsRemoteControlExcludesHeadroomWrap(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{HeadroomWrap: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // row 0: toggle Remote Control on

	assert.True(t, lo.Options().RemoteControl)
	assert.False(t, lo.Options().HeadroomWrap, "enabling Remote Control must disable Headroom Wrap")
}

func TestSessionLaunchOptionsRowNavigationClamps(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"}) // up from row 0 stays at row 0
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, lo.Options().RemoteControl)

	for i := 0; i < 6; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // clamps at row 4 (Effort)
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "low", lo.Options().Effort)
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
	l := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	// Move to row 4 (Effort): down x4 from row 0.
	for i := 0; i < 4; i++ {
		l.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "low", l.Options().Effort)
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "medium", l.Options().Effort)
}

func TestSessionLaunchOptionsRendersAllFourRows(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "plan", Model: "opus", HeadroomWrap: false}, false, "")
	rendered := lo.Render()
	assert.Contains(t, rendered, "Remote Control")
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
	assert.Contains(t, rendered, "Headroom Wrap")
}
