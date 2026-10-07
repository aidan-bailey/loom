package core

import (
	"fmt"
	"slices"
	"strings"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
)

// RecoverySummary tallies what a reconcileOrphans pass did, for the
// non-blocking one-line summary shown to the user.
type RecoverySummary struct {
	cleaned int // stale worktrees auto-removed
	review  int // Recoverable entries added to the list
	failed  int // records that failed reconcile (storage unrecovered cache)
	// undecodable counts records this binary cannot decode (corrupt, or
	// written by a newer loom); storage preserves them verbatim.
	undecodable int
}

func (s RecoverySummary) Empty() bool {
	return s.cleaned == 0 && s.review == 0 && s.failed == 0 && s.undecodable == 0
}

func (s RecoverySummary) String() string {
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var parts []string
	if s.cleaned > 0 {
		parts = append(parts, "cleaned "+plural(s.cleaned, "stale worktree", "stale worktrees"))
	}
	if s.review > 0 {
		verb := "need"
		if s.review == 1 {
			verb = "needs"
		}
		parts = append(parts, fmt.Sprintf("%s %s review (in list)", plural(s.review, "session", "sessions"), verb))
	}
	if s.failed > 0 {
		// These records are preserved on disk and retried next launch,
		// but never appear in the list — without this line they would
		// look like silently lost sessions.
		parts = append(parts, fmt.Sprintf("%s failed to load (kept; see loom.log)", plural(s.failed, "session", "sessions")))
	}
	if s.undecodable > 0 {
		// Typically left by a newer loom after a downgrade. Saves write
		// them back untouched, so the newer binary finds them intact.
		verb := "were"
		if s.undecodable == 1 {
			verb = "was"
		}
		parts = append(parts, fmt.Sprintf("%s could not be read by this version of loom and %s preserved unchanged",
			plural(s.undecodable, "session record", "session records"), verb))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Recovery: " + strings.Join(parts, " · ")
}

// claimedWorktreePaths returns the set of worktree paths already accounted
// for: live instances plus the records storage preserves outside the live
// list (reconcile failures and undecodable records, both still tracked in
// state.json). Orphan discovery skips these.
func claimedWorktreePaths(claimed []*session.Instance, storage *session.Storage) map[string]bool {
	paths := make(map[string]bool, len(claimed))
	for _, inst := range claimed {
		wt, err := inst.GetGitWorktree()
		if err != nil || wt == nil {
			continue
		}
		if p := wt.GetWorktreePath(); p != "" {
			paths[p] = true
		}
	}
	if storage != nil {
		for p := range storage.PreservedWorktreePaths() {
			paths[p] = true
		}
	}
	return paths
}

// claimTitles adds to claimed every session title one workspace owns, for
// the title-keyed sweeps: the orphan tmux sweep (CleanupOrphanedSessions,
// which also spares sessions started outside the roots it owns) and the
// subagent hooks sweep. That is each instance in ws (Recoverable orphans
// included) plus each record ws's storage preserves on disk outside the list
// (Storage.PreservedTitles: reconcile failures and undecodable records,
// e.g. a newer loom's after a downgrade) — sparing those keeps a preserved
// record's agent alive for the binary that can load it. ws's storage may be nil.
func claimTitles(claimed map[string]bool, ws *Workspace) {
	for _, inst := range ws.insts {
		claimed[inst.Title] = true
	}
	if ws.storage == nil {
		return
	}
	for _, title := range ws.storage.PreservedTitles() {
		claimed[title] = true
	}
}

// reconcileOrphans discovers orphaned worktrees for one workspace, auto-cleans
// stale leftovers, and adds inline Recoverable entries for orphans that need a
// human decision. It mutates ws (adds Recoverable instances) and returns a
// summary for the caller to surface. Safe to run on any workspace-load path.
func (m *Model) reconcileOrphans(ws *Workspace, cfgDir, program string, cmdExec cmd2.Executor) RecoverySummary {
	var summary RecoverySummary
	orphans, err := session.DiscoverOrphans(cfgDir, claimedWorktreePaths(ws.insts, ws.storage), cmdExec)
	if err != nil {
		log.For("core").Warn("orphan_discovery_failed", "cfg_dir", cfgDir, "err", err)
		return summary
	}
	for _, cand := range orphans {
		switch cand.Disposition() {
		case session.DisposeClean:
			if err := session.RemoveOrphanWorktree(cand.RepoPath, cand.WorktreePath); err != nil {
				log.For("core").Warn("orphan_autoclean_failed", "worktree", cand.WorktreePath, "err", err)
				continue
			}
			summary.cleaned++
		case session.DisposeReview:
			data := session.InstanceDataFromOrphan(cand, program)
			data.Status = session.Recoverable
			inst, err := session.FromInstanceData(data, cfgDir)
			if err != nil {
				log.For("core").Warn("orphan_placeholder_failed", "title", cand.Title, "err", err)
				continue
			}
			ws.add(inst)
			summary.review++
		}
	}
	// Records that failed reconcile at load time live only in the storage
	// cache, and undecodable ones only on disk — surface their counts so
	// they don't read as lost sessions.
	if ws.storage != nil {
		summary.failed = len(ws.storage.UnrecoveredTitles())
		summary.undecodable = ws.storage.UndecodableCount()
	}
	// Preserved records may come back on a later load (or under a newer
	// loom); claimTitles keeps their hooks folders.
	claimed := make(map[string]bool)
	claimTitles(claimed, ws)
	session.SweepSubagentHooks(cfgDir, claimed, cmdExec)
	return summary
}

// applySessionConfig syncs the process-wide session flags (loom-context
// injection, subagent tracking) from cfg and, when cfgDir is non-empty,
// rewrites that config dir's loom-context prompt files — the per-load
// setup every Claude session launched afterwards relies on.
func applySessionConfig(cfg *config.Config, cfgDir string) {
	if cfg == nil {
		return
	}
	session.SetLoomContextEnabled(cfg.LoomContextEnabled())
	session.SetSubagentTrackingEnabled(cfg.SubagentTrackingEnabled())
	if cfgDir == "" {
		return
	}
	if err := session.WriteLoomContextFiles(cfgDir); err != nil {
		log.For("core").Warn("loom_context.write_failed", "err", err.Error())
	}
}

// LoadClassic loads the classic workspace's storage with startup
// semantics (loadWorkspace). sweepTmux adds the orphan tmux sweep;
// RestoreSaved's fallback passes false, because the workspaces that failed
// to load still have live sessions whose titles it cannot read. Formerly
// app.loadStartupStorage.
func (m *Model) LoadClassic(sweepTmux bool) error {
	cfgDir := ""
	if m.classic.ctx != nil {
		cfgDir = m.classic.ctx.ConfigDir
	}
	return m.loadWorkspace(m.classic, cfgDir, sweepTmux)
}

// loadClassicFallback runs when no workspace could be restored: loading
// was deferred to RestoreSaved, so without it the user would land in
// global mode over a never-loaded storage whose first save replaces its
// readable records with the empty list. On failure it fails closed: the
// storage's write latch refuses every save, and the error is a notice
// rather than an exit, so the user can still open a workspace from the
// picker. Formerly app.loadStartupStorageFallback.
func (m *Model) loadClassicFallback() {
	// Each failed OpenTab re-synced these process-wide flags from its own
	// workspace's config; put the startup config's values back before
	// anything below launches a session.
	if cfg := m.classic.cfg; cfg != nil {
		session.SetLoomContextEnabled(cfg.LoomContextEnabled())
		session.SetSubagentTrackingEnabled(cfg.SubagentTrackingEnabled())
	}
	if err := m.LoadClassic(false); err != nil {
		m.notifyErr(fmt.Errorf("no workspace could be restored, and loading sessions failed (nothing will be saved): %w", err))
	}
}

// loadWorkspace loads ws's storage into ws's (empty) instances with
// startup semantics: LoadAndReconcile, crash-restart, inline orphan
// recovery, then the workspace-terminal auto-create for a workspace
// context. cfgDir is the directory ws's storage lives in. sweepTmux adds
// the orphan tmux sweep, scoped to the sessions started under ws's
// repo or cfgDir's worktrees (see LoadClassic). A load error
// is returned before anything is added to ws; the storage's write
// latch is then engaged, so nothing can overwrite the unreadable payload.
// Used by startup (the classic workspace) and EnterGlobal (the global
// workspace it is about to install). Formerly app.loadSlotStorage.
func (m *Model) loadWorkspace(ws *Workspace, cfgDir string, sweepTmux bool) error {
	storage := ws.storage
	wsCtx := ws.ctx
	cmdExec := m.executor()

	// LoadAndReconcile centralizes RenameLegacySessions + per-record
	// reconcile, and on a per-record failure stashes the raw data in
	// storage.unrecovered so the next SaveInstances preserves it.
	// The inline loop this replaced silently dropped failures.
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		return err
	}

	hasWorkspaceTerminal := false
	for _, instance := range instances {
		if instance.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		ws.add(instance)
	}

	// Restart crash-recovered instances
	for _, inst := range ws.insts {
		if !inst.CrashRecovered() {
			continue
		}
		if err := inst.CrashRestart(); err != nil {
			log.For("core").Error("crash_recovery.restart_failed", "title", inst.Title, "err", err)
			if tErr := inst.TransitionTo(session.Paused); tErr != nil {
				log.For("core").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr.Error())
			}
		}
		inst.SetCrashRecovered(false)
	}

	// Discover orphan worktrees (on disk but not in state.json),
	// auto-clean stale leftovers, and add inline Recoverable entries
	// for any with unsaved work or a live agent. Runs before
	// CleanupOrphanedSessions so a live recoverable's tmux (now a
	// list instance) is exempted by the claimedTitles loop below.
	// Placeholders get the program of the config this slot loaded, as
	// OpenTab's do — not m.program, the process's startup program,
	// which for EnterGlobal's workspace may be another workspace's.
	program := m.program
	if ws.cfg != nil {
		program = ws.cfg.GetProgram()
	}
	ws.recovery = m.reconcileOrphans(ws, cfgDir, program, cmdExec)

	// Clean up orphaned tmux sessions from previous crashes, sparing
	// those of records preserved on disk outside the list. Only sessions
	// started under this slot's repo or worktrees dir are candidates: the
	// server is shared, and another running loom's sessions are unclaimed
	// here too.
	if sweepTmux {
		claimedTitles := make(map[string]bool)
		claimTitles(claimedTitles, ws)
		owned := &config.WorkspaceContext{ConfigDir: cfgDir}
		if wsCtx != nil {
			owned.RepoPath = wsCtx.RepoPath
		}
		scope := session.NewSweepScope([]*config.WorkspaceContext{owned}, m.registry)
		if _, err := session.CleanupOrphanedSessions(claimedTitles, scope, cmdExec); err != nil {
			log.For("core").Error("orphan_cleanup_failed", "err", err)
		}
	}

	// Auto-create workspace terminal if in a workspace context and none
	// exists — unless a record storage preserves but could not load
	// already owns the title (see OpenTab). The global context
	// (startup's, or EnterGlobal's workspace) has no repo path, so global
	// mode never gets one.
	wtTitle := "Workspace Terminal"
	if wsCtx != nil && wsCtx.Name != "" {
		wtTitle = wsCtx.Name
	}
	if !hasWorkspaceTerminal && wsCtx != nil && wsCtx.RepoPath != "" && !slices.Contains(storage.PreservedTitles(), wtTitle) {
		wtOpts := launch.FromConfig(ws.cfg)
		if launch.RemoteControlBlocked(m.rcAuth, launch.EffectiveRemoteControl(wtOpts), m.program) {
			// Non-interactive startup: fall back silently but leave an
			// info-style note (clears on the next status update).
			m.notifyInfo("remote control off: " + m.rcAuth.Reason)
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             launch.Compose(wtOpts, m.rcAuth, m.program, wtTitle),
			HeadroomProxy:       wtOpts.HeadroomProxy,
			CacheTTL1h:          wtOpts.CacheTTL1h,
			IsWorkspaceTerminal: true,
			ConfigDir:           cfgDir,
		})
		if wtErr != nil {
			log.For("core").Error("workspace_terminal.create_failed", "err", wtErr)
		} else {
			ws.add(wtInstance)
			if err := wtInstance.Start(true); err != nil {
				log.For("core").Error("workspace_terminal.start_failed", "err", err)
			}
		}
	}
	return nil
}
