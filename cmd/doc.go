// Package cmd wraps os/exec behind the [Executor] interface so
// callers that shell out (git operations, tmux calls, the claude
// command lookup) can be unit-tested against a fake executor without
// spawning real subprocesses.
//
// The production implementation is [RealExecutor]; tests supply a
// mock that records invocations and returns scripted output. This is
// the canonical way to introduce a new shell-out in Loom — reach for
// this interface before calling exec.Command directly.
package cmd
