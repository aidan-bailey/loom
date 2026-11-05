package app

import (
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// feedbackScriptKeys are the keys of the feedback view that go to the
// script engine: F leaves the view, q quits and ? shows the help.
var feedbackScriptKeys = map[string]bool{"F": true, "q": true, "?": true}

// feedbackModelKeys are the feedback view's keys that send a request.
var feedbackModelKeys = map[string]bool{"x": true, "c": true}

// fbState is the feedback view's own state. The rows are home.feedback's,
// re-ranked on every use, so a drain that changes the inbox changes the
// view.
type fbState struct {
	// from is the mode F and esc return to: viewFocus or viewOverview.
	from viewMode
	// sel is the selected row's index, clamped against the rows on every
	// use; expanded shows its notes under it.
	sel      int
	expanded bool
}

// enterFeedbackView shows the feedback view over the mode it is entered
// from (focus or overview). The selection survives a visit.
func (m *home) enterFeedbackView() {
	m.fb = fbState{from: m.viewMode, sel: m.fb.sel}
	m.viewMode = viewFeedback
}

// leaveFeedbackView returns to the mode the view was entered from.
func (m *home) leaveFeedbackView() {
	if m.fb.from == viewOverview {
		m.enterOverview()
		return
	}
	m.viewMode = viewFocus
}

// toggleFeedbackView is the F key: into the view from focus or overview,
// out of it to where it came from. The workbench and the work view have no
// way in.
func (m *home) toggleFeedbackView() {
	switch m.viewMode {
	case viewFeedback:
		m.leaveFeedbackView()
	case viewFocus, viewOverview:
		m.enterFeedbackView()
	}
}

// feedbackSelected is the selected row, false when the view lists none.
func (m *home) feedbackSelected() (feedback.Row, bool) {
	rows := ui.FeedbackRows(m.feedback)
	if len(rows) == 0 {
		m.fb.sel = 0
		return feedback.Row{}, false
	}
	m.fb.sel = max(0, min(m.fb.sel, len(rows)-1))
	return rows[m.fb.sel], true
}

// feedbackContent is the view's content for View.
func (m *home) feedbackContent() string {
	m.feedbackSelected()
	return ui.RenderFeedback(m.lastWidth, m.workHeight(), ui.FeedbackData{View: m.feedback, Selected: m.fb.sel, Expanded: m.fb.expanded, Now: time.Now()})
}

// feedbackHint is the status line under the view.
func (m *home) feedbackHint() string {
	return "F/esc back · j/k move · enter notes · x fix · c close · q quit"
}

// feedbackBadge is the status line's prefix in focus and overview while
// threads are open, "" when none is.
func (m *home) feedbackBadge() string {
	if n := feedback.OpenThreads(m.feedback.Inbox); n > 0 {
		return fmt.Sprintf("F feedback %d · ", n)
	}
	return ""
}

// handleFeedbackKey is the feedback view's keys: j/k and the arrows move
// the selection, enter shows the selected row's notes, x starts a fix
// session for the selected thread, c closes it, esc returns to the mode the
// view came from, and F, q and ? go to the script engine. Every other key
// does nothing: it would act on the focus layout, which is not on screen.
func (m *home) handleFeedbackKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if feedbackModelKeys[key] && m.offline() {
		m.errBox.SetInfo(fmt.Sprintf("the loom daemon is %s: %s needs it", m.link.state.word(), key))
		return m, nil
	}
	switch key {
	case "esc":
		m.leaveFeedbackView()
		return m, m.instanceChanged()
	case "j", "down":
		m.fb.sel++
		m.feedbackSelected()
	case "k", "up":
		m.fb.sel = max(0, m.fb.sel-1)
	case "enter":
		m.fb.expanded = !m.fb.expanded
	case "x":
		return m, m.askFixThread()
	case "c":
		return m, m.askCloseThread()
	default:
		if feedbackScriptKeys[key] {
			cmd, _ := m.dispatchScript(key)
			return m, cmd
		}
	}
	return m, nil
}

// selectedThread is the selected row when it is a thread; a note no thread
// holds yet says so on the info line.
func (m *home) selectedThread() (feedback.Row, bool) {
	r, ok := m.feedbackSelected()
	switch {
	case !ok:
		return feedback.Row{}, false
	case r.Thread == 0:
		m.errBox.SetInfo(fmt.Sprintf("n%d isn't in a thread yet", r.Note))
		return feedback.Row{}, false
	}
	return r, true
}

// askFixThread is x: a confirm for starting the selected thread's fix
// session in feedback.workspace. The thread is taken now, by its ID; the
// model re-validates it. On yes the view leaves for focus first, so the
// start's inline attach lands there.
func (m *home) askFixThread() tea.Cmd {
	r, ok := m.selectedThread()
	if !ok {
		return nil
	}
	ws := m.feedback.Workspace
	if ws == "" {
		m.errBox.SetInfo(fmt.Sprintf("set feedback.workspace in the global config.json, or run: loom feedback fix %d -w <workspace>", r.Thread))
		return nil
	}
	t := r.Thread
	return m.confirmTask(fmt.Sprintf("Start fix session %s in %s?", feedback.FixTitle(t, r.Title), ws), overlay.ConfirmationTask{Sync: func() {
		m.leaveFeedbackView()
		m.core.FixThread(t, "", m.opReq("fix", fmt.Sprintf("thread %d", t)))
	}})
}

// askCloseThread is c: a confirm for closing the selected thread.
func (m *home) askCloseThread() tea.Cmd {
	r, ok := m.selectedThread()
	if !ok {
		return nil
	}
	t := r.Thread
	return m.confirmTask(fmt.Sprintf("Close thread %d (%s)?", t, ui.CleanText(r.Title)), overlay.ConfirmationTask{Sync: func() {
		m.core.CloseThread(t, m.opReq("close", fmt.Sprintf("thread %d", t)))
	}})
}

// dropFeedbackMouse swallows mouse input while the feedback view is up, as
// the work view does.
func (m *home) dropFeedbackMouse(msg tea.Msg) bool {
	if m.viewMode != viewFeedback {
		return false
	}
	switch msg.(type) {
	case tea.MouseWheelMsg, tea.MouseClickMsg, tea.MouseMotionMsg:
		return true
	case tea.MouseReleaseMsg:
		m.dragging = false
		m.splitPane.ClearSelections()
		return true
	}
	return false
}
