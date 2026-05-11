package app

import (
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	tea "github.com/charmbracelet/bubbletea"
)

// handleStateOrphanRecoveryKey routes keys to the active orphan-
// recovery overlay. On commit, the user's selection is materialized
// into recovered Instance objects (added to the right slot's list and
// persisted), then state returns to default — and any deferred
// startup-overlay closure runs so pendingDir / picker dialogs the
// orphan overlay had preempted are shown next.
//
// Skipped orphans whose tmux is alive get their session killed here:
// the startup CleanupOrphanedSessions pass exempted them so the user's
// PTY would survive the recovery decision; once they've explicitly
// declined to recover, the exemption no longer applies and a leftover
// loom_<title> tmux pane is just dead-state to be cleaned up.
func handleStateOrphanRecoveryKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	op := m.orphanRecovery()
	if op == nil {
		return m, nil
	}
	closed, _ := op.HandleKeyPress(msg)
	if !closed {
		return m, nil
	}
	selected := op.Selected()
	skipped := op.Skipped()
	m.dismissOverlay()
	m.state = stateDefault

	// Sweep skipped-but-tmux-alive sessions. The startup orphan-tmux
	// exemption was conditional on the user choosing to recover; a
	// skip means "yes, this is dead state — clean it up." Errors are
	// best-effort: a missing session was probably killed elsewhere
	// already, and the next CleanupOrphanedSessions sweep will catch
	// anything we missed.
	exec := cmd2.MakeExecutor()
	for _, c := range skipped {
		if !c.HasLiveTmux {
			continue
		}
		if err := session.KillTmuxSessionByTitle(c.Title, exec); err != nil {
			log.For("app").Debug("orphan_skip.kill_tmux_failed", "title", c.Title, "err", err.Error())
		}
	}

	// Apply recovery BEFORE clearing pendingOrphans / orphanCfgDirs.
	// applyOrphanRecovery looks up each candidate's cfgDir in
	// orphanCfgDirs to route the recovered Instance to the right
	// workspace slot; clearing the map first would force every cfgDir
	// lookup to return "" → listForCfgDir falls through to m.list (the
	// focused slot), and all orphans land in the wrong workspace.
	cmd := m.applyOrphanRecovery(selected)
	m.pendingOrphans = nil
	m.orphanCfgDirs = nil

	// Run the deferred next-overlay closure (pendingDir confirm or
	// startup workspace picker) that newHome captured before showing
	// the orphan overlay.
	next := m.pendingStartupOverlay
	m.pendingStartupOverlay = nil
	if next != nil {
		next()
	}

	return m, tea.Batch(cmd, tea.WindowSize(), m.instanceChanged())
}
