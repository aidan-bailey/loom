# Pause Keeps the Worktree Implementation Plan

> **For agentic workers:** Execute inline with superpowers:executing-plans (the user's choice). Steps use checkbox (`- [ ]`) syntax for tracking. Four work packages, one commit each, then a final verification.

**Goal:** Pause stops the agent and keeps the worktree, so gitignored files (a `/draft-plan` run's `.superpowers/`, `.env`, build caches) survive a pause and resume.

**Architecture:** `session.Instance.Pause` loses its stash, lock check, `git worktree remove`/prune and clipboard copy, and gains a checkpoint save before it closes the agent's tmux session. Resume is unchanged: `decideResume` already relaunches an intact tree under a dead session in place. Orphan discovery compares claimed paths canonically, so a kept tree spelled through a symlink is never auto-removed. The e2e pause-in-flight test holds the pause through a link-time variable, because Pause runs no git step a hook could hold.

**Tech Stack:** Go, testify, git worktrees, tmux (faked in unit tests; a private server in e2e).

**Spec:** `docs/superpowers/specs/2026-10-22-pause-keeps-worktree-design.md`

**Ground rules (from the repo's CLAUDE.md files):**
- Scratch files go in the session scratchpad, never the worktree. Commit with explicit `-- <paths>`, never `-a` or `git add -A`.
- Never run loom itself (`./loom`, `go run .`) or `loom serve stop` in a loom pane.
- Tests: `CGO_ENABLED=0 go test ...` (no gcc on this machine). Race: `CC=clang CGO_ENABLED=1 go test -race ./...`.
- Format with `gofmt -w` on the changed `.go` files only, never `gofmt -w .`.
- golangci-lint is v2 locally and CI pins v1: run `CGO_ENABLED=0 go vet ./...`, and say plainly that golangci-lint was skipped.
- Each commit message below ends with `<attribution lines from the session's system reminder>`. Replace it with the attribution lines (`Co-Authored-By:`, `Claude-Session:`) that the executing session's system reminder gives.

---

### Task 1: Pause keeps the worktree

**Files:**
- Modify: `session/instance.go` (the `Pause` method, its imports)
- Create: `session/pause_keeps_worktree_test.go`
- Modify: `session/resume_inplace_test.go` (new fixture `newRunningOnFakeTmux`; delete `TestPause_AbortDropsItsStash`)
- Modify: `session/instance_lifecycle_test.go`, `session/claude_tmp_test.go`, `session/claude_tmp_archive_dir_test.go`, `session/stash_notice_test.go`
- Modify: `session/CLAUDE.md` (two rules name tests this task removes or adds; the lint fails on a missing test name)

- [ ] **Step 1: Add the realistic fixture**

In `session/resume_inplace_test.go`, replace `newCrashRecoveredInstance` (doc comment included) with:

```go
// newRunningOnFakeTmux is a started, Running Claude instance over a real
// worktree whose tmux commands and recovery launches go to a fake server.
// The server answers has-session "no such session" until something is
// launched, as a real one does after Pause's kill-session, so a Resume
// after Pause meets what production meets: a dead session over the kept
// worktree.
func newRunningOnFakeTmux(t *testing.T) (*Instance, *fakeTmuxServer) {
	t.Helper()
	srv := &fakeTmuxServer{}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	inst.program = "claude"
	orig := newRecoverySession
	newRecoverySession = func(name, program string, env ...string) *tmux.Session {
		return tmux.NewSessionWithDeps(name, program, srv, srv.runner(), env...)
	}
	t.Cleanup(func() { newRecoverySession = orig })
	require.Equal(t, Running, inst.GetStatus())
	return inst, srv
}

// newCrashRecoveredInstance is the record ReconcileAndRestore hands to
// CrashRestart: persisted Running, its tmux session gone, worktree on disk.
func newCrashRecoveredInstance(t *testing.T) (*Instance, *fakeTmuxServer) {
	t.Helper()
	return newRunningOnFakeTmux(t)
}
```

- [ ] **Step 2: Write the failing regression tests**

Create `session/pause_keeps_worktree_test.go`:

```go
package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInstance_PauseKeepsTheWorktree is the regression guard for a pause
// losing ignored files. Pause used to remove the worktree and Resume
// rebuild it from the branch, carrying uncommitted work across in a stash;
// the stash honours .gitignore, so every ignored file (a /draft-plan run's
// .superpowers/ state, .env, .claude/settings.local.json) was lost. Pause
// now stops the agent and leaves the tree alone, and Resume relaunches the
// agent in it.
func TestInstance_PauseKeepsTheWorktree(t *testing.T) {
	inst, srv := newRunningOnFakeTmux(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	exclude := filepath.Join(gw.GetRepoPath(), ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(exclude), 0o755))
	require.NoError(t, os.WriteFile(exclude, []byte(".superpowers/\n"), 0o644))
	ignored := filepath.Join(dir, ".superpowers", "drafts", "STATE.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(ignored), 0o755))
	require.NoError(t, os.WriteFile(ignored, []byte("phase: drafting\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("tracked edit\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("never committed\n"), 0o644))
	require.Empty(t, gitIn(t, dir, "status", "--porcelain", "--", ".superpowers"), "precondition: the file is ignored")

	require.NoError(t, inst.Pause(nil))

	assert.Equal(t, Paused, inst.GetStatus())
	assert.Equal(t, "phase: drafting\n", readFile(t, ignored), "Pause keeps ignored files")
	assert.Equal(t, "tracked edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	assert.Empty(t, gw.GetStashRef(), "nothing is stashed")
	assert.Empty(t, gitIn(t, gw.GetRepoPath(), "stash", "list"))

	require.NoError(t, resumeLikeApp(t, inst))

	assert.Equal(t, "phase: drafting\n", readFile(t, ignored), "Resume keeps ignored files")
	assert.Equal(t, "tracked edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	launches := srv.launchArgs()
	require.Len(t, launches, 1, "resume relaunches the agent")
	assert.Equal(t, dir, argAfter(launches[0], "-c"), "in the kept worktree")
}

// TestPause_SavesLoadingBeforeItStopsTheAgent: with the worktree kept, a
// record still saved Running over a dead session and an intact tree is one
// reconcile crash-restarts, so a pause that crashed after its kill would be
// undone. Pause saves first, while the caller has the instance in Loading,
// which reconcile marks Paused whether or not the session survived.
func TestPause_SavesLoadingBeforeItStopsTheAgent(t *testing.T) {
	var events []string
	srv := &fakeTmuxServer{}
	runner := srv.runner()
	inst := newTestPausableInstanceWithExec(t, cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") {
				events = append(events, "kill")
			}
			return runner.RunFunc(c)
		},
		OutputFunc: runner.OutputFunc,
	})
	require.NoError(t, inst.TransitionTo(Loading), "as core.Model.Pause does")
	var saved []Status
	save := func() error {
		saved = append(saved, inst.ToInstanceData().Status)
		events = append(events, "save")
		return nil
	}

	require.NoError(t, inst.Pause(save))

	require.NotEmpty(t, events)
	assert.Equal(t, "save", events[0], "the checkpoint is saved before the agent's session is killed")
	assert.Equal(t, Loading, saved[0])
	assert.Equal(t, ActionMarkPaused, DetermineRecoveryAction(saved[0], false, true, false),
		"a crash after the kill reconciles to Paused")
	assert.Equal(t, ActionKillAndPause, DetermineRecoveryAction(saved[0], true, true, false),
		"and a crash before it too")
	assert.Equal(t, Paused, saved[len(saved)-1])
}
```

- [ ] **Step 3: Run them and watch them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestInstance_PauseKeepsTheWorktree|TestPause_SavesLoadingBeforeItStopsTheAgent' -count=1`
Expected: both FAIL. `TestInstance_PauseKeepsTheWorktree` fails in `readFile` because the worktree is gone. `TestPause_SavesLoadingBeforeItStopsTheAgent` fails because `events[0]` is `"kill"`: the old Pause only checkpoints when it stashed, and this tree is clean.

- [ ] **Step 4: Replace `Pause`**

In `session/instance.go`, replace the whole `Pause` method and its doc comment (from `// Pause stops the tmux session and removes the worktree, preserving the branch.` through the method's closing `}`) with:

```go
// Pause stops the agent and keeps its worktree. It closes the agent's tmux
// session and the terminal pane's, archives Claude's temp dir, copies its
// chats and marks the instance Paused. The worktree is left as it is, with
// its uncommitted work and ignored files, and Resume relaunches the agent
// in it (decideResume: an intact tree under a dead session).
//
// If saveState is non-nil, it is called before anything is stopped and
// again once the instance is Paused. The caller has moved the instance to
// Loading (core.Model.Pause), and reconcile marks a saved Loading record
// Paused whether its session lives or not; a record still saved Running
// over a dead session and an intact tree is crash-restarted instead, which
// would undo a pause that crashed after the kill.
//
// It refuses while another Pause or a Resume of the same worktree is
// running (see opsInFlight).
func (i *Instance) Pause(saveState func() error) (err error) {
	lg := i.getLogger()
	t0 := time.Now()
	lg.Debug("instance.pause.begin")
	defer func() {
		args := []any{"duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		lg.Debug("instance.pause.end", args...)
	}()

	if i.IsWorkspaceTerminal {
		return fmt.Errorf("cannot pause workspace terminal")
	}
	if !i.isStarted() {
		return fmt.Errorf("cannot pause instance that has not been started")
	}
	if i.GetStatus() == Paused {
		return fmt.Errorf("instance is already paused")
	}

	gw := i.getGitWorktree()
	ts := i.getTmuxSession()

	// Held until Pause returns: no Resume of this worktree starts while the
	// archive below is still deleting Claude's temp dir, and no second Pause
	// repeats this one's steps (see opsInFlight).
	end, err := beginOp(gw.GetWorktreePath(), opPause)
	if err != nil {
		return err
	}
	defer end()

	// Checkpoint before anything stops (see the doc comment). A failed save
	// stops the pause with the agent still running.
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("pause checkpoint save: %w", err)
		}
	}

	// Kill the tmux session so the agent process actually stops. Resume
	// relaunches it with BuildRecoveryCommand, so --continue (or its
	// equivalent) restores the conversation for agents that support it.
	if err := ts.Close(); err != nil {
		log.For("session").Warn("pause_close_tmux_failed", "err", err)
		// A Close failure usually just means the session was already dead,
		// which is harmless — carry on. Only an answered "no such session"
		// clears us to: the same load that makes kill-session fail also
		// starves the liveness probe, and a pause that went on would mark
		// Paused an agent that is still running, which a resume would then
		// relaunch beside. Abort instead; the core reverts the instance to
		// Running, and the pause can be retried.
		if ts.SessionLiveness() != tmux.LivenessDead {
			return fmt.Errorf("failed to stop the agent's tmux session and cannot confirm it is gone; the session was not paused: %w", err)
		}
	}

	// The terminal pane (ui.TerminalPane) runs its own tmux session for this
	// instance, keyed by the same title but tracked outside Instance and
	// TmuxSession. A pause stops everything the session runs, and a shell
	// kept alive under a Paused row has no client to show it. Best-effort:
	// "no such session" (never opened) is the common case.
	if err := ts.CloseRelatedSession(tmux.TerminalSessionName(i.Title)); err != nil {
		log.For("session").Debug("pause_close_terminal_tmux_failed", "err", err)
	}

	// Before the status reads Paused: the app can start a resume the moment
	// it does, and a resume that began while this slow step was still
	// deleting the temp dir would find it in place, skip the restore, launch
	// Claude in it, and have the archive delete what the new session writes.
	// A crash mid-archive leaves the saved status Loading; the next load's
	// reconcile marks the record Paused, with no relaunch attempted. Resume
	// then finds the temp dir either intact or zipped and restores it from
	// either.
	archiveClaudeTempFn(i.ConfigDir, gw.GetWorktreePath(), "pause")
	// Claude deletes a transcript a month after its last write, and a paused
	// session writes none: keep a copy for Resume to put back. A copy deletes
	// nothing, so unlike the archive above it has no ordering to keep.
	i.archiveChat(gw.GetWorktreePath(), "pause")

	_ = i.TransitionTo(Paused)
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("pause checkpoint save: %w", err)
		}
	}
	return nil
}
```

Then delete the imports only the old Pause used: `"os"` and `"github.com/atotto/clipboard"` from `session/instance.go`'s import block.

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./session/`
Expected: no output. (A spike on 2026-10-22 confirmed these are the only two imports to drop.)

- [ ] **Step 5: Run the regression tests and watch them pass**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestInstance_PauseKeepsTheWorktree|TestPause_SavesLoadingBeforeItStopsTheAgent' -count=1`
Expected: PASS.

- [ ] **Step 6: Rework the lifecycle tests (`session/instance_lifecycle_test.go`)**

The spike found 12 session tests failing against the new Pause, and none in `core`, `app`, `internal`, `cmd` or `script`. This step and the next three fix them, and also remove tests that would still pass but no longer test anything.

(a) Add this helper below `newTestPausableInstanceWithExec`:

```go
// pauseLikeAnOlderBuild leaves inst as a build from before Pause kept the
// worktree left a paused session: files written and stashed (when there
// are any), the agent's session closed, the worktree removed, the status
// Paused. Resume still meets such records after an upgrade.
func pauseLikeAnOlderBuild(t *testing.T, inst *Instance, files map[string]string) *git.GitWorktree {
	t.Helper()
	gw := inst.getGitWorktree()
	dir := gw.GetWorktreePath()
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	if len(files) > 0 {
		sha, err := gw.StashChanges("[loom] stash from an older pause")
		require.NoError(t, err)
		gw.SetStashRef(sha)
	}
	require.NoError(t, inst.getTmuxSession().Close())
	out, err := exec.Command("git", "-C", gw.GetRepoPath(), "worktree", "remove", "--force", dir).CombinedOutput()
	require.NoError(t, err, "worktree remove: %s", out)
	require.NoError(t, inst.TransitionTo(Paused))
	return gw
}
```

(b) In `TestInstance_ResumeSurfacesBranchGoneHint`, replace
`require.NoError(t, inst.Pause(nil), "precondition: pause succeeds")` with:

```go
	// The branch-gone hint comes from the rebuild, which only a tree-less
	// record reaches; a kept tree holds the branch checked out, so git
	// would refuse to delete it.
	pauseLikeAnOlderBuild(t, inst, nil)
```

(c) Delete `TestInstance_PauseStashesUncommittedWork` and `TestInstance_PauseCleanWorktreeSetsNoStashRef`, doc comments included. `TestInstance_PauseKeepsTheWorktree` replaces them.

(d) Replace `TestInstance_ResumeRestoresStashedWork` (doc comment included) with:

```go
// TestInstance_ResumeRestoresAnOlderPausesStash: a session paused by a
// build from before Pause kept the worktree has no tree on disk and its
// uncommitted work in a stash. Resume rebuilds the tree from the branch and
// re-applies the stash, for both change classes `git stash` treats
// differently (a tracked modification and an untracked file), and clears
// the reference.
func TestInstance_ResumeRestoresAnOlderPausesStash(t *testing.T) {
	inst := newTestPausableInstance(t)
	gw := pauseLikeAnOlderBuild(t, inst, map[string]string{
		"README.md": "uncommitted\n",
		"new.txt":   "untracked\n",
	})
	require.NotEmpty(t, gw.GetStashRef())
	require.NoDirExists(t, gw.GetWorktreePath())

	require.NoError(t, inst.Resume(nil))

	assert.Empty(t, gw.GetStashRef(), "Resume must clear StashRef after a clean apply")
	dirty, err := os.ReadFile(filepath.Join(gw.GetWorktreePath(), "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "uncommitted\n", string(dirty))
	untracked, err := os.ReadFile(filepath.Join(gw.GetWorktreePath(), "new.txt"))
	require.NoError(t, err)
	assert.Equal(t, "untracked\n", string(untracked))
}
```

(e) In `TestInstance_StashRefSurvivesSnapshotRoundTrip`, replace everything from `gw, err := inst.GetGitWorktree()` through `require.NoError(t, inst.Pause(nil))` with:

```go
	gw := pauseLikeAnOlderBuild(t, inst, map[string]string{"dirty.txt": "uncommitted\n"})
```

Keep the lines after it, from `wantSha := gw.GetStashRef()` on. Change the doc comment's first sentence to: `// TestInstance_StashRefSurvivesSnapshotRoundTrip guards the disk round-trip of a pending stash (left by an older build's pause):`.

(f) In `TestInstance_KillDropsLeftoverStash`, replace everything from `gw, err := inst.GetGitWorktree()` through `require.NotEmpty(t, gw.GetStashRef())` with:

```go
	gw := pauseLikeAnOlderBuild(t, inst, map[string]string{"dirty.txt": "uncommitted\n"})
	repoPath := gw.GetRepoPath()
	require.NotEmpty(t, gw.GetStashRef())
```

Keep the rest. In its doc comment, change `a Paused instance's stash` to `a pending stash (an older build's pause left one)`.

(g) Replace the doc comment of `TestInstance_PauseClosesTerminalPaneSession` with the following, and change its assertion message to `"Pause must kill the terminal pane's tmux session"`:

```go
// TestInstance_PauseClosesTerminalPaneSession: the terminal pane runs its
// own tmux session ("term_<title>") decoupled from Instance, keyed only by
// title. A pause stops everything the session runs, whichever caller asked
// for it (core.Model.Pause, a Lua inst:pause(), `loom session pause`), and
// a shell left alive under a Paused row has no client to show it.
```

(h) In `TestInstance_PauseAbortsWhenTmuxSurvives` and `TestInstance_PauseAbortsWhenTmuxLivenessUnknown`, replace each final `assert.DirExists(...)` with:

```go
	assert.Equal(t, Running, inst.GetStatus(), "an aborted pause leaves the instance as it was")
	assert.FileExists(t, filepath.Join(gw.GetWorktreePath(), "README.md"))
```

In the first test's doc comment, replace the sentences from `Pause kills the tmux session precisely` to the end with: `Pause must not report a session Paused while its agent may still be running: a resume would then launch a second agent beside it, in the same worktree.` In the second, replace `so Pause read "already dead" and went on to remove the worktree — the very thing the guard exists to prevent.` with `so Pause read "already dead" and marked Paused an agent that may still be running.`, and replace `abort, keep the worktree.` with `abort.`.

- [ ] **Step 7: Rework the archive tests**

In `session/claude_tmp_test.go`:

(a) `TestPause_ArchivesClaudeTempAndResumeRestoresIt`: replace `inst := newTestPausableInstance(t)` with `inst, _ := newRunningOnFakeTmux(t)`. The old fixture's tmux mock answered has-session as alive after the kill, so Resume reattached and never restored.

(b) `TestResume_RestoresClaudeTempBeforeTheAgentLaunches`: rename the subtest `"rebuild"` to `"rebuild (an older build's pause)"`, and replace its body up to `seen := ...` with:

```go
		root := claudeRoot(t)
		inst, srv := newRunningOnFakeTmux(t)
		withConfigDir(inst)
		gw := inst.getGitWorktree()
		dir := claudeTempDir(t, root, gw.GetWorktreePath())
		require.NoError(t, inst.Pause(nil))
		require.NoDirExists(t, dir)
		// A build from before Pause kept the worktree also removed it, so
		// Resume rebuilds.
		gitIn(t, gw.GetRepoPath(), "worktree", "remove", "--force", gw.GetWorktreePath())
```

Keep `seen := seeScratchpadBeforeLaunch(...)` and everything after it.

(c) `TestPause_ArchivesBeforeTheInstanceIsPaused`: replace the three assertions `assert.Zero(t, savesAtArchive[0], ...)`, `assert.Equal(t, Paused, inst.GetStatus())` and `assert.Equal(t, 1, saves)` with:

```go
	assert.Equal(t, 1, savesAtArchive[0], "only the checkpoint before the kill precedes the archive; the Paused save waits for it")
	assert.Equal(t, Paused, inst.GetStatus())
	assert.Equal(t, 2, saves, "the checkpoint, then Paused")
```

(d) `TestResume_RestoreFailureIsANoticeAndTheLaunchProceeds`: replace `inst := newTestPausableInstance(t)` with `inst, _ := newRunningOnFakeTmux(t)`.

(e) Replace `TestPause_RefusesALockedTreeBeforeItStashesOrKills` (doc comment included) with:

```go
// TestPause_LeavesALockedTreeAsItIs: a lock guards a tree against removal,
// and Pause no longer removes one, so it pauses a locked tree like any
// other and leaves the lock for the user to lift.
func TestPause_LeavesALockedTreeAsItIs(t *testing.T) {
	inst, _ := newRunningOnFakeTmux(t)
	gw := inst.getGitWorktree()
	wt, repo := gw.GetWorktreePath(), gw.GetRepoPath()
	wip := filepath.Join(wt, "wip.txt")
	require.NoError(t, os.WriteFile(wip, []byte("uncommitted"), 0o644))
	gitIn(t, repo, "worktree", "lock", "--reason", "mine", wt)

	require.NoError(t, inst.Pause(nil))

	assert.Equal(t, Paused, inst.GetStatus())
	assert.FileExists(t, wip)
	assert.Contains(t, gitIn(t, repo, "worktree", "list", "--porcelain"), "locked mine")
}
```

In `session/claude_tmp_archive_dir_test.go`, in both `TestPause_ArchivesIntoTheConfiguredDirAndResumeRestores` and `TestResume_FindsAZipParkedBeforeTheSettingChanged`, replace `inst := newTestPausableInstance(t)` with `inst, _ := newRunningOnFakeTmux(t)`.

- [ ] **Step 8: Delete the stash-abort tests**

Pause no longer stashes, so it has no stash to drop on an abort.
- Delete `TestPause_AbortDropsItsStash` (doc comment included) from `session/resume_inplace_test.go`.
- Delete `TestPause_AbortReportsUndroppableStash` (doc comment included) from `session/stash_notice_test.go`.

Run: `CGO_ENABLED=0 go vet ./session/`
Expected: no output. If vet reports an unused import in a test file (`cmd_test` in `stash_notice_test.go`, or `git` or `errors` in `claude_tmp_test.go`), delete that import and run vet again.

- [ ] **Step 9: Update `session/CLAUDE.md`**

Replace the rule that begins `- **Paused does not mean the worktree is gone:` (the whole line) with:

```markdown
- **Paused does not mean the worktree is gone, and Pause keeps it: classify with `InspectTree` and decide with `decideResume`, and never route an intact tree to `Setup`.** Pause stops the agent and leaves the tree, ignored files included (a stash honours `.gitignore`), and an agent that exits on its own is only marked Paused, so a paused worktree may hold the only copy of uncommitted work; `Setup` runs `git worktree remove -f`, which deletes an intact dirty tree without a word. Pause saves its checkpoint (the caller's Loading) before it closes the agent's session, since reconcile crash-restarts a record saved Running over a dead session and an intact tree. Refuse on `TreeUnverified` or an unanswered probe. **Enforced** by `TestDecideResume`, `TestInstance_PauseKeepsTheWorktree`, `TestPause_SavesLoadingBeforeItStopsTheAgent`, `TestResume_AgentExitedKeepsUncommittedWork` and `TestCrashRestart_RefusesGuttedWorktree`. Gutted trees and mass deaths: [`../docs/claude/incident-triage.md`](../docs/claude/incident-triage.md)
```

In the rule that begins `- **Report a forgotten stash or a failed drop`, replace `(Resume's names the recovery commands too; Kill's names the SHA only), and Pause's abort puts the failed drop in its error. **Enforced** by `TestKill_UndroppableStashIsANotice`, `TestResume_RebuildUndroppableStashIsANotice` and `TestPause_AbortReportsUndroppableStash`.` with `(Resume's names the recovery commands too; Kill's names the SHA only). **Enforced** by `TestKill_UndroppableStashIsANotice` and `TestResume_RebuildUndroppableStashIsANotice`.`

- [ ] **Step 10: Run the suites**

Run: `CGO_ENABLED=0 go test ./session/... ./core/... ./app/... ./internal/... ./cmd/... ./script/... -count=1 2>&1 | grep -E '^(--- FAIL|FAIL|ok|panic)'`
Expected: every line is `ok`.
Run: `CGO_ENABLED=0 go test . -run TestRepo_ClaudeMDStructure -count=1`
Expected: `ok`. This is the lint that resolves the test names in `session/CLAUDE.md`. If it can't run from the root package, run `go run ./tools/claudemd` instead; it should exit 0.

- [ ] **Step 11: Format and commit**

```bash
gofmt -l session/*.go
```
Expected: no output. If any file is listed, run `gofmt -w` on that file.

```bash
git add -- session/instance.go session/pause_keeps_worktree_test.go session/resume_inplace_test.go session/instance_lifecycle_test.go session/claude_tmp_test.go session/claude_tmp_archive_dir_test.go session/stash_notice_test.go session/CLAUDE.md
git commit -m "fix(session): pause keeps the worktree

Pause removed the worktree and Resume rebuilt it from the branch, with
uncommitted work carried in a stash that honours .gitignore, so every
ignored file was lost across a pause. Pause now stops the agent and
leaves the tree; Resume's in-place path already relaunches it there.
It saves its Loading checkpoint before the kill, so a crash mid-pause
reconciles to Paused instead of a crash-restart.

<attribution lines from the session's system reminder>" -- session/instance.go session/pause_keeps_worktree_test.go session/resume_inplace_test.go session/instance_lifecycle_test.go session/claude_tmp_test.go session/claude_tmp_archive_dir_test.go session/stash_notice_test.go session/CLAUDE.md
```

---

### Task 2: Orphan claims compare canonical paths

**Files:**
- Modify: `session/orphan.go` (`DiscoverOrphans`)
- Test: `session/orphan_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/orphan_test.go`, after `TestDiscoverOrphans_FiltersClaimed`:

```go
// TestDiscoverOrphans_ClaimMatchesThroughASymlink: a record may spell its
// worktree through a symlink (a repo reached under two paths), and paused
// sessions keep their trees under a dead session. A claim that misses that
// spelling leaves a clean tree to be auto-removed, ignored files and all,
// and a dirty one listed a second time. An unclaimed tree beside it is
// still found.
func TestDiscoverOrphans_ClaimMatchesThroughASymlink(t *testing.T) {
	isolateOrphanTmux(t)
	withStubProbe(t)
	cfgDir := t.TempDir()
	worktreesDir := filepath.Join(cfgDir, "worktrees", "aidanb")
	require.NoError(t, os.MkdirAll(worktreesDir, 0o755))
	orphanPath := filepath.Join(worktreesDir, "example-jupyter-notebook_18acb35cb8ad6e5a")
	pausedPath := filepath.Join(worktreesDir, "readibility-analysis_18acb32c71e51ff4")
	require.NoError(t, os.Mkdir(orphanPath, 0o755))
	require.NoError(t, os.Mkdir(pausedPath, 0o755))

	link := filepath.Join(t.TempDir(), "via-link")
	require.NoError(t, os.Symlink(cfgDir, link))
	claimed := map[string]bool{
		filepath.Join(link, "worktrees", "aidanb", filepath.Base(pausedPath)): true,
	}

	got, err := DiscoverOrphans(cfgDir, claimed, internalexec.Default{})
	require.NoError(t, err)
	require.Len(t, got, 1, "the tree claimed through the link is not an orphan")
	assert.Equal(t, orphanPath, got[0].WorktreePath)
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `CGO_ENABLED=0 go test ./session/ -run TestDiscoverOrphans_ClaimMatchesThroughASymlink -count=1`
Expected: FAIL, `"[...]" should have 1 item(s), but has 2`.

- [ ] **Step 3: Compare canonically**

In `session/orphan.go`, in `DiscoverOrphans`, insert this right after the `os.Stat(worktreesDir)` check and before `out := make(...)`:

```go
	// Compare by canonical path: a record may spell its worktree through a
	// symlink (a repo reached under two paths), and a claimed tree read as
	// unclaimed is auto-removed when clean (DisposeClean), ignored files
	// and all, or listed a second time when dirty. Paused sessions keep
	// their trees under a dead session, so either would be common.
	claimed := make(map[string]bool, len(claimedPaths))
	for p, ok := range claimedPaths {
		if ok {
			claimed[worktreeKey(p)] = true
		}
	}
```

In the walk, replace `if claimedPaths[path] {` with `if claimed[worktreeKey(path)] {`. Then add this sentence to the `claimedPaths` paragraph of the doc comment: `Paths are compared after resolving symlinks (worktreeKey), so a record's spelling of its path need not match the walk's.`

- [ ] **Step 4: Run the orphan tests**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestDiscoverOrphans|TestOrphan' -count=1 && CGO_ENABLED=0 go test ./core/ -count=1`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
gofmt -l session/orphan.go session/orphan_test.go
git add -- session/orphan.go session/orphan_test.go
git commit -m "fix(session): claim orphan worktrees by canonical path

A record spelling its worktree through a symlink left the tree
unclaimed, so a clean one under a dead session was auto-removed. Paused
sessions now keep their trees, which makes that the ordinary case.

<attribution lines from the session's system reminder>" -- session/orphan.go session/orphan_test.go
```

---

### Task 3: Hold the e2e pause through a link-time variable

**Files:**
- Create: `session/pause_hold.go`, `session/pause_hold_test.go`
- Modify: `session/instance.go` (one call in `Pause`)
- Modify: `e2e/daemon_test.go` (`TestE2E_Daemon_PauseAcrossAStop`; delete `holdStashes` and `dirtyWorktree`)
- Modify: `e2e/CLAUDE.md` (the pause-in-flight rule)

- [ ] **Step 1: Write the failing unit test**

Create `session/pause_hold_test.go`:

```go
package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHoldPauseForE2E(t *testing.T) {
	orig := e2ePauseHold
	t.Cleanup(func() { e2ePauseHold = orig })

	marker := filepath.Join(t.TempDir(), "held")
	e2ePauseHold = marker + "|50ms"
	began := time.Now()
	holdPauseForE2E()
	assert.FileExists(t, marker, "the marker says the pause is in flight")
	assert.GreaterOrEqual(t, time.Since(began), 50*time.Millisecond)

	for _, v := range []string{"", "no-duration", "|1s", marker + "|soon"} {
		e2ePauseHold = v
		began = time.Now()
		holdPauseForE2E()
		assert.Less(t, time.Since(began), 50*time.Millisecond, "%q holds nothing", v)
	}
}
```

Run: `CGO_ENABLED=0 go test ./session/ -run TestHoldPauseForE2E -count=1`
Expected: FAIL to build with `undefined: e2ePauseHold`.

- [ ] **Step 2: Add the hold**

Create `session/pause_hold.go`:

```go
package session

import (
	"os"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// e2ePauseHold is empty in every build but the e2e suite's, which sets it
// at link time
// (-X github.com/aidan-bailey/loom/session.e2ePauseHold=<marker>|<duration>)
// to hold each Pause in flight: TestE2E_Daemon_PauseAcrossAStop stops the
// daemon under a pause, and Pause runs no git step a hook could hold.
var e2ePauseHold string

// holdPauseForE2E creates e2ePauseHold's marker file and sleeps its
// duration. It does nothing when e2ePauseHold is empty, and logs and does
// nothing when it does not parse.
func holdPauseForE2E() {
	if e2ePauseHold == "" {
		return
	}
	marker, wait, ok := strings.Cut(e2ePauseHold, "|")
	d, err := time.ParseDuration(wait)
	if !ok || marker == "" || err != nil {
		log.For("session").Warn("e2e_pause_hold.unparsable", "value", e2ePauseHold)
		return
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		log.For("session").Warn("e2e_pause_hold.marker_failed", "err", err)
	}
	time.Sleep(d)
}
```

In `session/instance.go`'s `Pause`, directly after the checkpoint block (the first `if saveState != nil { ... }`), add:

```go
	holdPauseForE2E()
```

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestHoldPauseForE2E|TestPause_|TestInstance_Pause' -count=1`
Expected: `ok`.

- [ ] **Step 3: Retarget the e2e test**

In `e2e/daemon_test.go`, delete `holdStashes` and `dirtyWorktree` with their doc comments; nothing else uses them. In `TestE2E_Daemon_PauseAcrossAStop`, replace the lines from `sb := newSandbox(t, "")` through `held := holdStashes(t, sb, hold)` with:

```go
	sb := newSandbox(t, "")
	// This build holds each pause for hold after creating held, and the stop
	// below waits for that marker: the pause request has reached the daemon
	// and its job is under way, so the stop arrives with it in flight,
	// whichever of the TUI and the stop's process is quicker.
	const hold = 3 * time.Second
	held := filepath.Join(t.TempDir(), "pause-held")
	buildLoom(t, sb, filepath.Base(sb.LoomBin()),
		"-X github.com/aidan-bailey/loom/session.e2ePauseHold="+held+"|"+hold.String())
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	rec := servingDaemon(t, sb)
```

Change the `require.Eventually` message from `"the pause never reached its stash"` to `"the pause never reached its hold"`. If vet then reports an import that only `holdStashes` used (`fmt` or `devsandbox.ShellQuote`'s package), delete it.

Run: `CGO_ENABLED=0 go vet -tags e2e ./e2e/`
Expected: no output.

- [ ] **Step 4: Run the e2e test**

This needs tmux and builds loom into a private sandbox; it never touches the user's daemon.
Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_Daemon_PauseAcrossAStop -count=1 -v 2>&1 | tail -20`
Expected: `--- PASS: TestE2E_Daemon_PauseAcrossAStop`.

- [ ] **Step 5: Update `e2e/CLAUDE.md`**

Replace the rule that begins `- **Prove a pause is in flight before stopping the daemon under it.**` (the whole line) with:

```markdown
- **Prove a pause is in flight before stopping the daemon under it.** The fake agent pauses in milliseconds, and the stop's process races the TUI's request, so a stop sent on a timing guess can arrive first (the pause is refused) or after the pause. `TestE2E_Daemon_PauseAcrossAStop` builds loom with `session.e2ePauseHold` set at link time (`-X github.com/aidan-bailey/loom/session.e2ePauseHold=<marker>|<duration>`, read by `holdPauseForE2E` after Pause's checkpoint), so each pause creates the marker and then waits; the test waits for the marker, stops the daemon and asserts `serve stop` waited. Pause runs no git, so no git hook can hold it. **Enforced** by that test's marker wait and elapsed-time assertion; a copy that sends the stop without the marker passes or fails by timing.
```

- [ ] **Step 6: Commit**

```bash
gofmt -l session/pause_hold.go session/pause_hold_test.go session/instance.go e2e/daemon_test.go
git add -- session/pause_hold.go session/pause_hold_test.go session/instance.go e2e/daemon_test.go e2e/CLAUDE.md
git commit -m "test(e2e,session): hold a pause in flight through a link-time variable

Pause no longer stashes, so the reference-transaction hook that held
refs/stash has nothing to hold. The e2e build sets session.e2ePauseHold;
every other build leaves it empty.

<attribution lines from the session's system reminder>" -- session/pause_hold.go session/pause_hold_test.go session/instance.go e2e/daemon_test.go e2e/CLAUDE.md
```

---

### Task 4: Text, comments and docs

**Files:** `ui/preview.go`, `app/help.go`, `app/views_freshness_test.go`, `keys/keys.go`, `script/defaults.lua`, `script/intent.go`, `internal/cli/session.go`, `core/ops.go`, `core/completions.go`, `core/reset.go`, `session/instance.go`, `main.go`, `USAGE.md`, `README.md`, `docs/ARCHITECTURE.md`, `session/README.md`, `docs/specs/scripting.md`, `docs/claude/incident-triage.md`

- [ ] **Step 1: UI and help text**

`ui/preview.go`, the `session.Paused` case: replace the `Render(fmt.Sprintf(...))` argument, which builds the "checked out at … (copied to your clipboard)" sentence, with:

```go
				Render(fmt.Sprintf("Its worktree and branch '%s' are kept as they were.", instance.Branch)),
```

`app/help.go`:
- `generalHandoffEntries`: change `"Stash: stash changes and pause session"` to `"Pause: stop the session's agent, keeping its worktree"`.
- `instanceStartHandoffEntries`: change `"Stash this instance's branch"` to `"Pause this instance"`.
- `stashCommandEntries`: change `"Stash: stash changes locally and pause session"` to `"Pause: stop the session's agent, keeping its worktree"`.
- `helpTypeInstanceStash.toContent`: change the title `"Stash Instance"` to `"Pause Instance"`, the line `"Changes will be stashed locally. The branch name has been copied to your clipboard for you to checkout."` to `"The agent stops. Its worktree stays as it is, uncommitted and ignored files included."`, and `"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off."` to `"Feel free to make changes in the worktree and commit them. When resuming, the agent is relaunched there and continues from where it left off."`.

`keys/keys.go`: `key.WithHelp("s", "stash")` becomes `key.WithHelp("s", "pause")`.
`script/defaults.lua`: on the `s` binding, `{ help = "stash" }` becomes `{ help = "pause" }`. Keep the column alignment.

`app/views_freshness_test.go`, `TestViewsChanged_RefreshesTheSelectionsCopies`: the menu now says `pause`. Change the fixture assertion to `require.Contains(t, ansi.Strip(m.menu.String()), "s pause", "fixture: a running session offers pause")` and the last one to `assert.NotContains(t, menu, "s pause")`. Match on `"s pause"`, not `"pause"`, so that a status word like "paused" can't match. Before relying on that form, print `ansi.Strip(m.menu.String())` once (a temporary `t.Log`) and confirm the menu renders key and help as `s pause`. If it renders them differently, use the rendered form.

- [ ] **Step 2: Lua and CLI text**

`script/intent.go`: replace the `StashIntent` doc comment with:

```go
// StashIntent asks the app to pause the selected instance: its agent stops
// and its worktree is kept. Confirm gates the operation; Help opens the
// explanatory overlay instead of pausing.
```

`internal/cli/session.go`: change the `pause` command's help from `"Pause a session: its agent stops and its worktree is removed, its branch kept"` to `"Pause a session: its agent stops; its worktree and branch are kept"`.

- [ ] **Step 3: Code comments and reset warnings**

- `core/ops.go`, the `pauseInst` doc comment: change `(Instance.Pause: stash, kill the session and its terminal pane's shell, remove the worktree, save ws's instances through saveSnapshot)` to `(Instance.Pause: save a checkpoint, kill the session and its terminal pane's shell, archive Claude's temp dir, save ws's instances through saveSnapshot)`.
- `core/completions.go`: change the `deliverPause` comment to `// deliverPause finishes a pause; Pause already killed the session and saved.`
- `session/instance.go`, the `Paused` status comment: change it to `// Paused is if the instance is paused: its agent stopped, its worktree normally kept (one an older build paused may be gone; Resume decides, decideResume).`
- `core/reset.go` step 3 of the doc comment: change `removes its worktrees and their branches: unpushed commits on them are lost.` to `removes its worktrees and their branches: unpushed commits on them, and uncommitted work in them (paused sessions' included), are lost.`
- `main.go`, `resetCmd.Long`: change `"worktrees INCLUDING their branches — unpushed commits on those branches are\n" + "lost. Without --workspace ..."` so that it reads `"worktrees INCLUDING their branches — unpushed commits on those branches, and\n" + "uncommitted work in the worktrees (paused sessions' too), are lost. Without\n" + "--workspace it resets the global workspace. ..."`. Re-wrap the following lines of that string at about 80 columns. In the `--force` refusal, change `(unpushed commits are lost)` to `(unpushed commits and uncommitted work are lost)`.

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./app/ ./ui/ ./script/ ./internal/cli/ ./core/ . -count=1 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: all `ok`. If a test pins the old help or refusal text, update its expected string to the new text.

- [ ] **Step 4: User docs**

`USAGE.md`:
- Step 8 of the quick start: `8. Press \`s\` to pause the session when done`.
- The key-bar mock-up: `s stash` becomes `s pause`. Both words are five letters, so the box stays aligned.
- Replace the `**Running → Paused**` and `**Paused → Running**` blocks with:

```markdown
**Running → Paused** (on pause, `s`):
1. The agent's tmux session and the terminal pane's shell are closed
2. The worktree stays as it is: uncommitted changes, untracked and ignored files (`.env`, `.claude/settings.local.json`, build caches) are all kept, and so is the branch
3. Claude's temp dir for the session (its scratchpad and task output under `/tmp/claude-<uid>/`) is zipped into the archive and removed, since `/tmp` may not survive a reboot — see [Claude Temp-Dir Archives](#claude-temp-dir-archives)

**Paused → Running** (on resume, `r`):
1. Claude's archived scratchpad is restored before the agent starts
2. The agent is relaunched in the worktree and picks up its conversation where it left off
3. Diff baseline preserved — you see cumulative changes since session creation

A session an older loom paused (it removed the worktree on pause) is resumed by recreating the worktree from its branch and re-applying the changes that pause stashed.
```

- Key table: `| \`s\` | Pause — stop the session's agent, keeping its worktree |`.
- `### Pause and Resume`: `**Pause** — saves everything and frees disk space:` becomes `**Pause** — stops the agent and keeps everything:`. The line `Changes are stashed, worktree is removed, branch name is on your clipboard. The agent's tmux session remains in the background.` becomes `The agent's tmux session ends; the worktree stays exactly as it is, uncommitted and ignored files included, and so does its branch.` The line `Worktree is recreated from the branch, tmux session is restored.` becomes `The agent is relaunched in the worktree and continues its conversation.`
- Locked worktrees bullet: `(or removed by pause or kill, which tell you to run \`git worktree unlock\`)` becomes `(or removed by kill, which tells you to run \`git worktree unlock\`; pause leaves the tree and its lock alone)`.
- Replace the `**Stashes on pause/resume**` paragraph with:

```markdown
**Stashes from older pauses**: a loom from before pause kept the worktree
stashed uncommitted work on pause (`[loom] stash from '<title>' …` in
`git stash list`); resuming such a session re-applies it. If re-apply
conflicts, the session returns to paused, the stash is preserved, and the
error names the stash SHA so you can resolve it manually in the worktree.
```

- CLI table, `session pause`: `Pause a running session: its agent stops; its worktree and branch stay as they are`.
- CLI table, `reset --force`: change `and remove its worktrees **and their branches**.` to `and remove its worktrees **and their branches**, with any uncommitted work in them (paused sessions' included).`

`README.md`: change `` - `s` - Stash. Stashes changes and pauses the session `` to `` - `s` - Pause. Stops the session's agent, keeping its worktree ``.

- [ ] **Step 5: Architecture, package and spec docs**

- `docs/ARCHITECTURE.md`: in the status list, `` `Paused` (worktree removed, branch preserved) `` becomes `` `Paused` (agent stopped; its worktree normally kept, though one an older loom paused may be gone) ``. Lifecycle step 4 becomes `4. **Pause**: saves a checkpoint, kills the tmux session (and the terminal pane's), archives Claude's temp dir and copies its chats; the worktree and branch stay as they are. Paused.`
- `session/README.md`:
  - Line 7: the same `Paused` wording as the ARCHITECTURE status list.
  - Line 41: change `the model's tick only calls \`TransitionTo(Paused)\`: no stash, no removal, so the worktree still holds the only copy of any uncommitted work.` to `the model's tick only calls \`TransitionTo(Paused)\`, leaving the worktree as a Pause does, so it may hold the only copy of any uncommitted work.`
  - Line 55: replace `When Pause aborts because the session survived \`Close\`, it drops its own stash and saves the cleared reference: the tree still holds everything the stash captured.` with `Only records an older loom paused, which stashed on pause, still carry a pending stash.`
  - Line 57: delete the sentence `Pause's abort includes a failed drop in its error.`
  - Line 82: change `Pause archives the dir after the worktree is removed but before it marks the instance Paused.` to `Pause archives the dir before it marks the instance Paused.`
  - Line 147: change `by \`GitWorktree.Cleanup\`/\`Remove\` (Kill, Pause)` to `by \`GitWorktree.Cleanup\` (Kill) and \`Remove\``, and change `Pause and Kill check locks first (before Pause stashes, and before Kill closes the agent's session or drops a stash), so a respected lock refuses either without killing the agent;` to `Kill checks locks first (before it closes the agent's session or drops a stash), so a respected lock refuses it without killing the agent, and Pause, which removes nothing, ignores them;`.
- `docs/specs/scripting.md`: `inst:paused()` becomes `True while the session is paused: its agent stopped, its worktree kept.` In `inst:pause()`, `(stash, end its tmux session, remove the worktree)` becomes `(stop its agent and end its tmux session; the worktree stays)`.
- `docs/claude/incident-triage.md`: in the locked-worktree row, `on a pause or a kill` becomes `on a kill`.

- [ ] **Step 6: Check the docs lint and stale wording**

Run: `CGO_ENABLED=0 go test . -run TestRepo_ClaudeMDStructure -count=1 && go run ./tools/claudemd`
Expected: `ok`, and the lint exits 0.
Run: `grep -rn -i 'worktree is removed\|worktree removed\|copied to your clipboard\|stashes changes\|s stash' --include='*.md' --include='*.go' --include='*.lua' . | grep -v 'vendor/\|docs/superpowers\|docs/plans\|CHANGELOG\|docs/audit'`
Expected: no line that describes today's pause. A remaining hit about Kill, orphans or older pauses is correct.

- [ ] **Step 7: Commit**

```bash
gofmt -l ui/preview.go app/help.go app/views_freshness_test.go keys/keys.go script/intent.go internal/cli/session.go core/ops.go core/completions.go core/reset.go session/instance.go main.go
git add -- ui/preview.go app/help.go app/views_freshness_test.go keys/keys.go script/defaults.lua script/intent.go internal/cli/session.go core/ops.go core/completions.go core/reset.go session/instance.go main.go USAGE.md README.md docs/ARCHITECTURE.md session/README.md docs/specs/scripting.md docs/claude/incident-triage.md
git commit -m "docs(app,ui,cli,core): describe the pause that keeps the worktree

The key help, help screens, paused preview, CLI help, Lua docs and
user docs said pause stashes, removes the worktree and copies the
branch name; it now stops the agent and keeps the tree. loom reset's
warning adds the uncommitted work in paused trees it now deletes.

<attribution lines from the session's system reminder>" -- ui/preview.go app/help.go app/views_freshness_test.go keys/keys.go script/defaults.lua script/intent.go internal/cli/session.go core/ops.go core/completions.go core/reset.go session/instance.go main.go USAGE.md README.md docs/ARCHITECTURE.md session/README.md docs/specs/scripting.md docs/claude/incident-triage.md
```

If Step 3 changed a test's expected string, add that test file to both path lists.

---

### Final verification

- [ ] `CGO_ENABLED=0 go test ./... -count=1`: all `ok`.
- [ ] `CC=clang CGO_ENABLED=1 go test -race ./session/... ./core/... -count=1`: all `ok`.
- [ ] `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go vet -tags e2e ./e2e/`: no output. golangci-lint skipped (the local v2 rejects CI's v1 config); say so in the report.
- [ ] `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`: no output.
- [ ] `GOOS=windows CGO_ENABLED=0 go build ./...`: succeeds. Cheap, and `session/` is built for Windows.
- [ ] `go test -tags e2e ./e2e/... -count=1`: all pass. Pause is a lifecycle, and no CI job runs this suite.
- [ ] `git status --short`: clean, with no ` D` index ghosts.
- [ ] `docs/claude/`: decide whether this work taught something a future session needs. A candidate: a pause-in-flight e2e test now needs a link-time hold, because Pause runs no git. It is already recorded in the `e2e/CLAUDE.md` rule, so add a guide only if something else came up during execution.
- [ ] Report the follow-up the spec leaves out of scope: `loom workspace migrate` renames worktree directories without `git worktree repair` (`cmd/workspace_migrate.go`). Running sessions already hit that, and paused ones now do too.
