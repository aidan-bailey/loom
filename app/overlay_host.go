package app

import (
	"github.com/aidan-bailey/loom/ui/overlay"
)

// overlayKind distinguishes overlays that share the Overlay interface
// but require different render placement. Today only the startup
// workspace picker needs fullscreen centering; the rest are rendered
// via PlaceOverlay on top of the main view. Keeping this as a tag on
// home (rather than probing the concrete type at render time) means
// render code doesn't have to import the concrete overlay packages'
// signals.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayTextInput
	overlayText
	overlayConfirmation
	overlayWorkspacePicker
	overlayWorkspacePickerStartup
)

// setOverlay installs o as the active overlay and records its kind.
// Callers that open an overlay should flip m.state in the same
// function — keeping overlay + state in lockstep is the contract that
// makes the single-pointer storage safe.
func (m *home) setOverlay(o overlay.Overlay, kind overlayKind) {
	m.activeOverlay = o
	m.activeOverlayKind = kind
}

// dismissOverlay clears the active overlay. Use this whenever the
// overlay has closed — setting the field directly risks leaving the
// kind tag stale.
func (m *home) dismissOverlay() {
	m.activeOverlay = nil
	m.activeOverlayKind = overlayNone
}

// textInput returns the active TextInputOverlay, or nil if a different
// overlay (or none) is active. State handlers that need to read
// submit/cancel/branch-filter signals use this accessor — it replaces
// the former home.textInputOverlay field without changing read
// ergonomics at the call site.
func (m *home) textInput() *overlay.TextInputOverlay {
	if o, ok := m.activeOverlay.(*overlay.TextInputOverlay); ok {
		return o
	}
	return nil
}

// textOverlay returns the active TextOverlay, or nil when a different
// overlay is active. Used by the help-screen state handler.
func (m *home) textOverlay() *overlay.TextOverlay {
	if o, ok := m.activeOverlay.(*overlay.TextOverlay); ok {
		return o
	}
	return nil
}

// confirmation returns the active ConfirmationOverlay, or nil when a
// different overlay is active.
func (m *home) confirmation() *overlay.ConfirmationOverlay {
	if o, ok := m.activeOverlay.(*overlay.ConfirmationOverlay); ok {
		return o
	}
	return nil
}

// workspacePicker returns the active WorkspacePicker, or nil when a
// different overlay is active. Works for both mid-session and
// startup variants.
func (m *home) workspacePicker() *overlay.WorkspacePicker {
	if o, ok := m.activeOverlay.(*overlay.WorkspacePicker); ok {
		return o
	}
	return nil
}
