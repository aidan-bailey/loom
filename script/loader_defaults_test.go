package script

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// TestEngineLoadsEmbeddedDefaults verifies the baked-in keymap binds
// every stock key. Any regression to defaults.lua (typo, removed
// primitive, reorder) would drop a key from the set and fail here.
func TestEngineLoadsEmbeddedDefaults(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.LoadDefaults()

	bound := e.actionKeys()
	for _, k := range []string{
		"up", "k", "down", "j", "d",
		"n", "N", "D", "p", "c", "r", "?", "q",
		"W", "[", "l", "]", ";",
		"alt+a", "alt+t", "ctrl+a", "ctrl+t",
		"a", "t",
	} {
		assert.Contains(t, bound, k, "default binding missing for %q", k)
	}
}

// TestUserScriptOverridesDefault proves the load-order contract: user
// scripts run after defaults, so an unbind+rebind for a key like "j"
// replaces the default handler. Regression would mean users can't
// override the stock keymap, defeating the whole migration.
func TestUserScriptOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "user.lua"), []byte(`
		cs.unbind("j")
		cs.bind("j", function() _G.user_j = true end)
	`), 0o644))

	e := NewEngine(nil)
	defer e.Close()
	e.LoadDefaults()
	e.Load(dir)

	_, err := e.Dispatch("j", &fakeHost{})
	assert.NoError(t, err)
	assert.Equal(t, lua.LTrue, e.L.GetGlobal("user_j"))
}
