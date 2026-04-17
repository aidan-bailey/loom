package daemon

import (
	"claude-squad/log"
	"claude-squad/session"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSyncTracked_PausedInstance asserts paused instances are tracked
// across ticks without spawning a PTY. This is the DAEMON-05 regression
// guard: the previous behaviour reloaded + Started every instance every
// tick; the new behaviour tracks them once and never attaches a PTY to
// a paused one.
func TestSyncTracked_PausedInstance(t *testing.T) {
	dir := t.TempDir()
	tracked := map[string]*session.Instance{}
	everyN := log.NewEvery(time.Second)

	fresh := []session.InstanceData{
		{Title: "paused-ws", Status: session.Paused, IsWorkspaceTerminal: true, Program: "true"},
	}

	syncTracked(tracked, fresh, dir, everyN)
	assert.Len(t, tracked, 1)
	firstInst := tracked["paused-ws"]
	assert.NotNil(t, firstInst)

	// Second tick with identical data must not re-construct the instance.
	syncTracked(tracked, fresh, dir, everyN)
	assert.Same(t, firstInst, tracked["paused-ws"],
		"syncTracked must reuse the existing Instance across ticks (DAEMON-05)")
}

// TestSyncTracked_DropsDisappearedInstance asserts instances removed from
// disk are also dropped from the tracked map — otherwise the daemon
// would keep ticking HasUpdated on phantom tmux sessions.
func TestSyncTracked_DropsDisappearedInstance(t *testing.T) {
	dir := t.TempDir()
	tracked := map[string]*session.Instance{}
	everyN := log.NewEvery(time.Second)

	fresh := []session.InstanceData{
		{Title: "alpha", Status: session.Paused, IsWorkspaceTerminal: true, Program: "true"},
		{Title: "beta", Status: session.Paused, IsWorkspaceTerminal: true, Program: "true"},
	}
	syncTracked(tracked, fresh, dir, everyN)
	assert.Len(t, tracked, 2)

	// Remove beta — only alpha remains in fresh.
	syncTracked(tracked, fresh[:1], dir, everyN)
	assert.Len(t, tracked, 1)
	_, hasBeta := tracked["beta"]
	assert.False(t, hasBeta, "beta should be dropped when it disappears from disk")
}
