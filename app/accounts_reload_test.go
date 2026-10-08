package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// otherTerminal is a second handle on m's accounts.json, the way a `loom
// account` run in another terminal sees it.
func otherTerminal(t *testing.T, m *home) *account.Registry {
	t.Helper()
	return account.LoadRegistry(filepath.Dir(testModel(m).AccountsRegistryForTest().Path()))
}

func TestHealthTick_RemovingTheLastExtraAccountElsewhereHidesTheStrip(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.refreshAccountViews()
	require.Equal(t, 1, m.accountStrip.Height())

	_, err := otherTerminal(t, m).Remove("max-2", false)
	require.NoError(t, err)
	tickModel(t, m)

	assert.Equal(t, 0, m.accountStrip.Height(), "the strip follows the file on the next tick")
	assert.False(t, ui.ShowAccounts(), "and the badges with it")
	assert.False(t, m.core.HasExtraAccounts())
}

func TestHealthTick_AnAccountAddedElsewhereShowsTheStrip(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	m.refreshAccountViews()
	require.Equal(t, 0, m.accountStrip.Height())

	_, _, err := otherTerminal(t, m).Create("max-2", main)
	require.NoError(t, err)
	m.core.ReloadAccounts()
	cmd := m.drainCore()

	assert.NotNil(t, cmd, "the strip appearing asks for a relayout")
	assert.Equal(t, 1, m.accountStrip.Height())
	assert.True(t, ui.ShowAccounts())
}

func TestHealthTick_ACorruptFileSurfacesOneErrorNotOnePerTick(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, os.WriteFile(testModel(m).AccountsRegistryForTest().Path(), []byte("not json"), 0o644))

	tickModel(t, m)
	require.Contains(t, m.errBox.String(), "accounts")
	m.errBox.Clear()

	tickModel(t, m)
	tickModel(t, m)
	assert.NotContains(t, m.errBox.String(), "accounts", "an unchanged corrupt file is not re-reported")

	// Still corrupt, differently: the error was already surfaced.
	require.NoError(t, os.WriteFile(testModel(m).AccountsRegistryForTest().Path(), []byte("still not json"), 0o644))
	tickModel(t, m)
	assert.NotContains(t, m.errBox.String(), "accounts")
	assert.Error(t, testModel(m).AccountsRegistryForTest().LoadErr())
}
