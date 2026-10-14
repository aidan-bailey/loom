# Archiving Claude Chats

**Date:** 2026-10-14
**Status:** Approved design. Brainstormed with the user as a follow-up to
`2026-10-05-worktree-locks-and-claude-temp-archive-design.md`, which left
transcripts out as a non-goal.

## Problem

Claude Code keeps each conversation in
`<claude config>/projects/<encoded-cwd>/`: `<session-uuid>.jsonl`, plus
`<session-uuid>/subagents/` and `<session-uuid>/tool-results/`, and
sometimes `memory/`. It deletes transcript files whose mtime is older than
`cleanupPeriodDays` (30 by default) whenever any Claude starts.

A loom session's cwd is its worktree, whose path ends in a fresh
nanosecond timestamp, so once a session is killed nothing ever writes to
its project dir again. The conversation disappears about a month later.

A **paused** session has the same problem. Pause leaves the transcript in
place, and nothing writes to it while paused. After 30 days Claude deletes
it, `BuildResumeCommand` (`session/agent_restart.go`) finds no file, and
Resume falls back to `--continue`. Nothing tells the user the conversation
is gone.

### Evidence (one machine, 2026-10-14)

- `~/.claude/projects` holds 3.4 GB in 330 dirs, 291 of them loom
  worktrees'. Its 528 transcripts include none older than 2026-09-13.
- Both extra accounts' `projects/` are links to `~/.claude/projects`
  (`account.Sync`), so every account's chats land in one place.
- A large worktree chat dir (`daemon`, 101 MB, 60 subagent and 11
  tool-result files) compresses to 29 MB at the fastest deflate level.

## Decisions

| Question | Decision |
|---|---|
| Goal | Outlive Claude's cleanup: keep a permanent copy of each loom session's chat. |
| Copy or move | Copy. Claude's own files are never deleted or changed by loom. |
| Which chats | Dead and paused loom worktree sessions. Not live sessions, not repo-root or non-loom chats. |
| Paused sessions | Snapshot on Pause; on Resume, put back a transcript Claude deleted before the resume command is built. |
| Layout | One zip per session at `archive/chats/<name>.zip`, `<name>` matching the scratchpad zip's. |
| Re-archiving | Each snapshot replaces the zip, carrying forward any entry whose source file is gone. |
| Retention | Manual, as for scratchpads. |
| When it runs | Pause, Kill, and a sweep in the existing Claude temp-sweep job. |

## Non-goals

- Snapshots of live sessions, including the older `/clear`ed transcripts
  in a long-running worktree's dir.
- Chats run in a repository root (workspace terminals, the user's own
  sessions) or outside loom.
- Rendering chats to markdown, or any loom UI to browse them.
- Restoring a dead session's chat so `claude --resume` finds it. Its cwd no
  longer exists.
- Archive pruning.

## Locating

The project dir of a session is, in order:

1. `filepath.Dir(transcriptPath)` when the instance recorded one from a
   hook (`Instance.ClaudeSession`). This is the dir Claude actually wrote.
2. Otherwise `claudetmp.Locate(<config>/projects, worktreePath)`. Claude
   names project dirs with the same encoding as temp dirs (every
   `[^a-zA-Z0-9]` becomes `-`, over 200 characters a prefix plus a hash),
   so `Locate`'s physical-path and as-stored handling, and its fail-closed
   ambiguity rule, apply unchanged. `<config>` is the Claude config dir
   the instance's launch env uses; an account's `projects/` link resolves
   through to the main dir.

Neither found: a no-op, so non-Claude programs and sessions that never
sent a message need no special case.

The sweep's roots are computed in `core`, which already imports `account`:
the resolved `projects/` of `account.MainDir` for the default identity,
plus each registered account's `projects/` that resolves somewhere else
(a real dir, not a link). Duplicates after `EvalSymlinks` are dropped.
`session` receives the list and never imports `account`.

## Archive layout

- Directory: `<configDir>/archive/chats/`, mode `0700`. When the global
  config.json sets `claude_tmp_archive_dir`, it is
  `<that root>/<workspace subdir>/chats/`, beside that workspace's
  scratchpad zips, so one setting moves both.
- File: `<name>.zip`, mode `0600` (tool results can hold secrets). `<name>`
  is `claudetmp.ArchiveName` of the project dir's name, the same as the
  session's scratchpad zip.
- Entries: the project dir's contents at their relative paths, plus the
  `.loom-archive.json` manifest (`claudetmp.Manifest`): `dir_name`,
  `source`, `worktree`, `reason` (`pause | kill | sweep`), `archived_at`,
  and a new `carried_forward` list of entry names kept from the previous
  zip.
- Symlinks are stored as links, sockets, FIFOs and devices skipped, and
  `CACHEDIR.TAG` dirs skipped, by the same walk `claudetmp.Archive` uses.

## Snapshot

A copy mode for `claudetmp`'s archive (shape left to the plan), used as:

```go
func Snapshot(src, zipPath string, m Manifest) (Result, bool, error)
```

1. If `zipPath` exists and no file or dir under `src` has an mtime later
   than the zip's, return without writing (`false`). Repeated sweeps cost
   one walk of `stat`s.
2. Write `<zipPath>.partial` (`O_TRUNC`, deflate at `flate.BestSpeed`) from
   a walk of `src`.
3. If the previous zip exists, copy across with `zip.Writer.Copy` (no
   recompression) every entry whose name the walk did not write, and list
   them in the manifest's `carried_forward`. A file still on disk always
   wins over its archived copy. A file Claude's cleanup deleted after the
   last snapshot is never dropped from the archive.
4. fsync, close, rename over `zipPath`, fsync the dir, and set the zip's
   mtime to the moment the walk began, so a write that landed during the
   walk still reads as newer at the next check.
5. Never touch `src`. On any error, remove the partial file, keep the
   previous zip, and return the error.

A previous zip that cannot be read is renamed to
`<name>.<UTC timestamp>.zip` (as a Pause collision is for scratchpads) and
the snapshot is written fresh, so an unreadable zip is kept, not replaced.

## Restore on Resume

`restoreClaudeChat(configDir, worktreePath, transcriptPath)` runs on both
Resume launch paths (rebuild and relaunch in place), beside
`restoreClaudeTemp` and before `finishResume`, so that
`BuildResumeCommand` sees the transcript:

- A no-op when `transcriptPath` is empty or exists, or no chat zip exists
  for the worktree (in the archive dir in effect, then the workspace's own
  folder, as `restoreClaudeTemp` looks).
- Otherwise extract every entry whose target does not exist into
  `filepath.Dir(transcriptPath)`, creating it if needed, after checking
  that its base name equals the manifest's `dir_name` (a mismatch refuses:
  the zip belongs to another dir). Never overwrite an existing
  file. Extraction goes through `os.Root`, rejecting `..`, absolute paths
  and symlink escapes, as `claudetmp.Restore` does.
- The zip stays: it is the archive, not a parked copy.
- A failure returns a `Notice`: *"couldn't restore the conversation: …;
  archive at `<path>`"*. The launch goes ahead with `--continue`, as today.

Restored files get their archived mtimes, so a transcript paused for more
than 30 days could be deleted again by a Claude starting before the agent
writes to it. Restore sets each restored file's mtime to now.

## Lifecycle wiring

**Pause** (`Instance.Pause`): snapshot with reason `pause` after the
scratchpad archive. The snapshot deletes nothing and the restore only fills
gaps, so a Resume that starts while it runs is safe. A failure is logged
(`claudechat.archive_failed`) and Pause succeeds.

**Kill** (`Instance.Kill`): snapshot with reason `kill` beside the
scratchpad archive: after everything else is gone, never for a workspace
terminal (`isWorkspaceTerm`). Killing a paused session snapshots too; an
unchanged chat costs a stat walk. A failure is logged and Kill succeeds;
the next sweep retries.

**Sweep** (`session.SweepClaudeChats`): run by `claudeTmpSweepJob` after
`SweepClaudeTemp` for the same workspace, so it shares `gateClaudeTmp` and
its one-in-flight rule. `requestClaudeTmpSweep` also snapshots, on the
model's goroutine, the worktree paths of the workspace's instances that
are not Paused, and the projects roots. Under each root, a dir is
snapshotted (reason `sweep`) when all of these hold:

1. Its name starts with the workspace's encoded worktrees prefix and with
   no other known config dir's (`foreignWorktreePrefixes`).
2. Its last `-` segment passes `looksLikeTimestampSuffix` and the name is
   not truncated.
3. No non-Paused instance's worktree encodes to it. Paused instances,
   preserved records and orphans all qualify; this covers sessions paused
   before the feature shipped.
4. It is newer than its archive (Snapshot's step 1).

There is no on-disk recheck and no quiet period: nothing is deleted, and
snapshotting a dir a live session is writing costs a zip at worst.

The first sweep of each workspace copies its backlog: every dead worktree
chat Claude has not yet deleted.

## Logging and visibility

- `claudechat.archived` (info): source, zip, reason, bytes in and out,
  entries carried forward, the chats dir's total size.
- `claudechat.archive_failed`, `claudechat.restore_failed` (warn);
  `claudechat.restored` (info); `claudechat.sweep_done` (info, count).
- `loom debug` prints the chats archive dir beside the temp one.

## Documentation

- `session/CLAUDE.md`: a rule that loom copies chats and never moves or
  edits Claude's files, carries forward vanished entries, and restores a
  deleted transcript before the resume command is built.
- `docs/ARCHITECTURE.md`: `archive/chats/` under Persistent State.
- `USAGE.md`: where chat archives live, that they are never pruned, and how
  to read one (`unzip -p <zip> <uuid>.jsonl`, `zipgrep`).

## Testing

**Isolation.** `internal/testenv.IsolateLoomDirs` already points
`CLAUDE_CONFIG_DIR` at a throwaway dir, so no test reaches the developer's
`~/.claude/projects`.

**`session/claudetmp`** (unit):

- Snapshot keeps the source byte for byte.
- An entry whose source vanished is carried forward; a file still on disk
  replaces its archived copy.
- An unchanged source writes nothing; a newer file anywhere in the tree
  triggers a write.
- An injected write failure leaves the previous zip intact and no
  `.partial`.
- An unreadable previous zip is demoted, not replaced.
- The fill-in restore never overwrites, creates a missing dir, sets mtimes
  to now, and rejects `../x`, absolute paths and a symlink followed by a
  file beneath it.

**`session`** (mock tmux, temp config dir):

- Pause and Kill snapshot the dir named by `transcriptPath`, and the one
  `Locate` finds when it is unset.
- A workspace terminal's Kill snapshots nothing.
- Resume after the transcript was deleted restores it and
  `BuildResumeCommand` chooses `--resume`; with no zip it still chooses
  `--continue`.
- A restore failure yields a `Notice` and the launch proceeds.
- An archive failure fails neither Pause nor Kill.
- The sweep snapshots dead and paused dirs, and skips running,
  foreign-prefix, look-alike, truncated and unchanged ones.

**`core`**: the chat sweep runs after the temp sweep for each queued
workspace, with the non-Paused set and the roots snapshotted at request
time; an account whose `projects/` is a link adds no second root.

**Contract** (opt-in, `LOOM_TEST_REAL_CLAUDE=1`): extend
`TestRealClaude_TempDirLayout` to assert that `Locate` on
`<CLAUDE_CONFIG_DIR>/projects` finds the dir that the real transcript
(from the hook's `transcript_path`) sits in, under the same symlinked
parent.

## Work packages

1. **`claudetmp` snapshot and fill-in restore**, with their unit tests.
2. **`session` wiring**: Pause, Kill, Resume, `SweepClaudeChats`, logging,
   the contract test extension.
3. **`core` and docs**: the sweep job and its snapshot, the roots,
   `loom debug`, `session/CLAUDE.md`, `ARCHITECTURE.md`, `USAGE.md`.

## Risks

- **Claude's projects layout is undocumented.** The contract test catches
  drift; until it runs, a change makes loom find nothing (a no-op).
- **Archives grow without bound.** A session's chat costs about a third of
  its size; the dir's total is logged on every archive.
- **Pause and Kill get slower**, by about a second per 100 MB of chat. Both
  already run off the model's goroutine with a transitional status.
- **A torn last line.** A sweep can snapshot a dir mid-write by a session
  it cannot see; the next snapshot replaces that copy. Pause and Kill
  snapshot only after tmux is closed, so the copy a Resume restores from
  is whole.
