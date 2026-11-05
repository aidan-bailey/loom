package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
//
// Saving the global workspace never writes a "scheduler" block it was
// handed or holds: the block is the work queue's, which a hand edit or
// another client's SetSchedulerCaps changes in the file, while ws.cfg and
// an overlay's copy of the settings go stale. So the block is re-read from
// the global config.json (as refreshPolicy does), written, and set as
// ws.cfg's. A file that can't be read or parsed refuses the save and
// changes nothing; a missing file reads as no block.
func (m *Model) SaveSettings(id WorkspaceID, s config.Settings) error {
	ws := m.wsLookup(id)
	if ws == nil || ws.cfg == nil {
		return fmt.Errorf("save settings: workspace %d is not served", id)
	}
	// The build sweep's threshold is ConfigureBuildSweep's to change: a
	// client's copy made before a change must not write the old one back.
	s.BuildSweepAfterHours = ws.cfg.Snapshot().BuildSweepAfterHours
	if isGlobalWS(ws) {
		block, err := diskSchedulerBlock(ws.configDir())
		if err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		s.Scheduler = block // s is this call's copy: the caller's is not changed
	}
	ws.cfg.ReplaceSettings(s)
	dir := settingsDir(ws)
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

// settingsDir is the dir SaveSettings writes ws's config.json to: its
// context's config dir, or the default config dir for a bare context,
// which loaded from there (otherwise the change would live only in memory
// and silently vanish on restart); "" when neither resolves.
func settingsDir(ws *Workspace) string {
	if dir := ws.configDir(); dir != "" {
		return dir
	}
	if globalDir, err := config.GetConfigDir(); err == nil {
		return globalDir
	}
	return ""
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

// diskSchedulerBlock reads the "scheduler" block of dir's config.json as the
// file holds it: nil for a file without the block or a missing file. A file
// that can't be read or parsed is an error, and is left as it is
// (config.LoadConfigFrom would quarantine an unparseable one and fall back to
// defaults, which would make the save write a block nobody chose).
func diskSchedulerBlock(dir string) (*config.SchedulerSettings, error) {
	return diskConfigBlock[config.SchedulerSettings](dir, "scheduler")
}

// diskConfigBlock reads the block under key from dir's config.json: nil
// when the file or the key is absent, an error when the file can't be read
// or parsed.
func diskConfigBlock[T any](dir, key string) (*T, error) {
	path := filepath.Join(dir, config.ConfigFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the %s block from %s: %w", key, path, err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	raw, ok := file[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var block T
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("parse %s: the %s block: %w", path, key, err)
	}
	return &block, nil
}
