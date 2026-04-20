// Package daemon implements the background auto-yes process.
//
// When auto-yes is enabled, the TUI launches a detached child
// process (this package's [RunDaemon] entry point) that polls every
// tracked [session.Instance] on a configurable interval, uses the
// [session/agent] adapters to detect agent-specific prompts, and
// sends Enter to dismiss them. The daemon's PID is persisted to
// {configDir}/daemon.pid; the TUI kills and restarts the daemon on
// every launch to guarantee a clean state.
//
// Per-tick work runs through a bounded goroutine pool
// ([daemonPool]) with a per-instance timeout ([tickInstanceTimeout])
// so a single wedged tmux capture cannot stall the rest, and so
// persistent wedges surface as pool back-pressure rather than
// unbounded goroutine growth.
package daemon
