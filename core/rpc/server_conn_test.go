package rpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestServerConn_CoalescesQueuedState: a newer state event replaces an
// older one still queued, in its place; replies and other events keep
// their order.
func TestServerConn_CoalescesQueuedState(t *testing.T) {
	c := &serverConn{signal: make(chan struct{}, 1)}
	c.enqueue(Frame{Event: "ViewsChanged", Data: []byte(`"a"`)}, "views:1")
	c.enqueue(Frame{ID: 7}, "")
	c.enqueue(Frame{Event: "ViewsChanged", Data: []byte(`"b"`)}, "views:1")
	c.enqueue(Frame{Event: "ViewsChanged", Data: []byte(`"c"`)}, "views:2")
	c.enqueue(Frame{Event: "Notice"}, "")
	var got []string
	for _, q := range c.queue {
		got = append(got, q.key+"="+string(q.frame.Data))
	}
	assert.Equal(t, []string{`views:1="b"`, "=", `views:2="c"`, "="}, got)
	assert.Equal(t, uint64(7), c.queue[1].frame.ID)
}
