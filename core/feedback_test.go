package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fbGrouperOff is a global config.json that turns the grouper off.
const fbGrouperOff = `{"feedback":{"grouper":{"enabled":false}}}`

// fbModel is a model whose feedback inbox booted under a fresh global dir
// (its config.json holding cfg, when not ""), serving workspace "a" (with a
// repository path) that holds a started Claude session "owner" on launch
// L1 whose mod said hello. It returns the global dir.
func fbModel(t *testing.T, cfg string) (*Model, string) {
	t.Helper()
	withMod(t)
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	if cfg != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(cfg), 0o600))
	}
	m := NewForTest(Options{Build: "0.15.0 abc123"})
	wsDir := t.TempDir()
	state := config.LoadStateFrom(wsDir)
	storage, err := session.NewStorage(state, wsDir)
	require.NoError(t, err)
	m.SetWorkspacesForTest(NewWorkspace(WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: "a", ConfigDir: wsDir, RepoPath: t.TempDir()},
		Storage: storage, Config: config.DefaultConfig(), State: state,
	}))
	modInst(t, m, "owner")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Poller: "p", Agents: true}))
	m.bootFeedback()
	m.Drain()
	return m, dir
}

// fbFile files a bug titled title from L1 at high effort and returns its
// Reply.
func fbFile(t *testing.T, m *Model, title string) Reply {
	t.Helper()
	m.FileFeedback(FeedbackNote{Kind: feedback.KindBug, Title: title, Text: title + ": what happened", Launch: "L1", Effort: "high"}, 5)
	rs := replies(Out{Events: m.out.Events})
	require.NotEmpty(t, rs)
	return rs[len(rs)-1]
}

// fbRunAll runs m's background jobs, and the ones their deliveries queue,
// until none is left, returning the events.
func fbRunAll(t *testing.T, m *Model) []Event {
	t.Helper()
	var events []Event
	for range 20 {
		out := m.Drain()
		events = append(events, out.Events...)
		if len(out.Jobs) == 0 {
			return events
		}
		for _, job := range out.Jobs {
			m.Deliver(job())
		}
	}
	t.Fatal("the jobs never settled")
	return nil
}

// fbGrouper points m's grouper at a fake claude that answers answer (the
// structured output's JSON), or exits 1 when answer is "".
func fbGrouper(t *testing.T, m *Model, answer string) {
	t.Helper()
	body := "echo boom >&2; exit 1"
	if answer != "" {
		body = `printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"x","structured_output":` + answer + `}'`
	}
	program := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\ncat > /dev/null\n"+body+"\n"), 0o755))
	m.SetFeedbackGrouperForTest(program, 5*time.Second)
}

func TestFileFeedback_StampsTheSessionAndSaves(t *testing.T) {
	m, dir := fbModel(t, fbGrouperOff)
	r := fbFile(t, m, "loom job run refused --after")
	require.NoError(t, r.Err)
	assert.Equal(t, feedback.NoteID(1), r.Note)

	n, ok := m.Feedback().Inbox.Note(1)
	require.True(t, ok)
	owner := m.workspaces[0].insts[0]
	assert.Equal(t, uint64(m.idOf(owner)), n.From.ID)
	assert.Equal(t, "owner", n.From.Title)
	assert.Equal(t, "a", n.From.Workspace)
	assert.Equal(t, owner.GetBranch(), n.From.Branch)
	assert.Equal(t, "high", n.From.Effort)
	assert.Equal(t, "0.15.0 abc123", n.Build)
	assert.Equal(t, feedback.Pending, n.Grouping)
	assert.Equal(t, "turned off (feedback.grouper.enabled)", m.Feedback().Grouper)

	disk, err := feedback.Load(filepath.Join(dir, feedbackDirName))
	require.NoError(t, err)
	assert.Len(t, disk.Notes, 1, "saved before the reply")
}

func TestFileFeedback_Refusals(t *testing.T) {
	m, _ := fbModel(t, fbGrouperOff)
	for name, note := range map[string]FeedbackNote{
		"no launch":      {Kind: feedback.KindBug, Title: "t", Text: "x"},
		"unknown launch": {Kind: feedback.KindBug, Title: "t", Text: "x", Launch: "L9"},
		"a bad kind":     {Kind: "wrong", Title: "t", Text: "x", Launch: "L1"},
		"an empty title": {Kind: feedback.KindBug, Title: " ", Text: "x", Launch: "L1"},
	} {
		m.FileFeedback(note, 3)
		rs := replies(m.Drain())
		require.Len(t, rs, 1, name)
		assert.ErrorIs(t, rs[0].Err, ErrRefused, name)
	}
	assert.Empty(t, m.Feedback().Inbox.Notes)
}

func TestFileFeedback_ABadEffortIsDropped(t *testing.T) {
	m, _ := fbModel(t, fbGrouperOff)
	for _, effort := range []string{"default", "turbo", "high; rm -rf"} {
		m.FileFeedback(FeedbackNote{Kind: feedback.KindBug, Title: effort, Text: "x", Launch: "L1", Effort: effort}, 3)
	}
	m.Drain()
	for _, n := range m.Feedback().Inbox.Notes {
		assert.Empty(t, n.From.Effort, n.Title)
	}
}

func TestFileFeedback_AFailedSaveKeepsNothing(t *testing.T) {
	m, dir := fbModel(t, fbGrouperOff)
	fbDir := filepath.Join(dir, feedbackDirName)
	require.NoError(t, os.RemoveAll(fbDir))
	require.NoError(t, os.WriteFile(fbDir, []byte("a file in the folder's place"), 0o600))
	r := fbFile(t, m, "t")
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Empty(t, m.Feedback().Inbox.Notes, "rolled back")
}

func TestFileFeedback_TheSubagentAndModelComeFromTheReportedCall(t *testing.T) {
	m, _ := fbModel(t, fbGrouperOff)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: `loom feedback add --kind bug --title "x" --text "y"`, Model: "claude-opus-5-5"})
	fbFile(t, m, "one call")
	n, _ := m.Feedback().Inbox.Note(1)
	assert.Equal(t, "a1", n.From.Agent)
	assert.Equal(t, "claude-opus-5-5", n.From.Model)

	reportCall(t, m, ModCall{ID: "c2", Agent: "a2", Command: `loom feedback add --kind bug --title "z" --text "w"`, Model: "claude-opus-5-5"})
	fbFile(t, m, "two calls")
	n, _ = m.Feedback().Inbox.Note(2)
	assert.Empty(t, n.From.Agent, "two calls in flight: neither")
	assert.Empty(t, n.From.Model)
}

func TestFeedback_AnInboxThatFailedToLoadIsLeftAndRefuses(t *testing.T) {
	withMod(t)
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	fbDir := filepath.Join(dir, feedbackDirName)
	require.NoError(t, os.MkdirAll(fbDir, 0o700))
	body := []byte(`{"version": 2}`)
	require.NoError(t, os.WriteFile(filepath.Join(fbDir, feedback.StoreFile), body, 0o600))
	m := NewForTest(Options{})
	modInst(t, m, "owner")
	m.bootFeedback()
	notices, _ := noticesAndStarts(m.Drain().Events)
	require.Len(t, notices, 1)
	assert.ErrorContains(t, notices[0].Err, "notes are refused, and feedback.json is left as it is")
	assert.NotEmpty(t, m.Feedback().Err)

	m.FileFeedback(FeedbackNote{Kind: feedback.KindBug, Title: "t", Text: "x", Launch: "L1"}, 2)
	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	assert.ErrorIs(t, rs[0].Err, ErrRefused)
	after, _ := os.ReadFile(filepath.Join(fbDir, feedback.StoreFile))
	assert.Equal(t, body, after)
}

func TestGroup_OpensThenAttaches(t *testing.T) {
	m, dir := fbModel(t, "")
	fbGrouper(t, m, `{"thread":0,"title":"loom job: --after","why":"new"}`)
	fbFile(t, m, "first")
	fbRunAll(t, m)
	in := m.Feedback().Inbox
	require.Len(t, in.Threads, 1)
	assert.Equal(t, "loom job: --after", in.Threads[0].Title)

	fbGrouper(t, m, `{"thread":1,"title":"","why":"same"}`)
	fbFile(t, m, "second")
	fbRunAll(t, m)
	in = m.Feedback().Inbox
	require.Len(t, in.Threads, 1)
	assert.Equal(t, []feedback.NoteID{1, 2}, in.Threads[0].Notes)

	log, err := os.ReadFile(filepath.Join(dir, feedbackDirName, feedback.CallLogFile))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(log), "\n"), "one line per call")
	disk, _ := feedback.Load(filepath.Join(dir, feedbackDirName))
	assert.Len(t, disk.Threads, 1, "saved")
}

func TestGroup_AClosedThreadAnswerOpensANewThread(t *testing.T) {
	m, _ := fbModel(t, "")
	fbGrouper(t, m, `{"thread":0,"title":"t","why":"new"}`)
	fbFile(t, m, "first")
	fbRunAll(t, m)

	fbGrouper(t, m, `{"thread":1,"title":"","why":"same"}`)
	fbFile(t, m, "second") // its call is queued, not run
	m.CloseThread(1, 6)
	fbRunAll(t, m)
	in := m.Feedback().Inbox
	require.Len(t, in.Threads, 2, "the thread closed between the request and the answer")
	n, _ := in.Note(2)
	assert.Equal(t, feedback.ThreadID(2), n.Thread)
}

func TestGroup_FailureLeavesUngroupedAndRetries(t *testing.T) {
	m, _ := fbModel(t, "")
	fbGrouper(t, m, "")
	before := time.Now()
	fbFile(t, m, "first")
	fbRunAll(t, m)
	n, _ := m.Feedback().Inbox.Note(1)
	assert.Equal(t, feedback.Ungrouped, n.Grouping)
	assert.NotEmpty(t, n.GroupErr)
	assert.WithinDuration(t, before.Add(grouperRetryAfter), n.RetryAt, 5*time.Second)
	due, ok := m.Feedback().Inbox.Due(n.RetryAt)
	require.True(t, ok)
	assert.Equal(t, n.ID, due.ID)
}

func TestGroup_ThreeFailuresPauseWithANotice(t *testing.T) {
	m, dir := fbModel(t, "")
	fbGrouper(t, m, "")
	var events []Event
	for _, title := range []string{"a", "b", "c", "d"} {
		fbFile(t, m, title)
		events = append(events, fbRunAll(t, m)...)
	}
	notices, _ := noticesAndStarts(events)
	require.Len(t, notices, 1, "one notice for the pause")
	assert.ErrorContains(t, notices[0].Err, "feedback grouper: paused after 3 failed calls in a row")
	assert.Contains(t, m.Feedback().Grouper, "paused after 3 failed calls in a row")
	n, _ := m.Feedback().Inbox.Note(4)
	assert.Equal(t, feedback.Pending, n.Grouping, "the fourth is never offered")

	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"feedback":{"grouper":{"model":"sonnet"}}}`), 0o600))
	m.refreshFeedbackSettings(time.Now(), true)
	assert.Empty(t, m.Feedback().Grouper, "a settings change ends the pause")
}

func TestGroup_OffLeavesNotesPending(t *testing.T) {
	m, _ := fbModel(t, fbGrouperOff)
	fbGrouper(t, m, `{"thread":0,"title":"t","why":"new"}`)
	fbFile(t, m, "first")
	fbRunAll(t, m)
	n, _ := m.Feedback().Inbox.Note(1)
	assert.Equal(t, feedback.Pending, n.Grouping)
	assert.Empty(t, m.Feedback().Inbox.Threads)
}

// fbThread files a note and groups it into thread 1.
func fbThread(t *testing.T, m *Model, title string) {
	t.Helper()
	fbGrouper(t, m, `{"thread":0,"title":"`+title+`","why":"new"}`)
	fbFile(t, m, title)
	fbRunAll(t, m)
	require.Len(t, m.Feedback().Inbox.Threads, 1)
}

func TestFixThread_CreatesInTheWorkspaceAndMarksFixing(t *testing.T) {
	m, _ := fbModel(t, "")
	fbThread(t, m, "loom job run refuses --after")
	m.FixThread(1, "a", 7)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	require.NotZero(t, rs[0].ID)
	v, ok := m.View(rs[0].ID)
	require.True(t, ok)
	assert.Equal(t, feedback.FixTitle(1, "loom job run refuses --after"), v.Title)
	inst := m.InstanceForTest(rs[0].ID)
	require.NotNil(t, inst)
	assert.Contains(t, inst.Prompt(), "loom job run refuses --after: what happened")
	assert.Contains(t, inst.Prompt(), "First check whether the base branch already fixes this")
	assert.Len(t, out.Jobs, 1, "the start job")

	th, _ := m.Feedback().Inbox.Thread(1)
	assert.Equal(t, feedback.Fixing, th.Status)
	assert.Equal(t, feedback.Fix{ID: uint64(rs[0].ID), Title: v.Title, Workspace: "a", At: th.Fix.At}, th.Fix)
}

func TestFixThread_Refusals(t *testing.T) {
	m, _ := fbModel(t, "")
	fbThread(t, m, "t")
	refused := func(name string, thread feedback.ThreadID, ws string) {
		t.Helper()
		m.FixThread(thread, ws, 8)
		rs := replies(m.Drain())
		require.Len(t, rs, 1, name)
		assert.ErrorIs(t, rs[0].Err, ErrRefused, name)
	}
	refused("an unknown thread", 9, "a")
	refused("no workspace and no setting", 1, "")
	refused("an unserved workspace", 1, "nope")

	m.FixThread(1, "a", 7)
	m.Drain()
	refused("a live fix session", 1, "a")

	m.CloseThread(1, 6)
	m.Drain()
	refused("a closed thread", 1, "a")
}

func TestFixThread_TheSettingNamesTheWorkspace(t *testing.T) {
	m, _ := fbModel(t, `{"feedback":{"workspace":"a"}}`)
	fbThread(t, m, "t")
	m.FixThread(1, "", 7)
	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	assert.NoError(t, rs[0].Err)
}

func TestFixThread_AGoneFixSessionCanBeFixedAgain(t *testing.T) {
	m, _ := fbModel(t, "")
	fbThread(t, m, "t")
	m.FixThread(1, "a", 7)
	first := replies(m.Drain())[0].ID
	m.removeEverywhere(m.InstanceForTest(first))
	m.Drain()

	m.FixThread(1, "a", 8)
	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	th, _ := m.Feedback().Inbox.Thread(1)
	assert.Equal(t, uint64(rs[0].ID), th.Fix.ID, "the new session replaces the gone one")
}

func TestFixTitle_FitsCoresLimit(t *testing.T) {
	assert.Equal(t, MaxTitleWidth, feedback.FixTitleWidth)
	assert.NoError(t, CheckNewTitle(feedback.FixTitle(99999, strings.Repeat("a", 80)), WorkspaceView{}))
}

func TestCloseThread(t *testing.T) {
	m, dir := fbModel(t, "")
	fbThread(t, m, "t")
	m.CloseThread(1, 6)
	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	disk, _ := feedback.Load(filepath.Join(dir, feedbackDirName))
	assert.Equal(t, feedback.Closed, disk.Threads[0].Status, "saved")

	for name, thread := range map[string]feedback.ThreadID{"again": 1, "unknown": 4} {
		m.CloseThread(thread, 6)
		rs := replies(m.Drain())
		require.Len(t, rs, 1, name)
		assert.ErrorIs(t, rs[0].Err, ErrRefused, name)
	}
}

func TestFeedback_PublishesOnChange(t *testing.T) {
	m, _ := fbModel(t, fbGrouperOff)
	m.publishState()
	assert.Empty(t, m.publishFeedback(), "nothing changed")
	fbFile(t, m, "t")
	evs := m.publishFeedback()
	require.Len(t, evs, 1)
	fc := evs[0].(FeedbackChanged)
	assert.Len(t, fc.View.Inbox.Notes, 1)
	fc.View.Inbox.Notes[0].Title = "edited"
	n, _ := m.Feedback().Inbox.Note(1)
	assert.Equal(t, "t", n.Title, "the event holds a copy")
}
