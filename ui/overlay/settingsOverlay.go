package overlay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// settingsField indexes the rows of the main settings list, in display order.
type settingsField int

const (
	settingsFieldDefaultProgram settingsField = iota
	settingsFieldAutoYes
	settingsFieldDaemonPollInterval
	settingsFieldBranchPrefix
	settingsFieldProfiles
	settingsFieldClaudePreferences
	settingsFieldCount
)

func (f settingsField) label() string {
	switch f {
	case settingsFieldDefaultProgram:
		return "Default Program"
	case settingsFieldAutoYes:
		return "Auto Yes"
	case settingsFieldDaemonPollInterval:
		return "Daemon Poll Interval"
	case settingsFieldBranchPrefix:
		return "Branch Prefix"
	case settingsFieldProfiles:
		return "Profiles"
	case settingsFieldClaudePreferences:
		return "Claude Preferences"
	}
	return ""
}

// settingsMode distinguishes the main row list from a nested sub-screen
// or an in-place text edit. Only one is active at a time; HandleKeyPress
// proxies to whichever child is active rather than introducing a second
// top-level app state for "editing a field."
type settingsMode int

const (
	settingsBrowsing settingsMode = iota
	settingsEditingText
	settingsProfilesSub
	settingsClaudePrefsSub
)

// SettingsOverlay is the config.json editor: a vertical list of scalar
// fields plus two drill-in rows (Profiles, Claude Preferences). It owns
// no persistence — HandleKeyPress's second return value reports whether
// a field changed; the caller (app.handleStateSettingsKey) is
// responsible for config.SaveConfigTo and refreshing the home fields
// that shadow cfg (m.program, m.autoYes).
//
// authBlocked/authReason mirror session.RemoteControlAuth.Blocked()/
// Reason, passed as plain values rather than the session type so this
// package stays decoupled from session (matching every other overlay).
type SettingsOverlay struct {
	cfg         *config.Config
	authBlocked bool
	authReason  string

	cursor int
	width  int
	height int

	mode         settingsMode
	editing      *TextInputOverlay
	editingField settingsField

	profiles    *ProfilesManager
	claudePrefs *ClaudePreferences

	lastErr error
}

// NewSettingsOverlay creates the settings overlay over cfg.
func NewSettingsOverlay(cfg *config.Config, authBlocked bool, authReason string) *SettingsOverlay {
	return &SettingsOverlay{cfg: cfg, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// HandleKeyPress processes one key press. closed reports whether the
// whole overlay should be dismissed (only true from browsing mode);
// changed reports whether cfg was mutated, so the caller knows to
// persist and refresh its shadow fields.
func (s *SettingsOverlay) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	s.lastErr = nil
	switch s.mode {
	case settingsEditingText:
		return s.handleEditingText(msg)
	case settingsProfilesSub:
		closedSub, ch := s.profiles.HandleKeyPress(msg)
		if closedSub {
			s.mode = settingsBrowsing
			s.profiles = nil
		}
		return false, ch
	case settingsClaudePrefsSub:
		closedSub, ch := s.claudePrefs.HandleKeyPress(msg)
		if closedSub {
			s.mode = settingsBrowsing
			s.claudePrefs = nil
		}
		return false, ch
	}

	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < int(settingsFieldCount)-1 {
			s.cursor++
		}
	case "esc", "q":
		return true, false
	case " ", "space", "enter":
		return s.activateRow()
	}
	return false, false
}

// activateRow runs the Enter/space action for the currently selected
// row. Only Auto Yes changes cfg synchronously here; the rest open a
// nested edit mode that reports its own change on a later HandleKeyPress.
func (s *SettingsOverlay) activateRow() (closed, changed bool) {
	switch settingsField(s.cursor) {
	case settingsFieldAutoYes:
		s.cfg.Mutate(func(c *config.Config) { c.AutoYes = !c.AutoYes })
		return false, true
	case settingsFieldDefaultProgram:
		s.startTextEdit(settingsFieldDefaultProgram, "Default Program", s.cfg.DefaultProgram)
	case settingsFieldBranchPrefix:
		s.startTextEdit(settingsFieldBranchPrefix, "Branch Prefix", s.cfg.BranchPrefix)
	case settingsFieldDaemonPollInterval:
		s.startTextEdit(settingsFieldDaemonPollInterval, "Daemon Poll Interval (ms)", fmt.Sprintf("%d", s.cfg.DaemonPollInterval))
	case settingsFieldProfiles:
		s.profiles = NewProfilesManager(s.cfg)
		s.profiles.SetWidth(s.width)
		s.mode = settingsProfilesSub
	case settingsFieldClaudePreferences:
		s.claudePrefs = NewClaudePreferences(s.cfg, s.authBlocked, s.authReason)
		s.claudePrefs.SetWidth(s.width)
		s.mode = settingsClaudePrefsSub
	}
	return false, false
}

func (s *SettingsOverlay) startTextEdit(field settingsField, title, value string) {
	s.mode = settingsEditingText
	s.editingField = field
	s.editing = NewTextInputOverlay(title, value)
	s.editing.SetSize(s.width, 3)
}

// handleEditingText owns Enter/Esc directly rather than relying on
// TextInputOverlay's Tab-to-focus-the-Enter-button convention (built
// for the multi-line prompt overlay, where Enter on the textarea must
// insert a newline). These are single-line fields: Enter submits
// whatever's in the textarea immediately, Esc cancels. Any other key
// (typed runes, backspace, arrows) is forwarded to the embedded widget.
func (s *SettingsOverlay) handleEditingText(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.Code {
	case tea.KeyEnter:
		value := s.editing.GetValue()
		field := s.editingField
		s.mode = settingsBrowsing
		s.editing = nil
		return false, s.applyTextEdit(field, value)
	case tea.KeyEsc:
		s.mode = settingsBrowsing
		s.editing = nil
		return false, false
	}
	s.editing.HandleKeyPress(msg)
	return false, false
}

// applyTextEdit parses and stores value for field. Returns whether cfg
// changed; on a parse failure it records the error via s.lastErr
// (polled by TakeError) and leaves cfg untouched.
func (s *SettingsOverlay) applyTextEdit(field settingsField, value string) bool {
	switch field {
	case settingsFieldDefaultProgram:
		s.cfg.Mutate(func(c *config.Config) { c.DefaultProgram = value })
		return true
	case settingsFieldBranchPrefix:
		s.cfg.Mutate(func(c *config.Config) { c.BranchPrefix = value })
		return true
	case settingsFieldDaemonPollInterval:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			s.lastErr = fmt.Errorf("daemon poll interval must be a positive integer, got %q", value)
			return false
		}
		s.cfg.Mutate(func(c *config.Config) { c.DaemonPollInterval = n })
		return true
	}
	return false
}

// TakeError returns and clears the last validation error (currently only
// possible from the Daemon Poll Interval field). Callers poll this after
// HandleKeyPress.
func (s *SettingsOverlay) TakeError() error {
	err := s.lastErr
	s.lastErr = nil
	return err
}

// HandleKey satisfies the Overlay interface. State handlers that need
// the changed signal call HandleKeyPress directly instead (mirrors
// WorkspacePicker.HandleKey/HandleKeyPress).
func (s *SettingsOverlay) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := s.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface.
func (s *SettingsOverlay) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// View satisfies the Overlay interface.
func (s *SettingsOverlay) View() string {
	return s.Render()
}

var (
	settingsTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	settingsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	settingsNormalStyle   = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	settingsHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
)

// Render renders whichever mode is active: the main row list, an
// embedded text edit, or a nested sub-screen.
func (s *SettingsOverlay) Render() string {
	switch s.mode {
	case settingsEditingText:
		return s.editing.Render()
	case settingsProfilesSub:
		return s.profiles.Render()
	case settingsClaudePrefsSub:
		return s.claudePrefs.Render()
	}

	content := settingsTitleStyle.Render("Settings") + "\n\n"
	for i := settingsField(0); i < settingsFieldCount; i++ {
		cursor := "  "
		if int(i) == s.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%-22s %s", cursor, i.label(), s.valueFor(i))
		if int(i) == s.cursor {
			content += settingsSelectedStyle.Render(line) + "\n"
		} else {
			content += settingsNormalStyle.Render(line) + "\n"
		}
	}
	content += "\n" + settingsHintStyle.Render("enter edit/open • esc close")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(s.width)
	return border.Render(content)
}

// valueFor renders the current display value for row f.
func (s *SettingsOverlay) valueFor(f settingsField) string {
	switch f {
	case settingsFieldDefaultProgram:
		return s.cfg.DefaultProgram
	case settingsFieldAutoYes:
		if s.cfg.AutoYes {
			return "[x]"
		}
		return "[ ]"
	case settingsFieldDaemonPollInterval:
		return fmt.Sprintf("%d ms", s.cfg.DaemonPollInterval)
	case settingsFieldBranchPrefix:
		return s.cfg.BranchPrefix
	case settingsFieldProfiles:
		return fmt.Sprintf("(%d) →", len(s.cfg.Profiles))
	case settingsFieldClaudePreferences:
		return "→"
	}
	return ""
}
