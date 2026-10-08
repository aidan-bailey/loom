package core

import (
	"errors"
	"fmt"
	"os"

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
// fix the cause and retry, but a latched storage refuses every write: only
// an open of its workspace clears it, by reading the workspace afresh
// (retryLoad), so no retried quit could succeed. Its list is also empty by
// construction (the TUI's latchedStorageErr), and the unreadable file is
// left untouched. Quit.
func quitSkipsSave(err error) bool {
	return errors.Is(err, session.ErrStorageLoadFailed)
}

// saveWS persists ws's instances after a change. Formerly app.saveSlot.
func (m *Model) saveWS(ws *Workspace) error {
	return ws.storage.SaveInstances(Persistable(ws.insts))
}

// SaveForQuit saves every workspace the model serves before the TUI exits
// (the TUI persists its own open list first: PersistOpenList). A failed
// save of a workspace a client opened in this run is returned, and the TUI
// then refuses to quit so the user can fix the cause and retry (silent data
// loss on exit is worse than a sticky quit), except the storage's write
// latch (quitSkipsSave), which no retry could clear.
//
// A workspace nobody opened is still saved, since its records change
// without a client (its agents are crash-restarted at boot and paused by
// the tick), but it must not hold quit hostage: a registered repository on
// an unmounted or read-only path would refuse every quit, and every
// takeover would time out. So its failure is only logged, and when its
// config dir is gone (a deleted or unmounted repository) it is skipped,
// since the save would create the dir. Formerly handleQuit's saves.
func (m *Model) SaveForQuit() error {
	var firstErr error
	for _, ws := range m.workspaces {
		if ws.storage == nil {
			continue
		}
		if !ws.opened {
			if dir := ws.configDir(); dir != "" {
				if _, err := os.Stat(dir); err != nil {
					log.For("core").Warn("quit.save_skipped", "name", ws.Name(), "reason", "config_dir_unavailable", "err", err)
					continue
				}
			}
		}
		err := ws.storage.SaveInstances(Persistable(ws.insts))
		switch {
		case err == nil:
		case quitSkipsSave(err):
			log.For("core").Warn("quit.save_skipped", "name", ws.Name(), "reason", "storage_load_failed", "err", err)
		case !ws.opened:
			log.For("core").Warn("quit.save_failed_unopened", "name", ws.Name(), "err", err)
		default:
			log.For("core").Error("workspace.save_failed", "name", ws.Name(), "err", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to save workspace %s: %w", ws.Label(), err)
			}
		}
	}
	return firstErr
}
