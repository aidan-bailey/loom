package core

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/config"
)

// The workspace half of the boundary by ID (daemon stage 1D): each wraps
// the pointer version it replaces. Package C of the plan deletes those.

// ownerFields names a completion's owner for its event: its ID and label,
// and, when it is no longer loaded, where it went (ClosedNoteWS). A nil
// owner is 0, "global" and no note.
func (m *Model) ownerFields(owner *Workspace, loaded bool) (WorkspaceID, string, string) {
	if owner == nil {
		return 0, owner.Label(), ""
	}
	note := ""
	if !loaded {
		note = m.closedNoteWS(owner)
	}
	return m.wsIDOf(owner), owner.Label(), note
}

// Classic is the view of the workspace shown while no tab is open; false
// while one is (or before any loads).
func (m *Model) Classic() (WorkspaceView, bool) {
	if m.classic == nil || len(m.tabs) > 0 {
		return WorkspaceView{}, false
	}
	return m.wsViewOf(m.classic), true
}

// Tabs are the open tabs' views, in tab order.
func (m *Model) Tabs() []WorkspaceView {
	out := make([]WorkspaceView, len(m.tabs))
	for i, ws := range m.tabs {
		out[i] = m.wsViewOf(ws)
	}
	return out
}

// IsLoaded reports whether the workspace id is still loaded.
func (m *Model) IsLoaded(id WorkspaceID) bool { return m.wsLookup(id) != nil }

// OpenTab loads def as a new tab (OpenTabWS) and returns its view.
func (m *Model) OpenTab(def config.Workspace) (WorkspaceView, error) {
	ws, err := m.openTabWS(def)
	if err != nil || ws == nil {
		return WorkspaceView{}, err
	}
	return m.wsViewOf(ws), nil
}

// CloseTab saves and closes the tab named name (CloseTabWS).
func (m *Model) CloseTab(name string) error {
	_, err := m.closeTabWS(name)
	return err
}

// EnterGlobal replaces every loaded workspace with the global one
// (EnterGlobalWS) and returns its view. focused is the workspace the
// caller had focused, 0 for none.
func (m *Model) EnterGlobal(focused WorkspaceID) (WorkspaceView, error) {
	ws, err := m.enterGlobalWS(m.wsLookup(focused))
	if err != nil || ws == nil {
		return WorkspaceView{}, err
	}
	return m.wsViewOf(ws), nil
}

// Save persists the workspace id's instances (SaveWS). An unknown id is an
// error: there is nothing loaded to save.
func (m *Model) Save(id WorkspaceID) error {
	ws := m.wsLookup(id)
	if ws == nil {
		return fmt.Errorf("save: the workspace is no longer open")
	}
	return m.saveWS(ws)
}

// Views returns the workspace id's instances as views (ViewsWS); nil for
// an unknown id.
func (m *Model) Views(id WorkspaceID) []InstanceView {
	ws := m.wsLookup(id)
	if ws == nil {
		return nil
	}
	return m.viewsWS(ws)
}

// Create builds an instance in the workspace id (CreateWS), which refuses
// a workspace that is not loaded.
func (m *Model) Create(id WorkspaceID, spec NewInstance, req ReqID) {
	m.createWS(m.wsLookup(id), spec, req)
}

// Registry is a copy of the workspace registry: the registered workspaces
// and the open list (resolved, as GetOpenWorkspaces resolves it). Zero in
// bare tests.
func (m *Model) Registry() RegistryView {
	if m.registry == nil {
		return RegistryView{}
	}
	return RegistryView{
		Workspaces: slices.Clone(m.registry.Workspaces),
		Open:       m.registry.GetOpenWorkspaces(),
	}
}

// ReloadRegistry rereads the workspace registry from disk, for a caller
// about to show it (the workspace picker): another process may have
// registered a workspace since.
func (m *Model) ReloadRegistry() error {
	if m.registry == nil {
		return fmt.Errorf("no workspace registry")
	}
	return m.registry.Reload()
}

// AccountNames are the registered accounts, default first; Present is
// false before InitAccounts.
func (m *Model) AccountNames() AccountNames {
	if m.accounts == nil {
		return AccountNames{}
	}
	return AccountNames{Present: true, Default: m.accounts.Default(), Names: m.accounts.Names()}
}
