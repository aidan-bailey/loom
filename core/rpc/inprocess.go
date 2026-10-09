package rpc

import (
	"net"

	"github.com/aidan-bailey/loom/core"
)

// InProcess runs model, booted already (core.Model.Boot), as the daemon
// does (internal/daemon), inside this process: on its own loop
// (core.Start), served by a Server over one end of an in-memory pipe, with
// a Client on the other, which raises the model's loss (a panic reaches
// its caller). The loop starts its background work (core.Loop.Begin) once
// the client has connected, so nothing its first jobs publish (a Notice)
// goes to no one. It returns the client, which implements core.Core, and
// the function that stops all three. Tests use it for the production
// stack in one process.
func InProcess(model *core.Model) (*Client, func(), error) {
	loop := core.Start(model)
	c, stop, err := inProcess(loop, false)
	if err != nil {
		return nil, nil, err
	}
	loop.Begin()
	return c, stop, nil
}

// inProcess serves loop to a raising client over a pipe; synchronous
// makes the client ping around its local reads and casts
// (InProcessForTest).
func inProcess(loop *core.Loop, synchronous bool) (*Client, func(), error) {
	srv := NewServer(loop)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := dial(b, synchronous, true)
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
