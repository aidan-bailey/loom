package overlay

import (
	"fmt"
	"strings"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// AccountRow is one line of the Accounts screen, formatted by app.
type AccountRow struct {
	Name  string
	Email string
	Plan  string
	// Usage is ui.AccountUsageText for the account.
	Usage string
	// Warning is "logged out", "not shared: settings.json", … or "".
	Warning   string
	IsDefault bool
}

// AccountRequestKind names an action the user asked the Accounts screen
// for. The screen does no I/O; app carries each request out.
type AccountRequestKind int

const (
	AccountRequestAdd AccountRequestKind = iota + 1
	AccountRequestLogin
	AccountRequestRemove
	AccountRequestSetDefault
)

// AccountRequest is one action for app to carry out on account Name.
type AccountRequest struct {
	Kind AccountRequestKind
	Name string
}

type accountsMode int

const (
	accountsBrowsing accountsMode = iota
	accountsAddingName
	accountsConfirmingRemove
)

// defaultAccountName mirrors account.DefaultName; overlay stays free of
// the account package.
const defaultAccountName = "default"

// AccountsManager is the Accounts drill-in of the Settings overlay: the
// Claude accounts with their login and usage, and keys to add, log in,
// remove, and pick the default. Rows are supplied (and refreshed) by app.
type AccountsManager struct {
	rows   []AccountRow
	cursor int
	width  int

	mode    accountsMode
	input   *TextInputOverlay
	request *AccountRequest
}

// accountsManagerDefaultWidth is wider than the other Settings sub-screens'
// default (60): a row's name/email/plan/usage columns plus a trailing
// warning need up to ~92 content columns, versus their single-value rows.
const accountsManagerDefaultWidth = 100

// NewAccountsManager creates the Accounts screen over rows.
func NewAccountsManager(rows []AccountRow) *AccountsManager {
	a := &AccountsManager{width: accountsManagerDefaultWidth}
	a.SetRows(rows)
	return a
}

// SetRows replaces the rows, keeping the cursor in range.
func (a *AccountsManager) SetRows(rows []AccountRow) {
	a.rows = rows
	if a.cursor >= len(rows) {
		a.cursor = max(len(rows)-1, 0)
	}
}

// SetWidth propagates the available width to any embedded text input.
func (a *AccountsManager) SetWidth(w int) {
	a.width = w
	if a.input != nil {
		a.input.SetSize(w, 3)
	}
}

// TakeRequest returns and clears the pending request.
func (a *AccountsManager) TakeRequest() (AccountRequest, bool) {
	if a.request == nil {
		return AccountRequest{}, false
	}
	req := *a.request
	a.request = nil
	return req, true
}

func (a *AccountsManager) selected() (AccountRow, bool) {
	if a.cursor < 0 || a.cursor >= len(a.rows) {
		return AccountRow{}, false
	}
	return a.rows[a.cursor], true
}

func (a *AccountsManager) ask(kind AccountRequestKind, name string) {
	a.request = &AccountRequest{Kind: kind, Name: name}
}

// HandleKeyPress processes one key press. closed reports whether the
// screen should return control to the parent SettingsOverlay.
func (a *AccountsManager) HandleKeyPress(msg tea.KeyPressMsg) (closed bool) {
	switch a.mode {
	case accountsAddingName:
		// Enter/Esc are owned here, not by the textarea (see
		// SettingsOverlay.handleEditingText for why).
		switch msg.Code {
		case tea.KeyEnter:
			if name := strings.TrimSpace(a.input.GetValue()); name != "" {
				a.ask(AccountRequestAdd, name)
			}
			a.mode, a.input = accountsBrowsing, nil
		case tea.KeyEsc:
			a.mode, a.input = accountsBrowsing, nil
		default:
			a.input.HandleKeyPress(msg)
		}
		return false
	case accountsConfirmingRemove:
		switch msg.String() {
		case "y":
			if row, ok := a.selected(); ok {
				a.ask(AccountRequestRemove, row.Name)
			}
			a.mode = accountsBrowsing
		case "n", "esc":
			a.mode = accountsBrowsing
		}
		return false
	}

	switch msg.String() {
	case "up", "k":
		if a.cursor > 0 {
			a.cursor--
		}
	case "down", "j":
		if a.cursor < len(a.rows)-1 {
			a.cursor++
		}
	case "esc", "q":
		return true
	case "a":
		a.mode = accountsAddingName
		a.input = NewTextInputOverlay("Account name (a-z, 0-9, -)", "")
		a.input.SetSize(a.width, 3)
	case "l":
		if row, ok := a.selected(); ok {
			a.ask(AccountRequestLogin, row.Name)
		}
	case "enter", " ", "space":
		if row, ok := a.selected(); ok {
			a.ask(AccountRequestSetDefault, row.Name)
		}
	case "x":
		if row, ok := a.selected(); ok && row.Name != defaultAccountName {
			a.mode = accountsConfirmingRemove
		}
	}
	return false
}

var (
	accountsTitleStyle, accountsSelectedStyle, accountsNormalStyle,
	accountsHintStyle, accountsWarnStyle lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildAccountsManagerStyles) }

func rebuildAccountsManagerStyles() {
	accountsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	accountsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	accountsNormalStyle = lipgloss.NewStyle().Foreground(ui.Text)
	accountsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	accountsWarnStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

// Render renders whichever mode is active.
func (a *AccountsManager) Render() string {
	if a.mode == accountsAddingName {
		return a.input.Render()
	}
	content := accountsTitleStyle.Render("Accounts") + "\n\n"
	if len(a.rows) == 0 {
		content += accountsNormalStyle.Render("No accounts — press 'a' to add one") + "\n"
	}
	for i, r := range a.rows {
		cursor := "  "
		if i == a.cursor {
			cursor = "> "
		}
		mark := "  "
		if r.IsDefault {
			mark = "* "
		}
		line := fmt.Sprintf("%s%s%-12s %-26s %-5s %s", cursor, mark, r.Name, r.Email, r.Plan, r.Usage)
		if i == a.cursor {
			content += accountsSelectedStyle.Render(line)
		} else {
			content += accountsNormalStyle.Render(line)
		}
		if r.Warning != "" {
			content += "  " + accountsWarnStyle.Render(r.Warning)
		}
		content += "\n"
	}
	if row, ok := a.selected(); ok && a.mode == accountsConfirmingRemove {
		content += "\n" + accountsHintStyle.Render(fmt.Sprintf("Remove account %q and delete its config dir? y/n", row.Name))
	} else {
		content += "\n" + accountsHintStyle.Render("a add • l log in • enter set default • x remove • esc back")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(a.width).
		Render(content)
}
