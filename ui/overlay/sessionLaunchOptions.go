package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// LaunchOptions holds the per-session launch overrides. Defined
// here (rather than in app) so it's usable both by
// SessionLaunchOptions (ephemeral, edited as a plain value) and by
// app's launch-command composition, without an import cycle back to
// app.
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	// Context1M requests Claude's [1m] long-context suffix on Model.
	// Applied only when Model accepts it (see
	// config.ClaudeModelSupports1M); otherwise silently ignored.
	Context1M     bool
	HeadroomProxy bool
	Effort        string
	CacheTTL1h    bool
	// BranchPrefix overrides config.BranchPrefix for this one session.
	// Unlike the fields above it never reaches the agent command line —
	// it is consumed by git worktree setup (see session.Instance.
	// SetBranchPrefix), so ParseLaunchOptions cannot recover it and the
	// restart path seeds it from the instance instead.
	BranchPrefix string
}

// SessionLaunchOptions is the per-instance "Session Launch Options"
// modal shown right before a new session starts. Unlike
// ClaudePreferences, which edits *config.Config directly and persists
// on every change, this edits a local LaunchOptions value that the
// caller applies to just one instance — closing without saving
// anything to disk.
type SessionLaunchOptions struct {
	opts        LaunchOptions
	authBlocked bool
	authReason  string
	width       int
	cursor      int
	// editing is the nested single-line editor for the Branch Prefix row,
	// non-nil only while that row is being edited. It exists because this
	// modal's other rows are all toggles/cycles, so its key handling can
	// treat enter/esc as confirm/cancel — a text row cannot, and routing
	// through this field is what keeps those keys off the modal.
	editing *TextInputOverlay
	// prefixLocked replaces the editable Branch Prefix row with a read-only
	// one. Set on the restart path, where the branch already exists and
	// renaming it is not possible. Kept separate from lockedBranch because
	// an instance may have no branch name recorded — locking must not
	// depend on having something to display.
	prefixLocked bool
	// lockedBranch is the already-created branch shown on a locked row, or
	// "" when it is not known.
	lockedBranch string
}

// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, 1M Context, Headroom Proxy, Effort,
// Cache TTL (1h), and Branch Prefix.
const sessionLaunchOptionsRowCount = 8

// sessionLaunchOptionsBranchPrefixRow is the cursor index of the Branch
// Prefix row — the only text-entry row, so several call sites need it by
// name rather than by position.
const sessionLaunchOptionsBranchPrefixRow = 7

// NewSessionLaunchOptions creates the modal seeded with initial
// (typically the global config's current values).
func NewSessionLaunchOptions(initial LaunchOptions, authBlocked bool, authReason string) *SessionLaunchOptions {
	return &SessionLaunchOptions{opts: initial, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// SetBranchPrefixLocked switches the Branch Prefix row to a read-only
// display of branch. Used on the restart path, where the session's branch
// already exists and an editable prefix would imply a rename that cannot
// happen.
func (l *SessionLaunchOptions) SetBranchPrefixLocked(branch string) {
	l.prefixLocked = true
	l.lockedBranch = branch
}

// SetWidth sets the render width.
func (l *SessionLaunchOptions) SetWidth(w int) { l.width = w }

// Options returns the current (possibly edited) launch options.
func (l *SessionLaunchOptions) Options() LaunchOptions { return l.opts }

// HandleKeyPress processes one key press. closed reports whether the
// modal should close (either canceled or confirmed); confirmed
// distinguishes the two — the caller only applies Options() and starts
// the instance when confirmed is true.
func (l *SessionLaunchOptions) HandleKeyPress(msg tea.KeyPressMsg) (closed, confirmed bool) {
	// Editing must be checked before the switch below: while a text row is
	// open, enter and esc belong to the editor, not to the modal.
	if l.editing != nil {
		l.handleEditingKey(msg)
		return false, false
	}
	switch msg.String() {
	case "esc", "q":
		return true, false
	case "enter":
		return true, true
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
		return false, false
	case "down", "j":
		if l.cursor < sessionLaunchOptionsRowCount-1 {
			l.cursor++
		}
		return false, false
	case " ", "space":
		l.toggleCursor()
		return false, false
	}
	return false, false
}

// toggleCursor applies the toggle/cycle action for the focused row,
// enforcing the same Remote-Control/Headroom-Proxy exclusivity rule as
// ClaudePreferences.
func (l *SessionLaunchOptions) toggleCursor() {
	switch l.cursor {
	case 0:
		l.opts.RemoteControl = !l.opts.RemoteControl
		if l.opts.RemoteControl {
			l.opts.HeadroomProxy = false
		}
	case 1:
		l.opts.PermissionMode = nextInList(config.ClaudePermissionModes, l.opts.PermissionMode)
	case 2:
		l.opts.Model = nextInList(config.ClaudeModelAliases(), l.opts.Model)
	case 3:
		l.opts.Context1M = !l.opts.Context1M
	case 4:
		l.opts.HeadroomProxy = !l.opts.HeadroomProxy
		if l.opts.HeadroomProxy {
			l.opts.RemoteControl = false
		}
	case 5:
		l.opts.Effort = nextInList(config.ClaudeEfforts, l.opts.Effort)
	case 6:
		l.opts.CacheTTL1h = !l.opts.CacheTTL1h
	case sessionLaunchOptionsBranchPrefixRow:
		if l.prefixLocked {
			return
		}
		l.editing = NewTextInputOverlay("Branch Prefix", l.opts.BranchPrefix)
		l.editing.SetSize(l.width, 3)
	}
}

// handleEditingKey owns enter/esc while the Branch Prefix editor is open,
// mirroring SettingsOverlay.handleEditingText: enter commits the single-line
// value immediately, esc discards it, and everything else is forwarded to the
// embedded widget.
func (l *SessionLaunchOptions) handleEditingKey(msg tea.KeyPressMsg) {
	switch msg.Code {
	case tea.KeyEnter:
		// No trimming or trailing-slash coercion: an empty prefix is a
		// legitimate choice, and sanitizeBranchName already normalizes the
		// composed branch name downstream.
		l.opts.BranchPrefix = l.editing.GetValue()
		l.editing = nil
	case tea.KeyEsc:
		l.editing = nil
	default:
		l.editing.HandleKeyPress(msg)
	}
}

var (
	sessionLaunchOptionsTitleStyle, sessionLaunchOptionsRowStyle,
	sessionLaunchOptionsSelectedStyle, sessionLaunchOptionsHintStyle,
	sessionLaunchOptionsBlockedText lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildSessionLaunchOptionsStyles) }

func rebuildSessionLaunchOptionsStyles() {
	sessionLaunchOptionsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	sessionLaunchOptionsRowStyle = lipgloss.NewStyle().Foreground(ui.Text)
	sessionLaunchOptionsSelectedStyle = lipgloss.NewStyle().Foreground(ui.Accent).Bold(true)
	sessionLaunchOptionsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	sessionLaunchOptionsBlockedText = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

// Render renders the modal.
func (l *SessionLaunchOptions) Render() string {
	// The nested editor draws its own complete bordered box, so it replaces
	// the modal rather than rendering inside it — same as
	// SettingsOverlay.Render does for its text-edit mode. Nesting it would
	// double-border and line-wrap the inner box.
	if l.editing != nil {
		return l.editing.Render()
	}

	row := func(idx int, label, value string) string {
		cursor := "  "
		if l.cursor == idx {
			cursor = "> "
		}
		line := cursor + label + value
		if idx == 0 && l.authBlocked {
			line += "  " + sessionLaunchOptionsBlockedText.Render("(blocked: "+l.authReason+")")
		}
		if l.cursor == idx {
			return sessionLaunchOptionsSelectedStyle.Render(line)
		}
		return sessionLaunchOptionsRowStyle.Render(line)
	}

	rcCheck := "[ ]"
	if l.opts.RemoteControl {
		rcCheck = "[x]"
	}
	hwCheck := "[ ]"
	if l.opts.HeadroomProxy {
		hwCheck = "[x]"
	}
	ctxCheck := "[ ]"
	if l.opts.Context1M {
		ctxCheck = "[x]"
	}
	cacheCheck := "[ ]"
	if l.opts.CacheTTL1h {
		cacheCheck = "[x]"
	}

	content := sessionLaunchOptionsTitleStyle.Render("Session Launch Options") + "\n\n" +
		row(0, "Remote Control    ", rcCheck) + "\n" +
		row(1, "Permission Mode   ", "< "+l.opts.PermissionMode+" >") + "\n" +
		row(2, "Model             ", "< "+l.opts.Model+" >") + "\n" +
		row(3, "1M Context        ", ctxCheck) + "\n" +
		row(4, "Headroom Proxy    ", hwCheck) + "\n" +
		row(5, "Effort            ", "< "+l.opts.Effort+" >") + "\n" +
		row(6, "Cache TTL (1h)    ", cacheCheck) + "\n" +
		row(sessionLaunchOptionsBranchPrefixRow, "Branch Prefix     ", l.branchPrefixValue()) + "\n\n" +
		sessionLaunchOptionsHintStyle.Render(l.hint())

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(l.width)
	return border.Render(content)
}

// branchPrefixValue renders the Branch Prefix row's right-hand side: the
// already-created branch when locked, the editable prefix otherwise, and an
// explicit marker when the prefix is empty (which is valid but invisible).
func (l *SessionLaunchOptions) branchPrefixValue() string {
	if l.prefixLocked {
		if l.lockedBranch == "" {
			return sessionLaunchOptionsHintStyle.Render("(fixed)")
		}
		return sessionLaunchOptionsHintStyle.Render(l.lockedBranch + " (fixed)")
	}
	if l.opts.BranchPrefix == "" {
		return sessionLaunchOptionsHintStyle.Render("(none)")
	}
	return l.opts.BranchPrefix
}

// hint tailors the key legend to the focused row, since Branch Prefix is the
// only row where space opens an editor rather than toggling.
func (l *SessionLaunchOptions) hint() string {
	if l.cursor == sessionLaunchOptionsBranchPrefixRow && !l.prefixLocked {
		return "up/down move • space edit • enter start • esc cancel"
	}
	return "up/down move • space toggle/cycle • enter start • esc cancel"
}

// HandleKey satisfies the Overlay interface. State handlers that need
// the confirmed signal call HandleKeyPress directly instead (mirrors
// SettingsOverlay.HandleKey/HandleKeyPress).
func (l *SessionLaunchOptions) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := l.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface.
func (l *SessionLaunchOptions) SetSize(width, _ int) {
	l.width = width
	// Keep an open editor in step with a resize; it owns the whole frame
	// while it is up.
	if l.editing != nil {
		l.editing.SetSize(width, 3)
	}
}

// View satisfies the Overlay interface.
func (l *SessionLaunchOptions) View() string {
	return l.Render()
}
