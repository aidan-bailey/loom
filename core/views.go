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

// lookup resolves id to its instance and the served workspace holding it,
// or nil, nil when none holds it. It runs on every pane event (PaneOutput,
// PaneQuiet), so it walks the workspaces directly rather than through
// Loaded, which allocates.
func (m *Model) lookup(id InstanceID) (*session.Instance, *Workspace) {
	if id == 0 {
		return nil, nil
	}
	find := func(ws *Workspace) *session.Instance {
		for _, inst := range ws.insts {
			if m.ids[inst] == id {
				return inst
			}
		}
		return nil
	}
	for _, ws := range m.workspaces {
		if inst := find(ws); inst != nil {
			return inst, ws
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
	if gw, err := inst.GetGitWorktree(); err == nil && gw != nil {
		v.WorktreeRepoPath = gw.GetRepoPath()
	}
	_, _, v.StatusReported = inst.ClaudeStatus()
	v.LastMessage, v.HasLastMessage = inst.LastMessage()
	if d := inst.GetDiffStats(); d != nil {
		v.Diff, v.HasDiff = *d, true
	}
	v.Ahead, v.Behind, v.ParityKnown = inst.Parity()
	return v
}

// viewsWS returns ws's instances as views, in display order (a fresh slice).
// The TUI seeds a new slot's store with it; afterwards ViewsChanged keeps
// the store current. ws must be loaded: the IDs it assigns to a workspace
// that isn't are pruned by the next publish, and its instances get new
// ones if it loads later.
func (m *Model) viewsWS(ws *Workspace) []InstanceView {
	if ws == nil {
		return nil
	}
	out := make([]InstanceView, len(ws.insts))
	for i, inst := range ws.insts {
		out[i] = m.viewOf(inst)
	}
	return out
}

// View returns the view of the instance id, which a served workspace must
// hold (false otherwise).
func (m *Model) View(id InstanceID) (InstanceView, bool) {
	inst, _ := m.lookup(id)
	if inst == nil {
		return InstanceView{}, false
	}
	return m.viewOf(inst), true
}

// publishViews builds every served workspace's views and returns a
// ViewsChanged for each whose views differ from the last published ones
// (always for a workspace not published before). It then forgets the
// published views of workspaces no longer served and the IDs of instances
// no served workspace holds.
func (m *Model) publishViews() []Event {
	var events []Event
	next := make(map[*Workspace][]InstanceView, len(m.published))
	live := make(map[*session.Instance]bool, len(m.ids))
	for _, ws := range m.workspaces {
		views := make([]InstanceView, len(ws.insts))
		for i, inst := range ws.insts {
			live[inst] = true
			views[i] = m.viewOf(inst)
		}
		next[ws] = views
		if prev, ok := m.published[ws]; !ok || !reflect.DeepEqual(prev, views) {
			// The event gets its own copy: the TUI keeps it as its store, which
			// must not alias the copy the next publish compares against.
			events = append(events, ViewsChanged{WS: m.wsIDOf(ws), Views: cloneViews(views)})
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

// cloneViews deep-copies views: the slice and each view's Subagents, the
// one field that refers to shared memory. Its other reference fields
// (strings, Diff.Error, StatusSince's location) are immutable.
func cloneViews(views []InstanceView) []InstanceView {
	out := slices.Clone(views)
	for i := range out {
		out[i].Subagents = slices.Clone(out[i].Subagents)
	}
	return out
}

// syncEvents publishes the workspace views that changed (WorkspacesChanged,
// first), then the model, account and GitHub views that changed
// (publishState), then the instance views that changed (ViewsChanged), and
// returns them ahead of every other event produced since the last call,
// which it forgets. It leaves the jobs: the loop starts those after every
// step.
func (m *Model) syncEvents() []Event {
	published := m.publishWorkspaces()
	published = append(published, m.publishState()...)
	published = append(published, m.publishViews()...)
	events := m.out.Events
	m.out.Events = nil
	return append(published, events...)
}

// Sync is syncEvents plus the jobs queued since the last call: everything
// the model produced, for a caller that runs its jobs itself (core's own
// tests; until stage 1E, the TUI). The loop (Loop.Sync) uses syncEvents.
func (m *Model) Sync() Out {
	return Out{Events: m.syncEvents(), Jobs: m.takeJobs()}
}
