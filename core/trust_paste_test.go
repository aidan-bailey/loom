package core

import (
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promptPane is a tmux whose pane shows screens[i] at the i-th capture (the
// last one from then on), or fails every capture with fail, and that
// records the paste and the keys a prompt's send makes, with how many
// captures came before each.
type promptPane struct {
	mu       sync.Mutex
	screens  []string
	fail     error
	captures int
	pastes   []int // the capture count at each paste-buffer
	enters   int
}

func (p *promptPane) runner() cmd_test.MockCmdExec {
	output := func(c *exec.Cmd) ([]byte, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "capture-pane":
			if p.fail != nil {
				return nil, p.fail
			}
			screen := p.screens[min(p.captures, len(p.screens)-1)]
			p.captures++
			return []byte(screen), nil
		case "paste-buffer":
			p.pastes = append(p.pastes, p.captures)
		case "send-keys":
			p.enters++
		}
		return nil, nil
	}
	return cmd_test.MockCmdExec{
		RunFunc:            func(*exec.Cmd) error { return nil },
		OutputFunc:         output,
		CombinedOutputFunc: output,
	}
}

func (p *promptPane) seen() (captures int, pastes []int, enters int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.captures, append([]int(nil), p.pastes...), p.enters
}

// paneInst is a started, Running Claude instance titled "a", held by m, on
// pane: no tmux server contacted.
func paneInst(t *testing.T, m *Model, pane *promptPane) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	hold(m, inst)
	inst.SetTmuxSessionForTest(tmux.NewSessionWithDeps("a", "claude", fakePtyFactory{t: t}, pane.runner()))
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")
	return inst
}

// shortFirstPrompt shortens a first prompt's wait for a trust prompt.
func shortFirstPrompt(t *testing.T, every, within time.Duration) {
	t.Helper()
	prevEvery, prevFor := firstPromptPollEvery, firstPromptWaitFor
	firstPromptPollEvery, firstPromptWaitFor = every, within
	t.Cleanup(func() { firstPromptPollEvery, firstPromptWaitFor = prevEvery, prevFor })
}

// noticesAndStarts are the Notices and Started events in events.
func noticesAndStarts(events []Event) (notices []Notice, starts []Started) {
	for _, ev := range events {
		switch e := ev.(type) {
		case Notice:
			notices = append(notices, e)
		case Started:
			starts = append(starts, e)
		}
	}
	return notices, starts
}

// A start's first prompt waits while a trust prompt is open, since its
// Enter would confirm whatever the prompt selects ("No, exit" included),
// and is pasted once the prompt has cleared; with none open it is pasted
// at once.
func TestFirstPrompt_WaitsForAnOpenTrustPromptToClear(t *testing.T) {
	shortFirstPrompt(t, time.Millisecond, time.Minute)
	t.Run("open, then cleared", func(t *testing.T) {
		m := NewForTest(Options{})
		pane := &promptPane{screens: []string{claudeTrustScreen, claudeTrustScreen, "❯ \n? for shortcuts"}}
		inst := paneInst(t, m, pane)

		result := m.sendInitialPrompt(inst, m.workspaces[0], "do the thing")()

		assert.Equal(t, promptSent{inst: inst, owner: m.workspaces[0]}, result)
		captures, pastes, enters := pane.seen()
		assert.Equal(t, 3, captures, "it looked until the prompt had cleared")
		assert.Equal(t, []int{3}, pastes, "and pasted only then")
		assert.Equal(t, 1, enters)
	})
	t.Run("none open", func(t *testing.T) {
		m := NewForTest(Options{})
		pane := &promptPane{screens: []string{"❯ \n? for shortcuts"}}
		inst := paneInst(t, m, pane)

		m.sendInitialPrompt(inst, m.workspaces[0], "do the thing")()

		captures, pastes, _ := pane.seen()
		assert.Equal(t, 1, captures)
		assert.Equal(t, []int{1}, pastes, "pasted at once")
	})
	t.Run("a capture that fails does not block the paste", func(t *testing.T) {
		m := NewForTest(Options{})
		pane := &promptPane{screens: []string{""}, fail: errors.New("tmux: no answer")}
		inst := paneInst(t, m, pane)

		m.sendInitialPrompt(inst, m.workspaces[0], "do the thing")()

		_, pastes, _ := pane.seen()
		assert.Len(t, pastes, 1, "pasted as before the check")
	})
}

// A trust prompt still open at the bound is never pasted into: the start
// finishes (Started, once), and the requester hears that the prompt was
// not sent, with its text, so nothing is lost silently.
func TestFirstPrompt_StillOpenAtTheBoundIsNotPasted(t *testing.T) {
	shortFirstPrompt(t, time.Millisecond, 20*time.Millisecond)
	m := NewForTest(Options{})
	pane := &promptPane{screens: []string{claudeTrustScreen}}
	inst := paneInst(t, m, pane)

	m.causedBy(7, func() { m.Deliver(m.sendInitialPrompt(inst, m.workspaces[0], "do the thing")()) })

	captures, pastes, enters := pane.seen()
	assert.Greater(t, captures, 1, "it waited for the prompt to clear")
	assert.Empty(t, pastes, "nothing pasted into the open prompt")
	assert.Zero(t, enters, "and no Enter sent")
	notices, starts := noticesAndStarts(m.Drain().Events)
	require.Len(t, starts, 1, "the start finishes, once")
	assert.Equal(t, ReqID(7), starts[0].Req)
	require.Len(t, notices, 1)
	assert.Equal(t, ReqID(7), notices[0].Req, "the requester hears it")
	assert.Nil(t, notices[0].Err, "an info: the session started")
	assert.Equal(t, "the first prompt for a was not sent: its trust prompt is still open. Answer it, then send the prompt yourself: do the thing", notices[0].Info)
}

// A daemon's stop (Loop.Quiesce) ends a first prompt's wait at once: the
// quiesce would otherwise wait out its timeout on it, then drop it.
func TestFirstPrompt_AStopEndsTheWait(t *testing.T) {
	shortFirstPrompt(t, time.Millisecond, time.Minute)
	m := NewForTest(Options{})
	pane := &promptPane{screens: []string{claudeTrustScreen}}
	inst := paneInst(t, m, pane)
	job := m.sendInitialPrompt(inst, m.workspaces[0], "do the thing")
	done := make(chan any, 1)
	go func() { done <- job() }()
	require.Eventually(t, func() bool { c, _, _ := pane.seen(); return c > 2 }, 5*time.Second, time.Millisecond, "the job waits")

	m.beginStop()

	select {
	case result := <-done:
		sent, ok := result.(promptSent)
		require.True(t, ok)
		assert.NotEmpty(t, sent.unsent, "not sent")
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not end the wait")
	}
	_, pastes, _ := pane.seen()
	assert.Empty(t, pastes)
}

// Someone waits on a send (the overlay, the quick bar, a script's
// fallback), so an open trust prompt refuses it at once, and nothing is
// pasted; with none open it is pasted.
func TestSendPromptInst_RefusesAnOpenTrustPrompt(t *testing.T) {
	m := NewForTest(Options{})
	pane := &promptPane{screens: []string{claudeTrustScreen}}
	inst := paneInst(t, m, pane)

	result := m.sendPromptInst(inst, "hi")()

	failed, ok := result.(promptFailed)
	require.True(t, ok, "refused")
	assert.EqualError(t, failed.err, "prompt not sent to a: its trust prompt is open: answer it in the pane first")
	_, pastes, enters := pane.seen()
	assert.Empty(t, pastes)
	assert.Zero(t, enters)

	m2 := NewForTest(Options{})
	clear := &promptPane{screens: []string{"❯ \n? for shortcuts"}}
	assert.Nil(t, m2.sendPromptInst(paneInst(t, m2, clear), "hi")(), "sent")
	_, pastes, _ = clear.seen()
	assert.Equal(t, []int{1}, pastes)
}

// A daemon's stop quiesces the loop, which waits for foreground jobs: a
// first prompt waiting out an open trust prompt is one, and the quiesce's
// start ends its wait, so the stop neither waits out its timeout on it nor
// drops it unheard.
func TestLoop_QuiesceEndsAFirstPromptsWait(t *testing.T) {
	shortFirstPrompt(t, time.Millisecond, time.Minute)
	m := NewForTest(Options{})
	pane := &promptPane{screens: []string{claudeTrustScreen}}
	inst := paneInst(t, m, pane)
	l := startLoop(m, false, 0)
	t.Cleanup(l.Stop)
	var landed []any
	l.do(func(m *Model) {
		l.deliver = func(r any) { landed = append(landed, r) }
		m.spawn(m.sendInitialPrompt(inst, m.workspaces[0], "do the thing"))
	})
	require.Eventually(t, func() bool { c, _, _ := pane.seen(); return c > 2 }, 5*time.Second, time.Millisecond, "the job waits")

	start := time.Now()
	require.True(t, l.Quiesce(10*time.Second), "the job landed")

	assert.Less(t, time.Since(start), 5*time.Second, "without waiting out the bound")
	l.do(func(*Model) {
		require.Len(t, landed, 1)
		sent, ok := landed[0].(promptSent)
		require.True(t, ok)
		assert.NotEmpty(t, sent.unsent, "and unsent, with its notice")
	})
	_, pastes, _ := pane.seen()
	assert.Empty(t, pastes)
}
