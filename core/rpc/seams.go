package rpc

import "github.com/aidan-bailey/loom/core"

// InProcessForTest is InProcess for a test of the client's user (the
// TUI): model runs on a loop that holds its jobs (core.StartForTest,
// returned for its seams) and has not begun (the test drives its jobs and
// tick by hand), and the client is synchronous, as the TUI's
// in-process calls were in stage 1E: it pings before every local read, so
// a read sees whatever the model published by then (a change the test
// made to the model directly included), and after every cast, so a cast
// has reached the model before the test reads it. Production code never
// calls it.
func InProcessForTest(model *core.Model) (*Client, *core.Loop, func(), error) {
	loop := core.StartForTest(model)
	c, stop, err := inProcess(loop, true)
	return c, loop, stop, err
}

// FlushForTest is the client's barrier (a ping): when it returns,
// everything the server published before answering is in the replica.
func (c *Client) FlushForTest() error { return c.ping() }
