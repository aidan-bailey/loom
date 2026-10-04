package core

import (
	"errors"
	"fmt"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Persistable filters out instances whose state should not reach disk:
// a creation flow's instance that has never started (Ready and not Started),
// Deleting (kill in progress, about to be removed via DeleteInstance), and
// Recoverable (an orphan surfaced inline; it is re-derived from disk each load
// and adopted only on explicit recovery, so persisting it would resurrect a
// never-confirmed entry). Every other instance is persisted — Loading,
// Running, Prompting, Paused and a started Ready one — so that a crash or quit
// during the kill window cannot orphan a live worktree from its JSON record.
//
// Ready is overloaded: a creation flow's instance is Ready before it starts,
// and the status ladder and Claude's roster report an idle agent or workspace
// terminal as Ready too. Skipping every Ready instance dropped idle sessions'
// records on each save, so the next load offered their worktrees as
// Recoverable orphans and killed and recreated an idle workspace terminal.
func Persistable(instances []*session.Instance) []*session.Instance {
	var result []*session.Instance
	for _, inst := range instances {
		status := inst.GetStatus()
		if (status == session.Ready && !inst.Started()) || status == session.Deleting || status == session.Recoverable {
			continue
		}
		result = append(result, inst)
	}
	return result
}

// quitSkipsSave reports whether a save error on quit is the storage's write
// latch (ErrStorageLoadFailed). The sticky-quit policy exists so the user can
// fix the cause and retry, but a latched storage is never reloaded by the
// TUI, so no retry could succeed; its list is also empty by construction
// (latchedStorageErr), and the unreadable file is left untouched. Quit.
func quitSkipsSave(err error) bool {
	return errors.Is(err, session.ErrStorageLoadFailed)
}

// Save persists ws's instances after a change. A workspace no longer
// loaded is saved only if no loaded workspace holds the same workspace:
// that one reloaded state.json into its own, newer copy, which a save from
// the dropped one's stale copy would overwrite. Formerly app.saveSlot.
func (m *Model) Save(ws *Workspace) error {
	if !m.IsLoaded(ws) && m.Reopened(ws) {
		log.For("core").Warn("closed_slot_save_skipped", "workspace", ws.Label(), "reason", "workspace_reopened")
		return nil
	}
	return ws.storage.SaveInstances(Persistable(ws.insts))
}

// SaveForQuit saves every loaded workspace and the registry's open list
// before the TUI exits. A failed save is returned, and the TUI then
// refuses to quit so the user can fix the cause and retry (silent data
// loss on exit is worse than a sticky quit), except the storage's write
// latch (quitSkipsSave), which no retry could clear. With no tab open the
// open list is written only if it holds something: it then keeps just
// the workspaces that failed to restore. Formerly handleQuit's saves.
func (m *Model) SaveForQuit() error {
	if len(m.tabs) > 0 {
		var firstErr error
		for _, ws := range m.tabs {
			if err := ws.storage.SaveInstances(Persistable(ws.insts)); err != nil {
				if quitSkipsSave(err) {
					log.For("core").Warn("quit.save_skipped", "name", ws.Name(), "reason", "storage_load_failed", "err", err)
					continue
				}
				log.For("core").Error("workspace.save_failed", "name", ws.Name(), "err", err)
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to save workspace %s: %w", ws.Name(), err)
				}
			}
		}
		if firstErr != nil {
			return firstErr
		}
		m.PersistOpenList()
		return nil
	}
	if err := m.classic.storage.SaveInstances(Persistable(m.classic.insts)); err != nil {
		if !quitSkipsSave(err) {
			return err
		}
		log.For("core").Warn("quit.save_skipped", "reason", "storage_load_failed", "err", err)
	}
	// Classic/global mode has no open tabs: this clears the list,
	// except for workspaces that failed to restore (restoreFailed).
	if m.registry != nil && len(m.registry.OpenWorkspaces) > 0 {
		m.PersistOpenList()
	}
	return nil
}
