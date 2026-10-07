package app

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// claudeTmpJob is one workspace's queued Claude temp-dir sweep, with its
// claim set and the known config dirs as they stood when it was queued.
type claudeTmpJob struct {
	cfgDir  string
	claimed map[string]bool
	others  []string
}

// requestClaudeTmpSweep queues a sweep of cfgDir's Claude temp dirs
// (session.SweepClaudeTemp), snapshotting the claim set (list plus the
// records storage preserves) here, on the Update goroutine.
// reconcileOrphans calls it, so every workspace-load path queues one, after
// the orphan auto-clean removed its worktrees. The health tick dispatches
// it; a request made while a sweep runs gets one more pass when it lands.
func (m *home) requestClaudeTmpSweep(cfgDir string, list *ui.List, storage *session.Storage) {
	if cfgDir == "" {
		return
	}
	if m.claudeTmpPending == nil {
		m.claudeTmpPending = map[string]claudeTmpJob{}
	}
	m.claudeTmpPending[cfgDir] = claudeTmpJob{
		cfgDir:  cfgDir,
		claimed: claimedWorktreePaths(list.GetInstances(), storage),
		others:  m.knownConfigDirs(),
	}
	m.gate(gateClaudeTmp).request()
}

// knownConfigDirs lists every registered workspace's config dir and the
// global one, so a sweep can leave alone any name another workspace could
// own (see session.SweepClaudeTemp).
func (m *home) knownConfigDirs() []string {
	var dirs []string
	if m.registry != nil {
		for i := range m.registry.Workspaces {
			if ws := &m.registry.Workspaces[i]; ws.Path != "" {
				dirs = append(dirs, config.WorkspaceConfigDir(ws))
			}
		}
	}
	if dir, err := config.GetGlobalConfigDir(); err == nil {
		dirs = append(dirs, dir)
	}
	return dirs
}

// maybeClaudeTmpSweep dispatches the queued sweeps when gateClaudeTmp is
// due (none in flight). nil when nothing is queued. Update goroutine only.
func (m *home) maybeClaudeTmpSweep() tea.Cmd {
	return m.dispatchGated(gateClaudeTmp, time.Now(), func() tea.Cmd {
		if len(m.claudeTmpPending) == 0 {
			return nil
		}
		jobs := slices.Collect(maps.Values(m.claudeTmpPending))
		m.claudeTmpPending = nil
		return claudeTmpSweepCmd(jobs)
	})
}

// claudeTmpSweepCmd runs the sweeps off the Update goroutine. Each logs
// what it archived and nothing comes back to apply, so the message is nil
// (deliverGated still disarms the gate).
func claudeTmpSweepCmd(jobs []claudeTmpJob) tea.Cmd {
	return func() tea.Msg {
		for _, j := range jobs {
			session.SweepClaudeTemp(j.cfgDir, j.claimed, j.others)
		}
		return nil
	}
}
