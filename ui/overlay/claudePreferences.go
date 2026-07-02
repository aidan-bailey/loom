package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ClaudePreferences is the Claude-specific preferences drill-in
// sub-screen. Structured as its own screen (rather than flat rows on
// the main settings list) so more Claude-adapter-specific preferences
// can be added later without growing that list — today it holds two
// rows: Remote Control and Permission Mode.
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
	cursor      int
}

// claudePrefsRowCount is the number of navigable rows: Remote Control
// and Permission Mode.
const claudePrefsRowCount = 2

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
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
		return false, false
	case "down", "j":
		if c.cursor < claudePrefsRowCount-1 {
			c.cursor++
		}
		return false, false
	case " ", "space", "enter":
		switch c.cursor {
		case 0:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.RemoteControlEnabled()
				cc.ClaudeRemoteControl = &v
			})
		case 1:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextPermissionMode(cc.PermissionMode())
				cc.ClaudePermissionMode = &next
			})
		}
		return false, true
	}
	return false, false
}

// nextPermissionMode returns the value in config.ClaudePermissionModes
// after current, wrapping from the last value back to the first.
func nextPermissionMode(current string) string {
	modes := config.ClaudePermissionModes
	for i, m := range modes {
		if m == current {
			return modes[(i+1)%len(modes)]
		}
	}
	return modes[0]
}

var (
	claudePrefsTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	claudePrefsRowStyle      = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	claudePrefsSelectedStyle = lipgloss.NewStyle().Foreground(ui.TitleAccent).Bold(true)
	claudePrefsHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
	claudePrefsBlockedText   = lipgloss.NewStyle().Foreground(ui.DangerAccent)
)

// Render renders the sub-screen.
func (c *ClaudePreferences) Render() string {
	check := "[ ]"
	if c.cfg.RemoteControlEnabled() {
		check = "[x]"
	}
	rcCursor := "  "
	if c.cursor == 0 {
		rcCursor = "> "
	}
	rcRow := rcCursor + "Remote Control    " + check
	if c.authBlocked {
		rcRow += "  " + claudePrefsBlockedText.Render("(blocked: "+c.authReason+")")
	}
	if c.cursor == 0 {
		rcRow = claudePrefsSelectedStyle.Render(rcRow)
	} else {
		rcRow = claudePrefsRowStyle.Render(rcRow)
	}

	pmCursor := "  "
	if c.cursor == 1 {
		pmCursor = "> "
	}
	pmRow := pmCursor + "Permission Mode   < " + c.cfg.PermissionMode() + " >"
	if c.cursor == 1 {
		pmRow = claudePrefsSelectedStyle.Render(pmRow)
	} else {
		pmRow = claudePrefsRowStyle.Render(pmRow)
	}

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n\n" +
		claudePrefsHintStyle.Render("up/down move • enter/space toggle/cycle • esc back")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(c.width)
	return border.Render(content)
}
