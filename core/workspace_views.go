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

// shownLookup is wsLookup over the shown workspaces alone: the queries a
// client answers from its replica (IsLoaded, Workspace, Views, View) answer
// for what is published, as the replica does.
func (m *Model) shownLookup(id WorkspaceID) *Workspace {
	if ws := m.wsLookup(id); ws != nil && slices.Contains(m.shown(), ws) {
		return ws
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
	return v
}

// Workspace is the view of the loaded workspace id; false when no loaded
// workspace has it (closed, or never reported).
func (m *Model) Workspace(id WorkspaceID) (WorkspaceView, bool) {
	ws := m.shownLookup(id)
	if ws == nil {
		return WorkspaceView{}, false
	}
	return m.wsViewOf(ws), true
}

// publishWorkspaces returns a WorkspacesChanged with every shown
// workspace's view, in shown order, when any of them differs from the last
// publish (or the shown set changed), and forgets the IDs of workspaces no
// longer served.
func (m *Model) publishWorkspaces() []Event {
	shown := m.shown()
	views := make([]WorkspaceView, len(shown))
	for i, ws := range shown {
		views[i] = m.wsViewOf(ws)
	}
	live := make(map[*Workspace]bool, len(m.workspaces))
	for _, ws := range m.workspaces {
		live[ws] = true
	}
	for ws := range m.wsIDs {
		if !live[ws] {
			delete(m.wsIDs, ws)
		}
	}
	classic := m.classicShown()
	if m.publishedWS != nil && m.publishedClassic == classic && reflect.DeepEqual(m.publishedWS, views) {
		return nil
	}
	m.publishedWS, m.publishedClassic = views, classic
	// The event gets its own copy, as ViewsChanged does: the TUI keeps
	// the views, which must not alias what the next publish compares.
	return []Event{WorkspacesChanged{Views: cloneWorkspaceViews(views), Classic: classic}}
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
