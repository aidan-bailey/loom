package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func boolPtr(b bool) *bool { return &b }

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

func TestClaudePreferencesCyclesPermissionMode(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.Equal(t, "default", cfg.PermissionMode())

	// Move focus down to the Permission Mode row.
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.Equal(t, "acceptEdits", cfg.PermissionMode())

	for _, want := range []string{"plan", "auto", "dontAsk", "bypassPermissions", "default"} {
		_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, changed)
		assert.Equal(t, want, cfg.PermissionMode())
	}
}

func TestClaudePreferencesCyclesModel(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.Equal(t, "default", cfg.Model())

	// Move focus down to the Model row (row 2).
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})

	for _, want := range []string{"sonnet", "opus", "fable", "haiku", "default"} {
		_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, changed)
		assert.Equal(t, want, cfg.Model())
	}
}

func TestClaudePreferencesHeadroomProxyExcludesRemoteControl(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.RemoteControlEnabled())

	// Move focus down to the Headroom Proxy row (row 4) and enable it.
	for i := 0; i < 4; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.HeadroomProxyEnabled())
	assert.False(t, cfg.RemoteControlEnabled(), "enabling Headroom Proxy must disable Remote Control")
}

func TestClaudePreferencesRemoteControlExcludesHeadroomProxy(t *testing.T) {
	cfg := &config.Config{HeadroomProxy: boolPtr(true), ClaudeRemoteControl: boolPtr(false)}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.HeadroomProxyEnabled())

	// Row 0 (Remote Control) is already focused by default.
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.RemoteControlEnabled())
	assert.False(t, cfg.HeadroomProxyEnabled(), "enabling Remote Control must disable Headroom Proxy")
}

func TestClaudePreferencesRowNavigationClamps(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")

	// Up from row 0 stays at row 0: toggles Remote Control, not any other row.
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.RemoteControlEnabled())

	// Down eight times stays at row 7 (only eight rows): toggles Loom
	// Context, not any earlier row.
	for i := 0; i < 8; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.LoomContextEnabled())
}

func TestClaudePreferencesRendersPermissionMode(t *testing.T) {
	mode := "plan"
	cfg := &config.Config{ClaudePermissionMode: &mode}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
}

func TestClaudePreferencesRendersModel(t *testing.T) {
	model := "opus"
	cfg := &config.Config{ClaudeModel: &model}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
}

func TestClaudePreferencesRendersHeadroomProxy(t *testing.T) {
	cfg := &config.Config{HeadroomProxy: boolPtr(true)}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Headroom Proxy")
	assert.Contains(t, rendered, "[x]")
}

func TestClaudePreferencesShowsBlockedHint(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, true, "not logged in — run `claude auth login`.")
	rendered := cp.Render()
	assert.Contains(t, rendered, "not logged in")
}

func TestClaudePreferences_EffortRowCycles(t *testing.T) {
	cfg := &config.Config{}
	c := NewClaudePreferences(cfg, false, "")
	for i := 0; i < 5; i++ {
		c.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	c.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "low", cfg.Effort())
}

func TestClaudePreferencesRendersCacheTTL1h(t *testing.T) {
	cfg := &config.Config{CacheTTL1h: boolPtr(true)}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Cache TTL")
	assert.Contains(t, rendered, "[x]")
}

func TestClaudePreferences_CacheTTL1hRowToggles(t *testing.T) {
	cfg := &config.Config{}
	c := NewClaudePreferences(cfg, false, "")
	for i := 0; i < 6; i++ {
		c.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	c.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, cfg.CacheTTL1hEnabled())
	c.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, cfg.CacheTTL1hEnabled())
}

func TestClaudePreferencesEscCloses(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	closed, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}

func TestClaudePreferences_LoomContextToggle(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")

	// Row 7 is Loom Context. Move the cursor there from row 0.
	for i := 0; i < 7; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}

	// Default (nil) is enabled; toggling once turns it off.
	assert.True(t, cfg.LoomContextEnabled())
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.LoomContextEnabled())

	// Render shows the row.
	assert.Contains(t, cp.Render(), "Loom Context")
}

func TestClaudePreferences_Context1MRow(t *testing.T) {
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}

	t.Run("row count", func(t *testing.T) {
		assert.Equal(t, 8, claudePrefsRowCount)
	})

	t.Run("enter on row 3 toggles Claude1MContext", func(t *testing.T) {
		cfg := &config.Config{}
		cp := NewClaudePreferences(cfg, false, "")
		cp.cursor = 3
		_, changed := cp.HandleKeyPress(enter)
		assert.True(t, changed)
		assert.True(t, cfg.Context1MEnabled())
		cp.HandleKeyPress(enter)
		assert.False(t, cfg.Context1MEnabled())
	})

	t.Run("rows below Model shifted down by one", func(t *testing.T) {
		cfg := &config.Config{}
		cp := NewClaudePreferences(cfg, false, "")
		cp.cursor = 4
		cp.HandleKeyPress(enter)
		assert.True(t, cfg.HeadroomProxyEnabled())

		cfg = &config.Config{}
		cp = NewClaudePreferences(cfg, false, "")
		cp.cursor = 5
		cp.HandleKeyPress(enter)
		assert.Equal(t, "low", cfg.Effort())

		cfg = &config.Config{}
		cp = NewClaudePreferences(cfg, false, "")
		cp.cursor = 6
		cp.HandleKeyPress(enter)
		assert.True(t, cfg.CacheTTL1hEnabled())

		cfg = &config.Config{}
		cp = NewClaudePreferences(cfg, false, "")
		cp.cursor = 7
		cp.HandleKeyPress(enter)
		// Loom Context defaults to enabled, so one toggle turns it off.
		assert.False(t, cfg.LoomContextEnabled())
	})

	t.Run("render shows the row", func(t *testing.T) {
		cp := NewClaudePreferences(&config.Config{}, false, "")
		assert.Contains(t, cp.Render(), "1M Context")
	})
}
