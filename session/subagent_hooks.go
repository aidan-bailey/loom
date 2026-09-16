package session

import (
	"context"
	"os"
	"os/exec"
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
// whether a launch gets hooks; sessions already launched with hooks keep
// being scanned, so their rows never go stale when the setting changes.
var subagentTrackingEnabled atomic.Bool

// SetSubagentTrackingEnabled updates the global subagent-tracking toggle.
func SetSubagentTrackingEnabled(enabled bool) { subagentTrackingEnabled.Store(enabled) }

// hooksRoot holds every instance's hooks folder. It is deliberately outside
// worktrees/: DiscoverOrphans descends into any directory there that lacks
// the _<hex> suffix.
func hooksRoot(configDir string) string { return filepath.Join(configDir, "hooks") }

// SubagentHooksDir returns the hooks folder for the instance titled title.
// It is keyed by tmux session name, so workspace terminals, which have no
// worktree, get one too.
func SubagentHooksDir(configDir, title string) string {
	return filepath.Join(hooksRoot(configDir), tmux.ToLoomTmuxName(title))
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
// hooks folder, and preparing a new one would wipe its history.
func (i *Instance) launchProgram(program string, launching bool) string {
	program = loomContextProgram(program, i.ConfigDir, i.IsWorkspaceTerminal)
	if launching {
		program = i.prepareSubagentHooks(program)
	}
	return program
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
	claimed := make(map[string]bool, len(claimedTitles))
	for title := range claimedTitles {
		claimed[tmux.ToLoomTmuxName(title)] = true
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
	out, err := cmdExec.Output(exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}"))
	if err != nil {
		return
	}
	alive := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			alive[name] = true
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
