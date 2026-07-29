package app

import (
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/review"
	"github.com/aidan-bailey/loom/session/files"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	reviewui "github.com/aidan-bailey/loom/ui/review"
)

// workbenchKeyAllowed whitelists script-dispatched keys in workbench
// mode (same shape and caveats as overviewKeyAllowed — the gate keys
// on raw key strings, not actions). Session ops (D/r/R/p/s/m), attach
// (i/ctrl+a/alt+a), quick input (a), waiting-jump (]/[), workspace
// nav, and app chrome pass through; layout keys that address the
// hidden focus chrome (\, T, K/J list paging) do not. The
// terminal-addressed keys (t/ctrl+t/alt+t) are intercepted by
// handleWorkbenchKey to switch the terminal tab first, then
// dispatched from there — they are deliberately absent here.
var workbenchKeyAllowed = map[string]bool{
	"q": true, "?": true, "W": true, "S": true,
	"{": true, "}": true, "l": true, ";": true,
	"]": true, "[": true,
	"D": true, "r": true, "R": true, "p": true, "s": true, "m": true,
	"i": true, "ctrl+a": true,
	"alt+a": true,
	"a":     true,
}

// enterWorkbench flips to workbench mode for the selected instance.
// Returns nil (no-op) when nothing is selected.
func (m *home) enterWorkbench() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return nil
	}
	// The workbench has its own diff tab; a lingering focus-mode diff
	// overlay would otherwise reappear over the split on exit.
	if m.splitPane.IsDiffVisible() {
		m.splitPane.ToggleDiff()
	}
	m.viewMode = viewWorkbench
	m.wbPrevTerminalHidden = m.splitPane.IsTerminalHidden()
	m.splitPane.SetTerminalHidden(true)
	m.workbench.SetSession(sel.Title, sel.GetWorktreePath())
	// SetSession only clears the workbench's interface field on an actual
	// title change; nil-ing unconditionally keeps the wbReview invariant
	// trivially true (cleanup runs on every exit path anyway, so a stale
	// same-title pane cannot survive to here).
	m.wbReview = nil
	m.wbRatio = 0
	if m.appState != nil {
		if r, ok := m.appState.GetUIPrefs().WorkbenchRatios[sel.Title]; ok {
			m.wbRatio = r
		}
	}
	// Eagerly compute the left-column width so a wheel tick or `4` press
	// arriving before the RequestWindowSize round-trip routes on the
	// correct split boundary (updateHandleWindowSizeEvent recomputes it
	// once the round-trip lands).
	if m.lastWidth > 0 {
		m.wbLeftWidth = int(float64(m.lastWidth) * m.workbenchRatio())
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
		m.wbReview = nil
		m.workbench.SetReview(nil)
		m.workbench.Markdown.SetFollowing(true)
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
		// Persistence parity with the focus-mode overview toggle
		// (scriptHost.ToggleOverview): the hop must survive a restart.
		m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "overview" })
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

// flushWorkbenchRatio persists a non-default ratio for the session the
// workbench is actually showing — not the list selection, which may
// already have moved by flush time (slot switches and quit teardown
// reorder selection vs. cleanup). Called on exit and from handleQuit.
func (m *home) flushWorkbenchRatio() {
	if m.workbench == nil || m.wbRatio == 0 {
		return
	}
	title := m.workbench.SessionTitle()
	if title == "" {
		return
	}
	r := m.wbRatio
	m.mutateUIPrefs(func(p *config.UIPrefs) {
		if p.WorkbenchRatios == nil {
			p.WorkbenchRatios = map[string]float64{}
		}
		p.WorkbenchRatios[title] = r
	})
}

// handleWorkbenchKey processes workbench-local keys. Returns
// handled=false for keys that should fall through to the whitelist
// gate + script dispatch.
func handleWorkbenchKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	md := m.workbench.Markdown

	// Review tab owns its keys. The pane declines idle-esc
	// (handled=false) so workbench exit still works; q inside the pane
	// exits the review back to the markdown tab.
	if m.workbench.Tab() == ui.WbTabReview && m.wbReview != nil {
		rv := m.wbReview
		if msg.String() == "S" && !rv.Busy() {
			return m, m.sendReviewCmd(), true
		}
		if cmd, handled, exit := rv.HandleKey(msg); handled {
			if exit {
				return m, tea.Batch(cmd, m.closeReview()), true
			}
			return m, cmd, true
		}
		// fall through: idle esc → workbench exit below.
	}

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
	case "ctrl+t", "t", "alt+t":
		// Terminal-addressed actions (inline attach, quick input,
		// full-screen attach) act on the shared terminal pane — bring
		// its tab into view before dispatching so the user sees what
		// they are typing into.
		m.workbench.SetTab(ui.WbTabTerminal)
		cmd, _ := m.dispatchScript(msg.String())
		return m, cmd, true
	case "e":
		if m.workbench.Tab() == ui.WbTabMarkdown {
			md.StartEdit()
		}
		return m, nil, true
	case "c":
		if m.workbench.Tab() == ui.WbTabMarkdown && !md.Editing() && md.Path() != "" {
			return m, m.openDocReview(md.Path()), true
		}
		return m, nil, true
	case "5":
		if m.wbReview != nil {
			m.workbench.SetTab(ui.WbTabReview)
		} else {
			m.errBox.SetInfo("no active review — press c on a markdown doc to start one")
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
		switch m.workbench.Tab() {
		case ui.WbTabDiff:
			m.workbench.Diff().GotoTop()
		case ui.WbTabFiles:
			m.workbench.FilesTop()
		case ui.WbTabMarkdown:
			md.ScrollTop()
		}
		return m, nil, true
	case "G":
		switch m.workbench.Tab() {
		case ui.WbTabDiff:
			m.workbench.Diff().GotoBottom()
		case ui.WbTabFiles:
			m.workbench.FilesBottom()
		case ui.WbTabMarkdown:
			md.ScrollBottom()
		}
		return m, nil, true
	case "pgup":
		switch m.workbench.Tab() {
		case ui.WbTabDiff:
			m.workbench.Diff().PageUp()
		case ui.WbTabMarkdown:
			md.PageUp()
		}
		return m, nil, true
	case "pgdown":
		switch m.workbench.Tab() {
		case ui.WbTabDiff:
			m.workbench.Diff().PageDown()
		case ui.WbTabMarkdown:
			md.PageDown()
		}
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

// openDocReview freezes the markdown pane (same contract as edit mode:
// follow-mode pauses so line anchors can't rot under a live agent) and
// opens the review tab on docPath.
func (m *home) openDocReview(docPath string) tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil || sel.Paused() {
		return nil
	}
	root := sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	m.workbench.Markdown.SetFollowing(false)
	p := reviewui.NewDocPane(sel.Title, root, docPath)
	m.wbReview = p
	m.workbench.SetReview(p)
	m.workbench.SetTab(ui.WbTabReview)
	return p.LoadCmd()
}

// closeReview leaves the review tab back to markdown and resumes
// follow mode (the freeze counterpart of openDocReview).
func (m *home) closeReview() tea.Cmd {
	m.wbReview = nil
	m.workbench.SetReview(nil)
	m.workbench.SetTab(ui.WbTabMarkdown)
	m.workbench.Markdown.SetFollowing(true)
	return m.workbenchScanCmd()
}

// sendReviewCmd composes the review comments into a prompt and, after
// confirmation, sends it to the session's agent pane. The prompt is
// composed at press time — the confirm overlay's Sync step runs on the
// main goroutine (SendPrompt has its own locking, same precedent as
// the quick-input bar).
func (m *home) sendReviewCmd() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	rv := m.wbReview
	if sel == nil || rv == nil {
		return nil
	}
	if sel.Paused() || !sel.TmuxAlive() {
		m.errBox.SetInfo("agent is not running — resume the session first")
		return nil
	}
	prompt := review.ComposePrompt(rv.Root(), rv.States())
	if prompt == "" {
		m.errBox.SetInfo("no review comments to send")
		return nil
	}
	title := sel.Title
	msg := fmt.Sprintf("Send %d review comment(s) to %s?", rv.CommentCount(), title)
	return m.confirmTask(msg, overlay.ConfirmationTask{
		Sync: func() {
			if err := sel.SendPrompt(prompt); err != nil {
				m.errBox.SetError(err)
			}
		},
	})
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

// wbScanMsg reports the follow scan's most-recent markdown file.
// title pins the result to the session it was scanned for, so a
// selection change between dispatch and delivery drops it stale.
type wbScanMsg struct {
	title string
	path  string
	mtime time.Time
	err   error
}

// wbLoadMsg delivers a loaded document (follow reloads and manual
// files-tab opens both land here; follow records which).
type wbLoadMsg struct {
	title  string
	path   string
	raw    string
	mtime  time.Time
	follow bool
	err    error
}

// wbFilesMsg delivers the files-tab listing.
type wbFilesMsg struct {
	title string
	root  string
	paths []string
	err   error
}

// workbenchScanCmd builds the follow scan for the selected instance:
// resolve the worktree's most recently modified markdown file off the
// Update goroutine and report it via wbScanMsg. Recoverable instances
// are intentionally scannable: they report Started()=true with a real
// on-disk worktree, and the scan is read-only and harmless.
func (m *home) workbenchScanCmd() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil || !sel.Started() || sel.Paused() {
		return nil
	}
	title, root := sel.Title, sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	return func() tea.Msg {
		path, mtime, ok, err := files.MostRecentMarkdown(root)
		if err != nil || !ok {
			return wbScanMsg{title: title, err: err}
		}
		return wbScanMsg{title: title, path: path, mtime: mtime}
	}
}

// loadMarkdownCmd reads path off the Update goroutine and delivers it
// via wbLoadMsg (stat first so the mtime rides along as the
// save-conflict baseline).
func loadMarkdownCmd(title, path string, follow bool) tea.Cmd {
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return wbLoadMsg{title: title, path: path, follow: follow, err: err}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return wbLoadMsg{title: title, path: path, follow: follow, err: err}
		}
		return wbLoadMsg{title: title, path: path, raw: string(raw),
			mtime: info.ModTime(), follow: follow}
	}
}

// workbenchFilesCmd lists the selected instance's worktree for the
// files tab.
func (m *home) workbenchFilesCmd() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return nil
	}
	title, root := sel.Title, sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := files.List(root)
		if err != nil {
			return wbFilesMsg{title: title, root: root, err: err}
		}
		return wbFilesMsg{title: title, root: root, paths: res.Paths}
	}
}

// workbenchRefresh kicks the initial scan + files load on entry.
func (m *home) workbenchRefresh() tea.Cmd {
	return tea.Batch(m.workbenchScanCmd(), m.workbenchFilesCmd())
}

// wbCurrentTitle guards stale message delivery.
func (m *home) wbCurrentTitle() (string, bool) {
	sel := m.list.GetSelectedInstance()
	if m.viewMode != viewWorkbench || sel == nil {
		return "", false
	}
	return sel.Title, true
}

// wbSaveMsg reports a save attempt. conflict=true means the file
// changed on disk after load and force was false — nothing written;
// content rides along so the confirm path can retry with force.
type wbSaveMsg struct {
	title    string
	path     string
	content  string
	mtime    time.Time
	conflict bool
	err      error
}

// saveMarkdownCmd writes content to path off the Update goroutine,
// guarding against concurrent disk edits: unless force is set, a disk
// mtime newer than loadedMtime reports conflict instead of writing.
func saveMarkdownCmd(title, path, content string, loadedMtime time.Time, force bool) tea.Cmd {
	return func() tea.Msg {
		if !force {
			if info, err := os.Stat(path); err == nil && info.ModTime().After(loadedMtime) {
				return wbSaveMsg{title: title, path: path, content: content, conflict: true}
			}
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return wbSaveMsg{title: title, path: path, err: err}
		}
		info, err := os.Stat(path)
		if err != nil {
			return wbSaveMsg{title: title, path: path, err: err}
		}
		return wbSaveMsg{title: title, path: path, content: content, mtime: info.ModTime()}
	}
}

// saveWorkbenchMarkdown dispatches a save of the open editor buffer.
// Gated on Editing() at read time — EditValue returns the stale
// discarded buffer after a cancel (pane-side contract).
func (m *home) saveWorkbenchMarkdown(force bool) tea.Cmd {
	md := m.workbench.Markdown
	sel := m.list.GetSelectedInstance()
	if sel == nil || !md.Editing() || md.Path() == "" {
		return nil
	}
	return saveMarkdownCmd(sel.Title, md.Path(), md.EditValue(), md.Mtime(), force)
}

// confirmDiscardEdit asks before dropping unsaved editor changes.
// CancelEdit runs as the Sync step: on the main goroutine, per the
// no-model-mutation-in-Cmd rule.
func (m *home) confirmDiscardEdit() tea.Cmd {
	return m.confirmTask("Discard unsaved changes?", overlay.ConfirmationTask{
		Sync: func() { m.workbench.Markdown.CancelEdit() },
	})
}
