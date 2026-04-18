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
	AttachPaneAgent AttachPane = iota
	AttachPaneTerminal
)

type QuitIntent struct{}
type PushSelectedIntent struct{ Confirm bool }
type KillSelectedIntent struct{ Confirm bool }
type CheckoutIntent struct{ Confirm, Help bool }
type ResumeIntent struct{}
type NewInstanceIntent struct {
	Prompt bool
	Title  string
}
type ShowHelpIntent struct{}
type WorkspacePickerIntent struct{}
type InlineAttachIntent struct{ Pane AttachPane }
type FullscreenAttachIntent struct{ Pane AttachPane }
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
