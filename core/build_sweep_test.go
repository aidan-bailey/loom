package core

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sweepInst is a session record in status st whose worktree is a fresh
// temp dir, as a reconciled record comes back (FromInstanceData).
func sweepInst(t *testing.T, title string, st session.Status, terminal bool) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: st, Program: "claude", IsWorkspaceTerminal: terminal,
		Worktree: session.GitWorktreeData{WorktreePath: t.TempDir()},
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// sweepModel serves one workspace holding insts.
func sweepModel(t *testing.T, insts ...*session.Instance) *Model {
	t.Helper()
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "ws")
	ws.insts = insts
	m.SetWorkspacesForTest(ws)
	return m
}

func skips(targets []buildsweep.Target) map[string]buildsweep.Skip {
	out := map[string]buildsweep.Skip{}
	for _, t := range targets {
		out[t.Title] = t.Skip
	}
	return out
}

// replyOf runs the one job a request queued, delivers its result, and
// returns the Reply the model emitted.
func replyOf(t *testing.T, m *Model) Reply {
	t.Helper()
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	m.Deliver(jobs[0]())
	for _, ev := range m.Drain().Events {
		if r, ok := ev.(Reply); ok {
			return r
		}
	}
	require.Fail(t, "no Reply")
	return Reply{}
}

func TestBuildSweepTargets_OnlyIdleAndPausedSessions(t *testing.T) {
	m := sweepModel(t,
		sweepInst(t, "ready", session.Ready, false),
		sweepInst(t, "paused", session.Paused, false),
		sweepInst(t, "running", session.Running, false),
		sweepInst(t, "loading", session.Loading, false),
		sweepInst(t, "prompting", session.Prompting, false),
		sweepInst(t, "deleting", session.Deleting, false),
		sweepInst(t, "recoverable", session.Recoverable, false),
		sweepInst(t, "terminal", session.Ready, true),
	)

	assert.Equal(t, map[string]buildsweep.Skip{"ready": "", "paused": ""}, skips(m.buildSweepTargets(false)))
	assert.Equal(t, map[string]buildsweep.Skip{
		"ready": "", "paused": "",
		"running": buildsweep.SkipStatus, "loading": buildsweep.SkipStatus, "prompting": buildsweep.SkipStatus,
		"deleting": buildsweep.SkipStatus, "recoverable": buildsweep.SkipStatus,
	}, skips(m.buildSweepTargets(true)), "a request reports the rest; the terminal is never listed")
}

func TestMaybeBuildSweep_NoIdleSessionArmsNothing(t *testing.T) {
	m := sweepModel(t, sweepInst(t, "running", session.Running, false))

	assert.False(t, m.maybeBuildSweep())
	assert.False(t, m.gate(gateBuildSweep).inFlight)
	assert.Empty(t, m.Drain().Jobs)
}

func TestBuildSweepJob_ReadsTheThresholdFromTheGlobalConfig(t *testing.T) {
	targets := []buildsweep.Target{{Title: "s", Worktree: t.TempDir()}}
	job := func(opts buildsweep.Options) buildSweepResult {
		return buildSweepJob(buildsweep.Default(), targets, opts)().(buildSweepResult)
	}

	assert.ErrorIs(t, job(buildsweep.Options{}).err, errBuildSweepOff)

	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	cfg := config.LoadConfigFrom(dir)
	cfg.BuildSweepAfterHours = 12
	require.NoError(t, config.SaveConfigTo(cfg, dir))
	t.Cleanup(func() {
		cfg.BuildSweepAfterHours = 0
		_ = config.SaveConfigTo(cfg, dir)
	})

	res := job(buildsweep.Options{DryRun: true})
	require.NoError(t, res.err)
	assert.Equal(t, 12, res.report.AfterHours)
	require.Len(t, res.report.Results, 1)
	assert.Equal(t, buildsweep.SkipNoTarget, res.report.Results[0].Skip)

	assert.Equal(t, 48, job(buildsweep.Options{DryRun: true, AfterHours: 48}).report.AfterHours, "a request's threshold wins")
}

func TestDeliverBuildSweep_NoticesAMountWithoutAccessTimesOnce(t *testing.T) {
	m := NewForTest(Options{})
	off := buildsweep.Result{Target: buildsweep.Target{Workspace: "ws", Title: "a", Skip: buildsweep.SkipAtimeOff, Detail: "/tb is mounted noatime, so it records no access times"}}
	other := off
	other.Title = "b"
	res := buildSweepResult{report: buildsweep.Report{Results: []buildsweep.Result{off, other}}}

	m.Deliver(res)
	m.Deliver(res)

	var notices []Notice
	for _, ev := range m.Drain().Events {
		if n, ok := ev.(Notice); ok {
			notices = append(notices, n)
		}
	}
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0].Err.Error(), "/tb is mounted noatime")
}

func TestSweepBuilds_RepliesWithTheReport(t *testing.T) {
	m := sweepModel(t, sweepInst(t, "idle", session.Ready, false), sweepInst(t, "busy", session.Running, false))

	m.SweepBuilds(buildsweep.Options{DryRun: true, AfterHours: 48}, 7)
	reply := replyOf(t, m)

	assert.Equal(t, ReqID(7), reply.Req)
	require.NoError(t, reply.Err)
	assert.True(t, reply.Sweep.DryRun)
	got := map[string]buildsweep.Skip{}
	for _, r := range reply.Sweep.Results {
		got[r.Title] = r.Skip
	}
	assert.Equal(t, map[string]buildsweep.Skip{"idle": buildsweep.SkipNoTarget, "busy": buildsweep.SkipStatus}, got)
}

func TestSweepBuilds_WithoutAThresholdFails(t *testing.T) {
	m := sweepModel(t, sweepInst(t, "idle", session.Ready, false))

	m.SweepBuilds(buildsweep.Options{DryRun: true}, 7)

	assert.ErrorIs(t, replyOf(t, m).Err, errBuildSweepOff)
}
