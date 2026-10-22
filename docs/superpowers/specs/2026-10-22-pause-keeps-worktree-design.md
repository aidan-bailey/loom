# Pause keeps the worktree

**Date:** 2026-10-22
**Status:** Approved design. Brainstormed with the user.

## Problem

Pause deletes the session's worktree (`git worktree remove`) and Resume
rebuilds it from the branch. Uncommitted work survives through a stash, but
the stash is built with `git add -A`, which honours `.gitignore`, so every
ignored file in the tree is lost: `.env`, `.claude/settings.local.json`,
`CLAUDE.local.md`, build caches, and tool state kept in ignored folders.

It has already cost work. `/draft-plan` keeps its whole run in
`.superpowers/drafts/<plan>/` (recon, each drafter's story file, review
reports, and `STATE.md`, the progress record a fresh run resumes from), and
commits only at its last step. A session paused on 2026-10-21 lost that
folder: `serve.log` shows the pause archiving Claude's scratchpad
(`claudetmp.archived … reason=pause`), and the resume on 2026-10-22
restoring it (`claudetmp.restored`) into a worktree created new at
10:14:31, with `.superpowers/` in neither the zip nor git. Losing it
mid-run means replaying every drafter's writes from its transcript or
paying for the drafters and reviewers a second time.

The deletion is inherited from claude-squad, where it bought two things:
disk space, and a free branch (the branch name goes to the clipboard so
the user can check it out in the main repo; Resume refuses while it is
checked out there). The user relies on pause for neither. They pause to
stop an idle agent, and to relaunch Claude (new launch options, another
account, the mod after an upgrade), and neither needs the tree gone.

## Decisions

1. **Pause stops the agent and keeps the worktree.** No stash, no
   `git worktree remove`, no prune, no lock check, no clipboard copy.
   Rejected: keeping today's behaviour behind a config flag (two pause
   paths to maintain for uses nobody has), and archiving ignored files
   across the deletion (all of them means tarring `node_modules` or
   `target/` on every pause; an allowlist misses whatever nobody thought
   of, which is how `.superpowers/` was lost).
2. **Resume is unchanged.** `decideResume` already sends an intact tree
   with a dead session to `resumeRelaunchInPlace`: the path an agent that
   exits on its own takes today, when the health tick marks it Paused over
   its tree. After this change every paused session has that shape.
3. **Records paused by an older build keep working.** A Paused record
   with no tree (and perhaps a pending `StashRef`) still takes the rebuild
   path, and Resume's and Kill's stash handling stays for it. No schema
   change.
4. **Pause still archives Claude's temp dir and copies its chats.**
   `/tmp/claude-<uid>/` does not survive a reboot, and a paused session
   writes no transcript for Claude's month-long cleanup to spare. The
   `beginOp` guard against a concurrent Pause or Resume of the same
   worktree stays, since the archive is still the slow step it guards.
5. **Pause still closes the terminal pane's tmux session.** Pause stops
   everything the session runs; a shell kept alive under a Paused row has
   no client to show it.
6. **Push, merge and the files views work on a paused session.** They
   errored only because the directory was missing; with no agent running
   they cannot race it. No new refusals.

## Design

### Pause (`session/instance.go`, `Instance.Pause`)

The core moves the instance to Loading before the job runs
(`core/requests.go`, `Model.Pause`). Pause then:

1. Refuses a workspace terminal, an unstarted instance and a Paused one,
   as today.
2. Takes `beginOp(worktree, opPause)`, as today.
3. **Saves (`saveState`) before anything is stopped**, unconditionally.
   Today this checkpoint runs only when a stash was made. The saved status
   is Loading, and reconcile turns a Loading record into Paused whether
   its tmux session is alive (`ActionKillAndPause`) or dead
   (`ActionMarkPaused`; `session/reconcile.go`, `DetermineRecoveryAction`).
   Without it, a crash after the kill leaves the saved status Running over
   a dead session and an intact tree, which reconcile crash-restarts
   (`ActionRestart`), undoing the pause. A failed save aborts the pause
   with nothing stopped.
4. Closes the agent's tmux session (`ts.Close`). A close error with a
   session not confirmed Dead (`SessionLiveness() != LivenessDead`) aborts
   the pause, which the core reverts to Running, as today. The abort no
   longer drops a stash: there is none.
5. Closes the terminal pane's session (`CloseRelatedSession`), best
   effort, as today.
6. Archives Claude's temp dir (`archiveClaudeTempFn`) and copies its
   chats (`archiveChat`), before the status reads Paused, as today.
7. Moves to Paused and saves.

Removed: `git.RefuseLocked`, `StashChanges` and its checkpoint, the
abandoned-stash drop, the `os.Stat`/`gw.Remove`/`gw.Prune` block, and
`clipboard.WriteAll`. The doc comment says the worktree is kept and
names `decideResume` as the reader of that shape.

### Orphan claims compare canonical paths (`session/orphan.go`, `core/load.go`)

`DiscoverOrphans` skips a directory under `worktrees/` only when
`claimedPaths[path]` matches the recorded `worktree_path` byte for byte
(`session/orphan.go`, the walk in `DiscoverOrphans`;
`core/load.go`, `claimedWorktreePaths`). A record whose path is spelled
through a symlink (this machine reaches the same repos as `/tb/Source` and
`/home/aidanb/Source`) is then unclaimed, and a clean tree with a dead
session is auto-removed (`DisposeClean`, `RemoveOrphanWorktree`). That
deletes its ignored files, which is this bug again. A dirty one shows up as
a second, Recoverable row. Health-tick pauses can already hit this.
After this change every paused session can.

`DiscoverOrphans` compares both sides by canonical path, using the
existing `session.worktreeKey`. On entry it rekeys the claimed set it was
handed (the core's live instances and the paths storage preserves), and it
looks each walked directory up the same way. A path that cannot be
resolved falls back to `filepath.Clean`, as `worktreeKey` does. The core's
`claimedWorktreePaths` is unchanged.

### Text

- The paused preview drops the clipboard sentence (`ui/preview.go`, the
  paused fallback), and the help screen's pause entry drops the stash and
  clipboard wording (`app/help.go`).
- The `s` binding's help text reads "pause", not "stash"
  (`keys/keys.go`, `script/defaults.lua`). The key stays `s`; the Lua names
  `stash_selected` and `StashIntent` stay (the Lua API has users), with
  their docs corrected (`script/intent.go`, `docs/specs/scripting.md`:
  `inst:paused()`, `inst:pause()`).
- `loom session pause`'s help no longer says the worktree is removed
  (`internal/cli/session.go`).
- Comments that describe the old Pause: `pauseInst` (`core/ops.go`),
  `deliverPause` (`core/completions.go`), the `Paused` status comment in
  `session/instance.go`.
- `loom reset`'s warning adds that uncommitted work in paused sessions is
  lost (`core/reset.go` and its USAGE entry): before, it survived a reset
  as a stash entry.

### Docs

`USAGE.md` (the lifecycle steps, Pause and Resume, the locks note, the
stashes note, `session pause`, the temp-dir and chat archive sections),
`README.md` (the `s` key), `docs/ARCHITECTURE.md` (the `Paused` status and
the Pause step), `session/README.md`, `session/CLAUDE.md` (the stash and
archive rules that name Pause), `e2e/CLAUDE.md` (the pause-in-flight rule)
and `docs/claude/incident-triage.md` (checking `git status` before resuming
now applies to every paused session).

## Testing

- **New, the regression:** a gitignored file and an uncommitted change in
  the worktree both survive Pause and Resume, and Resume took the in-place
  path (no `Setup`). It fails against today's Pause.
- **New:** a crash after Pause's checkpoint reconciles to Paused, not to a
  crash-restart: the saved record reads Loading before the tmux session is
  closed.
- **New:** a paused tree whose record spells its path through a symlink is
  claimed, so `DiscoverOrphans` neither removes nor surfaces it; pin both
  sides (the unclaimed tree still surfaces).
- **Rewritten:** the Pause tests that assert a stash, a removed tree, a
  lock refusal or an abort-time stash drop (`session/instance_lifecycle_test.go`,
  `session/stash_notice_test.go`, `session/resume_inplace_test.go`,
  `session/claude_tmp_test.go`) now assert the kept tree. Tests that used
  Pause to reach the rebuild path or a pending stash build their own
  tree-less fixture or set the stash by hand, through the production
  constructors.
- **Retargeted:** `TestE2E_Daemon_PauseAcrossAStop` holds the pause in
  flight by holding `refs/stash` updates with a `reference-transaction`
  hook that first creates a marker file (`e2e/daemon_test.go`,
  `holdStashes`). The new Pause runs no git, so no git hook can hold it.
  What it must still prove, as `e2e/CLAUDE.md` requires: a marker shows
  the pause is in the daemon's job before the stop is sent, and the stop
  waits for it. The plan chooses the hold after it reads how the archive
  walks its source. If the archive opens non-regular files, a FIFO in the
  session's Claude temp dir works. Otherwise it adds a test-only hold in
  the archive step, read from the sandbox's environment. The archive
  writes through `os.CreateTemp` and `os.Rename`
  (`session/claudetmp/archive.go`), so a FIFO at the zip's path cannot
  hold it.
- `CGO_ENABLED=0 go test ./...`, the race run, and the e2e suite
  (`go test -tags e2e ./e2e/...`), since Pause is a lifecycle.

## Costs accepted

- Paused trees keep their disk space until Kill.
- A paused session's branch stays checked out in its worktree, so it
  cannot be checked out in the main repo or picked for a new session.
- `loom reset` deletes uncommitted work in paused trees.
- A binary older than v0.13.0 (before `decideResume`) would rebuild over a
  kept tree with `git worktree remove -f` if the user downgraded.

## Out of scope

- `loom workspace migrate` renames worktree directories without
  `git worktree repair` (`cmd/workspace_migrate.go`), which leaves their
  `.git` links pointing at the old path. That already affects running
  sessions, and now affects paused ones as well. It is a follow-up.
- A paused card keeps the diff stats it had when it paused; nothing
  refreshes them while it is paused.
- Subagents running at the moment of a pause still die with the session.
  That is what stopping Claude means.
