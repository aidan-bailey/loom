# Adding throttled background work to the model (a gated job)

**Symptom:** a background job runs once after the daemon starts and never again ("latched off"); or it stacks concurrent subprocesses when the CLI it runs hangs; or it keeps a `claude` process alive most of the time on the snapshot path.

**Cause:** the model's health tick (`core/tick.go:tickInst`) fires every few seconds, and faster on the snapshot path. Work that rides it needs its own cadence and at most one run in flight, and the flag that says "in flight" must be cleared on every delivery, errors included. Before `pollGate` each job carried its own flags, and a missed disarm latched the job off for the session.

**Rule:** dispatch throttled background work only through `dispatchGated` (`core/gate.go`), which arms the gate only when there is a job and wraps the result so `Deliver` disarms before any handler runs.

## Checklist

1. **Add a `gateKind`** in `core/gate.go` (before `numGateKinds`) and its name in `String()`, which the logs use.
2. **Add its interval to `gateIntervals`**, keyed by kind, as a constant beside the job (`rosterInterval`, `ghInterval`, …). Leave it 0 for a job that runs on events, not a cadence: a zero interval only dedupes. Keyed by kind, so even a zero-value `core.Model` is throttled. *Enforced* for today's kinds by `TestGateIntervalsUseEachJobsInterval`.
3. **Write `maybe<Job>()`**, the dispatcher: `return m.dispatchGated(gate<Job>, time.Now(), func() Job { … })`. The builder runs on the model's goroutine and may read model state; it returns `nil` when there is nothing to do (no Claude agent, nothing queued), which arms nothing. The `Job` it returns must not read model state: capture its inputs in the builder. *Enforced* that a nil build arms nothing by `TestDispatchGatedNilBuildArmsNothing`.
4. **Write the job**: blocking I/O only, returning one result value of a type of its own. `dispatchGated` spawns it with `spawnBackground`, so it serves no request and `Loop.Quiesce` doesn't wait for it at a stop.
5. **Route the result in `Deliver`** (`core/model.go`): a `case <job>Result:` calling `deliver<Job>`. `deliverGated` disarms the gate first, then calls `Deliver` with your inner result, so your handler never touches the gate. A result type with no case is logged as `deliver.unknown_result` and dropped, though the gate still disarms.
6. **Call `maybe<Job>()` from the tick** (`tickInst` in `core/tick.go`), and from any event that should run it sooner.
7. **For "run as soon as possible" triggers**, call `m.gate(gate<Job>).request()` then `maybe<Job>()`: a request made while a dispatch is in flight dispatches once more after that flight lands. Add the kind to `redispatch` (`core/gate.go`), or a mid-flight request is lost. *Enforced* for today's kinds by `TestDeliverGatedRedispatchesPendingOnce`.
8. **For "run at the next tick" triggers**, call `expedite()`, as `ExpediteGitHub` does; an in-flight dispatch still lands first.
9. **Add your kind to `TestProductionGatedJobsYieldOneResult`** (`core/gate_test.go`), which runs each production job and checks it yields exactly one `gatedResult` of its own type. *Silent* until you do.

## Traps

- **Arm only when dispatching.** A pre-check that skips the dispatch must happen before `dispatchGated` (as `maybeGHQuery`'s `ghAvailable` backoff does), never by arming and returning: no job means no result, so nothing would ever disarm the gate.
- **One result per job, and never a job as a result.** `Deliver` unwraps one `gatedResult`; a job that answers with another job still disarms its gate, but the stray job is logged as an unknown result and never runs (`TestGatedNestedJobStillDisarms`). Queue follow-on work from the handler instead.
- **A hung subprocess holds the gate.** Bound every subprocess the job runs with its own timeout, and bound the whole job when it runs several (the GitHub poll's `ghPollBudget` stays under its interval).
- **Stale answers.** A failed query should usually clear what the last one set (the roster does: stale statuses are worse than none); display-only data may keep it, dimmed (usage samples do).
- **The job's result is applied later**, possibly after the instance it names was killed or moved to Deleting or Loading; check `statusEligible` or look the instance up again before acting on it.

- **Not every throttle is a gate.** The TUI's split-ratio flush tick runs in the TUI, not the model, and dedupes with a plain flag (`ratioTickArmed`); a gate is for the model's background jobs.

## What the gates won't tell you

- Whether the interval is right for the snapshot path, where the tick fires several times faster.
- Whether the job reads model state off the loop: only `CC=clang CGO_ENABLED=1 go test -race` catches that, and only when a test overlaps the read with a write.
- Whether a new trigger that should `request()` a run does so.
