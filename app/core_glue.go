package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// coreWakeMsg says the model did something unprompted (a job's result
// landed, its tick fired): the drain Update runs after every message
// (drainCore) applies what it produced. forwardWakes sends it.
type coreWakeMsg struct{}

// forwardWakes sends a coreWakeMsg for each of the model loop's wakes
// until they end (the loop stopped). It runs on a goroutine of its own:
// send blocks until Update takes the message, and the model's goroutine
// must never wait on the TUI.
func forwardWakes(wakes <-chan struct{}, send func(tea.Msg)) {
	for range wakes {
		send(coreWakeMsg{})
	}
}

// drainCore applies everything the model produced since the last drain:
// the views that changed first (core.ViewsChanged, from core.Core.Sync),
// then each event in order (applyCoreEvent). Applying an event can call
// the model again, so it drains until nothing is left. Update runs it
// after every message, a wake (coreWakeMsg) included. A caller whose later
// steps must see an event's effect (a workspace transition) runs it right
// after the model call. A bare test home without a model drains nothing.
func (m *home) drainCore() tea.Cmd {
	if m.core == nil {
		return nil
	}
	var cmds []tea.Cmd
	for events := m.core.Sync(); len(events) > 0; events = m.core.Sync() {
		for _, ev := range events {
			cmds = append(cmds, m.applyCoreEvent(ev))
		}
	}
	return tea.Batch(cmds...)
}

// publishSelection tells the model which row is selected, when that
// changed since the last Update: its health probe refreshes that session's
// full diff (core.Core.SetSelected). None, or a draft row, is 0.
func (m *home) publishSelection() {
	if m.core == nil || m.workspaceSlot == nil || m.list == nil {
		return
	}
	var id core.InstanceID
	if sel := m.list.GetSelectedInstance(); sel != nil {
		id = sel.ID
	}
	if id != m.sentSelected {
		m.core.SetSelected(id)
		m.sentSelected = id
	}
}

// applyCoreEvent applies one model event to the view.
func (m *home) applyCoreEvent(ev core.Event) tea.Cmd {
	switch ev := ev.(type) {
	case core.WorkspacesChanged:
		// Each open slot takes its workspace's newest view: a load's
		// recovery summary and storage flags, a pref or setting written
		// anywhere. Sync puts this event first, so the appliers below
		// read the new views.
		for _, v := range ev.Views {
			if s := m.slotFor(v.ID); s != nil {
				if s == m.workspaceSlot && s.info.ID == v.ID {
					m.applySettingsChange(s.info.Settings, v.Settings)
				}
				s.info = v
			}
		}
	case core.ViewsChanged:
		if s := m.slotFor(ev.WS); s != nil {
			s.views = ev.Views
			m.pruneBells()
			m.pruneLadder()
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
		// tab tracks the agent's work live. The model's loop re-arms its own
		// tick; the TUI's re-arms itself.
		if m.viewMode == viewWorkbench && m.workbench != nil && m.workbench.Tab() == ui.WbTabDiff {
			if selected := m.list.GetSelectedInstance(); selected != nil {
				m.workbench.Diff().SetDiff(selected)
			}
		}
	case core.GitHubChanged:
		if p := m.issuePicker(); p != nil {
			p.SetRows(m.issueRows())
			p.SetStatus(m.issuePickerStatus())
			// An open picker still wants its repository: renew the watch,
			// which would otherwise expire (ghWatchTTL) under a picker
			// left open, emptying it at the next poll.
			m.core.WatchGitHub(m.repoPath())
		}
	case core.AccountsChanged:
		ui.SetShowAccounts(m.core.HasExtraAccounts())
		return tea.Batch(m.refreshAccountViews(), m.showRunningAsAccount())
	case core.Reply:
		return m.handleReply(ev)
	}
	return nil
}

// newSlotView builds the view of a loaded workspace: a rail reading its
// rows, a split pane and a workbench, sized when the terminal size is
// known. Its store is seeded from the model (seedViews), and its agent
// sessions get their pane clients (ensureSlotPanes).
func (m *home) newSlotView(v core.WorkspaceView) *workspaceSlot {
	slot := &workspaceSlot{id: v.ID, info: v}
	list := ui.NewList(&m.spinner, slotRows{m, slot})
	list.SetPanes(m.panes)
	list.SetWorkspaceName(v.Name)
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

// slotFor returns the loaded view of the workspace id, or nil when no slot
// shows it (its tab was closed).
func (m *home) slotFor(id core.WorkspaceID) *workspaceSlot {
	if id == 0 {
		return nil
	}
	for _, s := range m.openSlots() {
		if s.id == id {
			return s
		}
	}
	return nil
}
