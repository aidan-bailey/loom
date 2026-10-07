# Locked Worktrees and Claude Temp-Dir Archiving

**Date:** 2026-10-05
**Status:** Approved design. Brainstormed with the user from a bug report
(below).

## Problem

Loom removes a session's git worktree on kill and pause, and auto-cleans
orphaned worktrees at every workspace load. Two kinds of leftovers survive
indefinitely.

1. **Locked orphan worktrees are never removed.** The orphan sweep fails
   on any worktree carrying a git lock, logs a warning, and fails again on
   every later start.
2. **Claude Code's per-session temp dir is never removed.** Claude writes
   `<tmp>/claude-<uid>/<encoded-cwd>/<session-uuid>/{scratchpad,tasks}/`
   for every session. Loom removes the worktree; nothing removes this dir.

### Evidence (one machine, 2026-10-05)

- `/tmp/claude-1000` holds 9.8 GB, and `/tmp` is **tmpfs**, so this is RAM
  and swap, not disk. For the kermit repository alone, 27 of its 33 temp
  dirs belong to worktrees loom has already removed: about 6.1 GB.
- About 4.3 GB of the root (45%) sits in 14 directories tagged with
  `CACHEDIR.TAG` (cargo `target/`). The rest is copied release binaries
  (319 MB each), benchmark outputs (223 MB each), AppImages, cloned repos,
  and small notes, scripts and task logs.
- `git worktree list` in kermit still shows
  `.loom/worktrees/aidanb/lubm-benchmark_18ac538533d14b9d … locked`
  (reason `initializing`). The branch is merged and the tree clean.
  `loom.log.1` shows the sweep failing on it at every start from
  2026-09-14 to 2026-09-17:

  ```
  level=WARN msg=orphan_autoclean_failed subsystem=app
  worktree=/home/aidanb/Source/Academia/kermit/.loom/worktrees/aidanb/lubm-benchmark_18ac538533d14b9d
  err="remove worktree …: exit status 128 (fatal: cannot remove a locked working tree,
  lock reason: initializing\nuse 'remove -f -f' to override or unlock first)"
  ```

### Causes

**Locks.** `RemoveOrphanWorktree` (`session/orphan.go`) runs
`git worktree remove -f`, and a single `-f` does not override a lock. So
do `GitWorktree.cleanup()` (Kill) and `GitWorktree.Remove()` (Pause),
through `removeWorktree()` (`session/git/worktree_git.go`). The
`initializing` lock is git's own: `git worktree add` writes it before the
checkout and removes it at the end, so an `add` killed mid-way (before
loom gave `add` its own 5-minute deadline, the 8 s tick budget killed it)
leaves it forever. `GitWorktree.unlockWorktree()` exists, but only
`clearWorktreePath` calls it, and only when nothing live is left at the
path.

**Temp dirs.** Nothing in loom references Claude's temp root. Read from the
Claude Code 2.1.288 bundle:

- Root: `realpath(join($CLAUDE_CODE_TMPDIR || os.tmpdir(), "claude-<uid>"))`.
  Bun's `os.tmpdir()` reads `$TMPDIR`, `$TMP`, `$TEMP`, then `/tmp`.
- Project dir: the cwd with every `[^a-zA-Z0-9]` replaced by `-`. A result
  longer than 200 characters becomes `first200 + "-" + base36(hash(cwd))`.
- The scratchpad is `join(projectDir, sessionId, "scratchpad")`.
- The cwd is the **physical** path. The report's log names the worktree
  `/home/aidanb/Source/Academia/kermit/.loom/...`, but Claude's dir is
  `-tb-Source-Academia-kermit--loom-...`, because `~/Source` is a symlink
  to `/tb/Source`. Encoding the path loom stored would never find it.

## Decisions

| Question | Decision |
|---|---|
| Which locks to override | Only git's own `initializing` lock, and only once older than the `add` deadline. Every other lock is respected. |
| Delete or keep temp dirs | Keep: zip into an archive on disk, then delete the source. |
| Archive contents | Everything except directories tagged with a valid `CACHEDIR.TAG`. |
| Retention | Manual. Loom never deletes an archive; it logs the archive dir's size. |
| Paused sessions | Archive on Pause, restore on Resume. |
| Where the work runs | Inside Pause and Kill (their existing Cmds), plus a background sweep at workspace load for orphans and the backlog. |

## Non-goals

- Claude's transcript dirs (`<config>/projects/<encoded-cwd>/`). They live
  on disk, hold conversation history and back `--resume` across accounts.
- Files agents wrote straight into the temp root (`admin_build.log`,
  `acct-fixed.ts`, …). Nothing ties them to a worktree.
- Temp dirs of cwds outside loom's worktrees, including workspace
  terminals: they run in the repository root, which the user's own Claude
  sessions share.
- A `CLAUDE_CODE_TMPDIR` or `TMPDIR` set only inside a profile's program
  string. Loom computes the root from its own environment.
- Archive pruning.

## Part 1: Locked worktrees

### Helper

A new helper in `session/git`, shaped roughly as:

```go
type lockState int // notLocked, unlocked, kept

func unlockStaleInit(repoPath, worktreePath string, r CommandRunner) (state lockState, reason string, err error)
```

1. If the tree has no `.git`, report `notLocked` and do nothing. Gutted
   trees keep their current handling.
2. Find the admin dir with `git -C <wt> rev-parse --absolute-git-dir`
   (through `internalexec.GitCommand`).
3. Read `<admindir>/locked`:
   - Absent: `notLocked`.
   - Content (trimmed) is `initializing` and the file's mtime is older than
     `gitWorktreeAddTimeout` plus one minute: run `git worktree unlock
     <path>`, report `unlocked`. Loom kills its own `add` at that deadline,
     so a lock this old cannot belong to a running `add`. Loom's git runs
     with `LC_MESSAGES=C`, so its own lock text is always English.
   - Anything else (a recent `initializing` lock, or a user's
     `git worktree lock --reason …`): report `kept` with the reason.

### Callers

Only the three paths that intend to delete a tree call it, before their
`remove -f`:

- **`RemoveOrphanWorktree`** (orphan auto-clean). On `kept`, skip the
  removal and return a sentinel `ErrWorktreeLocked` wrapping the reason.
  `reconcileOrphans` logs it at debug (replacing the warning printed at
  every start) and does not count the tree as cleaned.
- **`GitWorktree.cleanup()`** (Kill) and **`GitWorktree.Remove()`**
  (Pause). On `kept`, return an error naming the remedy: *"worktree
  `<path>` is locked (reason: X); run `git worktree unlock <path>` to let
  loom remove it"*.

**Unchanged:** `removeWorktree()` and `clearWorktreePath`. The Setup and
rebuild path keeps its `nothingLiveAt` rule. An intact tree must never
reach it, and if one does, the lock is what stops `-f` from deleting it.
Putting the unlock inside the shared `removeWorktree()` would remove that
protection.

With this in place, the kermit `lubm-benchmark` tree (locked since about
2026-09-14) is unlocked and removed at the next start, keeping its branch.

## Part 2: Claude temp-dir archiving

### Package `session/claudetmp`

No tmux, UI or app dependencies, like `session/hooks`.

**Locating**

- `Root() (string, bool)`: the first of `$CLAUDE_CODE_TMPDIR`, `$TMPDIR`,
  `$TMP`, `$TEMP` that is set, else `/tmp`; joined with
  `claude-<uid>`; symlinks resolved. When the root does not exist, every
  operation is a no-op.
- `DirName(physicalPath string) (name string, prefixOnly bool)`: the
  encoding above. Over 200 characters it returns `first200 + "-"` and
  `prefixOnly`, since loom cannot compute Claude's hash.
- `Locate(root, worktreePath string) (dir string, ok bool)`: resolves the
  deepest existing ancestor of `worktreePath` with `filepath.EvalSymlinks`
  and appends the remaining components, because the worktree itself is
  usually gone by the time loom looks. It encodes that physical path, and
  also the path exactly as stored. An exact name must exist; a prefix must
  match exactly one directory. Two or more matches are ambiguous, and
  `Locate` reports not found (fails closed).

**Archive layout**

- Directory: `<configDir>/archive/claude-tmp/`, mode `0700`.
- File: `<name>.zip`, mode `0600` (scratchpads can hold secrets).
  `<name>` is the encoded dir name with the encoded
  `<configDir>/worktrees/` prefix removed, e.g.
  `aidanb-67-18daaecd490cb896.zip`. A leading `-` would read as a flag to
  `unzip`.
- Entries: `<session-uuid>/scratchpad/…`, `<session-uuid>/tasks/…`, plus a
  manifest `.loom-archive.json`:

  ```json
  {
    "dir_name": "-tb-Source-Academia-kermit--loom-worktrees-aidanb-67-18daaecd490cb896",
    "source": "/tmp/claude-1000/-tb-Source-…-18daaecd490cb896",
    "worktree": "/home/aidanb/Source/Academia/kermit/.loom/worktrees/aidanb/67_18daaecd490cb896",
    "reason": "pause | kill | sweep",
    "archived_at": "2026-10-05T13:29:00Z",
    "skipped_caches": [{"path": "6ac833f1-…/scratchpad/gallop/target", "bytes": 912345678}]
  }
  ```

**`Archive(src, zipPath string, m Manifest) error`**

- Walks with `filepath.WalkDir`, never following symlinks. A symlink is
  stored as a symlink entry; following one could archive `$HOME`.
- Skips sockets, FIFOs and devices.
- Skips any directory holding a `CACHEDIR.TAG` whose first bytes are the
  spec's signature (`Signature: 8a477f597d28d172789f06886806bc55`). A
  malformed tag is ordinary content.
- Compresses with deflate at `flate.BestSpeed`, writing to
  `<zipPath>.partial` opened with `O_TRUNC`, so a leftover from a crash is
  overwritten. On success it fsyncs, closes and renames to `zipPath`.
- Deletes the source **only after the rename**, with `os.RemoveAll`,
  retrying after `chmod u+w` on directories when that fails (Go module
  caches are read-only).
- On any error it removes the partial file, leaves the source untouched,
  and returns the error.

**`Restore(zipPath, root string) error`**

- Reads the manifest and recreates the directory under the manifest's
  exact `dir_name`, which covers the over-200 case without the hash.
- Refuses when that directory already exists.
- Extracts into a temporary sibling through `os.Root` (Go 1.24+;
  `Root.Symlink` since 1.25), which rejects `..`, absolute paths and
  escapes through symlinks. Restores file modes and symlinks.
- Renames the sibling into place, then deletes the zip.

**File-name lifecycle.** `<name>.zip` is the only file Resume restores.
When Pause finds an existing `<name>.zip` (a parked zip whose restore
failed earlier), it renames it to `<name>.<UTC timestamp>.zip`, which is
then a permanent archive, before writing the new one. A parked zip whose
session is killed while paused simply stays as that session's archive.
Nothing is recorded on the instance, so there is no schema change.

### Lifecycle wiring

**Pause** (`Instance.Pause`): tmux closes, the worktree is removed, the
Paused checkpoint is saved, and then `Locate` + `Archive` run with reason
`pause`. Archiving after the checkpoint means a crash during the slow step
leaves the instance Paused, with its temp dir either intact or zipped. A
failure is logged as a warning and Pause still succeeds; the dir stays in
place and Resume finds it there.

**Kill** (`Instance.Kill`): after `Cleanup()` succeeds, archive with reason
`kill`. When `Cleanup` fails, the instance is restored for a retry and is
still claimed, so nothing is archived. A Paused session's dir is already
gone, so its parked zip stays as the archive. Workspace terminals are
skipped. A failure is logged as a warning and Kill still succeeds; the dir
is now unclaimed and the next sweep retries it.

**Resume** (`Instance.Resume`): both launch paths, rebuild and
relaunch-in-place, restore `<name>.zip` before the agent launches, when
the zip exists and the temp dir does not. Reattaching to a live session
skips it, since Claude is already running. A failed restore becomes a
`session.Notice`, surfaced through `resumeDoneMsg.notice`: *"couldn't
restore Claude's scratchpad: …; archive kept at `<path>`"*. The launch
proceeds.

All three no-op when the root or the dir is absent, so non-Claude
programs need no special case.

**Sweep**: `session.SweepClaudeTemp(cfgDir string, claimed map[string]bool)`.

- Dispatched from `app` as a gated Cmd (a new gate kind) after
  `reconcileOrphans` on every workspace-load path, so it runs after the
  orphan auto-clean has removed its worktrees. The claim set is
  `claimedWorktreePaths` for the loaded slot (list plus preserved
  records), snapshotted on Update. At most one sweep runs at a time; a
  load during a run queues one more pass (`pollGate.request()`).
- A directory under the root is archived (reason `sweep`) and deleted only
  when all of these hold:
  1. Its name starts with the encoded `<cfgDir>/worktrees/` prefix, in
     either the resolved or the as-stored form.
  2. Its last `-`-separated segment passes `looksLikeTimestampSuffix`,
     which excludes look-alikes such as `.loom/worktrees-backup/`.
  3. No claimed path encodes to it.
  4. Checked again **immediately before archiving it**: no worktree
     directory on disk under `<cfgDir>/worktrees/` encodes to it.
- Why it cannot take a live session's dir: a worktree exists before its
  Claude creates the temp dir, so a live session fails check 4 even when
  another loom process started it after the snapshot. Paused sessions are
  claimed. Worktree paths end in a fresh nanosecond timestamp, so no new
  session can reuse an archived name.
- The first sweep of each workspace clears the existing backlog.

### Logging and visibility

- `claudetmp.archived` (info): source, zip path, reason, bytes in and out,
  cache bytes skipped, the archive dir's total size.
- `claudetmp.archive_failed` and `claudetmp.restore_failed` (warn).
- `loom debug` prints the archive directory.

### Documentation

- CLAUDE.md: a gotcha bullet covering physical-path encoding, the claim
  rules, archive-after-checkpoint and restore-before-launch; and an
  `archive/claude-tmp/` entry under Persistent State.
- USAGE.md: where archives live, what they omit (cache-tagged dirs), and
  that pruning them is manual.

## Testing

**Isolation.** `internal/testenv.IsolateLoomDirs` also points
`CLAUDE_CODE_TMPDIR` at a throwaway directory, so no test can sweep the
developer's real Claude temp root (which holds live sessions'
scratchpads). `TestEveryConfigReachingPackageIsolatesLoomDirs` then covers
it for every package.

**`session/git`** (real git fixtures): a stale `initializing` lock lets
Kill, Pause and `RemoveOrphanWorktree` succeed; a fresh `initializing`
lock or a user reason keeps the tree, and Kill and Pause return the
actionable error; an unlocked tree behaves as before; a gutted tree gets
no unlock attempt; an intact locked tree passed to `clearWorktreePath` is
still refused.

**`session/claudetmp`** (unit):

- `DirName` on the report's example:
  `/tb/Source/Academia/kermit/.loom/worktrees/aidanb/tmp-analysis_18db9e2ddd36ba14`
  → `-tb-Source-Academia-kermit--loom-worktrees-aidanb-tmp-analysis-18db9e2ddd36ba14`.
- Over 200 characters: prefix only; a unique match is found, an ambiguous
  one is not.
- `Root` precedence across the four variables.
- `Locate` through a symlinked parent after the leaf is deleted.
- `Archive`: skips a validly tagged cache dir but keeps a malformed tag;
  stores a symlink to an outside directory as a link; skips a FIFO;
  deletes a tree with read-only directories; after an injected write
  failure leaves the source intact with no `.zip` or `.partial`.
- `Restore`: round-trips content, modes and symlinks; uses the manifest's
  long name; refuses an existing target; rejects hand-built zips with
  `../x`, an absolute path, and a symlink followed by a file beneath it.

**`session`** (mock tmux, temp root via the env var): Pause archives and
Resume restores; Kill of a Paused session keeps its zip; a Pause
collision demotes the old zip; a restore failure yields a `Notice` and the
launch proceeds; an archive failure fails neither Pause nor Kill;
workspace terminals are skipped; the sweep keeps claimed, on-disk,
look-alike and other-workspace dirs, archives an unclaimed one, and
matches both path forms.

**`app`**: the sweep is dispatched after `reconcileOrphans` on every load
path, with at most one in flight and a load during a run queuing one more
pass.

**Contract test** (opt-in, `LOOM_TEST_REAL_CLAUDE=1`, a few cents): run
`claude -p` on Haiku in a cwd under a symlinked parent, prompting it to
write a file into its scratchpad so the directory certainly exists, and
assert that `Locate` finds it. It fails if a later Claude release changes
the root or the encoding.

## Work packages

1. **Locks:** `unlockStaleInit`, its three call sites, the
   `reconcileOrphans` count fix, tests.
2. **`claudetmp`:** the package, the testenv isolation, the contract test.
3. **Wiring:** Pause, Kill, Resume, the sweep and its gate, `loom debug`,
   docs.

## Risks

- **Claude's layout is undocumented.** The root and encoding were read
  from a minified bundle. The contract test catches drift; until it runs,
  a change makes loom find nothing, which is safe (no-op), not
  destructive.
- **Pause and Kill get slower** by the time to compress non-cache content:
  seconds for hundreds of megabytes. Both already run off the Update
  goroutine with a transitional status.
- **Restored sessions lose their build caches.** A resumed session
  rebuilds its cache-tagged directories.
- **A Claude process outliving its tmux session** could write after the
  archive walk; those writes are lost with the source. Pause and Kill
  close tmux first, so this needs a process that ignores SIGHUP.
