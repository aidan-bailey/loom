package session

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/aidan-bailey/loom/session/tmux"
)

// subagentRowsHidden is the inverse of config.SubagentTrackingEnabled(),
// so its zero value matches the config's default (nil means enabled). The
// app sets it through SetSubagentTrackingEnabled at the same points as
// SetLoomContextEnabled. It only decides whether Subagents returns the
// tracked rows: every Claude launch gets hooks, because they also carry
// the session's status, ID and last message, and the tracker keeps
// running with the setting off, so turning it back on shows the current
// agents at once.
var subagentRowsHidden atomic.Bool

// noHooksLaunchID is the hookLaunchID a launch starts with before
// prepareHooks runs. hooks.Prepare's launch IDs are 16
// lowercase hex characters, so this can never collide with a real one: it
// exists so ApplyHookScan drops every scan result — from the previous
// launch's folder, or one already in flight — until a successful Prepare
// (if any) sets a real ID for the new launch.
const noHooksLaunchID = "-"

// SetSubagentTrackingEnabled updates the global subagent-tracking toggle.
func SetSubagentTrackingEnabled(enabled bool) { subagentRowsHidden.Store(!enabled) }

// hooksRoot holds every instance's hooks folder. It is deliberately outside
// worktrees/: DiscoverOrphans descends into any directory there that lacks
// the _<hex> suffix.
func hooksRoot(configDir string) string { return filepath.Join(configDir, "hooks") }

// hooksFolderName is the hooks folder name for the instance titled title:
// its tmux session name, path-escaped so it is always a single path
// segment. Ordinary titles come out unchanged; "/" becomes "%2F" and "'"
// becomes "%27". Titles accept any printable text, and ToLoomTmuxName
// only drops whitespace and maps ":" and "." to "_"; unescaped,
// "fix/login" would nest inside the folder of "fix", which launching or
// killing "fix" wipes and SweepSubagentHooks deletes as an unclaimed
// "loom_fix".
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
// launching is true, resetHookLaunch first clears any state left by
// the previous launch, so a relaunch that ends up skipping hooks
// (tracking turned off, program no longer Claude, etc.) never keeps a
// stale row, a stale launch ID or the old hooks folder around.
func (i *Instance) launchProgram(program string, launching bool) string {
	program = loomContextProgram(program, i.ConfigDir, i.IsWorkspaceTerminal)
	if launching {
		i.resetHookLaunch()
		program = i.prepareHooks(program)
	}
	return program
}

// resetHookLaunch clears the previous launch's hook state before a new
// process starts: the tracker and warm flag, the Claude status and last
// message (the conversation ID is kept for the relaunch to resume),
// hookLaunchID (set to the sentinel, so no stale scan result can match
// until prepareHooks maybe sets a real one), and the old hooks folder on
// disk. Called only
// when launching is true, so no old Claude process for this instance can
// still be writing to that folder.
func (i *Instance) resetHookLaunch() {
	i.mu.Lock()
	i.hookLaunchID = noHooksLaunchID
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.claude.newLaunch(time.Now())
	i.mu.Unlock()
	i.removeSubagentHooks()
}

// recoveryLaunch returns the full launch command and the tmux session env
// for a recovery launch. The env keys off the recovery program (the bare
// program rewritten by BuildResumeCommand: --resume <id> for the recorded
// conversation, else --continue), not the full command.
// startFreshWithRecovery and CrashRestart always start a new Claude
// process, so an unregistered account fails the launch here.
func (i *Instance) recoveryLaunch() (launch string, env []string, err error) {
	le, err := i.launchEnv(true)
	if err != nil {
		return "", nil, err
	}
	sessionID, transcriptPath := i.ClaudeSession()
	le.Program = BuildResumeCommand(le.Program, sessionID, transcriptPath)
	return i.launchProgram(le.Program, true), InstanceEnv(le), nil
}

// prepareHooks readies a fresh hooks folder and returns program
// with --settings added. On any failure it returns program unchanged, so
// the session still launches, just untracked. It also adopts the new
// launch ID and resets the tracker, so scan results from before this
// launch are dropped.
func (i *Instance) prepareHooks(program string) string {
	if i.ConfigDir == "" || runtime.GOOS == "windows" || !IsClaudeProgram(program) {
		return program
	}
	if agent.HasSettingsFlag(program) {
		i.getLogger().Info("subagent_hooks.skipped", "reason", "program already passes --settings")
		return program
	}
	dir := SubagentHooksDir(i.ConfigDir, i.Title)
	if !hooks.SafePath(dir) {
		i.getLogger().Debug("subagent_hooks.skipped", "reason", "single quote in hooks folder path")
		return program
	}
	launchID, err := hooks.Prepare(dir)
	if err != nil {
		i.getLogger().Warn("subagent_hooks.prepare_failed", "err", err.Error())
		return program
	}
	i.mu.Lock()
	i.hookLaunchID = launchID
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	return BuildSettingsCommand(program, hooks.SettingsPath(dir))
}

// subagentTrackerLocked returns the tracker, creating it on first use.
// Callers hold i.mu for writing.
func (i *Instance) subagentTrackerLocked() *subagent.Tracker {
	if i.subagents == nil {
		i.subagents = subagent.NewTracker()
	}
	return i.subagents
}

// NextHookScan describes the scan this instance needs, or returns
// false when it should not be scanned: a non-Claude agent, no config dir,
// or a status in which no agent can be running. Call it on the Update
// goroutine; the returned request is safe to hand to a tea.Cmd.
func (i *Instance) NextHookScan() (HookScanRequest, bool) {
	// Update goroutine only, like the setters, so i.program needs no lock.
	if i.ConfigDir == "" || !IsClaudeProgram(i.program) {
		return HookScanRequest{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !subagentLive(i.Status) {
		return HookScanRequest{}, false
	}
	return HookScanRequest{
		Dir:         SubagentHooksDir(i.ConfigDir, i.Title),
		Cold:        !i.subagentWarm,
		MissingMeta: i.subagentTrackerLocked().MissingMeta(),
	}, true
}

// ApplyHookScan applies one scan result to the subagent tracker and the
// Claude state, and reports whether it was applied. A result for another
// launch is dropped. An instance restored after a loom restart has no
// launch ID yet and adopts the result's. A replayed result rebuilds the
// tracker and the last message from scratch; the status observation needs
// no reset, since replayed events are never newer than it. An incremental
// result is only applied once the tracker is warm.
func (i *Instance) ApplyHookScan(res HookScanResult) bool {
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
		i.claude.lastMessage, i.claude.lastMsgValid = "", false
	} else if !i.subagentWarm {
		return false
	}
	tracker.Apply(res.Events, res.Meta)
	for _, ev := range res.Events {
		i.claude.applyEvent(ev)
	}
	i.subagentWarm = true
	return true
}

// ForgetSubagentsWithoutHooks drops the tracked agents of a launch whose
// hooks folder has disappeared, which a scan reports as
// hooks.ErrNoHooks: the folder was deleted by hand, or by another loom
// process's sweep. Without it the last rows would stay on screen until
// the next launch. It acts only when hookLaunchID is a real launch ID:
// for an instance restored after a loom restart ("", nothing adopted yet)
// or launched without hooks (noHooksLaunchID), ErrNoHooks is the normal
// state. hookLaunchID itself is kept, so a folder written by another
// launch is still never adopted. The Claude status the hooks reported and
// the last message go with the rows; a roster-sourced status stays. Call it
// on the Update goroutine.
func (i *Instance) ForgetSubagentsWithoutHooks() {
	i.mu.Lock()
	if i.hookLaunchID == "" || i.hookLaunchID == noHooksLaunchID {
		i.mu.Unlock()
		return
	}
	wasWarm := i.subagentWarm
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.claude.folderGone(time.Now())
	i.mu.Unlock()
	if wasWarm {
		i.getLogger().Debug("subagent_hooks.folder_gone", "action", "tracked agents dropped")
	}
}

// Subagents returns the live agents to render, or nil when nothing is
// tracked, when the instance is in a status where no agent can be running
// (Paused, Recoverable, Deleting), or when subagent tracking is turned off.
func (i *Instance) Subagents() []subagent.View {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.subagents == nil || !subagentLive(i.Status) || subagentRowsHidden.Load() {
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
