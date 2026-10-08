package core

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Loaded is every workspace the model serves (Boot): it never drops one,
// so this is more than a client shows (its tabs, or the workspace it shows
// while none is open). It returns a fresh slice, so a caller may keep
// iterating it while calling back into the model.
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
	return m.wsViewOf(ws), nil
}
