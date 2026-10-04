package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// coreResultMsg carries a core job's result back to Update, which hands it
// to the model (core.Model.Deliver).
type coreResultMsg struct{ msg any }

// coreCmd runs job as a tea.Cmd: off the Update goroutine, its result
// coming back as a coreResultMsg. nil for a nil job.
func coreCmd(job core.Job) tea.Cmd {
	if job == nil {
		return nil
	}
	return func() tea.Msg { return coreResultMsg{msg: job()} }
}

// drainCore applies everything the model produced since the last drain:
// each event in order (applyCoreEvent), and each job as a Cmd. Applying an
// event can call the model again, so it drains until nothing is left.
// Update runs it after every message. A caller whose later steps must see
// an event's effect (a workspace transition) runs it right after the model
// call. A bare test home without a model drains nothing.
func (m *home) drainCore() tea.Cmd {
	if m.core == nil {
		return nil
	}
	var cmds []tea.Cmd
	for out := m.core.Drain(); !out.Empty(); out = m.core.Drain() {
		for _, ev := range out.Events {
			cmds = append(cmds, m.applyCoreEvent(ev))
		}
		for _, job := range out.Jobs {
			cmds = append(cmds, coreCmd(job))
		}
	}
	return tea.Batch(cmds...)
}

// applyCoreEvent applies one model event to the view. Packages B and C add
// cases.
func (m *home) applyCoreEvent(ev core.Event) tea.Cmd {
	switch ev := ev.(type) {
	case core.Notice:
		if ev.Err != nil {
			return m.handleError(ev.Err)
		}
		m.errBox.SetInfo(ev.Info)
	}
	return nil
}

// newSlotView builds the view of a loaded workspace: a rail reading its
// instances, a split pane and a workbench, sized when the terminal size is
// known. Its agent sessions get their pane clients (ensureSlotPanes).
func (m *home) newSlotView(ws *core.Workspace) *workspaceSlot {
	list := ui.NewList(&m.spinner, ws)
	list.SetPanes(m.panes)
	list.SetWorkspaceName(ws.Name())
	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	splitPane.SetPanes(m.panes)
	// Pre-size components if terminal dimensions are known.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.topChromeHeight() - 2
		list.SetSize(listWidth, contentHeight)
		splitPane.SetSize(paneWidth, contentHeight)
	}
	slot := &workspaceSlot{
		ws:        ws,
		list:      list,
		splitPane: splitPane,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), splitPane.Terminal()),
	}
	m.ensureSlotPanes(slot)
	return slot
}

// slotFor returns the loaded view of ws, or nil when no slot shows it
// (its tab was closed).
func (m *home) slotFor(ws *core.Workspace) *workspaceSlot {
	if ws == nil {
		return nil
	}
	for _, s := range m.openSlots() {
		if s.ws == ws {
			return s
		}
	}
	return nil
}

// slotHolding returns the loaded slot whose workspace holds inst (by
// identity), or nil.
func (m *home) slotHolding(inst *session.Instance) *workspaceSlot {
	return m.slotFor(m.core.Holding(inst))
}
