# sched

The work scheduler's package. This file covers the logic half: the job types, the caps and policy, the admission pass, the parallelism hints, and the `jobs.json` store with the per-job files. The model (`core`) owns the queue, persists it under `<globalDir>/jobs/` and calls this package from its loop; nothing here keeps state of its own, so every function takes the host, the policy and the clock as arguments. The design is in [`docs/superpowers/specs/2026-11-04-work-scheduler-design.md`](../docs/superpowers/specs/2026-11-04-work-scheduler-design.md).

## Files

- `doc.go` — the package comment.
- `job.go` — `JobID`, `State`, `Claim`, `Caps`, `Need`, `Host`, `Policy`, `Notice`, `Record`, `Exit` and `Submit`, with `Claim.Need`, `Submit.Validate` and `CheckClaim`.
- `policy.go` — `Resolve`: a `config.SchedulerSettings` and a `Host` in, a `Policy` or an error out.
- `admit.go` — `Admit` and its `Decision`.
- `hints.go` — `Hints`.
- `store.go` — `Store`, `LoadStore`, `SaveStore` and `ErrNewerStore`.
- `files.go` — the file names, `JobDir`, `CreateJobDir`, `RemoveJobDir`, and the codecs of `Spec`, `Started` and `Exit`.
- `command.go` — `CheckCommand`, and the command lookup on the submitter's `PATH` that the shim and the check share.
- `signature.go` — `SignatureID`, `Signature`, `NewSignature`, `Normalize`, `RepoName`.
- `repo.go` — `FindRepo`.
- `belief.go` — `Outcome`, `Run`, `RunOf`, `Prior`, `PriorScore`, `Note`, `AdvisorMarks`, `Belief`, `Beliefs`.
- `beliefs_store.go` — `LoadBeliefs`, `SaveBeliefs`, `ErrNewerBeliefs`.
- `size.go` — `Source`, `ClaimSources`, `Sized`, `Size`.
- `durations.go` — `Usual`, `OverdueAfter`.
- `scaling.go` — `Scaling`, `ScalingOf`.
- `grant.go` — `SpareWidth`, `Grant`.
- `ioacct.go`, `ioacct_linux.go`, `ioacct_other.go` — the shim's `/proc/self/io` counters.
- `surprise.go` — `NeedsPrior`, `Surprise`, `ScorePrior`: when the advisor is asked, and how its priors are scored.
- `forecast.go` — `Forecast`.
- `beliefs_view.go` — `BeliefQuery`, `BeliefView`, `ViewBelief`, `Track`, `TrackRecord`, `BeliefsPage`, `Belief.Clone`.
- `belief_text.go` — `FormatBelief`, `RoughDuration`.
- `testmain_test.go` — isolates the loom dirs for the package's tests.

## Jobs

A job is `queued`, then `running`, then one of four finished states: `done` (exit 0), `failed` (a non-zero exit, a signal, or a start that failed), `cancelled` (cancelled by anyone, whatever its exit) or `lost` (its shim vanished without writing its result). `State.Finished` is true for the last four. A `JobID` is per host, from 1, and never reused.

A `Submit` is what a client sends; the model validates it (`Submit.Validate` for the bounds, `CheckClaim` for the caps), applies the default claim and stores a `Record`, which is everything about the job but its environment. A `Record`'s `Order` is its ID at submit and the lowest queued `Order` minus 1 after a move to the front, and the queue runs lowest first.

## Claims, caps and the policy

A `Claim` is whole cores and whole GB, whether the job is I/O-heavy (one of the `io_jobs` slots), or `Exclusive` (every cap, as the caps stand when it is admitted). `Claim.Need` turns a claim into what it takes from the `Caps`. `Resolve` turns the `scheduler` block of the global `config.json` and the `Host` into a `Policy`:

- a field that is 0 or absent takes the host's default: `cpus` is max(1, cores − 2); `memory_gb` is max(1, ⌊0.75 × RAM in GB⌋), with a GB being 2^30 bytes; `io_jobs` is 2; the default claim is {min(4, cpus), min(6, memory_gb)}; `starve_after_minutes` is 10; `keep_finished` is 50;
- the bounds apply once a default is taken: `cpus` 1–1024, `memory_gb` 1–65536, `io_jobs` 1–64, `starve_after_minutes` 1–1440, `keep_finished` 1–10000, each default claim value 1 up to its cap;
- a value outside its bounds, or an unreadable RAM with `memory_gb` unset, is an error and no `Policy`: the model pauses admission and says why.

## Admission

`Admit(jobs, policy, now)` returns the jobs to start and, for each queued job left waiting, why. It walks the queued jobs in queue order: a job over a cap is skipped (`over this host's caps`) and is never the head; once the head, the first job not skipped, has waited `StarveAfter` since it was submitted, it starts if it fits and nothing else starts (the others wait `waits for job <id>, which has waited past <m>m`); otherwise every job that fits what the running jobs and the earlier starts leave starts, first fit. A job that does not fit waits for cores (`needs <n> cores (<free> free)`), then GB (`needs <n> GB (<free> free)`), then I/O slots (`needs an I/O slot`, or `needs <n> I/O slots (<free> free)` for a need of more than one).

## Signatures, beliefs and sizing

The smart scheduler's half ([spec](../docs/superpowers/specs/2026-11-05-smart-scheduler-design.md)): what each command measured, and the claim that follows.

- **Signature.** `FindRepo` finds the job's repository from git's files alone (the first `.git` up from its folder; a worktree's `gitdir:` and its `commondir` lead to the common dir), so every worktree of a repository shares one; `Normalize` strips wrapper programs (`env` with only `NAME=value` operands, `direnv exec DIR`, `nix develop|shell … -c`, `time`, `nice`, `ionice`) and keeps their pairs apart, sorted. A signature is the repo, the folder within it, the command and the pairs; its ID is 12 hex digits of their SHA-256.
- **Runs.** `RunOf` turns a finished record into a run: a done run teaches memory (its peak, the larger of the sampled group peak and `max_rss`), cores (`cpu_seconds` over wall time) and I/O (`write_bytes`); a run that failed with an exit code teaches only its duration and gives its peak as a floor; a signal nobody requested or a cancel gives a floor only; an adopted run's peak is a floor; a start failure or a lost job teaches nothing.
- **Size.** `Size(asked, belief, policy)` takes each part from the first source with a value: the flag, the belief's last 10 runs, its prior, the default claim. Memory is the largest peak plus 15% (a floor only raises it), held to the cap; cores are 1.25 × the median busy cores, at most the default claim's; an I/O slot goes to a median write of 2 GiB or more. An overrunner's cores (its runs keep more than 1.25 × their cores busy: the hint variables don't reach it) are 1.25 × its median busy cores, up to the cap. A run that had spare cores counts toward memory as a floor scaled to the claim's cores while the last 10 runs include a full peak without spare cores, and as it is without one. `--cpus` with `--mem` makes the whole claim explicit. `ClaimSources.Tag` and `Describe` say where the parts came from.
- **Durations.** `Usual` is the median and P90 of the last 10 runs that exited (done, or with an exit code), each wall time scaled from the cores it had to the cores asked about (none at 0), or the prior's duration; `OverdueAfter` is max(2 × P90, P90 + 5 min) once there are three.
- **Scaling.** `ScalingOf` reads a belief's done runs for Amdahl's parallel fraction (each run with 2 cores or more gives (1 − 1/busy) ÷ (1 − 1/cores); the median), the ideal width (the most cores above 50% efficiency, at most twice the most cores a run had) and whether the command overruns ([spec](../docs/superpowers/specs/2026-11-05-job-scaling-design.md)).
- **The grant.** `Grant` runs on what `Admit` starts: while no queued job waits but over the caps, each starting job `SpareWidth` can widen takes spare cores up to its ideal width, from the caps less the running claims, the starting base claims and a reserve of the default claim, with memory in proportion (lowered to what a run at that width measured).
- **The forecast.** `Forecast` runs `Admit` forward from now over the running jobs' expected ends (start plus usual duration, or a minute from now when that has passed) and gives each queued job its expected start; jobs being sized are left out, and it stops after 1,000 steps or 24 hours.
- **Reads.** `BeliefsPage` is what `ReadBeliefs` answers: `ViewBelief` adds the repo's name, the claim a job claiming nothing would get now and the durations; `TrackRecord` sums how the advisor's priors scored against their first runs; `FormatBelief` is the text `loom job beliefs show` and the work view's `b` print, its scaling line included (the parallel fraction, the ideal width and the usual duration at the claim's cores and at that width; or an overrunner's busy cores).
- **The argv check.** `CheckCommand` refuses a one-word command with spaces that names no program on the submitter's `PATH` (or, with a slash, no file in the job's folder).

## On disk

Everything is under `<globalDir>/jobs/`, in directories of mode 0700 and files of mode 0600, written atomically.

| Path | Holds |
|---|---|
| `beliefs.json` | `Beliefs`: `{"version": 1, "beliefs": [...]}`, at most 1,000 beliefs (least recently seen dropped), 20 runs and 5 notes each. `LoadBeliefs` never writes, and leaves a file that fails to load (`ErrNewerBeliefs` for a newer version, an ID that is not its signature's, a repeat) as it is |
| `jobs.json` | `Store`: `{"version": 1, "next_id": N, "jobs": [...]}`. `LoadStore` never writes, leaves a file that fails to load alone, reports a newer version as `ErrNewerStore`, and raises `next_id` past every ID in the file and past every numeric directory under the jobs dir (a missing `jobs.json` included), failing the load when the dir can't be listed |
| `<id>/spec.json` | `Spec`: argv, cwd, the submitter's environment, and the parallelism the start hints |
| `<id>/output.log` | the command's stdout and stderr |
| `<id>/run.lock` | the shim's lock, held while the job runs |
| `<id>/started.json` | `Started`: the shim's pid and start time |
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
| `sample.go` | `Sample`, `ParsePS`, `Snapshot.Group` |
| `host.go`, `host_linux.go`, `host_darwin.go`, `host_other.go` | `DetectHost` and the RAM readers |
| `testshim.go` | `ShimTestEnv`, `MaybeRunTestShim`, `TestRunner()` |

**The shim.** `loom job-shim <job dir>` (`job_shim.go` in the root package) calls `RunShim`. The daemon starts it with `Runner.Start` as `<loom> job-shim <job dir>`, a child in a session of its own (`Setsid`), so it leads a process group whose id is its pid. Its stdin is `/dev/null`, and its stdout and stderr are appended to `output.log`. It:
1. refuses to run unless it leads its group;
2. reads `spec.json` and sets nice 10 and, on Linux, the best-effort I/O class at level 7, on the thread that will start the command, so the command and its children inherit both;
3. takes an exclusive `flock` on `run.lock`, opened close-on-exec so nothing it starts holds the lock, and writes `started.json`;
4. starts the command with no shell, in the job's folder, with the environment of `spec.json` plus the parallelism hints (`Hints`), found on that environment's `PATH`;
5. catches SIGTERM, SIGINT and SIGHUP and passes none on, since a signal sent to the group reaches the command itself; a SIGTERM that arrives before the command starts is not checked for, since Go delivers signals asynchronously and no check is reliable; the command starts anyway and the daemon's SIGKILL escalation, 10 s after the cancel, ends it and the job settles as cancelled;
6. writes `exit.json` (a signal ends a job with code -1 and the name `unix.SignalName` gives it, a command that cannot be executed with code 127 and the error) and returns, which releases the lock.

A shim that cannot run the job writes an `exit.json` with code 127 and the reason, and returns 1. A shim that finds `started.json` already there is a duplicate for a job that has run: it returns 1 and writes nothing. It prints to its stderr, which is `output.log`.

**Asking about a job.** `Probe` is a non-blocking `flock` test of `run.lock`, released at once: held means the shim lives, free means it is gone, and no `run.lock` yet also reads free, since no shim has taken it. `SignalGroup(pgid, sig)` is `kill(-pgid, sig)`; it refuses a pgid of 1 or below and the caller's own group, and answers `ESRCH` for a group that is gone. Neither proves that a group is still the job's: the caller does, with the lock held, or with a `Sample` showing members in the group and no process holding the shim's pid.

**Usage.** `Sample` runs `ps -A -o pid=,pgid=,rss=,time=` once. `ParsePS` reads the procps time column (`[d-]hh:mm:ss`) and the BSD one (`[[h:]m:]s.cc`), and fails the whole sample on a line it cannot read. `Snapshot.Group(pgid)` sums a group's memory and CPU time, counts its members, and says whether any process has the group's id as its pid. `DetectHost` reads the host's name, `runtime.NumCPU()` and its RAM (`/proc/meminfo`, or the sysctl `hw.memsize` through `unix.SysctlUint64`); a RAM it cannot read stays 0.

**Windows.** The stubs answer `ErrUnsupported`: sessions, process groups and `flock` have no counterpart here.

**Tests.** A test binary has no `job-shim` command, so a package whose tests start real shims calls `MaybeRunTestShim()` first in its `TestMain` and builds its runner with `TestRunner()`: the test binary, run with `ShimTestEnv` set to `1`, becomes the shim.
