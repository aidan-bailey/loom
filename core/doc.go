// Package core is loom's session model: the loaded workspaces and their
// instances, and everything that acts on them without a terminal:
// loading and saving, reconcile and the sweeps, the lifecycle operations
// and their completions, the health tick and the background jobs.
//
// Since daemon stage 1E the model runs on a goroutine of its own: a Loop
// owns it, serves every Core method as a round trip over that goroutine,
// starts the Jobs the model queues on goroutines of their own and
// delivers their results on the loop (Deliver), and fires the health tick
// from its own timer. A Job reads no model state. The model reports to
// its client (the TUI, package app) through Events, which the client
// drains (Sync) when the loop wakes it (Loop.Wakes) and after each of its
// own messages. Core's own tests drive a Model directly, on their own
// goroutine, with Sync, Drain and Deliver.
//
// core imports nothing of the TUI (TestCoreImportsNoUI): the daemon
// (loom serve) runs it with no terminal. Focus is the TUI's: where an
// operation needs "the workspace the user is looking at", the caller
// passes it.
package core
