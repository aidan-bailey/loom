package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/takeover"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// takeoverHome is a one-workspace home whose storage writes to cfgDir.
func takeoverHome(t *testing.T, cfgDir string) *home {
	t.Helper()
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)
	h := &home{
		ctx:        context.Background(),
		state:      stateDefault,
		menu:       ui.NewMenu(),
		errBox:     ui.NewErrBox(),
		fullScreen: &foregroundAttach{},
	}
	focusSlots(h, 0, slotOver(t, testWS(core.WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: "ws", ConfigDir: cfgDir},
		Storage: storage,
		Config:  config.DefaultConfig(),
		State:   state,
	})))
	// The quit's saves are the model's (core.Model.SaveForQuit): install it
	// with the slot's workspace as its one tab, as newHome would have.
	return wireCore(t, h)
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// A takeover is a quit another loom asked for: everything is saved before
// the newcomer, waiting on the lock, reads it.
func TestTakeover_SavesAndQuits(t *testing.T) {
	cfgDir := t.TempDir()
	h := takeoverHome(t, cfgDir)

	_, cmd := h.Update(takeoverMsg{by: takeover.Holder{PID: 42, TTY: "/dev/pts/13"}})

	assert.True(t, isQuit(cmd))
	require.NotNil(t, h.takenOverBy)
	assert.Equal(t, 42, h.takenOverBy.PID)
	_, err := os.Stat(filepath.Join(cfgDir, config.StateFileName))
	assert.NoError(t, err, "the workspace was saved before quitting")
}

// A save that fails keeps this loom running, as a failed q does: quitting
// would lose what it couldn't save. The newcomer times out waiting.
func TestTakeover_StaysWhenSaveFails(t *testing.T) {
	cfgDir := t.TempDir()
	h := takeoverHome(t, cfgDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the error toast's Cmd returns at once
	h.ctx = ctx
	h.errBox.SetSize(400, 1)
	require.NoError(t, os.Chmod(cfgDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })

	_, cmd := h.Update(takeoverMsg{by: takeover.Holder{PID: 42}})

	assert.False(t, isQuit(cmd))
	assert.Nil(t, h.takenOverBy)
	assert.Contains(t, h.errBox.String(), "pid 42")
}

// The request arrives on the listener's goroutine. A full-screen attach
// blocks the event loop until its tmux client exits, so the listener ends
// it before it asks the program to quit.
func TestTakeoverListener_EndsFullScreenAttachThenSends(t *testing.T) {
	fs := &foregroundAttach{}
	var events []string
	fs.set(func() { events = append(events, "attach ended") })
	var sent tea.Msg
	listener := takeoverListener(fs, func(msg tea.Msg) {
		events = append(events, "sent")
		sent = msg
	})

	listener(takeover.Holder{PID: 42})

	assert.Equal(t, []string{"attach ended", "sent"}, events)
	assert.Equal(t, takeoverMsg{by: takeover.Holder{PID: 42}}, sent)
}

func TestForegroundAttach_SetReleasesThePrevious(t *testing.T) {
	fs := &foregroundAttach{}
	calls := 0
	fs.set(func() { calls++ })
	fs.set(nil) // the attach returned: release its context
	assert.Equal(t, 1, calls)
	fs.end() // nothing in the foreground any more
	assert.Equal(t, 1, calls)

	var none *foregroundAttach // bare test homes have none
	none.set(nil)
	none.end()
}
