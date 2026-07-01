package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestClaudePreferencesTogglesRemoteControl(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.RemoteControlEnabled(), "nil ClaudeRemoteControl defaults to enabled")

	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.RemoteControlEnabled())

	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, changed)
	assert.True(t, cfg.RemoteControlEnabled())
}

func TestClaudePreferencesShowsBlockedHint(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, true, "not logged in — run `claude auth login`.")
	rendered := cp.Render()
	assert.Contains(t, rendered, "not logged in")
}

func TestClaudePreferencesEscCloses(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	closed, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}
