package app

import (
	"sort"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// jumpWaiting moves selection to the next/prev agent needing attention
// (Prompting or bell), across all open workspaces, wrapping. When the
// target is in another slot it saves the current slot, focuses the
// target's, and selects there. No-op when none wait. Main-goroutine
// only.
func (m *home) jumpWaiting(dir int) {
	// Overview mode with slots: `]`/`[` move the overview cursor to the
	// next waiting card — no focus switch, no OpenWorkspaces write.
	// Committing is enter/D/r's job (focusCursorSlot). Collapsed groups
	// are skipped, matching cursor motion (the cursor cannot sit on an
	// unrendered card). Classic overview falls through: m.list's
	// selection IS the cursor there.
	if m.viewMode == viewOverview && len(m.slots) > 0 {
		order := m.fleetOrder()
		n := len(order)
		if n == 0 {
			return
		}
		start := 0
		for i, p := range order {
			if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
				start = i
				break
			}
		}
		for step := 1; step <= n; step++ {
			p := order[((start+dir*step)%n+n)%n]
			inst := m.slotList(p.slot).GetInstances()[p.inst]
			if inst.GetStatus() == session.Prompting || inst.BellPending() {
				m.overviewCursor = overviewCursor{slot: p.slot, inst: p.inst}
				return
			}
		}
		return
	}
	// Build fleet display order over ALL slots (not just loaded/expanded
	// — waiting agents in collapsed groups are still reachable). Unlike
	// fleetOrder (used by overview cursor motion), this does NOT skip
	// collapsed groups.
	var order []fleetPos
	if len(m.slots) == 0 {
		// Classic/global mode: no slots, walk m.list directly.
		items := m.list.GetInstances()
		for _, idx := range ui.SortForOverview(items) {
			if items[idx].GetStatus() == session.Deleting {
				continue
			}
			order = append(order, fleetPos{slot: 0, inst: idx})
		}
	} else {
		for _, si := range m.fleetSlotOrder() {
			items := m.slotList(si).GetInstances()
			for _, idx := range ui.SortForOverview(items) {
				if items[idx].GetStatus() == session.Deleting {
					continue
				}
				order = append(order, fleetPos{slot: si, inst: idx})
			}
		}
	}
	n := len(order)
	if n == 0 {
		return
	}
	// Current position: focused slot's current selection (or -1).
	start := -1
	selIdx := m.list.SelectedIdx()
	for i, p := range order {
		if p.slot == m.focusedSlot && p.inst == selIdx {
			start = i
			break
		}
	}
	if start < 0 {
		start = 0
	}
	for step := 1; step <= n; step++ {
		i := ((start+dir*step)%n + n) % n
		p := order[i]
		var list *ui.List
		if len(m.slots) == 0 {
			list = m.list
		} else {
			list = m.slotList(p.slot)
		}
		inst := list.GetInstances()[p.inst]
		if inst.GetStatus() == session.Prompting || inst.BellPending() {
			if len(m.slots) != 0 && p.slot != m.focusedSlot {
				m.saveCurrentSlot()
				m.loadSlot(p.slot)
			}
			m.list.SetSelectedInstance(p.inst)
			return
		}
	}
}

// enterOverview switches to overview mode and seeds the cursor.
// Main-goroutine only.
func (m *home) enterOverview() {
	m.viewMode = viewOverview
	m.seedOverviewCursor()
}

// fleetPos is one selectable card in fleet display order.
type fleetPos struct{ slot, inst int }

// slotList resolves the live list for a slot index: the focused slot's
// list is hoisted onto m.list, every other slot keeps its own.
func (m *home) slotList(si int) *ui.List {
	if si == m.focusedSlot {
		return m.list
	}
	return m.slots[si].list
}

// fleetOrder flattens all loaded, non-collapsed slots into a single
// display-ordered, attention-sorted list of selectable positions,
// skipping Deleting instances. Mirrors overviewData's grouping so cursor
// motion matches what's on screen.
func (m *home) fleetOrder() []fleetPos {
	var out []fleetPos
	for _, si := range m.fleetSlotOrder() {
		if m.overview.IsCollapsed(m.slotGroupName(m.slots[si])) {
			continue
		}
		items := m.slotList(si).GetInstances()
		for _, idx := range ui.SortForOverview(items) {
			if items[idx].GetStatus() == session.Deleting {
				continue
			}
			out = append(out, fleetPos{slot: si, inst: idx})
		}
	}
	return out
}

// normalizeOverviewCursor re-anchors a cursor that no longer maps to a
// visible card (instance killed, group collapsed, slots changed) to the
// first selectable position in order. Reports whether it moved. The
// single staleness choke point: called from render (overviewData) and
// nav (moveCursor) so every event that invalidates the cursor is healed
// without per-event-site bookkeeping. No-op in classic mode, where the
// cursor is m.list's selection.
func (m *home) normalizeOverviewCursor(order []fleetPos) bool {
	for _, p := range order {
		if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
			return false
		}
	}
	if len(order) == 0 {
		return false
	}
	m.overviewCursor = overviewCursor{slot: order[0].slot, inst: order[0].inst}
	return true
}

// seedOverviewCursor points the cursor at the focused slot's selection,
// or the first selectable fleet position if that isn't selectable.
func (m *home) seedOverviewCursor() {
	m.overviewCursor = overviewCursor{slot: m.focusedSlot, inst: m.list.SelectedIdx()}
	m.normalizeOverviewCursor(m.fleetOrder())
}

// focusCursorSlot makes the overview cursor's slot the focused slot and
// selects the cursor's instance. No-op fast path when already focused.
// The single primitive every cursor-committing overview action routes
// through, so they reuse focus-mode intents unchanged. Main-goroutine
// only.
func (m *home) focusCursorSlot() {
	c := m.overviewCursor
	if c.slot < 0 || c.slot >= len(m.slots) {
		return
	}
	if c.slot != m.focusedSlot {
		m.saveCurrentSlot()
		m.loadSlot(c.slot)
	}
	if c.inst >= 0 && c.inst < len(m.list.GetInstances()) {
		m.list.SetSelectedInstance(c.inst)
	}
}

// peerSectionFor summarizes one non-focused slot's instance statuses
// into a PeerSection for refreshPeerSections (rail). Main-goroutine
// only.
func (m *home) peerSectionFor(slot workspaceSlot) ui.PeerSection {
	p := ui.PeerSection{Name: slot.wsCtx.Name}
	for _, inst := range slot.list.GetInstances() {
		st := inst.GetStatus()
		switch {
		case st == session.Prompting || inst.BellPending():
			p.Attention++
		case st == session.Running || st == session.Loading:
			p.Running++
		default:
			// Paused/Deleting/unstarted intentionally count as idle.
			p.Idle++
		}
	}
	return p
}

// overviewGroupName is the label for the active group in overview mode:
// the workspace name, or "global" in classic/global mode (activeCtx nil
// or unnamed) so the header never renders empty and `z` still has a
// stable collapse key.
func (m *home) overviewGroupName() string {
	if m.activeCtx != nil && m.activeCtx.Name != "" {
		return m.activeCtx.Name
	}
	return "global"
}

// fleetSlotOrder returns slot indices in overview display order: focused
// slot first, then the rest alphabetical by workspace name.
func (m *home) fleetSlotOrder() []int {
	order := make([]int, 0, len(m.slots))
	if m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		order = append(order, m.focusedSlot)
	}
	rest := make([]int, 0, len(m.slots))
	for i := range m.slots {
		if i != m.focusedSlot {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool {
		return m.slots[rest[a]].wsCtx.Name < m.slots[rest[b]].wsCtx.Name
	})
	return append(order, rest...)
}

// slotGroupName is the display name for a slot's overview group,
// falling back to "global" for the unnamed classic slot.
func (m *home) slotGroupName(slot workspaceSlot) string {
	if slot.wsCtx != nil && slot.wsCtx.Name != "" {
		return slot.wsCtx.Name
	}
	return "global"
}

// overviewGroupFor builds one OverviewGroup from a list's instances,
// deriving GroupEmpty/Order from the item count.
func overviewGroupFor(name string, items []*session.Instance) ui.OverviewGroup {
	g := ui.OverviewGroup{Name: name, Items: items, State: ui.GroupLoaded}
	if len(items) == 0 {
		g.State = ui.GroupEmpty
	} else {
		g.Order = ui.SortForOverview(items)
	}
	return g
}

// cursorFor translates the domain cursor position (an instance index)
// into the render cursor for group gi, defaulting to item 0 when the
// index is not found in the group's sorted order.
func cursorFor(gi, inst int, g ui.OverviewGroup) ui.OverviewCursor {
	for pos, idx := range g.Order {
		if idx == inst {
			return ui.OverviewCursor{Group: gi, Item: pos}
		}
	}
	return ui.OverviewCursor{Group: gi, Item: 0}
}

// overviewData assembles the multi-group overview over the open
// workspace slots: the focused slot first, then the remaining slots
// alphabetical by workspace name. It translates the domain cursor
// (slot,inst) into render coordinates (group,item). In classic/global
// mode (no slots) it renders the focused m.list as a single "global"
// group. Update-goroutine only.
func (m *home) overviewData() ui.OverviewData {
	cursor := ui.OverviewCursor{}

	// Classic/global mode: no slots loaded, render the focused m.list as
	// a single "global" group (matching the pre-fleet overview behavior).
	if len(m.slots) == 0 {
		items := m.list.GetInstances()
		g := overviewGroupFor(m.overviewGroupName(), items)
		cursor = cursorFor(0, m.list.SelectedIdx(), g)
		return ui.OverviewData{
			Groups:  []ui.OverviewGroup{g},
			Cursor:  cursor,
			Spinner: m.spinner.View(),
		}
	}

	// Heal a cursor invalidated since the last frame (instance killed,
	// group collapsed, slots changed) before translating it — a cursor
	// pointing into a collapsed group would window to a bogus offset.
	m.normalizeOverviewCursor(m.fleetOrder())

	slotOrder := m.fleetSlotOrder()
	groups := make([]ui.OverviewGroup, 0, len(slotOrder))
	for _, si := range slotOrder {
		slot := m.slots[si]
		g := overviewGroupFor(m.slotGroupName(slot), m.slotList(si).GetInstances())
		// Translate the domain cursor (slot,inst) → render cursor
		// (group,item) when this is the cursor's slot.
		if si == m.overviewCursor.slot {
			cursor = cursorFor(len(groups), m.overviewCursor.inst, g)
		}
		groups = append(groups, g)
	}

	return ui.OverviewData{Groups: groups, Cursor: cursor, Spinner: m.spinner.View()}
}

// moveCursor advances selection: list order in focus mode, fleet display
// order (across all groups) in overview mode. No wrap in the grid.
func (m *home) moveCursor(dir int) {
	if m.viewMode != viewOverview {
		if dir < 0 {
			m.list.Up()
		} else {
			m.list.Down()
		}
		return
	}
	// Classic/global mode (no slots): fleetSlotOrder is empty, so fleetOrder
	// yields nothing. Walk m.list in sorted overview order directly, mirroring
	// overviewData's classic fallback (which renders the cursor from m.list).
	if len(m.slots) == 0 {
		items := m.list.GetInstances()
		if len(items) == 0 {
			return
		}
		order := ui.SortForOverview(items)
		pos := 0
		for p, idx := range order {
			if idx == m.list.SelectedIdx() {
				pos = p
				break
			}
		}
		for i := 1; i <= len(order); i++ {
			np := pos + dir*i
			if np < 0 || np >= len(order) {
				return // no wrap in the grid
			}
			if items[order[np]].GetStatus() != session.Deleting {
				m.list.SetSelectedInstance(order[np])
				return
			}
		}
		return
	}
	order := m.fleetOrder()
	if len(order) == 0 {
		return
	}
	// A stale cursor re-anchors to the first visible card on this
	// keypress; stepping from a phantom position would skip a card.
	if m.normalizeOverviewCursor(order) {
		return
	}
	cur := 0
	for i, p := range order {
		if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
			cur = i
			break
		}
	}
	np := cur + dir
	if np < 0 || np >= len(order) {
		return
	}
	m.overviewCursor = overviewCursor{slot: order[np].slot, inst: order[np].inst}
}
