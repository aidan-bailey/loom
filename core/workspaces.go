package core

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Loaded is every workspace the model serves (boot): it never drops one,
// so this is more than the TUI shows (its tabs, or the classic workspace).
// It returns a fresh slice, so a caller may keep iterating it while calling
// back into the model.
func (m *Model) Loaded() []*Workspace { return slices.Clone(m.workspaces) }

// shown is the workspaces the TUI shows: its tabs, or the classic
// workspace alone. The published state (WorkspacesChanged, ViewsChanged)
// covers these, as if they were all the model loaded, while the model
// serves every workspace (Loaded).
func (m *Model) shown() []*Workspace {
	if len(m.tabs) > 0 {
		return slices.Clone(m.tabs)
	}
	if m.classic == nil {
		return nil
	}
	return []*Workspace{m.classic}
}

// RestoreFailed names the workspaces that failed to restore and are kept
// in the open list (see Model.restoreFailed).
func (m *Model) RestoreFailed() []string { return slices.Clone(m.restoreFailed) }

// holding returns the loaded workspace holding inst (by identity), or nil.
func (m *Model) holding(inst *session.Instance) *Workspace {
	for _, ws := range m.Loaded() {
		if ws.holds(inst) {
			return ws
		}
	}
	return nil
}

// isLoadedWS reports whether the model serves ws.
func (m *Model) isLoadedWS(ws *Workspace) bool {
	return ws != nil && slices.Contains(m.workspaces, ws)
}

// allInstances returns every instance of every loaded workspace.
func (m *Model) allInstances() []*session.Instance {
	var out []*session.Instance
	for _, ws := range m.Loaded() {
		out = append(out, ws.insts...)
	}
	return out
}

// activeInstances returns the loaded instances the background jobs may
// touch: started and not paused. Recoverable placeholders are ephemeral
// orphan-review rows: they report Started() (so recover/discard can reach
// their handles) but must never be driven by a background job, since the
// tick's repair would attach a pane client and TransitionTo(Running) would
// promote a never-confirmed orphan past the explicit recover flow. Loading
// rows are likewise owned by an in-flight Start/Resume/Recover: probing
// them mid-setup reads a dead tmux session and force-flips them to Paused
// under the op. Deleting rows are being torn down. The same set is what
// keeps a pane client (livePaneNames).
//
// A workspace terminal is left out until its workspace is first opened:
// it is not started then (ensureTerminal), and one restored from disk
// with its session gone must not be found dead and relaunched, or paused,
// for a workspace nobody has looked at.
func (m *Model) activeInstances() []*session.Instance {
	var active []*session.Instance
	for _, ws := range m.Loaded() {
		for _, inst := range ws.insts {
			if activeInstance(inst) && (ws.opened || !inst.IsWorkspaceTerminal) {
				active = append(active, inst)
			}
		}
	}
	return active
}

// activeInstance reports whether the background jobs may touch inst (see
// activeInstances).
func activeInstance(inst *session.Instance) bool {
	st := inst.GetStatus()
	return inst.Started() && !inst.Paused() && st != session.Deleting && st != session.Recoverable && st != session.Loading
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
	// Served from now on, like every registered workspace. A load error is
	// the open's to report: OpenTab retries it.
	_, _ = m.ensureLoaded(*ws)
	return *ws, nil
}

// openTabWS shows def's workspace as a new tab: the model serves it
// already (boot, or ensureLoaded for one registered since), so opening
// loads nothing but retries a load that failed. Its first open starts its
// workspace terminal (open). A load error opens nothing and leaves
// state.json untouched. Formerly the lifecycle half of
// app.activateWorkspace; the TUI builds the tab's view over the returned
// workspace.
func (m *Model) openTabWS(def config.Workspace) (*Workspace, error) {
	ws, err := m.ensureLoaded(def)
	if ws == nil {
		return nil, err
	}
	if err := m.retryLoad(ws); err != nil {
		return nil, err
	}
	if !slices.Contains(m.tabs, ws) {
		m.tabs = append(m.tabs, ws)
	}
	// Opened at last: no longer a restore failure to retry.
	m.restoreFailed = slices.DeleteFunc(m.restoreFailed, func(n string) bool { return n == def.Name })
	m.open(ws)
	return ws, nil
}

// closeTabWS closes the tab named name, returning it (nil, nil when no tab
// has that name). The workspace stays served: closing a tab only stops
// showing it. The last tab is never closed: leaving no tab means global
// mode, which only EnterGlobal sets up.
func (m *Model) closeTabWS(name string) (*Workspace, error) {
	idx := slices.IndexFunc(m.tabs, func(w *Workspace) bool { return w.Name() == name })
	if idx == -1 {
		return nil, nil
	}
	if len(m.tabs) == 1 {
		return nil, fmt.Errorf("cannot close %s, the last open workspace: return to global mode instead", name)
	}
	ws := m.tabs[idx]
	m.tabs = slices.Delete(m.tabs, idx, idx+1)
	return ws, nil
}

// enterGlobalWS shows the global workspace in place of the tabs (the
// picker's Global row): it is the classic workspace from now on. The model
// served it all along, so nothing loads, saves or is dropped; a load of it
// that failed is retried, and still failing switches nothing. focused, the
// workspace the TUI shows, is unused. Returns the global workspace.
func (m *Model) enterGlobalWS(focused *Workspace) (*Workspace, error) {
	global, err := m.globalWS()
	if global == nil {
		return nil, err
	}
	if err := m.retryLoad(global); err != nil {
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
	m.open(global)
	return global, nil
}

// RestoreSaved boots the model (boot, whose sweep covers every workspace)
// and shows saved (the registry's open tabs from the last run) as tabs,
// plus the startup workspace (the classic context's name) when it is not
// among them. A workspace that fails to open is logged; one that was open
// last time stays in the open list to be retried (RestoreFailed). With no
// tab open it shows the classic workspace instead (loadClassicFallback).
// Returns the index of the tab to focus (the startup workspace's, else the
// registry's last used, else 0), or -1 when no tab opened. Formerly the
// lifecycle half of app.restoreSavedWorkspaces.
func (m *Model) RestoreSaved(saved []config.Workspace) int {
	m.boot()
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

	for _, def := range desired {
		if _, err := m.openTabWS(def); err != nil {
			log.For("core").Error("workspace.restore_failed", "name", def.Name, "err", err)
			if slices.ContainsFunc(saved, func(s config.Workspace) bool { return s.Name == def.Name }) {
				// Was open: keep it open, to be retried (restoreFailed).
				m.restoreFailed = append(m.restoreFailed, def.Name)
			}
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
