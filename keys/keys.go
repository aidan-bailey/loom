package keys

import (
	"github.com/charmbracelet/bubbles/key"
)

type KeyName int

const (
	KeyUp KeyName = iota
	KeyDown
	KeyNew
	KeyKill
	KeyQuit
	KeyReview
	KeyPush
	KeySubmit

	KeySubmitName // SubmitName is a special keybinding for submitting the name of a new instance.

	KeyCheckout
	KeyResume
	KeyPrompt // New key for entering a prompt
	KeyHelp   // Key for showing help screen

	KeyWorkspace      // Key for switching workspaces
	KeyWorkspaceLeft  // Key for previous workspace tab
	KeyWorkspaceRight // Key for next workspace tab

	KeyFullScreenAttach // Key for full-screen attach (existing attach behavior)
	KeyDiff             // Key for toggling diff overlay

	KeyQuickInputAgent    // Key for quick input targeting agent pane
	KeyQuickInputTerminal // Key for quick input targeting terminal pane
	// ctrl+a/ctrl+t are only dispatched in stateDefault, so they don't conflict
	// with the textinput widget's ctrl+a (LineStart) binding in stateQuickInteract.
	KeyDirectAttachAgent    // Key for direct attach to agent pane
	KeyDirectAttachTerminal // Key for direct attach to terminal pane
)

// GlobalKeyStringsMap is a global, immutable map string to keybinding.
var GlobalKeyStringsMap = map[string]KeyName{
	"up":         KeyUp,
	"k":          KeyUp,
	"down":       KeyDown,
	"j":          KeyDown,
	"N":          KeyPrompt,
	"n":          KeyNew,
	"D":          KeyKill,
	"q":          KeyQuit,
	"c":          KeyCheckout,
	"r":          KeyResume,
	"p":          KeySubmit,
	"?":          KeyHelp,
	"W":          KeyWorkspace,
	"[":          KeyWorkspaceLeft,
	"h":          KeyWorkspaceLeft,
	"]":          KeyWorkspaceRight,
	"l":          KeyWorkspaceRight,
	"O":          KeyFullScreenAttach,
	"d":          KeyDiff,
	"a":          KeyQuickInputAgent,
	"t":          KeyQuickInputTerminal,
	"ctrl+a":     KeyDirectAttachAgent,
	"ctrl+t":     KeyDirectAttachTerminal,
}

// GlobalkeyBindings is a global, immutable map of KeyName tot keybinding.
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
		key.WithKeys("[", "h"),
		key.WithHelp("[/h", "prev ws"),
	),
	KeyWorkspaceRight: key.NewBinding(
		key.WithKeys("]", "l"),
		key.WithHelp("]/l", "next ws"),
	),

	KeyFullScreenAttach: key.NewBinding(
		key.WithKeys("O"),
		key.WithHelp("O", "fullscreen"),
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
