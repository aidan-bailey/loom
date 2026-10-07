package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
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
// the views that changed first (core.ViewsChanged, from core.Model.Sync),
// then each event in order (applyCoreEvent), and each job as a Cmd.
// Applying an event can call the model again, so it drains until nothing
// is left.
// Update runs it after every message. A caller whose later steps must see
// an event's effect (a workspace transition) runs it right after the model
// call. A bare test home without a model drains nothing.
func (m *home) drainCore() tea.Cmd {
	if m.core == nil {
		return nil
	}
	var cmds []tea.Cmd
	for out := m.core.Sync(); !out.Empty(); out = m.core.Sync() {
		for _, ev := range out.Events {
			cmds = append(cmds, m.applyCoreEvent(ev))
		}
		for _, job := range out.Jobs {
			cmds = append(cmds, coreCmd(job))
		}
	}
	return tea.Batch(cmds...)
}

// applyCoreEvent applies one model event to the view.
func (m *home) applyCoreEvent(ev core.Event) tea.Cmd {
	switch ev := ev.(type) {
	case core.ViewsChanged:
		if s := m.slotFor(ev.Workspace); s != nil {
			s.views = ev.Views
			m.pruneBells()
			if s == m.workspaceSlot {
				m.refreshSelection()
			}
		}
	case core.Notice:
		if ev.Err != nil {
			return m.handleError(ev.Err)
		}
		m.errBox.SetInfo(ev.Info)
	case core.InstancesChanged:
		cmd := m.instanceChanged()
		if ev.Relayout {
			return tea.Batch(tea.RequestWindowSize, cmd)
		}
		return cmd
	case core.ClientsStale:
		return m.prunePanes()
	case core.SessionLaunched:
		v, _ := m.viewByID(ev.ID)
		return m.replacePane(v)
	case core.Reactivated:
		v, _ := m.viewByID(ev.ID)
		m.ensurePane(v)
	case core.Started:
		return m.applyStarted(ev)
	case core.Recovered:
		return m.applyRecovered(ev)
	case core.StatusesChanged:
		m.updateTabBarStatuses()
	case core.Alive:
		for _, id := range ev.IDs {
			v, _ := m.viewByID(id)
			if v == nil || id == m.attachingID || m.panes.For(v).Attached() {
				continue
			}
			// The session exists but its attach client is not attached (a
			// reattach failed after full-screen attach returned, or the
			// client's pump hit EOF on a session that has since been
			// relaunched under the same name). Self-heal here: the same
			// shape as the workspace-terminal restart, at the client layer.
			log.For("app").Warn("pane.client_dead_repairing", "title", v.Title, "source", ev.Source)
			m.ensurePane(v)
		}
	case core.HealthChecked:
		// A user parked on the workbench's diff tab generates none of the
		// nav traffic that refreshes the diff in focus mode, so ride the
		// health tick: re-render from the just-updated diff stats so the
		// tab tracks the agent's work live.
		if m.viewMode == viewWorkbench && m.workbench != nil && m.workbench.Tab() == ui.WbTabDiff {
			if selected := m.list.GetSelectedInstance(); selected != nil {
				m.workbench.Diff().SetDiff(selected)
			}
		}
		return tickUpdateMetadataCmd
	case core.GitHubChanged:
		if p := m.issuePicker(); p != nil {
			p.SetRows(m.issueRows())
			p.SetStatus(m.issuePickerStatus())
		}
	case core.AccountsChanged:
		ui.SetShowAccounts(m.core.HasExtraAccounts())
		return m.refreshAccountViews()
	case core.Reply:
		return m.handleReply(ev)
	}
	return nil
}

// newSlotView builds the view of a loaded workspace: a rail reading its
// rows, a split pane and a workbench, sized when the terminal size is
// known. Its store is seeded from the model (seedViews), and its agent
// sessions get their pane clients (ensureSlotPanes).
func (m *home) newSlotView(ws *core.Workspace) *workspaceSlot {
	slot := &workspaceSlot{ws: ws}
	list := ui.NewList(&m.spinner, slotRows{m, slot})
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
	slot.list = list
	slot.splitPane = splitPane
	slot.workbench = ui.NewWorkbench(ui.NewDiffPane(), splitPane.Terminal())
	m.seedViews(slot)
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

// slotHolding returns the open slot whose rows hold id, or nil.
func (m *home) slotHolding(id core.InstanceID) *workspaceSlot {
	_, s := m.viewByID(id)
	return s
}
