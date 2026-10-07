package core

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
)

// Classic is the workspace shown while no tab is open; nil while one is.
func (m *Model) Classic() *Workspace { return m.classic }

// Tabs are the open workspace tabs, in tab order. It is the model's own
// slice: valid until the next model call; copy it to keep it, and always
// before handing it to a job. Callers must not modify it.
func (m *Model) Tabs() []*Workspace { return m.tabs }

// Loaded is every loaded workspace: the tabs, or the classic one alone. It
// returns a fresh slice, so a caller may keep iterating it while calling
// back into the model (which can close a tab).
func (m *Model) Loaded() []*Workspace {
	if len(m.tabs) == 0 {
		if m.classic == nil {
			return nil
		}
		return []*Workspace{m.classic}
	}
	return slices.Clone(m.tabs)
}

// Registry is the workspace registry (nil in bare tests). The TUI reads
// it for the picker; writes go through the model.
func (m *Model) Registry() *config.WorkspaceRegistry { return m.registry }

// RestoreFailed names the workspaces that failed to restore and are kept
// in the open list (see Model.restoreFailed).
func (m *Model) RestoreFailed() []string { return m.restoreFailed }

// Holding returns the loaded workspace holding inst (by identity), or nil.
func (m *Model) Holding(inst *session.Instance) *Workspace {
	for _, ws := range m.Loaded() {
		if ws.Holds(inst) {
			return ws
		}
	}
	return nil
}

// IsLoaded reports whether ws is still part of the model: an open tab, or
// the classic workspace.
func (m *Model) IsLoaded(ws *Workspace) bool {
	return ws != nil && slices.Contains(m.Loaded(), ws)
}

// Reopened reports whether ws's workspace is open in a loaded workspace
// other than ws itself: for one no longer loaded, whether the user has
// reopened it since.
func (m *Model) Reopened(ws *Workspace) bool {
	for _, w := range m.Loaded() {
		if w != ws && w.Label() == ws.Label() {
			return true
		}
	}
	return false
}

// ClosedNote describes, for a completion's notice, an owner workspace
// that was closed while the operation ran.
func (m *Model) ClosedNote(ws *Workspace) string {
	if m.Reopened(ws) {
		return "which was closed and reopened meanwhile"
	}
	return "which is no longer open"
}

// Instances returns every instance of every loaded workspace.
func (m *Model) Instances() []*session.Instance {
	var out []*session.Instance
	for _, ws := range m.Loaded() {
		out = append(out, ws.insts...)
	}
	return out
}

// ActiveInstances returns the loaded instances the background jobs may
// touch: started and not paused. Recoverable placeholders are ephemeral
// orphan-review rows: they report Started() (so recover/discard can reach
// their handles) but must never be driven by a background job, since the
// tick's repair would attach a pane client and TransitionTo(Running) would
// promote a never-confirmed orphan past the explicit recover flow. Loading
// rows are likewise owned by an in-flight Start/Resume/Recover: probing
// them mid-setup reads a dead tmux session and force-flips them to Paused
// under the op. Deleting rows are being torn down. The same set is what
// keeps a pane client (livePaneNames).
func (m *Model) ActiveInstances() []*session.Instance {
	var active []*session.Instance
	for _, inst := range m.Instances() {
		if ActiveInstance(inst) {
			active = append(active, inst)
		}
	}
	return active
}

// ActiveInstance reports whether the background jobs may touch inst (see
// ActiveInstances).
func ActiveInstance(inst *session.Instance) bool {
	st := inst.GetStatus()
	return inst.Started() && !inst.Paused() && st != session.Deleting && st != session.Recoverable && st != session.Loading
}

// InstanceForSession resolves a tmux session name (as carried by pane
// events) to the owning instance across every loaded workspace, or nil for
// terminal-pane sessions and unknown names.
func (m *Model) InstanceForSession(name string) *session.Instance {
	if name == "" {
		return nil
	}
	check := func(ws *Workspace) *session.Instance {
		for _, inst := range ws.insts {
			if inst.Pane().TmuxSessionName() == name {
				return inst
			}
		}
		return nil
	}
	// Runs on every pane event: check classic mode's one workspace directly
	// rather than through Loaded, which allocates there.
	if len(m.tabs) == 0 {
		if m.classic == nil {
			return nil
		}
		return check(m.classic)
	}
	for _, ws := range m.tabs {
		if inst := check(ws); inst != nil {
			return inst
		}
	}
	return nil
}

// OpenNames is the open set PersistOpenList persists and the picker shows
// selected: the tabs' names, then the workspaces that failed to restore
// and are to be retried.
func (m *Model) OpenNames() []string {
	names := make([]string, 0, len(m.tabs)+len(m.restoreFailed))
	for _, ws := range m.tabs {
		names = append(names, ws.Name())
	}
	for _, name := range m.restoreFailed {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// PersistOpenList writes OpenNames to the registry's open list, so the
// next launch restores them.
func (m *Model) PersistOpenList() {
	if m.registry == nil {
		return
	}
	if err := m.registry.SetOpenWorkspaces(m.OpenNames()); err != nil {
		log.For("core").Error("persist_open_workspaces_failed", "err", err)
	}
}

// SetLastUsed records name as the workspace the next launch focuses.
func (m *Model) SetLastUsed(name string) error {
	if m.registry == nil || name == "" {
		return nil
	}
	return m.registry.UpdateLastUsed(name)
}

// KeepRestoreFailed forgets every failed-to-restore workspace not in
// desired: the user left it unchecked in the picker, which closes it.
func (m *Model) KeepRestoreFailed(desired map[string]bool) {
	m.restoreFailed = slices.DeleteFunc(m.restoreFailed, func(n string) bool { return !desired[n] })
}

// StayGlobal applies a picker commit with nothing selected, made from
// global mode: nothing is rebuilt, and the only thing such a commit can
// change, the workspaces that failed to restore, is closed and the
// now-empty open list persisted. See app.stayInGlobalMode for why nothing
// reloads.
func (m *Model) StayGlobal() {
	m.restoreFailed = nil
	m.PersistOpenList()
}

// Register adds dir to the registry as workspace name and returns its
// entry. The registry has no lock, so this runs on the model's goroutine,
// never in a job.
func (m *Model) Register(name, dir string) (config.Workspace, error) {
	if err := m.registry.Add(name, dir); err != nil {
		return config.Workspace{}, fmt.Errorf("failed to register workspace: %w", err)
	}
	ws := m.registry.FindByPath(dir)
	if ws == nil {
		return config.Workspace{}, fmt.Errorf("workspace not found after registration")
	}
	return *ws, nil
}

// OpenTab loads a workspace as a new tab: its state, config and
// instances, reconciled against tmux and disk, crash-recovered sessions
// relaunched, its workspace terminal created when it has none, and orphan
// worktrees surfaced inline (reconcileOrphans). The first tab opened
// replaces the classic workspace (Classic is nil on return). A load error
// opens nothing and leaves state.json untouched. Formerly the lifecycle
// half of app.activateWorkspace; the TUI builds the tab's view over the
// returned workspace.
func (m *Model) OpenTab(def config.Workspace) (*Workspace, error) {
	wsCtx := config.WorkspaceContextFor(&def)
	state := config.LoadStateFrom(wsCtx.ConfigDir)
	appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)
	// Loom-context injection: keep the config-dir prompt files current and
	// sync the global enabled flag on every workspace load, before any
	// Claude session (workspace terminal, crash-restart, resume) launches.
	applySessionConfig(appConfig, wsCtx.ConfigDir)
	storage, err := session.NewStorage(state, wsCtx.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage for workspace %s: %w", def.Name, err)
	}

	cmdExec := m.executor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		// Fail closed: do NOT proceed to build an empty slot. Continuing
		// here would append a slot with zero instances, and the next
		// SaveInstances for it would overwrite a possibly-recoverable
		// (e.g. transiently unreadable or corrupt) state.json with only
		// the survivors — silent per-workspace data loss. The classic
		// startup path already fails closed this way; mirror it. The slot
		// is simply not opened, leaving state.json on disk untouched.
		return nil, fmt.Errorf("load instances for workspace %s: %w", def.Name, err)
	}
	// Orphan discovery runs here so every workspace-load path (startup
	// picker, mid-session toggle, restore, registration) surfaces
	// recovered sessions identically — no restart required.

	ws := NewWorkspace(WorkspaceParts{Ctx: wsCtx, Storage: storage, Config: appConfig, State: state})
	hasWorkspaceTerminal := false
	for _, inst := range instances {
		if inst.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		ws.Add(inst)
	}

	// Restart crash-recovered instances.
	for _, inst := range instances {
		if !inst.CrashRecovered() {
			continue
		}
		if err := inst.CrashRestart(); err != nil {
			log.For("core").Error("crash_recovery.restart_failed", "instance", inst.Title, "err", err)
			if tErr := inst.TransitionTo(session.Paused); tErr != nil {
				log.For("core").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr)
			}
		}
		inst.SetCrashRecovered(false)
	}

	// Auto-create workspace terminal if none exists. A record storage
	// preserves but could not load (after a downgrade every record is
	// undecodable, the terminal included) may already own the title: then
	// the terminal exists, just not in this binary's list, and killing its
	// session plus creating a second same-titled record would clobber it.
	wtTitle := def.Name
	if wtTitle == "" {
		wtTitle = "Workspace Terminal"
	}
	if !hasWorkspaceTerminal && wsCtx.RepoPath != "" && !slices.Contains(storage.PreservedTitles(), wtTitle) {
		// A prior non-clean exit may have left a tmux session named
		// loom_<wtTitle> alive without persisting the instance. The
		// multi-tab restore sweep (CleanupOrphanedSessions in
		// RestoreSaved) only runs AFTER every tab has
		// opened — but the workspace-terminal Start below happens now,
		// during OpenTab, and would fail with "session already exists"
		// against that orphan. Kill it here first so Start gets a clean
		// name; the later sweep handles any other stragglers. Only if it
		// is this workspace's, by the sweep's own ownership test: the
		// tmux server is shared, and another loom's session (a workspace
		// elsewhere with the same name) can carry this title. Anything
		// else is left running, and Start then fails on the name.
		scope := session.NewSweepScope([]*config.WorkspaceContext{wsCtx}, m.registry)
		if killed, err := session.KillOwnedTmuxSession(wtTitle, scope, cmdExec); err != nil {
			log.For("core").Warn("workspace_terminal.orphan_kill_skipped", "workspace", def.Name, "err", err.Error())
		} else if killed {
			log.For("core").Info("workspace_terminal.orphan_killed", "workspace", def.Name, "title", wtTitle)
		}

		wtOpts := launch.FromConfig(appConfig)
		if launch.RemoteControlBlocked(m.rcAuth, launch.EffectiveRemoteControl(wtOpts), appConfig.GetProgram()) {
			m.notifyInfo("remote control off: " + m.rcAuth.Reason)
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             launch.Compose(wtOpts, m.rcAuth, appConfig.GetProgram(), wtTitle),
			HeadroomProxy:       wtOpts.HeadroomProxy,
			CacheTTL1h:          wtOpts.CacheTTL1h,
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
		if wtErr != nil {
			log.For("core").Error("workspace_terminal.create_failed", "workspace", def.Name, "err", wtErr)
		} else {
			ws.Add(wtInstance)
			if startErr := wtInstance.Start(true); startErr != nil {
				log.For("core").Error("workspace_terminal.start_failed", "workspace", def.Name, "err", startErr)
			}
		}
	}

	ws.recovery = m.reconcileOrphans(ws, wsCtx.ConfigDir, appConfig.GetProgram(), cmdExec)
	if len(m.tabs) == 0 {
		m.classic = nil // the first tab replaces the classic workspace
	}
	m.tabs = append(m.tabs, ws)
	// Opened at last: no longer a restore failure to retry.
	m.restoreFailed = slices.DeleteFunc(m.restoreFailed, func(n string) bool { return n == def.Name })
	// Force the next health tick to poll: a newly opened workspace's repo
	// wasn't in openRepoPaths() until just now, and without this the
	// poller stays silent on it until the ambient ghInterval next elapses.
	m.ExpediteGitHub()
	return ws, nil
}

// CloseTab saves and closes the tab named name, returning it (nil, nil
// when no tab has that name). A failed save keeps the tab open, so its
// unsaved state stays reachable (silent data loss on teardown is worse
// than a sticky tab), and the last tab is never closed: leaving no tab
// means global mode, which only EnterGlobal builds.
func (m *Model) CloseTab(name string) (*Workspace, error) {
	idx := slices.IndexFunc(m.tabs, func(w *Workspace) bool { return w.Name() == name })
	if idx == -1 {
		return nil, nil
	}
	if len(m.tabs) == 1 {
		return nil, fmt.Errorf("cannot close %s, the last open workspace: return to global mode instead", name)
	}
	ws := m.tabs[idx]
	if err := ws.storage.SaveInstances(Persistable(ws.insts)); err != nil {
		log.For("core").Error("workspace.save_failed", "name", name, "err", err)
		return nil, fmt.Errorf("failed to save workspace %s: %w", name, err)
	}
	m.tabs = slices.Delete(m.tabs, idx, idx+1)
	return ws, nil
}

// EnterGlobal replaces every loaded workspace with the global one (the
// picker's Global row). It saves every tab, then loads the global context
// like classic startup (loadWorkspace, without the tmux sweep: the
// closing tabs' sessions are unclaimed here), and only then drops what was
// loaded, so a failure (a save or the load) switches nothing. The load has
// side effects an abort could not undo (it relaunches crash-recovered
// agents, writes the loom-context files, cleans orphan worktrees, sweeps
// hooks folders); that is why every save comes first. focused is the
// workspace the TUI shows: an aborted load puts its config's session flags
// back. Returns the global workspace, now Classic.
func (m *Model) EnterGlobal(focused *Workspace) (*Workspace, error) {
	// Persist every workspace tab before touching global state. A tab
	// whose save fails keeps its unpersisted state reachable only while
	// open, so abort — with nothing global loaded yet.
	for _, ws := range m.tabs {
		if err := ws.storage.SaveInstances(Persistable(ws.insts)); err != nil {
			log.For("core").Error("workspace.save_failed", "name", ws.Name(), "err", err)
			return nil, fmt.Errorf("failed to save workspace %s (staying in workspace mode): %w", ws.Name(), err)
		}
	}

	// Reconstruct global storage in the global config dir — resolved up
	// front, since orphan discovery and the hooks sweep need the directory
	// itself.
	globalCtx, err := config.GlobalWorkspaceContext()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the global config dir: %w", err)
	}
	cfgDir := globalCtx.ConfigDir
	appState := config.LoadStateFrom(cfgDir)
	appConfig := config.LoadConfigFrom(cfgDir)
	storage, err := session.NewStorage(appState, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("failed to construct global storage: %w", err)
	}
	global := NewWorkspace(WorkspaceParts{Ctx: globalCtx, Storage: storage, Config: appConfig, State: appState})
	// Sessions the load (re)starts launch under the global config's
	// settings, like OpenTab's; an abort puts the focused workspace's back.
	applySessionConfig(appConfig, cfgDir)
	if err := m.loadWorkspace(global, cfgDir, false); err != nil {
		if focused != nil {
			applySessionConfig(focused.cfg, "")
		}
		return nil, fmt.Errorf("failed to load global sessions (staying in workspace mode): %w", err)
	}
	m.tabs = nil
	m.classic = global
	// Clear the registry's open list so the next launch lands in global
	// mode rather than restoring tabs the user just closed. An explicit
	// return to global mode closes the workspaces that failed to restore
	// too.
	m.restoreFailed = nil
	if m.registry != nil {
		if err := m.registry.SetOpenWorkspaces(nil); err != nil {
			log.For("core").Warn("clear_open_workspaces_failed", "err", err)
		}
	}
	return global, nil
}

// RestoreSaved opens saved (the registry's open tabs from the last run)
// as tabs, plus the startup workspace (the classic context's name) when
// it is not among them, then sweeps orphan tmux sessions across the open
// tabs. A workspace that fails to open is logged; one that was open last
// time stays in the open list to be retried (RestoreFailed), and any
// failure skips the sweep, since that workspace's titles are unknown and
// the sweep would kill its live sessions. With no tab open it loads the
// classic workspace instead (loadClassicFallback). Returns the index of
// the tab to focus (the startup workspace's, else the registry's last
// used, else 0), or -1 when no tab opened. Formerly the lifecycle half of
// app.restoreSavedWorkspaces.
func (m *Model) RestoreSaved(saved []config.Workspace) int {
	explicit := m.classic.Name()

	desired := saved
	if explicit != "" && m.registry != nil {
		found := false
		for _, w := range desired {
			if w.Name == explicit {
				found = true
				break
			}
		}
		if !found {
			if ws := m.registry.Get(explicit); ws != nil {
				desired = append(desired, *ws)
			}
		}
	}

	var failed []string
	for _, def := range desired {
		if _, err := m.OpenTab(def); err != nil {
			log.For("core").Error("workspace.restore_failed", "name", def.Name, "err", err)
			failed = append(failed, def.Name)
			if slices.ContainsFunc(saved, func(s config.Workspace) bool { return s.Name == def.Name }) {
				// Was open: keep it open, to be retried (restoreFailed).
				m.restoreFailed = append(m.restoreFailed, def.Name)
			}
		}
	}

	// Sweep orphan tmux sessions left by prior crashes. The classic
	// startup path does this inline in loadWorkspace; the
	// multi-tab restore path historically did not, so stale
	// loom_*/claudesquad_* sessions accumulated across restarts. Each
	// tab's OpenTab call above already ran reconcileOrphans,
	// which adds recovered-but-undecided orphans as Recoverable rows
	// directly into the workspace — so the claimed set here (built from every
	// tab's live instances, Recoverable included, plus the records each
	// tab's storage preserves outside its list) is complete without a
	// separate pending-orphans accumulator. The sweep only considers
	// sessions started under an open tab's repo or worktrees dir: those
	// of workspaces this process did not open may belong to another
	// running loom.
	//
	// Fail closed when any workspace failed to load: its titles are
	// unreadable, so the sweep can't spare them and would kill its live
	// sessions. Skipping only defers stale-session cleanup to a later run.
	if len(failed) > 0 {
		log.For("core").Warn("orphan_cleanup_skipped", "reason", "workspace_load_failed", "workspaces", failed)
	} else {
		claimedTitles := make(map[string]bool)
		owned := make([]*config.WorkspaceContext, 0, len(m.tabs))
		for _, ws := range m.tabs {
			claimTitles(claimedTitles, ws)
			owned = append(owned, ws.ctx)
		}
		scope := session.NewSweepScope(owned, m.registry)
		if _, err := session.CleanupOrphanedSessions(claimedTitles, scope, m.executor()); err != nil {
			log.For("core").Error("orphan_cleanup_failed", "err", err)
		}
	}

	if len(m.tabs) == 0 {
		m.loadClassicFallback()
		return -1
	}

	focused := 0
	focusName := explicit
	if focusName == "" && m.registry != nil {
		focusName = m.registry.LastUsed
	}
	if focusName != "" {
		for i, ws := range m.tabs {
			if ws.Name() == focusName {
				focused = i
				break
			}
		}
	}

	if m.registry != nil {
		m.PersistOpenList()
		if name := m.tabs[focused].Name(); name != "" {
			if err := m.registry.UpdateLastUsed(name); err != nil {
				log.For("core").Debug("registry.update_last_used_failed", "workspace", name, "err", err)
			}
		}
	}
	return focused
}
