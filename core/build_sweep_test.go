package core

import (
	"os"
	"path/filepath"
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
		return buildSweepJob(buildsweep.Default(), targets, opts, 0)().(buildSweepResult)
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

// withGlobalConfig writes body as the isolated global config.json and
// restores what was there when the test ends; it returns the file's path.
func withGlobalConfig(t *testing.T, body string) string {
	t.Helper()
	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, config.ConfigFileName)
	prev, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
			return
		}
		_ = os.WriteFile(path, prev, 0o644)
	})
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func modelChanged(events []Event) *ModelChanged {
	for _, ev := range events {
		if mc, ok := ev.(ModelChanged); ok {
			return &mc
		}
	}
	return nil
}

func TestConfigureBuildSweep_SetsOnlyThatFieldOfTheGlobalConfig(t *testing.T) {
	path := withGlobalConfig(t, `{"default_program": "aider", "branch_prefix": "x/"}`)
	m := NewForTest(Options{})
	m.Sync()

	require.NoError(t, m.ConfigureBuildSweep(12))

	cfg, err := config.ReadConfigFrom(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, 12, cfg.GetBuildSweepAfterHours())
	assert.Equal(t, "aider", cfg.DefaultProgram, "the other fields are kept")
	assert.Equal(t, "x/", cfg.BranchPrefix)
	assert.Equal(t, BuildSweepView{AfterHours: 12}, m.BuildSweep())
	mc := modelChanged(m.Sync().Events)
	require.NotNil(t, mc, "the change republishes")
	assert.Equal(t, 12, mc.View.BuildSweep.AfterHours)
}

func TestConfigureBuildSweep_RefusesAnUnreadableGlobalConfig(t *testing.T) {
	path := withGlobalConfig(t, "{not json")
	m := NewForTest(Options{})

	assert.ErrorContains(t, m.ConfigureBuildSweep(12), "parse")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data), "left byte for byte")
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), "corrupt", "nothing quarantined")
	}
	assert.Zero(t, m.BuildSweep().AfterHours)
}

func TestConfigureBuildSweep_RefusesAThresholdOutOfRange(t *testing.T) {
	path := withGlobalConfig(t, `{"build_sweep_after_hours": 6}`)
	m := NewForTest(Options{})
	for _, bad := range []int{-1, config.MaxBuildSweepAfterHours + 1} {
		assert.Error(t, m.ConfigureBuildSweep(bad), bad)
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"build_sweep_after_hours": 6}`, string(data))
}

// TestConfigureBuildSweep_TheWorkspaceSavingThatFileFollows: a served
// workspace whose config is the global file takes the value in memory, so
// its next SaveSettings, from a copy made before the change, keeps it;
// another workspace's config is left alone.
func TestConfigureBuildSweep_TheWorkspaceSavingThatFileFollows(t *testing.T) {
	global, a := storedWorkspace(t, ""), storedWorkspace(t, "a")
	t.Setenv(config.EnvHome, global.ctx.ConfigDir)
	t.Setenv(config.EnvGlobalDir, global.ctx.ConfigDir)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(global, a)
	stale := global.cfg.Snapshot()

	require.NoError(t, m.ConfigureBuildSweep(12))
	assert.Equal(t, 12, global.cfg.Snapshot().BuildSweepAfterHours)
	assert.Zero(t, a.cfg.Snapshot().BuildSweepAfterHours)

	stale.DefaultProgram = "aider"
	require.NoError(t, m.SaveSettings(m.wsIDOf(global), stale))
	cfg, err := config.ReadConfigFrom(global.ctx.ConfigDir)
	require.NoError(t, err)
	assert.Equal(t, "aider", cfg.DefaultProgram, "fixture: the save landed")
	assert.Equal(t, 12, cfg.GetBuildSweepAfterHours(), "the old copy didn't write the old threshold back")
}

func TestBuildSweepJob_ItsReadUpdatesTheView(t *testing.T) {
	withGlobalConfig(t, `{"build_sweep_after_hours": 6}`)
	m := NewForTest(Options{})
	job := buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true}, m.buildSweepGen)

	m.Deliver(job())

	assert.Equal(t, 6, m.BuildSweep().AfterHours)
}

func TestBuildSweepJob_AReadBeforeAConfigureIsDropped(t *testing.T) {
	withGlobalConfig(t, `{"build_sweep_after_hours": 6}`)
	m := NewForTest(Options{})
	job := buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true}, m.buildSweepGen)
	res := job() // read 6

	require.NoError(t, m.ConfigureBuildSweep(12))
	m.Deliver(res)

	assert.Equal(t, 12, m.BuildSweep().AfterHours)
}

func TestBuildSweepJob_AnUnreadableGlobalConfigFailsTheSweep(t *testing.T) {
	path := withGlobalConfig(t, "{not json")
	res := buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true}, 0)().(buildSweepResult)

	assert.ErrorContains(t, res.err, "can't be read")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data), "not quarantined")
	assert.Equal(t, 12, buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true, AfterHours: 12}, 0)().(buildSweepResult).report.AfterHours, "a request's threshold still sweeps")
}

func TestInitBuildSweep_ReadsTheGlobalThreshold(t *testing.T) {
	withGlobalConfig(t, `{"build_sweep_after_hours": 24}`)
	m := NewForTest(Options{})

	m.initBuildSweep()

	assert.Equal(t, 24, m.BuildSweep().AfterHours)
}
