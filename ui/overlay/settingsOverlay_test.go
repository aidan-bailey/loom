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

func TestSettingsOverlayEscCloses(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}

// submitText opens the editor for field, clears whatever value it was
// pre-filled with, types text, and submits. The clear step matters:
// NewTextInputOverlay pre-fills the textarea with the field's current
// value and leaves the cursor at the end, so typed runes append rather
// than replace — without clearing first, editing "aidan/" to "team/"
// would actually produce "aidan/team/".
func submitText(t *testing.T, so *SettingsOverlay, field settingsField, text string) (closed, changed bool) {
	t.Helper()
	so.cursor = int(field)
	so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // open the edit
	for i := 0; i < 64; i++ {                              // clear the pre-filled value regardless of its length
		so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range text {
		so.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit
}

func TestSettingsOverlayEditsBranchPrefix(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldBranchPrefix, "team/")
	assert.True(t, changed)
	assert.Equal(t, "team/", cfg.BranchPrefix)
}

func TestSettingsOverlayEditsDefaultProgram(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldDefaultProgram, "aider")
	assert.True(t, changed)
	assert.Equal(t, "aider", cfg.DefaultProgram)
}

func TestSettingsOverlayEditsDaemonPollInterval(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldDaemonPollInterval, "2000")
	assert.True(t, changed)
	assert.Equal(t, 2000, cfg.DaemonPollInterval)
}

func TestSettingsOverlayRejectsNonNumericPollInterval(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldDaemonPollInterval, "notanumber")
	assert.False(t, changed)
	assert.Equal(t, 1000, cfg.DaemonPollInterval, "invalid input must not overwrite the existing value")
	assert.Error(t, so.TakeError())
}

func TestSettingsOverlayEscCancelsTextEdit(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	so.cursor = int(settingsFieldBranchPrefix)
	so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.False(t, closed, "Esc from a text edit returns to browsing, not to the caller")
	assert.False(t, changed)
	assert.Equal(t, "aidan/", cfg.BranchPrefix)
	assert.Equal(t, settingsBrowsing, so.mode)
}
