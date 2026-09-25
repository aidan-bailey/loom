package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRosterStatusFor_JoinsTheInstancesAccountRoster(t *testing.T) {
	inst := startedInstanceWithProgram(t, "acct-join", "claude", "x")
	inst.SetAccount("max-2")
	m := homeWithAppState(t)
	wt := inst.GetWorktreePath()
	m.roster = map[string]session.RosterEntry{wt: {Status: session.RosterStatusBusy}}
	m.rosterByAccount = map[string]map[string]session.RosterEntry{"max-2": {wt: {Status: session.RosterStatusIdle}}}

	status, _, ok := m.rosterStatusFor(inst)

	require.True(t, ok)
	assert.Equal(t, session.Ready, status, "an account's session is joined against that account's roster")
}

func TestRosterStatusFor_TheDefaultRosterNeverAnswersForAnotherAccount(t *testing.T) {
	inst := startedInstanceWithProgram(t, "acct-none", "claude", "x")
	inst.SetAccount("max-2")
	m := homeWithAppState(t)
	m.roster = map[string]session.RosterEntry{inst.GetWorktreePath(): {Status: session.RosterStatusBusy}}

	_, _, ok := m.rosterStatusFor(inst)

	assert.False(t, ok)
}

func TestRosterReady_OneAccountsFailureKeepsTheOthers(t *testing.T) {
	m := homeWithAppState(t)

	m.Update(rosterReadyMsg{
		entries:   map[string]session.RosterEntry{"/w": {Status: session.RosterStatusBusy}},
		extra:     map[string]map[string]session.RosterEntry{"max-3": {"/v": {Status: session.RosterStatusIdle}}},
		extraErrs: map[string]error{"max-2": errors.New("daemon down")},
	})

	assert.Len(t, m.roster, 1)
	assert.Contains(t, m.rosterByAccount, "max-3")
	assert.NotContains(t, m.rosterByAccount, "max-2")
}

// TestRosterQueryCmd_NeverQueriesAnAccountAsAnother: an instance whose
// account is gone from the registry, or whose account dir vanished, must
// not be answered for by some other account's roster. Neither case gets
// as far as spawning the CLI, so this runs no subprocess.
func TestRosterQueryCmd_NeverQueriesAnAccountAsAnother(t *testing.T) {
	gone := startedInstanceWithProgram(t, "acct-gone", "claude", "x")
	gone.SetAccount("gone")
	missing := startedInstanceWithProgram(t, "acct-missing", "claude", "x")
	missing.SetAccount("max-2")
	dirs := map[string]string{"max-2": filepath.Join(t.TempDir(), "deleted")}

	cmd := rosterQueryCmd([]*session.Instance{gone, missing}, dirs)
	require.NotNil(t, cmd)
	msg, ok := cmd().(rosterReadyMsg)
	require.True(t, ok)

	assert.Nil(t, msg.entries, "no default-account instance: no default query")
	assert.NoError(t, msg.err)
	assert.NotContains(t, msg.extra, "gone", "an unregistered account is not queried at all")
	assert.NotContains(t, msg.extraErrs, "gone")
	assert.ErrorIs(t, msg.extraErrs["max-2"], account.ErrAccountDirMissing)
}
