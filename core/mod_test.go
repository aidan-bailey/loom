package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withMod has a daemon serve the mod for the test: Claude launches can have
// one (modLaunch).
func withMod(t *testing.T) {
	t.Helper()
	session.SetModSocket("/tmp/loom-test.mod.sock")
	t.Cleanup(func() { session.SetModSocket("") })
}

// adoptLaunch delivers a scan naming launch with its SessionStart at started,
// as a restored instance adopts its launch (startedInst's mock tmux prepares
// no hooks folder). No session ID: a conversation change would save.
func adoptLaunch(t *testing.T, m *Model, inst *session.Instance, launch string, started time.Time) {
	t.Helper()
	m.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: session.HookScanResult{
		LaunchID: launch, Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSessionStart, Source: "startup", At: started}},
	}}}})
	got, hooked := inst.HookLaunch()
	require.True(t, hooked)
	require.Equal(t, launch, got)
}

// modInst is a started Claude instance m holds, on launch L1, which reported
// its SessionStart just now.
func modInst(t *testing.T, m *Model, title string) *session.Instance {
	t.Helper()
	inst := activeInst(t, m, title)
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	return inst
}

func cliMessage(req ReqID, text string, wait bool) *modMessage {
	return &modMessage{text: text, origin: OriginCLI, req: req, wait: wait}
}

// noticeFor is the Notice in events naming req, if any.
func noticeFor(events []Event, req ReqID) (Notice, bool) {
	for _, ev := range events {
		if n, ok := ev.(Notice); ok && n.Req == req {
			return n, true
		}
	}
	return Notice{}, false
}

// answers are the Answered events in out, in order.
func answers(out Out) []Answered {
	var as []Answered
	for _, ev := range out.Events {
		if a, ok := ev.(Answered); ok {
			as = append(as, a)
		}
	}
	return as
}

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func TestMod_AMessageWaitsForItsModAndRepliesWhenQueued(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.enqueue(inst, cliMessage(5, "hello", false))
	assert.Empty(t, replies(m.Drain()), "no reply until the mod has queued it")

	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "hello"}, msgs[0])

	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	assert.Equal(t, []Reply{{Req: 5, ID: m.idOf(inst)}}, replies(m.Drain()))

	again, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	assert.Empty(t, again, "a queued message leaves the inbox")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	assert.Empty(t, replies(m.Drain()), "a repeated report replies nothing more")
}

func TestMod_WaitHearsTheTurnsAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "hello", true))
	msgs, _, _ := m.ModInbox("L1")
	id := msgs[0].ID

	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "queued", ID: id},
		{Kind: "started", ID: id, TurnID: "t1"},
		{Kind: "complete", ID: id, TurnID: "t1", Text: "hi back", Reason: "answer"},
	}))

	out := m.Drain()
	assert.Equal(t, []Reply{{Req: 5, ID: m.idOf(inst)}}, replies(out))
	assert.Contains(t, out.Events, Answered{Req: 5, ID: m.idOf(inst), Text: "hi back", Reason: "answer"})
}

// Claude merges the messages queued during one turn into the next turn, so
// the mod reports one turn for several messages: each waiter hears it.
func TestMod_MessagesMergedIntoOneTurnEachHearItsAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "first", true))
	m.enqueue(inst, cliMessage(6, "second", true))
	msgs, _, _ := m.ModInbox("L1")
	require.Len(t, msgs, 2)
	a, b := msgs[0].ID, msgs[1].ID

	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "queued", ID: a},
		{Kind: "queued", ID: b},
		{Kind: "started", ID: a, TurnID: "t1"},
		{Kind: "started", ID: b, TurnID: "t1"},
	}))
	assert.Len(t, replies(m.Drain()), 2)
	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "complete", ID: a, TurnID: "t1", Text: "both done", Reason: "answer"},
		{Kind: "complete", ID: b, TurnID: "t1", Text: "both done", Reason: "answer"},
	}))

	id := m.idOf(inst)
	assert.Equal(t, []Answered{
		{Req: 5, ID: id, Text: "both done", Reason: "answer"},
		{Req: 6, ID: id, Text: "both done", Reason: "answer"},
	}, answers(m.Drain()))
}

func TestMod_AnUnknownLaunchIsRefused(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")

	assert.ErrorIs(t, m.ModHello("L9", ModHello{}), ErrModUnknownLaunch)
	_, _, err := m.ModInbox("L9")
	assert.ErrorIs(t, err, ErrModUnknownLaunch)
	assert.ErrorIs(t, m.ModEvents("", nil), ErrModUnknownLaunch)
}

// A newer poll, and a new message, end a held poll (its channel closes).
func TestMod_APollIsWokenByAMessageAndSupersededByAPoll(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	_, first, err := m.ModInbox("L1")
	require.NoError(t, err)
	_, second, err := m.ModInbox("L1")
	require.NoError(t, err)
	assert.True(t, closed(first), "a newer poll supersedes the held one")
	assert.False(t, closed(second))

	m.enqueue(inst, cliMessage(5, "hello", false))
	assert.True(t, closed(second), "a message wakes the held poll")
}

// Both sides of the grace: a pending launch inside it keeps its messages;
// past it, with no hello, a CLI message is refused and a script's is pasted.
func TestMod_PastTheGraceAModlessLaunchFallsBack(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	started := time.Now().Add(-time.Minute)
	adoptLaunch(t, m, inst, "L1", started)
	m.Drain()
	m.enqueue(inst, cliMessage(5, "from the cli", false))
	m.enqueue(inst, &modMessage{text: "from a script", asUser: true, origin: OriginScript, req: 6})

	m.modTick(started.Add(modGrace - time.Second))
	out := m.Drain()
	assert.Empty(t, replies(out), "inside the grace the launch is still pending")
	assert.Empty(t, out.Jobs)

	m.modTick(started.Add(modGrace))
	out = m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1, "the CLI message is refused at once; the script's is pasted by a job")
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "runs without the loom mod")
	require.Len(t, out.Jobs, 1, "the script's paste")
	msgs, _, _ := m.ModInbox("L1")
	assert.Empty(t, msgs)

	m.enqueue(inst, cliMessage(7, "later", false))
	rs = replies(m.Drain())
	require.Len(t, rs, 1, "an absent launch refuses a CLI message at once")
	assert.Equal(t, ReqID(7), rs[0].Req)
}

// The grace runs from the daemon's boot at the earliest: a restarted daemon
// re-reads an old SessionStart, and the mod needs time to reconnect.
func TestMod_TheGraceRunsFromTheBootAtTheEarliest(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now().Add(-time.Hour))
	m.bootedAt = time.Now()
	m.enqueue(inst, cliMessage(5, "hello", false))
	m.Drain()

	m.modTick(m.bootedAt.Add(modGrace - time.Second))
	assert.Empty(t, replies(m.Drain()))
	m.modTick(m.bootedAt.Add(modGrace))
	assert.Len(t, replies(m.Drain()), 1)
}

// A late hello brings an absent launch back: after a daemon restart the mod
// can reconnect after the grace. (A launch has a link only once a message or
// its mod names it, so the tick judges the grace after the first message.)
func TestMod_ALateHelloMakesTheLaunchLive(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now().Add(-time.Minute))
	m.enqueue(inst, cliMessage(5, "refused", false))
	m.modTick(time.Now())
	require.Len(t, replies(m.Drain()), 1, "past the grace with no hello: absent")

	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	m.enqueue(inst, cliMessage(6, "delivered", false))
	assert.Empty(t, replies(m.Drain()), "live again: it waits for the mod")
	msgs, _, _ := m.ModInbox("L1")
	require.Len(t, msgs, 1)
	assert.Equal(t, "delivered", msgs[0].Text)
}

// A launch that can't have a mod is absent from the start: here no daemon
// serves the mod.
func TestMod_ALaunchWithoutAModIsAbsentAtOnce(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()

	m.enqueue(inst, cliMessage(5, "hello", false))

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	assert.ErrorContains(t, rs[0].Err, "runs without the loom mod")
}

// A restored instance before its first scan has launch "": pending, and the
// scan's launch is the same process, not a relaunch.
func TestMod_ARestoredLaunchIsAdoptedInPlace(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "hello", false))
	assert.Empty(t, replies(m.Drain()), "pending until the scan names the launch")

	adoptLaunch(t, m, inst, "L1", time.Now())
	m.modTick(time.Now())
	assert.Empty(t, replies(m.Drain()), "the same launch: the message still waits")
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
}

func TestMod_AStoppedSessionRefusesWhatItHeld(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "never queued", false))
	m.enqueue(inst, cliMessage(6, "queued, waiting", true))
	msgs, wake, _ := m.ModInbox("L1")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[1].ID}}))
	m.Drain()

	require.NoError(t, inst.TransitionTo(session.Paused))
	m.modTick(time.Now())

	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "stopped before the message reached it")
	n, ok := noticeFor(out.Events, 6)
	require.True(t, ok, "the waiter hears its turn will not end")
	assert.ErrorContains(t, n.Err, "stopped before its turn ended")
	assert.True(t, closed(wake), "a held poll ends")
}

// A message the mod could not submit falls back as a modless launch's does;
// one Claude dropped after it was queued is reported to the waiter.
func TestMod_FailedMessages(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "refused by claude", false))
	m.enqueue(inst, cliMessage(6, "dropped later", true))
	msgs, _, _ := m.ModInbox("L1")
	m.Drain()

	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "failed", ID: msgs[0].ID, Error: "no"},
		{Kind: "queued", ID: msgs[1].ID},
		{Kind: "failed", ID: msgs[1].ID, Error: "dropped: a Stop stands"},
	}))

	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 2)
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "could not submit it: no")
	assert.Equal(t, Reply{Req: 6, ID: m.idOf(inst)}, rs[1])
	n, ok := noticeFor(out.Events, 6)
	require.True(t, ok)
	assert.ErrorContains(t, n.Err, "dropped: a Stop stands")
}

// A mod's report wakes the client, which must drain the Reply it produced.
func TestLoop_AModReportWakesTheClient(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "hello", false))
	m.Drain()
	l := StartForTest(m)
	t.Cleanup(l.Stop)
	msgs, _, err := l.ModInbox("L1")
	require.NoError(t, err)
	for len(l.Wakes()) > 0 {
		<-l.Wakes()
	}

	require.NoError(t, l.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))

	select {
	case <-l.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake after a mod's report")
	}
	assert.Len(t, replies(Out{Events: l.Sync()}), 1)
}

// A mod's call that queued no event wakes no client: polls come every few
// seconds per session, and a wake has the server diff every view.
func TestLoop_AnIdleModCallWakesNoClient(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	l := StartForTest(m)
	t.Cleanup(l.Stop)
	for len(l.Wakes()) > 0 {
		<-l.Wakes()
	}

	require.NoError(t, l.ModHello("L1", ModHello{Mod: 1}))
	_, _, err := l.ModInbox("L1")
	require.NoError(t, err)
	require.NoError(t, l.ModEvents("L1", []ModEvent{{Kind: "started", ID: "m1", TurnID: "t1"}}))

	assert.Empty(t, l.Wakes(), "no wake waits")
}
