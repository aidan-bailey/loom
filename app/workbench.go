package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"
)

// workbenchKeyAllowed whitelists script-dispatched keys in workbench
// mode (same shape and caveats as overviewKeyAllowed — the gate keys
// on raw key strings, not actions). Session ops (D/r/R/p/s/m), attach
// (i/ctrl+a/ctrl+t/alt+a/alt+t), quick input (a/t), waiting-jump
// (]/[), workspace nav, and app chrome pass through; layout keys that
// address the hidden focus chrome (\, T, K/J list paging) do not.
var workbenchKeyAllowed = map[string]bool{
	"q": true, "?": true, "W": true, "S": true,
	"{": true, "}": true, "l": true, ";": true,
	"]": true, "[": true,
	"D": true, "r": true, "R": true, "p": true, "s": true, "m": true,
	"i": true, "ctrl+a": true, "ctrl+t": true,
	"alt+a": true, "alt+t": true,
	"a": true, "t": true,
}

// enterWorkbench flips to workbench mode for the selected instance.
// Returns nil (no-op) when nothing is selected.
func (m *home) enterWorkbench() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return nil
	}
	m.viewMode = viewWorkbench
	m.wbPrevTerminalHidden = m.splitPane.IsTerminalHidden()
	m.splitPane.SetTerminalHidden(true)
	m.workbench.SetSession(sel.Title, sel.GetWorktreePath())
	m.wbRatio = 0
	if m.appState != nil {
		if r, ok := m.appState.GetUIPrefs().WorkbenchRatios[sel.Title]; ok {
			m.wbRatio = r
		}
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), m.workbenchRefresh())
}

// cleanupWorkbench tears down workbench-mode state: flushes the ratio,
// cancels any in-progress markdown edit, restores the split terminal to
// its pre-entry setting, and lands in focus mode. Idempotent — no-op
// unless currently in workbench mode. Shared by exitWorkbench (explicit
// esc/tab) and the slot-switch choke points saveCurrentSlot/loadSlot:
// workbench mode does not survive an implicit workspace switch (v1
// design decision — a half-cleaned workbench is worse than landing in
// the target workspace's focus mode).
func (m *home) cleanupWorkbench() {
	if m.viewMode != viewWorkbench {
		return
	}
	m.flushWorkbenchRatio()
	// Reset after the flush: handleQuit also flushes, and a stale
	// non-zero ratio would otherwise be re-written later under whatever
	// instance happens to be selected at quit time.
	m.wbRatio = 0
	if m.workbench != nil {
		m.workbench.Markdown.CancelEdit()
	}
	m.splitPane.SetTerminalHidden(m.wbPrevTerminalHidden)
	m.viewMode = viewFocus
}

// exitWorkbench returns to `to` (viewFocus or viewOverview),
// restoring the split terminal and flushing the ratio.
func (m *home) exitWorkbench(to viewMode) tea.Cmd {
	m.cleanupWorkbench()
	m.viewMode = to
	if to == viewOverview {
		m.enterOverview()
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged())
}

// workbenchRatio is the effective agent share (default 0.5).
func (m *home) workbenchRatio() float64 {
	if m.wbRatio == 0 {
		return 0.5
	}
	return m.wbRatio
}

// flushWorkbenchRatio persists a non-default ratio for the current
// session title. Called on exit and from handleQuit.
func (m *home) flushWorkbenchRatio() {
	sel := m.list.GetSelectedInstance()
	if sel == nil || m.wbRatio == 0 {
		return
	}
	r := m.wbRatio
	m.mutateUIPrefs(func(p *config.UIPrefs) {
		if p.WorkbenchRatios == nil {
			p.WorkbenchRatios = map[string]float64{}
		}
		p.WorkbenchRatios[sel.Title] = r
	})
}

// handleWorkbenchKey processes workbench-local keys. Returns
// handled=false for keys that should fall through to the whitelist
// gate + script dispatch.
func handleWorkbenchKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	md := m.workbench.Markdown

	// Edit mode captures everything except save/cancel.
	if md.Editing() {
		switch msg.String() {
		case "ctrl+s":
			return m, m.saveWorkbenchMarkdown(false), true
		case "esc":
			if md.EditDirty() {
				return m, m.confirmDiscardEdit(), true
			}
			md.CancelEdit()
			return m, nil, true
		default:
			md.HandleEditKey(msg)
			return m, nil, true
		}
	}

	switch msg.String() {
	case "esc":
		return m, m.exitWorkbench(viewFocus), true
	case "tab":
		return m, m.exitWorkbench(viewOverview), true
	case "1":
		m.workbench.SetTab(ui.WbTabMarkdown)
		return m, nil, true
	case "2", "d":
		m.workbench.SetTab(ui.WbTabDiff)
		return m, nil, true
	case "3":
		m.workbench.SetTab(ui.WbTabFiles)
		return m, m.workbenchFilesCmd(), true
	case "4":
		m.workbench.SetTab(ui.WbTabTerminal)
		return m, nil, true
	case "e":
		if m.workbench.Tab() == ui.WbTabMarkdown {
			md.StartEdit()
		}
		return m, nil, true
	case "f":
		if !md.Following() {
			md.SetFollowing(true)
			return m, m.workbenchScanCmd(), true
		}
		return m, nil, true
	case "enter":
		if m.workbench.Tab() == ui.WbTabFiles {
			if path, ok := m.workbench.FileUnderCursor(); ok && ui.IsMarkdownPath(path) {
				md.SetFollowing(false)
				m.workbench.SetTab(ui.WbTabMarkdown)
				sel := m.list.GetSelectedInstance()
				if sel != nil {
					return m, loadMarkdownCmd(sel.Title, path, false), true
				}
			}
		}
		return m, nil, true
	case "j", "down":
		m.workbenchScrollDown()
		return m, nil, true
	case "k", "up":
		m.workbenchScrollUp()
		return m, nil, true
	case "g":
		md.ScrollTop()
		return m, nil, true
	case "G":
		md.ScrollBottom()
		return m, nil, true
	case "pgup":
		md.PageUp()
		return m, nil, true
	case "pgdown":
		md.PageDown()
		return m, nil, true
	case "ctrl+left", "ctrl+right":
		delta := 0.05
		if msg.String() == "ctrl+left" {
			delta = -0.05
		}
		r := m.workbenchRatio() + delta
		if r < 0.2 {
			r = 0.2
		}
		if r > 0.8 {
			r = 0.8
		}
		m.wbRatio = r
		return m, tea.RequestWindowSize, true
	case "n", "N":
		// Same rationale as overview: the create flow is a
		// focus-layout affordance — drop to focus, then dispatch.
		cmds := []tea.Cmd{m.exitWorkbench(viewFocus)}
		if cmd, handled := m.dispatchScript(msg.String()); handled {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true
	}
	return m, nil, false
}

// workbenchScrollUp/Down route j/k to the active tab.
func (m *home) workbenchScrollUp() {
	switch m.workbench.Tab() {
	case ui.WbTabDiff:
		m.workbench.Diff().ScrollUp()
	case ui.WbTabFiles:
		m.workbench.FilesUp()
	case ui.WbTabMarkdown:
		m.workbench.Markdown.ScrollUp()
	}
}

func (m *home) workbenchScrollDown() {
	switch m.workbench.Tab() {
	case ui.WbTabDiff:
		m.workbench.Diff().ScrollDown()
	case ui.WbTabFiles:
		m.workbench.FilesDown()
	case ui.WbTabMarkdown:
		m.workbench.Markdown.ScrollDown()
	}
}

// ---- stubs replaced by the next task (data flow) ----

func (m *home) workbenchRefresh() tea.Cmd                     { return nil }
func (m *home) workbenchScanCmd() tea.Cmd                     { return nil }
func (m *home) workbenchFilesCmd() tea.Cmd                    { return nil }
func (m *home) saveWorkbenchMarkdown(force bool) tea.Cmd      { return nil }
func (m *home) confirmDiscardEdit() tea.Cmd                   { return nil }
func loadMarkdownCmd(title, path string, follow bool) tea.Cmd { return nil }
