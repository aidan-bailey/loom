package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func newTestProfilesCfg() *config.Config {
	return &config.Config{
		DefaultProgram: "alpha",
		Profiles: []config.Profile{
			{Name: "alpha", Program: "claude"},
			{Name: "beta", Program: "aider --model gpt-4"},
		},
	}
}

func typeText(t *testing.T, pm *ProfilesManager, text string) {
	t.Helper()
	for _, r := range text {
		pm.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestProfilesManagerAdd(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})
	typeText(t, pm, "gamma")
	pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit name, move to program
	typeText(t, pm, "gemini")
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit program

	assert.True(t, changed)
	assert.Len(t, cfg.Profiles, 3)
	assert.Equal(t, config.Profile{Name: "gamma", Program: "gemini"}, cfg.Profiles[2])
}

func TestProfilesManagerEditProgram(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1 // beta

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'e', Text: "e"})
	// Clear the pre-filled value first.
	for range "aider --model gpt-4" {
		pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(t, pm, "aider --model gpt-5")
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.True(t, changed)
	assert.Equal(t, "aider --model gpt-5", cfg.Profiles[1].Program)
}

func TestProfilesManagerSetDefault(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1 // beta

	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, changed)
	assert.Equal(t, "beta", cfg.DefaultProgram)
}

func TestProfilesManagerDelete(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1 // beta, not the default

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: 'y', Text: "y"})

	assert.True(t, changed)
	assert.Len(t, cfg.Profiles, 1)
	assert.Equal(t, "alpha", cfg.Profiles[0].Name)
}

func TestProfilesManagerDeleteCancelled(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})

	assert.False(t, changed)
	assert.Len(t, cfg.Profiles, 2)
}

func TestProfilesManagerBlocksDeletingDefault(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 0 // alpha, the default

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	assert.Len(t, cfg.Profiles, 2, "delete must be blocked before entering confirm mode")
	assert.Error(t, pm.TakeError())
}

func TestProfilesManagerEscClosesFromBrowsing(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	closed, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}
