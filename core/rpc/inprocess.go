package rpc

import (
	"net"

	"github.com/aidan-bailey/loom/core"
)

// InProcess runs model as a daemon will (stage 3), inside this process:
// on its own loop (core.Start), served by a Server over one end of an
// in-memory pipe, with a Client on the other. It returns the client,
// which implements core.Core, and the function that stops all three.
func InProcess(model *core.Model) (*Client, func(), error) {
	return inProcess(core.Start(model), false)
}

// inProcess serves loop to a client over a pipe; synchronous makes the
// client ping around its local reads and casts (InProcessForTest).
func inProcess(loop *core.Loop, synchronous bool) (*Client, func(), error) {
	srv := NewServer(loop)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := dial(b, synchronous)
	if err != nil {
		srv.Close()
		loop.Stop()
		return nil, nil, err
	}
	stop := func() {
		c.Close()
		srv.Close()
		loop.Stop()
	}
	return c, stop, nil
}
