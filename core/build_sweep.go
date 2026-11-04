package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/buildsweep"
)

// buildSweepInterval is the build sweep's cadence: often enough to keep a
// threshold in hours to within half an hour, rarely enough that walking
// every Rust session's target dir costs nothing noticeable.
const buildSweepInterval = 30 * time.Minute

// errBuildSweepOff is a sweep's error when no threshold is set: the
// setting is absent or 0 and the request named none.
var errBuildSweepOff = errors.New("the build sweep is off: turn it on in Settings (S) → Build Sweep (build_sweep_after_hours in the global config.json), or pass --after")

// buildSweepResult is a build sweep's job result: its report, or why it
// didn't run.
type buildSweepResult struct {
	report buildsweep.Report
	err    error
	// configured is the global config's threshold as the job read it,
	// when read is true; gen is buildSweepGen when the job was made.
	configured int
	read       bool
	gen        uint64
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
		return buildSweepJob(buildsweep.Default(), targets, buildsweep.Options{}, m.buildSweepGen)
	})
}

// SweepBuilds sweeps every session's build dir now, as the periodic sweep
// would (or, with opts.DryRun, reports what would go), and answers with
// the Reply's Sweep. The sessions the periodic sweep passes over are
// reported as skipped, with their status.
func (m *Model) SweepBuilds(opts buildsweep.Options, req ReqID) {
	m.spawn(m.track(req, 0, buildSweepJob(buildsweep.Default(), m.buildSweepTargets(true), opts, m.buildSweepGen)))
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

// buildSweepJob runs a sweep off the loop: it reads the global config's
// threshold strictly (globalBuildSweepHours), now, so an edit takes effect
// without a restart; sweeps targets with opts.AfterHours or else that
// threshold; and carries the read back for the view. gen is the model's
// buildSweepGen when the job was made.
func buildSweepJob(s buildsweep.Sweeper, targets []buildsweep.Target, opts buildsweep.Options, gen uint64) Job {
	return func() any {
		r := buildSweepResult{gen: gen}
		configured, err := globalBuildSweepHours()
		r.configured, r.read = configured, err == nil
		hours := opts.AfterHours
		if hours == 0 {
			if err != nil {
				r.err = fmt.Errorf("build sweep: the global config can't be read: %w", err)
				return r
			}
			hours = configured
		}
		if hours <= 0 {
			r.err = errBuildSweepOff
			return r
		}
		r.report = s.Run(targets, time.Duration(hours)*time.Hour, opts.DryRun)
		return r
	}
}

// BuildSweepView is the build sweep's setting as a client sees it
// (Core.BuildSweep): the global config's build_sweep_after_hours, 0 for
// off.
type BuildSweepView struct {
	AfterHours int
}

// BuildSweep is the build sweep's setting (Core.BuildSweep).
func (m *Model) BuildSweep() BuildSweepView { return BuildSweepView{AfterHours: m.buildSweepHours} }

// globalBuildSweepHours reads build_sweep_after_hours from the global
// config.json, strictly (config.ReadConfigFrom).
func globalBuildSweepHours() (int, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return 0, err
	}
	cfg, err := config.ReadConfigFrom(dir)
	if err != nil {
		return 0, err
	}
	return cfg.GetBuildSweepAfterHours(), nil
}

// initBuildSweep reads the build sweep's threshold at boot. One that
// can't be read leaves it 0 until a sweep job or ConfigureBuildSweep
// reads it.
func (m *Model) initBuildSweep() {
	hours, err := globalBuildSweepHours()
	if err != nil {
		log.For("build_sweep").Warn("build_sweep.config_unreadable", "err", err)
		return
	}
	m.buildSweepHours = hours
}

// ConfigureBuildSweep sets the build sweep's threshold to afterHours (0
// turns the sweep off) in the global config.json, whichever workspace asks
// (Core.ConfigureBuildSweep). The file is read strictly and only that
// field changes. A served workspace whose settings are that file takes the
// value in memory too, so its next SaveSettings keeps it. When the value
// is out of range or the file can't be read or written, nothing changes.
func (m *Model) ConfigureBuildSweep(afterHours int) error {
	if err := config.ValidateBuildSweepAfterHours(afterHours); err != nil {
		return err
	}
	dir, err := config.GetConfigDir()
	if err != nil {
		return fmt.Errorf("configure build sweep: %w", err)
	}
	cfg, err := config.ReadConfigFrom(dir)
	if err != nil {
		return fmt.Errorf("configure build sweep: %w", err)
	}
	set := func(c *config.Config) { c.BuildSweepAfterHours = afterHours }
	cfg.Mutate(set)
	if err := config.SaveConfigTo(cfg, dir); err != nil {
		return fmt.Errorf("configure build sweep: %w", err)
	}
	for _, ws := range m.workspaces {
		if ws.cfg != nil && canonicalDir(settingsDir(ws)) == canonicalDir(dir) {
			ws.cfg.Mutate(set)
		}
	}
	m.buildSweepHours = afterHours
	m.buildSweepGen++
	return nil
}

// deliverBuildSweep takes the threshold the job read for the view, unless a
// ConfigureBuildSweep came after the job was made, and raises a Notice for
// each mount the periodic sweep skipped for its access times, once per
// mount while the daemon runs: a sweep that can't work must say so, but
// not every half hour. A request's own sweep raises none, since its report
// says it.
func (m *Model) deliverBuildSweep(r buildSweepResult) {
	if r.read && r.gen == m.buildSweepGen {
		m.buildSweepHours = r.configured
	}
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
