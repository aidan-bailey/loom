# Build sweep: delete session worktrees' unused Rust binaries

Status: design approved 2026-11-03; spike done (§9).

## 1. Problem

Every loom session builds in its own worktree, so every Rust session grows its own `target/`. Measured on 2026-11-03: 22 session worktrees across 13 repos held 447G, 418G of it in `target/`, all in sessions active within two days; `/tb` was 99% full. Older and paused worktrees held 0–0.8G each.

The waste is inside live sessions. In polyoxide's `wasm` session, `target/debug/deps` held 131G, 122G of it executables, and 98G of those had a newer build of the same binary beside them: cargo writes a new test binary under a new hash after every change and never deletes the old one. prader-rs's `nautilus-worker` showed the same shape (8.4G of 11.5G).

## 2. Decisions

- **Which sessions:** live sessions whose agent is idle, and paused ones (option (b) of the brainstorm). The per-file rule below supplies "for a while", so a session's own idle time is not checked.
- **What "unused" means:** the file's last access time. Its filesystem must record access times: `/tb` moves from `noatime` to `strictatime,lazytime` (§3), and the threshold is in hours.
- **Only executables.** Libraries, proc-macros, build scripts and the incremental cache stay (§4.3).
- **Off unless configured,** by one global setting (§5).
- **A manual command** with a dry run (§6).
- Rejected: running `cargo-sweep` (it deletes whole build units, libraries and proc-macros included, which sets off rebuild chains, and its `--time` counts whole days); a standalone command on a systemd timer (it would still ask the daemon for statuses, and adds a timer to install).

## 3. Prerequisite outside loom

The user changes `/tb`'s mount options in `~/System` from `noatime` to `strictatime,lazytime`, keeping `commit=60`. `strictatime` records every access; `lazytime` keeps timestamp updates in memory and writes them out in batches, and `stat` sees them at once. `relatime` is not enough for hour thresholds: the kernel refreshes its access times at most once a day.

## 4. Behaviour

### 4.1 When

A gated job (`gateBuildSweep`, interval 30 minutes) dispatched from the model's tick, as the Claude temp-dir sweep is (`gateClaudeTmp`, `core/claude_tmp.go`). Its builder returns nil, arming nothing, when the setting is off or no session is eligible. One sweep runs at a time across the periodic job and the manual command (a mutex in the sweep package).

### 4.2 Where

- **Eligible sessions:** status `Ready` or `Paused`, in a workspace the daemon has loaded. Never `Running`, `Loading`, `Prompting`, `Deleting`, `Recoverable`, or a workspace terminal (it runs in the main checkout, not a loom worktree).
- **Target dir:** `<worktree>/target` only, and only when it holds `CACHEDIR.TAG` or `.rustc_info.json`. (Amended 2026-11-04: cargo writes the tag only into a target dir it creates, so worktrees whose agents wrote notes into `target/` before the first build went unswept.) A `CARGO_TARGET_DIR` elsewhere is never touched. Symlinks are never followed.
- **Profile dirs:** every directory under the target dir, up to six levels deep, that holds both `deps/` and `.fingerprint/`. This covers `debug/`, `release/`, `<triple>/<profile>/`, and the nested target dirs tools create (`target/semver-checks/…`). The walk never descends into `deps/`, `build/`, `incremental/` or `.fingerprint/`.

### 4.3 What

- **Candidates:** regular files directly in a profile dir's `deps/` or `examples/` whose name matches `^[A-Za-z0-9_]+-[0-9a-f]{16}$` (no extension) and whose owner-execute bit is set.
- **Unused:** `max(atime, mtime)` is older than the threshold.
- **Hard links:** cargo links each built program and example into the profile dir under its plain name (`target/debug/<name>`, `target/debug/examples/<name>`). A candidate's other names in the profile dir itself or its `examples/` are found by inode and deleted with it. When the inode's link count exceeds the names found there, the file is skipped: something outside the profile dir holds it, and deleting would free nothing.
- **Never deleted:** `.rlib`, `.rmeta`, `.so`, `.d` and any file with an extension; anything under `build/`, `incremental/` or `.fingerprint/`; the fingerprints of deleted binaries (cargo rebuilds a unit whose output is missing).

### 4.4 Safety checks, all fail-closed

- **Access times recorded:** the job reads `/proc/self/mountinfo` and finds the mount holding each worktree (the longest mount-point prefix, with `\040`-style escapes decoded).
  - `noatime`: the mount is skipped.
  - `relatime`: the mount is swept only when the threshold is at least 24 hours.
  - Neither flag: the mount records every access (`strictatime`), and is swept.
  - An unreadable mountinfo, or any OS but Linux: everything is skipped.
- **Not building:** before touching a target dir, the job takes every cargo lock file present in each profile dir (`.cargo-lock`, `.cargo-build-lock`, `.cargo-artifact-lock`) with a non-blocking exclusive `flock`, and holds them while it deletes. If any is held, the target dir is skipped this round. A cargo started during a sweep waits a few seconds on the lock.
- **Not testing:** `cargo test` releases its locks before it runs the test binaries (§9). A binary cargo found up to date but hasn't run yet would look unused. So a worktree is skipped while any process of the daemon's user named `cargo` or `cargo-*` (`/proc/<pid>/comm`) has its working directory inside it. A process that exits mid-scan is ignored. One of the user's own processes whose working directory can't be read counts as busy.
- **Status at dispatch:** the builder captures each session's status and worktree. The job doesn't recheck status, since the lock and the per-file access time cover a session that started working since then.
- **Errors:** a file that fails to delete is logged and skipped. A target dir that can't be walked is skipped with its reason. Nothing is retried within a round.

### 4.5 Reporting

- Every round logs one `build_sweep.swept` record per swept session, through `log.For("build_sweep")`, with the files deleted and bytes freed. Skips are logged with a reason (`status`, `building`, `testing`, `atime_off`, `no_target`, `error`).
- An `atime_off` skip also raises a `Notice` once per mount per daemon run, naming the mount and its option. A sweep that silently stops is the failure this guards against.
- Freed space isn't shown in the TUI or the phone app.

## 5. Configuration

`build_sweep_after_hours` (integer) in the global `config.json` (`~/.loom/config.json`, `config.Settings`). Absent or 0 means off. It is global because disk space is machine-wide and one daemon serves every workspace. The job reads it at each dispatch through `config.LoadConfigFromGlobal`, so an edit takes effect within one interval without restarting the daemon. The user documentation (`USAGE.md`) gains the key.

## 6. Manual command

`loom sweep [--dry-run] [--after <hours>] [-v]`

- Asks the running daemon (`withDaemon`, as `loom session` does) to sweep now with the same rules. `--dry-run` deletes nothing and reports what would go.
- `--after` overrides the setting. With neither set, the command fails, saying to set `build_sweep_after_hours` or pass `--after`. This lets a dry run try thresholds before the sweep is turned on.
- Prints one line per session (workspace, title, status, files, bytes, or the skip reason) and a total. `-v` also lists each file, as a path relative to the worktree, with its size and time since last use.
- Wire: a new `core.Core` method, `SweepBuilds(req ReqID, opts BuildSweepOptions)`, where `BuildSweepOptions{DryRun bool; AfterHours int}`. The job is `track`ed, and the `Reply` gains a `Sweep` field carrying the report. Adding a method and a field is wire-compatible, so `rpc.Protocol` stays. The procedure in `docs/claude/adding-a-core-method.md` applies.

## 7. Components

| Unit | Holds | Depends on |
|---|---|---|
| `session/buildsweep` (new) | `Plan(worktree, threshold, now)` returning candidates and skip reasons; `Sweep(plan, dryRun)` returning a report; the mountinfo parser (`mount_linux.go`, with `mount_other.go` reporting unsupported); the `flock` helper (`lock_unix.go`, `lock_windows.go` reporting unsupported); the cargo-process check (`proc_linux.go`, with `proc_other.go` reporting unsupported); the one-sweep mutex | the filesystem only |
| `core/build_sweep.go` (new) | `gateBuildSweep` and its interval; `maybeBuildSweep` (eligibility, setting); the job; the `Deliver` case; the once-per-mount notice; `SweepBuilds` | `session/buildsweep`, `config` |
| `config/settings.go` | `BuildSweepAfterHours` | — |
| `internal/cli/sweep.go` (new) | `loom sweep` | `core.Core` over the daemon link |

No persisted record changes, so `CurrentSchemaVersion` stays.

## 8. Testing

- **`session/buildsweep`, synthetic trees** in temp dirs with `os.Chtimes`-set times:
  - The candidate pattern, both sides: a hashed executable is taken; a `.so`, an `.rlib`, a non-executable file, a `build/` script and an `incremental/` file are left.
  - The threshold at its edge.
  - Hard links: an uplifted name is deleted with its deps copy, and a link held outside the profile dir skips the file.
  - Profile-dir discovery: a triple dir and a nested tool target dir are found; symlinks are not followed.
  - `CACHEDIR.TAG` or `.rustc_info.json` required.
- **Production geometry:** one test builds a two-binary crate with real cargo, changes it, rebuilds, sweeps with a zero threshold after aging the old binary, and checks that the old binary is gone, the current one stays, and `cargo test` still passes. It skips when `cargo` is absent, as in the Nix sandbox.
- **Mountinfo parser:** a table of real lines covering `noatime`, `relatime`, neither flag, nested mounts with the longest prefix winning, and escaped spaces.
- **Lock:** the test holds the `flock` and sees the target dir skipped as `building`; once released, it is swept.
- **Cargo process:** the process scan reads a fake `/proc` root: a `cargo` process with its working directory inside the worktree marks it `testing`, one outside doesn't, a vanished pid is ignored, and another user's process is not read.
- **Model:**
  - Eligibility, both sides: `Ready` and `Paused` swept; `Running`, `Loading`, `Prompting`, `Deleting`, `Recoverable` and a workspace terminal not.
  - A setting of 0 arms nothing.
  - Add the kind to `TestGateIntervalsUseEachJobsInterval`, `TestProductionGatedJobsYieldOneResult` and `TestGatedDeliveryDisarmsFirst`.
  - The notice raised once per mount.
- **Wire and CLI:** `SweepBuilds` names no instance, so it runs no `admit` and needs no case in `TestRequests_RefuseWhatTheTUIRefuses`; regenerate `core/rpc` and the protocol reference; a CLI test covering output, the missing-threshold error and `--dry-run` deleting nothing.
- **Isolation and platforms:** tests reach config through `internal/testenv.IsolateLoomDirs`. After the platform files, run `GOOS=windows CGO_ENABLED=0 go build ./...`.

## 9. Spike results (2026-11-03, cargo 1.99.1)

1. **Locks.** While compiling (a slow `build.rs`), `/proc/locks` shows `.cargo-build-lock` and `.cargo-artifact-lock` held `FLOCK/WRITE` and `.cargo-lock` held `FLOCK/READ`. While the test binaries run, all three are free. The lock therefore guards compiles only, and the cargo-process check (§4.4) covers the test run.
2. **Access times.** On a tmpfs mounted `strictatime,lazytime` in a user namespace, a file's atime set to 2026-01-01 jumped to the present at once when the test binary was exec'd, and the rlib's did when the test was relinked. Its `mountinfo` per-mount options read `rw,lazytime`: no atime flag. Re-check on `/tb` with `findmnt -T /tb` after the remount.
3. **Lock kind.** Cargo's locks are `flock`s (`FLOCK` in `/proc/locks`), so `syscall.Flock` conflicts with them.

## 10. Known limits

- Only the worktrees of sessions loom has a record of are swept. The daemon loads every registered workspace at boot, so a paused session in an unopened workspace is covered; an orphan worktree (`Recoverable`) is not.
- Libraries and the incremental cache still grow. Per-repo profile settings (`[profile.dev] debug = "line-tables-only"`, merging integration-test files into one binary) shrink every binary and are the repos' business, not loom's.
- Linux only. Elsewhere the sweep skips everything and raises the notice.
