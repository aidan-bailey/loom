package overlay

import (
	"fmt"
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
	settingsFieldBranchPrefix
	settingsFieldBaseBranch
	settingsFieldProfiles
	settingsFieldClaudePreferences
	settingsFieldTheme
	settingsFieldAccounts
	settingsFieldCount
)

func (f settingsField) label() string {
	switch f {
	case settingsFieldDefaultProgram:
		return "Default Program"
	case settingsFieldBranchPrefix:
		return "Branch Prefix"
	case settingsFieldBaseBranch:
		return "Base Branch"
	case settingsFieldProfiles:
		return "Profiles"
	case settingsFieldClaudePreferences:
		return "Claude Preferences"
	case settingsFieldTheme:
		return "Theme"
	case settingsFieldAccounts:
		return "Accounts"
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
	settingsAccountsSub
)

// SettingsOverlay is the config.json editor: a vertical list of scalar
// fields plus two drill-in rows (Profiles, Claude Preferences). It owns
// no persistence — HandleKeyPress's second return value reports whether
// a field changed; the caller (app.handleStateSettingsKey) is
// responsible for config.SaveConfigTo and refreshing the home fields
// that shadow cfg (m.program).
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
	accounts    *AccountsManager
	accountRows []AccountRow

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
	case settingsAccountsSub:
		if s.accounts.HandleKeyPress(msg) {
			s.mode = settingsBrowsing
			s.accounts = nil
		}
		return false, false
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
// row. Most rows open a nested edit mode that reports its own change on
// a later HandleKeyPress; the Theme row cycles in place and reports the
// change immediately.
func (s *SettingsOverlay) activateRow() (closed, changed bool) {
	switch settingsField(s.cursor) {
	case settingsFieldDefaultProgram:
		s.startTextEdit(settingsFieldDefaultProgram, "Default Program", s.cfg.DefaultProgram)
	case settingsFieldBranchPrefix:
		s.startTextEdit(settingsFieldBranchPrefix, "Branch Prefix", s.cfg.BranchPrefix)
	case settingsFieldBaseBranch:
		s.startTextEdit(settingsFieldBaseBranch, "Base Branch", s.cfg.BaseBranch)
	case settingsFieldProfiles:
		s.profiles = NewProfilesManager(s.cfg)
		s.profiles.SetWidth(s.width)
		s.mode = settingsProfilesSub
	case settingsFieldClaudePreferences:
		s.claudePrefs = NewClaudePreferences(s.cfg, s.authBlocked, s.authReason)
		s.claudePrefs.SetWidth(s.width)
		s.mode = settingsClaudePrefsSub
	case settingsFieldTheme:
		names := ui.ThemeNames()
		if len(names) == 0 {
			return false, false
		}
		cur := s.cfg.GetTheme()
		if cur == "" {
			cur = ui.DefaultThemeName
		}
		// idx stays 0 when cur isn't registered (hand-edited config), so
		// cycling always lands on a valid name.
		idx := 0
		for i, n := range names {
			if n == cur {
				idx = i
				break
			}
		}
		next := names[(idx+1)%len(names)]
		s.cfg.Mutate(func(c *config.Config) { c.Theme = next })
		ui.ApplyTheme(next)
		return false, true
	case settingsFieldAccounts:
		s.accounts = NewAccountsManager(s.accountRows)
		s.accounts.SetWidth(s.width)
		s.mode = settingsAccountsSub
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
	case settingsFieldBaseBranch:
		s.cfg.Mutate(func(c *config.Config) { c.BaseBranch = strings.TrimSpace(value) })
		return true
	}
	return false
}

// TakeError returns and clears the last validation error from a text-edit
// field. Callers poll this after HandleKeyPress.
func (s *SettingsOverlay) TakeError() error {
	err := s.lastErr
	s.lastErr = nil
	return err
}

// SetAccountRows supplies the Accounts screen's rows (app refreshes them as
// probes land), updating the screen if it is open.
func (s *SettingsOverlay) SetAccountRows(rows []AccountRow) {
	s.accountRows = rows
	if s.accounts != nil {
		s.accounts.SetRows(rows)
	}
}

// TakeAccountRequest returns and clears the Accounts screen's pending
// request. Callers poll it after HandleKeyPress, like TakeError.
func (s *SettingsOverlay) TakeAccountRequest() (AccountRequest, bool) {
	if s.accounts == nil {
		return AccountRequest{}, false
	}
	return s.accounts.TakeRequest()
}

// HandleKey satisfies the Overlay interface. State handlers that need
// the changed signal call HandleKeyPress directly instead (mirrors
// WorkspacePicker.HandleKey/HandleKeyPress).
func (s *SettingsOverlay) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := s.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface. A resize while a sub-screen is
// open (the window changed, or the overlay was just opened) must reach
// it too — otherwise it keeps rendering at whatever width it was created
// with, which for Accounts specifically must track the real width to
// stay wrap-free (see AccountsManager.contentWidth).
func (s *SettingsOverlay) SetSize(width, height int) {
	s.width = width
	s.height = height
	if s.profiles != nil {
		s.profiles.SetWidth(width)
	}
	if s.claudePrefs != nil {
		s.claudePrefs.SetWidth(width)
	}
	if s.accounts != nil {
		s.accounts.SetWidth(width)
	}
}

// View satisfies the Overlay interface.
func (s *SettingsOverlay) View() string {
	return s.Render()
}

var (
	settingsTitleStyle, settingsSelectedStyle, settingsNormalStyle, settingsHintStyle lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildSettingsStyles) }

func rebuildSettingsStyles() {
	settingsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	settingsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	settingsNormalStyle = lipgloss.NewStyle().Foreground(ui.Text)
	settingsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
}

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
	case settingsAccountsSub:
		return s.accounts.Render()
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
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(s.width)
	return border.Render(content)
}

// valueFor renders the current display value for row f.
func (s *SettingsOverlay) valueFor(f settingsField) string {
	switch f {
	case settingsFieldDefaultProgram:
		return s.cfg.DefaultProgram
	case settingsFieldBranchPrefix:
		return s.cfg.BranchPrefix
	case settingsFieldBaseBranch:
		if v := s.cfg.GetBaseBranch(); v != "" {
			return v
		}
		// Empty is the common case and means auto-detect, not "unset".
		return "(auto)"
	case settingsFieldProfiles:
		return fmt.Sprintf("(%d) →", len(s.cfg.Profiles))
	case settingsFieldClaudePreferences:
		return "→"
	case settingsFieldTheme:
		if v := s.cfg.GetTheme(); v != "" {
			return v
		}
		return ui.DefaultThemeName
	case settingsFieldAccounts:
		return fmt.Sprintf("(%d) →", max(len(s.accountRows)-1, 0))
	}
	return ""
}
