package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// LaunchOptions holds the six per-session launch toggles. Defined
// here (rather than in app) so it's usable both by
// SessionLaunchOptions (ephemeral, edited as a plain value) and by
// app's launch-command composition, without an import cycle back to
// app.
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	HeadroomProxy  bool
	Effort         string
	CacheTTL1h     bool
}

// SessionLaunchOptions is the per-instance "Session Launch Options"
// modal shown right before a new session starts. Unlike
// ClaudePreferences, which edits *config.Config directly and persists
// on every change, this edits a local LaunchOptions value that the
// caller applies to just one instance — closing without saving
// anything to disk.
type SessionLaunchOptions struct {
	opts        LaunchOptions
	authBlocked bool
	authReason  string
	width       int
	cursor      int
}

// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, Headroom Proxy, Effort, and Cache
// TTL (1h).
const sessionLaunchOptionsRowCount = 6

// NewSessionLaunchOptions creates the modal seeded with initial
// (typically the global config's current values).
func NewSessionLaunchOptions(initial LaunchOptions, authBlocked bool, authReason string) *SessionLaunchOptions {
	return &SessionLaunchOptions{opts: initial, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// SetWidth sets the render width.
func (l *SessionLaunchOptions) SetWidth(w int) { l.width = w }

// Options returns the current (possibly edited) launch options.
func (l *SessionLaunchOptions) Options() LaunchOptions { return l.opts }

// HandleKeyPress processes one key press. closed reports whether the
// modal should close (either canceled or confirmed); confirmed
// distinguishes the two — the caller only applies Options() and starts
// the instance when confirmed is true.
func (l *SessionLaunchOptions) HandleKeyPress(msg tea.KeyPressMsg) (closed, confirmed bool) {
	switch msg.String() {
	case "esc", "q":
		return true, false
	case "enter":
		return true, true
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
		return false, false
	case "down", "j":
		if l.cursor < sessionLaunchOptionsRowCount-1 {
			l.cursor++
		}
		return false, false
	case " ", "space":
		l.toggleCursor()
		return false, false
	}
	return false, false
}

// toggleCursor applies the toggle/cycle action for the focused row,
// enforcing the same Remote-Control/Headroom-Proxy exclusivity rule as
// ClaudePreferences.
func (l *SessionLaunchOptions) toggleCursor() {
	switch l.cursor {
	case 0:
		l.opts.RemoteControl = !l.opts.RemoteControl
		if l.opts.RemoteControl {
			l.opts.HeadroomProxy = false
		}
	case 1:
		l.opts.PermissionMode = nextInList(config.ClaudePermissionModes, l.opts.PermissionMode)
	case 2:
		l.opts.Model = nextInList(config.ClaudeModels, l.opts.Model)
	case 3:
		l.opts.HeadroomProxy = !l.opts.HeadroomProxy
		if l.opts.HeadroomProxy {
			l.opts.RemoteControl = false
		}
	case 4:
		l.opts.Effort = nextInList(config.ClaudeEfforts, l.opts.Effort)
	case 5:
		l.opts.CacheTTL1h = !l.opts.CacheTTL1h
	}
}

var (
	sessionLaunchOptionsTitleStyle, sessionLaunchOptionsRowStyle,
	sessionLaunchOptionsSelectedStyle, sessionLaunchOptionsHintStyle,
	sessionLaunchOptionsBlockedText lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildSessionLaunchOptionsStyles) }

func rebuildSessionLaunchOptionsStyles() {
	sessionLaunchOptionsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	sessionLaunchOptionsRowStyle = lipgloss.NewStyle().Foreground(ui.Text)
	sessionLaunchOptionsSelectedStyle = lipgloss.NewStyle().Foreground(ui.Accent).Bold(true)
	sessionLaunchOptionsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	sessionLaunchOptionsBlockedText = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

// Render renders the modal.
func (l *SessionLaunchOptions) Render() string {
	row := func(idx int, label, value string) string {
		cursor := "  "
		if l.cursor == idx {
			cursor = "> "
		}
		line := cursor + label + value
		if idx == 0 && l.authBlocked {
			line += "  " + sessionLaunchOptionsBlockedText.Render("(blocked: "+l.authReason+")")
		}
		if l.cursor == idx {
			return sessionLaunchOptionsSelectedStyle.Render(line)
		}
		return sessionLaunchOptionsRowStyle.Render(line)
	}

	rcCheck := "[ ]"
	if l.opts.RemoteControl {
		rcCheck = "[x]"
	}
	hwCheck := "[ ]"
	if l.opts.HeadroomProxy {
		hwCheck = "[x]"
	}
	cacheCheck := "[ ]"
	if l.opts.CacheTTL1h {
		cacheCheck = "[x]"
	}

	content := sessionLaunchOptionsTitleStyle.Render("Session Launch Options") + "\n\n" +
		row(0, "Remote Control    ", rcCheck) + "\n" +
		row(1, "Permission Mode   ", "< "+l.opts.PermissionMode+" >") + "\n" +
		row(2, "Model             ", "< "+l.opts.Model+" >") + "\n" +
		row(3, "Headroom Proxy    ", hwCheck) + "\n" +
		row(4, "Effort            ", "< "+l.opts.Effort+" >") + "\n" +
		row(5, "Cache TTL (1h)    ", cacheCheck) + "\n\n" +
		sessionLaunchOptionsHintStyle.Render("up/down move • space toggle/cycle • enter start • esc cancel")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(l.width)
	return border.Render(content)
}

// HandleKey satisfies the Overlay interface. State handlers that need
// the confirmed signal call HandleKeyPress directly instead (mirrors
// SettingsOverlay.HandleKey/HandleKeyPress).
func (l *SessionLaunchOptions) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := l.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface.
func (l *SessionLaunchOptions) SetSize(width, _ int) {
	l.width = width
}

// View satisfies the Overlay interface.
func (l *SessionLaunchOptions) View() string {
	return l.Render()
}
