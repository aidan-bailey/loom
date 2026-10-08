package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/launch"
)

// RecoverySummary tallies what a reconcileOrphans pass did, for the
// non-blocking one-line summary shown to the user.
type RecoverySummary struct {
	Cleaned int // stale worktrees auto-removed
	Review  int // Recoverable entries added to the list
	Failed  int // records that failed reconcile (storage unrecovered cache)
	// Undecodable counts records this binary cannot decode (corrupt, or
	// written by a newer loom); storage preserves them verbatim.
	Undecodable int
}

func (s RecoverySummary) Empty() bool {
	return s.Cleaned == 0 && s.Review == 0 && s.Failed == 0 && s.Undecodable == 0
}

func (s RecoverySummary) String() string {
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var parts []string
	if s.Cleaned > 0 {
		parts = append(parts, "cleaned "+plural(s.Cleaned, "stale worktree", "stale worktrees"))
	}
	if s.Review > 0 {
		verb := "need"
		if s.Review == 1 {
			verb = "needs"
		}
		parts = append(parts, fmt.Sprintf("%s %s review (in list)", plural(s.Review, "session", "sessions"), verb))
	}
	if s.Failed > 0 {
		// These records are preserved on disk and retried next launch,
		// but never appear in the list — without this line they would
		// look like silently lost sessions.
		parts = append(parts, fmt.Sprintf("%s failed to load (kept; see loom.log)", plural(s.Failed, "session", "sessions")))
	}
	if s.Undecodable > 0 {
		// Typically left by a newer loom after a downgrade. Saves write
		// them back untouched, so the newer binary finds them intact.
		verb := "were"
		if s.Undecodable == 1 {
			verb = "was"
		}
		parts = append(parts, fmt.Sprintf("%s could not be read by this version of loom and %s preserved unchanged",
			plural(s.Undecodable, "session record", "session records"), verb))
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
				// A lock loom respects is the user's call and lasts across
				// starts: a debug line, not a warning at every load.
				if errors.Is(err, git.ErrWorktreeLocked) {
					log.For("core").Debug("orphan_autoclean_locked", "worktree", cand.WorktreePath, "err", err)
				} else {
					log.For("core").Warn("orphan_autoclean_failed", "worktree", cand.WorktreePath, "err", err)
				}
				continue
			}
			summary.Cleaned++
		case session.DisposeReview:
			data := session.InstanceDataFromOrphan(cand, program)
			data.Status = session.Recoverable
			inst, err := session.FromInstanceData(data, cfgDir)
			if err != nil {
				log.For("core").Warn("orphan_placeholder_failed", "title", cand.Title, "err", err)
				continue
			}
			ws.add(inst)
			summary.Review++
		}
	}
	// Records that failed reconcile at load time live only in the storage
	// cache, and undecodable ones only on disk — surface their counts so
	// they don't read as lost sessions.
	if ws.storage != nil {
		summary.Failed = len(ws.storage.UnrecoveredTitles())
		summary.Undecodable = ws.storage.UndecodableCount()
	}
	// Claude's temp dirs of sessions that are gone, the worktrees just
	// auto-cleaned included: archived off the model's goroutine once the
	// next health tick dispatches the sweep.
	m.requestClaudeTmpSweep(ws, cfgDir)
	// Preserved records may come back on a later load (or under a newer
	// loom); claimTitles keeps their hooks folders.
	claimed := make(map[string]bool)
	claimTitles(claimed, ws)
	session.SweepSubagentHooks(cfgDir, claimed, cmdExec)
	return summary
}

// applySessionConfig sets cfgDir's session flags from cfg
// (syncSessionFlags) and, when cfgDir is non-empty, rewrites that config
// dir's loom-context prompt files — the per-load setup every Claude
// session launched afterwards relies on.
func applySessionConfig(cfg *config.Config, cfgDir string) {
	if cfg == nil {
		return
	}
	syncSessionFlags(cfg, cfgDir)
	if cfgDir == "" {
		return
	}
	if err := session.WriteLoomContextFiles(cfgDir); err != nil {
		log.For("core").Warn("loom_context.write_failed", "err", err.Error())
	}
}

// syncSessionFlags sets the session flags (loom-context injection,
// subagent tracking) of the sessions whose config dir is cfgDir from cfg.
// They are kept per config dir: every loaded workspace launches with its
// own config's.
func syncSessionFlags(cfg *config.Config, cfgDir string) {
	session.SetLoomContextEnabled(cfgDir, cfg.LoomContextEnabled())
	session.SetSubagentTrackingEnabled(cfgDir, cfg.SubagentTrackingEnabled())
}

// Boot readies the model to serve, before its loop starts (the model is
// single-goroutine until then). In order, it loads the account registry
// (InitAccounts); detects the default account's remote-control auth, when
// the global config enables remote control or an extra account is
// registered (the identity it reads also locates the main config dir the
// accounts link to); and loads every workspace the model serves (boot). The
// detection runs once, up front, so every launch decision after it is
// synchronous. Boot returns the events it raised (the account registry's
// notices): no client is connected yet to be sent them, so the caller
// shows them to its own. A client never boots the model: Boot is not in
// Core.
func (m *Model) Boot() []Event {
	m.InitAccounts()
	if remoteControlConfigured() || m.HasExtraAccounts() {
		m.SetRCAuth(session.DetectClaudeRemoteControlAuth(m.program, m.executor()))
	}
	m.boot()
	events := m.out.Events
	m.out.Events = nil
	return events
}

// remoteControlConfigured reports whether the global config enables remote
// control; false when the global config dir can't be resolved.
func remoteControlConfigured() bool {
	ctx, err := config.GlobalWorkspaceContext()
	if err != nil {
		return false
	}
	return config.LoadConfigFrom(ctx.ConfigDir).RemoteControlEnabled()
}

// boot loads, once, every workspace the model serves (daemon stage 3A): the
// global one and each registered one, one per config dir. Then one orphan
// tmux sweep covers them all (sweepOrphans). A workspace that fails to load
// is kept, latched (loadWS). A workspace registered later is loaded when it
// is registered (Register) or reread (ReloadRegistry).
func (m *Model) boot() {
	if m.booted {
		return
	}
	m.booted = true
	if _, err := m.globalWS(); err != nil {
		log.For("core").Error("workspace.global_load_failed", "err", err)
	}
	if m.registry != nil {
		for _, def := range m.registry.Workspaces {
			_, _ = m.ensureLoaded(def)
		}
	}
	m.sweepOrphans()
}

// newWS builds ctx's workspace over its config dir's state, config and
// storage, not loaded yet (loadWS).
func newWS(ctx *config.WorkspaceContext) (*Workspace, error) {
	state := config.LoadStateFrom(ctx.ConfigDir)
	cfg := config.LoadConfigFrom(ctx.ConfigDir)
	storage, err := session.NewStorage(state, ctx.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage for workspace %s: %w", ctx.Name, err)
	}
	return NewWorkspace(WorkspaceParts{Ctx: ctx, Storage: storage, Config: cfg, State: state}), nil
}

// wsByConfigDir is the served workspace whose config dir is dir, nil when
// none is. A workspace is one config dir: the registry may name it
// differently (a rename), the startup context may be one of its entries,
// or two entries may reach it through a symlink (the registry dedups by the
// path as spelled), so dirs compare canonically. Serving one directory
// twice would let a stale copy's quit save overwrite the other's changes.
func (m *Model) wsByConfigDir(dir string) *Workspace {
	dir = canonicalDir(dir)
	for _, ws := range m.workspaces {
		if canonicalDir(ws.configDir()) == dir {
			return ws
		}
	}
	return nil
}

// ensureLoaded is the served workspace of def, loading it first when the
// model does not serve it yet. A load that fails keeps the workspace,
// latched, and returns it with the error; nil only when its storage could
// not even be built.
func (m *Model) ensureLoaded(def config.Workspace) (*Workspace, error) {
	ctx := config.WorkspaceContextFor(&def)
	if ws := m.wsByConfigDir(ctx.ConfigDir); ws != nil {
		return ws, ws.loadErr
	}
	ws, err := newWS(ctx)
	if err != nil {
		log.For("core").Error("workspace.create_failed", "name", def.Name, "err", err)
		return nil, err
	}
	m.workspaces = append(m.workspaces, ws)
	return ws, m.loadWS(ws)
}

// globalWS is the global workspace (config.GlobalWorkspaceContext), loading
// it when the model does not serve it yet.
func (m *Model) globalWS() (*Workspace, error) {
	ctx, err := config.GlobalWorkspaceContext()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the global config dir: %w", err)
	}
	if ws := m.wsByConfigDir(ctx.ConfigDir); ws != nil {
		return ws, ws.loadErr
	}
	ws, err := newWS(ctx)
	if err != nil {
		return nil, err
	}
	m.workspaces = append(m.workspaces, ws)
	return ws, m.loadWS(ws)
}

// loadWS loads ws's storage into its (empty) instance list:
// LoadAndReconcile, the crash-restart of every session the reconcile found
// dead with its worktree intact, and inline orphan recovery
// (reconcileOrphans, which also queues the Claude temp-dir sweep and
// sweeps the hooks folders). The workspace terminal's crash-restart waits
// for the workspace's first open (ensureTerminal). A load error leaves ws
// empty, its storage's write latch engaged so nothing can overwrite the
// unreadable payload, and is kept in ws.loadErr for an open to retry
// (retryLoad). Formerly loadWorkspace.
func (m *Model) loadWS(ws *Workspace) error {
	if ws.storage == nil {
		return nil
	}
	cfgDir := ws.configDir()
	// Loom-context injection: keep the config dir's prompt files current
	// and its session flags in step with its config, before any Claude
	// session (crash-restart, workspace terminal, resume) launches.
	applySessionConfig(ws.cfg, cfgDir)
	cmdExec := m.executor()
	// LoadAndReconcile centralizes RenameLegacySessions + per-record
	// reconcile, and on a per-record failure stashes the raw data in
	// storage.unrecovered so the next SaveInstances preserves it.
	instances, err := ws.storage.LoadAndReconcile(cmdExec)
	if err != nil {
		// Fail closed: nothing is added, so no save of this workspace can
		// overwrite a possibly-recoverable (transiently unreadable or
		// corrupt) state.json with an empty list.
		ws.loadErr = fmt.Errorf("load instances for workspace %s: %w", ws.Label(), err)
		log.For("core").Error("workspace.load_failed", "workspace", ws.Label(), "err", err)
		return ws.loadErr
	}
	ws.loadErr = nil
	for _, inst := range instances {
		ws.add(inst)
	}
	for _, inst := range ws.insts {
		if inst.CrashRecovered() && (!inst.IsWorkspaceTerminal || ws.opened) {
			crashRestart(inst)
		}
	}
	// Discover orphan worktrees (on disk but not in state.json), auto-clean
	// stale leftovers, and add inline Recoverable entries for any with
	// unsaved work or a live agent. Placeholders get the program of the
	// workspace's own config.
	program := m.program
	if ws.cfg != nil {
		program = ws.cfg.GetProgram()
	}
	ws.recovery = m.reconcileOrphans(ws, cfgDir, program, cmdExec)
	return nil
}

// crashRestart relaunches a session the reconcile found dead with its
// worktree intact; one that will not relaunch is marked Paused.
func crashRestart(inst *session.Instance) {
	if err := inst.CrashRestart(); err != nil {
		log.For("core").Error("crash_recovery.restart_failed", "title", inst.Title, "err", err)
		if tErr := inst.TransitionTo(session.Paused); tErr != nil {
			log.For("core").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr.Error())
		}
	}
	inst.SetCrashRecovered(false)
}

// retryLoad loads again a workspace whose load failed (loadWS): a client
// opened it, so the cause may have been fixed. Its state, config and
// storage are read from disk afresh, since a storage keeps the payload it
// was built over; a failed workspace holds no instance they could strand.
func (m *Model) retryLoad(ws *Workspace) error {
	if ws.loadErr == nil {
		return nil
	}
	if ws.ctx != nil {
		fresh, err := newWS(ws.ctx)
		if err != nil {
			return err
		}
		ws.storage, ws.state, ws.cfg = fresh.storage, fresh.state, fresh.cfg
	}
	return m.loadWS(ws)
}

// open is a client showing ws. A workspace whose load failed is loaded
// again first (retryLoad), and its error returned when it still fails. On
// its first open since the model started, its workspace terminal starts
// (ensureTerminal), the health tick probes it, and the GitHub poll covers
// its repository from the next tick on.
func (m *Model) open(ws *Workspace) error {
	if err := m.retryLoad(ws); err != nil {
		return err
	}
	if ws.opened {
		return nil
	}
	ws.opened = true
	m.ensureTerminal(ws)
	m.ExpediteGitHub()
	return nil
}

// ensureTerminal gives an opened workspace with a repository its workspace
// terminal: it relaunches one that died while nobody had the workspace
// open (CrashRestart) or that is Paused (its restart breaker stopped it),
// unless a session it can't prove its own holds the terminal's tmux name
// (session.HeldElsewhere; settleTerminal). It creates one when there is none, unless a
// record storage preserves but could not load already owns the title
// (after a downgrade every record is undecodable, the terminal included:
// the terminal exists, just not in this binary's list, and a second
// same-titled record would clobber it).
func (m *Model) ensureTerminal(ws *Workspace) {
	if ws.ctx == nil || ws.ctx.RepoPath == "" || ws.loadErr != nil {
		return
	}
	if t := ws.terminal(); t != nil {
		if t.CrashRecovered() || t.Paused() {
			held, err := session.HeldElsewhere(t.Title, t.SessionHome(), m.executor())
			m.settleTerminal(ws, t, held, err)
		}
		return
	}
	title := ws.terminalTitle()
	if slices.Contains(ws.storage.PreservedTitles(), title) {
		return
	}
	cmdExec := m.executor()
	// A prior non-clean exit may have left a tmux session named
	// loom_<title> alive without persisting the instance; the Start below
	// would fail with "session already exists" against it. Kill it first,
	// but only if it is this workspace's, by the sweep's own ownership test:
	// the tmux server is shared, and another loom's session (a workspace
	// elsewhere with the same name) can carry this title. Anything else is
	// left running, and Start then fails on the name.
	scope := session.NewSweepScope([]*config.WorkspaceContext{ws.ctx}, m.registry)
	if killed, err := session.KillOwnedTmuxSession(title, scope, cmdExec); err != nil {
		log.For("core").Warn("workspace_terminal.orphan_kill_skipped", "workspace", ws.Label(), "err", err.Error())
	} else if killed {
		log.For("core").Info("workspace_terminal.orphan_killed", "workspace", ws.Label(), "title", title)
	}
	program := ws.cfg.GetProgram()
	wtOpts := launch.FromConfig(ws.cfg)
	if launch.RemoteControlBlocked(m.rcAuth, launch.EffectiveRemoteControl(wtOpts), program) {
		m.notifyInfo("remote control off: " + m.rcAuth.Reason)
	}
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:               title,
		Path:                ws.ctx.RepoPath,
		Program:             launch.Compose(wtOpts, m.rcAuth, program, title),
		HeadroomProxy:       wtOpts.HeadroomProxy,
		CacheTTL1h:          wtOpts.CacheTTL1h,
		IsWorkspaceTerminal: true,
		ConfigDir:           ws.ctx.ConfigDir,
	})
	if err != nil {
		log.For("core").Error("workspace_terminal.create_failed", "workspace", ws.Label(), "err", err)
		return
	}
	ws.add(inst)
	if err := inst.Start(true); err != nil {
		log.For("core").Error("workspace_terminal.start_failed", "workspace", ws.Label(), "err", err)
	}
	m.saveTerminal(ws)
}

// settleTerminal relaunches ws's terminal t, dead (CrashRecovered) or
// Paused, once session.HeldElsewhere has answered held (or err) about its
// tmux name. Both relaunches act on whatever holds that name: Restart kills
// it first, and CrashRestart's start can adopt it when its probe times
// out. The name is unique per tmux server, not per workspace, so another
// workspace's session (a global agent titled after this workspace, another
// loom's terminal) can hold it; one the record can't prove its own is left
// running, and the terminal stays Paused.
//
// A check tmux left unanswered (err: a listing that timed out under load)
// is no evidence either way, so nothing changes or is saved: Paused for
// good would leave the terminal stuck until loom restarts, since only a
// first open relaunches one. The workspace stays unsettled, and the health
// tick asks again (maybeSettleTerminals) until tmux answers.
func (m *Model) settleTerminal(ws *Workspace, t *session.Instance, held bool, err error) {
	ws.terminalUnsettled = err != nil
	switch {
	case err != nil:
		log.For("core").Warn("workspace_terminal.name_check_unanswered", "workspace", ws.Label(), "title", t.Title, "err", err)
	case held:
		log.For("core").Warn("workspace_terminal.name_held_elsewhere", "workspace", ws.Label(), "title", t.Title)
		if t.CrashRecovered() {
			t.SetCrashRecovered(false)
			if err := t.TransitionTo(session.Paused); err != nil {
				log.For("core").Warn("workspace_terminal.transition_failed", "title", t.Title, "err", err.Error())
			}
			m.saveTerminal(ws)
		}
	case t.CrashRecovered():
		crashRestart(t)
		m.saveTerminal(ws)
	default:
		// Its breaker gave up (applyLiveness), or reconcile found its name
		// held: nothing can resume a workspace terminal, so a new open of
		// the workspace is the user asking for it again.
		t.ResetRestartFailures()
		if err := t.Restart(); err != nil {
			log.For("core").Error("workspace_terminal.restart_failed", "title", t.Title, "err", err)
			return
		}
		m.emit(SessionLaunched{ID: m.idOf(t)})
		m.saveTerminal(ws)
	}
}

// terminalChecked is the answer of a name check maybeSettleTerminals
// queued for ws's terminal inst.
type terminalChecked struct {
	ws   *Workspace
	inst *session.Instance
	held bool
	err  error
}

// maybeSettleTerminals asks again, off the model's goroutine, who holds the
// tmux name of each terminal settleTerminal left unsettled, one check per
// workspace in flight; the answer settles it (deliverTerminalChecked). The
// health tick calls it.
func (m *Model) maybeSettleTerminals() {
	cmdExec := m.executor()
	for _, ws := range m.workspaces {
		if !ws.terminalUnsettled || ws.terminalChecking {
			continue
		}
		t := ws.terminal()
		if t == nil {
			ws.terminalUnsettled = false // killed meanwhile
			continue
		}
		ws.terminalChecking = true
		title, home := t.Title, t.SessionHome()
		m.spawnBackground(func() any {
			held, err := session.HeldElsewhere(title, home, cmdExec)
			return terminalChecked{ws: ws, inst: t, held: held, err: err}
		})
	}
}

// deliverTerminalChecked settles the terminal a name check was about,
// unless it left its workspace or stopped needing a relaunch meanwhile.
func (m *Model) deliverTerminalChecked(r terminalChecked) {
	r.ws.terminalChecking = false
	if r.ws.terminal() != r.inst {
		return // the next tick looks again
	}
	if !r.inst.CrashRecovered() && !r.inst.Paused() {
		r.ws.terminalUnsettled = false
		return
	}
	m.settleTerminal(r.ws, r.inst, r.held, r.err)
}

// saveTerminal saves ws after ensureTerminal created, relaunched or paused
// its terminal. Otherwise the record waits for the next save (a Create, or
// quit), and after a crash the next start finds a live terminal with no
// record, which the next first open kills and recreates, losing its
// conversation. A failure is logged: the terminal runs either way.
func (m *Model) saveTerminal(ws *Workspace) {
	if err := m.saveWS(ws); err != nil {
		log.For("core").Warn("workspace_terminal.save_failed", "workspace", ws.Label(), "err", err)
	}
}

// sweepOrphans kills the loom tmux sessions no served workspace claims
// that were started under one of their roots (CleanupOrphanedSessions).
// Each workspace's terminal title is claimed too: a live terminal with no
// record waits for its workspace's first open, which replaces it
// (ensureTerminal). A workspace whose load failed is left out of the owned
// roots, its titles being unknown, so its sessions are spared while every
// other workspace is still swept.
func (m *Model) sweepOrphans() {
	claimed := map[string]bool{}
	var owned []*config.WorkspaceContext
	for _, ws := range m.workspaces {
		if ws.loadErr != nil || ws.ctx == nil {
			continue
		}
		claimTitles(claimed, ws)
		claimed[ws.terminalTitle()] = true
		owned = append(owned, ws.ctx)
	}
	if len(owned) == 0 {
		return
	}
	scope := session.NewSweepScope(owned, m.registry)
	if _, err := session.CleanupOrphanedSessions(claimed, scope, m.executor()); err != nil {
		log.For("core").Error("orphan_cleanup_failed", "err", err)
	}
}
