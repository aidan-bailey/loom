package web

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// One permission prompt is one push, however its labels move under it: a
// failed roster query clears the reason and summary, and the next good answer
// brings them back. (The roster no longer relabels a hook's wait: the session
// package keeps the hook's reason.)
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
