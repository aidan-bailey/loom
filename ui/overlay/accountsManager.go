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
	// removeTarget is the account name a pending remove confirmation
	// applies to, captured by identity when "x" opens the prompt. "y"
	// removes this account, never whatever row the cursor now sits on —
	// a SetRows refresh between "x" and "y" (the registry reloaded
	// elsewhere, e.g. a CLI remove) can reorder or shrink rows, and a
	// position-based target would then fire against the wrong account.
	removeTarget string
}

// Compact column widths for the Accounts screen's rows. Border + padding
// eat 6 columns of whatever width the screen is given (border.go/
// settingsOverlay's default sub-screen width is 60, matching every other
// Settings drill-in), so an unbounded email or usage string would wrap
// the box onto a second line — Usage is the whole point of this screen,
// so it is never truncated; Name and Email are, with an ellipsis.
const (
	accountsNameWidth  = 8
	accountsEmailWidth = 18
	accountsPlanWidth  = 5
)

// NewAccountsManager creates the Accounts screen over rows.
func NewAccountsManager(rows []AccountRow) *AccountsManager {
	a := &AccountsManager{width: 60}
	a.SetRows(rows)
	return a
}

// contentWidth is the box's inner content width: the border (2 columns)
// and Padding(1, 2) (4 columns) come off whatever width the screen was
// given (SettingsOverlay.SetSize propagates its own width here — see
// SettingsOverlay.SetSize).
func (a *AccountsManager) contentWidth() int {
	w := a.width - 6
	if w < 20 {
		w = 20
	}
	return w
}

// SetRows replaces the rows. The cursor follows the previously selected
// account by identity when it still exists (a refresh can reorder or
// insert rows; a position-based cursor would otherwise silently land on
// a different account — e.g. "enter" then setting the wrong default). A
// pending remove confirmation whose target vanished from the new rows is
// canceled rather than left to fire against whatever now sits in its old
// row.
func (a *AccountsManager) SetRows(rows []AccountRow) {
	var selectedName string
	if row, ok := a.selected(); ok {
		selectedName = row.Name
	}
	a.rows = rows
	if selectedName != "" {
		if i := a.indexOf(selectedName); i >= 0 {
			a.cursor = i
		}
	}
	if a.cursor >= len(rows) {
		a.cursor = max(len(rows)-1, 0)
	}
	if a.cursor < 0 {
		a.cursor = 0
	}
	if a.mode == accountsConfirmingRemove && a.indexOf(a.removeTarget) < 0 {
		a.mode = accountsBrowsing
		a.removeTarget = ""
	}
}

// indexOf returns the row index of the account named name, or -1.
func (a *AccountsManager) indexOf(name string) int {
	for i, r := range a.rows {
		if r.Name == name {
			return i
		}
	}
	return -1
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
			if a.removeTarget != "" {
				a.ask(AccountRequestRemove, a.removeTarget)
			}
			a.mode = accountsBrowsing
			a.removeTarget = ""
		case "n", "esc":
			a.mode = accountsBrowsing
			a.removeTarget = ""
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
			a.removeTarget = row.Name
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
	cw := a.contentWidth()
	for i, r := range a.rows {
		cursor := "  "
		if i == a.cursor {
			cursor = "> "
		}
		mark := "  "
		if r.IsDefault {
			mark = "* "
		}
		row := fmt.Sprintf("%s%s%-*s %-*s %-*s %s", cursor, mark,
			accountsNameWidth, truncateRight(r.Name, accountsNameWidth),
			accountsEmailWidth, truncateRight(r.Email, accountsEmailWidth),
			accountsPlanWidth, truncateRight(r.Plan, accountsPlanWidth),
			r.Usage)
		// Belt and braces: the column widths above are sized to fit
		// Usage in full at the narrowest width this screen is ever
		// given (60), but a safety clamp means a row can never wrap the
		// box regardless of what app sends as Usage.
		row = truncateRight(row, cw)
		if i == a.cursor {
			content += accountsSelectedStyle.Render(row)
		} else {
			content += accountsNormalStyle.Render(row)
		}
		content += "\n"
		// The warning gets its own indented line rather than riding the
		// row: "not shared: settings.json" alongside a full row would
		// itself overflow the same width budget.
		if r.Warning != "" {
			content += "    " + accountsWarnStyle.Render(truncateRight(r.Warning, cw-4)) + "\n"
		}
	}
	if a.mode == accountsConfirmingRemove && a.removeTarget != "" {
		msg := fmt.Sprintf("Remove account %q and delete its config dir? y/n", a.removeTarget)
		content += "\n" + accountsHintStyle.Render(truncateRight(msg, cw))
	} else {
		// Short enough to fit the screen's narrowest width (60) without
		// truncation ("log in"/"set default" spelled out would not).
		content += "\n" + accountsHintStyle.Render(truncateRight("a add • l login • enter default • x remove • esc back", cw))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(a.width).
		Render(content)
}
