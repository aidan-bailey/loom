package core

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Loaded is every workspace the model serves (Boot): every registered one
// and the global one, dropped only when it leaves the registry
// (Unregister, applyRegistry), so this is more than a client shows (its
// tabs, or the workspace it shows while none is open). It returns a fresh
// slice, so a caller may keep iterating it while calling back into the
// model.
func (m *Model) Loaded() []*Workspace { return slices.Clone(m.workspaces) }

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

// PersistOpenList writes names to the registry's open list, so the next
// launch restores them: a client's tabs in order, then the workspaces it
// failed to open and keeps open. The open list is the client's; the model
// only writes it when asked.
func (m *Model) PersistOpenList(names []string) {
	if m.registry == nil {
		return
	}
	if err := m.registry.SetOpenWorkspaces(names); err != nil {
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

// Register adds dir to the registry as workspace name and returns the view
// of the workspace the model serves for it from now on, like every
// registered one, for the client to open (Open, which reports a load that
// failed). The registry has no lock, so this runs on the model's
// goroutine, never in a job.
func (m *Model) Register(name, dir string) (WorkspaceView, error) {
	if err := m.registry.Add(name, dir); err != nil {
		return WorkspaceView{}, fmt.Errorf("failed to register workspace: %w", err)
	}
	def := m.registry.FindByPath(dir)
	if def == nil {
		return WorkspaceView{}, fmt.Errorf("workspace not found after registration")
	}
	ws, err := m.ensureLoaded(*def)
	if ws == nil {
		return WorkspaceView{}, err
	}
	// Add reread the registry to add the entry: whatever else changed in
	// it meanwhile is served as every registry change is.
	m.serveRegistry()
	return m.wsViewOf(ws), nil
}

// errNoWorkspaceRegistry is the error of a registry request on a model
// with no registry (a bare test's).
var errNoWorkspaceRegistry = errors.New("no workspace registry")

// Unregister removes workspace name from the registry and stops serving
// it, saving it first. Its sessions keep running, untouched: registering
// its directory again serves them anew, under the same IDs, which the
// disk derives (stableID). Refused (ErrRefused) for the global workspace,
// which no entry names, an unknown name, and a workspace with a busy
// session or a reset running (busy), whose completion must find it
// served. An entry that is another name for a directory still registered
// (a twin), or for the global workspace's, only leaves the registry: the
// workspace stays served, under a name still registered (applyRegistry).
func (m *Model) Unregister(name string) error {
	if name == "" {
		return refusedError{errors.New("unregister: the global workspace is not registered")}
	}
	if m.registry == nil {
		return errNoWorkspaceRegistry
	}
	if err := m.registry.Reload(); err != nil {
		return err
	}
	def := m.registry.Get(name)
	if def == nil {
		return refusedError{fmt.Errorf("unregister %s: no such workspace", name)}
	}
	ws := m.soleServed(*def)
	if ws != nil {
		if why := busy(ws); why != "" {
			return refusedError{fmt.Errorf("unregister %s: %s", name, why)}
		}
		if err := m.saveForUnload(ws); err != nil {
			return fmt.Errorf("unregister %s: %w", name, err)
		}
	}
	if err := m.registry.Remove(name); err != nil {
		return err
	}
	if ws != nil {
		m.drop(ws)
	}
	m.serveRegistry()
	return nil
}

// RenameWorkspace renames registered workspace from to to: in the
// registry, whose open list and last used workspace follow, and in place
// in the model, whose workspace keeps its ID (its config dir's, wsIDOf),
// its sessions and its terminal's title. Refused (ErrRefused) for the
// global workspace, an unknown from, and a to already registered.
func (m *Model) RenameWorkspace(from, to string) error {
	switch {
	case from == "":
		return refusedError{errors.New("rename: the global workspace has no name to change")}
	case to == "":
		return refusedError{fmt.Errorf("rename %s: the new name is empty", from)}
	case m.registry == nil:
		return errNoWorkspaceRegistry
	}
	if err := m.registry.Reload(); err != nil {
		return err
	}
	def := m.registry.Get(from)
	switch {
	case def == nil:
		return refusedError{fmt.Errorf("rename %s: no such workspace", from)}
	case from == to:
		return nil
	case m.registry.Get(to) != nil:
		return refusedError{fmt.Errorf("rename %s: %s is already registered", from, to)}
	}
	dir := config.WorkspaceConfigDir(def)
	if err := m.registry.Rename(from, to); err != nil {
		return err
	}
	if ws := m.wsByConfigDir(dir); ws != nil && ws.Name() == from {
		m.renameWS(ws, to)
	}
	m.serveRegistry()
	return nil
}

// serveRegistry brings the served workspaces in line with the registry
// once it changed (a reread, an unregister, a rename): it serves every
// registered workspace (ensureLoaded), then drops or renames the served
// ones the registry no longer names as they are (applyRegistry).
func (m *Model) serveRegistry() {
	for _, def := range m.registry.Workspaces {
		_, _ = m.ensureLoaded(def)
	}
	m.applyRegistry()
}

// applyRegistry unloads a served workspace no registry entry reaches any
// more (unload), and renames in place one the entries reaching it name
// otherwise (renameWS), keeping its ID. The global workspace is never
// registered (an entry for its directory is its twin), so it is left
// alone.
func (m *Model) applyRegistry() {
	if m.registry == nil {
		return
	}
	for _, ws := range m.Loaded() {
		if ws.Name() == "" {
			continue
		}
		names := m.registeredNames(ws)
		switch {
		case len(names) == 0:
			m.unload(ws)
		case !slices.Contains(names, ws.Name()):
			m.renameWS(ws, names[0])
		}
	}
}

// registeredNames are the registry's names for ws's directory, in
// registry order: one, or more for twins (wsByConfigDir).
func (m *Model) registeredNames(ws *Workspace) []string {
	dir := canonicalDir(ws.configDir())
	var names []string
	for _, def := range m.registry.Workspaces {
		if canonicalDir(config.WorkspaceConfigDir(&def)) == dir {
			names = append(names, def.Name)
		}
	}
	return names
}

// soleServed is the served workspace def is the only registry entry for:
// nil when the model serves none for it, serves it as the global
// workspace, or another entry reaches it too.
func (m *Model) soleServed(def config.Workspace) *Workspace {
	ws := m.wsByConfigDir(config.WorkspaceConfigDir(&def))
	if ws == nil || ws.Name() == "" || len(m.registeredNames(ws)) > 1 {
		return nil
	}
	return ws
}

// busy says why ws can't be unloaded or reset now, "" when it can: a
// reset of it running, or a session a lifecycle job owns (Loading or
// Deleting), whose completion acts on its workspace.
func busy(ws *Workspace) string {
	if ws.resetting {
		return "a reset of it is running"
	}
	for _, inst := range ws.insts {
		if st := inst.GetStatus(); st == session.Loading || st == session.Deleting {
			return fmt.Sprintf("its session %s is busy (%s)", inst.Title, st)
		}
	}
	return ""
}

// unload stops serving ws, which the registry no longer names, once it is
// idle: a busy session (busy) or a failed save keeps it served until the
// next reread. Its sessions keep running.
func (m *Model) unload(ws *Workspace) {
	if why := busy(ws); why != "" {
		log.For("core").Info("workspace.unload_deferred", "workspace", ws.Label(), "reason", why)
		return
	}
	if err := m.saveForUnload(ws); err != nil {
		log.For("core").Warn("workspace.unload_deferred", "workspace", ws.Label(), "err", err)
		return
	}
	m.drop(ws)
}

// saveForUnload saves ws before the model stops serving it. A latched
// storage (quitSkipsSave) has nothing to save, and a config dir that is
// gone (a deleted repository) is not recreated by the save.
func (m *Model) saveForUnload(ws *Workspace) error {
	if ws.storage == nil {
		return nil
	}
	if dir := ws.configDir(); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			log.For("core").Warn("workspace.unload_save_skipped", "workspace", ws.Label(), "err", err)
			return nil
		}
	}
	if err := m.saveWS(ws); err != nil && !quitSkipsSave(err) {
		return err
	}
	return nil
}

// drop stops serving ws. Its view and its instances' IDs are forgotten at
// the next publish (publishWorkspaces, publishViews); a job of its still
// running finds no workspace holding its instance and changes nothing
// served.
func (m *Model) drop(ws *Workspace) {
	if i := slices.Index(m.workspaces, ws); i >= 0 {
		m.workspaces = slices.Delete(m.workspaces, i, i+1)
		log.For("core").Info("workspace.unloaded", "workspace", ws.Label())
	}
}

// renameWS serves ws under name from now on, through a renamed copy of
// its context (a job may hold the old one). Its ID derives from its
// config dir (wsIDOf), so it is unchanged; its terminal keeps its title,
// and its sessions their tmux names.
func (m *Model) renameWS(ws *Workspace, name string) {
	ctx := *ws.ctx
	log.For("core").Info("workspace.renamed", "from", ctx.Name, "to", name)
	ctx.Name = name
	ws.ctx = &ctx
}
