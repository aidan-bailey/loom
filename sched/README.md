# sched

The work scheduler's package. This file covers the logic half: the job types, the caps and policy, the admission pass, the parallelism hints, and the `jobs.json` store with the per-job files. The model (`core`) owns the queue, persists it under `<globalDir>/jobs/` and calls this package from its loop; nothing here keeps state of its own, so every function takes the host, the policy and the clock as arguments. The design is in [`docs/superpowers/specs/2026-11-04-work-scheduler-design.md`](../docs/superpowers/specs/2026-11-04-work-scheduler-design.md).

## Files

- `doc.go` — the package comment.
- `job.go` — `JobID`, `State` (with `Held`, `State.Finished` and `State.Waiting`), `Claim`, `Caps`, `Need`, `Host`, `Policy`, `Notice` (with `NoticeCarried`), `Record` (with `Record.QueuedSince`), `Exit` and `Submit`, with `Claim.Need`, `Submit.Validate` and `CheckClaim`.
- `policy.go` — `Resolve`: a `config.SchedulerSettings` and a `Host` in, a `Policy` or an error out.
- `admit.go` — `Admit` and its `Decision`.
- `hints.go` — `Hints`.
- `store.go` — `Store`, `LoadStore`, `SaveStore` and `ErrNewerStore`.
- `files.go` — the file names, `JobDir`, `CreateJobDir`, `RemoveJobDir`, and the codecs of `Spec`, `Started` and `Exit`.
- `command.go` — `CheckCommand`, and the command lookup on the submitter's `PATH` that the shim and the check share.
- `signature.go` — `SignatureID`, `Signature`, `AfterNone`, `NewSignature`, `Normalize`, `RepoName`.
- `repo.go` — `FindRepo`: the repository, the folder within it and the checkout.
- `belief.go` — `Outcome`, `Run`, `RunOf`, `Prior`, `PriorScore`, `Note`, `AdvisorMarks`, `Belief`, `Beliefs`.
- `beliefs_store.go` — `LoadBeliefs`, `SaveBeliefs`, `ErrNewerBeliefs`.
- `size.go` — `Source`, `ClaimSources`, `Sized`, `Size`, `JoinAnd`.
- `durations.go` — `Usual`, `OverdueAfter`.
- `scaling.go` — `Scaling`, `ScalingOf`.
- `grant.go` — `SpareWidth`, `Grant`.
- `profile.go` — `TracePoint`, `Trace`, `Step`, `Staircase`, `Profile`, `BuildProfile`, `ProfiledRuns`.
- `curve.go` — `CurveStep`, `Curve`, `BuildCurve`, `Curve.Validate`, `CurvedRuns`.
- `predict.go` — `Span`, `Prediction`, `Flat`, `Predict`, `PeakSum`, `WithMarginKB`.
- `greedy.go` — `Greedy`, `Budget`, `AdmitGreedy` and its fitter.
- `hold.go` — `HoldState`, `Hold`.
- `ioacct.go`, `ioacct_linux.go`, `ioacct_other.go` — the shim's `/proc/self/io` counters.
- `surprise.go` — `NeedsPrior`, `Surprise`, `ScorePrior`: when the advisor is asked, and how its priors are scored.
- `forecast.go` — `Forecast`.
- `beliefs_view.go` — `BeliefQuery`, `BeliefView`, `ViewBelief`, `Track`, `TrackRecord`, `BeliefsPage`, `Belief.Clone`.
- `belief_text.go` — `FormatBelief`, `RoughDuration`.
- `units.go` — `SliceName`, `ScopeName`, `CPUWeight`: the systemd names and weights of a loom's jobs.
- `cgroup.go` — `CgroupRoot`, `CgroupSample`, `ReadCgroup`, `CgroupKill`, `SelfCgroup`.
- `meminfo_linux.go`, `meminfo_other.go` — `ReadMemAvailableKB`.
- `confine.go`, `confine_linux.go`, `confine_other.go` — `ScopeSpec`, `SliceProps`, `Confiner`, `NewConfiner`: the user's systemd over D-Bus (go-systemd).
- `testmain_test.go` — isolates the loom dirs for the package's tests.

## Jobs

A job is `held` while it waits for its prerequisite (`Record.After`, [dependent jobs](../docs/superpowers/specs/2026-11-05-dependent-jobs-design.md)), then `queued`, then `running`, then one of four finished states: `done` (exit 0), `failed` (a non-zero exit, a signal, or a start that failed), `cancelled` (cancelled by anyone, whatever its exit) or `lost` (its shim vanished without writing its result). `State.Finished` is true for the last four, and `State.Waiting` for `held` and `queued`. A `JobID` is per host, from 1, and never reused.

A `Submit` is what a client sends; the model validates it (`Submit.Validate` for the bounds, `CheckClaim` for the caps), applies the default claim and stores a `Record`, which is everything about the job but its environment. A `Record`'s `Order` is its ID at submit and the lowest queued `Order` minus 1 after a move to the front, and the queue runs lowest first. `Ready` is when a job joined the queue: at its submit, or at its release for a dependent; `QueuedSince` falls back on `Submitted` for a held job and for a record from an earlier build.

## Claims, caps and the policy

A `Claim` is whole cores and whole GB, whether the job is I/O-heavy (one of the `io_jobs` slots), or `Exclusive` (every cap, as the caps stand when it is admitted). `Claim.Need` turns a claim into what it takes from the `Caps`. `Resolve` turns the `scheduler` block of the global `config.json` and the `Host` into a `Policy`:

- a field that is 0 or absent takes the host's default: `cpus` is max(1, cores − 2); `memory_gb` is max(1, ⌊0.75 × RAM in GB⌋), with a GB being 2^30 bytes; `io_jobs` is 2; the default claim is {min(4, cpus), min(6, memory_gb)}; `starve_after_minutes` is 10; `keep_finished` is 50;
- the bounds apply once a default is taken: `cpus` 1–1024, `memory_gb` 1–65536, `io_jobs` 1–64, `starve_after_minutes` 1–1440, `keep_finished` 1–10000, each default claim value 1 up to its cap;
- a value outside its bounds, or an unreadable RAM with `memory_gb` unset, is an error and no `Policy`: the model pauses admission and says why.

## Admission

`Admit(jobs, policy, now)` returns the jobs to start and, for each queued job left waiting, why. It walks the queued jobs in queue order: a job over a cap is skipped (`over this host's caps`) and is never the head; once the head, the first job not skipped, has waited `StarveAfter` since it joined the queue (`QueuedSince`), it starts if it fits and nothing else starts (the others wait `waits for job <id>, which has waited past <m>m`); otherwise every job that fits what the running jobs and the earlier starts leave starts, first fit. A job that does not fit waits for cores (`needs <n> cores (<free> free)`), then GB (`needs <n> GB (<free> free)`), then I/O slots (`needs an I/O slot`, or `needs <n> I/O slots (<free> free)` for a need of more than one). A held job is never seen.

## Greedy admission

While jobs are contained ([cgroup spec](../docs/superpowers/specs/2026-11-05-cgroup-greedy-scheduler-design.md) §7) the model admits with `AdmitGreedy`, which walks the queue as `Admit` does (`admitWith`: the over-cap skip, the starvation guard, `Decision.Starved` naming a head it started) with another fitter. `Budget` is the running jobs' use plus the host's MemAvailable, less `ReserveGB`, between 0 and the memory cap. A `Prediction` is a job's memory over time, non-overlapping spans: `Predict` reads a belief's curves (3 runs that lasted past the progress asked about), each step from the wall time its run reached it, less the run's wall time at the current progress, its level given 15% and its edges widened by max(30 s, a quarter of how far ahead), the most of every run at each moment; `Flat` is a claim's. A job fits when the sum of every prediction (`PeakSum`, at each span's start) stays within the budget until its own prediction ends; a job also waits while the slice's CPU pressure is at `CPUPressurePct` with a job running, or while claimed cores would pass `CPUOvercommit` × the cores cap; an exclusive claim waits for an empty host; `Greedy.Holdoff` holds every start. `ForecastGreedy` runs the same fit forward, predicting a simulated start from its simulated start.

## Signatures, beliefs and sizing

The smart scheduler's half ([spec](../docs/superpowers/specs/2026-11-05-smart-scheduler-design.md)): what each command measured, and the claim that follows.

- **Signature.** `FindRepo` finds the job's repository from git's files alone (the first `.git` up from its folder; a worktree's `gitdir:` and its `commondir` lead to the common dir), so every worktree of a repository shares one; `Normalize` strips wrapper programs (`env` with only `NAME=value` operands, `direnv exec DIR`, `nix develop|shell … -c`, `time`, `nice`, `ionice`) and keeps their pairs apart, sorted. A signature is the repo, the folder within it, the command and the pairs; its ID is 12 hex digits of their SHA-256. A dependent's signature also carries its prerequisite's (`After`; `AfterNone` when that one had none), hashed only when set, so every other ID is what it always was. `FindRepo` also gives the checkout, the directory holding the `.git` (symlinks resolved): a record and its run keep it (`Checkout`), apart from the signature, which every checkout of a repository shares.
- **Runs.** `RunOf` turns a finished record into a run: a done run teaches memory (its peak, the larger of the sampled group peak and `max_rss`), cores (`cpu_seconds` over wall time) and I/O (`write_bytes`); a run that failed with an exit code teaches only its duration and gives its peak as a floor; a signal nobody requested or a cancel gives a floor only; an adopted run's peak is a floor; a start failure or a lost job teaches nothing.
- **Size.** `Size(asked, belief, policy)` takes each part from the first source with a value: the flag, the belief's last 10 runs, its prior, the default claim. Memory is the largest peak plus 15% (a floor only raises it), held to the cap; cores are 1.25 × the median busy cores, at most the default claim's; an I/O slot goes to a median write of 2 GiB or more. An overrunner's cores (its runs keep more than 1.25 × their cores busy: the hint variables don't reach it) are 1.25 × its median busy cores, up to the cap. A run that had spare cores counts toward memory as a floor scaled to the claim's cores while the last 10 runs include a full peak without spare cores, and as it is without one. `--cpus` with `--mem` makes the whole claim explicit. `ClaimSources.Tag` and `Describe` say where the parts came from.
- **Durations.** `Usual` is the median and P90 of the last 10 runs that exited (done, or with an exit code), each wall time scaled from the cores it had to the cores asked about (none at 0), or the prior's duration; `OverdueAfter` is max(2 × P90, P90 + 5 min) once there are three.
- **Scaling.** `ScalingOf` reads a belief's done runs for Amdahl's parallel fraction (each run with 2 cores or more gives (1 − 1/busy) ÷ (1 − 1/cores); the median), the ideal width (the most cores above 50% efficiency, at most twice the most cores a run had) and whether the command overruns ([spec](../docs/superpowers/specs/2026-11-05-job-scaling-design.md)).
- **The grant.** `Grant` runs on what `Admit` starts: while no queued job waits but over the caps, each starting job `SpareWidth` can widen takes spare cores up to its ideal width, from the caps less the running claims, the starting base claims and a reserve of the default claim, with memory in proportion (lowered to what a run at that width measured).
- **Holds** ([spec](../docs/superpowers/specs/2026-11-05-past-peak-holds-design.md)). The model keeps a `Trace` of each running job's samples: its progress (the CPU-seconds its session has used, which never falls), its memory and its busy cores, halved to 128 points by pairs (the later progress, the higher usage) past 256, and incomplete after a gap over 30 s. At a done run's settle, `BuildProfile` turns a complete trace into a `Profile`: for memory (KB, rounded up to MB) and cores (milli-cores), the most the run still used from each point of its progress to its end, a falling `Staircase` of at most 8 steps whose merges keep the higher level, so it is never below the run, with the progress of its first peak and of its end, each rounded up to 0.1. `Hold` gives a running job's hold: its claim unless (1) its belief has 3 valid profiles among its newest 5 runs and none of its last 10 runs broke a hold; (2) the trace is complete and fresh, the job wasn't adopted, the switch is on (`Policy.ReleasePastPeaks`) and the claim is neither exclusive nor fully explicit; and then, per resource, (3) the job has reached 90% of the lowest profiled peak and is past every live run's peak, (4) some profiled run lasted past its progress (the envelope is the highest step among those runs), and (5) its last 2 points are within the envelope now. The hold is the envelope plus 15% (memory) or 25% (cores), rounded up, at least 1, below the claim; once released it only falls, and rules 3 and 5 stop counting, while a failing rule 1, 2 or 4 counts the claim for that pass and keeps the state. `HoldState.Exceeded` names a released resource whose usage passed its hold, which the model turns into `Record.HoldBroke`. `Admit`, `Grant` and `Forecast` count a running job at its hold when they are given one (never an exclusive claim's).
- **Curves** ([cgroup spec](../docs/superpowers/specs/2026-11-05-cgroup-greedy-scheduler-design.md) §6). Beside its profile, a done run's complete trace becomes a `Curve` (`BuildCurve`): per resource, the highest level within each stretch of progress, rise and fall both, at most 16 steps merged by the least added area with the higher level kept (never below the run), each step `[until, level, wall]` with the wall seconds since the job's start at its end. Each trace point keeps its time (`TracePoint.At`). The newest 5 runs keep their curves; `CurvedRuns` is the valid ones.
- **The forecast.** `Forecast` runs `Admit` forward from now over the running jobs' expected ends (start plus usual duration, or a minute from now when that has passed) and gives each queued job its expected start; jobs being sized are left out, a held job joins the simulated queue at its prerequisite's expected end, at the lower of the two orders (`releaseHeld`), and it stops after 1,000 steps or 24 hours.
- **Reads.** `BeliefsPage` is what `ReadBeliefs` answers: `ViewBelief` adds the repo's name, the claim a job claiming nothing would get now and the durations; `TrackRecord` sums how the advisor's priors scored against their first runs; `FormatBelief` is the text `loom job beliefs show` and the work view's `b` print, its scaling line included (the parallel fraction, the ideal width and the usual duration at the claim's cores and at that width; or an overrunner's busy cores).
- **The argv check.** `CheckCommand` refuses a one-word command with spaces that names no program on the submitter's `PATH` (or, with a slash, no file in the job's folder).

## On disk

Everything is under `<globalDir>/jobs/`, in directories of mode 0700 and files of mode 0600, written atomically.

| Path | Holds |
|---|---|
| `beliefs.json` | `Beliefs`: `{"version": N, "beliefs": [...]}`, N being 2 while a belief's signature has a prerequisite part and 1 otherwise, so an older loom still reads a registry with none; at most 1,000 beliefs (least recently seen dropped), 20 runs and 5 notes each; the newest 5 runs of each carry their `profile` (`[until, level]` steps) and `curve` (`[until, level, wall]` steps), and a run whose usage passed its hold says `hold_broke`. `LoadBeliefs` never writes, and leaves a file that fails to load (`ErrNewerBeliefs` for a newer version, an ID that is not its signature's, a repeat) as it is |
| `jobs.json` | `Store`: `{"version": 1, "next_id": N, "jobs": [...]}`. `LoadStore` never writes, leaves a file that fails to load alone, reports a newer version as `ErrNewerStore`, and raises `next_id` past every ID in the file and past every numeric directory under the jobs dir (a missing `jobs.json` included), failing the load when the dir can't be listed |
| `<id>/spec.json` | `Spec`: argv, cwd, the submitter's environment, the parallelism the start hints, and the scope to join (`scope`, when the job starts contained) |
| `<id>/output.log` | the command's stdout and stderr |
| `<id>/run.lock` | the shim's lock, held while the job runs |
| `<id>/started.json` | `Started`: the shim's pid and start time, and the scope it joined with the cgroup it landed in (`unit`, `cgroup`), or why it runs uncontained (`uncontained`) |
| `<id>/exit.json` | `Exit`: the exit code or signal, the end time, the command's max RSS, its CPU seconds, on Linux the bytes it read and wrote (from `/proc/self/io` after the shim reaps it), and the start error if the exec failed |

`JobDir(dir, id)` is `<dir>/<id>`, `CreateJobDir` makes it only if it does not exist (a submit never writes into a directory it did not create), and `RemoveJobDir` removes it when retention drops a finished job.

## The runtime

The process half of the package: it starts a job's runner, tests whether the runner lives, signals the job's process group, measures it and reads the host. It keeps no record of any job; `core` decides what each answer means.

| File | Holds |
|---|---|
| `shim.go` | `Runner`, `NewRunner`, `ErrUnsupported` |
| `shim_unix.go` | `RunShim`, `Runner.Start`, `Probe`, `SignalGroup` |
| `shim_windows.go` | the same four names, each answering `ErrUnsupported` |
| `prio_linux.go`, `prio_other.go` | the shim's nice value and, on Linux, its I/O class |
| `sample.go` | `Proc`, `Snapshot`, `ParsePS`, `parseProcStat`, `Snapshot.Group`, `Snapshot.Session` |
| `sample_linux.go`, `sample_other.go` | `Sample`: from `/proc` on Linux, from `ps` elsewhere |
| `host.go`, `host_linux.go`, `host_darwin.go`, `host_other.go` | `DetectHost` and the RAM readers |
| `testshim.go` | `ShimTestEnv`, `MaybeRunTestShim`, `TestRunner()` |

**The shim.** `loom job-shim <job dir>` (`job_shim.go` in the root package) calls `RunShim`. The daemon starts it with `Runner.Start` as `<loom> job-shim <job dir>`, a child in a session of its own (`Setsid`), so it leads a process group whose id is its pid. Its stdin is `/dev/null`, and its stdout and stderr are appended to `output.log`. It:
1. refuses to run unless it leads its group;
2. reads `spec.json` and sets nice 10 and, on Linux, the best-effort I/O class at level 7, on the thread that will start the command, so the command and its children inherit both;
3. takes an exclusive `flock` on `run.lock`, opened close-on-exec so nothing it starts holds the lock; when `spec.json` names a scope, joins it (`joinScope`: `StartTransientUnit` with its own pid, in the slice, with `CPUWeight` from the claim, `OOMPolicy=kill` and `CollectMode=inactive-or-failed`, bounded by 2 s), and runs uncontained, saying why, when it can't; and writes `started.json`;
4. starts the command with no shell, in the job's folder, with the environment of `spec.json` plus the hints (`Hints`: the parallelism variables, and `SCCACHE_CLIENT_SIDE=1`, so sccache runs each compile in its client, inside the job's tree, rather than in its shared daemon), found on that environment's `PATH`;
5. catches SIGTERM, SIGINT and SIGHUP and passes none on, since a signal sent to the group reaches the command itself; a SIGTERM that arrives before the command starts is not checked for, since Go delivers signals asynchronously and no check is reliable; the command starts anyway and the daemon's SIGKILL escalation, 10 s after the cancel, ends it and the job settles as cancelled;
6. writes `exit.json` (a signal ends a job with code -1 and the name `unix.SignalName` gives it, a command that cannot be executed with code 127 and the error) and returns, which releases the lock.

A shim that cannot run the job writes an `exit.json` with code 127 and the reason, and returns 1. A shim that finds `started.json` already there is a duplicate for a job that has run: it returns 1 and writes nothing. It prints to its stderr, which is `output.log`.

**Asking about a job.** `Probe` is a non-blocking `flock` test of `run.lock`, released at once: held means the shim lives, free means it is gone, and no `run.lock` yet also reads free, since no shim has taken it. `SignalGroup(pgid, sig)` is `kill(-pgid, sig)`; it refuses a pgid of 1 or below and the caller's own group, and answers `ESRCH` for a group that is gone. Neither proves that a group is still the job's: the caller does, with the lock held, or with a `Sample` showing members in the group and no process holding the shim's pid.

**Usage.** On Linux `Sample` reads `/proc/<pid>/stat` for every process (`parseProcStat`): its group, its session, its resident memory, and its CPU time with its waited-for children's, so a session's sum keeps the processes reaped inside it and a short-lived compile still counts ([spec](../docs/superpowers/specs/2026-11-05-sccache-measurement-design.md)). Elsewhere it runs `ps -A -o pid=,pgid=,rss=,time=` once, and `ParsePS` reads the procps time column (`[d-]hh:mm:ss`) and the BSD one (`[[h:]m:]s.cc`), with a process's session taken to be its group. A record it cannot read fails the whole sample. `Snapshot.Session(sid)` sums a session's memory and CPU time and counts its members: a job's usage, since the shim leads its session and a child that changes group (coreutils `timeout`) stays in it. `Snapshot.Group(pgid)` does the same for a group and says whether any process has the group's id as its pid: the lost-job proof, since a signal reaches the group. `DetectHost` reads the host's name, `runtime.NumCPU()` and its RAM (`/proc/meminfo`, or the sysctl `hw.memsize` through `unix.SysctlUint64`); a RAM it cannot read stays 0.

**Confinement** ([spec](../docs/superpowers/specs/2026-11-05-cgroup-greedy-scheduler-design.md)). On Linux a job's shim joins a systemd scope of its own, `loom-<tag>-job-<id>-<attempt>.scope`, in its loom's slice, `loom_<tag>.slice`, the tag being 8 hex digits of the SHA-256 of the jobs directory (`SliceName`, `ScopeName`); the slice has no dash, so it sits directly under the user's manager beside the agents' panes. `Confiner` is the user's systemd as the daemon drives it (`JoinScope`, `EnsureSlice`, `SetMemoryHigh`, `Freeze`, `Thaw`); every call waits on D-Bus. `ReadCgroup` reads a cgroup's working set (`memory.current` less `inactive_file`), `memory.high`, CPU (`usage_usec`), pressure (`some` avg10), `oom_kill` and `cgroup.events`, refusing a path that is not clean and absolute; `CgroupKill` writes `cgroup.kill` only for a loom job's scope.

**Windows.** The stubs answer `ErrUnsupported`: sessions, process groups and `flock` have no counterpart here.

**Tests.** A test binary has no `job-shim` command, so a package whose tests start real shims calls `MaybeRunTestShim()` first in its `TestMain` and builds its runner with `TestRunner()`: the test binary, run with `ShimTestEnv` set to `1`, becomes the shim.
