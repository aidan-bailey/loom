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
	// notice is a screen-wide warning shown above the rows (SetNotice).
	notice string
}

// accountsPlanWidth is the fixed width of the row's Plan column; plan
// names ("max", "pro", "free", …) are always short.
const accountsPlanWidth = 5

// accountsNameWidthCap bounds the Name column. It normally grows to fit
// the longest registered name (see nameWidth) — this is the screen used
// to tell apart accounts named, say, "work-sub-1" and "work-sub-2" when
// choosing which to log in or make default, so a name that fits under
// the cap is never cut — but a single very long name must not blow up
// the whole layout, hence the cap.
const accountsNameWidthCap = 16

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

// nameWidth is the row Name column's width: the longest currently
// registered name, capped at accountsNameWidthCap. Sizing to content
// rather than a fixed column means a name that fits under the cap is
// never truncated.
func (a *AccountsManager) nameWidth() int {
	w := 1
	for _, r := range a.rows {
		if n := lipgloss.Width(r.Name); n > w {
			w = n
		}
	}
	if w > accountsNameWidthCap {
		w = accountsNameWidthCap
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

// SetNotice sets a warning that concerns every row, such as a credential
// in loom's environment that every account runs as, shown marked "⚠"
// above the rows; "" clears it.
func (a *AccountsManager) SetNotice(msg string) { a.notice = msg }

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
	accountsHintStyle, accountsWarnStyle, accountsEmailStyle,
	accountsNoticeStyle lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildAccountsManagerStyles) }

func rebuildAccountsManagerStyles() {
	accountsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	accountsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	accountsNormalStyle = lipgloss.NewStyle().Foreground(ui.Text)
	accountsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	accountsWarnStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor)
	accountsEmailStyle = lipgloss.NewStyle().Foreground(ui.Dim)
	// An error that voids every row, not a request for input: ErrorColor,
	// never Attention.
	accountsNoticeStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor).Bold(true)
}

// shortenUsage fits a formatted usage string (ui.AccountUsageText's
// output — "5h X% · 7d Y%", optionally with a " · Nm ago" staleness
// suffix, or a single word like "n/a"/"logged out"/"—") into maxWidth
// cells. It never clips a percentage or a word mid-character: it drops
// whole segments instead, weekly (7d) first and the staleness suffix
// next — the same priority AccountStrip's compact form uses for the
// weekly part — before falling back to a hard clamp for a width so
// narrow no real screen this modal renders at would produce it.
func shortenUsage(usage string, maxWidth int) string {
	if lipgloss.Width(usage) <= maxWidth {
		return usage
	}
	parts := strings.Split(usage, " · ")
	if len(parts) >= 2 && strings.HasPrefix(parts[1], "7d ") {
		reduced := append(append([]string{}, parts[:1]...), parts[2:]...)
		if candidate := strings.Join(reduced, " · "); lipgloss.Width(candidate) <= maxWidth {
			return candidate
		}
		parts = reduced
	}
	if lipgloss.Width(parts[0]) <= maxWidth {
		return parts[0]
	}
	return truncateRight(usage, maxWidth)
}

// Render renders whichever mode is active.
func (a *AccountsManager) Render() string {
	if a.mode == accountsAddingName {
		return a.input.Render()
	}
	content := accountsTitleStyle.Render("Accounts") + "\n\n"
	if a.notice != "" {
		// Wrapped to the content width: it must be read whole, and a line
		// wider than that would widen the box.
		content += accountsNoticeStyle.Width(a.contentWidth()).Render("⚠ "+a.notice) + "\n\n"
	}
	if len(a.rows) == 0 {
		content += accountsNormalStyle.Render("No accounts — press 'a' to add one") + "\n"
	}
	cw := a.contentWidth()
	nw := a.nameWidth()
	// 6 fixed columns (2-cell cursor + 2-cell default mark + the row's
	// two separating spaces) plus the Name and Plan columns; whatever's
	// left is what Usage gets, shortened to fit rather than clipped
	// mid-number.
	usageBudget := cw - 6 - nw - accountsPlanWidth
	for i, r := range a.rows {
		cursor := "  "
		if i == a.cursor {
			cursor = "> "
		}
		mark := "  "
		if r.IsDefault {
			mark = "* "
		}
		// The Name column is sized (nameWidth) to the longest registered
		// name up to accountsNameWidthCap, so truncateRight only actually
		// cuts a name past that cap — this is the screen used to tell
		// apart similarly named accounts when picking one, so a name
		// that fits must never be shortened. Plan values are always
		// short; Usage — the screen's whole reason for existing — is
		// shortened whole-segment-at-a-time (shortenUsage), never
		// mid-number.
		row := fmt.Sprintf("%s%s%-*s %-*s %s", cursor, mark,
			nw, truncateRight(r.Name, nw),
			accountsPlanWidth, truncateRight(r.Plan, accountsPlanWidth),
			shortenUsage(r.Usage, usageBudget))
		// Belt and braces: sized to fit above, but a safety clamp means
		// the row can never wrap the box regardless of what app sends.
		row = truncateRight(row, cw)
		if i == a.cursor {
			content += accountsSelectedStyle.Render(row)
		} else {
			content += accountsNormalStyle.Render(row)
		}
		content += "\n"
		// Email and a warning each get their own indented line rather
		// than riding the row: either one could alone overflow the same
		// width budget the row already spends on Name/Plan/Usage.
		if r.Email != "" {
			content += "    " + accountsEmailStyle.Render(truncateRight(r.Email, cw-4)) + "\n"
		}
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
