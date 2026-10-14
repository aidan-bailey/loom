package core

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
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

// relaunchableInst is a started Claude instance m holds, on a mock tmux
// session, whose Restart relaunches it with a new hooks launch
// (prepareHooks: it has a config dir). It is a workspace terminal, the one
// kind Restart serves, so the relaunch touches no worktree.
func relaunchableInst(t *testing.T, m *Model, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: title, Path: t.TempDir(), Program: "claude", ConfigDir: t.TempDir(), IsWorkspaceTerminal: true,
	})
	require.NoError(t, err)
	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") && !created {
				return fmt.Errorf("session does not exist")
			}
			switch {
			case strings.Contains(s, "new-session"):
				created = true
			case strings.Contains(s, "kill-session"):
				created = false
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(""), nil },
	}
	inst.SetTmuxSessionForTest(tmux.NewSessionWithDeps(title, "claude", runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
	require.NoError(t, inst.Start(true))
	hold(m, inst)
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
	m.modTick(time.Now()) // a tick between the hello and the first poll: the hello counts as heard
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

// A restored launch no scan adopts has no hooks, so it never reports a
// SessionStart: its grace runs from the boot, and then it falls back.
func TestMod_AnUnadoptedRestoredLaunchsGraceRunsFromTheBoot(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	launch, _ := inst.HookLaunch()
	require.Empty(t, launch, "fixture: restored, never adopted")
	m.bootedAt = time.Now()
	m.enqueue(inst, cliMessage(5, "from the cli", false))
	m.enqueue(inst, &modMessage{text: "from a script", origin: OriginScript, req: 6})

	m.modTick(m.bootedAt.Add(modGrace - time.Second))
	out := m.Drain()
	assert.Empty(t, replies(out), "inside the grace it may still be adopted")
	assert.Empty(t, out.Jobs)

	m.modTick(m.bootedAt.Add(modGrace))
	out = m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1, "the CLI message is refused; the script's is pasted by a job")
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "runs without the loom mod")
	assert.Len(t, out.Jobs, 1, "the script's paste")
}

// An adopted launch's grace runs from its own SessionStart when that is
// later than the boot: a launch made long after the boot gets its grace.
func TestMod_AnAdoptedLaunchsGraceRunsFromItsSessionStart(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	m.bootedAt = time.Now().Add(-time.Hour)
	started := time.Now()
	adoptLaunch(t, m, inst, "L1", started)
	m.Drain()
	m.enqueue(inst, cliMessage(5, "hello", false))

	m.modTick(started.Add(modGrace - time.Second))
	assert.Empty(t, replies(m.Drain()), "inside its grace it waits for its mod")
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
}

// A restored launch of another program can't have the mod: absent at once,
// not pending until a grace.
func TestMod_ARestoredNonClaudeLaunchIsAbsentAtOnce(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := startedInst(t, "x", "aider")
	hold(m, inst)
	launch, _ := inst.HookLaunch()
	require.Empty(t, launch, "fixture: restored, never adopted")

	m.enqueue(inst, cliMessage(5, "hello", false))

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	assert.ErrorContains(t, rs[0].Err, "runs without the loom mod")
}

// A live mod that stops polling (its socket removed, its module unloaded,
// Claude's hooks worker gone) turns absent after ModPollTimeout: a message
// it never took falls back, one it took is never pasted (it may have reached
// Claude), and a turn's waiter hears the turn will not end. A hello brings
// it back.
func TestMod_ALiveModThatStopsPollingTurnsAbsent(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	m.mods[inst].heard = time.Now().Add(-time.Hour) // greeted long ago; only its polls keep it live
	m.enqueue(inst, cliMessage(5, "queued, waiting", true))
	m.enqueue(inst, &modMessage{text: "taken, never reported", origin: OriginScript, req: 6})
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	polled := time.Now()
	require.Len(t, msgs, 2)
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	m.enqueue(inst, &modMessage{text: "never taken", origin: OriginScript, req: 7})
	m.enqueue(inst, cliMessage(8, "never taken either", false))
	m.Drain()

	m.modTick(polled.Add(ModPollTimeout - time.Second))
	out := m.Drain()
	assert.Empty(t, out.Events, "a mod that polled recently stays live")
	assert.Empty(t, out.Jobs)

	held := m.mods[inst].wake
	m.modTick(time.Now().Add(ModPollTimeout))
	out = m.Drain()
	rs := replies(out)
	require.Len(t, rs, 2)
	assert.Equal(t, ReqID(6), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "may or may not have reached Claude", "a message the mod took is never pasted")
	assert.Equal(t, ReqID(8), rs[1].Req)
	assert.ErrorContains(t, rs[1].Err, "the loom mod stopped answering")
	assert.Len(t, out.Jobs, 1, "the message the mod never took is pasted")
	n, ok := noticeFor(out.Events, 5)
	require.True(t, ok, "the waiter hears its turn will not end")
	assert.ErrorContains(t, n.Err, "the loom mod stopped answering before its turn ended")
	assert.True(t, closed(held), "a held poll ends")

	m.enqueue(inst, cliMessage(9, "refused", false))
	require.Len(t, replies(m.Drain()), 1, "absent: a CLI message is refused at once")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	m.enqueue(inst, cliMessage(10, "delivered", false))
	assert.Empty(t, replies(m.Drain()), "live again after a hello")
}

// A relaunch ends the old launch's link: what it held is refused or told its
// turn will not end, and the new launch starts pending, served under its own
// ID.
func TestMod_ARelaunchEndsTheOldLaunchsLink(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := relaunchableInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	m.enqueue(inst, cliMessage(5, "never queued", false))
	m.enqueue(inst, cliMessage(6, "queued, waiting", true))
	msgs, _, _ := m.ModInbox("L1")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[1].ID}}))
	m.Drain()

	require.NoError(t, inst.Restart())
	next, hooked := inst.HookLaunch()
	require.True(t, hooked, "fixture: the relaunch prepared hooks")
	require.NotEqual(t, "L1", next)
	m.modTick(time.Now())

	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "the session relaunched before the message reached it")
	n, ok := noticeFor(out.Events, 6)
	require.True(t, ok)
	assert.ErrorContains(t, n.Err, "the session relaunched before its turn ended")

	m.enqueue(inst, cliMessage(7, "to the new launch", false))
	assert.Empty(t, replies(m.Drain()), "the new launch is pending")
	_, _, err := m.ModInbox("L1")
	assert.ErrorIs(t, err, ErrModUnknownLaunch, "the old launch is gone")
	got, _, err := m.ModInbox(next)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "to the new launch", got[0].Text)
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
