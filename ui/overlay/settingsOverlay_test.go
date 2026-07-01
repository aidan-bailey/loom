package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func newTestSettingsCfg() *config.Config {
	return &config.Config{
		DefaultProgram:     "claude",
		AutoYes:            false,
		DaemonPollInterval: 1000,
		BranchPrefix:       "aidan/",
	}
}

func TestSettingsOverlayNavigation(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	assert.Equal(t, 0, so.cursor)

	so.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, 1, so.cursor)

	so.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.Equal(t, 0, so.cursor)
}

func TestSettingsOverlayNavigationDoesNotOverflow(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	for i := 0; i < 20; i++ {
		so.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	assert.Equal(t, int(settingsFieldCount)-1, so.cursor)

	for i := 0; i < 20; i++ {
		so.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	assert.Equal(t, 0, so.cursor)
}

func TestSettingsOverlayTogglesAutoYes(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	so.cursor = int(settingsFieldAutoYes)

	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, closed)
	assert.True(t, changed)
	assert.True(t, cfg.AutoYes)

	closed, changed = so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, closed)
	assert.True(t, changed)
	assert.False(t, cfg.AutoYes)
}

func TestSettingsOverlayEscCloses(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}
