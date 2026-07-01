package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHomeWithActiveCtx extends newTestHome with a resolved
// activeCtx, since handleStateSettingsKey needs ConfigDir to persist.
func newTestHomeWithActiveCtx(t *testing.T) *home {
	t.Helper()
	m := newTestHome(t)
	m.activeCtx = &config.WorkspaceContext{ConfigDir: t.TempDir()}
	m.program = m.appConfig.DefaultProgram
	return m
}

func TestHandleStateSettingsKeyRefreshesProgramShadow(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	so := overlay.NewSettingsOverlay(m.appConfig, false, "")
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings

	// Default Program starts at whatever DefaultConfig resolved (an
	// absolute path GetClaudeCommand found on PATH, not a short fixed
	// string); edit it to a distinct value and confirm m.program
	// follows. The textarea pre-fills with the current value and
	// leaves the cursor at the end, so the existing text must be
	// cleared before typing or "aider" would land appended to it.
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // open edit on row 0 (Default Program)
	for i := 0; i < 128; i++ {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range "aider" {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit

	assert.Equal(t, "aider", m.appConfig.DefaultProgram)
	assert.Equal(t, "aider", m.program, "m.program must be refreshed, not left stale")
}

func TestHandleStateSettingsKeyPersistsToDisk(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	so := overlay.NewSettingsOverlay(m.appConfig, false, "")
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings

	// Branch Prefix is row 2 (Default Program, Daemon Poll Interval, Branch Prefix).
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // open edit
	for i := 0; i < 64; i++ {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range "team/" {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit

	reloaded := config.LoadConfigFrom(m.activeCtx.ConfigDir)
	require.NotNil(t, reloaded)
	assert.Equal(t, "team/", reloaded.BranchPrefix, "the edit must be persisted immediately, not only in memory")
}

func TestSettingsDrillsIntoClaudePreferences(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	_, _ = runOpenSettings(m)

	so := m.settingsOverlay()
	require.NotNil(t, so)

	// Row 4 is Claude Preferences (Default Program, Daemon Poll Interval,
	// Branch Prefix, Profiles, Claude Preferences).
	for i := 0; i < 4; i++ {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // drill in
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // toggle remote control off

	assert.False(t, m.appConfig.RemoteControlEnabled())
}
