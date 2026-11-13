package web

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
)

// pushTestView is a started session in a status.
func pushTestView(id core.InstanceID, status session.Status) core.InstanceView {
	return core.InstanceView{ID: id, Title: "fix the build", Status: status, Started: true}
}

func TestPushWatch_TheFirstProjectionIsABaseline(t *testing.T) {
	w := newPushWatch()
	views := []core.InstanceView{pushTestView(1, session.Prompting), pushTestView(2, session.Running)}

	assert.Empty(t, w.observe(10, views), "a session already waiting when the layer starts pushes nothing")
	assert.Empty(t, w.observe(10, views), "the same state published again is no transition either")
}

func TestPushWatch_WaitingFiresOnEnteringPrompting(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})

	prompting := pushTestView(1, session.Prompting)
	prompting.WaitReason = "permission: Bash"
	prompting.WaitDetail = "git push origin main"
	jobs := w.observe(10, []core.InstanceView{prompting})

	require.Len(t, jobs, 1)
	assert.Equal(t, pushJob{
		Kind:    pushWaiting,
		Session: 1,
		Title:   "fix the build",
		Body:    "permission: Bash · git push origin main",
		Badge:   1,
	}, jobs[0])
	assert.Empty(t, w.observe(10, []core.InstanceView{prompting}), "republished unchanged, it pushes nothing")
}

func TestPushWatch_WaitingFiresAgainOnANewReasonOrToolSummary(t *testing.T) {
	w := newPushWatch()
	first := pushTestView(1, session.Prompting)
	first.WaitReason, first.WaitDetail = "permission: Bash", "ls"
	w.observe(10, []core.InstanceView{first})

	assert.Empty(t, w.observe(10, []core.InstanceView{first}))

	newReason := first
	newReason.WaitReason = "sandbox request"
	require.Len(t, w.observe(10, []core.InstanceView{newReason}), 1, "a new reason")
	assert.Empty(t, w.observe(10, []core.InstanceView{newReason}))

	newDetail := newReason
	newDetail.WaitDetail = "rm -rf build"
	require.Len(t, w.observe(10, []core.InstanceView{newDetail}), 1, "a new tool summary")
	assert.Empty(t, w.observe(10, []core.InstanceView{newDetail}))
}

// One permission prompt is one push, however its labels move under it. The
// session layer keeps a hook's reason and summary across a failed roster query,
// so the return carries the values already pushed; this test pins the trigger
// side, feeding it a gap view with the labels cleared as well, which the
// session layer does not produce, and the return to the pushed values.
func TestPushWatch_ARelabelOrAGapInOneWaitPushesOnce(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})

	hook := pushTestView(1, session.Prompting)
	hook.WaitReason, hook.WaitDetail = "permission: Bash", "ls"
	require.Len(t, w.observe(10, []core.InstanceView{hook}), 1, "entering Prompting")

	gap := pushTestView(1, session.Prompting)
	assert.Empty(t, w.observe(10, []core.InstanceView{gap}), "reason and summary cleared by a silent roster")
	assert.Empty(t, w.observe(10, []core.InstanceView{hook}), "back to the value already pushed")
	assert.Empty(t, w.observe(10, []core.InstanceView{gap}))
	assert.Empty(t, w.observe(10, []core.InstanceView{hook}))

	reasonOnly := pushTestView(1, session.Prompting)
	reasonOnly.WaitReason = "permission: Bash"
	assert.Empty(t, w.observe(10, []core.InstanceView{reasonOnly}), "one field cleared, the other the value pushed")
}

// A value that really is new still pushes after a gap, and the comparison is
// with the last non-empty value pushed, not the last one seen.
func TestPushWatch_ANewValueAfterAGapPushesAgain(t *testing.T) {
	w := newPushWatch()
	first := pushTestView(1, session.Prompting)
	first.WaitReason, first.WaitDetail = "permission: Bash", "ls"
	w.observe(10, []core.InstanceView{first})

	assert.Empty(t, w.observe(10, []core.InstanceView{pushTestView(1, session.Prompting)}), "a gap")

	next := first
	next.WaitDetail = "rm -rf build"
	jobs := w.observe(10, []core.InstanceView{next})
	require.Len(t, jobs, 1, "a new tool summary after the gap")
	assert.Equal(t, "permission: Bash · rm -rf build", jobs[0].Body)
}

// Leaving Prompting ends the stretch: the next wait pushes, with whatever
// reason it has, and so does one the roster alone reports after a hook's.
func TestPushWatch_LeavingPromptingEndsTheStretch(t *testing.T) {
	w := newPushWatch()
	hook := pushTestView(1, session.Prompting)
	hook.WaitReason, hook.WaitDetail = "permission: Bash", "ls"
	w.observe(10, []core.InstanceView{hook})

	assert.Empty(t, w.observe(10, []core.InstanceView{pushTestView(1, session.Running)}))

	again := pushTestView(1, session.Prompting)
	again.WaitReason = "permission: Bash"
	require.Len(t, w.observe(10, []core.InstanceView{again}), 1, "the same reason in a new stretch")
	assert.Empty(t, w.observe(10, []core.InstanceView{again}))
}

func TestPushWatch_FinishedNeedsAStartedSessionGoingFromRunningToReady(t *testing.T) {
	cases := []struct {
		name     string
		from, to session.Status
		started  bool
		want     int
	}{
		{"running to ready", session.Running, session.Ready, true, 1},
		{"running to ready, never started", session.Running, session.Ready, false, 0},
		{"loading to ready", session.Loading, session.Ready, true, 0},
		{"prompting to ready", session.Prompting, session.Ready, true, 0},
		{"paused to ready", session.Paused, session.Ready, true, 0},
		{"ready to running", session.Ready, session.Running, true, 0},
		{"running to paused", session.Running, session.Paused, true, 0},
		{"running to running", session.Running, session.Running, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newPushWatch()
			before := pushTestView(1, c.from)
			before.Started = c.started
			w.observe(10, []core.InstanceView{before})

			after := pushTestView(1, c.to)
			after.Started = c.started
			jobs := w.observe(10, []core.InstanceView{after})

			require.Len(t, jobs, c.want)
			if c.want == 1 {
				assert.Equal(t, pushFinished, jobs[0].Kind)
				assert.Equal(t, pushFinishedBody, jobs[0].Body)
				assert.Equal(t, "fix the build", jobs[0].Title)
			}
		})
	}
}

func TestPushWatch_ASessionFirstSeenInAKnownWorkspace(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})

	jobs := w.observe(10, []core.InstanceView{
		pushTestView(1, session.Running),
		pushTestView(2, session.Ready),
		pushTestView(3, session.Running),
	})
	assert.Empty(t, jobs, "a new session that is Ready or Running is nothing to push")

	jobs = w.observe(10, []core.InstanceView{
		pushTestView(1, session.Running),
		pushTestView(2, session.Ready),
		pushTestView(3, session.Running),
		pushTestView(4, session.Prompting),
	})
	require.Len(t, jobs, 1, "a new session that already waits for you is a waiting push")
	assert.Equal(t, core.InstanceID(4), jobs[0].Session)
	assert.Equal(t, pushWaiting, jobs[0].Kind)
}

func TestPushWatch_WorkspaceTerminalsNeverPushOrCount(t *testing.T) {
	w := newPushWatch()
	terminal := pushTestView(1, session.Running)
	terminal.IsWorkspaceTerminal = true
	w.observe(10, []core.InstanceView{terminal, pushTestView(2, session.Running)})

	terminal.Status = session.Prompting
	assert.Empty(t, w.observe(10, []core.InstanceView{terminal, pushTestView(2, session.Running)}))

	terminal.Status = session.Running
	w.observe(10, []core.InstanceView{terminal, pushTestView(2, session.Running)})
	terminal.Status = session.Ready
	assert.Empty(t, w.observe(10, []core.InstanceView{terminal, pushTestView(2, session.Running)}))

	terminal.Status = session.Prompting
	jobs := w.observe(10, []core.InstanceView{terminal, pushTestView(2, session.Prompting)})
	require.Len(t, jobs, 1)
	assert.Equal(t, core.InstanceID(2), jobs[0].Session)
	assert.Equal(t, 1, jobs[0].Badge, "the terminal waiting is not a session that needs you")
}

func TestPushWatch_EachWorkspaceHasItsOwnBaseline(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})

	assert.Empty(t, w.observe(20, []core.InstanceView{pushTestView(2, session.Prompting)}),
		"a workspace first seen later is a baseline of its own, even with a session already waiting")
	require.Len(t, w.observe(10, []core.InstanceView{pushTestView(1, session.Prompting)}), 1)
}

func TestPushWatch_AWorkspaceNoLongerServedStartsOverAsABaseline(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})
	w.observe(20, []core.InstanceView{pushTestView(2, session.Running)})

	w.forget([]core.WorkspaceView{{ID: 20}})

	assert.Empty(t, w.observe(10, []core.InstanceView{pushTestView(1, session.Prompting)}),
		"served again, workspace 10 is a baseline, not a transition")
	require.Len(t, w.observe(20, []core.InstanceView{pushTestView(2, session.Prompting)}), 1,
		"workspace 20 was kept")
}

func TestPushWatch_TheBadgeCountsEverySessionThatNeedsYou(t *testing.T) {
	w := newPushWatch()
	// Workspace 10 also holds a started Ready session (your turn) and a Paused
	// one: neither needs you, so neither is counted.
	w.observe(10, []core.InstanceView{
		pushTestView(1, session.Running), pushTestView(2, session.Prompting),
		pushTestView(5, session.Ready), pushTestView(6, session.Paused),
	})
	w.observe(20, []core.InstanceView{pushTestView(3, session.Running)})

	jobs := w.observe(20, []core.InstanceView{pushTestView(3, session.Prompting)})
	require.Len(t, jobs, 1)
	assert.Equal(t, 2, jobs[0].Badge, "one waiting in workspace 10 and this one; a Ready and a Paused session are not counted")

	// Workspace 10's waiting session moves on: the count falls with it.
	w.observe(10, []core.InstanceView{
		pushTestView(1, session.Running), pushTestView(2, session.Running),
		pushTestView(5, session.Ready), pushTestView(6, session.Paused),
	})
	jobs = w.observe(20, []core.InstanceView{pushTestView(3, session.Prompting), pushTestView(4, session.Prompting)})
	require.Len(t, jobs, 1)
	assert.Equal(t, 2, jobs[0].Badge, "sessions 3 and 4 in workspace 20")
}

func TestPushWatch_Texts(t *testing.T) {
	waiting := func(title, reason, detail string) pushJob {
		w := newPushWatch()
		w.observe(10, nil)
		v := pushTestView(1, session.Prompting)
		v.Title, v.WaitReason, v.WaitDetail = title, reason, detail
		jobs := w.observe(10, []core.InstanceView{v})
		require.Len(t, jobs, 1)
		return jobs[0]
	}

	assert.Equal(t, "Waiting for you", waiting("t", "", "").Body, "no reason and no summary")
	assert.Equal(t, "sandbox request", waiting("t", "sandbox request", "").Body)
	assert.Equal(t, "rm -rf build", waiting("t", "", "rm -rf build").Body)
	assert.Equal(t, "loom", waiting("  \n ", "", "").Title, "a title of white space falls back")
	assert.Equal(t, "a b c", waiting("a\tb\x1b\n c", "", "").Title, "control characters and runs of white space flatten")

	long := waiting(strings.Repeat("t", 200), "", strings.Repeat("é", 500))
	assert.Equal(t, strings.Repeat("t", 79)+"…", long.Title)
	assert.Equal(t, strings.Repeat("é", 199)+"…", long.Body)
}

func TestPushJob_PayloadIsTheContractsJSON(t *testing.T) {
	raw, err := pushJob{Kind: pushWaiting, Session: 42, Title: "fix", Body: "permission: Bash", Badge: 3}.payload()
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"waiting","session":42,"title":"fix","body":"permission: Bash","badge":3,"url":"/#s/42"}`, string(raw))
}

func TestPushJob_HeadersFollowTheKind(t *testing.T) {
	waiting := pushJob{Kind: pushWaiting, Session: 42}
	finished := pushJob{Kind: pushFinished, Session: 9007199254740991}

	assert.Equal(t, "high", waiting.urgency())
	assert.Equal(t, "normal", finished.urgency())
	assert.Equal(t, "AAAAAAAAACo", waiting.topic())
	assert.Equal(t, "AB________8", finished.topic())
}

// Apple refuses a Topic whose length is 1 mod 4 (spec, "Push"), which "s<id>"
// has for every 4- and 8-digit ID. The topic is the ID's 8 bytes as unpadded
// base64url: 11 characters for IDs of every size, and one per session.
func TestPushJob_TopicHasALengthAppleTakes(t *testing.T) {
	seen := map[string]core.InstanceID{}
	for _, id := range []core.InstanceID{0, 1, 9, 10, 999, 1000, 9999, 12345678, 99999999, 9007199254740991} {
		topic := pushJob{Session: id}.topic()
		assert.Len(t, topic, 11, "id %d", id)
		raw, err := base64.RawURLEncoding.DecodeString(topic)
		require.NoError(t, err, "id %d", id)
		require.Len(t, raw, 8, "id %d", id)
		assert.Equal(t, uint64(id), binary.BigEndian.Uint64(raw), "id %d", id)
		assert.NotContains(t, seen, topic, "id %d shares a topic", id)
		seen[topic] = id
	}
}

// TestPush_AFreshMockupLinksItsTab: a push opens the session's mockup tab
// while its companion's screen changed in the latest turn and no dialog
// waits on the session view; otherwise the session view.
func TestPush_AFreshMockupLinksItsTab(t *testing.T) {
	fresh := core.MockupView{Screen: "layout.html", Fresh: true}
	for _, tc := range []struct {
		name string
		v    core.InstanceView
		url  string
	}{
		{"fresh", core.InstanceView{ID: 4, Mockup: fresh}, "/#s/4/mockup"},
		{"stale", core.InstanceView{ID: 4, Mockup: core.MockupView{Screen: "layout.html"}}, "/#s/4"},
		{"fresh but an ask is open", core.InstanceView{ID: 4, Mockup: fresh, Asks: []core.AskView{{}}}, "/#s/4"},
		{"none", core.InstanceView{ID: 4}, "/#s/4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := pushJob{Kind: pushFinished, Session: 4, Mockup: opensMockup(tc.v)}.payload()
			require.NoError(t, err)
			var p pushPayload
			require.NoError(t, json.Unmarshal(raw, &p))
			assert.Equal(t, tc.url, p.URL)
		})
	}

	for _, kind := range []struct {
		name     string
		from, to session.Status
	}{{"finished", session.Running, session.Ready}, {"waiting", session.Running, session.Prompting}} {
		t.Run("a "+kind.name+" push of a fresh mockup", func(t *testing.T) {
			w := newPushWatch()
			w.observe(10, []core.InstanceView{pushTestView(1, kind.from)})
			after := pushTestView(1, kind.to)
			after.Mockup = fresh
			jobs := w.observe(10, []core.InstanceView{after})
			require.Len(t, jobs, 1)
			assert.True(t, jobs[0].Mockup)
		})
	}
}

// verdictView is a started Ready session whose turn at stop has verdict
// (pending when verdict is "pending").
func verdictView(id core.InstanceID, stop time.Time, verdict, gist string) core.InstanceView {
	v := pushTestView(id, session.Ready)
	v.HasLastMessage, v.VerdictStop = true, stop
	if verdict == "pending" {
		v.VerdictPending = true
	} else {
		v.Verdict, v.VerdictGist = verdict, gist
	}
	return v
}

func TestPushWatch_AVerdictPushesOncePerTurn(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})
	stop := time.Now().Add(time.Minute) // after the baseline
	assert.Empty(t, w.observe(10, []core.InstanceView{verdictView(1, stop, "pending", "")}), "the finished push waits for the verdict")

	asks := verdictView(1, stop, attention.NeedsYou, "choose A or B")
	jobs := w.observe(10, []core.InstanceView{asks})
	require.Len(t, jobs, 1)
	assert.Equal(t, pushJob{Kind: pushWaiting, Session: 1, Title: "fix the build", Body: "asks: choose A or B", Badge: 1}, jobs[0])
	assert.Equal(t, "high", jobs[0].urgency())
	assert.Empty(t, w.observe(10, []core.InstanceView{asks}), "the same turn pushes once")
}

func TestPushWatch_ACoalescedVerdictStillPushesOnce(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})
	stop := time.Now().Add(time.Minute)
	done := verdictView(1, stop, attention.Done, "fixed the test")
	jobs := w.observe(10, []core.InstanceView{done})
	require.Len(t, jobs, 1, "Running to a verdict in one projection")
	assert.Equal(t, pushDone, jobs[0].Kind)
	assert.Equal(t, "Done: fixed the test", jobs[0].Body)
	assert.Equal(t, "normal", jobs[0].urgency())
	assert.Empty(t, w.observe(10, []core.InstanceView{done}))
}

func TestPushWatch_WaitingAndStopped(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running), pushTestView(2, session.Running)})
	stop := time.Now().Add(time.Minute)
	w.observe(10, []core.InstanceView{verdictView(1, stop, "pending", ""), verdictView(2, stop, "pending", "")})
	jobs := w.observe(10, []core.InstanceView{verdictView(1, stop, attention.Waiting, "job 12"), verdictView(2, stop, attention.Stopped, "")})
	require.Len(t, jobs, 1, "waiting pushes nothing")
	assert.Equal(t, pushJob{Kind: pushFinished, Session: 2, Title: "fix the build", Body: pushFinishedBody}, jobs[0])

	// A later turn with no Running edge between: stopped owes nothing.
	later := stop.Add(time.Minute)
	assert.Empty(t, w.observe(10, []core.InstanceView{verdictView(1, stop, attention.Waiting, "job 12"), verdictView(2, later, attention.Stopped, "")}))
}

func TestPushWatch_APendingCallWithNoVerdictPushesFinished(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})
	stop := time.Now().Add(time.Minute)
	w.observe(10, []core.InstanceView{verdictView(1, stop, "pending", "")})
	none := pushTestView(1, session.Ready)
	none.HasLastMessage, none.VerdictStop = true, stop
	jobs := w.observe(10, []core.InstanceView{none})
	require.Len(t, jobs, 1)
	assert.Equal(t, pushFinished, jobs[0].Kind)
	assert.Empty(t, w.observe(10, []core.InstanceView{none}))
}

func TestPushWatch_AReplyBeforeTheVerdictDropsThePush(t *testing.T) {
	w := newPushWatch()
	w.observe(10, []core.InstanceView{pushTestView(1, session.Running)})
	stop := time.Now().Add(time.Minute)
	w.observe(10, []core.InstanceView{verdictView(1, stop, "pending", "")})
	assert.Empty(t, w.observe(10, []core.InstanceView{pushTestView(1, session.Running)}), "the person replied")
	assert.Empty(t, w.observe(10, []core.InstanceView{pushTestView(1, session.Running)}))
}

func TestPushWatch_ATurnEndedBeforeTheBaselineNeverPushes(t *testing.T) {
	w := newPushWatch()
	start := time.Now()
	w.now = func() time.Time { return start }
	w.observe(10, []core.InstanceView{pushTestView(1, session.Ready)})
	old := verdictView(1, start.Add(-time.Hour), attention.NeedsYou, "pick") // a replayed turn's verdict, after boot
	assert.Empty(t, w.observe(10, []core.InstanceView{old}))
	assert.Equal(t, 1, w.needs[10], "it still counts toward the badge")

	fresh := verdictView(1, start.Add(time.Minute), attention.NeedsYou, "pick again")
	assert.Len(t, w.observe(10, []core.InstanceView{fresh}), 1, "a turn after the baseline pushes")
}

func TestPushAllows_DoneFollowsTheFinishedToggle(t *testing.T) {
	now := time.Now()
	assert.True(t, pushAllows(core.NotifySettings{Finished: true}, pushDone, now))
	assert.False(t, pushAllows(core.NotifySettings{Waiting: true}, pushDone, now))
}

var escalationAt = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

// escalationView is a served workspace whose watcher summary is loaded and
// lists escs.
func escalationView(id core.WorkspaceID, label string, escs ...core.WatchEscalation) core.WorkspaceView {
	return core.WorkspaceView{ID: id, Label: label, Watch: core.WatchSummary{On: true, Loaded: true, Escalations: escs}}
}

// unloadedView is a workspace whose watch switch is on but whose memory has
// not loaded yet (its first poll has not run): the summary lists nothing.
func unloadedView(id core.WorkspaceID, label string) core.WorkspaceView {
	return core.WorkspaceView{ID: id, Label: label, Watch: core.WatchSummary{On: true}}
}

func TestPushWatch_AnEscalationPushesOnce(t *testing.T) {
	w := newPushWatch()
	e14 := core.WatchEscalation{Issue: 14, Reason: "PR #40: ci nudge limit (3) reached", At: escalationAt}
	e15 := core.WatchEscalation{Issue: 15, Reason: "session heal 15 has been Recoverable since 2026-11-13T08:00:00Z", At: escalationAt.Add(time.Minute)}
	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a")}), "the first loaded projection is a baseline")

	jobs := w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14)})

	require.Len(t, jobs, 1)
	assert.Equal(t, pushJob{
		Kind:      pushEscalated,
		Title:     "ws-a: issue #14 escalated",
		Body:      "PR #40: ci nudge limit (3) reached",
		Workspace: 10,
		Issue:     14,
	}, jobs[0])
	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14)}), "published again, it pushes nothing")

	jobs = w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e15, e14)})
	require.Len(t, jobs, 1, "only the new escalation, newest first in the summary")
	assert.Equal(t, 15, jobs[0].Issue)
}

func TestPushWatch_EscalationsHeldAtTheBaselineNeverPush(t *testing.T) {
	w := newPushWatch()
	e14 := core.WatchEscalation{Issue: 14, Reason: "r", At: escalationAt}

	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{unloadedView(10, "ws-a")}), "before the first poll nothing is known")
	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14)}),
		"the memory has loaded and holds an old escalation: it is the baseline, not news")
	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14)}))

	e15 := core.WatchEscalation{Issue: 15, Reason: "r", At: escalationAt.Add(time.Hour)}
	require.Len(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e15, e14)}), 1,
		"the baseline did not blind the watch to a later one")
}

func TestPushWatch_EscalationsAreReBaselinedWhenTheWatchReloads(t *testing.T) {
	w := newPushWatch()
	e14 := core.WatchEscalation{Issue: 14, Reason: "r", At: escalationAt}
	w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a")})

	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{unloadedView(10, "ws-a")}), "the switch is off, or its memory not yet loaded")
	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14)}),
		"loaded again with an old escalation: a new baseline, not a flood")

	e15 := core.WatchEscalation{Issue: 15, Reason: "r", At: escalationAt.Add(time.Hour)}
	require.Len(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e15, e14)}), 1)
}

func TestPushWatch_AnIssueEscalatedAgainPushesAgain(t *testing.T) {
	w := newPushWatch()
	first := core.WatchEscalation{Issue: 14, Reason: "r1", At: escalationAt}
	w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a")})
	w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", first)})

	again := core.WatchEscalation{Issue: 14, Reason: "r2", At: escalationAt.Add(48 * time.Hour)}
	jobs := w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", again)})

	require.Len(t, jobs, 1, "the same issue at another time is another escalation")
	assert.Equal(t, "r2", jobs[0].Body)
}

func TestPushWatch_EachWorkspaceHasItsOwnEscalationBaseline(t *testing.T) {
	w := newPushWatch()
	e14 := core.WatchEscalation{Issue: 14, Reason: "r", At: escalationAt}
	w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a")})

	jobs := w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14), escalationView(20, "ws-b", e14)})

	require.Len(t, jobs, 1, "workspace 20 is first seen here, with the escalation already held: its baseline")
	assert.Equal(t, core.WorkspaceID(10), jobs[0].Workspace)
}

func TestPushWatch_AWorkspaceNoLongerServedStartsOverAsAnEscalationBaseline(t *testing.T) {
	w := newPushWatch()
	e14 := core.WatchEscalation{Issue: 14, Reason: "r", At: escalationAt}
	e15 := core.WatchEscalation{Issue: 15, Reason: "r", At: escalationAt.Add(time.Hour)}
	w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a"), escalationView(20, "ws-b")})

	w.forget([]core.WorkspaceView{{ID: 20}})

	assert.Empty(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e14), escalationView(20, "ws-b")}),
		"served again, workspace 10 is a baseline, not a transition")
	require.Len(t, w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", e15, e14), escalationView(20, "ws-b")}), 1)
}

func TestPushWatch_AnEscalationPushCarriesTheBadgeOfSessionsThatNeedYou(t *testing.T) {
	w := newPushWatch()
	w.observe(30, []core.InstanceView{pushTestView(1, session.Prompting), pushTestView(2, session.Prompting)})
	w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a")})

	jobs := w.observeEscalations([]core.WorkspaceView{escalationView(10, "ws-a", core.WatchEscalation{Issue: 14, Reason: "r", At: escalationAt})})

	require.Len(t, jobs, 1)
	assert.Equal(t, 2, jobs[0].Badge, "a badge of 0 would clear the Home Screen icon of the sessions still waiting")
}

func TestPushWatch_EscalationTexts(t *testing.T) {
	w := newPushWatch()
	w.observeEscalations([]core.WorkspaceView{escalationView(10, ""), escalationView(11, strings.Repeat("x", 100))})
	long := strings.Repeat("y", 300)

	jobs := w.observeEscalations([]core.WorkspaceView{
		escalationView(10, "", core.WatchEscalation{Issue: 3, Reason: "  stuck\n\tfor  hours ", At: escalationAt}),
		escalationView(11, strings.Repeat("x", 100), core.WatchEscalation{Issue: 4, Reason: long, At: escalationAt}),
		escalationView(12, "ws-c"),
	})

	require.Len(t, jobs, 2)
	assert.Equal(t, "loom: issue #3 escalated", jobs[0].Title, "a workspace with no label")
	assert.Equal(t, "stuck for hours", jobs[0].Body, "one line")
	assert.Len(t, []rune(jobs[1].Title), 80, "the title is cut like a session's")
	assert.Len(t, []rune(jobs[1].Body), 200, "the reason is cut to 200 runes")
	assert.True(t, strings.HasSuffix(jobs[1].Body, "…"))

	w.observeEscalations([]core.WorkspaceView{escalationView(13, "ws-d")})
	jobs = w.observeEscalations([]core.WorkspaceView{escalationView(13, "ws-d", core.WatchEscalation{Issue: 5, At: escalationAt})})
	require.Len(t, jobs, 1)
	assert.Equal(t, "Escalated", jobs[0].Body, "an escalation with no reason still says something")
}

func TestPushJob_AnEscalationOpensTheFleetAndIsUrgent(t *testing.T) {
	job := pushJob{Kind: pushEscalated, Title: "ws-a: issue #14 escalated", Body: "PR #40: stuck", Badge: 2, Workspace: 10, Issue: 14}

	raw, err := job.payload()

	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"escalated","session":0,"title":"ws-a: issue #14 escalated","body":"PR #40: stuck","badge":2,"url":"/"}`, string(raw))
	assert.Equal(t, "high", job.urgency())
}

func TestPushJob_AnEscalationTopicIsPerIssueAndNeverASessions(t *testing.T) {
	assert.Equal(t, "gAAACgAAAA4", pushJob{Kind: pushEscalated, Workspace: 10, Issue: 14}.topic())

	seen := map[string]bool{}
	for _, ws := range []core.WorkspaceID{1, 10, 20} {
		for _, issue := range []int{1, 14, 15, 1000} {
			topic := pushJob{Kind: pushEscalated, Workspace: ws, Issue: issue}.topic()
			assert.Len(t, topic, 11, "workspace %d issue %d", ws, issue)
			raw, err := base64.RawURLEncoding.DecodeString(topic)
			require.NoError(t, err)
			require.Len(t, raw, 8)
			assert.NotZero(t, raw[0]&0x80, "the top bit marks an escalation's topic")
			assert.False(t, seen[topic], "workspace %d issue %d shares a topic", ws, issue)
			seen[topic] = true
		}
	}
	for _, id := range []core.InstanceID{0, 14, 9007199254740991} {
		raw, err := base64.RawURLEncoding.DecodeString(pushJob{Kind: pushWaiting, Session: id}.topic())
		require.NoError(t, err)
		assert.Zero(t, raw[0]&0x80, "session %d: a session's topic never has it", id)
	}
}
