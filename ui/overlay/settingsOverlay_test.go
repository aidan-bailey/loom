package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func newTestSettingsCfg() *config.Config {
	return &config.Config{Settings: config.Settings{
		DefaultProgram: "claude",
		BranchPrefix:   "aidan/",
	}}
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

func TestSettingsOverlay_ThemeRowCycles(t *testing.T) {
	defer ui.ApplyTheme(ui.DefaultThemeName)
	cfg := newTestSettingsCfg()
	cfg.Theme = "afterglow"
	so := NewSettingsOverlay(cfg, false, "")

	so.cursor = int(settingsFieldTheme)
	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, closed)
	assert.True(t, changed)
	assert.Equal(t, "legacy", cfg.GetTheme())
	assert.Equal(t, "legacy", ui.CurrentThemeName())

	// Cycles back around.
	_, changed = so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.Equal(t, "afterglow", cfg.GetTheme())
}

// A hand-edited config naming an unknown theme must still cycle onto a
// valid registered name rather than wedging the row.
func TestSettingsOverlay_ThemeRowCyclesFromUnknownName(t *testing.T) {
	defer ui.ApplyTheme(ui.DefaultThemeName)
	cfg := newTestSettingsCfg()
	cfg.Theme = "bogus"
	so := NewSettingsOverlay(cfg, false, "")

	so.cursor = int(settingsFieldTheme)
	_, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.Contains(t, ui.ThemeNames(), cfg.GetTheme())
}

// Pins the cross-package invariant promised by the comment on
// config.DefaultConfig's Theme field (config cannot import ui, so only
// this package can assert it).
func TestDefaultConfigThemeMatchesUIDefault(t *testing.T) {
	assert.Equal(t, ui.DefaultThemeName, config.DefaultConfig().Theme)
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

func TestSettingsOverlay_BuildSweepRowShowsTheGlobalSetting(t *testing.T) {
	so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	assert.Contains(t, so.Render(), "off (all workspaces)")
	so.SetBuildSweep(12)
	assert.Contains(t, so.Render(), "12h (all workspaces)")
}

func TestSettingsOverlay_BuildSweepEditStartsFromTheCurrentValue(t *testing.T) {
	so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	so.SetBuildSweep(24)
	so.cursor = int(settingsFieldBuildSweep)
	so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "24", so.editing.GetValue())
}

// TestSettingsOverlay_BuildSweepEditIsARequestNotAWorkspaceSetting: the
// threshold is global, so the edit goes to the caller and the workspace's
// settings neither change nor save.
func TestSettingsOverlay_BuildSweepEditIsARequestNotAWorkspaceSetting(t *testing.T) {
	for in, want := range map[string]int{" 12 ": 12, "0": 0} {
		t.Run(in, func(t *testing.T) {
			cfg := newTestSettingsCfg()
			so := NewSettingsOverlay(cfg, false, "")
			so.SetBuildSweep(6)

			_, changed := submitText(t, so, settingsFieldBuildSweep, in)

			assert.False(t, changed, "the workspace's settings are not saved")
			assert.Zero(t, cfg.BuildSweepAfterHours, "nor changed")
			assert.NoError(t, so.TakeError())
			hours, ok := so.TakeBuildSweepRequest()
			assert.True(t, ok)
			assert.Equal(t, want, hours)
			_, ok = so.TakeBuildSweepRequest()
			assert.False(t, ok, "taken once")
		})
	}
}

func TestSettingsOverlay_BuildSweepRefusesABadValue(t *testing.T) {
	for _, bad := range []string{"abc", "-1", "8761", "1.5", ""} {
		t.Run(bad, func(t *testing.T) {
			so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
			submitText(t, so, settingsFieldBuildSweep, bad)
			assert.Error(t, so.TakeError())
			_, ok := so.TakeBuildSweepRequest()
			assert.False(t, ok)
		})
	}
}
