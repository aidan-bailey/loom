package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginWatch_NoClientCanSendIt(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "hi", Origin: OriginWatch}, 9)

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Contains(t, rs[0].Err.Error(), "is no sender's origin")
	assert.Empty(t, m.mods, "nothing reaches the mod")
}

func TestOriginWatch_NeedsALiveMod(t *testing.T) {
	assert.True(t, OriginWatch.needsMod())
}

func TestOriginWatch_ANudgeGoesToALiveModAsItsOwnText(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))

	m.enqueue(inst, &modMessage{text: "merge origin/main", origin: OriginWatch})

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "merge origin/main"}, msgs[0], "no From line, and not the person's words")
}

func TestOriginWatch_ANudgeTheModNeverTookIsReportedAndNotPasted(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("offered=%v", offered), func(t *testing.T) {
			withMod(t)
			m := NewForTest(Options{})
			inst := modInst(t, m, "x")
			var why string
			m.enqueue(inst, &modMessage{text: "nudge", origin: OriginWatch, onUndelivered: func(w string) { why = w }})
			if offered {
				require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
			}

			m.ModsStop()

			if offered {
				assert.Contains(t, why, "may or may not have reached Claude")
			} else {
				assert.Equal(t, modsStoppedWhy, why)
			}
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

func TestOriginWatch_AProgramWithoutAModIsReportedAtOnce(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := startedInst(t, "x", "aider")
	hold(m, inst)
	var why string

	m.enqueue(inst, &modMessage{text: "nudge", origin: OriginWatch, onUndelivered: func(w string) { why = w }})

	assert.Equal(t, "it runs aider, not Claude", why)
	assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
}

// wactModel is a model serving one workspace "a" with the watcher on and a
// decided tick's state: the title template "heal {issue}", a loaded memory in
// a file of its own, the base main at abc123, and the snapshot's viewer "bot".
func wactModel(t *testing.T, o Options) (*Model, *Workspace, *watchState) {
	t.Helper()
	withMod(t)
	m := NewForTest(o)
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	ws := NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "a", ConfigDir: dir, RepoPath: t.TempDir()}, Storage: storage, Config: config.DefaultConfig(), State: state})
	m.SetWorkspacesForTest(ws)
	wactOn(ws, true)
	tmpl, err := watch.ParseTitleTemplate("heal {issue}")
	require.NoError(t, err)
	st := m.watchFor(ws)
	st.tmpl, st.cfg = tmpl, watch.Config{Label: "drift", EscalateLabel: "needs-human", Title: "heal {issue}", Cap: 2, NudgeLimit: 3, LogTail: 50}
	st.mem, st.memLoaded, st.memPath = watch.Memory{Version: 1}, true, filepath.Join(dir, watch.MemoryFile)
	st.repo, st.base, st.baseSHA, st.startOK, st.checkout = "o/r", "main", "abc123", true, t.TempDir()
	st.snap = &watch.Snapshot{Now: time.Now(), Viewer: "bot"}
	st.inFlight = map[string]bool{}
	return m, ws, st
}

// wactOn switches ws's watcher on or off in its settings.
func wactOn(ws *Workspace, on bool) {
	ws.cfg.Mutate(func(c *config.Config) { c.Settings.Watch = &on })
}

// wactPR is a watcher PR for issue, open or not: its head branch is what the
// title "heal <issue>" makes, its author the snapshot's viewer.
func wactPR(issue, number int, state string) github.WatchPR {
	return github.WatchPR{Number: number, State: state, HeadRefName: git.SanitizeBranchName(fmt.Sprintf("heal %d", issue)), Author: "bot", Closes: []int{issue}, URL: fmt.Sprintf("https://github.test/o/r/pull/%d", number)}
}

// wactPaused is a Paused session in ws linked to issue.
func wactPaused(t *testing.T, ws *Workspace, title string, issue int) *session.Instance {
	t.Helper()
	inst := statusInst(t, title, session.Paused)
	inst.SetIssue(issue)
	ws.add(inst)
	return inst
}

func TestWatchKill_KillsTheWatchersOwnSession(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	inst := wactPaused(t, ws, "heal 7", 7)

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActKill, Issue: 7, Session: uint64(m.idOf(inst))}})

	assert.Equal(t, session.Deleting, inst.GetStatus())
	assert.Len(t, m.Drain().Jobs, 1, "the kill job")
}

func TestWatchKill_RefusesWhatIsNotItsOwnSession(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		issue     int
		elsewhere bool
	}{
		{"a title that does not parse", "other 7", 7, false},
		{"another issue's title", "heal 8", 7, false},
		{"no link to the issue", "heal 7", 0, false},
		{"another workspace's session", "heal 7", 7, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactModel(t, Options{})
			inst := statusInst(t, c.title, session.Paused)
			inst.SetIssue(c.issue)
			if c.elsewhere {
				other := storedWorkspace(t, "b")
				other.add(inst)
				m.SetWorkspacesForTest(ws, other)
			} else {
				ws.add(inst)
			}

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActKill, Issue: 7, Session: uint64(m.idOf(inst))}})

			assert.Equal(t, session.Paused, inst.GetStatus(), "nothing was killed")
			assert.Empty(t, m.Drain().Jobs)
			assert.NotEmpty(t, st.actErrs[7], "the refusal is reported")
		})
	}
}

func TestDispatchWatchActions_ActsOnlyOnADecidedTickOfAnOwnedWorkspace(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(ws *Workspace, st *watchState)
	}{
		{"no decided tick", func(_ *Workspace, st *watchState) { st.snap = nil }},
		{"memory not loaded", func(_ *Workspace, st *watchState) { st.memLoaded = false }},
		{"the watcher is off", func(ws *Workspace, _ *watchState) { wactOn(ws, false) }},
		{"no repository", func(ws *Workspace, _ *watchState) { ws.ctx.RepoPath = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactModel(t, Options{})
			inst := wactPaused(t, ws, "heal 7", 7)
			c.spoil(ws, st)

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActKill, Issue: 7, Session: uint64(m.idOf(inst))}})

			assert.Equal(t, session.Paused, inst.GetStatus())
			assert.Empty(t, m.Drain().Jobs)
		})
	}
}

func TestWatchBusy_AStartWaitsForAnyStartAndOtherKindsOnlyForTheirOwnKey(t *testing.T) {
	st := &watchState{inFlight: map[string]bool{}}
	start := watch.Action{Kind: watch.ActStart, Issue: 8}
	assert.False(t, watchBusy(st, start))

	st.inFlight[watchActKey(watch.Action{Kind: watch.ActStart, Issue: 7})] = true
	assert.True(t, watchBusy(st, start), "another issue's start is out")

	send := watch.Action{Kind: watch.ActSend, Issue: 8, Session: 3}
	assert.False(t, watchBusy(st, send), "a start in flight blocks only starts")
	st.inFlight[watchActKey(send)] = true
	assert.True(t, watchBusy(st, send))
	assert.False(t, watchBusy(st, watch.Action{Kind: watch.ActSend, Issue: 8, Session: 4}), "another session's nudge")

	closePR := watch.Action{Kind: watch.ActClosePR, Issue: 8, PR: 5}
	kill := watch.Action{Kind: watch.ActKill, Issue: 8, Session: 3}
	assert.NotEqual(t, watchActKey(closePR), watchActKey(kill), "one issue's close and kill don't block each other")
}

func TestDeliverWatchAction_ReleasesTheKeyAndReportsAFailureOrARefusal(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	a := watch.Action{Kind: watch.ActClosePR, Issue: 7, PR: 5}

	st.inFlight["k"] = true
	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a, err: errors.New("boom")})
	assert.Empty(t, st.inFlight)
	assert.Contains(t, st.actErrs[7], "boom")

	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a, refused: "the hook said no"})
	assert.Contains(t, st.actErrs[7], "the hook said no")

	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a})
	assert.Empty(t, st.actErrs, "a success clears the kind's error")
}

func TestDeliverWatchAction_ALateResultForAStateNoLongerLiveRecordsNothing(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	a := watch.Action{Kind: watch.ActClosePR, Issue: 7, PR: 5}
	st.inFlight["k"] = true
	wactOn(ws, false)

	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a, err: errors.New("boom")})

	assert.Empty(t, st.inFlight, "the key is still released")
	assert.Empty(t, st.actErrs)
}

// wactGH is a gh the watcher's calls run against: it records each call's
// arguments after "gh", fails the calls whose arguments start with fail, and
// answers the ones that start with a key of out.
type wactGH struct {
	mu    sync.Mutex
	calls []string
	fail  string
	out   map[string]string
}

func (g *wactGH) do(c *exec.Cmd) ([]byte, error) {
	args := strings.Join(c.Args[1:], " ")
	g.mu.Lock()
	g.calls = append(g.calls, args)
	g.mu.Unlock()
	if g.fail != "" && strings.HasPrefix(args, g.fail) {
		return nil, errors.New("boom")
	}
	for prefix, text := range g.out {
		if strings.HasPrefix(args, prefix) {
			return []byte(text), nil
		}
	}
	if strings.HasPrefix(args, "label list") {
		return []byte("[]"), nil // AddLabel lists the repo's labels first
	}
	return nil, nil
}

func (g *wactGH) Run(c *exec.Cmd) error {
	_, err := g.do(c)
	return err
}

func (g *wactGH) Output(c *exec.Cmd) ([]byte, error) { return g.do(c) }

func (g *wactGH) CombinedOutput(c *exec.Cmd) ([]byte, error) { return g.do(c) }

func (g *wactGH) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

// wactRunAll runs out's jobs and then the jobs their results spawn, until none
// is left, and returns every event produced on the way (out's own included).
func wactRunAll(m *Model, out Out) []Event {
	var events []Event
	for {
		events = append(events, out.Events...)
		if len(out.Jobs) == 0 {
			return events
		}
		out = run(m, out)
	}
}

// wactCount is how many of calls start with prefix.
func wactCount(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestWatchClosePR_ClosesTheWatchersOwnOpenPRWithTheCommentItWasGiven(t *testing.T) {
	gh := &wactGH{}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	st.snap.PRs = []github.WatchPR{wactPR(7, 5, "OPEN")}
	a := watch.Action{Kind: watch.ActClosePR, Issue: 7, PR: 5, Reason: "Closing: issue #7 is closed."}

	m.dispatchWatchActions(ws, st, []watch.Action{a})
	out := m.Drain()

	require.Len(t, out.Jobs, 1)
	assert.True(t, st.inFlight[watchActKey(a)])
	run(m, out)
	calls := gh.seen()
	require.Len(t, calls, 1)
	assert.True(t, strings.HasPrefix(calls[0], "pr close"), calls[0])
	assert.Contains(t, calls[0], "Closing: issue #7 is closed.")
	assert.Empty(t, st.inFlight)
	assert.Empty(t, st.actErrs)
}

func TestWatchClosePR_RefusesAPRThatIsNotItsOwnOpenPR(t *testing.T) {
	own := wactPR(7, 5, "OPEN")
	merged := own
	merged.State = "MERGED"
	stranger := own
	stranger.Author = "someone"
	crossRepo := own
	crossRepo.CrossRepo = true
	cases := []struct {
		name string
		prs  []github.WatchPR
	}{
		{"absent from the snapshot", nil},
		{"not open", []github.WatchPR{merged}},
		{"someone else's", []github.WatchPR{stranger}},
		{"cross-repository", []github.WatchPR{crossRepo}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gh := &wactGH{}
			m, ws, st := wactModel(t, Options{GHExec: gh})
			st.snap.PRs = c.prs

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActClosePR, Issue: 7, PR: 5, Reason: "close"}})

			assert.Empty(t, m.Drain().Jobs)
			assert.Empty(t, gh.seen())
			assert.NotEmpty(t, st.actErrs[7], "the refusal is reported")
		})
	}
}

func TestWatchClosePR_AFailedCloseIsReported(t *testing.T) {
	gh := &wactGH{fail: "pr close"}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	st.snap.PRs = []github.WatchPR{wactPR(7, 5, "OPEN")}

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActClosePR, Issue: 7, PR: 5, Reason: "close"}})
	run(m, m.Drain())

	assert.Contains(t, st.actErrs[7], "close PR #5")
	assert.Contains(t, st.actErrs[7], "boom")
	assert.Empty(t, st.inFlight)
}

func TestDispatchWatchActions_AnActionInFlightIsNotDispatchedAgain(t *testing.T) {
	m, ws, st := wactModel(t, Options{GHExec: &wactGH{}})
	st.snap.PRs = []github.WatchPR{wactPR(7, 5, "OPEN")}
	actions := []watch.Action{
		{Kind: watch.ActClosePR, Issue: 7, PR: 5, Reason: "close"},
		{Kind: watch.ActEscalate, Issue: 7, Reason: "stuck"},
	}

	m.dispatchWatchActions(ws, st, actions)
	m.dispatchWatchActions(ws, st, actions)

	assert.Len(t, m.Drain().Jobs, 2, "one close and one escalation, not four jobs")
}

func TestWatchEscalate_CommentsLabelsAlertsThenRecords(t *testing.T) {
	gh := &wactGH{}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	a := watch.Action{Kind: watch.ActEscalate, Issue: 7, PR: 5, Reason: "PR #5 was closed unmerged", Settle: []int{5}}

	m.dispatchWatchActions(ws, st, []watch.Action{a})
	out := m.Drain()
	require.Len(t, out.Jobs, 1)
	assert.Empty(t, st.mem.Issues, "nothing is recorded before the comment and the label are posted")
	assert.True(t, st.inFlight[watchActKey(a)])

	out = run(m, out)
	assert.Equal(t, 0, wactCount(gh.seen(), "issue edit"), "the label waits for the comment's record")
	assert.Equal(t, "PR #5 was closed unmerged", st.mem.Issues[7].Commented, "the posted comment is recorded before the label is tried")
	assert.Nil(t, st.mem.Issues[7].Escalated, "the escalation is recorded only once the label is on")
	require.Len(t, out.Jobs, 1, "the label is its own job")
	assert.True(t, st.inFlight[watchActKey(a)], "the key is held across the two jobs")
	onDisk, err := watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	assert.Equal(t, "PR #5 was closed unmerged", onDisk.Issues[7].Commented, "and saved")
	notes, _ := noticesAndStarts(wactRunAll(m, out))
	require.Len(t, notes, 1, "one alert, after the label")

	calls := gh.seen()
	comment := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "issue comment") })
	label := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "issue edit") })
	require.GreaterOrEqual(t, comment, 0, "calls: %v", calls)
	require.Greater(t, label, comment, "the comment comes before the label")
	assert.Contains(t, calls[comment], "PR #5 was closed unmerged")
	assert.Contains(t, calls[label], "needs-human")
	assert.Contains(t, notes[0].Info, "#7")
	assert.Contains(t, notes[0].Info, "PR #5 was closed unmerged")
	rec := st.mem.Issues[7].Escalated
	require.NotNil(t, rec)
	assert.Equal(t, "PR #5 was closed unmerged", rec.Reason)
	assert.False(t, rec.At.IsZero())
	assert.True(t, st.mem.PRs[5].Settled, "rule 5 settles its PRs")
	onDisk, err = watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	require.NotNil(t, onDisk.Issues[7].Escalated)
	assert.True(t, onDisk.PRs[5].Settled)
	assert.Empty(t, st.inFlight)
}

func TestWatchEscalate_AFailedStepAlertsAndEscalatesNothing(t *testing.T) {
	cases := []struct {
		name          string
		fail          string
		wantLabel     bool
		wantCommented string
	}{
		{"the comment", "issue comment", false, ""},
		{"the label", "issue edit", true, "stuck"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gh := &wactGH{fail: c.fail}
			m, ws, st := wactModel(t, Options{GHExec: gh})

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActEscalate, Issue: 7, Reason: "stuck"}})
			events := wactRunAll(m, m.Drain())

			calls := gh.seen()
			assert.Equal(t, c.wantLabel, wactCount(calls, "issue edit") > 0, "the label is tried only after the comment was posted: %v", calls)
			notes, _ := noticesAndStarts(events)
			assert.Empty(t, notes, "no alert")
			assert.Nil(t, st.mem.Issues[7].Escalated, "the escalation is not recorded")
			assert.Equal(t, c.wantCommented, st.mem.Issues[7].Commented, "only a posted comment is recorded")
			assert.Contains(t, st.actErrs[7], "boom")
			assert.Empty(t, st.inFlight, "the next tick decides again")
		})
	}
}

func TestWatchEscalate_ARetryAfterAFailedLabelPostsNoSecondComment(t *testing.T) {
	gh := &wactGH{fail: "issue edit"}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	a := watch.Action{Kind: watch.ActEscalate, Issue: 7, Reason: "stuck"}

	m.dispatchWatchActions(ws, st, []watch.Action{a})
	wactRunAll(m, m.Drain())
	require.Equal(t, 1, wactCount(gh.seen(), "issue comment"))
	require.Equal(t, 1, wactCount(gh.seen(), "issue edit"), "the first label failed")
	gh.fail = ""

	m.dispatchWatchActions(ws, st, []watch.Action{a})
	events := wactRunAll(m, m.Drain())

	calls := gh.seen()
	assert.Equal(t, 1, wactCount(calls, "issue comment"), "the retry skips the comment already posted: %v", calls)
	assert.Equal(t, 2, wactCount(calls, "issue edit"), "and tries the label again")
	notes, _ := noticesAndStarts(events)
	assert.Len(t, notes, 1, "the alert follows the label that finally went on")
	require.NotNil(t, st.mem.Issues[7].Escalated)
	assert.Empty(t, st.actErrs[7])
	assert.Empty(t, st.inFlight)
}

func TestWatchEscalate_ACommentRecordedForAnotherReasonOrNoneIsPostedAgain(t *testing.T) {
	cases := []struct{ name, recorded, reason string }{
		{"another reason", "old reason", "new reason"},
		{"none recorded and an empty reason", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gh := &wactGH{}
			m, ws, st := wactModel(t, Options{GHExec: gh})
			if c.recorded != "" {
				st.mem.RecordCommented(7, c.recorded)
			}

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActEscalate, Issue: 7, Reason: c.reason}})
			wactRunAll(m, m.Drain())

			assert.Equal(t, 1, wactCount(gh.seen(), "issue comment"), "the recorded comment was for another escalation: %v", gh.seen())
			assert.Equal(t, 1, wactCount(gh.seen(), "issue edit"))
		})
	}
}

func TestWatchEscalate_AMemorySaveThatFailsIsReported(t *testing.T) {
	cases := []struct {
		name        string
		want        string
		breakAfter  bool
		wantLabeled bool
	}{
		{"the comment's record", "record the comment", false, false},
		{"the escalation's record", "record the escalation", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gh := &wactGH{}
			m, ws, st := wactModel(t, Options{GHExec: gh})
			if !c.breakAfter {
				st.memPath = t.TempDir()
			}

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActEscalate, Issue: 7, Reason: "stuck"}})
			out := run(m, m.Drain())
			if c.breakAfter {
				st.memPath = t.TempDir()
			}
			events := wactRunAll(m, out)

			assert.Contains(t, st.actErrs[7], c.want)
			assert.Equal(t, c.wantLabeled, wactCount(gh.seen(), "issue edit") > 0, "an unsaved comment's record stops the label: %v", gh.seen())
			notes, _ := noticesAndStarts(events)
			var errs int
			for _, n := range notes {
				if n.Err != nil {
					errs++
				}
			}
			assert.Equal(t, 1, errs, "the failure is shown to the clients")
		})
	}
}

func TestWatchEscalationComment_NamesTheReasonTheSessionThePRAndTheCounts(t *testing.T) {
	_, _, st := wactModel(t, Options{})
	st.mem.RecordNudge(5, "ci", "k1", time.Now())
	st.mem.RecordNudge(5, "ci", "k2", time.Now())
	st.mem.RecordNudge(5, "conflict", "k3", time.Now())
	a := watch.Action{Kind: watch.ActEscalate, Issue: 7, PR: 5, Reason: "PR #5: ci nudge limit (3) reached"}

	got := watchEscalationComment(st, a, "heal 7")

	assert.Contains(t, got, "PR #5: ci nudge limit (3) reached")
	assert.Contains(t, got, "Session: heal 7")
	assert.Contains(t, got, "PR: #5")
	assert.Contains(t, got, "ci 2, conflict 1")
	assert.Contains(t, got, "`needs-human`")

	bare := watchEscalationComment(st, watch.Action{Kind: watch.ActEscalate, Issue: 7, Reason: "x"}, "")
	assert.NotContains(t, bare, "Session:")
	assert.NotContains(t, bare, "PR:")
	assert.NotContains(t, bare, "Nudges sent")
}

// wactReady is a Ready Claude session titled title in ws, linked to issue, on a
// live mod's launch L1.
func wactReady(t *testing.T, m *Model, title string, issue int) *session.Instance {
	t.Helper()
	inst := modInst(t, m, title)
	inst.SetIssue(issue)
	require.NoError(t, inst.TransitionTo(session.Ready))
	return inst
}

// wactNudge is a nudge action for issue 7's PR 5.
func wactNudge(m *Model, inst *session.Instance, kind, key, text string) watch.Action {
	return watch.Action{Kind: watch.ActSend, Issue: 7, Session: uint64(m.idOf(inst)), PR: 5, Nudge: watch.Nudge{Kind: kind, Key: key, Text: text}, N: 1}
}

func TestWatchSend_CountsAndSavesThenQueuesTheNudgeThroughTheMod(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	st.snap.PRs = []github.WatchPR{wactPR(7, 5, "OPEN")}
	inst := wactReady(t, m, "heal 7", 7)

	m.dispatchWatchActions(ws, st, []watch.Action{wactNudge(m, inst, "conflict", "k1", "merge origin/main, never rebase")})

	l := m.linkFor(inst)
	require.Len(t, l.outbox, 1)
	assert.Equal(t, "merge origin/main, never rebase\n\n(nudge 1 of 2; the next escalates to the owner)", l.outbox[0].text)
	assert.Equal(t, OriginWatch, l.outbox[0].origin)
	assert.False(t, l.outbox[0].asUser)
	rec := st.mem.PRs[5].Nudges["conflict"]
	assert.Equal(t, 1, rec.N)
	assert.Equal(t, "k1", rec.Key)
	onDisk, err := watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	assert.Equal(t, 1, onDisk.PRs[5].Nudges["conflict"].N, "saved")
}

func TestWatchSend_AnIssueChangedNudgeMovesSeen(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	st.snap.PRs = []github.WatchPR{wactPR(7, 5, "OPEN")}
	inst := wactReady(t, m, "heal 7", 7)

	m.dispatchWatchActions(ws, st, []watch.Action{wactNudge(m, inst, "issue-changed", "h2:c9", "the issue changed")})

	assert.Equal(t, "h2:c9", st.mem.Issues[7].Seen)
	assert.Len(t, m.linkFor(inst).outbox, 1)
}

func TestWatchSend_AFailedSaveSendsNothing(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	st.snap.PRs = []github.WatchPR{wactPR(7, 5, "OPEN")}
	st.memPath = t.TempDir()
	inst := wactReady(t, m, "heal 7", 7)

	m.dispatchWatchActions(ws, st, []watch.Action{wactNudge(m, inst, "conflict", "k1", "merge")})

	assert.Empty(t, m.linkFor(inst).outbox, "the count must be on disk before the message is sent")
	assert.Contains(t, st.actErrs[7], "nothing was sent")
	notes, _ := noticesAndStarts(m.Drain().Events)
	require.Len(t, notes, 1)
	assert.Error(t, notes[0].Err)
}

func TestWatchSend_SendsOnlyToTheWatchersOwnReadySessionForItsOwnPR(t *testing.T) {
	cases := []struct {
		name  string
		title string
		issue int
		ready bool
		prs   []github.WatchPR
	}{
		{"a session that is Running", "heal 7", 7, false, []github.WatchPR{wactPR(7, 5, "OPEN")}},
		{"a title that does not parse", "other 7", 7, true, []github.WatchPR{wactPR(7, 5, "OPEN")}},
		{"a session linked to another issue", "heal 7", 8, true, []github.WatchPR{wactPR(7, 5, "OPEN")}},
		{"a PR that is not its own open PR", "heal 7", 7, true, []github.WatchPR{wactPR(7, 5, "MERGED")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactModel(t, Options{})
			st.snap.PRs = c.prs
			inst := modInst(t, m, c.title)
			inst.SetIssue(c.issue)
			if !c.ready {
				require.NoError(t, inst.TransitionTo(session.Running)) // modInst's hook scan leaves it Ready
			}

			m.dispatchWatchActions(ws, st, []watch.Action{wactNudge(m, inst, "conflict", "k1", "merge")})

			assert.Empty(t, m.linkFor(inst).outbox, "nothing is sent")
			assert.Empty(t, st.mem.PRs, "and nothing is counted")
			assert.NotEmpty(t, st.actErrs[7])
		})
	}
}

func TestWatchSend_ACINudgeCarriesEachFailedJobsLogTailAfterTheCountIsSaved(t *testing.T) {
	gh := &wactGH{out: map[string]string{"run view": "compiling\nerror: expected `;`\n"}}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	pr := wactPR(7, 5, "OPEN")
	pr.Failing = []github.FailedCheck{{Name: "build", JobID: 11}, {Name: "lint", JobID: 12}, {Name: "deploy", JobID: 0}}
	st.snap.PRs = []github.WatchPR{pr}
	inst := wactReady(t, m, "heal 7", 7)
	a := wactNudge(m, inst, "ci", "sha1", "CI failed on the head commit:\n- build\n- lint\n- deploy")

	m.dispatchWatchActions(ws, st, []watch.Action{a})
	m.dispatchWatchActions(ws, st, []watch.Action{a})
	out := m.Drain()

	require.Len(t, out.Jobs, 1, "one log fetch, however often the action is dispatched")
	assert.Equal(t, 1, st.mem.PRs[5].Nudges["ci"].N, "counted before the logs are fetched")
	assert.Empty(t, m.linkFor(inst).outbox, "nothing is sent before the logs are in")

	run(m, out)

	calls := gh.seen()
	require.Len(t, calls, 2, "a check that names no Actions job has no log: %v", calls)
	l := m.linkFor(inst)
	require.Len(t, l.outbox, 1)
	text := l.outbox[0].text
	assert.True(t, strings.HasPrefix(text, "CI failed on the head commit:"), text)
	assert.Contains(t, text, "Log of build:\ncompiling\nerror: expected `;`")
	assert.Contains(t, text, "Log of lint:")
	assert.True(t, strings.HasSuffix(text, "(nudge 1 of 2; the next escalates to the owner)"), text)
	assert.Empty(t, st.inFlight)
}

func TestWatchSend_ACINudgeFetchesAtMostFiveJobsAndSurvivesAFailedFetch(t *testing.T) {
	gh := &wactGH{fail: "run view"}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	pr := wactPR(7, 5, "OPEN")
	for i := 1; i <= 7; i++ {
		pr.Failing = append(pr.Failing, github.FailedCheck{Name: fmt.Sprintf("job%d", i), JobID: int64(100 + i)})
	}
	st.snap.PRs = []github.WatchPR{pr}
	inst := wactReady(t, m, "heal 7", 7)

	m.dispatchWatchActions(ws, st, []watch.Action{wactNudge(m, inst, "ci", "sha1", "CI failed")})
	run(m, m.Drain())

	assert.Len(t, gh.seen(), 5, "at most five jobs")
	l := m.linkFor(inst)
	require.Len(t, l.outbox, 1, "the nudge goes without the tails it could not fetch")
	assert.Contains(t, l.outbox[0].text, "the log could not be fetched")
	assert.NotContains(t, l.outbox[0].text, "job6")
}

func TestWatchSend_ACINudgeWhoseSessionCanNoLongerTakeItIsReported(t *testing.T) {
	gh := &wactGH{out: map[string]string{"run view": "log\n"}}
	m, ws, st := wactModel(t, Options{GHExec: gh})
	pr := wactPR(7, 5, "OPEN")
	pr.Failing = []github.FailedCheck{{Name: "build", JobID: 11}}
	st.snap.PRs = []github.WatchPR{pr}
	inst := wactReady(t, m, "heal 7", 7)
	m.dispatchWatchActions(ws, st, []watch.Action{wactNudge(m, inst, "ci", "sha1", "CI failed")})
	out := m.Drain()
	require.NoError(t, inst.TransitionTo(session.Paused))

	run(m, out)

	assert.Empty(t, m.linkFor(inst).outbox)
	assert.Contains(t, st.actErrs[7], "not running")
}

func TestWatchNudgeText_CutsLogTailsFromTheTopToFitTheCap(t *testing.T) {
	suffix := "\n\n(nudge 1 of 2; the next escalates to the owner)"
	var long strings.Builder
	for i := 0; i < 40000; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
	}

	got := watchNudgeText("CI failed", []watchLog{{name: "build", tail: long.String()}}, suffix)

	assert.LessOrEqual(t, len(got), watchTextCap)
	assert.True(t, strings.HasSuffix(got, suffix))
	assert.Contains(t, got, "line 39999", "the bottom of the log is kept")
	assert.NotContains(t, got, "line 0\n", "the top is cut")
	assert.True(t, strings.HasPrefix(got, "CI failed\n\nLog of build:\n"))
}

func TestWatchNudgeText_NeverSplitsARune(t *testing.T) {
	tail := strings.Repeat("é", watchTextCap)

	got := watchNudgeText("CI failed", []watchLog{{name: "build", tail: tail}}, "")

	assert.True(t, utf8.ValidString(got))
	assert.LessOrEqual(t, len(got), watchTextCap)
}

func TestWatchNudgeText_AnUnfetchedLogIsNamedAndNoRoomLeftDropsTheRest(t *testing.T) {
	got := watchNudgeText("head", []watchLog{{name: "a", err: errors.New("gone")}}, "!")
	assert.Equal(t, "head\n\nLog of a:\n(the log could not be fetched: gone)!", got)

	huge := strings.Repeat("x", watchTextCap)
	got = watchNudgeText(huge, []watchLog{{name: "b", tail: "late"}}, "!")
	assert.NotContains(t, got, "Log of b", "no room is left for it")
	assert.True(t, strings.HasSuffix(got, "!"))
}

// wactIssue is a labelled open issue.
func wactIssue(n int) github.WatchIssue {
	return github.WatchIssue{Number: n, Title: fmt.Sprintf("Drift %d", n), URL: fmt.Sprintf("https://github.test/o/r/issues/%d", n), Labels: []string{"drift"}}
}

// wactStartModel is wactModel with issue 7 in the snapshot and both prompt
// templates set.
func wactStartModel(t *testing.T, o Options) (*Model, *Workspace, *watchState) {
	t.Helper()
	m, ws, st := wactModel(t, o)
	st.snap.Issues = []github.WatchIssue{wactIssue(7)}
	st.prompt = "Fix {issue_title} ({issue_url}) on {base}"
	st.resumePrompt = "Resume #{pr} on {branch}"
	return m, ws, st
}

// wactHook is a hook command that saves its stdin next to itself, as in.json,
// then runs body; the second result is that file's path.
func wactHook(t *testing.T, body string) (*watch.HookConfig, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")
	script := "#!/bin/sh\ncat > \"$(dirname \"$0\")/in.json\"\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return &watch.HookConfig{Command: []string{path}, Timeout: 20 * time.Second}, filepath.Join(dir, "in.json")
}

type wactHookSession struct {
	Title string            `json:"title"`
	Issue int               `json:"issue"`
	Vars  map[string]string `json:"vars"`
}

type wactHookPR struct {
	Number int `json:"number"`
	Issue  int `json:"issue"`
}

type wactHookIn struct {
	Point     string            `json:"point"`
	Workspace string            `json:"workspace"`
	Repo      string            `json:"repo"`
	BaseSHA   string            `json:"base_sha"`
	PR        *wactHookPR       `json:"pr"`
	Sessions  []wactHookSession `json:"sessions"`
	PRs       []wactHookPR      `json:"prs"`
}

// wactRunStart dispatches one start action, runs its job and returns the
// job's result without delivering it.
func wactRunStart(t *testing.T, m *Model, ws *Workspace, st *watchState, a watch.Action) watchActionResult {
	t.Helper()
	m.dispatchWatchActions(ws, st, []watch.Action{a})
	out := m.Drain()
	require.Len(t, out.Jobs, 1, "the hook and render job")
	assert.True(t, st.inFlight[watchActKey(a)])
	res, ok := out.Jobs[0]().(watchActionResult)
	require.True(t, ok)
	return res
}

func TestWatchStart_CreatesTheSessionCutFromTheBaseWithTheRenderedPrompt(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	a := watch.Action{Kind: watch.ActStart, Issue: 7}

	res := wactRunStart(t, m, ws, st, a)

	assert.Equal(t, "abc123", res.start.baseRef, "cut from origin/<base>'s SHA")
	assert.Empty(t, res.start.branch)
	m.Deliver(res)
	out := m.Drain()
	require.Len(t, ws.insts, 1)
	inst := ws.insts[0]
	assert.Equal(t, "heal 7", inst.Title)
	assert.Equal(t, 7, inst.IssueNumber())
	assert.True(t, strings.HasPrefix(inst.Prompt(), "Fix Drift 7 (https://github.test/o/r/issues/7) on main"), inst.Prompt())
	assert.Contains(t, inst.Prompt(), watch.PromptFooter(7))
	assert.Len(t, out.Jobs, 1, "the session's own start job")
	assert.Equal(t, watch.IssueSeen(wactIssue(7)), st.mem.Issues[7].Seen)
	onDisk, err := watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	assert.Equal(t, st.mem.Issues[7].Seen, onDisk.Issues[7].Seen)
	assert.Empty(t, st.inFlight)
	assert.Empty(t, st.actErrs)
}

func TestWatchStart_TheHooksVarsNameTheSessionAndItsInputDescribesTheWorkspace(t *testing.T) {
	hook, inPath := wactHook(t, `echo '{"start": true, "vars": {"spec": "kalshi"}}'`)
	m, ws, st := wactStartModel(t, Options{})
	tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
	require.NoError(t, err)
	st.tmpl, st.cfg.Hook = tmpl, hook
	st.prompt = "Fix {issue} for {spec}"
	existing := wactPaused(t, ws, "heal kalshi 3", 3)
	st.snap.Sessions = []watch.Session{{ID: uint64(m.idOf(existing)), Title: "heal kalshi 3", Status: "Paused", Branch: "heal-kalshi-3", Issue: 3}}
	pr := wactPR(3, 31, "OPEN")
	pr.HeadRefName = git.SanitizeBranchName("heal kalshi 3")
	st.snap.PRs = []github.WatchPR{pr}

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})

	assert.Equal(t, "heal kalshi 7", res.start.title)
	assert.True(t, strings.HasPrefix(res.start.prompt, "Fix 7 for kalshi"), res.start.prompt)
	data, err := os.ReadFile(inPath)
	require.NoError(t, err)
	var in wactHookIn
	require.NoError(t, json.Unmarshal(data, &in))
	assert.Equal(t, "start", in.Point)
	assert.Equal(t, "a", in.Workspace)
	assert.Equal(t, "o/r", in.Repo)
	assert.Equal(t, "abc123", in.BaseSHA)
	assert.Nil(t, in.PR, "a start has no PR")
	assert.Equal(t, []wactHookSession{{Title: "heal kalshi 3", Issue: 3, Vars: map[string]string{"spec": "kalshi"}}}, in.Sessions)
	assert.Equal(t, []wactHookPR{{Number: 31, Issue: 3}}, in.PRs)
}

func TestWatchStart_AHookThatDeclinesLeavesTheIssueWaitingWithItsReason(t *testing.T) {
	hook, _ := wactHook(t, `echo '{"start": false, "reason": "no row free"}'`)
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Hook = hook

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})
	m.Deliver(res)

	assert.Empty(t, ws.insts, "nothing was created")
	assert.Contains(t, st.actErrs[7], "no row free")
	assert.Empty(t, st.inFlight)
}

func TestWatchStart_AHookThatFailsLeavesTheIssueWaiting(t *testing.T) {
	hook, _ := wactHook(t, "exit 3")
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Hook = hook

	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

	assert.Empty(t, ws.insts)
	assert.Contains(t, st.actErrs[7], "hook start")
}

func TestWatchStart_RefusesATitleOrPromptItCannotUse(t *testing.T) {
	t.Run("a title wider than the limit", func(t *testing.T) {
		hook, _ := wactHook(t, `echo '{"start": true, "vars": {"spec": "abcdefghijklmnopqrstuvwxyz"}}'`)
		m, ws, st := wactStartModel(t, Options{})
		tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
		require.NoError(t, err)
		st.tmpl, st.cfg.Hook = tmpl, hook

		m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

		assert.Empty(t, ws.insts)
		assert.Contains(t, st.actErrs[7], "title")
	})
	t.Run("a prompt over 64 KiB", func(t *testing.T) {
		m, ws, st := wactStartModel(t, Options{})
		st.prompt = strings.Repeat("x", watchTextCap)

		m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

		assert.Empty(t, ws.insts)
		assert.Contains(t, st.actErrs[7], "over the")
	})
	t.Run("a prompt naming a variable nobody supplies", func(t *testing.T) {
		m, ws, st := wactStartModel(t, Options{})
		st.prompt = "Fix {nope}"

		m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

		assert.Empty(t, ws.insts)
		assert.Contains(t, st.actErrs[7], "prompt")
	})
}

func TestWatchStart_StartsNothingInATickWhoseFetchFailedOrForAnIssueNotInTheSnapshot(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	st.startOK = false
	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 7}})
	assert.Empty(t, m.Drain().Jobs)
	assert.Contains(t, st.actErrs[7], "fetch")

	st.startOK = true
	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 9}})
	assert.Empty(t, m.Drain().Jobs)
	assert.Contains(t, st.actErrs[9], "snapshot")
}

func TestWatchStart_OneStartAtATimeEvenAcrossTicks(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	st.snap.Issues = []github.WatchIssue{wactIssue(7), wactIssue(8)}

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 7}, {Kind: watch.ActStart, Issue: 8}})

	assert.Len(t, m.Drain().Jobs, 1, "the second start waits for the first's session to exist")
}

func TestWatchStart_AResumeFetchesThePRsBranchAndTakesItsPrompt(t *testing.T) {
	var calls []string
	do := func(c *exec.Cmd) ([]byte, error) {
		calls = append(calls, strings.Join(c.Args, " "))
		return []byte("deadbeef\n"), nil
	}
	mock := cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := do(c); return err },
		OutputFunc:         do,
		CombinedOutputFunc: do,
	}
	m, ws, st := wactStartModel(t, Options{CmdExec: mock})
	pr := wactPR(7, 5, "OPEN")
	st.snap.PRs = []github.WatchPR{pr}
	a := watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5}

	res := wactRunStart(t, m, ws, st, a)

	assert.Equal(t, pr.HeadRefName, res.start.branch)
	assert.Empty(t, res.start.baseRef, "a resume checks out the PR's branch, it is cut from nothing")
	assert.True(t, slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "fetch") && strings.Contains(c, pr.HeadRefName) }), "calls: %v", calls)
	m.Deliver(res)
	require.Len(t, ws.insts, 1)
	assert.True(t, strings.HasPrefix(ws.insts[0].Prompt(), "Resume #5 on "+pr.HeadRefName), ws.insts[0].Prompt())
}

func TestWatchStart_AResumeWhoseRenderedBranchIsNotThePRsIsRefusedWithoutAFetch(t *testing.T) {
	var calls []string
	do := func(c *exec.Cmd) ([]byte, error) {
		calls = append(calls, strings.Join(c.Args, " "))
		return nil, nil
	}
	mock := cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := do(c); return err },
		OutputFunc:         do,
		CombinedOutputFunc: do,
	}
	hook, _ := wactHook(t, `echo '{"start": true, "vars": {"spec": "other"}}'`)
	m, ws, st := wactStartModel(t, Options{CmdExec: mock})
	tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
	require.NoError(t, err)
	st.tmpl, st.cfg.Hook = tmpl, hook
	pr := wactPR(7, 5, "OPEN")
	pr.HeadRefName = git.SanitizeBranchName("heal kalshi 7")
	st.snap.PRs = []github.WatchPR{pr}

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5})
	m.Deliver(res)

	assert.Empty(t, ws.insts)
	assert.Contains(t, st.actErrs[7], "resume refused")
	assert.Empty(t, calls, "no fetch")
}

func TestWatchStart_AResumeOfAPRThatIsNotItsOwnIsRefusedBeforeAnyJob(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	pr := wactPR(7, 5, "OPEN")
	pr.Author = "someone"
	st.snap.PRs = []github.WatchPR{pr}

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5}})

	assert.Empty(t, m.Drain().Jobs)
	assert.NotEmpty(t, st.actErrs[7])
}

func TestWatchStart_ReChecksWhatTheHooksRuntimeMayHaveChanged(t *testing.T) {
	foreign := func(t *testing.T, ws *Workspace) { wactPaused(t, ws, "manual", 7) }
	owned := func(t *testing.T, ws *Workspace) { wactPaused(t, ws, "heal 3", 3) }
	sameTitle := func(t *testing.T, ws *Workspace) { wactPaused(t, ws, "heal 7", 0) }
	cases := []struct {
		name  string
		setup func(t *testing.T, ws *Workspace)
		cap   int
		want  string
	}{
		{"a session for the issue appeared", foreign, 2, "#7"},
		{"the cap filled", owned, 1, "cap"},
		{"the title is taken", sameTitle, 2, "already has a session titled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactStartModel(t, Options{})
			st.cfg.Cap = c.cap
			res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})
			c.setup(t, ws)
			before := len(ws.insts)

			m.Deliver(res)

			assert.Len(t, ws.insts, before, "nothing was created")
			assert.Contains(t, st.actErrs[7], c.want)
			assert.Empty(t, st.mem.Issues, "seen is not recorded")
		})
	}
}

func TestWatchStart_ANamedAccountThatIsNotRegisteredRefusesTheStartAndNeverFallsBack(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	withAccounts(t, m, "max-2")

	st.cfg.Session.Account = "ghost"
	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))
	assert.Empty(t, ws.insts, "the default account is never used in its place")
	assert.Contains(t, st.actErrs[7], "ghost")

	st.cfg.Session.Account = "max-2"
	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))
	require.Len(t, ws.insts, 1)
	assert.Equal(t, "max-2", ws.insts[0].Account())
}

func TestWatchStart_TheConfigsModelReachesTheLaunch(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Session.Model = "opus"

	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

	require.Len(t, ws.insts, 1)
	assert.Contains(t, ws.insts[0].Program(), "opus")
}
