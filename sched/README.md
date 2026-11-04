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

## On disk

Everything is under `<globalDir>/jobs/`, in directories of mode 0700 and files of mode 0600, written atomically.

| Path | Holds |
|---|---|
| `jobs.json` | `Store`: `{"version": 1, "next_id": N, "jobs": [...]}`. `LoadStore` never writes, leaves a file that fails to load alone, reports a newer version as `ErrNewerStore`, and raises `next_id` past every ID in the file and past every numeric directory under the jobs dir (a missing `jobs.json` included), failing the load when the dir can't be listed |
| `<id>/spec.json` | `Spec`: argv, cwd, the submitter's environment, and the parallelism the start hints |
| `<id>/output.log` | the command's stdout and stderr |
| `<id>/run.lock` | the shim's lock, held while the job runs |
| `<id>/started.json` | `Started`: the shim's pid and start time |
| `<id>/exit.json` | `Exit`: the exit code or signal, the end time, the command's max RSS, and the start error if the exec failed |

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
5. catches SIGTERM, SIGINT and SIGHUP and passes none on, since a signal sent to the group reaches the command itself; one that arrives before the command starts is lost, and the daemon's SIGKILL escalation covers it;
6. writes `exit.json` (a signal ends a job with code -1 and the name `unix.SignalName` gives it, a command that cannot be executed with code 127 and the error) and returns, which releases the lock.

A shim that cannot run the job writes an `exit.json` with code 127 and the reason, and returns 1. It prints to its stderr, which is `output.log`.

**Asking about a job.** `Probe` is a non-blocking `flock` test of `run.lock`, released at once: held means the shim lives, free means it is gone, and no `run.lock` yet also reads free, since no shim has taken it. `SignalGroup(pgid, sig)` is `kill(-pgid, sig)`; it refuses a pgid of 1 or below and the caller's own group, and answers `ESRCH` for a group that is gone. Neither proves that a group is still the job's: the caller does, with the lock held, or with a `Sample` showing members in the group and no process holding the shim's pid.

**Usage.** `Sample` runs `ps -A -o pid=,pgid=,rss=,time=` once. `ParsePS` reads the procps time column (`[d-]hh:mm:ss`) and the BSD one (`[[h:]m:]s.cc`), and fails the whole sample on a line it cannot read. `Snapshot.Group(pgid)` sums a group's memory and CPU time, counts its members, and says whether any process has the group's id as its pid. `DetectHost` reads the host's name, `runtime.NumCPU()` and its RAM (`/proc/meminfo`, or `sysctl hw.memsize` with a 5 s limit); a RAM it cannot read stays 0.

**Windows.** The stubs answer `ErrUnsupported`: sessions, process groups and `flock` have no counterpart here.

**Tests.** A test binary has no `job-shim` command, so a package whose tests start real shims calls `MaybeRunTestShim()` first in its `TestMain` and builds its runner with `TestRunner()`: the test binary, run with `ShimTestEnv` set to `1`, becomes the shim.
