package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ClaudePreferences is the Claude-specific preferences drill-in
// sub-screen. Structured as its own screen (rather than a flat row on
// the main settings list) so more Claude-adapter-specific preferences
// can be added later without growing that list — today it holds one
// row.
//
// authBlocked/authReason mirror session.RemoteControlAuth.Blocked()/
// Reason, passed as plain values so this package stays decoupled from
// session (matching SettingsOverlay and every other overlay). They are
// a snapshot taken once at startup by the caller (m.rcAuth) — toggling
// Remote Control here does not re-probe auth; the existing
// session-creation-time gating (app/remote_control.go) already handles
// the incompatible-auth case once the toggle takes effect.
type ClaudePreferences struct {
	cfg         *config.Config
	authBlocked bool
	authReason  string
	width       int
}

// NewClaudePreferences creates the Claude Preferences sub-screen over cfg.
func NewClaudePreferences(cfg *config.Config, authBlocked bool, authReason string) *ClaudePreferences {
	return &ClaudePreferences{cfg: cfg, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// SetWidth sets the render width.
func (c *ClaudePreferences) SetWidth(w int) { c.width = w }

// HandleKeyPress processes one key press. closed reports whether the
// sub-screen should return control to the parent SettingsOverlay;
// changed reports whether cfg was mutated.
func (c *ClaudePreferences) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.String() {
	case "esc", "q":
		return true, false
	case " ", "space", "enter":
		c.cfg.Mutate(func(cc *config.Config) {
			v := !cc.RemoteControlEnabled()
			cc.ClaudeRemoteControl = &v
		})
		return false, true
	}
	return false, false
}

var (
	claudePrefsTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	claudePrefsRowStyle    = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	claudePrefsHintStyle   = lipgloss.NewStyle().Foreground(ui.TextHint)
	claudePrefsBlockedText = lipgloss.NewStyle().Foreground(ui.DangerAccent)
)

// Render renders the sub-screen.
func (c *ClaudePreferences) Render() string {
	check := "[ ]"
	if c.cfg.RemoteControlEnabled() {
		check = "[x]"
	}
	row := claudePrefsRowStyle.Render("Remote Control    " + check)
	if c.authBlocked {
		row += "  " + claudePrefsBlockedText.Render("(blocked: "+c.authReason+")")
	}

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		row + "\n\n" +
		claudePrefsHintStyle.Render("enter/space toggle • esc back")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(c.width)
	return border.Render(content)
}
