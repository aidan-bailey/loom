package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckRoom_RefusesAtTheLimit(t *testing.T) {
	assert.NoError(t, CheckRoom(InstanceLimit-1))
	err := CheckRoom(InstanceLimit)
	require.Error(t, err)
	assert.Equal(t, "you can't create more than 10 instances", err.Error())
}

func TestCheckWritable_RefusesALatchedWorkspace(t *testing.T) {
	assert.NoError(t, CheckWritable(WorkspaceView{}))
	err := CheckWritable(WorkspaceView{WritesRefused: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this workspace's saved sessions could not be read")
}

func TestCheckNewTitle(t *testing.T) {
	ws := WorkspaceView{PreservedTitles: []string{"future"}}
	for _, tc := range []struct {
		name, title, want string
	}{
		{"a free title", "alpha", ""},
		{"exactly the widest", strings.Repeat("a", MaxTitleWidth), ""},
		{"empty", "", "title cannot be empty"},
		{"one cell too wide", strings.Repeat("a", MaxTitleWidth+1), "title cannot be longer than 32 characters"},
		{"wide runes count as cells", strings.Repeat("界", 17), "title cannot be longer than 32 characters"},
		{"preserved", "future", `title "future" belongs to a saved session this version of loom could not load; choose another`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckNewTitle(tc.title, ws)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

// accountsCore answers the account queries ChooseLaunchAccount reads; any
// other Core method panics (the embedded interface is nil).
type accountsCore struct {
	Core
	names  AccountNames
	loaded bool
	known  []string
}

func (c accountsCore) AccountNames() AccountNames { return c.names }
func (c accountsCore) AccountsLoaded() bool       { return c.loaded }
func (c accountsCore) Account(name string) (account.Account, bool) {
	return account.Account{Name: name}, slices.Contains(c.known, name)
}

func TestChooseLaunchAccount(t *testing.T) {
	present := AccountNames{Present: true, Default: "work", Names: []string{"work", account.DefaultName}}
	for _, tc := range []struct {
		name       string
		c          accountsCore
		program    string
		account    string
		want       string
		wantNotice string
	}{
		{"a non-Claude program records no account", accountsCore{names: present, loaded: true, known: []string{"work"}}, "aider", "work", "", ""},
		{"no account registry records none", accountsCore{}, "claude", "work", "", ""},
		{"none chosen takes the registry's default", accountsCore{names: present, loaded: true}, "claude", "", "work", ""},
		{"the default name is kept", accountsCore{names: present, loaded: true}, "claude", account.DefaultName, account.DefaultName, ""},
		{"a registry that failed to load keeps the choice", accountsCore{names: present}, "claude", "gone", "gone", ""},
		{"a registered account is kept", accountsCore{names: present, loaded: true, known: []string{"work"}}, "claude", "work", "work", ""},
		{"a removed account moves to the default, saying so", accountsCore{names: present, loaded: true}, "claude", "gone", "work", "gone was removed — this session will run on work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, notice := ChooseLaunchAccount(tc.c, launch.Options{Account: tc.account, Model: "opus"}, tc.program)
			assert.Equal(t, tc.want, opts.Account)
			assert.Equal(t, "opus", opts.Model, "the other options pass through")
			assert.Equal(t, tc.wantNotice, notice)
		})
	}
}
