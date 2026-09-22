package overlay

import (
	tea "charm.land/bubbletea/v2"
)

// Overlay is the shared contract for app-level overlays rendered above
// the main TUI. It unifies key routing, sizing, and rendering so the
// app model can hold a single active overlay rather than a handful of
// optional pointers.
//
// The nine types that satisfy this interface today are
// ConfirmationOverlay, FileExplorerOverlay, SessionLaunchOptions,
// MergePicker, WorkspacePicker, TextInputOverlay, IssuePicker,
// SettingsOverlay, and TextOverlay (see the var _ Overlay assertions
// below). Each retains its own specialized surface for callers that
// need richer signals (Submitted/Canceled flags, branch filter
// introspection, selected workspace). This interface is the common
// subset required for dispatch.
type Overlay interface {
	// View returns the overlay's current visual (matches the Bubble
	// Tea Model convention so overlays can stand in for a sub-model).
	View() string
	// HandleKey processes a single key press. closed reports whether
	// the overlay should be dismissed; cmd is an optional command the
	// host must dispatch (e.g., the dismiss callback for TextOverlay
	// or a branch-filter search kicked off by TextInputOverlay).
	HandleKey(msg tea.KeyPressMsg) (closed bool, cmd tea.Cmd)
	// SetSize reports the available rendering area. Overlays are free
	// to ignore dimensions they don't use.
	SetSize(width, height int)
}

// Compile-time checks that every overlay implementation still satisfies
// Overlay. Each of these types is constructed only through its own
// concrete-typed constructor elsewhere in the app, so nothing else forces
// the compiler to check the interface — without these, a signature drift
// on one of the three methods would fail only where the type is used as
// an Overlay (or not at all, if that call site always passes the
// concrete type), not at the type's own definition.
var (
	_ Overlay = (*ConfirmationOverlay)(nil)
	_ Overlay = (*FileExplorerOverlay)(nil)
	_ Overlay = (*SessionLaunchOptions)(nil)
	_ Overlay = (*MergePicker)(nil)
	_ Overlay = (*WorkspacePicker)(nil)
	_ Overlay = (*TextInputOverlay)(nil)
	_ Overlay = (*IssuePicker)(nil)
	_ Overlay = (*SettingsOverlay)(nil)
	_ Overlay = (*TextOverlay)(nil)
)

// ConfirmationTask bundles the synchronous preparation and the
// asynchronous body of a confirmed action so the host can schedule
// them in the correct order without exposing two separate fields.
//
// Sync runs first, on the main goroutine, and is the right place for
// state flips that must be visible to the very next render (e.g.,
// setting a Loading status so the spinner appears while the async
// work runs). Async is the tea.Cmd dispatched afterward; it may
// return a message that reconciles the state.
type ConfirmationTask struct {
	Sync  func()
	Async tea.Cmd
}

// Run executes the task: Sync on the current goroutine, then returns
// Async for the host to dispatch. A zero-value task is a no-op.
func (t ConfirmationTask) Run() tea.Cmd {
	if t.Sync != nil {
		t.Sync()
	}
	return t.Async
}
