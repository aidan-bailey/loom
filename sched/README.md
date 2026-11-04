# sched

The work scheduler's package. This file covers the logic half: the job types, the caps and policy, the admission pass, the parallelism hints, and the `jobs.json` store with the per-job files. The model (`core`) owns the queue, persists it under `<globalDir>/jobs/` and calls this package from its loop; nothing here keeps state of its own, so every function takes the host, the policy and the clock as arguments. The design is in [`docs/superpowers/specs/2026-11-04-work-scheduler-design.md`](../docs/superpowers/specs/2026-11-04-work-scheduler-design.md).

## Files

- `doc.go` — the package comment.
- `job.go` — `JobID`, `State`, `Claim`, `Caps`, `Need`, `Host`, `Policy`, `Notice`, `Record`, `Exit` and `Submit`, with `Claim.Need`, `Submit.Validate` and `CheckClaim`.
- `policy.go` — `Resolve`: a `config.SchedulerSettings` and a `Host` in, a `Policy` or an error out.
- `admit.go` — `Admit` and its `Decision`.
- `hints.go` — `Hints`.
- `store.go` — `Store`, `LoadStore`, `SaveStore` and `ErrNewerStore`.
- `files.go` — the file names, `JobDir`, `RemoveJobDir`, and the codecs of `Spec`, `Started` and `Exit`.
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
| `jobs.json` | `Store`: `{"version": 1, "next_id": N, "jobs": [...]}`. `LoadStore` never writes, leaves a file that fails to load alone, reports a newer version as `ErrNewerStore`, and raises `next_id` past every ID on disk |
| `<id>/spec.json` | `Spec`: argv, cwd, the submitter's environment, and the parallelism the start hints |
| `<id>/output.log` | the command's stdout and stderr |
| `<id>/run.lock` | the shim's lock, held while the job runs |
| `<id>/started.json` | `Started`: the shim's pid and start time |
| `<id>/exit.json` | `Exit`: the exit code or signal, the end time, the command's max RSS, and the start error if the exec failed |

`JobDir(dir, id)` is `<dir>/<id>`, and `RemoveJobDir` removes it when retention drops a finished job.
