package script

import "sync/atomic"

// IntentID is the handle a script uses to await an intent's
// completion. The engine yields it from cs.actions.* primitives; the
// host later calls Engine.Resume(id, value) once the matching UI
// flow resolves.
type IntentID uint64

var nextIntentID uint64

// newIntentID allocates a fresh, process-unique IntentID. Monotonic so
// debug traces read in the order intents were created.
func newIntentID() IntentID {
	return IntentID(atomic.AddUint64(&nextIntentID, 1))
}

// NewIntentID is the exported form for callers outside the package
// (notably the app layer's scriptHost.Enqueue).
func NewIntentID() IntentID { return newIntentID() }

// Intent is the marker interface every script-enqueued UI operation
// implements. The private intent() method keeps the set closed — only
// types declared in this file can satisfy it, which lets the app
// layer's type switch be exhaustive without an explicit default case.
type Intent interface{ intent() }

// AttachPane identifies which pane an attach-style intent targets.
// Used by InlineAttachIntent, FullscreenAttachIntent, and
// QuickInputIntent so callers don't need to carry separate types per
// pane variant.
type AttachPane int

const (
	// AttachPaneAgent targets the agent pane (top of the split-pane).
	AttachPaneAgent AttachPane = iota
	// AttachPaneTerminal targets the auxiliary terminal pane (bottom).
	AttachPaneTerminal
)

// QuitIntent requests app shutdown. Fulfilled by the app layer's
// top-level tea.Quit return.
type QuitIntent struct{}

// PushSelectedIntent asks the app to push the selected instance's
// branch to origin. Confirm=true prompts via a confirmation overlay
// before running the push.
type PushSelectedIntent struct{ Confirm bool }

// KillSelectedIntent asks the app to kill and clean up the selected
// instance. Confirm=true gates the destructive action behind an overlay.
type KillSelectedIntent struct{ Confirm bool }

// CheckoutIntent asks the app to check the selected instance's branch
// out into the root repo. Confirm gates the operation; Help opens the
// explanatory overlay instead of performing the checkout.
type CheckoutIntent struct{ Confirm, Help bool }

// ResumeIntent asks the app to resume the selected paused instance.
type ResumeIntent struct{}

// NewInstanceIntent asks the app to open the new-instance overlay.
// Prompt=true collects a starter prompt after the title. Title
// pre-fills the title field when non-empty.
type NewInstanceIntent struct {
	Prompt bool
	Title  string
}

// ShowHelpIntent opens the help overlay.
type ShowHelpIntent struct{}

// WorkspacePickerIntent opens the workspace-picker overlay for
// switching between registered workspaces.
type WorkspacePickerIntent struct{}

// InlineAttachIntent asks the app to inline-attach to the named pane
// (keystrokes route to tmux until the user detaches with ctrl+q).
type InlineAttachIntent struct{ Pane AttachPane }

// FullscreenAttachIntent asks the app to full-screen attach to the
// named pane via tea.ExecProcess.
type FullscreenAttachIntent struct{ Pane AttachPane }

// QuickInputIntent opens the quick-input bar targeting the named pane.
// Text typed there is sent to the backing tmux session on Enter.
type QuickInputIntent struct{ Pane AttachPane }

func (QuitIntent) intent()             {}
func (PushSelectedIntent) intent()     {}
func (KillSelectedIntent) intent()     {}
func (CheckoutIntent) intent()         {}
func (ResumeIntent) intent()           {}
func (NewInstanceIntent) intent()      {}
func (ShowHelpIntent) intent()         {}
func (WorkspacePickerIntent) intent()  {}
func (InlineAttachIntent) intent()     {}
func (FullscreenAttachIntent) intent() {}
func (QuickInputIntent) intent()       {}
