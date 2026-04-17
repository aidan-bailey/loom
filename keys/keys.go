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

	KeyFullScreenAttachAgent    // Key for full-screen attach to agent pane
	KeyFullScreenAttachTerminal // Key for full-screen attach to terminal pane
	KeyDiff                     // Key for toggling diff overlay

	KeyQuickInputAgent    // Key for quick input targeting agent pane
	KeyQuickInputTerminal // Key for quick input targeting terminal pane
	// ctrl+a/ctrl+t are only dispatched in stateDefault, so they don't conflict
	// with the textinput widget's ctrl+a (LineStart) binding in stateQuickInteract.
	KeyDirectAttachAgent    // Key for direct attach to agent pane
	KeyDirectAttachTerminal // Key for direct attach to terminal pane
)

// GlobalKeyStringsMap is a global, immutable map string to keybinding.
var GlobalKeyStringsMap = map[string]KeyName{
	"up":     KeyUp,
	"k":      KeyUp,
	"down":   KeyDown,
	"j":      KeyDown,
	"N":      KeyPrompt,
	"n":      KeyNew,
	"D":      KeyKill,
	"q":      KeyQuit,
	"c":      KeyCheckout,
	"r":      KeyResume,
	"p":      KeySubmit,
	"?":      KeyHelp,
	"W":      KeyWorkspace,
	"[":      KeyWorkspaceLeft,
	"l":      KeyWorkspaceLeft,
	"]":      KeyWorkspaceRight,
	";":      KeyWorkspaceRight,
	"alt+a":  KeyFullScreenAttachAgent,
	"alt+t":  KeyFullScreenAttachTerminal,
	"d":      KeyDiff,
	"a":      KeyQuickInputAgent,
	"t":      KeyQuickInputTerminal,
	"ctrl+a": KeyDirectAttachAgent,
	"ctrl+t": KeyDirectAttachTerminal,
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

// HelpPanelDescriptions carries long-form descriptions for the help panel.
// key.Binding.Help().Desc already holds the shorter menu-bar descriptions;
// this map is the single source of truth for the longer strings shown in
// the help overlay. Per-section overrides live alongside the ordered entry
// lists in app/help.go.
var HelpPanelDescriptions = map[KeyName]string{
	KeyNew:                      "Create a new session",
	KeyPrompt:                   "Create a new session with a prompt",
	KeyKill:                     "Kill (delete) the selected session",
	KeyFullScreenAttachAgent:    "Full-screen attach to agent pane",
	KeyFullScreenAttachTerminal: "Full-screen attach to terminal pane",
	KeyQuickInputAgent:          "Quick input: type and send to agent",
	KeyQuickInputTerminal:       "Quick input: type and send to terminal",
	KeyDirectAttachAgent:        "Inline attach to agent pane",
	KeyDirectAttachTerminal:     "Inline attach to terminal pane",
	KeySubmit:                   "Commit and push branch to github",
	KeyCheckout:                 "Checkout: commit changes and pause session",
	KeyResume:                   "Resume a paused session",
	KeyWorkspace:                "Switch workspace",
	KeyDiff:                     "Toggle diff overlay",
	KeyQuit:                     "Quit the application",
}
