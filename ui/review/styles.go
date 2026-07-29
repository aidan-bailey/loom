package reviewui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/aidan-bailey/loom/ui"
)

func tabBorderWithBottom(left, middle, right string) lipgloss.Border {
	border := lipgloss.RoundedBorder()
	border.BottomLeft = left
	border.Bottom = middle
	border.BottomRight = right
	return border
}

var (
	activeTabBorder   = tabBorderWithBottom("┘", " ", "└")
	inactiveTabBorder = tabBorderWithBottom("┴", "─", "┴")
)

// Role aliases. Reassigned at the top of rebuildReviewStyles so every
// derived style below picks up a live theme switch.
var (
	subtle  color.Color
	accent  color.Color
	success color.Color
	warning color.Color
	muted   color.Color
	bright  color.Color
)

var (
	headerStyle           lipgloss.Style
	focusedBorder         lipgloss.Style
	blurredBorder         lipgloss.Style
	commentStyle          lipgloss.Style
	commentLineStyle      lipgloss.Style
	footerStyle           lipgloss.Style
	footerKeyStyle        lipgloss.Style
	modalStyle            lipgloss.Style
	modalTitleStyle       lipgloss.Style
	lineNumStyle          lipgloss.Style
	cursorLineNumStyle    lipgloss.Style
	selectedLineNumStyle  lipgloss.Style
	cursorMarker          lipgloss.Style
	selectedMarker        lipgloss.Style
	inlineCommentBox      lipgloss.Style
	inlineLabelComment    lipgloss.Style
	annotationGutter      lipgloss.Style
	gutterOverlap         lipgloss.Style
	continuationGutter    string
	mdH1Style             lipgloss.Style
	mdH2Style             lipgloss.Style
	mdH3Style             lipgloss.Style
	mdH4Style             lipgloss.Style
	mdBoldStyle           lipgloss.Style
	mdItalicStyle         lipgloss.Style
	mdCodeStyle           lipgloss.Style
	mdListMarkerStyle     lipgloss.Style
	mdCheckboxOpen        lipgloss.Style
	mdCheckboxDone        lipgloss.Style
	mdCheckboxDoneText    lipgloss.Style
	mdBlockquoteBar       lipgloss.Style
	mdBlockquoteStyle     lipgloss.Style
	mdHrStyle             lipgloss.Style
	mdLinkStyle           lipgloss.Style
	mdTablePipe           lipgloss.Style
	mdTableSepStyle       lipgloss.Style
	mdTableHeaderStyle    lipgloss.Style
	mdTableCellStyle      lipgloss.Style
	sidebarSelectedText   lipgloss.Style
	modalBtnLabel         lipgloss.Style
	modalBtnHint          lipgloss.Style
	modalBtnFocused       lipgloss.Style
	modalBtnNormal        lipgloss.Style
	modalDeleteBtnLabel   lipgloss.Style
	modalDeleteBtnFocused lipgloss.Style
	inactiveTabStyle      lipgloss.Style
	activeTabStyle        lipgloss.Style
	tabSearchPromptStyle  lipgloss.Style
	diffAddedGutter       lipgloss.Style
	diffDeletedGutter     lipgloss.Style
	diffDeletedLineNum    lipgloss.Style
	tabChangeCount        lipgloss.Style
	contextBoxStyle       lipgloss.Style
	selectedLineBg        lipgloss.Style
	sidebarHighlightBg    lipgloss.Style
	diffChangedLineBg     lipgloss.Style
	diffDeletedLineBg     lipgloss.Style
	visualModeIndicator   lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildReviewStyles) }

// rebuildReviewStyles constructs every style from the current theme's
// color roles. Styles built at package-init time would capture the
// pre-ApplyTheme palette and go stale on a live theme switch.
func rebuildReviewStyles() {
	subtle = ui.Dim
	accent = ui.Accent
	success = ui.OK
	warning = ui.Attention
	muted = ui.Text
	bright = ui.SelectionFg

	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(bright).
		Background(accent).
		Padding(0, 1)

	focusedBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent)

	blurredBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtle)

	commentStyle = lipgloss.NewStyle().
		Foreground(muted).
		PaddingLeft(1)

	commentLineStyle = lipgloss.NewStyle().Foreground(subtle)

	footerStyle = lipgloss.NewStyle().Foreground(subtle)

	footerKeyStyle = lipgloss.NewStyle().Bold(true).Foreground(bright)

	modalStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2).
		Width(60)

	modalTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(accent).
		MarginBottom(1)

	lineNumStyle = lipgloss.NewStyle().
		Foreground(subtle).
		Width(5).
		Align(lipgloss.Right)

	cursorLineNumStyle = lipgloss.NewStyle().
		Foreground(warning).
		Bold(true).
		Width(5).
		Align(lipgloss.Right)

	selectedLineNumStyle = lipgloss.NewStyle().
		Foreground(accent).
		Bold(true).
		Width(5).
		Align(lipgloss.Right)

	cursorMarker = lipgloss.NewStyle().Foreground(warning).Bold(true)

	selectedMarker = lipgloss.NewStyle().Foreground(accent).Bold(true)

	// Inline annotation box styles
	inlineCommentBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Info).
		Foreground(ui.Info).
		PaddingLeft(1).
		PaddingRight(1)

	inlineLabelComment = lipgloss.NewStyle().Foreground(ui.Info).Bold(true)

	annotationGutter = lipgloss.NewStyle().Foreground(ui.Info).Bold(true)

	gutterOverlap = lipgloss.NewStyle().Foreground(accent).Bold(true)

	// Continuation line gutter (for wrapped lines)
	continuationGutter = lipgloss.NewStyle().
		Foreground(subtle).
		Width(5).
		Align(lipgloss.Right).
		Render("↪")

	// Markdown syntax highlighting styles
	mdH1Style = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mdH2Style = lipgloss.NewStyle().Bold(true).Foreground(ui.Highlight)
	mdH3Style = lipgloss.NewStyle().Bold(true).Foreground(ui.Info)
	mdH4Style = lipgloss.NewStyle().Bold(true).Foreground(ui.Info).Italic(true)
	mdBoldStyle = lipgloss.NewStyle().Bold(true).Foreground(bright)
	mdItalicStyle = lipgloss.NewStyle().Italic(true).Foreground(muted)
	mdCodeStyle = lipgloss.NewStyle().
		Foreground(ui.Highlight).
		Background(lipgloss.Color("#3a3a3a"))
	mdListMarkerStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mdCheckboxOpen = lipgloss.NewStyle().Foreground(subtle)
	mdCheckboxDone = lipgloss.NewStyle().Foreground(success).Bold(true)
	mdCheckboxDoneText = lipgloss.NewStyle().Foreground(subtle).Strikethrough(true)
	mdBlockquoteBar = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mdBlockquoteStyle = lipgloss.NewStyle().Foreground(subtle).Italic(true)
	mdHrStyle = lipgloss.NewStyle().Foreground(subtle)
	mdLinkStyle = lipgloss.NewStyle().Foreground(ui.Info).Underline(true)
	mdTablePipe = lipgloss.NewStyle().Foreground(subtle)
	mdTableSepStyle = lipgloss.NewStyle().Foreground(subtle)
	mdTableHeaderStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mdTableCellStyle = lipgloss.NewStyle().Foreground(muted)

	// Sidebar selected text (bright for contrast against highlight bg)
	sidebarSelectedText = lipgloss.NewStyle().Foreground(bright)

	// Modal button styles
	modalBtnLabel = lipgloss.NewStyle().Bold(true).Foreground(bright)
	modalBtnHint = lipgloss.NewStyle().Italic(true).Foreground(subtle)
	modalBtnFocused = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
	modalBtnNormal = lipgloss.NewStyle().Padding(0, 1)
	modalDeleteBtnLabel = lipgloss.NewStyle().Bold(true).Foreground(ui.ErrorColor)
	modalDeleteBtnFocused = lipgloss.NewStyle().
		Background(ui.ErrorColor).
		Foreground(bright).
		Bold(true).
		Padding(0, 1)

	// Tab bar styles — bordered tabs with open-bottom active tab
	inactiveTabStyle = lipgloss.NewStyle().
		Border(inactiveTabBorder, true).
		BorderForeground(accent).
		Foreground(muted).
		Padding(0, 1)

	activeTabStyle = lipgloss.NewStyle().
		Border(activeTabBorder, true).
		BorderForeground(accent).
		Bold(true).
		Foreground(bright).
		Padding(0, 1)

	tabSearchPromptStyle = lipgloss.NewStyle().Bold(true).Foreground(warning)

	// Diff gutter markers
	diffAddedGutter = lipgloss.NewStyle().Foreground(success).Bold(true)
	diffDeletedGutter = lipgloss.NewStyle().Foreground(ui.ErrorColor).Bold(true)
	diffDeletedLineNum = lipgloss.NewStyle().
		Foreground(ui.ErrorColor).
		Width(5).
		Align(lipgloss.Right)

	// Change count in tab labels
	tabChangeCount = lipgloss.NewStyle().Foreground(success)

	// Context box in comment/edit modals
	contextBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtle).
		Foreground(bright)

	// Background overlays. Loom themes are dark-first, so these use the
	// upstream dark-branch construction; the theme system owns variants.
	selectedLineBg = lipgloss.NewStyle().
		Background(lipgloss.Color("#2D2B55"))

	sidebarHighlightBg = lipgloss.NewStyle().
		Background(lipgloss.Color("#1E3A5F")).
		Foreground(lipgloss.Color("#E0E0E0"))

	diffChangedLineBg = lipgloss.NewStyle().
		Background(lipgloss.Color("#1A3A1A"))

	diffDeletedLineBg = lipgloss.NewStyle().
		Background(lipgloss.Color("#3A1A1A"))

	visualModeIndicator = lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.Highlight).
		Background(lipgloss.Color("#2D2B55")).
		Padding(0, 1)
}

// bgToAnsi converts a lipgloss color to a raw ANSI truecolor background escape sequence.
// Returns "" if the color is nil or zero-alpha.
func bgToAnsi(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return ""
	}
	// RGBA() returns 16-bit values; scale to 8-bit.
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}
