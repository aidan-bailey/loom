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
	// opened is set the first time a client shows the workspace
	// (Model.open). The model loads every workspace when it boots, but
	// starts a workspace's terminal, probes it, and polls GitHub for its
	// repository only once someone has looked at it.
	opened bool
	// loaded is set once its storage has been loaded (loadWS), or tried:
	// boot loads the startup workspace only if nothing has.
	loaded bool
	// loadErr is what the workspace's storage failed to load with, nil
	// once it loaded. A workspace that failed is kept, empty, its storage
	// latched shut (no write can overwrite the unreadable payload), and a
	// client opening it retries the load (Model.retryLoad).
	loadErr error
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

// Name is the workspace's registered name, "" for the global context (and
// for a nil workspace).
func (w *Workspace) Name() string {
	if w == nil || w.ctx == nil {
		return ""
	}
	return w.ctx.Name
}

// Label names the workspace in notices: its name, or "global" (also for a
// nil workspace).
func (w *Workspace) Label() string {
	if n := w.Name(); n != "" {
		return n
	}
	return "global"
}

// instances returns the workspace's instances in display order: the
// workspace terminal first when there is one, the rest in the order they
// were added. It is the model's own slice: valid until the next model
// call; copy it to keep it, and always before handing it to a job.
// Callers must not modify it.
func (w *Workspace) instances() []*session.Instance { return w.insts }

// add adds inst: first when it is the workspace terminal, last otherwise.
func (w *Workspace) add(inst *session.Instance) {
	if inst.IsWorkspaceTerminal {
		w.insts = append([]*session.Instance{inst}, w.insts...)
		return
	}
	w.insts = append(w.insts, inst)
}

// remove removes inst by identity, reporting whether the workspace held
// it. Identity, never a title: two workspaces can hold same-titled
// instances. Only bookkeeping; the caller runs any Kill.
func (w *Workspace) remove(inst *session.Instance) bool {
	i := slices.Index(w.insts, inst)
	if i < 0 {
		return false
	}
	w.insts = slices.Delete(w.insts, i, i+1)
	return true
}

// replace puts replacement in old's place (old found by identity), so the
// order, and the row a view's selection is on, are unchanged. Reports
// whether old was held.
func (w *Workspace) replace(old, replacement *session.Instance) bool {
	i := slices.Index(w.insts, old)
	if i < 0 {
		return false
	}
	w.insts[i] = replacement
	return true
}

// holds reports whether inst is one of the workspace's instances.
func (w *Workspace) holds(inst *session.Instance) bool {
	return inst != nil && slices.Contains(w.insts, inst)
}

// byTitle returns the instance titled title, or nil.
func (w *Workspace) byTitle(title string) *session.Instance {
	for _, inst := range w.insts {
		if inst.Title == title {
			return inst
		}
	}
	return nil
}

// configDir is the directory the workspace's state and config live in;
// "" for a bare test fixture.
func (w *Workspace) configDir() string {
	if w.ctx == nil {
		return ""
	}
	return w.ctx.ConfigDir
}

// terminalTitle is the title of the workspace's terminal: its name, or
// "Workspace Terminal" for a nameless context.
func (w *Workspace) terminalTitle() string {
	if w.ctx != nil && w.ctx.Name != "" {
		return w.ctx.Name
	}
	return "Workspace Terminal"
}

// terminal is the workspace's terminal instance, nil when it has none.
func (w *Workspace) terminal() *session.Instance {
	for _, inst := range w.insts {
		if inst.IsWorkspaceTerminal {
			return inst
		}
	}
	return nil
}
