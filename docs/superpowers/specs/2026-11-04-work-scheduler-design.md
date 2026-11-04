# Work scheduler: a per-host queue for sessions' heavy work

Status: design approved 2026-11-04.

## 1. Problem

The user runs many loom sessions at once on one host (fresco: 16 cores, 30 GB of RAM). Each agent builds, tests and benchmarks in its own worktree whenever it likes, so several cargo builds, test suites and links run at once. Three things go wrong: memory runs out (swap, freezes, the OOM killer), the CPU is oversubscribed so every session and the TUI crawl, and concurrent builds contend for the disk.

A session should be able to apply to have work done (compiling, a benchmark, a test run): give a description of the job, have it queued and scheduled against the host's resource caps, be told how it went, and see its output if it wants. Work that fits runs concurrently. The user can switch to a view of the schedule, and set the caps per host. A working queue is the goal; smarter load balancing, possibly agent-assisted, comes later.

## 2. Decisions

- **The daemon runs the job** (brainstorm option "daemon runs it"). A session submits a command; the daemon starts it in the session's cwd with the submitter's environment, logs its output, and tells the session when it ends. Rejected: a lease, where the submitting shell runs the command itself once admitted (simpler, but jobs could not outlive the agent's call or later run elsewhere).
- **Agents apply explicitly**, through the `loom job` CLI, which loom's injected Claude context tells them to use. Nothing rewrites an agent's commands. Rejected for now: the loom mod's Bash hook routing matching commands automatically (§13).
- **Jobs claim resources, and admission is the only control** (option "claims, admission only"). Per-host caps on CPU cores, memory and I/O-heavy jobs; each job claims a share; a job starts when its claim fits what is left. The CPU claim becomes the job's parallelism (§5.1); the memory claim is an estimate, shown beside the measured peak. Rejected: confining jobs to their claims with cgroups (deferred, §13); a bare concurrency cap (it can't tell a 2 GB test from a 16 GB link).
- **A detached shim hosts each running job** (option "detached shim"): a hidden `loom job-shim` process per job, in its own session, so jobs survive daemon restarts and upgrades as agents do. Rejected: a tmux window per job (the orphan sweep and reconcile act on loom's tmux sessions, logs would carry escape codes, exit status needs `remain-on-exit`); jobs as the daemon's own children, re-queued after a restart (every loom upgrade would restart running builds).
- **First fit, with a starvation guard** (§5.2).
- **Pausing or killing a session cancels its jobs** (§5.7).
- **The session hears only of a job's end** (§5.8). A notice at its start would wake the agent for nothing.

## 3. Scope

In: the queue in the daemon's model, persisted under the global dir; the shim; the `loom job` CLI; completion notices through the loom mod; the work view in the TUI, with a caps form and badges on session cards; the `scheduler` block in the global `config.json`; a paragraph in loom's Claude context; docs.

Out (§13): automatic routing, confinement, learned claims and agentic balancing, priorities, the phone view, cross-host scheduling, Windows.

## 4. Concepts and names

- **Job:** one command a session (or a person) submitted. It has an ID from a per-host counter, shown as "job 14" and never reused.
- **Owner:** the session that submitted it, resolved from `LOOM_LAUNCH` (§6.2), or none for a job submitted from a plain shell.
- **Claim:** what the job asks for: CPU cores and memory in whole GB, whether it is I/O-heavy, or `exclusive` (every cap).
- **Caps:** the host's totals that claims are admitted against (§5.1).
- **States:** `queued` → `running` → `done` | `failed` | `cancelled` | `lost`. `running` covers the start handshake (§5.3). `done` is exit 0; `failed` is a non-zero exit, a signal, or a start that failed; `cancelled` is a job cancelled by anyone, whatever its exit; `lost` is a job whose shim vanished without writing its result (a reboot, a SIGKILL from outside).
- **Code names:** the scheduler's own package is `sched` (`sched.JobID`, `sched.Claim`, `sched.Caps`, `sched.Record`). In `core`, `Job` already names the model's blocking work (`func() any`), so the model's side is named for the queue: `QueueView`, `QueueChanged`, and the requests `SubmitJob`, `CancelJob`, `MoveJobToFront`, `NotifyJob` and `SetSchedulerCaps`.

## 5. Behaviour

### 5.1 Caps, claims and hints

The caps live in a `scheduler` block of the global `config.json` (`~/.loom/config.json`, which is per host). A workspace's `config.json` never holds them, and the `S` settings overlay doesn't show them (it edits a workspace's config); they are edited in the work view's caps form (§7.2) or by hand.

```json
"scheduler": {
  "cpus": 14,
  "memory_gb": 24,
  "io_jobs": 2,
  "default_claim": { "cpus": 4, "memory_gb": 6 },
  "starve_after_minutes": 10,
  "keep_finished": 50
}
```

- A field that is absent or 0 takes the host's default: `cpus` = max(1, cores − 2); `memory_gb` = max(1, ⌊0.75 × RAM in GB⌋); `io_jobs` = 2; `default_claim` = {min(4, cpus), min(6, memory_gb)}; `starve_after_minutes` = 10; `keep_finished` = 50. A GB is 2^30 bytes throughout. On fresco (30.2 GB of RAM) that is 14 cores and 22 GB, so three jobs of the default claim run at once. Cores come from `runtime.NumCPU()`; RAM from `/proc/meminfo` (`MemTotal`) on Linux and `sysctl hw.memsize` on macOS.
- Bounds, once a 0 has taken its default: `cpus` 1–1024, `memory_gb` 1–65536, `io_jobs` 1–64, `starve_after_minutes` 1–1440, `keep_finished` 1–10000, and each default claim value 1 up to its cap. A value outside its bounds (a negative one included), or a default claim larger than a cap, makes the block invalid. Admission then pauses (§5.2) and the view says why. No cap is guessed.
- The host's cores and RAM are detected once per daemon process and kept; `sysctl` gets 5 s. When the RAM can't be read, `memory_gb` has no default: a block that doesn't set it is invalid, with the reason "this host's RAM could not be read: set scheduler.memory_gb".
- The block is read from the global `config.json` at every admission pass, so a hand edit applies without a restart.
- A claim is `--cpus N` (whole cores, at least 1) and `--mem N` (whole GB, at least 1), each defaulting to the default claim, plus `--io` for an I/O-heavy job, which takes one of the `io_jobs` slots. `--exclusive` claims every cap: all the cores, all the memory and every I/O slot, as the caps stand when it is admitted. It is stored as a flag, so it follows a cap edit. It can't be combined with `--cpus`, `--mem` or `--io`.
- **Hints.** The CPU claim is the job's parallelism. The shim adds each of these to the job's environment unless the submitter's environment already sets it: `CARGO_BUILD_JOBS=N`, `RUST_TEST_THREADS=N`, `MAKEFLAGS=-jN`, `NIX_BUILD_CORES=N`, `CMAKE_BUILD_PARALLEL_LEVEL=N`, `GOFLAGS=-p=N`. For an exclusive job, N is the CPU cap. `LOOM_JOB=<id>` is always set.

### 5.2 Admission

An admission pass runs when a job is submitted, when one ends, on `SetSchedulerCaps`, on `MoveJobToFront`, and on the model's tick while the queue is not empty (which picks up hand edits). It reads the caps, then walks the queued jobs in queue order (submit order, except for jobs moved to the front):

1. A job whose claim exceeds a cap is skipped. The view marks it "over this host's caps", and it is never the head.
2. The **head** is the first queued job that isn't skipped. Once it has waited `starve_after_minutes` since it was submitted, the pass starts the head if it fits and starts nothing else, so a large job can't be starved by smaller ones passing it.
3. Otherwise (first fit) the pass starts every job, in order, whose claim fits what the running jobs leave: their claimed cores plus its own within `cpus`, their claimed memory plus its own within `memory_gb`, and their I/O jobs plus its own within `io_jobs`.

For each job still waiting, the pass records why, for the view and `loom job list`: "needs 8 cores (6 free)", "needs 12 GB (8 free)", "needs an I/O slot", "waits for job 16, which has waited past 10m", "over this host's caps", or "admission paused: <reason>".

While admission is paused (an invalid `scheduler` block, §5.1, or an unreadable `jobs.json`, §5.9), nothing new starts, running jobs are left alone, and `SubmitJob` is refused with the reason.

Lowering a cap never touches running jobs. It only changes what starts next.

### 5.3 Running a job

Starting an admitted job is a model job (blocking work):

1. If the job's cwd no longer exists, the job is `failed` ("its folder is gone"), and nothing starts.
2. The daemon starts `<its own executable> job-shim <job dir>` (`os.Executable()`) as its own child in a new session (`Setsid`), with stdin `/dev/null` and stdout and stderr appended to `output.log`. A goroutine reaps the child. Every other check on the shim goes through its lock (step 4), so a shim that a restarted daemon adopted is watched the same way.
3. The shim reads `spec.json` and lowers its own priority, which everything it starts inherits: `setpriority` to nice 10, and on Linux `ioprio_set` to the best-effort class at level 7. It builds the environment (the submitter's, plus the hints of §5.1), takes an exclusive `flock` on `run.lock`, writes `started.json` (its pid and start time), and runs the argv in the cwd as its child. No shell is involved: a pipeline is `-- sh -c '…'`.
4. The start job waits up to 5 s for `started.json`. When it appears, the job is running, with the shim's pid recorded. If it doesn't appear, the start failed: the child is killed, and the job is `failed` ("the job runner did not start").
5. The shim catches SIGTERM, SIGINT and SIGHUP, so a signal sent to its process group reaches the command while the shim stays up to report. When the command exits, the shim writes `exit.json` atomically (exit code or signal, end time, the command's max RSS from `rusage`, and the start error if the exec failed) and exits, which releases the lock.

The lock file is opened close-on-exec, so only the shim holds it. Its process group is the job's: the shim leads it (pgid = its pid), and the command and its descendants inherit it.

### 5.4 Watching, settling and usage

While any job runs, a gated model job (`gateQueue`, interval 3 s) does two things.

It **tests each running job's lock** without blocking. If the lock is held, the shim is alive. If it is free, the shim is gone and the job settles from `exit.json`:
- `cancelled` if a cancel was requested;
- otherwise `done` for exit 0, and `failed` for a non-zero exit, a signal, or a start error.

A missing or unreadable `exit.json` makes the job `lost` (or `cancelled`, when a cancel was requested). In that case its process group is sent SIGKILL, so nothing it started runs on unaccounted. The kill goes ahead only when the latest sample shows processes in the group and no process with the shim's pid. A live process with that pid that doesn't hold the lock is a reused pid, and its group is never signalled.

It also **samples usage** by running `ps -A -o pid=,pgid=,rss=,time=` once, with a 5 s timeout, a command Linux (procps) and macOS (BSD) both accept, though they format `time` differently and the parser reads both. A sample that fails or times out is skipped: usage keeps its last values, and the lock tests and settling go ahead without it. For each running job it sums RSS and CPU time over its process group:
- current memory is the RSS sum, and peak memory is the largest sum seen;
- current CPU is the change in summed CPU time divided by the time between samples, in cores.

The peak restarts from zero when a daemon adopts a job. The view shows peak memory against the memory claim, and current CPU against the CPU claim.

### 5.5 Cancel

- A queued job is `cancelled` at once.
- For a running job, the model records the request, confirms the lock is held (so the pid is still the shim's), and sends SIGTERM to the job's process group. If a later check 10 s or more after that still finds the lock held, the group gets SIGKILL. The job settles as `cancelled` either way (§5.4).

Cancelling a finished job is refused, saying it has finished.

### 5.6 Daemon stop, restart and upgrade

A stopping daemon does nothing to jobs. Running ones keep running, since their shims are in their own sessions, and the queue is on disk.

A booting daemon (`Model.Boot`) loads `jobs.json`, settles every running job through the lock test of §5.4, and only then runs its first admission pass, so the caps count the jobs it adopted. A job that was mid-start (no `started.json`) is settled the same way: if its lock is held it runs, otherwise it settles from `exit.json` or is `lost`. A job the record calls queued is probed too, since a start whose save failed (§5.9) leaves a running shim behind a queued record. If its lock is held, it is adopted as running; if it has an `exit.json`, it settles from it. Either way it is never started twice.

The systemd unit in `USAGE.md` gains `KillMode=process`. Under the default (`control-group`), `systemctl --user restart loom` would kill every shim in the unit's cgroup.

### 5.7 Session lifecycle

- Pausing or killing a job's owner cancels its queued and running jobs first (§5.5), with the reason "because the session was paused" or "because the session was killed". The pause or kill then goes ahead without waiting for the jobs to exit. A paused agent can't take a result, and a kill removes the worktree the job runs in. No notice is sent for these cancellations.
- Only a pause or kill that someone requests cancels jobs. When loom pauses a session on its own because its tmux session died (`pauseGone`), the session's jobs keep their places and run on (the owner, 2026-11-04): a dead pane isn't anyone's choice to drop the work. A notice due while the session is paused is recorded as undelivered (§5.8), and the result stays in the view and the log.
- Resuming a session doesn't restart its jobs.
- A job whose owner is no longer served (unregistered, or gone when the daemon boots) keeps its place, and its notice is recorded as undelivered. A job whose cwd is gone fails when it starts (§5.3).
- Jobs without an owner are never touched by a session's lifecycle.

### 5.8 Notices

When a job with an owner and notify set settles, the model sends the owner's Claude a message through the loom mod, with the new origin `OriginJob`. It queues behind any running turn exactly as `Send` queues a CLI message, but carries no "From" line and isn't the person's own words.

```
[loom] job 14 "polyoxide tests" finished: exit 0 after 3m10s (1m42s in the queue).
Log: /home/aidanb/.loom/jobs/14/output.log (2431 lines). Last 20 lines:

…
```

- **First line, by outcome:**
  - `finished: exit N`
  - `was killed by SIGNAME`
  - `was cancelled from the work view` (or `with loom job cancel`)
  - `was lost: its runner died without a result (the host may have rebooted)`
  - `could not start: <error>`
- **Tail:** the last `--tail` lines of `output.log` (default 20, at most 200, 0 for none), capped at the last 4,000 bytes.
- **No mod:** where `Send` would refuse a CLI message for want of a mod (another program than Claude, a launch without a live mod), or the owner isn't running, the notice is recorded on the job as undelivered, with the reason, and shown in the view. It is never pasted into the pane, because a paste could answer an open dialog.
- **When notify is set:** `submit` sets it unless `--no-notify` is given; `run` clears it. `NotifyJob` sets it on a job that hasn't ended (§6.3).

### 5.9 Persistence and retention

Everything is under `<globalDir>/jobs/`, written only by the model (mode 0700 for directories, 0600 for files, atomic writes):

| Path | Holds |
|---|---|
| `jobs.json` | `{"version": 1, "next_id": N, "jobs": [...]}`: every job's record except its environment (ID, owner, description, argv, cwd, claim, notify and tail, state, the times it was submitted, started and ended, exit, peak memory, the shim's pid, any cancel request and the notice's fate) |
| `<id>/spec.json` | What the shim needs: the job's ID, argv, cwd and captured environment, and the parallelism hint (§5.1), which is written when the job starts |
| `<id>/output.log` | The command's stdout and stderr |
| `<id>/run.lock`, `<id>/started.json`, `<id>/exit.json` | The shim's lock, start record and result (§5.3) |

A job's `spec.json` is written at submit, so a queued job survives a restart. `jobs.json` is written on every change (submit, start, settle, cancel, a move, a notify flag). Usage samples aren't persisted.

A `jobs.json` that fails to load (unreadable, invalid, or a newer `version`) is left untouched. Submissions are then refused, saying why, admission pauses, and a notice tells the next TUI. Running shims carry on.

After a job settles, finished jobs beyond `keep_finished` are dropped from `jobs.json`, oldest end first, and their directories removed. The path removed is always `<globalDir>/jobs/<id>`, built from the job's ID, never a path read from a file. The ID counter never goes back.

`SubmitJob` replies only once the job's `spec.json` and `jobs.json` are written. A failed write refuses the request with the error and changes nothing. A change the model makes on its own (a start, a settle, a usage peak) stays in memory when its write fails, raises a notice, and is written with the next save.

## 6. The CLI: `loom job`

### 6.1 Commands

```
loom job submit [-d DESC] [--cpus N] [--mem GB] [--io | --exclusive] [--tail N] [--no-notify] -- CMD [ARGS…]
loom job run    [-d DESC] [--cpus N] [--mem GB] [--io | --exclusive] -- CMD [ARGS…]
loom job wait   ID
loom job logs   ID [--tail N] [-f]
loom job list   [--all]
loom job cancel ID
```

`internal/cli/job.go` holds these. They are daemon clients joined through `cli.Join` like `loom session`, so they spawn a daemon when none runs.

- `-d` defaults to the command line, shell-quoted and cut to 60 characters.
- Bounds, checked by the model so every client meets them:
  - a description of 1–200 characters after trimming;
  - `--cpus` and `--mem` at least 1 (on the wire, 0 means the default claim);
  - `--tail` 0–200;
  - a non-empty argv of at most 64 KiB in all, and an environment of at most 1 MiB;
  - a cwd that is absolute and exists.

  `logs --tail N` takes any N ≥ 0.
- `submit` sends the argv, `os.Getwd()`, `os.Environ()`, the claim, notify, tail and `$LOOM_LAUNCH`. It prints `job 14 queued (3rd in line, 2 running)` or `job 14 started` and exits 0.
- `run` submits without notify, with the default tail (used if an interrupted follower later sets notify), then follows the job (§6.3).
- `wait` follows an existing job.
- `logs` prints `output.log` (all of it, or the last N lines). With `-f` it follows until the job ends.
- `list` prints the running and queued jobs, and with `--all` the finished ones kept, as a table: ID, state, description, owner, claim, and wait time or duration. Waiting jobs show why they wait (§5.2).
- `cancel` sends `CancelJob`.
- A refused submission (over a cap, admission paused, `jobs.json` failed to load) prints the refusal and exits 1.

### 6.2 Owner

The model resolves `$LOOM_LAUNCH` to the session whose current launch it is (`instByLaunch`, `core/mod.go`). That is the job's owner, kept as its `InstanceID`.

- If the variable is empty (a plain shell), the job has no owner, and `submit` adds: `no session to notify: follow it with loom job wait 14`.
- If the launch is one no served session holds, the job also has no owner. The reply carries a warning, which the CLI prints: `this shell's loom session (launch <id>) isn't one the daemon serves: job 14 has no owner, so no message will come`.

### 6.3 Following a job (`run`, `wait`)

The follower reads the job from its replica and its output from `output.log` on disk, polling the file every 200 ms. Status lines go to stderr, prefixed `loom:`: its place in the queue as it changes, `started after 1m42s`, and at the end `job 14 done in 3m10s (exit 0)`. The log goes to stdout.

Its exit code is:
- the job's exit code;
- 128 + N for a job killed by signal N;
- 125 for a job that was lost, could not start, or was cancelled, with a stderr line saying which;
- 125 also when the follower itself can't go on: it lost the daemon, or the daemon doesn't know the job. Its stderr line says so, and, when the job may still be running, how to follow it again (`loom: lost the daemon; job 14 may still be running (loom job wait 14 follows it again)`). Exit 1 is left to the job's own code (the owner, 2026-11-04).

If the follower is interrupted (SIGINT, SIGTERM or SIGHUP, for example a Bash timeout), it sends `NotifyJob` when the job has an owner and hasn't ended, prints `loom: job 14 continues; a message will bring its result (loom job wait 14 follows it again)`, and exits 130, 143 or 129. The job itself is never cancelled by a follower's end.

## 7. The work view

### 7.1 Layout and keys

`w` toggles the work view (the Lua action `toggle_work_view`, bound in `script/defaults.lua` and listed in `docs/specs/scripting.md`). It is a fourth view mode beside focus, overview and workbench, entered from focus or overview; `w` or `esc` returns to the mode it came from. It isn't persisted. It covers the whole host, across every workspace, reading `QueueView` from the replica. It tails the selected job's `output.log` from disk, refreshed on `QueueChanged` and on the TUI's tick. Mouse input is dropped, as in overview.

```
 Work queue · fresco                                        w/esc back
 CPU ██████████░░░░ 10/14 claimed · 11.2 in use
 MEM ████████░░░░░░ 12/22 GB claimed · 9.8 GB in use
 I/O ██░░           1/2
 RUNNING
 ▸ 14  polyoxide tests   kalshi · polyoxide      4c 6G      3m10s  peak 5.1G
   15  go test ./...     work-scheduler · loom   4c 4G      0m42s  peak 1.2G
 QUEUED
   16  release build     wasm · polyoxide        8c 12G io  2m     needs 8 cores (4 free)
   17  bench orderbook   kalshi · polyoxide      exclusive  1m     waits for job 16
 RECENT
   13  clippy            wasm · polyoxide        ✓ 0    1m02s  2m ago
   12  cargo test        kalshi · polyoxide      ✗ 101  4m33s  10m ago
 ── job 14 · tail ────────────────────────────────────────────────────
 test orderbook::depth ... ok
 enter log · x cancel · t to front · o open session · e caps
```

- The header names the host (`os.Hostname`).
- The bars show claimed totals against the caps, and the running jobs' measured CPU and memory. When admission is paused, the bars give way to the reason.
- Sections:
  - RUNNING, in start order;
  - QUEUED, in queue order, each with why it waits;
  - RECENT, the last 10 finished, newest first, with a mark for an undelivered notice.
- Keys:
  - `j`/`k` (and the arrows) move;
  - `enter` opens the full log in a scrolling pager (`esc` back);
  - `x` cancels the selected job after a confirm (`Cancel job 14 "polyoxide tests"?`);
  - `t` moves a queued job to the front;
  - `o` focuses the owner's session in focus mode, opening its workspace's tab if it isn't open;
  - `e` opens the caps form.
- Offline (daemon lost), the view shows the last replica, and its actions are refused like every model key.

### 7.2 The caps form

A small form over the view, with fields for CPUs, memory GB, I/O jobs, the default claim's CPUs and memory GB, starve-after minutes and keep-finished. Each field shows the value in effect and whether it is the host default, and the form names the host's detected cores and RAM. Saving sends `SetSchedulerCaps`, which validates the values (as §5.1), writes the global `config.json`'s `scheduler` block, and runs an admission pass. A refusal is shown in the form.

### 7.3 Badges

A session's rail card and overview card show:
- `⚙ job 14` when it has a running job (the lowest running ID, with `+N` for more);
- `⚙ queued` when it has only queued ones.

## 8. Loom's Claude context

`session/claude-loom-context.md` and `session/claude-loom-context-workspace.md` gain this paragraph:

> **Heavy work goes through loom's work queue.** This machine is shared by many loom sessions. Run builds, full test suites and benchmarks with `loom job submit -d "<what>" -- <cmd>` and carry on: a `[loom] job N` message brings the result. Or use `loom job run -d "<what>" -- <cmd>` with Bash's `run_in_background` when you need the output to continue. A job gets 4 cores and 6 GB unless you claim more with `--cpus`/`--mem`, `--io` for full builds that write a lot, or `--exclusive` for benchmarks. Quick commands (`cargo check`, linters, git) run directly.

The claim figures in the paragraph are the defaults' wording. The file is static, so it doesn't follow a host's `default_claim`.

Each file's closing sentence drops "running builds and tests", which the paragraph now contradicts (the owner, 2026-11-04). They become `Everything else — editing files, committing, diffing, and viewing history within your branch — works exactly as normal.` and `Editing files, committing, and viewing diffs/history all work exactly as normal.`

## 9. Wire

- **Requests** (`core/iface.go`, request kind):
  - `SubmitJob(spec sched.Submit, req ReqID)`, where `sched.Submit` holds the argv, cwd, environment, description, claim, notify, tail and launch;
  - `CancelJob(id sched.JobID, from CancelFrom, req ReqID)`, where `from` says whether the cancel came from the CLI or the work view, for the notice's wording;
  - `MoveJobToFront(id sched.JobID, req ReqID)`;
  - `NotifyJob(id sched.JobID, req ReqID)`;
  - `SetSchedulerCaps(s config.SchedulerSettings, req ReqID)`.
- **Reply:** gains `Job sched.JobID`, the new job's ID for `SubmitJob`. The ownerless warning of §6.2 travels in `Reply.Notice`.
- **Query** (`rpc:local`): `Queue() QueueView`, answered from the replica.
- **State event:** `QueueChanged`, with its coalescing key. It is published after any change to the queue, a job's state, or the sampled usage.
- **`QueueView`:** the host name, the caps in effect (each marked default or set) or the reason admission is paused, the claimed and measured totals, and for each job its ID, state, description, argv, cwd, owner (`InstanceID`, title, workspace name), claim, times, exit, peak and current memory, current CPU, log path, why it waits, and the notice's fate. The environment crosses the wire only inside `SubmitJob` and never appears in a published view.
- **Protocol:** `rpc.Protocol` is unchanged, since adding methods is compatible (`docs/claude/adding-a-core-method.md`). `core/rpc/methods_gen.go` and `docs/specs/protocol.md` are regenerated.
- **Mod:** the mod protocol is unchanged. A notice is an ordinary outbox message.

## 10. Components

| Where | What |
|---|---|
| `sched/` (new) | `JobID`, `State`, `Claim`, `Caps`, `Record`, `Submit`; the pure admission function; the `jobs.json` store (versioned); the per-job files; `RunShim`, `Start`, the lock test and group signals (`shim_unix.go`; `shim_windows.go` stubs that return an unsupported error); priorities (`prio_linux.go` for `ioprio_set`, `prio_other.go`); the usage sampler and its `ps` parser; host detection; hints. Its own `CLAUDE.md` and `README.md` |
| `config/settings.go` | `SchedulerSettings` in `Settings` (`json:"scheduler,omitempty"`), read from the global config only, with getters applying the host defaults and validation |
| `core/` | The queue's state, admission passes, start, settle, cancel and persistence (`queue.go`); the requests (`queue_requests.go`); `QueueView` (`queue_view.go`); notices (`queue_notice.go`, and `OriginJob` in `mod.go`); `gateQueue`; cancelling an owner's jobs in the pause and kill paths; loading the queue in `Boot`; `QueueChanged` and `Reply.Job` in `events.go` |
| `core/rpc/` | Regenerated |
| `main.go` | The hidden `job-shim` command, which calls `sched.RunShim` |
| `internal/cli/job.go` | `loom job` |
| `app/`, `ui/`, `script/defaults.lua` | The work view mode, the log pager, the caps form, the badges, `w` |
| `session/claude-loom-context*.md` | §8 |
| Docs | `USAGE.md` (`loom job`, the view, the `scheduler` block, `KillMode=process` in the unit); `docs/ARCHITECTURE.md` (the `sched` row, `jobs/` in persistent state, the queue in the daemon's work with no TUI); `docs/specs/scripting.md`; rules in `sched/CLAUDE.md`, and in `core/CLAUDE.md` within its byte budget (check `go run ./tools/claudemd -v`, and move context into a guide if it is full) |

`GOOS=windows CGO_ENABLED=0 go build ./...` must pass. On Windows, `loom job` and the view say the scheduler is unavailable there.

## 11. Failure handling

All of these fail closed: nothing is guessed, and each says why.

| Situation | Result |
|---|---|
| A claim over a cap | `SubmitJob` refused, naming the cap: `claims 32 GB; this host's cap is 22 GB` |
| A submission outside the bounds of §6.1 | `SubmitJob` refused, naming the bound |
| A failed write of `spec.json` or `jobs.json` | A request is refused with the error, changing nothing; a change the model made on its own stays in memory, raises a notice, and is written with the next save |
| `ps` fails or takes over 5 s | That sample is skipped; settling goes on |
| On Windows | `SubmitJob` refused: "the work queue isn't available on Windows" |
| `--exclusive` with `--cpus`/`--mem`/`--io` | Refused by the CLI |
| An invalid `scheduler` block | Admission paused; running jobs untouched; the view and `list` give the reason |
| `jobs.json` fails to load | Left untouched; submissions refused; admission paused; a notice |
| The cwd is gone at start | `failed`: "its folder is gone" |
| The shim doesn't write `started.json` within 5 s | Child killed; `failed`: "the job runner did not start" |
| The command can't be executed | `failed`, with the exec error from `exit.json` |
| The shim gone and `exit.json` missing | `lost`; the process group gets SIGKILL |
| An unknown `LOOM_LAUNCH` | Accepted without an owner; the CLI prints the warning |
| No live mod when a notice is due | Recorded on the job as undelivered, with the reason; never pasted |
| Cancelling a finished job | Refused: it has finished |
| Moving a job to the front that isn't queued | Refused: it isn't waiting |
| `NotifyJob` on a job with no owner, or one that has ended | Refused, saying which |

## 12. Testing

- **`sched`:**
  - Admission tables: first fit; the head starving and blocking the rest; an over-cap job skipped and never the head; exclusive; cap edits; an empty host.
  - Store round trips, and a newer `version` left untouched.
  - Real-process shim tests: exit codes; signals; a cancel's SIGTERM reaching a grandchild through the group while the shim writes `exit.json`; the SIGKILL path; the lock held while running and free after; hints set only where the submitter didn't; nice applied (`getpriority`); ioprio on Linux.
  - `ps` parser tables for the procps and BSD formats; host detection.
- **The shim in tests.** A test binary has no `job-shim` command, so packages whose tests start real shims (`sched`, `core`, `internal/cli`) re-execute their own test binary as the shim: their `TestMain` hands over to the shim first when an environment variable marks the process as one, and a test runner points the daemon's shim command at the test binary.
- **`core`, through the production path** (a temp global dir under `IsolateLoomDirs`, a real shim):
  - submit, admit, run, settle;
  - cancel queued and running jobs;
  - pause and kill cancelling the owner's jobs;
  - boot adoption: a shim started, the model rebuilt from disk, the job adopted and then settled;
  - an invalid caps block pausing admission;
  - an ownerless job;
  - notices through a live mod's outbox, and recorded as undelivered with no mod;
  - `OriginJob` composition;
  - a case for `gateQueue` in `TestProductionGatedJobsYieldOneResult`.
- **`internal/cli`**, against an in-process daemon: `submit`; `run` passing on the exit code and the 125 cases; an interrupted follower sending `NotifyJob`; `wait`; `cancel`; `list`.
- **`app`/`ui`:** the view's rendering (empty, busy, paused), its keys, the caps form's validation, the badges.
- **e2e** (`-tags e2e`, opt-in): a daemon restarted while a job runs. The job keeps running, the new daemon adopts and settles it, and `loom job wait` returns its exit code.

## 13. Known limits and later work

- **Cooperative only.** Work not submitted through `loom job` competes as before, and subagents may not see loom's appended context. Later: the loom mod's Bash hook asks the daemon about each command and rewrites matching ones (per-host patterns) to `loom job run -- <cmd>`.
- **Memory claims are estimates, and overruns aren't stopped.** Later, on Linux: each job in a systemd user scope held to its claim (`MemoryMax`/`MemoryHigh`, `CPUQuota` or `CPUWeight`, `IOWeight`).
- **Claims are static.** Later: claims learned from measured peaks per command and repo, priorities, and agent-assisted balancing.
- **No log size cap.** Retention (`keep_finished`) is the only bound.
- **Local only.** A loomdev sandbox, or a second global dir on the same host, has its own queue and caps. Followers and the view read logs from disk, which a remote client (the cloud federation direction) would need as a request.
- **No phone view,** no band in Claude's UI showing a session's jobs, and no start notices.
- **Windows** has no scheduler.
