package core

import (
	"reflect"
	"slices"
)

// wsIDOf returns ws's ID, assigning the next one the first time.
func (m *Model) wsIDOf(ws *Workspace) WorkspaceID {
	if id, ok := m.wsIDs[ws]; ok {
		return id
	}
	if m.wsIDs == nil {
		m.wsIDs = make(map[*Workspace]WorkspaceID)
	}
	m.nextWSID++
	m.wsIDs[ws] = m.nextWSID
	return m.nextWSID
}

// wsLookup resolves id to its loaded workspace, or nil.
func (m *Model) wsLookup(id WorkspaceID) *Workspace {
	if id == 0 {
		return nil
	}
	for _, ws := range m.Loaded() {
		if m.wsIDs[ws] == id {
			return ws
		}
	}
	return nil
}

// wsViewOf copies ws's state into a view. Every handle locks itself
// (Config.Snapshot, the state's getters, the storage's), and every
// reference field is a fresh copy.
func (m *Model) wsViewOf(ws *Workspace) WorkspaceView {
	v := WorkspaceView{ID: m.wsIDOf(ws), Name: ws.Name(), Label: ws.Label(), Recovery: ws.recovery}
	if ws.ctx != nil {
		v.RepoPath, v.ConfigDir = ws.ctx.RepoPath, ws.ctx.ConfigDir
	}
	if ws.cfg != nil {
		v.Settings = ws.cfg.Snapshot()
	}
	if ws.state != nil {
		v.UIPrefs = ws.state.GetUIPrefs()
		v.HelpScreensSeen = ws.state.GetHelpScreensSeen()
	}
	if ws.storage != nil {
		v.WritesRefused = ws.storage.WritesRefused()
		v.PreservedTitles = ws.storage.PreservedTitles()
	}
	if ws.loadErr != nil {
		v.LoadErr = ws.loadErr.Error()
	}
	return v
}

// Workspace is the view of the served workspace id; false when the model
// serves none with it.
func (m *Model) Workspace(id WorkspaceID) (WorkspaceView, bool) {
	ws := m.wsLookup(id)
	if ws == nil {
		return WorkspaceView{}, false
	}
	return m.wsViewOf(ws), true
}

// publishWorkspaces returns a WorkspacesChanged with every served
// workspace's view, in serve order, when any of them differs from the last
// publish (or the served set changed), and forgets the IDs of workspaces no
// longer served.
func (m *Model) publishWorkspaces() []Event {
	views := make([]WorkspaceView, len(m.workspaces))
	live := make(map[*Workspace]bool, len(m.workspaces))
	for i, ws := range m.workspaces {
		views[i] = m.wsViewOf(ws)
		live[ws] = true
	}
	for ws := range m.wsIDs {
		if !live[ws] {
			delete(m.wsIDs, ws)
		}
	}
	if m.publishedWS != nil && reflect.DeepEqual(m.publishedWS, views) {
		return nil
	}
	m.publishedWS = views
	// The event gets its own copy, as ViewsChanged does: the TUI keeps
	// the views, which must not alias what the next publish compares.
	return []Event{WorkspacesChanged{Views: cloneWorkspaceViews(views)}}
}

// cloneWorkspaceViews deep-copies views: the slice and every field that
// refers to shared memory (the settings' profiles and pointers, the prefs'
// maps, the preserved titles).
func cloneWorkspaceViews(views []WorkspaceView) []WorkspaceView {
	out := slices.Clone(views)
	for i := range out {
		out[i].Settings = out[i].Settings.Clone()
		out[i].UIPrefs = out[i].UIPrefs.Clone()
		out[i].PreservedTitles = slices.Clone(out[i].PreservedTitles)
	}
	return out
}
