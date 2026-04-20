// Package session owns the core domain: a running AI coding agent
// represented by the [Instance] type.
//
// An Instance moves through a small lifecycle — Ready → Loading →
// Running → Paused (and optionally Killed) — coordinating with
// neighbouring packages at each stage: [session/git] for worktree
// creation and cleanup, [session/tmux] for the PTY that drives the
// agent, and [session/agent] for program-specific behaviour such as
// trust-prompt handling and recovery flags.
//
// Persistence lives in storage.go and is versioned via
// [CurrentSchemaVersion]; when the on-disk [InstanceData] schema
// changes, bump the constant and add an upgrade step in
// storage_migrate.go. State transitions are validated through
// [Instance.TransitionTo]; disallowed edges return an error rather
// than silently mutating.
package session
