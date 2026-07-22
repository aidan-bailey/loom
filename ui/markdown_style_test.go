package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkdownStyle_RebuildsOnApplyTheme(t *testing.T) {
	ApplyTheme("afterglow")
	genBefore := mdStyleGen
	afterglowText := *mdStyle.Document.StylePrimitive.Color

	ApplyTheme("legacy")
	t.Cleanup(func() { ApplyTheme("afterglow") })

	assert.Greater(t, mdStyleGen, genBefore, "ApplyTheme must bump the style generation")
	require.NotNil(t, mdStyle.Document.StylePrimitive.Color)
	assert.NotEqual(t, afterglowText, *mdStyle.Document.StylePrimitive.Color,
		"document text color must track the theme")
}

func TestMarkdownStyle_HeadingUsesAccent(t *testing.T) {
	ApplyTheme("afterglow")
	require.NotNil(t, mdStyle.Heading.StylePrimitive.Color)
	assert.Equal(t, *hexPtr(Accent), *mdStyle.Heading.StylePrimitive.Color)
}
