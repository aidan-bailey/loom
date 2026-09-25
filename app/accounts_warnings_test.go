package app

import (
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noCredentialOverride clears every credential env var that overrides the
// accounts, so a developer's own environment can't leak into a test.
func noCredentialOverride(t *testing.T) {
	t.Helper()
	for _, v := range account.CredentialOverrides {
		t.Setenv(v, "")
	}
}

// TestAccountsRefreshed_AnExtraAccountsBlockedHintNamesItsOwnLogin: `claude
// auth login` logs in the default account; an extra account logs in with
// `loom account login <name>` or from the Accounts screen.
func TestAccountsRefreshed_AnExtraAccountsBlockedHintNamesItsOwnLogin(t *testing.T) {
	noCredentialOverride(t)
	for _, tc := range []struct {
		name string
		id   account.Identity
	}{
		{"not logged in", account.Identity{LoggedIn: false}},
		{"not a claude.ai login", account.Identity{LoggedIn: true, AuthMethod: "console"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestHome(t)
			withAccounts(t, m, "max-2")
			acct, _ := m.accounts.Get("max-2")
			tc.id.ConfigDir = acct.Dir

			m.Update(accountsRefreshedMsg{auth: map[string]session.RemoteControlAuth{"max-2": {
				State: session.RemoteControlAuthBlocked, Reason: "… Run `claude auth login`.", Identity: tc.id,
			}}})

			reason := m.rcAuthFor("max-2").Reason
			assert.Contains(t, reason, "loom account login max-2")
			assert.Contains(t, reason, "Settings → Accounts → l")
			assert.NotContains(t, reason, "claude auth login")
			assert.True(t, m.rcAuthFor("max-2").Blocked(), "still blocked")
		})
	}
}

func TestAccountsRefreshed_TheDefaultAccountsHintIsUnchanged(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	main := withAccounts(t, m, "max-2")
	def := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in to Claude — run `claude auth login`.",
		Identity: account.Identity{ConfigDir: main}}

	m.Update(accountsRefreshedMsg{defaultAuth: &def})

	assert.Equal(t, def.Reason, m.rcAuth.Reason)
}

// TestAccountsRefreshed_AnOverridesHintIsUnchanged: with a credential in
// loom's environment, logging the account in changes nothing; the hint
// about the variable stands.
func TestAccountsRefreshed_AnOverridesHintIsUnchanged(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	reason := "ANTHROPIC_API_KEY is set — remote control needs a claude.ai login. Unset it, or run `claude auth login`."

	m.Update(accountsRefreshedMsg{auth: map[string]session.RemoteControlAuth{"max-2": {
		State: session.RemoteControlAuthBlocked, Reason: reason, Identity: account.Identity{LoggedIn: true, AuthMethod: "claude.ai"},
	}}})

	assert.Equal(t, reason, m.rcAuthFor("max-2").Reason)
}

func TestAccountsRefreshed_AnOKAuthIsUntouched(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	ok := session.RemoteControlAuth{State: session.RemoteControlAuthOK, Identity: account.Identity{LoggedIn: true, AuthMethod: "claude.ai"}}

	m.Update(accountsRefreshedMsg{auth: map[string]session.RemoteControlAuth{"max-2": ok}})

	require.True(t, m.rcAuthFor("max-2").OK())
	assert.Empty(t, m.rcAuthFor("max-2").Reason)
}
