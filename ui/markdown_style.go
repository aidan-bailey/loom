package ui

import (
	"fmt"
	"image/color"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// mdStyle is the glamour style config derived from the active theme
// roles. mdStyleGen increments on every rebuild so cached renders
// (MarkdownPane) can detect staleness without re-rendering per frame.
var (
	mdStyle    ansi.StyleConfig
	mdStyleGen int
)

func init() { RegisterThemeHook(rebuildMarkdownStyle) }

func rebuildMarkdownStyle() {
	mdStyle = buildMarkdownStyle()
	mdStyleGen++
}

// hexPtr renders any color.Color as the "#rrggbb" string glamour's
// style primitives expect. AdaptiveColor resolves via RGBA() against
// the detected background, which matches how lipgloss paints it.
func hexPtr(c color.Color) *string {
	r, g, b, _ := c.RGBA()
	s := fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	return &s
}

func mdBoolPtr(b bool) *bool { return &b }
func mdUintPtr(u uint) *uint { return &u }

// buildMarkdownStyle starts from glamour's dark config (keeping its
// chroma code-block palette — syntax highlighting has its own color
// system) and overrides the prose roles with the active theme.
func buildMarkdownStyle() ansi.StyleConfig {
	s := styles.DarkStyleConfig
	s.Document.StylePrimitive.Color = hexPtr(Text)
	s.Document.Margin = mdUintPtr(0)
	s.Heading.StylePrimitive.Color = hexPtr(Accent)
	s.Heading.StylePrimitive.Bold = mdBoolPtr(true)
	s.H1.StylePrimitive.Color = hexPtr(SelectionFg)
	s.H1.StylePrimitive.BackgroundColor = hexPtr(Accent)
	s.BlockQuote.StylePrimitive.Color = hexPtr(Dim)
	s.Item.Color = hexPtr(Text)
	s.Enumeration.Color = hexPtr(Dim)
	s.Link.Color = hexPtr(Info)
	s.LinkText.Color = hexPtr(Info)
	s.Code.StylePrimitive.Color = hexPtr(Highlight)
	s.HorizontalRule.Color = hexPtr(Rule)
	return s
}
