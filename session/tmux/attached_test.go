package tmux

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pairFactory attaches each client to a fresh testpty pair and keeps the
// peers, so a test can end a client's session by closing its peer.
type pairFactory struct {
	t     *testing.T
	mu    sync.Mutex
	peers []*os.File
}

func (f *pairFactory) Start(*exec.Cmd) (*os.File, error) {
	attach, peer := testpty.Pair(f.t)
	f.mu.Lock()
	f.peers = append(f.peers, peer)
	f.mu.Unlock()
	return attach, nil
}

func (f *pairFactory) Close() {}

// peer returns the peer of the i-th attach.
func (f *pairFactory) peer(i int) *os.File {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peers[i]
}

// quietRunner answers every tmux command with success and no output.
func quietRunner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// TestAttached_FollowsThePump: a client is attached from Restore until its
// pump stops on its own (the session ended) or PausePreview detaches it.
// PtmxAlive outlives the pump: it says only that there is a handle to
// close.
func TestAttached_FollowsThePump(t *testing.T) {
	f := &pairFactory{t: t}
	c := NewAttachClientWithDeps("loom_api", "claude", f, quietRunner())
	assert.False(t, c.Attached(), "nothing attached before Restore")

	require.NoError(t, c.Restore())
	assert.True(t, c.Attached(), "a fresh Restore is attached")

	require.NoError(t, f.peer(0).Close()) // the session ends
	require.Eventually(t, func() bool { return !c.Attached() }, 2*time.Second, 5*time.Millisecond,
		"the pump's EOF detaches the client")
	assert.True(t, c.PtmxAlive(), "while its handle stays open for a release to close")

	start := time.Now()
	require.NoError(t, c.Restore())
	assert.Less(t, time.Since(start), pumpWaitTimeout/2, "re-attaching never waits on the exited pump")
	assert.True(t, c.Attached(), "Restore re-attaches the same client")

	require.NoError(t, c.PausePreview())
	assert.False(t, c.Attached())
	assert.False(t, c.PtmxAlive())
}

// TestAttached_LateOldPumpExitLeavesTheNewAttach: a pump whose EOF raced
// a Restore can exit after waitPumpExit gave up on it (here, held in its
// Dead notification). Its exit marks only its own pump: the attach that
// Restore made meanwhile stays attached.
func TestAttached_LateOldPumpExitLeavesTheNewAttach(t *testing.T) {
	release := make(chan struct{})
	inDead := make(chan struct{}, 1)
	notifierGuard(t, Notifier{Dead: func(string) {
		inDead <- struct{}{}
		<-release
	}})
	f := &pairFactory{t: t}
	c := NewAttachClientWithDeps("loom_api", "claude", f, quietRunner())
	require.NoError(t, c.Restore())
	require.True(t, c.HasEmulator(), "fixture: the Dead event rides the emulator path")
	c.stateMu.Lock()
	old := c.pumpExited
	c.stateMu.Unlock()

	require.NoError(t, f.peer(0).Close())
	select {
	case <-inDead: // the old pump hit EOF, unrequested, and is stuck delivering it
	case <-time.After(2 * time.Second):
		t.Fatal("the old pump never reported its EOF")
	}

	require.NoError(t, c.Restore()) // gives up on the old pump after pumpWaitTimeout
	t.Cleanup(func() { _ = c.PausePreview() })
	require.True(t, c.Attached())
	require.False(t, old.Load(), "precondition: the old pump has not exited yet")

	close(release)
	require.Eventually(t, old.Load, 2*time.Second, 5*time.Millisecond, "the old pump marks its own exit")
	assert.True(t, c.Attached(), "and leaves the new attach alone")
}
