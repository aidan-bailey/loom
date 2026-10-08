package rpc

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
)

// TestCoalesceKeys_MatchTheReplicasStateEvents: the server coalesces
// exactly the events a replica keeps as state. A state event with no key
// would pile up in a slow client's queue; a key on an event the replica
// does not keep would replace one in place that a client needs in order.
func TestCoalesceKeys_MatchTheReplicasStateEvents(t *testing.T) {
	for _, ev := range core.EventTypes() {
		keyed := coalesceKey(ev) != ""
		kept := (&replica{}).apply(ev, &state{})
		assert.Equal(t, kept, keyed, "%T: the replica keeps it as state: %v; the server coalesces it: %v", ev, kept, keyed)
	}
}

// TestReplica_IgnoresViewsOfAWorkspaceThatIsNotLoaded: the queue coalesces a
// WorkspacesChanged in place, so one that closes a workspace can come ahead
// of a ViewsChanged queued for it; that must not bring the closed tab's
// views back.
func TestReplica_IgnoresViewsOfAWorkspaceThatIsNotLoaded(t *testing.T) {
	var r replica
	var changed state
	views := []core.InstanceView{{ID: 61, Title: "x"}}
	loaded := core.WorkspacesChanged{Views: []core.WorkspaceView{{ID: 6}}}

	assert.True(t, r.apply(loaded, &changed))
	assert.True(t, r.apply(core.ViewsChanged{WS: 6, Views: views}, &changed))
	assert.Len(t, r.views[6], 1, "a loaded workspace's views are kept")

	changed = state{}
	assert.True(t, r.apply(core.WorkspacesChanged{}, &changed), "the workspace is closed")
	assert.True(t, r.apply(core.ViewsChanged{WS: 6, Views: views}, &changed), "it is still a state event: consumed, not queued")
	assert.NotContains(t, r.views, core.WorkspaceID(6), "but a closed workspace keeps no views")
	assert.False(t, changed.views[6], "and nothing is marked changed")
	for _, ev := range r.events(changed) {
		_, isViews := ev.(core.ViewsChanged)
		assert.False(t, isViews, "so Sync hands out no ViewsChanged")
	}
}
