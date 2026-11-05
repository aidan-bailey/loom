package session

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/session/hooks"
)

// flowOn mirrors config.FlowEnabled() per config dir; the model sets it
// beside loomContextOn (core's syncSessionFlags). A dir never set reads as
// off, so a test's launch gets flow only when it asks.
var flowOn dirFlags

// SetFlowEnabled sets the flow toggle of the sessions whose config dir is
// configDir.
func SetFlowEnabled(configDir string, enabled bool) { flowOn.set(configDir, enabled) }

// FlowEnabled reports configDir's flow toggle.
func FlowEnabled(configDir string) bool { return flowOn.get(configDir, false) }

// flowExecutable is the loom binary the guard hook runs; tests replace it.
var flowExecutable = os.Executable

// flowDirOf is where a launch's flow root lives in its hooks folder.
func flowDirOf(hooksDir string) string { return filepath.Join(hooksDir, "flow") }

// flowGuardCommand is the PreToolUse command that runs loom's git guard
// for flow's guarded agents, or "" when this launch gets no flow: the
// setting is off for its config dir, or loom can't name its own binary in
// a command (unknown, or holding a single quote). Without the guard no
// launch gets flow's plugin, so its agents never run unguarded.
func (i *Instance) flowGuardCommand() string {
	if !FlowEnabled(i.ConfigDir) {
		return ""
	}
	exe, err := flowExecutable()
	if err != nil || !hooks.SafePath(exe) {
		i.getLogger().Warn("flow.skipped", "reason", "loom's own binary can't be named in a hook command")
		return ""
	}
	return "'" + exe + "' flow guard"
}

// prepareFlow writes flow into the launch's hooks folder (dir, which
// hooks.Prepare just emptied) and returns the environment naming it, or
// nil when the write failed: the launch then goes without flow's plugin.
func (i *Instance) prepareFlow(dir string) []string {
	root := flowDirOf(dir)
	if err := flow.Write(root); err != nil {
		i.getLogger().Warn("flow.prepare_failed", "err", err.Error())
		return nil
	}
	return []string{flow.EnvRoot + "=" + root}
}

// flowEnv is the environment naming the current launch's flow root, nil
// when the launch got no flow. launchCommand appends it after modEnv.
func (i *Instance) flowEnv() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return slices.Clone(i.flowEnvVars)
}
