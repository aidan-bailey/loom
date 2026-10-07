package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restoreSessionToggles puts the two process-wide launch toggles back as a
// test found them.
func restoreSessionToggles(t *testing.T) {
	loomCtx, tracking := session.LoomContextEnabled(), session.SubagentTrackingEnabled()
	t.Cleanup(func() {
		session.SetLoomContextEnabled(loomCtx)
		session.SetSubagentTrackingEnabled(tracking)
	})
}

func TestSaveSettings_WritesAppliesAndPublishes(t *testing.T) {
	restoreSessionToggles(t)
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.wsIDOf(ws)
	m.Sync()

	s := ws.cfg.Snapshot()
	s.DefaultProgram = "aider --yes"
	off, on := false, true
	s.ClaudeLoomContext = &off
	s.ClaudeSubagentTracking = &on
	session.SetSubagentTrackingEnabled(false)
	require.NoError(t, m.SaveSettings(id, s))

	assert.Equal(t, "aider --yes", m.Program(), "the agent program follows")
	assert.False(t, session.LoomContextEnabled(), "the loom-context toggle follows")
	assert.True(t, session.SubagentTrackingEnabled(), "the subagent-tracking toggle follows")
	data, err := os.ReadFile(filepath.Join(ws.ctx.ConfigDir, config.ConfigFileName))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"default_program": "aider --yes"`, "config.json is written")
	wc := workspacesEvent(m.Sync().Events)
	require.NotNil(t, wc, "the change republishes")
	assert.Equal(t, "aider --yes", wc.Views[0].Settings.DefaultProgram)

	s.DefaultProgram = "changed after"
	assert.Equal(t, "aider --yes", ws.cfg.GetProgram(), "the model keeps its own copy")
}

func TestSettingsRequests_RefuseAnUnknownWorkspace(t *testing.T) {
	m := NewForTest(Options{})
	assert.Error(t, m.SaveSettings(99, config.Settings{}))
	assert.Error(t, m.SetUIPrefs(99, config.UIPrefs{}))
	assert.Error(t, m.SetHelpScreensSeen(99, 1))
}

func TestSetUIPrefsAndHelpScreens_Persist(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.wsIDOf(ws)

	prefs := config.UIPrefs{RailHidden: true, SplitRatios: map[string]float64{"x": 0.4}}
	require.NoError(t, m.SetUIPrefs(id, prefs))
	require.NoError(t, m.SetHelpScreensSeen(id, 5))
	prefs.SplitRatios["x"] = 0.9

	reloaded := config.LoadStateFrom(ws.ctx.ConfigDir)
	assert.True(t, reloaded.GetUIPrefs().RailHidden, "state.json holds the prefs")
	assert.InDelta(t, 0.4, reloaded.GetUIPrefs().SplitRatios["x"], 0, "the model kept its own copy")
	assert.Equal(t, uint32(5), reloaded.GetHelpScreensSeen())
}
