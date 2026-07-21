package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
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
			// existing focus-mode intent (kill/recover). Re-seed the
			// cursor afterward since the target may now be gone (kill) or
			// have moved list position (resume).
			m.focusCursorSlot()
			cmd, _ := m.dispatchScript(msg.String())
			m.seedOverviewCursor()
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
		case "n", "N":
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

	if cmd, handled := m.dispatchScript(msg.String()); handled {
		return m, cmd
	}
	return m, nil
}
