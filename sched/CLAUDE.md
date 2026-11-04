# sched

The work scheduler's package: the job, claim, caps and policy types, `Resolve` (the host's defaults and the bounds of the `scheduler` config block), `Admit` (first fit with a starvation guard, saying why each job waits), `Hints` (the parallelism variables), the submission checks, and the `jobs.json` store with the per-job files; and the runtime that hosts a running job (the detached `loom job-shim` process, the lock probe, group signals, the usage sampler and host detection). The model in `core` owns the queue and calls this package from its loop; the logic half runs no process, and nothing in the package keeps state of its own.

## Rules when modifying this package

- **Keep every bound of a submission in `Submit.Validate` and `CheckClaim`, with the texts written there.** The model runs them for every client, so the CLI, the work view and a script meet the same bounds; a client that checks its own lets them drift, and a reworded text silently stops matching the tests and the CLI output that quote it. **Enforced** for the bounds and texts by `TestSubmit_Validate`, `TestSubmit_ValidateReportsTheFirstFault` and `TestCheckClaim` (`sched/job_test.go`); **Convention** for other packages' matches — a test elsewhere that matches a stale text.
- **Make `Resolve` fail closed: a field outside its bounds, a default claim above a cap, and an unreadable RAM with `memory_gb` unset are errors, never a clamp or a guessed cap.** The model pauses admission on the error and the view says why; a clamped or guessed cap admits work the owner did not allow, or over-commits the host, and nothing says so. **Enforced** by `TestResolve_Bounds` and `TestResolve_RAMUnreadable` (`sched/policy_test.go`), and for the defaults by `TestResolve_Defaults`.

## Pointers

- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
- [`../config/CLAUDE.md`](../config/CLAUDE.md) — the settings package that holds the `scheduler` block `Resolve` reads.
