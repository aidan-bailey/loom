package ui

import (
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// Theme is a named palette of semantic color roles. Components never
// reference literal colors — they reference the package-level role vars
// below, which ApplyTheme reassigns. Styles built from roles at package
// init would capture stale values, so any package-level lipgloss.Style
// derived from a role must be (re)built inside a RegisterThemeHook
// callback (see rebuildListStyles in list.go for the pattern).
type Theme struct {
	Name string

	// Surfaces and text.
	Ground color.Color // full-bleed background (reserved; no style paints it yet)
	Panel  color.Color // raised-surface background (workspace-terminal rows, selected cards)
	Text   color.Color // primary foreground
	Dim    color.Color // secondary labels, hints
	Faint  color.Color // barely-there text (aux hints, disabled)
	Rule   color.Color // unfocused borders, separators

	// Accents. Attention is reserved for "an agent needs the user" —
	// nothing else may use it, so a calm screen has no loud color.
	Accent    color.Color // focus, activity, selected borders
	Attention color.Color // needs-input / bell — the ONLY loud color
	Highlight color.Color // key glyphs, scroll footers
	OK        color.Color // ready status, added lines
	Error     color.Color // errors, removed lines, destructive accents
	Workspace color.Color // workspace labels, workspace-terminal identity
	Info      color.Color // informational text, hunk headers, running tabs

	// Selection (list rows, pickers).
	SelectionBg color.Color
	SelectionFg color.Color
}

// DefaultThemeName is applied at startup when config names no theme (or
// an unknown one).
const DefaultThemeName = "afterglow"

var themes = map[string]Theme{
	// Palette lifted from Afterglow.tmTheme
	// (github.com/Yabatadesign/afterglow-theme).
	"afterglow": {
		Name:        "afterglow",
		Ground:      lipgloss.Color("#2E2E2E"),
		Panel:       lipgloss.Color("#333435"),
		Text:        lipgloss.Color("#d6d6d6"),
		Dim:         lipgloss.Color("#797979"),
		Faint:       lipgloss.Color("#505050"),
		Rule:        lipgloss.Color("#404040"),
		Accent:      lipgloss.Color("#6c99bb"),
		Attention:   lipgloss.Color("#e5b567"),
		Highlight:   lipgloss.Color("#FFC66D"),
		OK:          lipgloss.Color("#b4c973"),
		Error:       lipgloss.Color("#c45330"),
		Workspace:   lipgloss.Color("#a1617a"),
		Info:        lipgloss.Color("#6D9CBE"),
		SelectionBg: lipgloss.Color("#5A647E"),
		SelectionFg: lipgloss.Color("#FFFFFF"),
	},
	// The pre-theme-system look, preserved as an escape hatch. Minor
	// consolidations (documented in the design doc): HeaderAccent
	// #36CFC9→Accent, KeyHighlight #FFCC00→Highlight #FFD700, deleting
	// #cc6666→Error, recoverable #d19a66→Attention, menu ANSI 205/99→
	// Accent/Workspace, err #FF0000→Error, diff greens/reds/blues→
	// OK/Error/Info, file-explorer #7c7cff→Accent.
	"legacy": {
		Name:        "legacy",
		Ground:      compat.AdaptiveColor{Light: lipgloss.Color("#ffffff"), Dark: lipgloss.Color("#000000")},
		Panel:       compat.AdaptiveColor{Light: lipgloss.Color("#e8e0f0"), Dark: lipgloss.Color("#2d2640")},
		Text:        compat.AdaptiveColor{Light: lipgloss.Color("#1a1a1a"), Dark: lipgloss.Color("#dddddd")},
		Dim:         compat.AdaptiveColor{Light: lipgloss.Color("#999999"), Dark: lipgloss.Color("#666666")},
		Faint:       lipgloss.Color("#777777"),
		Rule:        compat.AdaptiveColor{Light: lipgloss.Color("#999999"), Dark: lipgloss.Color("#555555")},
		Accent:      compat.AdaptiveColor{Light: lipgloss.Color("#874BFD"), Dark: lipgloss.Color("#7D56F4")},
		Attention:   lipgloss.Color("#e5c07b"),
		Highlight:   lipgloss.Color("#FFD700"),
		OK:          lipgloss.Color("#51bd73"),
		Error:       lipgloss.Color("#de613e"),
		Workspace:   lipgloss.Color("#6c71c4"),
		Info:        lipgloss.Color("#7AA2F7"),
		SelectionBg: lipgloss.Color("#dde4f0"),
		SelectionFg: lipgloss.Color("#1a1a1a"),
	},
}

// current is the active theme; role vars below mirror its fields.
var current = themes[DefaultThemeName]

// Active color roles. Reassigned (main goroutine only: startup, or the
// settings overlay's Update-goroutine handler) by ApplyTheme.
var (
	Ground      = current.Ground
	Panel       = current.Panel
	Text        = current.Text
	Dim         = current.Dim
	Faint       = current.Faint
	Rule        = current.Rule
	Accent      = current.Accent
	Attention   = current.Attention
	Highlight   = current.Highlight
	OK          = current.OK
	ErrorColor  = current.Error
	Workspace   = current.Workspace
	Info        = current.Info
	SelectionBg = current.SelectionBg
	SelectionFg = current.SelectionFg
)

// themeHooks are style-rebuild callbacks. Each runs once at
// registration (so derived styles are never zero) and again on every
// ApplyTheme.
var themeHooks []func()

// RegisterThemeHook registers fn to rebuild theme-derived styles and
// invokes it immediately. Call from init() in the file that owns the
// styles. Main-goroutine only.
func RegisterThemeHook(fn func()) {
	themeHooks = append(themeHooks, fn)
	fn()
}

// ApplyTheme activates the named theme, reassigns every role var, and
// re-runs all registered hooks. Unknown or empty names activate the
// default theme and return false. Main-goroutine only.
func ApplyTheme(name string) bool {
	t, ok := themes[name]
	if !ok {
		t = themes[DefaultThemeName]
	}
	current = t
	Ground, Panel, Text, Dim, Faint, Rule = t.Ground, t.Panel, t.Text, t.Dim, t.Faint, t.Rule
	Accent, Attention, Highlight, OK, ErrorColor = t.Accent, t.Attention, t.Highlight, t.OK, t.Error
	Workspace, Info, SelectionBg, SelectionFg = t.Workspace, t.Info, t.SelectionBg, t.SelectionFg
	for _, fn := range themeHooks {
		fn()
	}
	return ok
}

// CurrentThemeName returns the active theme's name.
func CurrentThemeName() string { return current.Name }

// ThemeNames returns all registered theme names, sorted.
func ThemeNames() []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
