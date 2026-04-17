package ui

import "github.com/charmbracelet/lipgloss"

// Shared color palette. Referenced by style vars across ui/ and app/ so the
// theme can be retuned in one place.
var (
	// BorderActive is the accent color used for focused borders, highlighted
	// tabs, and inline attach hints.
	BorderActive = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

	// BorderMuted is the color for unfocused borders and muted chrome.
	BorderMuted = lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"}

	// TextDim is for secondary labels, hints, and idle text elements.
	TextDim = lipgloss.AdaptiveColor{Light: "#999999", Dark: "#666666"}

	// TitleAccent highlights overlay titles and the help panel heading.
	// Matches BorderActive on dark terminals.
	TitleAccent = lipgloss.Color("#7D56F4")

	// HeaderAccent styles help-panel section headers.
	HeaderAccent = lipgloss.Color("#36CFC9")

	// KeyHighlight styles key glyphs in help content and prompts.
	KeyHighlight = lipgloss.Color("#FFCC00")

	// TextPrimary is the default foreground for overlay body text.
	TextPrimary = lipgloss.Color("#FFFFFF")

	// TextHint styles auxiliary help/instruction lines rendered inside overlays.
	TextHint = lipgloss.Color("#777777")

	// SelectionBg is the background for selected rows in list-style overlays.
	SelectionBg = lipgloss.Color("#dde4f0")

	// SelectionFg pairs with SelectionBg for selected-row foreground text.
	SelectionFg = lipgloss.Color("#1a1a1a")

	// DangerAccent borders confirmation overlays for destructive actions.
	DangerAccent = lipgloss.Color("#de613e")

	// ShadowFg is the drop-shadow color beneath floating overlays.
	ShadowFg = lipgloss.Color("#333333")

	// OverlayBorder is the ANSI accent used for overlay borders and the
	// selected-row background in pickers that opt into ANSI palette colors.
	// Kept distinct from TitleAccent because ANSI 62 maps to the user's
	// terminal palette and renders differently from the hex equivalent.
	OverlayBorder = lipgloss.Color("62")

	// OverlaySelectedFg is the foreground paired with OverlayBorder when used
	// as a selection background (ANSI 0 = terminal background).
	OverlaySelectedFg = lipgloss.Color("0")

	// OverlayItemFg is the foreground for unselected items in ANSI pickers.
	OverlayItemFg = lipgloss.Color("7")

	// OverlayHintFg styles the trailing instruction line in ANSI pickers
	// ("↑/↓ to select, enter to confirm").
	OverlayHintFg = lipgloss.Color("240")
)
