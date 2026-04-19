// Package script provides a sandboxed Lua scripting surface that lets
// users bind keys in the TUI to custom actions. It owns a single
// gopher-lua state, loads .lua files at startup, and dispatches on raw
// key strings after the built-in keymap misses.
//
// The engine is decoupled from the app/ package through the Host
// interface so that scripts can manipulate live session state without
// introducing an import cycle. Host is implemented by a small adapter
// inside app/ that forwards to the *home model.
package script

import (
	"claude-squad/config"
	"claude-squad/session"
)

// Host is the facade script userdata uses to touch live TUI state.
// Every method must be safe to call from a background goroutine — the
// Engine serializes Lua execution through a mutex but runs the Lua VM
// itself inside a tea.Cmd goroutine, so any state it touches must be
// RLock-protected (see session.Instance) or otherwise thread-safe.
type Host interface {
	// SelectedInstance returns the currently-focused instance in the
	// list panel, or nil if the list is empty.
	SelectedInstance() *session.Instance
	// Instances returns a snapshot slice of every instance currently
	// tracked by the list. Callers must not mutate the slice.
	Instances() []*session.Instance
	// Workspaces returns the loaded workspace registry. May be nil if
	// the registry failed to load at startup.
	Workspaces() *config.WorkspaceRegistry
	// ConfigDir returns the resolved workspace config directory for
	// the currently focused workspace.
	ConfigDir() string
	// RepoPath returns the repo root that new instances should be
	// created against (the focused workspace's path, or the current
	// directory when no workspace is registered).
	RepoPath() string
	// DefaultProgram returns the program string (agent command) that
	// new instances should launch by default.
	DefaultProgram() string
	// BranchPrefix returns the branch prefix configured for the
	// focused workspace (e.g. "alice/").
	BranchPrefix() string
	// QueueInstance asks the main goroutine to finalize the given
	// instance into the list. The engine calls this from userdata
	// methods like ctx:new_instance{}. The actual list mutation is
	// deferred to Update — see app.scriptDoneMsg.
	QueueInstance(inst *session.Instance)
	// Notify posts a transient message to the TUI's error/info bar.
	// Called from script userdata through ctx:notify().
	Notify(msg string)
	// Enqueue hands an Intent to the main goroutine for processing and
	// returns the IntentID scripts will match against a later
	// Engine.Resume call. Called from cs.actions.* primitives when the
	// requested operation cannot be performed synchronously on the
	// dispatch goroutine (e.g. anything that opens an overlay).
	Enqueue(intent Intent) IntentID

	// Sync primitives — safe to call on the dispatch goroutine because
	// they only mutate state that isn't also read by Update at the same
	// time (list cursor, diff-overlay flag, focused workspace slot).
	// Anything that needs to produce a tea.Cmd or open an overlay must
	// instead be handled through Enqueue + a matching Intent.

	CursorUp()
	CursorDown()
	ToggleDiff()
	WorkspacePrev()
	WorkspaceNext()

	// Scroll primitives — mutate UI viewport state synchronously. The
	// active-pane variants follow the same diff-visible > focused-pane
	// routing rule as mouse wheel scrolling.
	ScrollLineUp()
	ScrollLineDown()
	ScrollPageUp()
	ScrollPageDown()
	ScrollTop()
	ScrollBottom()

	// Explicit-target terminal scroll primitives — always scroll the
	// terminal pane regardless of focus or diff visibility.
	ScrollTerminalLineUp()
	ScrollTerminalLineDown()
	ScrollTerminalPageUp()
	ScrollTerminalPageDown()

	// List navigation primitives — selection jumps.
	ListPageUp()
	ListPageDown()
	ListTop()
	ListBottom()
}
