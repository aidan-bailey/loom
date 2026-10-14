package session

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatID is a well-formed Claude session ID: BuildResumeCommand resumes
// nothing else.
const chatID = "8c634184-0fe5-4b62-b437-8f364eeeefcc"

// chatProjects points Claude's main config dir at a fresh one for this test
// and returns its projects dir.
func chatProjects(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	projects := filepath.Join(cfg, "projects")
	require.NoError(t, os.Mkdir(projects, 0o700))
	return projects
}

// chatFor writes the project dir Claude keeps for a session whose cwd is
// wt, as a finished session leaves it (a transcript and a subagent's, all
// old), and returns the transcript's path.
func chatFor(t *testing.T, projects, wt string) string {
	t.Helper()
	dir := filepath.Join(projects, claudetmp.Names(wt)[0].Value)
	sub := filepath.Join(dir, chatID, "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-1.jsonl"), []byte("sub\n"), 0o600))
	transcript := filepath.Join(dir, chatID+".jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte("turn 1\n"), 0o600))
	backdate(t, dir, "")
	return transcript
}

// chatZips lists the chat archives in inst's own config folder.
func chatZips(t *testing.T, inst *Instance) []string {
	t.Helper()
	return zipsIn(t, claudetmp.ChatArchiveDir(inst.ConfigDir))
}

// zipNames lists zipPath's entry names.
func zipNames(t *testing.T, zipPath string) []string {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

// withChat gives inst, a pausable fixture, a chat under projects and the
// transcript path its hooks would have reported. It returns that path.
func withChat(t *testing.T, inst *Instance, projects string) string {
	t.Helper()
	withConfigDir(inst)
	transcript := chatFor(t, projects, inst.getGitWorktree().GetWorktreePath())
	inst.claude = claudeState{sessionID: chatID, transcriptPath: transcript}
	return transcript
}

func TestPause_SnapshotsTheChatAndLeavesClaudesCopy(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	transcript := withChat(t, inst, projects)
	wt := inst.getGitWorktree().GetWorktreePath()

	require.NoError(t, inst.Pause(nil))

	assert.FileExists(t, transcript, "loom never moves Claude's files")
	zips := chatZips(t, inst)
	require.Len(t, zips, 1)
	m := manifestOf(t, zips[0])
	assert.Equal(t, "pause", m.Reason)
	assert.Equal(t, wt, m.Worktree)
	assert.Subset(t, zipNames(t, zips[0]), []string{chatID + ".jsonl", chatID + "/subagents/agent-1.jsonl"})
}

func TestKill_SnapshotsTheChat(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	transcript := withChat(t, inst, projects)

	require.NoError(t, inst.Kill())

	assert.FileExists(t, transcript)
	zips := chatZips(t, inst)
	require.Len(t, zips, 1)
	assert.Equal(t, "kill", manifestOf(t, zips[0]).Reason)
}

// TestKill_FindsTheChatWithoutATranscriptPath: no hook reported one (an
// untracked launch, or a session older than the hooks), so the dir is
// found by the worktree's encoding under the main config dir.
func TestKill_FindsTheChatWithoutATranscriptPath(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	chatFor(t, projects, inst.getGitWorktree().GetWorktreePath())

	require.NoError(t, inst.Kill())

	assert.Len(t, chatZips(t, inst), 1)
}

// TestKill_OfAPausedSessionWritesNoNewSnapshot: nothing changed since the
// pause's snapshot, so the kill costs a stat walk and no zip.
func TestKill_OfAPausedSessionWritesNoNewSnapshot(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	withChat(t, inst, projects)
	require.NoError(t, inst.Pause(nil))
	zips := chatZips(t, inst)
	require.Len(t, zips, 1)
	before, err := os.Stat(zips[0])
	require.NoError(t, err)

	require.NoError(t, inst.Kill())

	after, err := os.Stat(zips[0])
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
	assert.Equal(t, "pause", manifestOf(t, zips[0]).Reason)
}

// TestKill_SnapshotsNoWorkspaceTerminalChat: a workspace terminal runs in
// the repository root, whose chats the user's own sessions share.
func TestKill_SnapshotsNoWorkspaceTerminalChat(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestStartedInstance(t)
	inst.Path = t.TempDir()
	inst.ConfigDir = t.TempDir()
	transcript := chatFor(t, projects, inst.Path)
	inst.claude = claudeState{sessionID: chatID, transcriptPath: transcript}

	require.NoError(t, inst.Kill())

	assert.Empty(t, chatZips(t, inst))
}

func TestChatArchiveFailure_FailsNeitherPauseNorKill(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(*Instance) error
	}{
		{"pause", func(i *Instance) error { return i.Pause(nil) }},
		{"kill", (*Instance).Kill},
	} {
		t.Run(op.name, func(t *testing.T) {
			projects := chatProjects(t)
			inst := newTestPausableInstance(t)
			transcript := withChat(t, inst, projects)
			// A file where the chat archive dir must go makes every snapshot fail.
			require.NoError(t, os.MkdirAll(filepath.Join(inst.ConfigDir, "archive"), 0o700))
			require.NoError(t, os.WriteFile(claudetmp.ChatArchiveDir(inst.ConfigDir), []byte("in the way"), 0o600))

			require.NoError(t, op.run(inst))
			assert.FileExists(t, transcript)
		})
	}
}

// TestResume_RestoresADeletedTranscriptAndResumesIt: Claude deleted the
// paused session's transcript (a month without a write). Resume puts it
// back before the launch command is built, so the relaunch resumes the
// conversation instead of starting over with --continue.
func TestResume_RestoresADeletedTranscriptAndResumesIt(t *testing.T) {
	t.Run("rebuild", func(t *testing.T) {
		projects := chatProjects(t)
		srv := &fakeTmuxServer{}
		inst := newTestPausableInstanceWithExec(t, srv.runner())
		inst.program = "claude"
		orig := newRecoverySession
		newRecoverySession = func(name, program string, env ...string) *tmux.Session {
			return tmux.NewSessionWithDeps(name, program, srv, srv.runner(), env...)
		}
		t.Cleanup(func() { newRecoverySession = orig })
		transcript := withChat(t, inst, projects)
		require.NoError(t, inst.Pause(nil))
		require.NoError(t, os.Remove(transcript)) // Claude's cleanup

		require.NoError(t, inst.Resume(nil))

		data, err := os.ReadFile(transcript)
		require.NoError(t, err)
		assert.Equal(t, "turn 1\n", string(data))
		require.Len(t, srv.launchArgs(), 1)
		assert.Contains(t, strings.Join(srv.launchArgs()[0], " "), "--resume "+chatID)
	})
	t.Run("relaunch in place", func(t *testing.T) {
		projects := chatProjects(t)
		inst, srv := newTickPausedInstance(t)
		transcript := withChat(t, inst, projects)
		inst.archiveChat(inst.getGitWorktree().GetWorktreePath(), "pause")
		require.NoError(t, os.Remove(transcript))

		require.NoError(t, inst.Resume(nil))

		assert.FileExists(t, transcript)
		require.Len(t, srv.launchArgs(), 1)
		assert.Contains(t, strings.Join(srv.launchArgs()[0], " "), "--resume "+chatID)
	})
}

// TestResume_WithNoChatArchiveStillContinues: the other side of the
// restore. No zip, no transcript: --continue, as before this feature.
func TestResume_WithNoChatArchiveStillContinues(t *testing.T) {
	projects := chatProjects(t)
	inst, srv := newTickPausedInstance(t)
	transcript := withChat(t, inst, projects)
	require.NoError(t, os.Remove(transcript))

	require.NoError(t, inst.Resume(nil))

	assert.NoFileExists(t, transcript)
	require.Len(t, srv.launchArgs(), 1)
	launch := strings.Join(srv.launchArgs()[0], " ")
	assert.NotContains(t, launch, "--resume")
	assert.Contains(t, launch, "--continue")
}

func TestResume_AChatRestoreFailureIsANoticeAndTheLaunchProceeds(t *testing.T) {
	projects := chatProjects(t)
	inst, srv := newTickPausedInstance(t)
	transcript := withChat(t, inst, projects)
	require.NoError(t, os.Remove(transcript))
	name := claudetmp.ArchiveName(filepath.Base(filepath.Dir(transcript)), claudetmp.WorktreePrefixes(inst.ConfigDir))
	adir := claudetmp.ChatArchiveDir(inst.ConfigDir)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	zipPath := filepath.Join(adir, name+".zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))

	err := inst.Resume(nil)

	n, ok := OnlyNotice(err)
	require.True(t, ok, "a failed restore never fails the resume: %v", err)
	assert.Contains(t, n.Error(), "couldn't restore the conversation")
	assert.Contains(t, n.Error(), zipPath)
	assert.FileExists(t, zipPath, "the archive is kept")
	require.Len(t, srv.launchArgs(), 1, "the launch went ahead")
}

func TestClaudeChatArchiveDir(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, t.TempDir()) // no config.json: unset
	cfg := filepath.Join(t.TempDir(), "repo", ".loom")
	assert.Equal(t, claudetmp.ChatArchiveDir(cfg), ClaudeChatArchiveDir(cfg))

	setGlobalArchiveDir(t, t.TempDir())
	assert.Equal(t, filepath.Join(ClaudeTmpArchiveDir(cfg), "chats"), ClaudeChatArchiveDir(cfg),
		"beside the workspace's temp-dir archives")
}

func TestPause_SnapshotsTheChatIntoTheConfiguredDir(t *testing.T) {
	setGlobalArchiveDir(t, t.TempDir())
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	withChat(t, inst, projects)

	require.NoError(t, inst.Pause(nil))

	assert.Len(t, zipsIn(t, ClaudeChatArchiveDir(inst.ConfigDir)), 1)
	assert.Empty(t, chatZips(t, inst), "not in the workspace's own folder")
}

// TestResume_FindsAChatArchivedBeforeTheSettingChanged: a session paused
// under the default location and resumed after claude_tmp_archive_dir was
// set still gets its transcript back.
func TestResume_FindsAChatArchivedBeforeTheSettingChanged(t *testing.T) {
	projects := chatProjects(t)
	inst, _ := newTickPausedInstance(t)
	transcript := withChat(t, inst, projects)
	inst.archiveChat(inst.getGitWorktree().GetWorktreePath(), "pause")
	require.Len(t, chatZips(t, inst), 1)
	setGlobalArchiveDir(t, t.TempDir())
	require.NoError(t, os.Remove(transcript))

	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, transcript)
}

func TestClaudeProjectsRoots(t *testing.T) {
	mainDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(mainDir, "projects"), 0o700))
	linked := t.TempDir() // projects/ is a link into the main dir, as account.Sync makes it
	require.NoError(t, os.Symlink(filepath.Join(mainDir, "projects"), filepath.Join(linked, "projects")))
	own := t.TempDir() // projects/ is a real dir of its own
	require.NoError(t, os.Mkdir(filepath.Join(own, "projects"), 0o700))
	none := t.TempDir() // Claude never ran here

	roots := ClaudeProjectsRoots(mainDir, map[string]string{"linked": linked, "own": own, "none": none})

	mainP, err := filepath.EvalSymlinks(filepath.Join(mainDir, "projects"))
	require.NoError(t, err)
	ownP, err := filepath.EvalSymlinks(filepath.Join(own, "projects"))
	require.NoError(t, err)
	assert.Equal(t, []string{mainP, ownP}, roots)
	assert.Empty(t, ClaudeProjectsRoots("", nil))
}

func TestSweepClaudeChats_SnapshotsDeadAndPausedChatsOnly(t *testing.T) {
	projects := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	goneWT := filepath.Join(wtDir, "gone_18be000000000001")
	pausedWT := filepath.Join(wtDir, "paused_18be000000000002")
	liveWT := filepath.Join(wtDir, "live_18be000000000003")
	require.NoError(t, os.Mkdir(liveWT, 0o755))
	noStamp := filepath.Join(cfg, "worktrees-old")
	otherCfg, otherWT := sweepWorkspace(t, t.TempDir(), "other")
	otherGone := filepath.Join(otherWT, "gone_18be000000000005")
	for _, wt := range []string{goneWT, pausedWT, liveWT, noStamp, otherGone} {
		chatFor(t, projects, wt)
	}
	running := map[string]bool{liveWT: true}
	others := []string{cfg, otherCfg}

	n := SweepClaudeChats(cfg, []string{projects}, running, others)

	assert.Equal(t, 2, n)
	var names []string
	for _, z := range zipsIn(t, claudetmp.ChatArchiveDir(cfg)) {
		names = append(names, filepath.Base(z))
		assert.Equal(t, "sweep", manifestOf(t, z).Reason)
	}
	assert.ElementsMatch(t, []string{"u-gone-18be000000000001.zip", "u-paused-18be000000000002.zip"}, names)
	for _, wt := range []string{goneWT, pausedWT} {
		assert.DirExists(t, filepath.Join(projects, claudetmp.Names(wt)[0].Value), "the sweep copies, never moves")
	}
	assert.Empty(t, zipsIn(t, claudetmp.ChatArchiveDir(otherCfg)), "another workspace's chats are its own sweep's")
	assert.Zero(t, SweepClaudeChats(cfg, []string{projects}, running, others), "an unchanged chat is not written again")
}

// TestSweepClaudeChats_CarriesForwardAfterClaudesCleanup: the deletion is
// a change, and the next snapshot keeps the deleted transcript.
func TestSweepClaudeChats_CarriesForwardAfterClaudesCleanup(t *testing.T) {
	projects := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	transcript := chatFor(t, projects, filepath.Join(wtDir, "gone_18be000000000001"))
	require.Equal(t, 1, SweepClaudeChats(cfg, []string{projects}, nil, []string{cfg}))
	require.NoError(t, os.Remove(transcript))

	assert.Equal(t, 1, SweepClaudeChats(cfg, []string{projects}, nil, []string{cfg}))

	zips := zipsIn(t, claudetmp.ChatArchiveDir(cfg))
	require.Len(t, zips, 1)
	assert.Contains(t, zipNames(t, zips[0]), chatID+".jsonl", "the transcript Claude deleted stays archived")
}

func TestSweepClaudeChats_NeverTakesATruncatedName(t *testing.T) {
	projects := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	prefix, truncated := claudetmp.DirName(filepath.Join(wtDir, strings.Repeat("d", 180), "gone_18be000000000001"))
	require.True(t, truncated)
	dir := filepath.Join(projects, prefix+"18be000000000001") // a hash that even looks like a timestamp
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, chatID+".jsonl"), []byte("x"), 0o600))

	assert.Zero(t, SweepClaudeChats(cfg, []string{projects}, nil, nil))
}
