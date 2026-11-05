package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/feedback"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFeedback is an inbox with thread 1 (two notes from two sessions) and
// note 3, which no thread holds, with ws as feedback.workspace.
func testFeedback(t *testing.T, ws string) core.FeedbackView {
	t.Helper()
	in := feedback.Empty()
	at := time.Now().Add(-time.Hour)
	n1 := in.Add(feedback.Note{Kind: feedback.KindBug, Title: "--after refused", Text: "x", From: feedback.Session{ID: 1, Title: "builder"}, At: at})
	th, err := in.OpenThread(n1, "loom job run: --after refuses")
	require.NoError(t, err)
	require.NoError(t, in.Attach(in.Add(feedback.Note{Kind: feedback.KindBug, Title: "again", Text: "y", From: feedback.Session{ID: 2}, At: at}), th))
	in.Add(feedback.Note{Kind: feedback.KindMissing, Title: "a loose note", Text: "z", From: feedback.Session{ID: 3}, At: at})
	return core.FeedbackView{Inbox: in, Workspace: ws}
}

// feedbackHome is a home showing v in the feedback view, entered from
// focus.
func feedbackHome(t *testing.T, v core.FeedbackView) *home {
	t.Helper()
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	_ = m.drainCore() // the model's own first FeedbackChanged must not land after v
	_ = m.applyCoreEvent(core.FeedbackChanged{View: v})
	require.Len(t, m.feedback.Inbox.Notes, len(v.Inbox.Notes), "fixture: the event reached home.feedback")
	m.toggleFeedbackView()
	require.Equal(t, viewFeedback, m.viewMode)
	return m
}

// feedbackWatch records the feedback requests the TUI sends.
type feedbackWatch struct {
	core.Core
	fixes  []feedback.ThreadID
	closes []feedback.ThreadID
	reqs   []core.ReqID
}

func (w *feedbackWatch) FixThread(t feedback.ThreadID, ws string, req core.ReqID) {
	w.fixes = append(w.fixes, t)
	w.reqs = append(w.reqs, req)
}

func (w *feedbackWatch) CloseThread(t feedback.ThreadID, req core.ReqID) {
	w.closes = append(w.closes, t)
	w.reqs = append(w.reqs, req)
}

// watchFeedback puts a feedbackWatch between m and its model.
func watchFeedback(t *testing.T, m *home) *feedbackWatch {
	t.Helper()
	w := &feedbackWatch{Core: m.core}
	testStacks.Store(core.Core(w), stackOf(m))
	t.Cleanup(func() { testStacks.Delete(core.Core(w)) })
	m.core = w
	return w
}

func TestFeedbackView_EntersAndLeavesForTheModeItCameFrom(t *testing.T) {
	for name, from := range map[string]viewMode{"focus": viewFocus, "overview": viewOverview} {
		t.Run(name, func(t *testing.T) {
			m := newTestHome(t)
			m.viewMode = from
			m.toggleFeedbackView()
			require.Equal(t, viewFeedback, m.viewMode)
			m.toggleFeedbackView()
			assert.Equal(t, from, m.viewMode, "F returns to the mode it came from")

			m.toggleFeedbackView()
			pressWork(t, m, "esc")
			assert.Equal(t, from, m.viewMode, "esc too")
		})
	}
	for name, from := range map[string]viewMode{"workbench": viewWorkbench, "work view": viewWork} {
		m := newTestHome(t)
		m.viewMode = from
		m.toggleFeedbackView()
		assert.Equal(t, from, m.viewMode, "the %s has no way in", name)
	}
}

// F goes through the keymap (script/defaults.lua), in and out, and works
// offline: it only moves the TUI.
func TestFKey_TogglesTheFeedbackViewEvenOffline(t *testing.T) {
	m := newTestHome(t)
	m.link.state = linkWaiting
	for _, want := range []viewMode{viewFeedback, viewFocus} {
		_, cmd := handleStateDefaultKey(m, keyFor(t, "F"))
		require.NotNil(t, cmd)
		msg, ok := cmd().(scriptDoneMsg)
		require.True(t, ok)
		_ = m.handleScriptDone(msg)
		assert.Equal(t, want, m.viewMode)
	}
}

func TestFeedbackView_MovesAndExpands(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	r, ok := m.feedbackSelected()
	require.True(t, ok)
	assert.Equal(t, feedback.ThreadID(1), r.Thread)
	pressWork(t, m, "j")
	r, _ = m.feedbackSelected()
	assert.Equal(t, feedback.NoteID(3), r.Note)
	pressWork(t, m, "j")
	r, _ = m.feedbackSelected()
	assert.Equal(t, feedback.NoteID(3), r.Note, "it stops at the end")
	pressWork(t, m, "k")
	pressWork(t, m, "enter")
	assert.True(t, m.fb.expanded)
	assert.Contains(t, ansi.Strip(m.feedbackContent()), "n1 bug · builder")
}

func TestFeedbackView_CloseAsksThenSends(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	w := watchFeedback(t, m)
	pressWork(t, m, "c")
	require.Equal(t, stateConfirm, m.state)
	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	assert.Equal(t, []feedback.ThreadID{1}, w.closes)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: core.ErrRefused})
	assert.NotEmpty(t, m.errBox.String(), "a refusal reaches the user")
}

func TestFeedbackView_FixLeavesTheViewAndSends(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	w := watchFeedback(t, m)
	pressWork(t, m, "x")
	require.Equal(t, stateConfirm, m.state)
	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	assert.Equal(t, []feedback.ThreadID{1}, w.fixes)
	assert.Equal(t, viewFocus, m.viewMode, "the start's attach lands in focus")
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.Join(core.ErrRefused, errors.New("thread 1 is closed"))})
	assert.Contains(t, m.errBox.String(), "thread 1 is closed")
}

func TestFeedbackView_FixWithoutAWorkspaceSaysHowToSetOne(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, ""))
	w := watchFeedback(t, m)
	assert.Nil(t, pressWork(t, m, "x"))
	assert.Equal(t, stateDefault, m.state)
	assert.Contains(t, m.errBox.String(), "set feedback.workspace in the global config.json")
	assert.Empty(t, w.fixes)
}

func TestFeedbackView_NoteRowsRefuseFixAndClose(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	w := watchFeedback(t, m)
	pressWork(t, m, "j") // n3
	for _, key := range []string{"x", "c"} {
		m.errBox.Clear()
		assert.Nil(t, pressWork(t, m, key))
		assert.Equal(t, stateDefault, m.state)
		assert.Contains(t, m.errBox.String(), "n3 isn't in a thread yet", key)
	}
	assert.Empty(t, w.fixes)
	assert.Empty(t, w.closes)
}

func TestFeedbackView_OfflineRefusesItsRequests(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	w := watchFeedback(t, m)
	m.link.state = linkWaiting
	for _, key := range []string{"x", "c"} {
		m.errBox.Clear()
		assert.Nil(t, pressWork(t, m, key))
		assert.Equal(t, stateDefault, m.state, "%q opens nothing offline", key)
		assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: "+key+" needs it")
	}
	assert.Empty(t, w.fixes)
	assert.Empty(t, w.closes)
	pressWork(t, m, "j")
	r, _ := m.feedbackSelected()
	assert.Equal(t, feedback.NoteID(3), r.Note, "moving needs no daemon")
}

func TestStatusLine_ShowsOpenThreads(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	m.toggleFeedbackView()
	require.Equal(t, viewFocus, m.viewMode)
	assert.Contains(t, ansi.Strip(m.View().Content), "F feedback 1 · ")

	empty := newTestHome(t)
	empty.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.NotContains(t, ansi.Strip(empty.View().Content), "F feedback")
}

func TestFeedbackView_DrawsInPlaceOfTheLayout(t *testing.T) {
	m := feedbackHome(t, testFeedback(t, "loom"))
	out := ansi.Strip(m.View().Content)
	assert.Contains(t, out, "Feedback · 1 open")
	assert.Contains(t, out, "loom job run: --after refuses")
	assert.Contains(t, out, "F/esc back · j/k move · enter notes · x fix · c close · q quit")
}
