package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
)

// The TUI's view of instances: each slot keeps its workspace's
// core.InstanceView values (workspaceSlot.views), replaced wholesale by
// core.ViewsChanged and seeded from core.Model.Views when the slot is
// built. The rail and every other reader see them through slotRows, which
// lays the TUI's own overlays (bells; the pane ladder from package C) over
// them and appends the creation flow's draft row. Nothing in app holds a
// *session.Instance once package D lands; until then the script host
// does, and its writes reach instances through the bridge (instOf).

// slotRows is a slot's list source (ui.InstanceSource): its views with the
// TUI's overlays applied.
type slotRows struct {
	m *home
	s *workspaceSlot
}

// Rows returns the slot's rows, freshly copied.
func (r slotRows) Rows() []core.InstanceView { return r.m.rowsOf(r.s) }

// rowsOf copies s's views with the TUI's overlays applied, followed by
// the open draft's row when the draft belongs to s.
func (m *home) rowsOf(s *workspaceSlot) []core.InstanceView {
	rows := make([]core.InstanceView, len(s.views), len(s.views)+1)
	copy(rows, s.views)
	for i := range rows {
		m.overlay(&rows[i])
	}
	if d, ok := m.draftRow(s); ok {
		rows = append(rows, d)
	}
	return rows
}

// overlay lays the TUI's own state over a copied view: every row a reader
// sees goes through it (rowsOf, findRow).
func (m *home) overlay(v *core.InstanceView) {
	v.Bell = m.bells[v.ID]
}

// seedViews fills a new slot's store from the model, when the model already
// holds its workspace (core.Model.Views assigns IDs only a loaded
// workspace keeps). A slot built before its workspace loads is filled by
// the first ViewsChanged after the load instead.
func (m *home) seedViews(s *workspaceSlot) {
	if m.core != nil && m.core.IsLoaded(s.ws) {
		s.views = m.core.Views(s.ws)
	}
}

// syncViews reseeds every open slot's store from the model. Production
// keeps the stores current through ViewsChanged; tests that change the
// model outside an Update call this before reading through the TUI. So
// does the TUI itself after it changes the model's instances directly
// (a write through the bridge, core.Model.InstanceOf, a pointer
// operation, a workspace edit, a script's), when the rest of the same
// Update reads the change back: the stores otherwise catch up only at the
// drain, and those reads used to see the shared instance. Package C's
// requests and overlays retire those calls.
func (m *home) syncViews() {
	if m.core == nil {
		return
	}
	for _, s := range m.openSlots() {
		if s != nil {
			s.views = m.core.Views(s.ws)
		}
	}
}

// viewByID returns the row with id in any open slot (overlays applied), and
// its slot.
func (m *home) viewByID(id core.InstanceID) (*core.InstanceView, *workspaceSlot) {
	return m.findRow(func(v *core.InstanceView) bool { return v.ID == id })
}

// viewBySession resolves a tmux session name (as pane events carry it) to
// the row of the instance whose agent session it is, across every open
// slot; nil for terminal-pane sessions and unknown names.
func (m *home) viewBySession(name string) (*core.InstanceView, *workspaceSlot) {
	if name == "" {
		return nil, nil
	}
	return m.findRow(func(v *core.InstanceView) bool { return v.TmuxSession == name })
}

// findRow returns a copy of the first row of any open slot that match
// accepts, overlays applied, and its slot; the rows are rowsOf's, the
// draft's included. It runs on every pane event, so it applies the
// overlays to the one row it returns rather than copying every slot's
// rows (rowsOf).
func (m *home) findRow(match func(*core.InstanceView) bool) (*core.InstanceView, *workspaceSlot) {
	for _, s := range m.openSlots() {
		for i := range s.views {
			if match(&s.views[i]) {
				v := s.views[i]
				m.overlay(&v)
				return &v, s
			}
		}
		if d, ok := m.draftRow(s); ok && match(&d) {
			return &d, s
		}
	}
	return nil, nil
}

// activeViews returns the rows of every open slot that are active (see
// core.InstanceView.Active): those the pane clients and the snapshot scan
// cover.
func (m *home) activeViews() []core.InstanceView {
	var out []core.InstanceView
	for _, s := range m.openSlots() {
		for _, v := range m.rowsOf(s) {
			if v.Active() {
				out = append(out, v)
			}
		}
	}
	return out
}

// pruneBells forgets the bells of instances no open slot shows any more:
// their IDs are never reused, so the entries would only accumulate. The
// ViewsChanged applier runs it after replacing a store.
func (m *home) pruneBells() {
	if len(m.bells) == 0 {
		return
	}
	shown := make(map[core.InstanceID]bool)
	for _, s := range m.openSlots() {
		if s == nil {
			continue
		}
		for i := range s.views {
			shown[s.views[i].ID] = true
		}
	}
	for id := range m.bells {
		if !shown[id] {
			delete(m.bells, id)
		}
	}
}

// refreshSelection repoints the split pane and the menu at a fresh copy
// of the selected row. They hold copies, so a change to the row (a
// status, a diff, an overlay) reaches them only through this. It has
// none of instanceChanged's side effects (focus forwarding, the stored
// ratio, the workbench retarget, the bell clear).
func (m *home) refreshSelection() {
	sel := m.list.GetSelectedInstance()
	m.splitPane.SetInstance(sel)
	m.menu.SetInstance(sel)
}

// instOf resolves id to its instance through the bridge
// (core.Model.InstanceOf): nil when no loaded workspace holds it, or in a
// bare test home. It serves the writes and tmux probes package C turns
// into requests and by-name probes, and nothing else.
func (m *home) instOf(id core.InstanceID) *session.Instance {
	if m.core == nil {
		return nil
	}
	return m.core.InstanceOf(id)
}

// tmuxAlive reports whether v's agent tmux session is alive (a has-session
// probe, through the bridge); false when no loaded workspace holds it.
func (m *home) tmuxAlive(v *core.InstanceView) bool {
	inst := m.instOf(v.ID)
	return inst != nil && inst.Pane().TmuxAlive()
}
