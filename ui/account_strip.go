package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	stripNameStyle, stripDefaultStyle, stripUsageStyle, stripWarnStyle,
	stripErrStyle, stripStaleStyle, stripAlertStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildAccountStripStyles) }

func rebuildAccountStripStyles() {
	stripNameStyle = lipgloss.NewStyle().Foreground(Text)
	stripDefaultStyle = lipgloss.NewStyle().Foreground(Text).Bold(true)
	stripUsageStyle = lipgloss.NewStyle().Foreground(Dim)
	// Highlight reads as Attention (needs input) at a glance, and Attention
	// itself is reserved for that one signal — neither belongs on a usage
	// number that needs no action yet. Bold Text draws the eye without
	// claiming either.
	stripWarnStyle = lipgloss.NewStyle().Foreground(Text).Bold(true)
	stripErrStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	stripStaleStyle = lipgloss.NewStyle().Foreground(Faint)
	// A warning that voids every account's selection is an error, not a
	// request for input: ErrorColor, never Attention.
	stripAlertStyle = lipgloss.NewStyle().Foreground(ErrorColor).Bold(true)
}

// AccountStrip is the one-row usage summary above the workspace tab bar:
// every Claude account with its 5-hour and weekly plan usage. It shows only
// when an extra account exists (two or more accounts).
type AccountStrip struct {
	width    int
	accounts []AccountStatus
	// warning leads the row while set (SetWarning).
	warning string
}

// NewAccountStrip creates an empty (hidden) strip.
func NewAccountStrip() *AccountStrip { return &AccountStrip{} }

// SetWidth sets the render width.
func (s *AccountStrip) SetWidth(w int) { s.width = w }

// SetAccounts replaces the accounts shown, default first.
func (s *AccountStrip) SetAccounts(a []AccountStatus) { s.accounts = a }

// SetWarning sets a condition that makes the usage misleading, such as a
// credential in loom's environment that every account runs as; "" clears
// it. It leads the row, marked "⚠", and at narrow widths the usage is cut
// before it is. It never shows the strip on its own.
func (s *AccountStrip) SetWarning(w string) { s.warning = w }

// Height is 1 while the strip shows, else 0.
func (s *AccountStrip) Height() int {
	if len(s.accounts) < 2 {
		return 0
	}
	return 1
}

// String renders the strip at the current time; "" when hidden.
func (s *AccountStrip) String() string { return s.render(time.Now()) }

func (s *AccountStrip) render(now time.Time) string {
	if s.Height() == 0 || s.width <= 0 {
		return ""
	}
	// Truncation cuts from the right, so the warning leading the row is
	// the last thing to go.
	lead := " "
	if s.warning != "" {
		lead = " " + stripAlertStyle.Render("⚠ "+s.warning) + "    "
	}
	line := lead + s.compose(now, true)
	if lipgloss.Width(line) > s.width {
		line = lead + s.compose(now, false)
	}
	if lipgloss.Width(line) > s.width {
		line = ansi.Truncate(line, s.width, "…")
	}
	return line
}

func (s *AccountStrip) compose(now time.Time, withWeek bool) string {
	segs := make([]string, 0, len(s.accounts))
	for _, a := range s.accounts {
		name := stripNameStyle.Render(a.Name)
		if a.IsDefault {
			name = stripDefaultStyle.Render("*" + a.Name)
		}
		usage := accountUsageText(a, now, withWeek)
		style := stripUsageStyle
		switch {
		case a.LoggedOut:
			style = stripErrStyle
		case a.Failing || usageStale(a, now):
			style = stripStaleStyle
		default:
			switch usageSeverity(a, now) {
			case 2:
				style = stripErrStyle
			case 1:
				style = stripWarnStyle
			}
		}
		segs = append(segs, name+"  "+style.Render(usage))
	}
	return strings.Join(segs, "    ")
}
