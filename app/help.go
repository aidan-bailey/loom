package app

import (
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type helpText interface {
	// toContent returns the help UI content.
	toContent() string
	// mask returns the bit mask for this help text. These are used to track which help screens
	// have been seen in the config and app state.
	mask() uint32
}

type helpTypeGeneral struct{}

type helpTypeInstanceStart struct {
	instance *session.Instance
}

type helpTypeInstanceAttach struct{}

type helpTypeInstanceCheckout struct{}

func helpStart(instance *session.Instance) helpText {
	return helpTypeInstanceStart{instance: instance}
}

// helpEntry describes one line in a help panel. The rendered label is the
// concatenation of each binding's Help().Key (joined with ", "), or rawKey
// when the action has no binding (e.g. "ctrl+q" detach). The description
// falls back to keys.HelpPanelDescriptions for the first binding when desc
// is empty, allowing per-panel overrides for context-specific wording.
type helpEntry struct {
	bindings []keys.KeyName
	rawKey   string
	desc     string
}

func (e helpEntry) label() string {
	if e.rawKey != "" {
		return e.rawKey
	}
	parts := make([]string, 0, len(e.bindings))
	for _, k := range e.bindings {
		parts = append(parts, keys.GlobalkeyBindings[k].Help().Key)
	}
	return strings.Join(parts, ", ")
}

func (e helpEntry) description() string {
	if e.desc != "" {
		return e.desc
	}
	if len(e.bindings) > 0 {
		return keys.HelpPanelDescriptions[e.bindings[0]]
	}
	return ""
}

// renderHelpSection returns a block of "<label><pad>- <desc>" lines with
// labels right-padded so the dash lands at column dashCol (0-indexed) for
// every entry.
func renderHelpSection(entries []helpEntry, dashCol int) string {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		label := e.label()
		pad := ""
		if w := lipgloss.Width(label); w < dashCol {
			pad = strings.Repeat(" ", dashCol-w)
		}
		b.WriteString(keyStyle.Render(label))
		b.WriteString(descStyle.Render(pad + "- " + e.description()))
	}
	return b.String()
}

var (
	generalManagingEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyNew}},
		{bindings: []keys.KeyName{keys.KeyPrompt}},
		{bindings: []keys.KeyName{keys.KeyKill}},
		{bindings: []keys.KeyName{keys.KeyUp, keys.KeyDown}, desc: "Navigate between sessions"},
		{bindings: []keys.KeyName{keys.KeyFullScreenAttachAgent}},
		{bindings: []keys.KeyName{keys.KeyFullScreenAttachTerminal}},
		{bindings: []keys.KeyName{keys.KeyQuickInputAgent}},
		{bindings: []keys.KeyName{keys.KeyQuickInputTerminal}},
		{bindings: []keys.KeyName{keys.KeyDirectAttachAgent}},
		{bindings: []keys.KeyName{keys.KeyDirectAttachTerminal}},
		{rawKey: "ctrl+q", desc: "Detach from session"},
	}

	generalHandoffEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeySubmit}},
		{bindings: []keys.KeyName{keys.KeyCheckout}},
		{bindings: []keys.KeyName{keys.KeyResume}},
	}

	generalOtherEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyWorkspace}},
		{bindings: []keys.KeyName{keys.KeyDiff}},
		{bindings: []keys.KeyName{keys.KeyQuit}},
	}

	instanceStartManagingEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyFullScreenAttachAgent}},
		{bindings: []keys.KeyName{keys.KeyFullScreenAttachTerminal}},
		{bindings: []keys.KeyName{keys.KeyDirectAttachAgent}},
		{bindings: []keys.KeyName{keys.KeyDirectAttachTerminal}},
		{bindings: []keys.KeyName{keys.KeyDiff}},
		{bindings: []keys.KeyName{keys.KeyKill}},
	}

	instanceStartHandoffEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyCheckout}, desc: "Checkout this instance's branch"},
		{bindings: []keys.KeyName{keys.KeySubmit}, desc: "Push branch to GitHub to create a PR"},
	}

	checkoutCommandEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyCheckout}, desc: "Checkout: commit changes locally and pause session"},
		{bindings: []keys.KeyName{keys.KeyResume}, desc: "Resume a paused session"},
	}
)

func (h helpTypeGeneral) toContent() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Claude Squad"),
		"",
		"A terminal UI that manages multiple Claude Code (and other local agents) in separate workspaces.",
		"",
		headerStyle.Render("Managing:"),
		renderHelpSection(generalManagingEntries, 10),
		"",
		headerStyle.Render("Handoff:"),
		renderHelpSection(generalHandoffEntries, 10),
		"",
		headerStyle.Render("Other:"),
		renderHelpSection(generalOtherEntries, 10),
	)
}

func (h helpTypeInstanceStart) toContent() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Instance Created"),
		"",
		descStyle.Render("New session created:"),
		descStyle.Render(fmt.Sprintf("• Git branch: %s (isolated worktree)",
			lipgloss.NewStyle().Bold(true).Render(h.instance.GetBranch()))),
		descStyle.Render(fmt.Sprintf("• %s running in background tmux session",
			lipgloss.NewStyle().Bold(true).Render(h.instance.Program))),
		"",
		headerStyle.Render("Managing:"),
		renderHelpSection(instanceStartManagingEntries, 7),
		"",
		headerStyle.Render("Handoff:"),
		renderHelpSection(instanceStartHandoffEntries, 6),
	)
}

func (h helpTypeInstanceAttach) toContent() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Full-Screen Attach"),
		"",
		descStyle.Render("You are entering full-screen mode. Press ")+keyStyle.Render("ctrl+q")+descStyle.Render(" to detach."),
	)
}

func (h helpTypeInstanceCheckout) toContent() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Checkout Instance"),
		"",
		"Changes will be committed locally. The branch name has been copied to your clipboard for you to checkout.",
		"",
		"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off.",
		"",
		headerStyle.Render("Commands:"),
		renderHelpSection(checkoutCommandEntries, 2),
	)
}
func (h helpTypeGeneral) mask() uint32 {
	return 1
}

func (h helpTypeInstanceStart) mask() uint32 {
	return 1 << 1
}
func (h helpTypeInstanceAttach) mask() uint32 {
	return 1 << 2
}
func (h helpTypeInstanceCheckout) mask() uint32 {
	return 1 << 3
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(ui.TitleAccent)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.HeaderAccent)
	keyStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.KeyHighlight)
	descStyle   = lipgloss.NewStyle().Foreground(ui.TextPrimary)
)

// showHelpScreen displays the help screen overlay if it hasn't been shown before.
// The onDismiss callback, if non-nil, is invoked when the overlay is closed (or
// immediately when the overlay is skipped because the user has already seen it).
// It may mutate model state (e.g. to open a follow-up modal) and return a tea.Cmd
// to be dispatched once the help state has been cleared.
func (m *home) showHelpScreen(helpType helpText, onDismiss func() tea.Cmd) (tea.Model, tea.Cmd) {
	// Get the flag for this help type
	var alwaysShow bool
	switch helpType.(type) {
	case helpTypeGeneral:
		alwaysShow = true
	}

	flag := helpType.mask()

	// Check if this help screen has been seen before
	// Only show if we're showing the general help screen or the corresponding flag is not set
	// in the seen bitmask.
	if alwaysShow || (m.appState.GetHelpScreensSeen()&flag) == 0 {
		// Mark this help screen as seen and save state
		if err := m.appState.SetHelpScreensSeen(m.appState.GetHelpScreensSeen() | flag); err != nil {
			log.WarningLog.Printf("Failed to save help screen state: %v", err)
		}

		content := helpType.toContent()

		to := overlay.NewTextOverlay(content)
		to.OnDismiss = onDismiss
		m.setOverlay(to, overlayText)
		m.state = stateHelp
		return m, nil
	}

	// Skip displaying the help screen — fire the dismiss Cmd inline.
	var cmd tea.Cmd
	if onDismiss != nil {
		cmd = onDismiss()
	}
	return m, cmd
}

// handleHelpState handles key events when in help state
func (m *home) handleHelpState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	to := m.textOverlay()
	if to == nil {
		return m, nil
	}
	// Any key press will close the help overlay. HandleKeyPress invokes
	// OnDismiss and returns its tea.Cmd (if any) alongside the close flag.
	shouldClose, dismissCmd := to.HandleKeyPress(msg)
	if shouldClose {
		// Only reset to default if the OnDismiss callback didn't transition to
		// another state (e.g. checkout's callback sets stateConfirm).
		if m.state == stateHelp {
			m.state = stateDefault
		}
		return m, tea.Batch(
			dismissCmd,
			tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			),
		)
	}

	return m, nil
}
