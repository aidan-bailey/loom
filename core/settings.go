package core

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
)

// SaveSettings replaces the workspace's settings with s and writes its
// config.json: to its context's config dir, or to the default config dir
// for a bare context. Then it applies what a settings change does at
// once: the two launch toggles the session package keeps per config dir
// (SetLoomContextEnabled, SetSubagentTrackingEnabled) and, for the global
// workspace only, the agent program the model runs the accounts' Claude
// commands and orphan placeholders with (m.program, ClaudeProgram): in a
// daemon that program is the global config's (Options.Program), so one
// workspace's save must not replace it for every other. The program a
// client's drafts default to is the client's to follow. An unknown id is
// an error. Moved from app/state_settings.go, which did this to the
// model's own config.
func (m *Model) SaveSettings(id WorkspaceID, s config.Settings) error {
	ws := m.wsLookup(id)
	if ws == nil || ws.cfg == nil {
		return fmt.Errorf("save settings: workspace %d is not served", id)
	}
	ws.cfg.ReplaceSettings(s)
	dir := ""
	if ws.ctx != nil {
		dir = ws.ctx.ConfigDir
	}
	if dir == "" {
		// A bare context loaded from the default dir, so save there:
		// otherwise the change lives only in memory and silently vanishes
		// on restart.
		if globalDir, err := config.GetConfigDir(); err == nil {
			dir = globalDir
		}
	}
	if dir != "" {
		if err := config.SaveConfigTo(ws.cfg, dir); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}
	if isGlobalWS(ws) {
		m.program = ws.cfg.GetProgram()
	}
	// Re-sync the session flags so an in-place change takes effect on the
	// workspace's next session launch. Keyed by the context's config dir,
	// which its instances carry, not the dir the settings were saved to.
	flagsDir := ""
	if ws.ctx != nil {
		flagsDir = ws.ctx.ConfigDir
	}
	syncSessionFlags(ws.cfg, flagsDir)
	return nil
}

// SetUIPrefs replaces and persists the workspace's UI prefs.
func (m *Model) SetUIPrefs(id WorkspaceID, p config.UIPrefs) error {
	ws := m.wsLookup(id)
	if ws == nil || ws.state == nil {
		return fmt.Errorf("save UI prefs: workspace %d is not served", id)
	}
	return ws.state.SetUIPrefs(p.Clone())
}

// SetHelpScreensSeen replaces and persists the workspace's seen help
// screens.
func (m *Model) SetHelpScreensSeen(id WorkspaceID, seen uint32) error {
	ws := m.wsLookup(id)
	if ws == nil || ws.state == nil {
		return fmt.Errorf("save help screens: workspace %d is not served", id)
	}
	return ws.state.SetHelpScreensSeen(seen)
}

// isGlobalWS reports whether ws is the global workspace: its config dir is
// the global config dir (config.GlobalWorkspaceContext), compared
// canonically.
func isGlobalWS(ws *Workspace) bool {
	dir := ws.configDir()
	if dir == "" {
		return false
	}
	global, err := config.GetGlobalConfigDir()
	return err == nil && canonicalDir(dir) == canonicalDir(global)
}
