package app

import (
	"errors"
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
	return account.LoadRegistry(filepath.Dir(m.accounts.Path()))
}

func TestHealthTick_RemovingTheLastExtraAccountElsewhereHidesTheStrip(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.refreshAccountViews()
	require.Equal(t, 1, m.accountStrip.Height())

	_, err := otherTerminal(t, m).Remove("max-2", false)
	require.NoError(t, err)
	m.Update(tickUpdateMetadataMessage{})

	assert.Equal(t, 0, m.accountStrip.Height(), "the strip follows the file on the next tick")
	assert.False(t, ui.ShowAccounts(), "and the badges with it")
	assert.False(t, m.hasExtraAccounts())
}

func TestHealthTick_AnAccountAddedElsewhereShowsTheStrip(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	m.refreshAccountViews()
	require.Equal(t, 0, m.accountStrip.Height())

	_, _, err := otherTerminal(t, m).Create("max-2", main)
	require.NoError(t, err)
	cmd := m.maybeReloadAccounts()

	assert.NotNil(t, cmd, "the strip appearing asks for a relayout")
	assert.Equal(t, 1, m.accountStrip.Height())
	assert.True(t, ui.ShowAccounts())
}

func TestHealthTick_ACorruptFileSurfacesOneErrorNotOnePerTick(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, os.WriteFile(m.accounts.Path(), []byte("not json"), 0o644))

	m.Update(tickUpdateMetadataMessage{})
	require.Contains(t, m.errBox.String(), "accounts")
	m.errBox.Clear()

	m.Update(tickUpdateMetadataMessage{})
	m.Update(tickUpdateMetadataMessage{})
	assert.NotContains(t, m.errBox.String(), "accounts", "an unchanged corrupt file is not re-reported")

	// Still corrupt, differently: the error was already surfaced.
	require.NoError(t, os.WriteFile(m.accounts.Path(), []byte("still not json"), 0o644))
	m.Update(tickUpdateMetadataMessage{})
	assert.NotContains(t, m.errBox.String(), "accounts")
	assert.Error(t, m.accounts.LoadErr())
}

func TestMaybeReloadAccounts_AnUnchangedFileIsNotReread(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	// In memory only: a reread would put the file's "" back.
	m.accounts.DefaultAccount = "max-2"

	assert.Nil(t, m.maybeReloadAccounts())
	assert.Equal(t, "max-2", m.accounts.DefaultAccount, "not reread")
}

func TestMaybeReloadAccounts_ARegistryWithNoFileDoesNothing(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	orig := errors.New("no home directory")
	m.accounts = account.Unavailable(orig)

	assert.Nil(t, m.maybeReloadAccounts())
	assert.ErrorIs(t, m.accounts.LoadErr(), orig)
}

// TestUsageProbe_DoesNotReadTheRegistry: the builder runs on every health
// tick that finds the gate due, and returns nil without an extra account,
// which leaves the gate due again next tick; the registry is the tick's
// cheap stat's business, not the builder's.
func TestUsageProbe_DoesNotReadTheRegistry(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m, "max-2")
	m.accounts.Accounts = nil // in memory only

	assert.Nil(t, m.maybeUsageProbe())
	assert.False(t, m.hasExtraAccounts(), "not reread")
}
