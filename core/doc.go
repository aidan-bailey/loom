// Package core is loom's session model: the loaded workspaces and their
// instances, and everything that acts on them without a terminal:
// loading and saving, reconcile and the sweeps, the lifecycle operations
// and their completions, the health tick and the background jobs.
//
// In daemon stage 1B the TUI (package app) drives the model synchronously
// on its Update goroutine, so every Model method must be called there. A
// Job is the one thing that runs elsewhere: it reads no model state and
// returns its result, which the caller hands back with Deliver. The model
// reports to the TUI through Events, which the caller drains (Drain)
// after each call and applies to its view.
//
// core imports nothing of the TUI (TestCoreImportsNoUI): the daemon will
// run it with no terminal. Focus is the TUI's: where an operation needs
// "the workspace the user is looking at", the caller passes it.
package core
