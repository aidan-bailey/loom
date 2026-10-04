package core

import (
	"slices"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
)

// Workspace is one loaded workspace: its context, the storage its
// instances persist to, its config and app state, its instances in
// display order, and the summary of its last orphan reconcile. The model
// owns it; the TUI reads it (its slot is a view over it) and changes its
// instances only through Add, Remove and Replace. The context, storage,
// config and state are fixed for the workspace's lifetime.
type Workspace struct {
	ctx      *config.WorkspaceContext
	storage  *session.Storage
	cfg      *config.Config
	state    config.AppState
	insts    []*session.Instance
	recovery RecoverySummary
}

// WorkspaceParts are a workspace's fixed handles. Any may be nil, as in a
// test's bare fixture.
type WorkspaceParts struct {
	Ctx     *config.WorkspaceContext
	Storage *session.Storage
	Config  *config.Config
	State   config.AppState
}

// NewWorkspace builds a workspace with no instances.
func NewWorkspace(p WorkspaceParts) *Workspace {
	return &Workspace{ctx: p.Ctx, storage: p.Storage, cfg: p.Config, state: p.State}
}

// Ctx is the workspace's context; nil only in bare test fixtures.
func (w *Workspace) Ctx() *config.WorkspaceContext { return w.ctx }

// Storage persists the workspace's instances.
func (w *Workspace) Storage() *session.Storage { return w.storage }

// Config is the workspace's config.json.
func (w *Workspace) Config() *config.Config { return w.cfg }

// State is the workspace's state.json: help screens seen and UI prefs.
func (w *Workspace) State() config.AppState { return w.state }

// Recovery is the summary of the workspace's last orphan reconcile.
func (w *Workspace) Recovery() RecoverySummary { return w.recovery }

// Name is the workspace's registered name, "" for the global context.
func (w *Workspace) Name() string {
	if w.ctx == nil {
		return ""
	}
	return w.ctx.Name
}

// Label names the workspace in notices: its name, or "global".
func (w *Workspace) Label() string {
	if n := w.Name(); n != "" {
		return n
	}
	return "global"
}

// Instances returns the workspace's instances in display order: the
// workspace terminal first when there is one, the rest in the order they
// were added. The slice is the workspace's own: callers must not modify
// it, and copy it to keep it past the next edit.
func (w *Workspace) Instances() []*session.Instance { return w.insts }

// Add adds inst: first when it is the workspace terminal, last otherwise.
func (w *Workspace) Add(inst *session.Instance) {
	if inst.IsWorkspaceTerminal {
		w.insts = append([]*session.Instance{inst}, w.insts...)
		return
	}
	w.insts = append(w.insts, inst)
}

// Remove removes inst by identity, reporting whether the workspace held
// it. Identity, never a title: two workspaces can hold same-titled
// instances. Only bookkeeping; the caller runs any Kill.
func (w *Workspace) Remove(inst *session.Instance) bool {
	i := slices.Index(w.insts, inst)
	if i < 0 {
		return false
	}
	w.insts = slices.Delete(w.insts, i, i+1)
	return true
}

// Replace puts replacement in old's place (old found by identity), so the
// order, and the row a view's selection is on, are unchanged. Reports
// whether old was held.
func (w *Workspace) Replace(old, replacement *session.Instance) bool {
	i := slices.Index(w.insts, old)
	if i < 0 {
		return false
	}
	w.insts[i] = replacement
	return true
}

// Holds reports whether inst is one of the workspace's instances.
func (w *Workspace) Holds(inst *session.Instance) bool {
	return inst != nil && slices.Contains(w.insts, inst)
}

// ByTitle returns the instance titled title, or nil.
func (w *Workspace) ByTitle(title string) *session.Instance {
	for _, inst := range w.insts {
		if inst.Title == title {
			return inst
		}
	}
	return nil
}
