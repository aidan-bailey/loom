package core

import (
	"fmt"
	"slices"
)

// The workspace half of the boundary by ID (daemon stage 1D): each wraps
// the pointer version it replaces. Package C of the plan deletes those.

// ownerFields names a completion's owner for its event: its ID and label.
// A nil owner is 0 and "global".
func (m *Model) ownerFields(owner *Workspace) (WorkspaceID, string) {
	if owner == nil {
		return 0, owner.Label()
	}
	return m.wsIDOf(owner), owner.Label()
}

// Workspaces are the views of every workspace the model serves, in serve
// order: what a client opens one by (Open) and shows a failed load from
// (LoadErr).
func (m *Model) Workspaces() []WorkspaceView {
	out := make([]WorkspaceView, len(m.workspaces))
	for i, ws := range m.workspaces {
		out[i] = m.wsViewOf(ws)
	}
	return out
}

// IsLoaded reports whether the model serves the workspace id.
func (m *Model) IsLoaded(id WorkspaceID) bool { return m.wsLookup(id) != nil }

// Open is a client showing the workspace id, as a tab or as the workspace
// it shows while none is open, and returns its view. A workspace whose
// load failed is loaded again first, and its error returned when it still
// fails: the client then shows nothing of it. The first open of a
// workspace starts its terminal (open). An unknown id is an error. Which
// workspaces a client shows is its own state: an open publishes nothing
// tab-like, so it changes nothing another client shows.
func (m *Model) Open(id WorkspaceID) (WorkspaceView, error) {
	ws := m.wsLookup(id)
	if ws == nil {
		return WorkspaceView{}, fmt.Errorf("open: the model serves no such workspace")
	}
	if err := m.open(ws); err != nil {
		return WorkspaceView{}, err
	}
	return m.wsViewOf(ws), nil
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

// Registry is a copy of the workspace registry: the registered
// workspaces, the open list (resolved, as GetOpenWorkspaces resolves it)
// and the last used workspace. Zero in bare tests.
func (m *Model) Registry() RegistryView {
	if m.registry == nil {
		return RegistryView{}
	}
	return RegistryView{
		Workspaces: slices.Clone(m.registry.Workspaces),
		Open:       m.registry.GetOpenWorkspaces(),
		LastUsed:   m.registry.LastUsed,
	}
}

// ReloadRegistry rereads the workspace registry from disk, for a caller
// about to show it (the workspace picker) or open a workspace it lacks:
// another process may have registered a workspace since, which the model
// then serves too. One no longer registered stays served until the next
// start.
func (m *Model) ReloadRegistry() error {
	if m.registry == nil {
		return fmt.Errorf("no workspace registry")
	}
	if err := m.registry.Reload(); err != nil {
		return err
	}
	for _, def := range m.registry.Workspaces {
		_, _ = m.ensureLoaded(def)
	}
	return nil
}

// AccountNames are the registered accounts, default first; Present is
// false before InitAccounts.
func (m *Model) AccountNames() AccountNames {
	if m.accounts == nil {
		return AccountNames{}
	}
	return AccountNames{Present: true, Default: m.accounts.Default(), Names: m.accounts.Names()}
}
