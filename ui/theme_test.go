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
	// Drop the test hook afterwards so it doesn't leak into every
	// ApplyTheme call made by later tests.
	pre := len(themeHooks)
	t.Cleanup(func() { themeHooks = themeHooks[:pre] })
	calls := 0
	RegisterThemeHook(func() { calls++ })
	assert.Equal(t, 1, calls, "hook must run once at registration")
	ApplyTheme("legacy")
	assert.Equal(t, 2, calls, "hook must re-run on ApplyTheme")
}

// End-to-end: a hook-rebuilt style (AdditionStyle in diff.go, built from
// the OK role) must actually retint when the theme switches — this is
// what makes a live switch repaint the UI rather than just the role vars.
func TestApplyTheme_RebuildsHookedStyles(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ApplyTheme("afterglow")
	afterglowFg := AdditionStyle.GetForeground()
	ApplyTheme("legacy")
	assert.NotEqual(t, afterglowFg, AdditionStyle.GetForeground())
}

func TestThemeNames_SortedAndComplete(t *testing.T) {
	names := ThemeNames()
	assert.Equal(t, []string{"afterglow", "legacy"}, names)
}
