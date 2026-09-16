package session

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/aidan-bailey/loom/session/tmux"
)

// subagentTrackingEnabled mirrors config.SubagentTrackingEnabled(). The
// app sets it at the same points as SetLoomContextEnabled. It only decides
// whether a launch gets hooks: a session already launched with hooks keeps
// being scanned under the old setting only until its next launch, when
// resetSubagentLaunch clears its state and prepareSubagentHooks re-decides.
var subagentTrackingEnabled atomic.Bool

// noHooksLaunchID is the hookLaunchID a launch starts with before
// prepareSubagentHooks runs. subagent.Prepare's launch IDs are 16
// lowercase hex characters, so this can never collide with a real one: it
// exists so ApplySubagentScan drops every scan result — from the previous
// launch's folder, or one already in flight — until a successful Prepare
// (if any) sets a real ID for the new launch.
const noHooksLaunchID = "-"

// SetSubagentTrackingEnabled updates the global subagent-tracking toggle.
func SetSubagentTrackingEnabled(enabled bool) { subagentTrackingEnabled.Store(enabled) }

// hooksRoot holds every instance's hooks folder. It is deliberately outside
// worktrees/: DiscoverOrphans descends into any directory there that lacks
// the _<hex> suffix.
func hooksRoot(configDir string) string { return filepath.Join(configDir, "hooks") }

// hooksFolderName is the hooks folder name for the instance titled title:
// its tmux session name, path-escaped so it is always a single path
// segment. Ordinary titles come out unchanged; "/" becomes "%2F" and "'"
// becomes "%27". Titles accept any printable text, and ToLoomTmuxName
// only drops whitespace and "."; unescaped, "fix/login" would nest inside
// the folder of "fix", which launching or killing "fix" wipes and
// SweepSubagentHooks deletes as an unclaimed "loom_fix".
func hooksFolderName(title string) string {
	return url.PathEscape(tmux.ToLoomTmuxName(title))
}

// SubagentHooksDir returns the hooks folder for the instance titled title,
// a direct child of the hooks root. It is keyed by tmux session name (see
// hooksFolderName), so workspace terminals, which have no worktree, get
// one too.
func SubagentHooksDir(configDir, title string) string {
	return filepath.Join(hooksRoot(configDir), hooksFolderName(title))
}

// BuildSettingsCommand returns program with Claude's --settings flag
// pointing at path. The adapter registry no-ops for non-Claude programs.
func BuildSettingsCommand(program, path string) string {
	return defaultRegistry.Lookup(program).ApplySettingsFlag(program, path)
}

// subagentLive reports whether an instance in status s can have running
// agents.
func subagentLive(s Status) bool {
	return s == Running || s == Ready || s == Prompting || s == Loading
}

// launchProgram composes the command for a new tmux session: loom's
// context flag and, when launching is true, loom's subagent hooks.
// launching is false only for Start(false), which reattaches to a live
// session with Restore. That Claude is still writing to its existing
// hooks folder, and preparing a new one would wipe its history. When
// launching is true, resetSubagentLaunch first clears any state left by
// the previous launch, so a relaunch that ends up skipping hooks
// (tracking turned off, program no longer Claude, etc.) never keeps a
// stale row, a stale launch ID or the old hooks folder around.
func (i *Instance) launchProgram(program string, launching bool) string {
	program = loomContextProgram(program, i.ConfigDir, i.IsWorkspaceTerminal)
	if launching {
		i.resetSubagentLaunch()
		program = i.prepareSubagentHooks(program)
	}
	return program
}

// resetSubagentLaunch clears the previous launch's subagent state before a
// new process starts: the tracker and warm flag, hookLaunchID (set to the
// sentinel, so no stale scan result can match until prepareSubagentHooks
// maybe sets a real one), and the old hooks folder on disk. Called only
// when launching is true, so no old Claude process for this instance can
// still be writing to that folder.
func (i *Instance) resetSubagentLaunch() {
	i.mu.Lock()
	i.hookLaunchID = noHooksLaunchID
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	i.removeSubagentHooks()
}

// recoveryLaunch returns the recovery program (what InstanceEnv keys off)
// and the full launch command. startFreshWithRecovery and CrashRestart
// always start a new Claude process.
func (i *Instance) recoveryLaunch() (program, launch string) {
	program = BuildRecoveryCommand(i.Program)
	return program, i.launchProgram(program, true)
}

// prepareSubagentHooks readies a fresh hooks folder and returns program
// with --settings added. On any failure it returns program unchanged, so
// the session still launches, just untracked. It also adopts the new
// launch ID and resets the tracker, so scan results from before this
// launch are dropped.
func (i *Instance) prepareSubagentHooks(program string) string {
	if !subagentTrackingEnabled.Load() || i.ConfigDir == "" ||
		runtime.GOOS == "windows" || !IsClaudeProgram(program) {
		return program
	}
	if agent.HasSettingsFlag(program) {
		i.getLogger().Info("subagent_hooks.skipped", "reason", "program already passes --settings")
		return program
	}
	dir := SubagentHooksDir(i.ConfigDir, i.Title)
	if !subagent.SafePath(dir) {
		i.getLogger().Debug("subagent_hooks.skipped", "reason", "single quote in hooks folder path")
		return program
	}
	launchID, err := subagent.Prepare(dir)
	if err != nil {
		i.getLogger().Warn("subagent_hooks.prepare_failed", "err", err.Error())
		return program
	}
	i.mu.Lock()
	i.hookLaunchID = launchID
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	return BuildSettingsCommand(program, subagent.SettingsPath(dir))
}

// subagentTrackerLocked returns the tracker, creating it on first use.
// Callers hold i.mu for writing.
func (i *Instance) subagentTrackerLocked() *subagent.Tracker {
	if i.subagents == nil {
		i.subagents = subagent.NewTracker()
	}
	return i.subagents
}

// SubagentScanRequest describes the scan this instance needs, or returns
// false when it should not be scanned: a non-Claude agent, no config dir,
// or a status in which no agent can be running. Call it on the Update
// goroutine; the returned request is safe to hand to a tea.Cmd.
func (i *Instance) SubagentScanRequest() (subagent.Request, bool) {
	if i.ConfigDir == "" || !IsClaudeProgram(i.Program) {
		return subagent.Request{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !subagentLive(i.Status) {
		return subagent.Request{}, false
	}
	return subagent.Request{
		Dir:         SubagentHooksDir(i.ConfigDir, i.Title),
		Cold:        !i.subagentWarm,
		MissingMeta: i.subagentTrackerLocked().MissingMeta(),
	}, true
}

// ApplySubagentScan applies one scan result and reports whether it was
// applied. A result for another launch is dropped. An instance restored
// after a loom restart has no launch ID yet and adopts the result's. A
// replayed result rebuilds the tracker from scratch, and an incremental
// one is only applied once the tracker is warm.
func (i *Instance) ApplySubagentScan(res subagent.Result) bool {
	if res.LaunchID == "" {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	switch {
	case i.hookLaunchID == "":
		i.hookLaunchID = res.LaunchID
	case i.hookLaunchID != res.LaunchID:
		return false
	}
	tracker := i.subagentTrackerLocked()
	if res.Replayed {
		tracker.Reset()
	} else if !i.subagentWarm {
		return false
	}
	tracker.Apply(res.Events, res.Meta)
	i.subagentWarm = true
	return true
}

// ForgetSubagentsWithoutHooks drops the tracked agents of a launch whose
// hooks folder has disappeared, which a scan reports as
// subagent.ErrNoHooks: the folder was deleted by hand, or by another loom
// process's sweep. Without it the last rows would stay on screen until
// the next launch. It acts only when hookLaunchID is a real launch ID:
// for an instance restored after a loom restart ("", nothing adopted yet)
// or launched without hooks (noHooksLaunchID), ErrNoHooks is the normal
// state. hookLaunchID itself is kept, so a folder written by another
// launch is still never adopted. Call it on the Update goroutine.
func (i *Instance) ForgetSubagentsWithoutHooks() {
	i.mu.Lock()
	if i.hookLaunchID == "" || i.hookLaunchID == noHooksLaunchID {
		i.mu.Unlock()
		return
	}
	wasWarm := i.subagentWarm
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	if wasWarm {
		i.getLogger().Debug("subagent_hooks.folder_gone", "action", "tracked agents dropped")
	}
}

// Subagents returns the live agents to render, or nil when nothing is
// tracked or the instance is in a status where no agent can be running
// (Paused, Recoverable, Deleting).
func (i *Instance) Subagents() []subagent.View {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.subagents == nil || !subagentLive(i.Status) {
		return nil
	}
	return i.subagents.Visible()
}

// removeSubagentHooks deletes this instance's hooks folder. Best effort:
// SweepSubagentHooks removes leftovers on a later load.
func (i *Instance) removeSubagentHooks() {
	if i.ConfigDir == "" {
		return
	}
	if err := os.RemoveAll(SubagentHooksDir(i.ConfigDir, i.Title)); err != nil {
		log.For("session").Debug("subagent_hooks.remove_failed", "title", i.Title, "err", err)
	}
}

// SweepSubagentHooks removes hooks folders under configDir that no
// instance claims and whose tmux session is not running. claimedTitles
// holds the title of every instance in the workspace. If tmux cannot be
// queried, nothing is removed, so a folder in use by a live session, such
// as one owned by another loom process, is never deleted on a guess.
func SweepSubagentHooks(configDir string, claimedTitles map[string]bool, cmdExec internalexec.Executor) {
	if configDir == "" {
		return
	}
	entries, err := os.ReadDir(hooksRoot(configDir))
	if err != nil {
		return
	}
	// Both sets are keyed by folder name, so they compare against the
	// directory entries the same way.
	claimed := make(map[string]bool, len(claimedTitles))
	for title := range claimedTitles {
		claimed[hooksFolderName(title)] = true
	}
	var unclaimed []string
	for _, e := range entries {
		if e.IsDir() && !claimed[e.Name()] {
			unclaimed = append(unclaimed, e.Name())
		}
	}
	if len(unclaimed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
	defer cancel()
	out, err := cmdExec.Output(tmux.Command(ctx, "list-sessions", "-F", "#{session_name}"))
	if err != nil {
		return
	}
	alive := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			alive[url.PathEscape(name)] = true
		}
	}
	for _, name := range unclaimed {
		if alive[name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(hooksRoot(configDir), name)); err != nil {
			log.For("session").Debug("subagent_hooks.sweep_failed", "folder", name, "err", err)
		}
	}
}
