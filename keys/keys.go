package keys

import (
	"github.com/charmbracelet/bubbles/key"
)

// KeyName is the enum identifier used by the help panel and menu-bar
// highlighter to refer to a built-in binding. It is NOT the dispatch
// authority — that lives in script/defaults.lua, which can rebind any
// key at will. KeyName values are stable across releases; binding
// values in GlobalkeyBindings may change.
type KeyName int

// KeyUp through KeyDirectAttachTerminal enumerate the built-in keybindings
// surfaced in GlobalkeyBindings. They drive the help panel and menu-bar
// highlighter; dispatch itself is owned by script/defaults.lua, which may
// rebind any key. KeyDirectAttachAgent and KeyDirectAttachTerminal are
// reserved to stateDefault so they do not collide with textinput's ctrl+a
// (LineStart) binding in stateQuickInteract.
const (
	KeyUp KeyName = iota
	KeyDown
	KeyNew
	KeyKill
	KeyQuit
	KeySubmit
	KeySubmitName
	KeyCheckout
	KeyResume
	KeyPrompt
	KeyHelp
	KeyWorkspace
	KeyWorkspaceLeft
	KeyWorkspaceRight
	KeyFullScreenAttachAgent
	KeyFullScreenAttachTerminal
	KeyDiff
	KeyQuickInputAgent
	KeyQuickInputTerminal
	KeyDirectAttachAgent
	KeyDirectAttachTerminal
)

// keyStringToName is the reverse lookup derived from GlobalkeyBindings. It
// exists solely to drive menu-bar highlighting when a built-in key is
// pressed; dispatch itself has moved to the Lua engine.
var keyStringToName = buildKeyStringToName()

func buildKeyStringToName() map[string]KeyName {
	out := make(map[string]KeyName)
	for name, binding := range GlobalkeyBindings {
		for _, k := range binding.Keys() {
			out[k] = name
		}
	}
	return out
}

// KeyForString returns the KeyName bound to s via GlobalkeyBindings, or
// (0, false) when the string is not a built-in binding.
func KeyForString(s string) (KeyName, bool) {
	n, ok := keyStringToName[s]
	return n, ok
}

// GlobalkeyBindings is the global, immutable table of built-in
// bindings used by the help panel and menu-bar highlighter. It mirrors
// the defaults in script/defaults.lua — the Lua table is authoritative
// for dispatch, this map is for UI rendering only. Keep the two in sync
// when adding or changing a binding; the migration_parity_test.go guard
// catches drift.
var GlobalkeyBindings = map[KeyName]key.Binding{
	KeyUp: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	KeyDown: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	KeyNew: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	KeyKill: key.NewBinding(
		key.WithKeys("D"),
		key.WithHelp("D", "kill"),
	),
	KeyHelp: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	KeyQuit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
	KeySubmit: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "push branch"),
	),
	KeyPrompt: key.NewBinding(
		key.WithKeys("N"),
		key.WithHelp("N", "new with prompt"),
	),
	KeyCheckout: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "checkout"),
	),
	KeyResume: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "resume"),
	),

	KeyWorkspace: key.NewBinding(
		key.WithKeys("W"),
		key.WithHelp("W", "workspace"),
	),

	KeyWorkspaceLeft: key.NewBinding(
		key.WithKeys("[", "l"),
		key.WithHelp("[/l", "prev ws"),
	),
	KeyWorkspaceRight: key.NewBinding(
		key.WithKeys("]", ";"),
		key.WithHelp("]/;", "next ws"),
	),

	KeyFullScreenAttachAgent: key.NewBinding(
		key.WithKeys("alt+a"),
		key.WithHelp("alt+a", "fullscreen agent"),
	),
	KeyFullScreenAttachTerminal: key.NewBinding(
		key.WithKeys("alt+t"),
		key.WithHelp("alt+t", "fullscreen terminal"),
	),

	KeyDiff: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "diff"),
	),

	KeyQuickInputAgent: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "input to agent"),
	),
	KeyQuickInputTerminal: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "input to terminal"),
	),
	KeyDirectAttachAgent: key.NewBinding(
		key.WithKeys("ctrl+a"),
		key.WithHelp("ctrl+a", "attach agent"),
	),
	KeyDirectAttachTerminal: key.NewBinding(
		key.WithKeys("ctrl+t"),
		key.WithHelp("ctrl+t", "attach terminal"),
	),

	// -- Special keybindings --

	KeySubmitName: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "submit name"),
	),
}
