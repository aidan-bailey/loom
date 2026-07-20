package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyTheme_KnownName(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ok := ApplyTheme("legacy")
	assert.True(t, ok)
	assert.Equal(t, "legacy", CurrentThemeName())
}

func TestApplyTheme_UnknownFallsBackToDefault(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ok := ApplyTheme("no-such-theme")
	assert.False(t, ok)
	assert.Equal(t, DefaultThemeName, CurrentThemeName())
}

func TestApplyTheme_EmptyIsDefault(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ok := ApplyTheme("")
	assert.False(t, ok)
	assert.Equal(t, DefaultThemeName, CurrentThemeName())
}

func TestApplyTheme_ReassignsRoleVars(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ApplyTheme(DefaultThemeName)
	afterglowAccent := Accent
	ApplyTheme("legacy")
	assert.NotEqual(t, afterglowAccent, Accent)
}

func TestRegisterThemeHook_RunsImmediatelyAndOnApply(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	calls := 0
	RegisterThemeHook(func() { calls++ })
	assert.Equal(t, 1, calls, "hook must run once at registration")
	ApplyTheme("legacy")
	assert.Equal(t, 2, calls, "hook must re-run on ApplyTheme")
}

func TestThemeNames_SortedAndComplete(t *testing.T) {
	names := ThemeNames()
	assert.Equal(t, []string{"afterglow", "legacy"}, names)
}
