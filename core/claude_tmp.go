package core

import (
	"maps"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
)

// claudeTmpJob is one workspace's queued Claude temp-dir sweep, with its
// claim set and the known config dirs as they stood when it was queued.
type claudeTmpJob struct {
	cfgDir  string
	claimed map[string]bool
	others  []string
}

// requestClaudeTmpSweep queues a sweep of cfgDir's Claude temp dirs
// (session.SweepClaudeTemp), snapshotting ws's claim set (its instances
// plus the records its storage preserves) here, on the model's goroutine.
// reconcileOrphans calls it, so every workspace-load path queues one, after
// the orphan auto-clean removed its worktrees. The health tick dispatches
// it; a request made while a sweep runs gets one more pass when it lands.
func (m *Model) requestClaudeTmpSweep(ws *Workspace, cfgDir string) {
	if cfgDir == "" {
		return
	}
	if m.claudeTmpPending == nil {
		m.claudeTmpPending = map[string]claudeTmpJob{}
	}
	m.claudeTmpPending[cfgDir] = claudeTmpJob{
		cfgDir:  cfgDir,
		claimed: claimedWorktreePaths(ws.insts, ws.storage),
		others:  m.knownConfigDirs(),
	}
	m.gate(gateClaudeTmp).request()
}

// knownConfigDirs lists every registered workspace's config dir and the
// global one, so a sweep can leave alone any name another workspace could
// own (see session.SweepClaudeTemp).
func (m *Model) knownConfigDirs() []string {
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
// due (none in flight). Reports whether it dispatched; false when nothing
// is queued.
func (m *Model) maybeClaudeTmpSweep() bool {
	return m.dispatchGated(gateClaudeTmp, time.Now(), func() Job {
		if len(m.claudeTmpPending) == 0 {
			return nil
		}
		jobs := slices.Collect(maps.Values(m.claudeTmpPending))
		m.claudeTmpPending = nil
		return claudeTmpSweepJob(jobs)
	})
}

// claudeTmpSweepJob runs the sweeps off the model's goroutine. Each logs
// what it archived and nothing comes back to apply, so the result is nil
// (deliverGated still disarms the gate).
func claudeTmpSweepJob(jobs []claudeTmpJob) Job {
	return func() any {
		for _, j := range jobs {
			session.SweepClaudeTemp(j.cfgDir, j.claimed, j.others)
		}
		return nil
	}
}
