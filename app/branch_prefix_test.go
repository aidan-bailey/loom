package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaunchOptionsFromConfigSeedsBranchPrefix(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.BranchPrefix = "team/"

	opts := launch.FromConfig(cfg)

	assert.Equal(t, "team/", opts.BranchPrefix, "the modal must open showing the prefix that would otherwise be used")
}

// editBranchPrefixTo drives the real modal: move to the Branch Prefix row,
// open the editor, clear it, type value, and commit.
func editBranchPrefixTo(m *home, current, value string) {
	for i := 0; i < 7; i++ {
		handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	for i := 0; i < len(current); i++ {
		handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range value {
		handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // commit the edit
}

func TestNewInstanceFlowAppliesBranchPrefixOverride(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	// Not the user's name: the default prefix is "<user>/", so a fixture
	// equal to it would pass even if the modal never saw this config.
	m.appConfig().BranchPrefix = "team/"
	m.syncWorkspaces() // the modal reads the slot's published settings

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, stateLaunchOptions, m.state)

	editBranchPrefixTo(m, "team/", "spike/")
	require.Equal(t, stateLaunchOptions, m.state, "committing the edit must not close the modal")

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // confirm
	instance := lastInst(m)                                             // the confirm's Create made it

	require.NotNil(t, instance.BranchPrefixOverride())
	assert.Equal(t, "spike/", *instance.BranchPrefixOverride())
}

// TestNewInstanceFlowRecordsPrefixEvenWhenUnedited pins that the override is
// always set from the modal, so the branch name comes from one source rather
// than sometimes the instance and sometimes a re-read of config.json.
func TestNewInstanceFlowRecordsPrefixEvenWhenUnedited(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.appConfig().BranchPrefix = "team/"
	m.syncWorkspaces()

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	instance := lastInst(m) // the confirm's Create made it

	require.NotNil(t, instance.BranchPrefixOverride())
	assert.Equal(t, "team/", *instance.BranchPrefixOverride())
}

// TestRestartWithOptionsLocksBranchPrefixRow pins that the restart path shows
// the existing branch read-only: the branch is already created, so offering
// an editable prefix would imply a rename that cannot happen.
func TestRestartWithOptionsLocksBranchPrefixRow(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	inst.Branch = "aidanb/restart-me"
	m.syncViews()

	runRestartWithOptionsSelected(m)

	lo := m.launchOptionsOverlay()
	require.NotNil(t, lo)
	assert.Contains(t, lo.Render(), "aidanb/restart-me")

	before := lo.Options().BranchPrefix
	editBranchPrefixTo(m, before, "zzz/")
	assert.Equal(t, before, lo.Options().BranchPrefix, "a locked row must ignore edits")
}

// TestRestartWithOptionsLocksPrefixEvenWithoutABranchName guards the reason
// locking is a bool rather than "lockedBranch != \"\"": an instance with no
// recorded branch must still get a read-only row, not a silently ineffective
// editable one.
func TestRestartWithOptionsLocksPrefixEvenWithoutABranchName(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	require.Empty(t, inst.GetBranch())

	runRestartWithOptionsSelected(m)
	lo := m.launchOptionsOverlay()
	require.NotNil(t, lo)

	editBranchPrefixTo(m, "", "zzz/")
	assert.Empty(t, lo.Options().BranchPrefix, "a locked row must ignore edits even with no branch to display")
}

var _ = overlay.LaunchOptions{}
