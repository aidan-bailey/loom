package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// pushActionFor used to return nil on success; the GitHub poller needs
// a signal to refresh right after a push, so success now yields
// ghRefreshMsg. An unstarted instance has no worktree, so the Cmd
// returns the error path — this pins that the two outcomes are
// distinguishable.
func TestPushActionFor_ErrorPathReturnsError(t *testing.T) {
	m := newTestHome(t)
	inst := addReadyInstance(t, m)
	msg := pushActionFor(inst)()
	_, isErr := msg.(error)
	assert.True(t, isErr, "no worktree on a test instance: expected error, got %T", msg)
}
