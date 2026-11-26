package web

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// termPaired is when the devices of these tests were paired.
var termPaired = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

// modelWith is the batch the drain hands the terminals' part when the model
// publishes the switch and the paired devices.
func modelWith(on bool, devices ...core.WebDevice) []core.Event {
	return []core.Event{core.ModelChanged{View: core.ModelView{Web: core.WebView{Terminal: on, Devices: devices}}}}
}

func pairedDev(name string, at time.Time) core.WebDevice {
	return core.WebDevice{Name: name, Paired: at}
}

// newTestTerms is a part on a layer that has a listener's origin rule and no
// client, so the model is never asked for anything. Its grace is short.
func newTestTerms(t *testing.T) *terminals {
	t.Helper()
	l := &Layer{}
	rule := originSet{{scheme: "http", host: "loom.test"}}
	l.rule.Store(&rule)
	tm := newTerminals(l)
	tm.grace = 100 * time.Millisecond
	return tm
}

// isOn is the part's copy of the switch.
func (tm *terminals) isOn() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.on
}

// knows reports whether the part has read the device.
func (tm *terminals) knows(name string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	_, ok := tm.devices[name]
	return ok
}

// slotCount is how many slots are held now.
func (tm *terminals) slotCount() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.slots)
}

// endedWith is the ending the slot was given, and whether it was given one.
func (s *termSlot) endedWith() (termEnd, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ending, s.ended
}

// logBuf is a log's destination that tests read.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// plainLog writes to buf with no timestamp, so a test compares whole lines.
func plainLog(buf *logBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}))
}

func TestTerminals_ASlotNeedsTheSwitchOnAndAKnownDevice(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(false, pairedDev("phone", termPaired)))
	_, err := tm.reserve("phone")
	assert.ErrorIs(t, err, errTermOff, "the switch is off")

	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err, "on, for a device the part has read")
	require.NotNil(t, slot)

	_, err = tm.reserve("stranger")
	var se *statusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusServiceUnavailable, se.status, "a device the part has not read yet is asked to try again")
	assert.Contains(t, se.msg, "try again")
}

func TestTerminals_NoListenerRefusesASlot(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	_, err := tm.reserve("phone")
	require.NoError(t, err, "a listener runs")

	tm.l.rule.Store(nil)
	_, err = tm.reserve("phone")
	assert.ErrorIs(t, err, errTermsStopping, "a request that passed guard before the listener closed is not counted")
}

func TestTerminals_TheCapsCountSlotsPerDeviceAndInAll(t *testing.T) {
	tm := newTestTerms(t)
	names := []string{"a", "b", "c", "d", "e"}
	var devices []core.WebDevice
	for _, n := range names {
		devices = append(devices, pairedDev(n, termPaired))
	}
	tm.apply(modelWith(true, devices...))
	held := map[string][]*termSlot{}
	fill := func(name string) {
		for i := 0; i < maxTerminalsPerDevice; i++ {
			s, err := tm.reserve(name)
			require.NoError(t, err, "%s's slot %d", name, i+1)
			held[name] = append(held[name], s)
		}
	}

	fill("a")
	_, err := tm.reserve("a")
	assert.ErrorIs(t, err, errTermsFull, "a fifth slot for one device")
	fill("b")
	fill("c")
	fill("d")
	require.Equal(t, maxTerminals, tm.slotCount(), "fixture: four devices of four slots are 16 in all")

	_, err = tm.reserve("e")
	assert.ErrorIs(t, err, errTermsFull, "a device with none is refused at 16 in all")
	tm.release(held["d"][0])
	_, err = tm.reserve("e")
	assert.NoError(t, err, "a released slot is room for the next")
}

func TestTerminals_TheSwitchTurningOffEndsEverySlot(t *testing.T) {
	tm := newTestTerms(t)
	devices := []core.WebDevice{pairedDev("a", termPaired), pairedDev("b", termPaired)}
	tm.apply(modelWith(true, devices...))
	sa, err := tm.reserve("a")
	require.NoError(t, err)
	sb, err := tm.reserve("b")
	require.NoError(t, err)

	tm.apply(modelWith(true, devices...))
	_, endedA := sa.endedWith()
	_, endedB := sb.endedWith()
	assert.False(t, endedA || endedB, "a model change that leaves the switch on ends nothing")

	tm.apply(modelWith(false, devices...))
	for name, s := range map[string]*termSlot{"a": sa, "b": sb} {
		e, ended := s.endedWith()
		assert.True(t, ended, name)
		assert.Equal(t, termEndOff, e, name)
	}
}

func TestTerminals_ARevokedOrRepairedDeviceEndsItsSlotsOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after []core.WebDevice
	}{
		{"revoked", []core.WebDevice{pairedDev("keep", termPaired)}},
		{"paired again", []core.WebDevice{pairedDev("keep", termPaired), pairedDev("gone", termPaired.Add(time.Hour))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := newTestTerms(t)
			tm.apply(modelWith(true, pairedDev("keep", termPaired), pairedDev("gone", termPaired)))
			gone, err := tm.reserve("gone")
			require.NoError(t, err)
			keep, err := tm.reserve("keep")
			require.NoError(t, err)

			tm.apply(modelWith(true, tc.after...))

			e, ended := gone.endedWith()
			assert.True(t, ended, "the device that is gone or paired again")
			assert.Equal(t, termEndRevoked, e)
			_, ended = keep.endedWith()
			assert.False(t, ended, "another device's slot stays")
		})
	}
}

func TestTerminals_ASlotEndedBeforeItsConnectionExistsEndsItWhenItAttaches(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	tm.apply(modelWith(false, pairedDev("phone", termPaired)))

	open := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: open.open, before: func(c *termConn) { slot.attach(c) }})
	end, _, ce := rig.finish(t)

	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, open.calls(), "a connection ended before it began opens no attach")
}

func TestTerminals_ASlotThatIsNotEndedLetsItsConnectionWork(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	tm.apply(modelWith(true, pairedDev("phone", termPaired), pairedDev("other", termPaired)))

	open := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: open.open, before: func(c *termConn) { slot.attach(c) }})
	sendResize(t, rig.client, 100, 30)
	expectLive(t, rig.client)
	assert.Equal(t, [][2]int{{100, 30}}, open.calls())
}

func TestTerminals_CloseEndsEverythingRefusesNewSlotsAndWaitsForTheHandlers(t *testing.T) {
	tm := newTestTerms(t)
	tm.grace = 5 * time.Second
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	rig := startTermConn(t, termRigOpts{before: func(c *termConn) { slot.attach(c) }})

	done := make(chan struct{})
	go func() {
		tm.close()
		close(done)
	}()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	require.Never(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 150*time.Millisecond, 10*time.Millisecond, "close returned while a handler still held its slot")

	tm.release(slot)
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "close never returned after the last handler released")

	_, err = tm.reserve("phone")
	assert.ErrorIs(t, err, errTermsStopping, "a closed part counts nothing")
	tm.close() // a second close returns at once
}

func TestTerminals_CloseGivesUpOnAHandlerThatNeverReleases(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		tm.close()
		close(done)
	}()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "close waited past its grace")
	e, ended := slot.endedWith()
	assert.True(t, ended)
	assert.Equal(t, termEndStopping, e)
}

func TestTerminals_EndAllLeavesThePartOpenForTheNextListener(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)

	tm.endAll(termEndStopping)

	e, ended := slot.endedWith()
	assert.True(t, ended)
	assert.Equal(t, termEndStopping, e)
	_, err = tm.reserve("phone")
	assert.NoError(t, err, "a rebind ends the old connections and the new listener takes new ones")
}

// failingPty is a pty factory that cannot make one, and keeps the commands
// it was asked to start.
type failingPty struct{ cmds []*exec.Cmd }

func (p *failingPty) Start(cmd *exec.Cmd) (*os.File, error) {
	p.cmds = append(p.cmds, cmd)
	return nil, errors.New("no pty here")
}

func (p *failingPty) Close() {}

// masterPty hands the attach the master of a real pty pair.
type masterPty struct{ f *os.File }

func (p masterPty) Start(*exec.Cmd) (*os.File, error) { return p.f, nil }
func (masterPty) Close()                              {}

func TestTerminalOpen_AFailedHistoryDoesNotStopTheAttach(t *testing.T) {
	tm := newTestTerms(t)
	buf := &logBuf{}
	tm.lg = plainLog(buf)
	tm.exec = cmd_test.MockCmdExec{OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, errors.New("tmux is not answering") }}
	ptys := &failingPty{}
	tm.ptys = ptys

	sh, hist, err := tm.tmuxOpen("loom_term_live")(120, 40)

	require.Error(t, err)
	assert.True(t, sh == nil, "a nil attach inside the interface would be a shell that is not there")
	assert.Nil(t, hist)
	require.Len(t, ptys.cmds, 1, "the attach was tried although the history was not read")
	args := ptys.cmds[0].Args
	assert.Equal(t, []string{"attach-session", "-t", "=loom_term_live"}, args[len(args)-3:], "on the exact target")
	assert.Contains(t, buf.String(), "web.terminal_history_failed")
	assert.Contains(t, buf.String(), "web.terminal_attach_failed", "the cause is logged: the connection tells the app only that it could not attach")
}

func TestTerminalOpen_ReturnsTheHistoryAndTheAttach(t *testing.T) {
	master, slave, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = slave.Close() })
	tm := newTestTerms(t)
	tm.exec = cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "display-message":
			return []byte("2\n"), nil
		case "capture-pane":
			return []byte("one\ntwo\n"), nil
		}
		return nil, errors.New("an unexpected tmux command")
	}}
	tm.ptys = masterPty{master}

	sh, hist, err := tm.tmuxOpen("loom_term_live")(100, 30)

	require.NoError(t, err)
	require.NotNil(t, sh)
	t.Cleanup(func() { _ = sh.Close() })
	assert.Equal(t, "one\r\ntwo\r\n\x1b[0m", string(hist), "the rows above the screen, each ended by CR LF and the history closed by an SGR reset, as ShellHistory gives them")
}
