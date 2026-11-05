# Work queue: cgroup confinement and greedy admission

Status: design approved 2026-11-05.

Builds on [the work scheduler](2026-11-04-work-scheduler-design.md), [the smart scheduler](2026-11-05-smart-scheduler-design.md), [job scaling](2026-11-05-job-scaling-design.md) and [past-peak holds](2026-11-05-past-peak-holds-design.md), each of which deferred confinement. The owner's ask: use cgroups to make the scheduler's use of the host better, greedily if need be, since today it is poor.

## 1. Problem

Claims bound admission only, so the queue must be cautious when it admits: once a job runs, nothing can take back what it uses. On fresco (Ryzen 7 7700X, 8 cores and 16 threads, 30 GB RAM, 15 GB zram swap), the 50 jobs in `jobs.json` on 2026-11-05 (22:00–23:38, 1.53 h with a job running):

- **Memory waits on claims, not on use.** While jobs ran, they claimed 15.3 of 22 GB on average, while the sum of each running job's whole-run peak averaged 10.2 GB. Of the 4,510 job-seconds spent queued, 3,790 (84%) would have fit by measured use. The daemon log's 30 starts waited 124 s on average; 11 waited over 30 s, 5 over 5 minutes, the longest 921 s.
- **A claim reserves a brief peak for a whole run.** Job 648 (4c/14G) peaked at 11.5 GB at 593 of its 647 CPU-seconds (its link and test phase), and used far less before.
- **Cores are unbounded at run time.** 4-core jobs peaked at 10–14 busy cores (run profiles of jobs 642, 643, 645, 648). The worst leak, nextest ignoring `RUST_TEST_THREADS`, is fixed in 8db36068; tokio, Go test binaries, rayon, linkers and Node test runners still size themselves to the host.
- **`nice` does not rank jobs below agents.** Every job runs in the daemon's login cgroup (`session-1.scope`), and the agents' panes in their own scopes under `user@1000.service`. The CPU controller is on at that level with equal weights (100), so under contention the kernel splits CPU between all jobs together and all agents together before `nice 10` decides anything, and only within the jobs' group.
- **Sampling misses processes.** A process that leaves the job's session (`setsid`) escapes the measured usage and a cancel.

Success, read after a week of use (§13.4): shorter waits than the baseline above (mean `waited_s`, starts waiting over 5 minutes), working-set GB-seconds closer to budget GB-seconds, and few ladder events (squeezes, pauses, requeues), which are the cost of greed.

## 2. Decisions

The owner's choices in the brainstorm, with what was rejected:

- **Fill the box, stay safe.** Admit greedily so jobs rarely wait while the host has room, never let the host run out of memory, and keep agents and the TUI responsive. Rejected: shortest waits above all, interactivity first, throughput first.
- **An escalation ladder when greed loses** (§8): squeeze the youngest job (`MemoryHigh`), then pause it (`cgroup.freeze`), then kill and requeue it. Rejected: throttle only, pause only, kill-and-requeue only.
- **Share CPU, don't cap it** (§7.3). No per-job CPU limit; `cpu.weight` proportional to the claim, and the jobs' slice weighted below the agents. Admission gates on CPU pressure, not summed claims. Rejected: `cpu.max` per job; `cpu.max` only under contention.
- **Forecast overlap** (§7.2). Admit if the jobs' expected memory curves, aligned in time, never sum past the budget; a command without curves counts at its claim. Rejected: admitting on current measured use (most greedy); summing exact peaks (least greedy).
- **A live budget** (§7.1): what jobs use now plus `MemAvailable` less a reserve for the interactive side, under `memory_gb` as a ceiling. Rejected: the fixed cap; pressure alone. The past-peak holds spec rejected live free memory because a job admitted 3 s ago hasn't allocated yet; here running jobs count by their forecast, not their measured use, so that race is closed.
- **Every job may be requeued, with an opt-out** (`--no-requeue`), and the owner is messaged when one is.
- **A systemd scope per job, created over D-Bus from the start** (§5), with go-systemd. Rejected: shelling out to `systemd-run`/`systemctl` (the recommendation, with D-Bus later); a cgroup subtree the daemon owns through `Delegate=yes`.
- **Usage curves** (§6) beside the stored staircases, since a falling staircase hides a run's low early phase.

Made while designing, open to planning: a requeued job keeps its ID and runs again as its next attempt (§8.4).

## 3. Scope

In: per-job scopes and the jobs slice; measurement from cgroup files; `KillUnit` cancels; the fallback; usage curves; the budget; the memory and CPU fits; `Forecast` on the same fits; the ladder; `--no-requeue`; what the work view, `loom job list`, `run` and `wait` show; config; docs.

Out (§14): I/O control, cpusets, nix-daemon and sccache-server work, macOS and Windows, a downgrade's handling of a paused job.

## 4. Concepts and names

- **Contained job:** a job whose shim joined its scope before starting the command. Every other running job is **uncontained**, with a reason.
- **Scope:** the transient systemd unit `loom-job-<id>.scope`, in the slice `loom-jobs.slice` under the user's systemd manager.
- **Working set:** a cgroup's `memory.current` less the `inactive_file` of its `memory.stat`, in KB: the memory the job can't give back without swapping.
- **Curve:** a profiled run's usage over its progress, rising and falling (§6).
- **Budget:** the memory greedy admission packs into (§7.1).
- **Headroom:** the host's `MemAvailable`.
- **Reserve:** `interactive_reserve_gb`, the headroom kept for everything outside the queue.
- **Short:** memory is short when headroom is below the reserve or the jobs slice's memory pressure is high (§8.1).
- **Rung:** a ladder step applied to a job: squeezed, paused, requeued.
- **Greedy mode:** containment works on this host, so the fits of §7 and the ladder run. **Fallback mode:** it doesn't, and admission is today's, holds included.

## 5. Confinement

### 5.1 The scope

The shim creates its own scope: after it takes `run.lock` and before it starts the command, it calls `StartTransientUnit("loom-job-<id>.scope", "fail", …)` on the user bus with `PIDs` = its own pid, `Slice=loom-jobs.slice`, `CPUWeight` = 100 × the claim's cores (bounded 1–10000), `OOMPolicy=kill` (so the kernel's OOM killer takes the whole job), and memory and CPU accounting on. It waits for the start job to finish, then starts the command. Migrating a process doesn't move children it already forked, so containment must come first; `systemd-run --scope` works the same way, and systemd, not the daemon, can move a process out of the login cgroup.

`started.json` gains `unit`, the scope's name. A shim that can't create its scope runs the command uncontained and writes why into `started.json` (`uncontained`).

### 5.2 The slice

`loom-jobs.slice` sits beside the agents' pane scopes under `user@1000.service`. At startup, and whenever the config changes, the daemon sets it with `SetUnitProperties(runtime=true)`: `CPUWeight=jobs_cpu_weight` (default 50, below the panes' 100), and `MemoryMax` at the `memory_gb` ceiling. The ladder keeps its `MemoryHigh` at the budget (§8.1).

### 5.3 Measurement

For a contained job, the daemon finds the scope's cgroup path once (the unit's `ControlGroup` property, on start and on adopt) and each sample reads, under `/sys/fs/cgroup`:

- `memory.current` and `memory.stat` (`inactive_file`, `anon`, `file`) for the working set;
- `cpu.stat`'s `usage_usec`: exact CPU of every process the job ever ran, `setsid` or not, which becomes the job's progress;
- `memory.pressure`, `cpu.pressure` and `memory.events` (`oom_kill`);
- `cgroup.events` (`frozen`, `populated`).

An uncontained job keeps the `/proc` session sampler. `RunOf`'s peak for a contained run is its largest sampled working set (not `memory.peak`, which counts file cache), with the 15% margin of sizing as before.

### 5.4 Cancel and the lost-job proof

A cancel of a contained job calls `KillUnit(unit, "all", SIGTERM)`, then SIGKILL after 10 s as today. The lock stays the proof that the shim lives; for a contained job, `cgroup.events`' `populated 0` is the proof that nothing of the job survives.

### 5.5 Fallback

Containment is Linux only; elsewhere the `Confiner` answers `ErrUnsupported`. On Linux the daemon probes the user bus at startup, after any D-Bus call fails, and every 60 s while in fallback mode, returning to greedy mode once a probe succeeds. If the bus is unreachable or `contain` is off, the host is in fallback mode: admission is today's code, holds included, the ladder is off, and the work view's header says `uncontained: <why>`. A job is never refused because it can't be contained, and an uncontained job counts at its claim or hold in either mode.

## 6. Usage curves

### 6.1 The curve

At a done run's settle, beside `BuildProfile`'s staircases, the trace becomes a curve per resource: the highest level within each stretch of progress, at most 16 steps, adjacent steps merged by keeping the higher level (so the curve is never below the run), each step carrying its end progress, its level and the wall seconds since the job's start at that point. Memory levels are working set (contained) or the session's RSS (uncontained), in KB rounded up to MB; cores are milli-cores.

### 6.2 Storage and holds

`beliefs.json` keeps writing each profiled run's `profile` and adds `curve` beside it; older looms ignore the field, so the file stays downgrade-safe. The falling staircase is computable from the curve (its running maximum from the end backwards); holds keep reading the stored staircase, and a test pins that the two agree on the same trace.

Runs learned before this ship have no curve. A command needs 3 curves among its newest 5 runs before the memory fit uses them (§7.2), the same threshold holds use.

## 7. Greedy admission

### 7.1 The budget

Each admission pass computes:

`budget = Σ measured memory of running jobs + MemAvailable − interactive_reserve_gb`

held between 0 and the `memory_gb` ceiling, where a contained job's memory is its working set and an uncontained one's its session's RSS. Swap counts as nothing. While any job waits, a pass runs after every usage sample (3 s) as well as on submits, ends and releases, since the budget moves with the agents.

### 7.2 The memory fit

For each queued job in queue order (first fit and the starvation guard unchanged), the predicted memory over wall time is summed from now until the candidate's expected end:

1. **A contained running job** follows its belief's curves from its current progress, as the pointwise maximum of its profiled curves; progress converts to wall time at the job's rate over its last 30 s of samples, or at its command's usual rate (below) for its first 30 s.
2. **The candidate** follows the pointwise maximum of its belief's curves from progress 0, at its command's usual rate (median CPU-seconds over wall seconds of its done runs).
3. Every predicted level carries the 15% margin, and every step is widened in time by ±max(30 s, 25% of how far ahead it is), since rates drift under shared CPU.
4. **Counted flat** for their whole expected run, at their claim or hold: uncontained running jobs, running jobs and candidates whose command has fewer than 3 curves, a laddered job (§8), and a running job whose measured working set is above its prediction, at the measured figure plus the margin.
5. Jobs started earlier in the same pass count as running.

The candidate fits if the sum stays at or under the budget throughout. An `--exclusive` job still waits for an empty queue and takes every cap.

### 7.3 The CPU fit

Summed claims no longer have to fit `cpus`. A job fits on CPU while the jobs slice's `cpu.pressure` "some" avg10 is under `cpu_pressure_pct` (default 25) and the running jobs' summed claims plus its own are at most `cpu_overcommit` × `cpus` (default 2). The second guard exists because pressure is a 10 s average: one quiet moment would otherwise admit many jobs before it rises.

Hints still give each tool its claim plus any granted spare cores (`Grant` unchanged): weights share the cores, the hints keep each tool's parallelism near its claim.

### 7.4 Unchanged, and the forecast

I/O slots, `--exclusive`, dependent jobs and the starvation guard are unchanged. In greedy mode the memory fit replaces holds in admission; holds stay computed for display and for fallback mode. `Forecast` simulates with the same fits, so the queued jobs' expected starts match what admission will do.

New wait reasons: `needs ~11 GB at its peak (forecast leaves 6)`, `CPU busy (pressure 34%)`, `CPU overcommitted (30/28 claimed)`, and `memory is short (job 649 paused)` while the ladder holds admissions (§8.3).

## 8. The ladder

### 8.1 Rung 0: the kernel

The daemon keeps `loom-jobs.slice`'s `MemoryHigh` at the budget, updating it when the budget moves by 256 MiB or more: past it, the kernel throttles and reclaims every job with no daemon in the loop. `MemoryMax` at the ceiling is the desktop's last line; a kernel OOM kill inside the slice takes one whole job (`OOMPolicy=kill`), which the daemon reads from `memory.events` and treats as a rung-3 requeue (§8.4).

Memory is **short** while headroom is below the reserve or the jobs slice's `memory.pressure` "some" avg10 is at least 10%.

### 8.2 Rungs 1–3

While memory is short, the daemon acts on the **victim**: the most recently started contained running job that is not `--exclusive`, not the starved head (which goes last), and not already at its last rung.

1. **Squeeze:** `MemoryHigh` = its current working set. It slows, and its cold pages go to compressed swap.
2. **Pause:** if memory is still short 15 s after the squeeze, `FreezeUnit`.
3. **Requeue:** if memory is still short 30 s after the pause, `KillUnit` and requeue (§8.4). A `--no-requeue` job stops at the pause rung, however long.

Each rung re-checks before acting. The ladder moves to the next most recently started job only once the victim is requeued, or paused with `--no-requeue`.

### 8.3 Recovery

Once headroom has been at least the reserve plus 2 GB, and the slice's memory pressure under 2%, for 30 s, laddered jobs are restored oldest first, one every 15 s, each in one step: thawed if paused, and its `MemoryHigh` set back to `infinity`. While any job is squeezed or paused, no new job is admitted, so admissions and pauses can't feed each other.

### 8.4 Requeue

A requeued job keeps its ID, its owner and its place in line (its `Order`), and goes back to `queued` as its next attempt: the job dir's `output.log`, `started.json` and `exit.json` move to `attempt-<n>/`, and the shim's duplicate check stays per attempt. Its memory floor is raised to the working set it reached. After 2 requeues it runs at its full claim, is counted flat (§7.2) and is never laddered again. Followers (`run`, `wait`) print `loom: job 649 requeued: memory ran short; following its next attempt` and follow the new `output.log`; dependents keep waiting on it. The owner gets a message at the requeue: `[loom] job 649 "<desc>" was stopped and requeued: memory ran short (headroom 1.2 GB); it keeps its place in line`.

### 8.5 Learning

A squeezed or paused run teaches memory floors only (its timing and curve are distorted); a killed attempt teaches a floor, as a cancel does today. The record and the run note the ladder (`laddered`), so `RunOf` can tell.

## 9. What people see

- **The work view:** a running job's state reads `squeezed` or `paused (memory)`; an uncontained job is marked `uncontained` with its reason on its detail line; the header shows the budget and the forecast's peak (`budget 19 GB · forecast peak 16 GB`) in greedy mode, or `uncontained: <why>` in fallback mode.
- **`loom job list`:** the same state text after the claim.
- **`run`/`wait`:** a status line on stderr at each rung (`loom: job 649 squeezed: memory is short`, `paused`, `resumed`, `requeued`).
- **Submit:** `--no-requeue` on `submit` and `run`.
- **Messages:** the requeue message (§8.4); squeezes and pauses send none.

## 10. Configuration

The global `config.json` `scheduler` block gains:

| Key | Default | Bounds | Meaning |
|---|---|---|---|
| `contain` | `true` | | per-job scopes, greedy admission and the ladder |
| `interactive_reserve_gb` | 4 | 0–1024 | headroom kept for everything outside the queue |
| `cpu_pressure_pct` | 25 | 1–100 | the CPU fit's pressure gate |
| `cpu_overcommit` | 2 | 1–8 | the CPU fit's summed-claims guard, a multiple of `cpus` |
| `jobs_cpu_weight` | 50 | 1–10000 | the jobs slice's `CPUWeight` |

`memory_gb` becomes the budget's ceiling and the slice's `MemoryMax`. `release_past_peaks` applies in fallback mode only. The ladder's thresholds and timings (10% and 2% pressure, 2 GB, 15 s and 30 s, 2 requeues) are constants. A cap edit through the settings form keeps every key it doesn't show, as `setSchedulerCaps` must already (past-peak holds spec, amendment 9).

## 11. Components

- **`sched` (logic):** the curve (`Curve`, `BuildCurve`, the staircase from a curve); `Budget`; the memory and CPU fits inside `Admit` and `Forecast`, taking predictions instead of holds in greedy mode; the ladder as a pure state machine (`Ladder`: samples and the clock in, actions out); the policy's new fields in `Resolve`.
- **`sched` (runtime):** the `Confiner` interface (create a scope for a pid and wait, set unit properties, freeze, thaw, kill a unit, find a unit's cgroup path) with a Linux implementation on `github.com/coreos/go-systemd/v22/dbus` and `github.com/godbus/dbus/v5` (vendored) and an `ErrUnsupported` one elsewhere; cgroup file readers taking a root directory; the shim joining its scope (§5.1).
- **`core`:** a confinement worker that makes every D-Bus call off the model's loop and posts results back; slice properties at startup and on config change; sampling contained jobs from their cgroups; the greedy pass; the ladder driver; requeue as a new attempt; reconciling ladder state with each unit after a restart (`frozen` in `cgroup.events`, `MemoryHigh`); record and run fields (`Unit`, `Uncontained`, `Ladder`, `Attempt`, `Requeues`, `NoRequeue`, `Laddered`, `Curve`).
- **`core/rpc`:** the job view's state and containment fields, the queue header's budget, `--no-requeue` on submit; the protocol reference regenerated.
- **`internal/cli`, `ui`:** §9.
- **`config`:** §10.
- **Docs:** `USAGE.md` (the queue's behaviour, config, flags), `sched/README.md`, `docs/claude/the-work-queue.md` (the loop rule for D-Bus, the new traps), `docs/ARCHITECTURE.md` (the scopes and the slice), and the rules in `sched/CLAUDE.md`/`core/CLAUDE.md` where they change.

## 12. Failure handling

- **No bus, or a scope that fails:** the job runs uncontained (§5.5); fallback mode when the bus is down.
- **A D-Bus call that fails in the ladder or recovery:** nothing is assumed done; it is logged, shown in the work view, and retried on the next pass. A rung counts as applied only when its call returned.
- **A cgroup read that fails:** that job's sample is skipped, as a failed `/proc` sample is today; the ladder never acts on a stale or missing reading.
- **A daemon restart:** scopes outlive the daemon. On load the daemon finds each running job's unit by the name in `started.json`, re-reads its cgroup path, and reconciles its ladder state against the unit (`frozen`, `MemoryHigh`); a paused job whose memory is no longer short is thawed through recovery.
- **The user manager restarts:** transient scopes are lost from systemd's view while their cgroups remain; the daemon treats a unit it can't find but whose cgroup is populated as contained-but-unmanaged: it counts at its claim and is never laddered.
- **Kernel OOM kill in the slice:** read from `memory.events`; a requeue (§8.4), or a failure with the OOM named for a `--no-requeue` job.

## 13. Testing

### 13.1 Pure

Table tests, with both sides of every threshold pinned: `BuildCurve` (merging keeps the higher level; at most 16 steps; never below the trace); the staircase from a curve agreeing with `BuildProfile` on the same traces; `Budget` (the clamp, swap ignored); the memory fit (two jobs whose peaks collide and two whose don't, the time widening, each flat-count case); the CPU fit (each guard alone); the ladder state machine (each rung's delay, the victim order, the starved head last, `--no-requeue`, `--exclusive`, recovery's hysteresis and order, no admissions while laddered).

### 13.2 Fakes

`Confiner` has a fake that records calls and can fail; cgroup readers run against a temp directory of written `memory.stat`, `cpu.stat`, pressure and `cgroup.events` files. Core tests drive the greedy pass, the ladder and a requeue through the production path (a booted model, real shims via `TestRunner`, `sched.SaveStore`), with the fake confiner.

### 13.3 Live

`-tags systemd_live` (like `advisor_live`): on the real user bus, create a `loom-test-<pid>.scope` in a `loom-test.slice`, check that `MemoryHigh` lands in `memory.high`, that freeze shows in `cgroup.events`, that thaw and `KillUnit` work and that the readers parse the real files; never touches `loom-jobs.slice`; skipped without a user bus (CI, the Nix sandbox). One e2e test runs a contained job end to end, skipped without a bus.

### 13.4 The week's read

Compare with §1: `queue.job_started` `waited_s`, working-set GB-seconds over budget GB-seconds, and counts of squeezes, pauses and requeues (each logged as `queue.ladder` with the rung, the job and the headroom).

## 14. Known limits and later work

- **I/O:** `io` is not delegated to the user manager here, and the NVMe drives run no I/O scheduler, so `io.weight` would do nothing; `io.max` would need delegation. I/O slots stay as they are.
- **cpusets:** not delegated here. With shared CPU there is no limit for tools to size themselves to, so the hints remain what sets their parallelism.
- **Work outside the job's tree:** nix-daemon builds and the sccache server stay in their own cgroups, unmeasured and unladdered.
- **macOS and Windows:** fallback mode.
- **A downgrade while a job is paused:** an older loom doesn't know the scope, so the job stays frozen until thawed by hand (`systemctl --user thaw loom-job-<id>.scope`).
- **Curves need runs:** greedy admission helps a command only after 3 curves; until then it counts at its claim.
- **Later:** CPU limits applied only under contention, `memory.peak` resets per sample for exact phase peaks, `io.max` once delegated, ladder thresholds learned from the week's events.
