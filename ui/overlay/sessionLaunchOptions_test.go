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

	for i := 0; i < 3; i++ {
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
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"}) // up from row 0 stays at row 0
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, lo.Options().RemoteControl)

	for i := 0; i < 5; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // clamps at row 3
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, lo.Options().HeadroomProxy)
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

func TestSessionLaunchOptionsRendersAllFourRows(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "plan", Model: "opus", HeadroomProxy: false}, false, "")
	rendered := lo.Render()
	assert.Contains(t, rendered, "Remote Control")
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
	assert.Contains(t, rendered, "Headroom Proxy")
}
