package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/buildsweep"
)

// buildSweepInterval is the build sweep's cadence: often enough to keep a
// threshold in hours to within half an hour, rarely enough that walking
// every Rust session's target dir costs nothing noticeable.
const buildSweepInterval = 30 * time.Minute

// errBuildSweepOff is a sweep's error when no threshold is set: the
// setting is absent or 0 and the request named none.
var errBuildSweepOff = errors.New("the build sweep is off: set build_sweep_after_hours in the global config.json, or pass --after")

// buildSweepResult is a build sweep's job result: its report, or why it
// didn't run.
type buildSweepResult struct {
	report buildsweep.Report
	err    error
}

// maybeBuildSweep dispatches the periodic build sweep when gateBuildSweep
// is due, over the sessions whose agent is idle or paused. The job reads
// the threshold, so a sweep turned off costs one config read an interval.
// Reports whether it dispatched; false when no session is eligible.
func (m *Model) maybeBuildSweep() bool {
	return m.dispatchGated(gateBuildSweep, time.Now(), func() Job {
		targets := m.buildSweepTargets(false)
		if len(targets) == 0 {
			return nil
		}
		return buildSweepJob(buildsweep.Default(), targets, buildsweep.Options{})
	})
}

// SweepBuilds sweeps every session's build dir now, as the periodic sweep
// would (or, with opts.DryRun, reports what would go), and answers with
// the Reply's Sweep. The sessions the periodic sweep passes over are
// reported as skipped, with their status.
func (m *Model) SweepBuilds(opts buildsweep.Options, req ReqID) {
	m.spawn(m.track(req, 0, buildSweepJob(buildsweep.Default(), m.buildSweepTargets(true), opts)))
}

// buildSweepTargets are the sessions a build sweep covers, in every
// workspace the model serves: those whose agent is Ready or Paused, and,
// with all, every other session with a worktree, marked skipped for its
// status. A workspace terminal runs in the main checkout and is never
// listed.
func (m *Model) buildSweepTargets(all bool) []buildsweep.Target {
	var out []buildsweep.Target
	for _, ws := range m.workspaces {
		for _, inst := range ws.insts {
			p := inst.GetWorktreePath()
			if inst.IsWorkspaceTerminal || p == "" {
				continue
			}
			st := inst.GetStatus()
			t := buildsweep.Target{Workspace: ws.Label(), Title: inst.Title, Status: st.String(), Worktree: p}
			if st != session.Ready && st != session.Paused {
				if !all {
					continue
				}
				t.Skip, t.Detail = buildsweep.SkipStatus, "the agent is "+st.String()
			}
			out = append(out, t)
		}
	}
	return out
}

// buildSweepJob runs a sweep off the loop: it resolves the threshold
// (opts.AfterHours, else the global config's build_sweep_after_hours,
// read now so an edit takes effect without a restart) and sweeps targets.
func buildSweepJob(s buildsweep.Sweeper, targets []buildsweep.Target, opts buildsweep.Options) Job {
	return func() any {
		hours := opts.AfterHours
		if hours == 0 {
			hours = config.LoadConfigFromGlobal().GetBuildSweepAfterHours()
		}
		if hours <= 0 {
			return buildSweepResult{err: errBuildSweepOff}
		}
		return buildSweepResult{report: s.Run(targets, time.Duration(hours)*time.Hour, opts.DryRun)}
	}
}

// deliverBuildSweep raises a Notice for each mount the periodic sweep
// skipped for its access times, once per mount while the daemon runs: a
// sweep that can't work must say so, but not every half hour. A request's
// own sweep raises none, since its report says it.
func (m *Model) deliverBuildSweep(r buildSweepResult) {
	if m.cause != 0 {
		return
	}
	for _, res := range r.report.Results {
		if res.Skip != buildsweep.SkipAtimeOff || m.buildSweepWarned[res.Detail] {
			continue
		}
		if m.buildSweepWarned == nil {
			m.buildSweepWarned = map[string]bool{}
		}
		m.buildSweepWarned[res.Detail] = true
		m.emit(Notice{Err: fmt.Errorf("build sweep: %s; build dirs there are not swept", res.Detail)})
	}
}
