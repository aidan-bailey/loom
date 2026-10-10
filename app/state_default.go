package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"
)

// overviewKeyAllowed whitelists script-dispatched keys in overview mode.
// Everything else (attach, quick input, scroll, diff, file explorer) is
// focus-mode-only and no-ops here rather than acting on an invisible
// pane. List-index jumps (K/J/g/G) are excluded too: they address list
// order, which is incoherent over the attention-sorted grid.
//
// v1 limitation: the gate is keyed on raw key strings, not on the
// actions they dispatch — a user script that rebinds a whitelisted key
// to a focus-only action (or a focus key to a grid-safe one) gets the
// default key's gating, not its action's. Fixing that properly means
// per-mode keymaps in the engine (deferred; see the plan's "required
// background" notes). n/N are absent because they are intercepted
// above the gate: they drop to focus mode first, then dispatch.
var overviewKeyAllowed = map[string]bool{
	"j": true, "k": true, "up": true, "down": true,
	"]": true, "[": true, "tab": true,
	"D": true, "r": true, "R": true,
	"q": true, "?": true, "W": true, "S": true,
	"{": true, "}": true, "l": true, ";": true,
}

// offlineKeyAllowed whitelists the keys that work while the TUI is offline
// (link.go: the daemon stopping, stopped or unreachable): the ones that
// talk to tmux or stay in the TUI. Everything else (n N I D p s m r R W S
// a, and every key a user script bound) needs the model, and is refused
// with an info line rather than dispatched. As a backstop, a request made
// anyway (an overlay that was open when the link dropped, committing) is
// refused by the client itself (core.ErrUnavailable).
var offlineKeyAllowed = map[string]bool{
	"up": true, "k": true, "down": true, "j": true, "]": true, "[": true,
	"tab": true, "\\": true, "T": true, "ctrl+up": true, "ctrl+down": true,
	"{": true, "l": true, "}": true, ";": true,
	"i": true, "ctrl+a": true, "ctrl+t": true, "alt+a": true, "alt+t": true,
	"t": true, "d": true, "f": true, "c": true, "e": true,
	"?": true, "q": true, "enter": true, "esc": true, "z": true,
	"pgup": true, "pgdown": true, "home": true, "end": true, "ctrl+u": true, "ctrl+d": true,
	"shift+up": true, "shift+down": true, "alt+pgup": true, "alt+pgdown": true,
	"K": true, "J": true, "g": true, "G": true,
	"1": true, "2": true, "3": true, "4": true, "5": true,
	"ctrl+left": true, "ctrl+right": true,
}

// offlineKeyRefused reports whether the offline gate refuses key, saying so
// on the info line when it does. Connected, nothing is refused.
func (m *home) offlineKeyRefused(key string) bool {
	if !m.offline() || offlineKeyAllowed[key] {
		return false
	}
	m.errBox.SetInfo(fmt.Sprintf("the loom daemon is %s: %s needs it", m.link.state.word(), key))
	return true
}

// workbenchOwnsKeys reports whether the workbench's markdown editor or
// review pane gets each key first (handleWorkbenchKey): the editor takes
// every key as text, and the review its own (s, v, n, N, / and digits
// among them), all in the TUI. The offline gate then meets only the keys
// they decline; the review's S, which needs the model, is refused by the
// client.
func (m *home) workbenchOwnsKeys() bool {
	if m.viewMode != viewWorkbench || m.workbench == nil {
		return false
	}
	if m.workbench.Markdown != nil && m.workbench.Markdown.Editing() {
		return true
	}
	return m.workbench.Tab() == ui.WbTabReview && m.wbReview != nil
}

// handleStateDefaultKey processes keys while the list is in its normal
// (no-overlay) state. ctrl+c is a hard-reserved panic exit. In overview
// mode, enter/esc drop back to focus, z collapses the active group, and
// only whitelisted keys reach the script engine. Esc gets first crack at
// dismissing the diff or exiting scroll mode (focus mode only — the
// overview branch returns first). Every remaining key is routed through
// the Lua engine via dispatchScript — ActionRegistry and the
// GlobalKeyStringsMap lookup have been retired in favor of defaults.lua.
func handleStateDefaultKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// ctrl+c is a panic-exit backstop. Evaluated BEFORE any engine
	// dispatch or handler so a broken or malicious user script that
	// unbinds or shadows ctrl+c still can't trap the user in the
	// TUI. Skips handleQuit's save path intentionally: ctrl+c is for
	// the case where something's gone wrong and the user wants out
	// now, not a clean shutdown.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	// Offline, ctrl+r joins a daemon now, starting one; connected, it is
	// any other key (unbound by default). Then the offline gate, ahead of
	// every mode's own keys, but for the workbench's editor and review,
	// which take theirs first (workbenchOwnsKeys).
	if msg.String() == "ctrl+r" && m.offline() {
		return m, m.retryNow()
	}
	if !m.workbenchOwnsKeys() && m.offlineKeyRefused(msg.String()) {
		return m, nil
	}
	if m.viewMode == viewWorkbench {
		if model, cmd, handled := handleWorkbenchKey(m, msg); handled {
			return model, cmd
		}
		if m.offlineKeyRefused(msg.String()) {
			return m, nil
		}
		if !workbenchKeyAllowed[msg.String()] {
			return m, nil
		}
	}
	if m.viewMode == viewOverview {
		switch msg.String() {
		case "enter":
			m.focusCursorSlot()
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			return m, m.instanceChanged()
		case "D", "r", "R":
			// Cross-workspace commit: the cursor may be sitting on a peer
			// workspace's card, so focus its slot before dispatching the
			// existing focus-mode intent (kill/recover/resume). The intent
			// applies later (dispatchScript returns a Cmd); any cursor
			// staleness it causes is healed at render time by
			// normalizeOverviewCursor.
			m.focusCursorSlot()
			cmd, _ := m.dispatchScript(msg.String())
			return m, cmd
		case "z":
			// Same fallback name overviewData renders with, so collapse
			// works in classic/global mode too.
			m.overview.ToggleCollapse(m.overviewGroupName())
			return m, nil
		case "esc":
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			return m, m.instanceChanged()
		case "n", "N", "I":
			// Creating a session from the grid would collect the title
			// blind — the inline title entry is a focus-layout
			// affordance. Drop to focus first (persisted, same as
			// enter/esc), then dispatch the create flow normally so it
			// proceeds in the layout the user will type into.
			m.focusCursorSlot()
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			cmds := []tea.Cmd{m.instanceChanged()}
			if cmd, handled := m.dispatchScript(msg.String()); handled {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if !overviewKeyAllowed[msg.String()] {
			return m, nil
		}
	}
	if msg.Code == tea.KeyEsc {
		// Dismiss diff overlay first
		if m.splitPane.IsDiffVisible() {
			m.splitPane.ToggleDiff()
			return m, m.instanceChanged()
		}
		// Exit agent scroll mode
		if m.splitPane.IsAgentInScrollMode() {
			selected := m.list.GetSelectedInstance()
			err := m.splitPane.ResetAgentToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// Exit terminal scroll mode
		if m.splitPane.IsTerminalInScrollMode() {
			m.splitPane.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Enter in focus mode opens the workbench deep-dive on the selected
	// instance. Intercepted here rather than script-bound: the overview
	// branch has already returned for its own enter, and defaults.lua
	// does not bind "enter", so this adds behavior without stealing any
	// existing binding.
	if msg.String() == "enter" && m.viewMode == viewFocus {
		if cmd := m.enterWorkbench(); cmd != nil {
			return m, cmd
		}
		return m, nil
	}

	if cmd, handled := m.dispatchScript(msg.String()); handled {
		return m, cmd
	}
	return m, nil
}
