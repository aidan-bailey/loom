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
// can be added later without growing that list — today it holds nine
// rows: Remote Control, Permission Mode, Model, 1M Context, Headroom Proxy,
// Effort, Cache TTL (1h), Loom Context, and Track Subagents.
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

// claudePrefsRowCount is the number of navigable rows: Remote Control,
// Permission Mode, Model, 1M Context, Headroom Proxy, Effort, Cache TTL
// (1h), Loom Context, and Track Subagents — nine rows.
const claudePrefsRowCount = 9

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
				if v {
					hp := false
					cc.HeadroomProxy = &hp
				}
			})
		case 1:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudePermissionModes, cc.PermissionMode())
				cc.ClaudePermissionMode = &next
			})
		case 2:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudeModelAliases(), cc.Model())
				cc.ClaudeModel = &next
			})
		case 3:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.Context1MEnabled()
				cc.Claude1MContext = &v
			})
		case 4:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.HeadroomProxyEnabled()
				cc.HeadroomProxy = &v
				if v {
					rc := false
					cc.ClaudeRemoteControl = &rc
				}
			})
		case 5:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudeEfforts, cc.Effort())
				cc.ClaudeEffort = &next
			})
		case 6:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.CacheTTL1hEnabled()
				cc.CacheTTL1h = &v
			})
		case 7:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.LoomContextEnabled()
				cc.ClaudeLoomContext = &v
			})
		case 8:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.SubagentTrackingEnabled()
				cc.ClaudeSubagentTracking = &v
			})
		}
		return false, true
	}
	return false, false
}

// nextInList returns the value in list after current, wrapping from the
// last value back to the first. Falls back to list[0] if current isn't
// found (e.g. a value predating this list's current contents).
func nextInList(list []string, current string) string {
	for i, v := range list {
		if v == current {
			return list[(i+1)%len(list)]
		}
	}
	return list[0]
}

var (
	claudePrefsTitleStyle, claudePrefsRowStyle, claudePrefsSelectedStyle,
	claudePrefsHintStyle, claudePrefsBlockedText lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildClaudePrefsStyles) }

func rebuildClaudePrefsStyles() {
	claudePrefsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	claudePrefsRowStyle = lipgloss.NewStyle().Foreground(ui.Text)
	claudePrefsSelectedStyle = lipgloss.NewStyle().Foreground(ui.Accent).Bold(true)
	claudePrefsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	claudePrefsBlockedText = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

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

	modelCursor := "  "
	if c.cursor == 2 {
		modelCursor = "> "
	}
	modelRow := modelCursor + "Model             < " + c.cfg.Model() + " >"
	if c.cursor == 2 {
		modelRow = claudePrefsSelectedStyle.Render(modelRow)
	} else {
		modelRow = claudePrefsRowStyle.Render(modelRow)
	}

	ctxCheck := "[ ]"
	if c.cfg.Context1MEnabled() {
		ctxCheck = "[x]"
	}
	ctxCursor := "  "
	if c.cursor == 3 {
		ctxCursor = "> "
	}
	ctxRow := ctxCursor + "1M Context        " + ctxCheck
	if c.cursor == 3 {
		ctxRow = claudePrefsSelectedStyle.Render(ctxRow)
	} else {
		ctxRow = claudePrefsRowStyle.Render(ctxRow)
	}

	hwCheck := "[ ]"
	if c.cfg.HeadroomProxyEnabled() {
		hwCheck = "[x]"
	}
	hwCursor := "  "
	if c.cursor == 4 {
		hwCursor = "> "
	}
	hwRow := hwCursor + "Headroom Proxy    " + hwCheck
	if c.cursor == 4 {
		hwRow = claudePrefsSelectedStyle.Render(hwRow)
	} else {
		hwRow = claudePrefsRowStyle.Render(hwRow)
	}

	effortCursor := "  "
	if c.cursor == 5 {
		effortCursor = "> "
	}
	effortRow := effortCursor + "Effort            < " + c.cfg.Effort() + " >"
	if c.cursor == 5 {
		effortRow = claudePrefsSelectedStyle.Render(effortRow)
	} else {
		effortRow = claudePrefsRowStyle.Render(effortRow)
	}

	cacheCheck := "[ ]"
	if c.cfg.CacheTTL1hEnabled() {
		cacheCheck = "[x]"
	}
	cacheCursor := "  "
	if c.cursor == 6 {
		cacheCursor = "> "
	}
	cacheRow := cacheCursor + "Cache TTL (1h)    " + cacheCheck
	if c.cursor == 6 {
		cacheRow = claudePrefsSelectedStyle.Render(cacheRow)
	} else {
		cacheRow = claudePrefsRowStyle.Render(cacheRow)
	}

	loomCheck := "[ ]"
	if c.cfg.LoomContextEnabled() {
		loomCheck = "[x]"
	}
	loomCursor := "  "
	if c.cursor == 7 {
		loomCursor = "> "
	}
	loomRow := loomCursor + "Loom Context      " + loomCheck
	if c.cursor == 7 {
		loomRow = claudePrefsSelectedStyle.Render(loomRow)
	} else {
		loomRow = claudePrefsRowStyle.Render(loomRow)
	}

	subCheck := "[ ]"
	if c.cfg.SubagentTrackingEnabled() {
		subCheck = "[x]"
	}
	subCursor := "  "
	if c.cursor == 8 {
		subCursor = "> "
	}
	subRow := subCursor + "Track Subagents   " + subCheck
	if c.cursor == 8 {
		subRow = claudePrefsSelectedStyle.Render(subRow)
	} else {
		subRow = claudePrefsRowStyle.Render(subRow)
	}

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n" +
		modelRow + "\n" +
		ctxRow + "\n" +
		hwRow + "\n" +
		effortRow + "\n" +
		cacheRow + "\n" +
		loomRow + "\n" + subRow + "\n\n" +
		claudePrefsHintStyle.Render("up/down move • enter/space toggle/cycle • esc back")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(c.width)
	return border.Render(content)
}
