package session

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeRoot points Claude's temp root at a fresh dir for this test and
// returns it, resolved.
func claudeRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv(claudetmp.EnvTmpDir, base)
	root := filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()))
	require.NoError(t, os.Mkdir(root, 0o700))
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return resolved
}

// claudeTempDir creates the temp dir Claude keeps for a session in wt,
// named after wt's physical path, with a scratchpad note.
func claudeTempDir(t *testing.T, root, wt string) string {
	t.Helper()
	dir := filepath.Join(root, claudetmp.Names(wt)[0].Value)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sess-1", "scratchpad"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sess-1", "scratchpad", "notes.md"), []byte("plan"), 0o600))
	return dir
}

// withConfigDir sets the instance's ConfigDir to the one its fixture
// worktree lives in (<configDir>/worktrees/<leaf>), as production does.
func withConfigDir(inst *Instance) {
	inst.ConfigDir = filepath.Dir(filepath.Dir(inst.getGitWorktree().GetWorktreePath()))
}

func archivedZips(t *testing.T, inst *Instance) []string {
	t.Helper()
	zips, err := filepath.Glob(filepath.Join(claudetmp.ArchiveDir(inst.ConfigDir), "*.zip"))
	require.NoError(t, err)
	return zips
}

func manifestOf(t *testing.T, zipPath string) claudetmp.Manifest {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == claudetmp.ManifestName {
			rc, err := f.Open()
			require.NoError(t, err)
			defer rc.Close()
			data, err := io.ReadAll(rc)
			require.NoError(t, err)
			var m claudetmp.Manifest
			require.NoError(t, json.Unmarshal(data, &m))
			return m
		}
	}
	t.Fatalf("%s has no manifest", zipPath)
	return claudetmp.Manifest{}
}

func TestPause_ArchivesClaudeTempAndResumeRestoresIt(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	wt := inst.getGitWorktree().GetWorktreePath()
	dir := claudeTempDir(t, root, wt)

	require.NoError(t, inst.Pause(nil))

	assert.NoDirExists(t, dir, "Pause archives the temp dir and removes it")
	zips := archivedZips(t, inst)
	require.Len(t, zips, 1)
	m := manifestOf(t, zips[0])
	assert.Equal(t, "pause", m.Reason)
	assert.Equal(t, wt, m.Worktree)

	require.NoError(t, inst.Resume(nil))

	data, err := os.ReadFile(filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))
	require.NoError(t, err, "Resume restores the scratchpad before the agent launches")
	assert.Equal(t, "plan", string(data))
	assert.Empty(t, archivedZips(t, inst), "a restored archive is deleted")
}

// TestRelaunchInPlace_RestoresClaudeTemp: the other launch path. The agent
// exited on its own, the worktree is intact, and a zip is parked.
func TestRelaunchInPlace_RestoresClaudeTemp(t *testing.T) {
	root := claudeRoot(t)
	inst, srv := newTickPausedInstance(t)
	withConfigDir(inst)
	wt := inst.getGitWorktree().GetWorktreePath()
	dir := claudeTempDir(t, root, wt)
	archiveClaudeTemp(inst.ConfigDir, wt, "pause")
	require.NoDirExists(t, dir)

	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))
	assert.Len(t, srv.launchArgs(), 1, "the agent was relaunched in place")
}

// seeScratchpadBeforeLaunch makes srv answer has-session "dead" until the
// agent is launched, noting on each probe whether scratch exists, and
// returns what it saw. finishResume's own liveness probe runs just before
// it launches, so the last answer is the disk as the agent will find it.
func seeScratchpadBeforeLaunch(srv *fakeTmuxServer, scratch string) func() []bool {
	var mu sync.Mutex
	var seen []bool
	srv.mu.Lock()
	srv.probe = func() error {
		if len(srv.launchArgs()) > 0 {
			return nil
		}
		_, err := os.Stat(scratch)
		mu.Lock()
		seen = append(seen, err == nil)
		mu.Unlock()
		return errors.New("can't find session")
	}
	srv.mu.Unlock()
	return func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return append([]bool(nil), seen...)
	}
}

// TestResume_RestoresClaudeTempBeforeTheAgentLaunches: Claude creates its
// temp dir as it starts, so a restore that ran after the launch would find
// the dir there already and leave the scratchpad in the zip.
func TestResume_RestoresClaudeTempBeforeTheAgentLaunches(t *testing.T) {
	t.Run("rebuild", func(t *testing.T) {
		root := claudeRoot(t)
		srv := &fakeTmuxServer{}
		inst := newTestPausableInstanceWithExec(t, srv.runner())
		inst.program = "claude"
		orig := newRecoverySession
		newRecoverySession = func(name, program string, env ...string) *tmux.Session {
			return tmux.NewSessionWithDeps(name, program, srv, srv.runner(), env...)
		}
		t.Cleanup(func() { newRecoverySession = orig })
		withConfigDir(inst)
		dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
		require.NoError(t, inst.Pause(nil))
		require.NoDirExists(t, dir)
		seen := seeScratchpadBeforeLaunch(srv, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))

		require.NoError(t, inst.Resume(nil))

		require.Len(t, srv.launchArgs(), 1)
		probes := seen()
		require.NotEmpty(t, probes)
		assert.True(t, probes[len(probes)-1], "the scratchpad was back before the agent launched")
	})
	t.Run("relaunch in place", func(t *testing.T) {
		root := claudeRoot(t)
		inst, srv := newTickPausedInstance(t)
		withConfigDir(inst)
		wt := inst.getGitWorktree().GetWorktreePath()
		dir := claudeTempDir(t, root, wt)
		archiveClaudeTemp(inst.ConfigDir, wt, "pause")
		require.NoDirExists(t, dir)
		seen := seeScratchpadBeforeLaunch(srv, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))

		require.NoError(t, inst.Resume(nil))

		require.Len(t, srv.launchArgs(), 1)
		probes := seen()
		require.NotEmpty(t, probes)
		assert.True(t, probes[len(probes)-1], "the scratchpad was back before the agent launched")
	})
}

// TestPause_ArchivesBeforeTheInstanceIsPaused: once the status reads Paused
// the app can start a resume. One that began while the archive was still
// deleting the temp dir would find it in place, skip the restore, launch
// Claude in it, and have the archive delete what the new session writes.
func TestPause_ArchivesBeforeTheInstanceIsPaused(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())

	var statuses []Status
	var savesAtArchive []int
	saves := 0
	orig := archiveClaudeTempFn
	archiveClaudeTempFn = func(configDir, worktreePath, reason string) {
		statuses = append(statuses, inst.GetStatus())
		savesAtArchive = append(savesAtArchive, saves)
		orig(configDir, worktreePath, reason)
	}
	t.Cleanup(func() { archiveClaudeTempFn = orig })

	require.NoError(t, inst.Pause(func() error { saves++; return nil }))

	require.Len(t, statuses, 1, "Pause archives once")
	assert.NotEqual(t, Paused, statuses[0], "a Paused status invites a resume that races the archive")
	assert.Zero(t, savesAtArchive[0], "the Paused checkpoint is saved only once the archive is done")
	assert.Equal(t, Paused, inst.GetStatus())
	assert.Equal(t, 1, saves)
	assert.NoDirExists(t, dir)
}

// TestKill_AnAlreadyDeadSessionIsNotAFailure: kill-session fails for a
// session that is already gone (a paused or crashed instance's), and tmux
// answers that it is not there. That is a success, so the kill goes through
// and archives the dir, as Pause does for the same answer.
func TestKill_AnAlreadyDeadSessionIsNotAFailure(t *testing.T) {
	root := claudeRoot(t)
	srv := &fakeTmuxServer{failKill: true}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())

	require.NoError(t, inst.Kill())

	assert.True(t, srv.ran("kill-session"), "precondition: the close failed")
	assert.NoDirExists(t, dir)
	zips := archivedZips(t, inst)
	require.Len(t, zips, 1)
	assert.Equal(t, "kill", manifestOf(t, zips[0]).Reason)
}

// TestKill_AnUnconfirmedSessionKeepsTheInstanceAndTheTempDir: the close
// failed and tmux never answered whether the session is gone, so the agent
// may still be writing to its temp dir: the kill fails, the instance is
// restored for a retry, and nothing is archived.
func TestKill_AnUnconfirmedSessionKeepsTheInstanceAndTheTempDir(t *testing.T) {
	t.Cleanup(tmux.SetLivenessProbeTimeoutForTest(20 * time.Millisecond))
	root := claudeRoot(t)
	srv := &fakeTmuxServer{failKill: true, probe: func() error {
		time.Sleep(60 * time.Millisecond) // outlive the probe deadline: Unknown
		return errors.New("signal: killed")
	}}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())

	err := inst.Kill()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to close tmux session")
	assert.True(t, inst.isStarted(), "the instance is restored for a retry")
	assert.DirExists(t, dir)
	assert.Empty(t, archivedZips(t, inst))
}

func TestKill_ArchivesClaudeTemp(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())

	require.NoError(t, inst.Kill())

	assert.NoDirExists(t, dir)
	zips := archivedZips(t, inst)
	require.Len(t, zips, 1)
	assert.Equal(t, "kill", manifestOf(t, zips[0]).Reason)
}

// TestKill_OfAPausedSessionKeepsItsParkedZip: the dir went at pause, so
// the parked zip simply stays as the session's archive.
func TestKill_OfAPausedSessionKeepsItsParkedZip(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
	require.NoError(t, inst.Pause(nil))
	parked := archivedZips(t, inst)
	require.Len(t, parked, 1)

	require.NoError(t, inst.Kill())

	assert.Equal(t, parked, archivedZips(t, inst))
}

// TestPause_DemotesAnEarlierParkedZip: a zip parked by an earlier pause
// whose restore failed becomes a permanent, timestamped archive.
func TestPause_DemotesAnEarlierParkedZip(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
	name := claudetmp.ArchiveName(filepath.Base(dir), claudetmp.WorktreePrefixes(inst.ConfigDir))
	adir := claudetmp.ArchiveDir(inst.ConfigDir)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(adir, name+".zip"), []byte("earlier"), 0o600))

	require.NoError(t, inst.Pause(nil))

	demoted, err := filepath.Glob(filepath.Join(adir, name+".*.zip"))
	require.NoError(t, err)
	require.Len(t, demoted, 1)
	data, err := os.ReadFile(demoted[0])
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(data))
	assert.Equal(t, "pause", manifestOf(t, filepath.Join(adir, name+".zip")).Reason)
}

func TestResume_RestoreFailureIsANoticeAndTheLaunchProceeds(t *testing.T) {
	claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	require.NoError(t, inst.Pause(nil))
	wt := inst.getGitWorktree().GetWorktreePath()
	name := claudetmp.ArchiveName(claudetmp.Names(wt)[0].Value, claudetmp.WorktreePrefixes(inst.ConfigDir))
	adir := claudetmp.ArchiveDir(inst.ConfigDir)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	zipPath := filepath.Join(adir, name+".zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))

	err := inst.Resume(nil)

	n, ok := OnlyNotice(err)
	require.True(t, ok, "a failed restore never fails the resume: %v", err)
	assert.Contains(t, n.Error(), "couldn't restore Claude's scratchpad")
	assert.Contains(t, n.Error(), zipPath)
	assert.Equal(t, Running, inst.GetStatus())
	assert.FileExists(t, zipPath, "the archive is kept")
}

func TestArchiveFailure_FailsNeitherPauseNorKill(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(*Instance) error
	}{
		{"pause", func(i *Instance) error { return i.Pause(nil) }},
		{"kill", (*Instance).Kill},
	} {
		t.Run(op.name, func(t *testing.T) {
			root := claudeRoot(t)
			inst := newTestPausableInstance(t)
			withConfigDir(inst)
			dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
			// A file where the archive dir must go makes every archive fail.
			require.NoError(t, os.MkdirAll(filepath.Join(inst.ConfigDir, "archive"), 0o700))
			require.NoError(t, os.WriteFile(claudetmp.ArchiveDir(inst.ConfigDir), []byte("in the way"), 0o600))

			require.NoError(t, op.run(inst))
			assert.DirExists(t, dir, "a failed archive leaves the temp dir where it was")
		})
	}
}

// TestKill_SkipsWorkspaceTerminals: they run in the repository root, which
// the user's own Claude sessions share.
func TestKill_SkipsWorkspaceTerminals(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestStartedInstance(t)
	inst.Path = t.TempDir()
	inst.ConfigDir = t.TempDir()
	dir := claudeTempDir(t, root, inst.Path)

	require.NoError(t, inst.Kill())

	assert.DirExists(t, dir)
}

// sweepWorkspace is a config dir with a worktrees/u dir, under parent.
func sweepWorkspace(t *testing.T, parent, repoName string) (cfg, wtDir string) {
	t.Helper()
	cfg = filepath.Join(parent, repoName, ".loom")
	wtDir = filepath.Join(cfg, "worktrees", "u")
	require.NoError(t, os.MkdirAll(wtDir, 0o755))
	return cfg, wtDir
}

// noQuietPeriod turns the sweep's quiet-period check off for this test:
// fixtures are written just now, so the check would keep every one of them.
func noQuietPeriod(t *testing.T) {
	t.Helper()
	prev := sweepQuietPeriod
	sweepQuietPeriod = 0
	t.Cleanup(func() { sweepQuietPeriod = prev })
}

func TestSweepClaudeTemp_ArchivesOnlyWhatNoSessionOwns(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")

	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	pausedWT := filepath.Join(wtDir, "paused_18be000000000002")
	paused := claudeTempDir(t, root, pausedWT)
	liveWT := filepath.Join(wtDir, "live_18be000000000003")
	require.NoError(t, os.Mkdir(liveWT, 0o755)) // started after the claim snapshot
	live := claudeTempDir(t, root, liveWT)
	backupWT := filepath.Join(cfg, "worktrees-backup", "u", "x_18be000000000004")
	require.NoError(t, os.MkdirAll(backupWT, 0o755))
	lookalike := claudeTempDir(t, root, backupWT)
	noStamp := claudeTempDir(t, root, filepath.Join(cfg, "worktrees-old"))
	otherCfg, otherWT := sweepWorkspace(t, t.TempDir(), "other")
	other := claudeTempDir(t, root, filepath.Join(otherWT, "gone_18be000000000005"))

	n := SweepClaudeTemp(cfg, map[string]bool{pausedWT: true}, []string{cfg, otherCfg})

	assert.Equal(t, 1, n)
	assert.NoDirExists(t, gone, "unclaimed, nothing on disk: archived")
	assert.DirExists(t, paused, "claimed")
	assert.DirExists(t, live, "its worktree is on disk")
	assert.DirExists(t, lookalike, "a look-alike dir that is on disk")
	assert.DirExists(t, noStamp, "no worktree timestamp")
	assert.DirExists(t, other, "another workspace's prefix")
	zips, err := filepath.Glob(filepath.Join(claudetmp.ArchiveDir(cfg), "*.zip"))
	require.NoError(t, err)
	require.Len(t, zips, 1)
	assert.Equal(t, "u-gone-18be000000000001.zip", filepath.Base(zips[0]))
	assert.Equal(t, "sweep", manifestOf(t, zips[0]).Reason)
}

// TestSweepClaudeTemp_BothPathForms: loom stores paths through a symlink,
// Claude names dirs after the physical path.
func TestSweepClaudeTemp_BothPathForms(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	cfg, wtDir := sweepWorkspace(t, link, "repo") // stored through the link

	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	claimedWT := filepath.Join(wtDir, "kept_18be000000000002")
	kept := claudeTempDir(t, root, claimedWT)
	require.True(t, strings.HasPrefix(filepath.Base(gone), claudetmp.WorktreePrefixes(cfg)[0]), "named by the physical path")

	n := SweepClaudeTemp(cfg, map[string]bool{claimedWT: true}, nil)

	assert.Equal(t, 1, n)
	assert.NoDirExists(t, gone)
	assert.DirExists(t, kept, "claimed by its stored path")
}

// TestSweepClaudeTemp_ACollidingWorkspaceStopsTheSweep: foo_bar and
// foo-bar encode alike, so a name could be either workspace's.
func TestSweepClaudeTemp_ACollidingWorkspaceStopsTheSweep(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	parent := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, parent, "my_proj")
	twin, _ := sweepWorkspace(t, parent, "my-proj")
	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))

	assert.Zero(t, SweepClaudeTemp(cfg, nil, []string{cfg, twin}))
	assert.DirExists(t, gone)

	assert.Equal(t, 1, SweepClaudeTemp(cfg, nil, []string{cfg}), "its own entry in the list is skipped")
	assert.NoDirExists(t, gone)
}

// TestSweepClaudeTemp_NeverTakesATruncatedName: past 200 characters the
// name ends in Claude's hash, which no check can compare. The config dir is
// short and the worktree path long, so the name starts with the config dir's
// prefix, looks like a timestamp at its end, and matches nothing on disk or
// claimed: only the length guard stands between it and an archive.
func TestSweepClaudeTemp_NeverTakesATruncatedName(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	require.Less(t, len(claudetmp.WorktreePrefixes(cfg)[0]), claudetmp.MaxDirName/2)
	prefix, truncated := claudetmp.DirName(filepath.Join(wtDir, strings.Repeat("d", 180), "gone_18be000000000001"))
	require.True(t, truncated)
	dir := filepath.Join(root, prefix+"18be000000000001") // a hash that even looks like a timestamp
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.Greater(t, len(filepath.Base(dir)), claudetmp.MaxDirName)

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, dir)
}

func TestSweepClaudeTemp_NoRootIsANoOp(t *testing.T) {
	t.Setenv(claudetmp.EnvTmpDir, t.TempDir()) // no claude-<uid> inside
	cfg, _ := sweepWorkspace(t, t.TempDir(), "repo")

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.NoDirExists(t, claudetmp.ArchiveDir(cfg))
}

// TestSweepClaudeTemp_AFailedDirIsNotRetriedInThisProcess: a dir that can
// never be archived (an unreadable subdir, say) costs one warning per loom
// run, not one per workspace load.
func TestSweepClaudeTemp_AFailedDirIsNotRetriedInThisProcess(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	// A file where the archive dir must go makes the archive fail.
	blocker := claudetmp.ArchiveDir(cfg)
	require.NoError(t, os.MkdirAll(filepath.Dir(blocker), 0o700))
	require.NoError(t, os.WriteFile(blocker, []byte("in the way"), 0o600))

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, gone, "the archive failed")

	require.NoError(t, os.Remove(blocker)) // a retry would succeed now
	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil), "the second sweep skips the dir")
	assert.DirExists(t, gone)
	assert.NoDirExists(t, claudetmp.ArchiveDir(cfg), "no second attempt was made")

	sweepFailedDirs.Delete(gone) // a restart forgets what failed
	assert.Equal(t, 1, SweepClaudeTemp(cfg, nil, nil))
	assert.NoDirExists(t, gone)
}

// unreadable makes dir mode perm for the test and restores it before the
// test's temp dirs are removed. Skipped as root, which reads anything.
func unreadable(t *testing.T, dir string, perm os.FileMode) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	require.NoError(t, os.Chmod(dir, perm))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// TestSweepClaudeTemp_FailsClosedOnAnUnreadableWorktreeDir: a prefix dir the
// sweep cannot list might hold the candidate's worktree, so the candidate
// stays.
func TestSweepClaudeTemp_FailsClosedOnAnUnreadableWorktreeDir(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	unreadable(t, wtDir, 0o000)

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, gone)
}

// TestSweepClaudeTemp_FailsClosedOnAnUnreadableConfigDir: the config dir
// can be searched and written, so an archive would succeed, but it cannot be
// listed to tell whether a worktree is on disk.
func TestSweepClaudeTemp_FailsClosedOnAnUnreadableConfigDir(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	unreadable(t, cfg, 0o300)

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, gone)
}

// TestSweepClaudeTemp_AWorktreeBeyondTheDepthLimitIsStillOnDisk: a branch
// prefix with many slashes nests a worktree deeper than the walk descends.
// Where it stops short of a directory that might hold one, check 4 says
// "on disk".
func TestSweepClaudeTemp_AWorktreeBeyondTheDepthLimitIsStillOnDisk(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, _ := sweepWorkspace(t, t.TempDir(), "repo")
	parts := []string{cfg, "worktrees"}
	for i := 0; i <= maxOrphanScanDepth; i++ {
		parts = append(parts, fmt.Sprintf("p%d", i))
	}
	deepWT := filepath.Join(append(parts, "live_18be000000000001")...)
	require.NoError(t, os.MkdirAll(deepWT, 0o755))
	live := claudeTempDir(t, root, deepWT)

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, live)
}

// backdate sets the mtime of dir and everything under it, except skip, to
// 48 hours ago.
func backdate(t *testing.T, dir, skip string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil || p == skip {
			return err
		}
		return os.Chtimes(p, old, old)
	}))
}

// TestSweepClaudeTemp_LeavesRecentlyActiveDirsAlone: defence in depth for
// a live session no other check can see. A dir (or an entry within its top
// three levels) touched inside the quiet period is kept; once everything is
// older, it is archived.
func TestSweepClaudeTemp_LeavesRecentlyActiveDirsAlone(t *testing.T) {
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	dir := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	note := filepath.Join(dir, "sess-1", "scratchpad", "notes.md")
	require.Equal(t, 24*time.Hour, sweepQuietPeriod, "the default period")

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, dir, "written just now")

	backdate(t, dir, note)
	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, dir, "one fresh file, three levels down")

	backdate(t, dir, "")
	assert.Equal(t, 1, SweepClaudeTemp(cfg, nil, nil))
	assert.NoDirExists(t, dir)
}

func TestForeignWorktreePrefixes_SkipsEmptyAndOwnEntries(t *testing.T) {
	cfg, _ := sweepWorkspace(t, t.TempDir(), "repo")
	other, _ := sweepWorkspace(t, t.TempDir(), "other")

	assert.Empty(t, foreignWorktreePrefixes(cfg, []string{"", cfg}))
	assert.Equal(t, claudetmp.WorktreePrefixes(other), foreignWorktreePrefixes(cfg, []string{"", cfg, other}))
}

// TestSweepClaudeTemp_ReapsOldTombstones: Archive deletes through a
// ".loom-trash-" tombstone, and one a stop or crash interrupted is
// otherwise never retried. The sweep clears the old ones, even when it has
// nothing to archive, and leaves a fresh one that may still be deleting.
func TestSweepClaudeTemp_ReapsOldTombstones(t *testing.T) {
	root := claudeRoot(t)
	cfg, _ := sweepWorkspace(t, t.TempDir(), "repo")
	oldTrash := filepath.Join(root, ".loom-trash-1-abc")
	require.NoError(t, os.MkdirAll(filepath.Join(oldTrash, "sess-1", "scratchpad"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(oldTrash, "sess-1", "scratchpad", "notes.md"), []byte("archived"), 0o600))
	backdate(t, oldTrash, "")
	freshTrash := filepath.Join(root, ".loom-trash-2-def")
	require.NoError(t, os.Mkdir(freshTrash, 0o700))

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil), "nothing to archive")

	assert.NoDirExists(t, oldTrash, "an old tombstone is reaped")
	assert.DirExists(t, freshTrash, "a fresh one may still be being deleted")
}

// TestRecentlyActive_FailsClosedOnAnUnreadableEntry: every mtime in the tree
// is old, so only an entry the check cannot examine can make it report
// activity. What it cannot list or stat might be what a live session is
// writing, so it counts as recent.
func TestRecentlyActive_FailsClosedOnAnUnreadableEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string // under the temp dir
		mode os.FileMode
	}{
		{"a session dir it cannot list", "sess-1", 0o000},
		{"a scratchpad dir it cannot list", filepath.Join("sess-1", "scratchpad"), 0o000},
		{"entries it can list but not stat", "sess-1", 0o400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := claudeRoot(t)
			_, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
			dir := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
			backdate(t, dir, "")
			require.False(t, recentlyActive(dir, sweepQuietPeriod), "precondition: every entry is old")

			unreadable(t, filepath.Join(dir, tc.path), tc.mode)

			assert.True(t, recentlyActive(dir, sweepQuietPeriod))
		})
	}
}

// TestSweepClaudeTemp_ReapsOldPartialArchives: Archive writes a zip as
// "<name>.zip.<random>.partial" and renames it into place when complete, so
// one a crash left is never finished. The sweep clears the old ones from the
// archive dir, even when it has nothing to archive, and leaves a fresh one
// that another archive may still be writing, and every real archive.
func TestSweepClaudeTemp_ReapsOldPartialArchives(t *testing.T) {
	claudeRoot(t)
	cfg, _ := sweepWorkspace(t, t.TempDir(), "repo")
	adir := claudetmp.ArchiveDir(cfg)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	write := func(name string, age time.Duration) string {
		p := filepath.Join(adir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		when := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(p, when, when))
		return p
	}
	oldPartial := write("u-gone-18be000000000001.zip.123456.partial", 48*time.Hour)
	freshPartial := write("u-gone-18be000000000002.zip.654321.partial", 0)
	oldArchive := write("u-gone-18be000000000003.zip", 48*time.Hour)

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil), "nothing to archive")

	assert.NoFileExists(t, oldPartial, "an old partial is reaped")
	assert.FileExists(t, freshPartial, "a fresh one may still be being written")
	assert.FileExists(t, oldArchive, "a finished archive is never touched, however old")
}

// pausableOnFakeTmux is a pausable instance whose tmux and recovery
// launches go to a fake server, with Claude's temp dir in place.
func pausableOnFakeTmux(t *testing.T) (inst *Instance, srv *fakeTmuxServer, dir string) {
	t.Helper()
	root := claudeRoot(t)
	srv = &fakeTmuxServer{}
	inst = newTestPausableInstanceWithExec(t, srv.runner())
	inst.program = "claude"
	orig := newRecoverySession
	newRecoverySession = func(name, program string, env ...string) *tmux.Session {
		return tmux.NewSessionWithDeps(name, program, srv, srv.runner(), env...)
	}
	t.Cleanup(func() { newRecoverySession = orig })
	withConfigDir(inst)
	dir = claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
	return inst, srv, dir
}

// twinOf is a second *Instance for inst's session, with its own worktree
// object on the same path: what a tab closed and reopened mid-pause yields
// once reconcile has rebuilt the record.
func twinOf(inst *Instance, srv *fakeTmuxServer, status Status) *Instance {
	return twinAt(inst, srv, status, inst.getGitWorktree().GetWorktreePath())
}

// twinAt is twinOf with the worktree reached through worktreePath, which may
// be another spelling of the same directory.
func twinAt(inst *Instance, srv *fakeTmuxServer, status Status, worktreePath string) *Instance {
	gw := inst.getGitWorktree()
	twin := &Instance{Title: inst.Title, Status: status, ConfigDir: inst.ConfigDir}
	twin.program = "claude"
	twin.setGitWorktree(git.NewGitWorktreeFromStorage(gw.GetRepoPath(), worktreePath,
		inst.Title, gw.GetBranchName(), gw.GetBaseCommitSHA(), true, inst.ConfigDir))
	twin.setTmuxSession(tmux.NewSessionWithDeps(inst.Title, "claude", srv, srv.runner()))
	twin.setStarted(true)
	return twin
}

// duringArchive runs during in Pause's own goroutine at the moment it
// archives, so whatever it calls overlaps that Pause deterministically.
func duringArchive(t *testing.T, during func()) {
	t.Helper()
	orig := archiveClaudeTempFn
	archiveClaudeTempFn = func(configDir, worktreePath, reason string) {
		during()
		orig(configDir, worktreePath, reason)
	}
	t.Cleanup(func() { archiveClaudeTempFn = orig })
}

// TestResume_RefusesWhileAPauseOfTheSameWorktreeIsInFlight: a Resume that
// began while Pause was still archiving would find the temp dir in place,
// skip the restore, launch Claude in it, and have the archive delete what
// the new session writes. Lua inst:resume() has no status gate, and a
// reopened tab yields a twin *Instance for the same worktree.
func TestResume_RefusesWhileAPauseOfTheSameWorktreeIsInFlight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		twin    bool
		symlink bool // the twin reaches the worktree through a symlink
	}{
		{"the same instance", false, false},
		{"a twin of it", true, false},
		{"a twin through a symlinked path", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst, srv, dir := pausableOnFakeTmux(t)
			resumer := inst
			if tc.twin {
				wt := inst.getGitWorktree().GetWorktreePath()
				if tc.symlink {
					base := filepath.Dir(inst.ConfigDir)
					alias := filepath.Join(t.TempDir(), "alias")
					require.NoError(t, os.Symlink(base, alias))
					rel, err := filepath.Rel(base, wt)
					require.NoError(t, err)
					wt = filepath.Join(alias, rel)
				}
				resumer = twinAt(inst, srv, Paused, wt)
			}
			var duringErr error
			var launchesDuring int
			duringArchive(t, func() {
				duringErr = resumer.Resume(nil)
				launchesDuring = len(srv.launchArgs())
			})

			require.NoError(t, inst.Pause(nil))

			require.Error(t, duringErr)
			assert.Contains(t, duringErr.Error(), "still pausing")
			assert.Zero(t, launchesDuring, "the refused resume launched nothing")
			assert.NoDirExists(t, dir, "the archive took the dir, undisturbed")

			require.NoError(t, resumer.Resume(nil), "once the pause is over, a resume goes through")
			assert.FileExists(t, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"), "and restores the scratchpad")
			assert.Len(t, srv.launchArgs(), 1)
		})
	}
}

// TestPause_RefusesASecondPauseOfTheSameWorktree: two pauses of one session
// would stash, close and archive the same tree twice.
func TestPause_RefusesASecondPauseOfTheSameWorktree(t *testing.T) {
	for _, tc := range []struct {
		name string
		twin bool
	}{{"the same instance", false}, {"a twin of it", true}} {
		t.Run(tc.name, func(t *testing.T) {
			inst, srv, _ := pausableOnFakeTmux(t)
			second := inst
			if tc.twin {
				second = twinOf(inst, srv, Running)
			}
			var secondErr error
			duringArchive(t, func() { secondErr = second.Pause(nil) })

			require.NoError(t, inst.Pause(nil))

			require.Error(t, secondErr)
			assert.Contains(t, secondErr.Error(), "already running")
			assert.Len(t, archivedZips(t, inst), 1, "one archive")
		})
	}
}

// duringLaunch runs during, once, in Resume's own goroutine at the moment it
// launches the agent, so whatever it calls overlaps that Resume
// deterministically. (Once: a call that is not refused would launch again.)
func duringLaunch(t *testing.T, during func()) {
	t.Helper()
	orig := newRecoverySession
	called := false
	newRecoverySession = func(name, program string, env ...string) *tmux.Session {
		if !called {
			called = true
			during()
		}
		return orig(name, program, env...)
	}
	t.Cleanup(func() { newRecoverySession = orig })
}

// TestPause_RefusesWhileAResumeOfTheSameWorktreeIsInFlight: a Pause started
// meanwhile (through Lua, or a twin *Instance that still reads Running)
// would stash, close and delete the tree the resume is launching in.
func TestPause_RefusesWhileAResumeOfTheSameWorktreeIsInFlight(t *testing.T) {
	inst, srv, dir := pausableOnFakeTmux(t)
	require.NoError(t, inst.Pause(nil))
	twin := twinOf(inst, srv, Running)
	var pauseErr error
	duringLaunch(t, func() { pauseErr = twin.Pause(nil) })

	require.NoError(t, inst.Resume(nil))

	require.Error(t, pauseErr)
	assert.Contains(t, pauseErr.Error(), "resume of this session is still running")
	assert.Equal(t, Running, inst.GetStatus())
	assert.FileExists(t, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"), "the restored scratchpad is undisturbed")
	require.NoError(t, inst.Pause(nil), "a finished resume leaves nothing in flight")
}

// TestResume_RefusesASecondResumeOfTheSameWorktree: two resumes would
// restore and launch the same session twice.
func TestResume_RefusesASecondResumeOfTheSameWorktree(t *testing.T) {
	inst, srv, _ := pausableOnFakeTmux(t)
	require.NoError(t, inst.Pause(nil))
	twin := twinOf(inst, srv, Paused)
	var secondErr error
	duringLaunch(t, func() { secondErr = twin.Resume(nil) })

	require.NoError(t, inst.Resume(nil))

	require.Error(t, secondErr)
	assert.Contains(t, secondErr.Error(), "already running")
	assert.Len(t, srv.launchArgs(), 1, "one launch")
}

// TestPause_RefusesALockedTreeBeforeItStashesOrKills: a lock the user put on
// the tree would refuse the removal at the end of Pause, by which time the
// agent's session was closed and its changes stashed. Checked first, a
// refused pause leaves the session running and the work where it was.
func TestPause_RefusesALockedTreeBeforeItStashesOrKills(t *testing.T) {
	srv := &fakeTmuxServer{}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	gw := inst.getGitWorktree()
	wt, repo := gw.GetWorktreePath(), gw.GetRepoPath()
	wip := filepath.Join(wt, "wip.txt")
	require.NoError(t, os.WriteFile(wip, []byte("uncommitted"), 0o644))
	gitIn(t, repo, "worktree", "lock", "--reason", "mine", wt)

	err := inst.Pause(nil)

	require.ErrorIs(t, err, git.ErrWorktreeLocked)
	assert.Contains(t, err.Error(), "worktree unlock", "the error names the remedy")
	assert.False(t, srv.ran("kill-session"), "the agent's session was left alone")
	assert.Empty(t, gw.GetStashRef(), "nothing was stashed")
	assert.Empty(t, gitIn(t, repo, "stash", "list"))
	assert.FileExists(t, wip, "the changes are still in the tree")
	assert.Equal(t, Running, inst.GetStatus())

	gitIn(t, repo, "worktree", "unlock", wt)
	require.NoError(t, inst.Pause(nil), "the refusal left no pause in flight")
	assert.Equal(t, Paused, inst.GetStatus())
}

// TestKill_RefusesALockedTreeBeforeItClosesAnything: Cleanup refuses a tree
// the user locked, but by then Kill has closed the agent's tmux session and
// dropped the pending stash. Checked first, a refused kill leaves the agent
// running, the stash listed, and the instance as it was, ready for a retry
// once the lock is gone.
func TestKill_RefusesALockedTreeBeforeItClosesAnything(t *testing.T) {
	root := claudeRoot(t)
	srv := &fakeTmuxServer{}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	withConfigDir(inst)
	gw, ts := inst.getGitWorktree(), inst.getTmuxSession()
	wt, repo := gw.GetWorktreePath(), gw.GetRepoPath()
	dir := claudeTempDir(t, root, wt)
	// A paused instance's pending stash, which a kill drops.
	require.NoError(t, os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("uncommitted"), 0o644))
	sha, err := gw.StashChanges("[loom] test stash")
	require.NoError(t, err)
	require.NotEmpty(t, sha)
	gw.SetStashRef(sha)
	gitIn(t, repo, "worktree", "lock", "--reason", "mine", wt)

	err = inst.Kill()

	require.ErrorIs(t, err, git.ErrWorktreeLocked)
	assert.Contains(t, err.Error(), "worktree unlock", "the error names the remedy")
	assert.False(t, srv.ran("kill-session"), "the agent's session was left alone")
	assert.True(t, inst.isStarted(), "the instance is as it was")
	assert.Same(t, ts, inst.getTmuxSession())
	assert.Same(t, gw, inst.getGitWorktree())
	assert.Equal(t, sha, gw.GetStashRef(), "the pending stash was not dropped")
	assert.NotEmpty(t, gitIn(t, repo, "stash", "list"))
	assert.DirExists(t, wt)
	assert.DirExists(t, dir, "nothing was archived")
	assert.Empty(t, archivedZips(t, inst))

	gitIn(t, repo, "worktree", "unlock", wt)
	require.NoError(t, inst.Kill(), "once the lock is gone, the kill goes through")
	assert.NoDirExists(t, wt)
	assert.NoDirExists(t, dir)
	assert.Len(t, archivedZips(t, inst), 1)
}

// TestSweepClaudeTemp_ACwdInsideALiveWorktreeIsOnDisk: a Claude session
// started in a subdirectory of a live worktree has a temp dir named after
// that subdirectory, which no directory on disk up to the worktree encodes
// to exactly. It still belongs to the live tree.
func TestSweepClaudeTemp_ACwdInsideALiveWorktreeIsOnDisk(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	liveWT := filepath.Join(wtDir, "live_18be000000000001")
	require.NoError(t, os.Mkdir(liveWT, 0o755))
	nested := claudeTempDir(t, root, filepath.Join(liveWT, "sub", "x_18be000000000009"))
	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000005"))
	// Its name starts with the live worktree's, but carries on without the
	// "-" that separates a subdirectory: a different worktree, long gone.
	lookalike := claudeTempDir(t, root, filepath.Join(wtDir, "live_18be000000000001y_18be000000000006"))

	assert.Equal(t, 2, SweepClaudeTemp(cfg, nil, nil))

	assert.DirExists(t, nested, "inside a live worktree")
	assert.NoDirExists(t, gone, "a sibling that is gone is still archived")
	assert.NoDirExists(t, lookalike, "a name that merely begins like the live one is not inside it")
}
