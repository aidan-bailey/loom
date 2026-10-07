package core

import (
	"reflect"
	"slices"

	"github.com/aidan-bailey/loom/session"
)

// idOf returns inst's ID, assigning the next one the first time.
func (m *Model) idOf(inst *session.Instance) InstanceID {
	if id, ok := m.ids[inst]; ok {
		return id
	}
	if m.ids == nil {
		m.ids = make(map[*session.Instance]InstanceID)
	}
	m.nextID++
	m.ids[inst] = m.nextID
	return m.nextID
}

// lookup resolves id to its instance and the loaded workspace holding it,
// or nil, nil when no loaded workspace holds it.
func (m *Model) lookup(id InstanceID) (*session.Instance, *Workspace) {
	if id == 0 {
		return nil, nil
	}
	for _, ws := range m.Loaded() {
		for _, inst := range ws.insts {
			if m.ids[inst] == id {
				return inst, ws
			}
		}
	}
	return nil, nil
}

// viewOf copies inst's state into a view, under the instance's own locks.
func (m *Model) viewOf(inst *session.Instance) InstanceView {
	pane := inst.Pane()
	v := InstanceView{
		ID:                  m.idOf(inst),
		Title:               inst.Title,
		RepoPath:            inst.Path,
		WorktreePath:        inst.GetWorktreePath(),
		Branch:              inst.GetBranch(),
		TmuxSession:         pane.TmuxSessionName(),
		SessionProgram:      pane.SessionProgram(),
		Program:             inst.Program(),
		Status:              inst.GetStatus(),
		StatusSince:         inst.StatusSince(),
		WaitReason:          inst.WaitReason(),
		Subagents:           slices.Clone(inst.Subagents()),
		GitHub:              inst.GitHubState(),
		Issue:               inst.IssueNumber(),
		Account:             inst.Account(),
		HeadroomProxy:       inst.HeadroomProxy(),
		CacheTTL1h:          inst.CacheTTL1h(),
		IsWorkspaceTerminal: inst.IsWorkspaceTerminal,
		Started:             inst.Started(),
	}
	_, _, v.StatusReported = inst.ClaudeStatus()
	v.LastMessage, v.HasLastMessage = inst.LastMessage()
	if d := inst.GetDiffStats(); d != nil {
		v.Diff, v.HasDiff = *d, true
	}
	v.Ahead, v.Behind, v.ParityKnown = inst.Parity()
	return v
}

// Views returns ws's instances as views, in display order (a fresh slice).
// The TUI seeds a new slot's store with it; afterwards ViewsChanged keeps
// the store current.
func (m *Model) Views(ws *Workspace) []InstanceView {
	if ws == nil {
		return nil
	}
	out := make([]InstanceView, len(ws.insts))
	for i, inst := range ws.insts {
		out[i] = m.viewOf(inst)
	}
	return out
}

// View returns the view of the loaded instance id.
func (m *Model) View(id InstanceID) (InstanceView, bool) {
	inst, _ := m.lookup(id)
	if inst == nil {
		return InstanceView{}, false
	}
	return m.viewOf(inst), true
}

// publishViews builds every loaded workspace's views and returns a
// ViewsChanged for each workspace whose views differ from the last
// published ones (always for a workspace not published before). It then
// forgets the published views of workspaces no longer loaded and the IDs
// of instances no loaded workspace holds.
func (m *Model) publishViews() []Event {
	var events []Event
	next := make(map[*Workspace][]InstanceView, len(m.published))
	live := make(map[*session.Instance]bool, len(m.ids))
	for _, ws := range m.Loaded() {
		views := make([]InstanceView, len(ws.insts))
		for i, inst := range ws.insts {
			views[i] = m.viewOf(inst)
			live[inst] = true
		}
		next[ws] = views
		if prev, ok := m.published[ws]; !ok || !reflect.DeepEqual(prev, views) {
			// The event gets its own copy: the TUI keeps it as its store, which
			// must not alias the copy the next publish compares against.
			events = append(events, ViewsChanged{Workspace: ws, Views: slices.Clone(views)})
		}
	}
	m.published = next
	for inst := range m.ids {
		if !live[inst] {
			delete(m.ids, inst)
		}
	}
	return events
}

// Sync publishes the views that changed (ViewsChanged, first) and then
// returns everything else produced since the last call (Drain). The TUI
// calls it where it drained; core's own tests may still call Drain.
func (m *Model) Sync() Out {
	views := m.publishViews()
	out := m.Drain()
	out.Events = append(views, out.Events...)
	return out
}
