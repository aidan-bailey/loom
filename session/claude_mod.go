package session

import (
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/aidan-bailey/loom/session/claudemod"
)

// modSocket is the socket the daemon serves the loom mod on (SetModSocket).
var modSocket atomic.Pointer[string]

// SetModSocket names the socket the daemon serves the loom mod on: every
// Claude launch from now on gets the mod (--plugin-dir) and the socket
// (claudemod.EnvSocket). "" stops giving launches the mod. The daemon sets it
// before its boot relaunches anything (internal/daemon.Serve). It is process
// wide, not per config dir, since one daemon serves one global dir.
func SetModSocket(path string) { modSocket.Store(&path) }

// ModSocket is the socket SetModSocket named, "" for none.
func ModSocket() string {
	if p := modSocket.Load(); p != nil {
		return *p
	}
	return ""
}

// modDirOf is where a launch's mod lives in its hooks folder.
func modDirOf(hooksDir string) string { return filepath.Join(hooksDir, "mod") }

// prepareMod writes the loom mod into the launch's hooks folder (dir, which
// hooks.Prepare just emptied) and returns the environment that points it at
// the daemon, or nil when no daemon serves the mod or the write failed: the
// launch then goes without it, and the daemon counts it absent.
func (i *Instance) prepareMod(dir, launchID string) []string {
	socket := ModSocket()
	if socket == "" {
		return nil
	}
	if err := claudemod.WriteTo(modDirOf(dir)); err != nil {
		i.getLogger().Warn("claude_mod.write_failed", "err", err.Error())
		return nil
	}
	return []string{claudemod.EnvSocket + "=" + socket, claudemod.EnvLaunch + "=" + launchID}
}

// modEnv is the environment that lets the current launch's mod reach the
// daemon, nil when the launch got no mod. The launch sites append it to
// InstanceEnv after launchProgram, which decides it.
func (i *Instance) modEnv() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return slices.Clone(i.modEnvVars)
}

// HookLaunch is the current launch's hooks ID and whether the launch is
// hooked: prepareHooks prepared its folder, or a restored instance adopted
// the ID its scan read. It is ("", false) before a restored instance's first
// scan, and (noHooksLaunchID, false) for a launch without hooks.
func (i *Instance) HookLaunch() (string, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.hookLaunchID, i.hookLaunchID != "" && i.hookLaunchID != noHooksLaunchID
}

// LaunchSessionStartedAt is when the current launch's Claude reported its
// SessionStart (startup or resume), and whether it has.
func (i *Instance) LaunchSessionStartedAt() (time.Time, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.sessionStartedAt, !i.claude.sessionStartedAt.IsZero()
}
