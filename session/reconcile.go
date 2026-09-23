package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// reconcileTmuxTimeout bounds one has-session probe attempt. A var, not a
// const, so tests can shorten it.
var reconcileTmuxTimeout = 5 * time.Second

// RecoveryAction describes what to do with an instance during startup reconciliation.
type RecoveryAction int

const (
	// ActionNoChange means the instance is already in a consistent state (e.g. Paused).
	ActionNoChange RecoveryAction = iota
	// ActionRestore means tmux + worktree are healthy; do a normal restore.
	ActionRestore
	// ActionRestart means tmux is dead but worktree exists; restart with scrollback + agent context.
	ActionRestart
	// ActionMarkPaused means both tmux and worktree are gone; mark as Paused (branch is preserved).
	ActionMarkPaused
	// ActionKillAndPause means tmux is alive but worktree is gone; kill tmux, mark Paused.
	ActionKillAndPause
	// ActionRestartWsTerminal means workspace terminal's tmux is dead; recreate it.
	ActionRestartWsTerminal
)

// CheckTmuxAlive checks if a tmux session exists by its sanitized name.
// A probe killed at its deadline is not evidence of death: under load
// `tmux has-session` can simply take too long while the session is
// perfectly healthy. Reading that as "gone" maps to ActionRestart, which
// then tries to create a session that already exists ("duplicate
// session"). So retry once, and if tmux still has not answered, assume
// alive — a wrong "alive" costs a failed restore, while a wrong "dead"
// tears down a running agent's session.
func CheckTmuxAlive(sessionTitle string, cmdExec internalexec.Executor) bool {
	sanitized := tmux.ToLoomTmuxName(sessionTitle)
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
		existsCmd := tmux.Command(ctx, "has-session", "-t="+sanitized)
		err := cmdExec.Run(existsCmd)
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()
		if err == nil {
			return true
		}
		if !timedOut {
			return false // tmux answered: the session really is gone
		}
	}
	log.For("session").Warn("reconcile.tmux_probe_inconclusive", "title", sessionTitle)
	return true
}

// KillTmuxSessionByTitle kills the tmux session matching title's sanitized
// name. Best-effort: returns the command error if any (commonly a benign
// "session not found"), which most callers can ignore. Used to clear
// orphan sessions left over from a prior crash before reusing the title.
func KillTmuxSessionByTitle(title string, cmdExec internalexec.Executor) error {
	sanitized := tmux.ToLoomTmuxName(title)
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
	defer cancel()
	killCmd := tmux.Command(ctx, "kill-session", "-t="+sanitized)
	return cmdExec.Run(killCmd)
}

// CheckWorktreeExists checks if the worktree directory exists on disk.
func CheckWorktreeExists(worktreePath string) bool {
	if worktreePath == "" {
		return false
	}
	_, err := os.Stat(worktreePath)
	return err == nil
}

// DetermineRecoveryAction decides what to do with a loaded instance based on
// its persisted status and current filesystem/tmux state.
func DetermineRecoveryAction(status Status, tmuxAlive, worktreeExists, isWorkspaceTerminal bool) RecoveryAction {
	if status == Paused {
		return ActionNoChange
	}

	if isWorkspaceTerminal {
		if tmuxAlive {
			return ActionRestore
		}
		return ActionRestartWsTerminal
	}

	// Loading means setup was interrupted mid-flight: the worktree may be
	// partially created and the base commit may never have been recorded.
	// We must NOT Restore (attaching would treat a half-built session as
	// healthy and emit bogus diffs against an empty base) or Restart with
	// --continue (the agent never started a conversation). Kill any live
	// session and mark it paused, leaving the branch and worktree on disk
	// for Resume, which decides from the tree itself (decideResume): an
	// intact one is relaunched in place (recording a missing base commit),
	// an absent or gutted one rebuilt, and one git cannot vouch for — such
	// as a `worktree add` still locked "initializing" — refused.
	if status == Loading {
		if tmuxAlive {
			return ActionKillAndPause
		}
		return ActionMarkPaused
	}

	switch {
	case tmuxAlive && worktreeExists:
		return ActionRestore
	case tmuxAlive && !worktreeExists:
		return ActionKillAndPause
	case !tmuxAlive && worktreeExists:
		return ActionRestart
	default: // !tmuxAlive && !worktreeExists
		return ActionMarkPaused
	}
}

// ReconcileAndRestore loads an instance from serialized data, checks the health
// of its tmux session and worktree, and takes the appropriate recovery action.
func ReconcileAndRestore(data InstanceData, configDir string, cmdExec internalexec.Executor) (*Instance, error) {
	tmuxAlive := CheckTmuxAlive(data.Title, cmdExec)
	wtExists := CheckWorktreeExists(data.Worktree.WorktreePath)
	action := DetermineRecoveryAction(data.Status, tmuxAlive, wtExists, data.IsWorkspaceTerminal)
	logRecoveryAction(data.Title, action)

	switch action {
	case ActionNoChange:
		return fromInstanceDataPaused(data, configDir)

	case ActionRestore:
		instance, err := FromInstanceData(data, configDir)
		if err != nil {
			return nil, err
		}
		if err := instance.EnsureRunning(); err != nil {
			// On a genuine Start-level failure (e.g. PTY exhaustion, bad
			// data) we deliberately return the error rather than masking
			// it with a crash-restart: LoadAndReconcile stashes the raw
			// record in the unrecovered cache (storage.go) so it survives
			// in state.json and is retried on the next launch. Note that a
			// session that merely died after the liveness probe does NOT
			// land here — tmux.Restore's pty.Start returns once the attach
			// process spawns, before tmux validates the session — so this
			// path fires only on real local failures, which the cache is
			// the right home for.
			return nil, err
		}
		return instance, nil

	case ActionRestart:
		instance, err := fromInstanceDataPaused(data, configDir)
		if err != nil {
			return nil, err
		}
		instance.crashRecovered = true // unpublished: no lock needed
		return instance, nil

	case ActionMarkPaused:
		data.Status = Paused
		return fromInstanceDataPaused(data, configDir)

	case ActionKillAndPause:
		// Best-effort: the tmux session may already be gone, or the
		// worktree-gone state may be stale. Log at Debug since this fires
		// often during normal reconciliation.
		if err := KillTmuxSessionByTitle(data.Title, cmdExec); err != nil {
			log.For("reconcile").Debug("tmux_kill_failed", "title", data.Title, "err", err.Error())
		}
		data.Status = Paused
		return fromInstanceDataPaused(data, configDir)

	case ActionRestartWsTerminal:
		instance, err := fromInstanceDataPaused(data, configDir)
		if err != nil {
			return nil, err
		}
		instance.crashRecovered = true // unpublished: no lock needed
		return instance, nil

	default:
		return nil, fmt.Errorf("unknown recovery action: %d", action)
	}
}

// fromInstanceDataPaused creates an Instance from serialized data in a
// detached (no live PTY) state: it sets started=true and creates a
// TmuxSession object but does not connect. Despite the name it backs
// several non-paused actions too (ActionRestart, ActionRestartWsTerminal)
// — in those cases the caller sets crashRecovered=true and a later
// CrashRestart spawns the real session.
func fromInstanceDataPaused(data InstanceData, configDir string) (*Instance, error) {
	// Delegate to the canonical rehydrator — this used to be a hand-rolled
	// near-copy that had already drifted (it dropped HeadroomProxy,
	// CacheTTL1h, the per-instance logger, and the tmux session env).
	instance, err := FromInstanceData(data, configDir)
	if err != nil {
		return nil, err
	}

	// Unlike FromInstanceData, which only wires these for Paused and
	// Recoverable records, every caller of this variant needs the started
	// flag and a detached TmuxSession regardless of persisted status —
	// ActionRestart/ActionRestartWsTerminal rehydrate Running records.
	instance.setStarted(true)
	if instance.getTmuxSession() == nil {
		// Unpublished, like FromInstanceData: the launch fields are read directly.
		instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.program, InstanceEnv(instance.program, instance.headroomProxy, instance.cacheTTL1h)...))
	}
	return instance, nil
}

// sweepListFormat is what the orphan sweep asks tmux for: each session's
// name and working directory. session_path is the directory new-session
// was started in (-c, which loom always passes); unlike pane_current_path
// it does not follow a cd in the pane, so a terminal-pane shell that cd'd
// into another workspace still reads as the session's own, and it is
// still reported after the directory is deleted.
const sweepListFormat = "#{session_name}\t#{session_path}"

// SweepScope bounds the orphan tmux sweep (CleanupOrphanedSessions) to
// sessions this process can prove it owns. The tmux server is shared by
// every loom process on it, so a loom_* session missing from this
// process's lists is not evidence of an orphan: it may be the live agent
// of another running loom, in a workspace this process never loaded.
// Every loom session starts in a directory loom chose: its worktree, or
// the repo root for a workspace terminal (and that terminal's pane). The
// sweep kills an unclaimed session only when the most specific root
// holding that directory is Owned.
type SweepScope struct {
	// Owned holds the roots (WorkspaceSweepRoots) of every workspace
	// whose sessions the sweep covers.
	Owned []string
	// Foreign holds the roots of every other known workspace. They
	// matter only nested inside an owned root (a submodule, or a linked
	// worktree registered as its own workspace): a session there belongs
	// to the inner workspace, and its own loom sweeps it.
	Foreign []string
}

// WorkspaceSweepRoots returns the directories a workspace's sessions start
// in: repoPath (its workspace terminal; the global context has none, so it
// passes "") and configDir's worktrees directory (every other session). An
// empty configDir means the default config dir, as for worktree creation.
func WorkspaceSweepRoots(repoPath, configDir string) []string {
	var roots []string
	if repoPath != "" {
		roots = append(roots, repoPath)
	}
	if configDir == "" {
		dir, err := config.GetConfigDir()
		if err != nil {
			log.For("reconcile").Warn("orphan_tmux.config_dir_unresolved", "err", err)
			return roots
		}
		configDir = dir
	}
	return append(roots, filepath.Join(configDir, "worktrees"))
}

// NewSweepScope returns the scope of a sweep covering the workspaces in
// owned (a nil entry owns nothing; an empty ConfigDir means the default
// config dir). Every workspace in registry (nil allowed) and the global
// config dir's worktrees are foreign; the ones owned also lists stay owned.
func NewSweepScope(owned []*config.WorkspaceContext, registry *config.WorkspaceRegistry) SweepScope {
	var s SweepScope
	for _, c := range owned {
		if c == nil {
			continue
		}
		s.Owned = append(s.Owned, WorkspaceSweepRoots(c.RepoPath, c.ConfigDir)...)
	}
	if registry != nil {
		for i := range registry.Workspaces {
			ws := &registry.Workspaces[i]
			if ws.Path == "" {
				continue
			}
			s.Foreign = append(s.Foreign, WorkspaceSweepRoots(ws.Path, config.WorkspaceConfigDir(ws))...)
		}
	}
	if dir, err := config.GetGlobalConfigDir(); err == nil {
		s.Foreign = append(s.Foreign, filepath.Join(dir, "worktrees"))
	}
	return s
}

// resolvedScope is a SweepScope with every root canonicalized.
type resolvedScope struct{ owned, foreign []string }

func (s SweepScope) resolve() resolvedScope {
	return resolvedScope{owned: canonicalRoots(s.Owned), foreign: canonicalRoots(s.Foreign)}
}

// canonicalRoots canonicalizes roots, dropping any that is empty, relative
// or a filesystem root (which would own every session on the machine).
func canonicalRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		c := canonicalPath(r)
		if c == "" || filepath.Dir(c) == c {
			continue
		}
		out = append(out, c)
	}
	return out
}

// owns reports whether dir lies inside an owned root with no foreign root
// nested deeper that also holds it. An empty or relative dir is owned by
// nothing.
func (r resolvedScope) owns(dir string) bool {
	d := canonicalPath(dir)
	if d == "" {
		return false
	}
	best, owned := -1, false
	for _, root := range r.owned {
		if pathWithin(d, root) && len(root) > best {
			best, owned = len(root), true
		}
	}
	for _, root := range r.foreign {
		// Strictly deeper only: a foreign root equal to an owned one is
		// the same workspace, which the registry lists too.
		if pathWithin(d, root) && len(root) > best {
			best, owned = len(root), false
		}
	}
	return owned
}

// canonicalPath resolves p's symlinks, so two spellings of one directory
// compare equal. A path that no longer exists (a deleted worktree)
// resolves through its nearest existing ancestor. Returns "" for an empty
// or relative path.
func canonicalPath(p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// pathWithin reports whether p is root or lies beneath it. Both must be
// canonical, and root must not be a filesystem root: a component-wise
// check, so /a/repo does not hold /a/repo2.
func pathWithin(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// CleanupOrphanedSessions kills the loom tmux sessions (either prefix, so
// upgrades from claude-squad don't leave zombie panes behind) that no
// title in claimedTitles owns and whose working directory scope owns.
// Unclaimed is not enough: the tmux server is shared, and the incident
// this rule answers was a second loom's startup sweep killing all 20
// sessions on the server, the first loom's live agents included. It fails
// closed: a session whose directory is empty, unreadable or outside every
// owned root is left alone. Kills are by exact name (-t=).
func CleanupOrphanedSessions(claimedTitles map[string]bool, scope SweepScope, cmdExec internalexec.Executor) error {
	listCtx, listCancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
	defer listCancel()
	// "ls" is list-sessions; app tests recognize the sweep by it.
	output, err := cmdExec.Output(tmux.Command(listCtx, "ls", "-F", sweepListFormat))
	if err != nil {
		// No tmux server running — nothing to clean up
		return nil
	}

	roots := scope.resolve()
	for _, line := range strings.Split(string(output), "\n") {
		sessionName, dir, _ := strings.Cut(line, "\t")
		if !(strings.HasPrefix(sessionName, tmux.TmuxPrefix) || strings.HasPrefix(sessionName, tmux.LegacyTmuxPrefix)) {
			continue
		}
		if sessionClaimed(sessionName, claimedTitles) {
			continue
		}
		if !roots.owns(dir) {
			log.For("reconcile").Debug("orphan_tmux.skip_unowned", "session", sessionName, "dir", dir)
			continue
		}
		log.For("reconcile").Info("orphan_tmux.kill_begin", "session", sessionName, "dir", dir)
		killCtx, killCancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
		if err := cmdExec.Run(tmux.Command(killCtx, "kill-session", "-t="+sessionName)); err != nil {
			log.For("reconcile").Error("orphan_tmux.kill_failed", "session", sessionName, "err", err)
		}
		killCancel()
	}
	return nil
}

// sessionClaimed reports whether a claimed title owns the tmux session
// sessionName — either its agent session (either prefix) or its separate
// terminal-pane session (loom_term_<title>, see tmux.TerminalSessionName).
// Every instance has both; missing the terminal-pane form would make every
// instance's terminal pane look orphaned on every sweep.
func sessionClaimed(sessionName string, claimedTitles map[string]bool) bool {
	for title := range claimedTitles {
		if tmux.ToLoomTmuxName(title) == sessionName ||
			tmux.ToLegacyTmuxName(title) == sessionName ||
			tmux.ToLoomTmuxName(tmux.TerminalSessionName(title)) == sessionName {
			return true
		}
	}
	return false
}

// logRecoveryAction logs the recovery action taken for an instance.
func logRecoveryAction(title string, action RecoveryAction) {
	switch action {
	case ActionRestore:
		log.For("reconcile").Info("recovery", "title", title, "action", "restore", "reason", "tmux+worktree healthy")
	case ActionRestart:
		log.For("reconcile").Info("recovery", "title", title, "action", "restart", "reason", "tmux dead but worktree exists")
	case ActionMarkPaused:
		log.For("reconcile").Info("recovery", "title", title, "action", "mark_paused", "reason", "tmux+worktree gone")
	case ActionKillAndPause:
		log.For("reconcile").Info("recovery", "title", title, "action", "kill_and_pause", "reason", "tmux alive but worktree gone")
	case ActionRestartWsTerminal:
		log.For("reconcile").Info("recovery", "title", title, "action", "restart_ws_terminal", "reason", "workspace terminal tmux dead")
	case ActionNoChange:
		log.For("reconcile").Debug("recovery", "title", title, "action", "no_change", "reason", "already consistent (paused)")
	default:
		log.For("reconcile").Warn("recovery", "title", title, "action", "unknown", "value", int(action))
	}
}
