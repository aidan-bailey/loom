# Changing kill, pause or what they save

Read this before changing `session.Instance.Kill`, `Instance.Pause`, core's `killInst`/`pauseInst` and their completions, or what `session.Storage` writes for them. Each section is a trap a past change fell into; the rules that guard them live in `session/CLAUDE.md`, `session/git/CLAUDE.md` and `config/CLAUDE.md`.

## What Kill returns decides the row

core's `killInst` reads `Instance.Kill`'s error three ways:

- **nil**: the kill is done. The record is deleted (`Storage.DeleteInstance`), the row leaves every workspace (`deliverKill`), and `KillResult.Trash`, when set, is deleted by a background job.
- **only a `session.Notice`** (`OnlyNotice`): the kill is done, and the user must act on what the notice names: a stash entry it could not drop, or a directory git refused to remove as no valid worktree (`git.LeftoverDirError`, left on disk because it may hold the only copy of work).
- **anything else**: the kill was refused or did not finish. `killInst` returns `OpFailed`, the row reverts to its previous status, and `deliverOpFailed` saves the workspace, because every save made while the kill ran left the Deleting record out of `state.json` (`core.Persistable`).

So return a plain error only when a retry can finish the job, and restore the instance for it (`Kill`'s `restore`). A kill once reported done whatever failed: the CLI printed "Killed", the record went, and the next load auto-cleaned a clean leftover tree, offered a dirty one for recovery without its launch options, or swept a still-running agent. A failure that a retry can never get past (a directory git will not remove, whatever the user does) must be a Notice, or `D` refuses forever.

Pause saves a checkpoint, the Loading status its caller set, before it closes the agent's session: reconcile crash-restarts a record saved Running over a dead session and an intact tree, which would undo a pause that crashed after the kill, but marks a Loading record Paused (`TestPause_SavesLoadingBeforeItStopsTheAgent`).

Pause follows the same shape: once the agent is stopped and its files archived, the pause has happened, so a failed final save is a Notice on a `PauseResult`, not an `OpFailed` that would show a dead agent as Running.

## Saving from a job

A pause or resume job runs for seconds (Pause archives Claude's temp dir and chats first). Save from it only the instance's own record (`Storage.SaveInstance`, through core's `saveOwn`), never the workspace's list: a list captured at the request drops every session created or recovered meanwhile, and `SaveInstances` rewrites the whole payload. A record is its title and `created_at` (`isRecord`): two records can share a title, each on its own branch.

## What the loop reads while a job saves

The daemon's loop builds every workspace's view on each wake (`wsViewOf`), reading `Storage.WritesRefused`, `Storage.PreservedTitles` and `State`'s UI prefs and help flags. A save holds `Storage.mu` and writes `state.json` with two fsyncs, so anything the loop reads must not wait on those locks:

- `Storage` publishes the two answers as an atomic view (`publishLocked`) after every change to `loadErr`, `unrecovered` or `undecodable`. A test that seeds those fields directly must publish too.
- `config.State` changes and marshals under `mu` and writes holding only `saveMu` (`update`), taken before `mu`, so saves land in order.

## Whose sessions a kill or pause closes

tmux session names are per server, not per workspace. Kill and Pause list the server once (`placeRecordSessions`) and close the agent's session and the terminal pane's shell only when each was started in the record's own home; a session started elsewhere runs on, and keeps its hooks folder, which is keyed by the name. A listing tmux does not answer refuses the kill or pause with nothing changed, and a probe whose tmux never ran (exec or fork failure, a signal) reads Unknown, never Dead (`tmuxNeverAnswered`).

Pinned by `TestKillAndPause_CloseOnlyTheirOwnTerminalShell`, `TestPause_LeavesASessionHoldingTheNameElsewhereRunning`, `TestKill_KeepsTheHooksFolderOfASessionHoldingTheName`, `TestKillAndPause_ListTmuxSessionsOnce` and `TestSessionLiveness_TmuxThatNeverRanIsUnknown`.

Fakes must list what they expect closed: a fake `tmux ls` that returns nothing says no session runs, so nothing is closed. Give a fake the listing production would have, the shell in the worktree (or, for a workspace terminal, its repository).

## Deleting the tree

`GitWorktree.Cleanup` moves a tree that `linkedWorktreeOf` vouches for from git's own files into `<configDir>/worktree-trash` and prunes its registration; core deletes it in a background job (`removeTrash`), and each load purges leftovers, queuing a job only when the trash holds something (a job queued on every load showed up in the `TestLink_…` tests, which check that nothing reaches the model). Everything else, and a rename that fails, goes to `git worktree remove -f`. Prune before `branch -D`: git refuses to delete a branch "used by worktree" while a tree gone from disk is still registered. A new session that reuses a branch an earlier session left behind marks it existing (`forNewSession`), so its kill keeps it.
