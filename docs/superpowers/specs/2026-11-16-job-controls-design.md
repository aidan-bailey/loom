# Work queue: job controls (start now, park, pause)

Status: design approved 2026-11-16.

Builds on the [work scheduler](2026-11-04-work-scheduler-design.md), [cgroup confinement and greedy admission](2026-11-05-cgroup-greedy-scheduler-design.md) and [memory borrowing](2026-11-06-memory-borrowing-design.md). The owner's ask, after the queue sat idle behind a job that could not fit (fixed in 0d0dc1a4): "Can I have more controls? Like forcing a job to run".

## 1. Problem

The owner can cancel a job (`x`), move a queued job to the front (`t`) and edit the caps (`e`), and nothing else. When the scheduler's judgement is wrong, or right but not what the owner wants now, there is no way to say so:

- On 2026-11-16 job 7478 waited 37.7 minutes and three jobs waited behind it; the only way to get work moving was to cancel it.
- A queued job the owner does not want yet (a long build before a demo) can only be cancelled and submitted again later, losing its place.
- A running job that is in the way (a long test suite while the owner needs the cores for something interactive) can only be cancelled, losing its work.

Success: each of the three can be done from the TUI's work view and from the phone, without a cancel, and the scheduler's protections (the ladder, the locks, a paused admission) still hold where they matter.

## 2. Decisions

The owner's choices in the brainstorm, with what was rejected:

- **Three controls: start now, park/unpark, pause/resume.** Rejected for now: editing a queued job's claim.
- **On the TUI's work view and the phone (PWA).** Rejected: the CLI (`loom job start|…`), so an agent cannot force its own work past the queue.
- **A forced job is the ladder's victim of last resort,** with the head the starvation guard started. Rejected: never laddered (the kernel would be the only backstop); an ordinary job (the ladder would usually undo the force within a minute, since it takes the most recently started job first).
- **A pause frees CPU, and memory when swap allows:** freeze, then reclaim the working set to swap when `SwapFree` covers it, else CPU only, and say which. Rejected: CPU only (never makes room for a big job); always reclaim (stalls or thrashes with swap full, as fresco's 14 of 15 GB was on 2026-11-16).
- **"Park", not "hold".** `held` and "released" already name a dependent job waiting on its prerequisite (`--after N`); a user hold under the same words would collide in `loom job list`, the view and the code.
- **Approach: one request, `ControlJob`, with an action,** and the state on the job's record, read by the pure `sched` functions. Rejected: one `Core` method per action (five generated methods for names alone); starting a forced job from the request handler (a second place that starts jobs, which grants, the forecast and the starvation bookkeeping would each have to learn).

Made while writing this spec, open to planning: an unpark restarts the starvation clock with a field of its own (`Unparked`), not by writing `Ready`, which the dependent's end line reads (§5.2); a forced job is never granted spare cores (§5.1); a pause's failure is shown on the job and logged, and is not a `Notice` (§6.4); `GiveBack` leaves paused borrowers out (§7.2).

## 3. Scope

In: the record fields; `ControlJob` and its refusals; forced starts in `admitWith`; the park skip; pause and resume through the confiner, with the reclaim; the paused job in the greedy fit, the claim fit, the forecast, the ladder, `Grant`, `Borrow`, overdue and learning; the rung reconcile; cancel of a paused job; the work view's keys and rows; `loom job list`'s rows; the phone's route, job JSON and buttons; logs; docs.

Out (§12): editing a claim; CLI controls; pausing an uncontained job (no scope to freeze); force past a named lock; resuming a job the ladder or the give-back froze.

## 4. Concepts and names

- **Forced job:** a job the owner started with start now. `Record.Forced`, set by the request, kept while it runs, cleared by a requeue.
- **Parked job:** a queued (or held) job the owner parked. `Record.Parked`; `Record.Unparked` is when it was last unparked.
- **Paused job:** a running job the owner paused: `Record.PausedAt` is set while it is paused, `Record.PausedSecs` sums its earlier pauses in this attempt, `Record.PauseReclaimed` says whether its memory went to swap. `Record.PausedFor(now)` is its time paused in this attempt, as `LentFor` is a lent job's.
- **Pause intent:** `Record.Control` is `pause` or `resume` from the request until the confiner's call returns; `Record.ControlErr` is the last call's error, cleared by the next success.

All are `omitempty` JSON fields of `sched.Record`, with no `StoreVersion` bump, as borrowing added its fields. A downgraded daemon ignores them: a parked job is admitted, and a paused job reads as a ladder-paused one and is thawed once memory is calm.

## 5. The controls

### 5.1 Start now

`ControlJob(start)` on a queued job sets `Forced` and runs an admission pass at once. In `admitWith`, a queued forced job is taken before the walk, in queue order: it starts whatever the fit says (memory, cores, I/O slots, an exclusive job running, the starvation guard), whatever its claim (over the caps included), while it is being sized (at the claim it holds then), and under the ladder's holdoff. The fitter counts it (`take`) and its locks are taken, so the jobs after it see it; it is never the head.

Refused, with the refusal's text:

- a job that is not queued: `job N is running`, `job N has ended`, and for a held dependent `job N waits for job M to end`;
- a job naming a lock a running job holds: the walk's own text, `waits for lock NAME (job M)`; a lock exists so two jobs never run together;
- while admission is paused: `admission paused: …`, the view's own reason;
- an agent or run job (`Unclaimed`): `job N is an agent job: it starts by its account's agent slots`.

Once running, a forced job is an ordinary running job but for three things: the ladder takes it last, with the head the starvation guard started (§7.1); `Grant` gives it no spare cores; it is firm, never a borrower. A requeue clears `Forced`: the owner forces it again if they still want it.

Parked jobs may be started: start now also clears `Parked`.

A forced job a pass cannot start (caps admission can't use, a lock a running job took since the request) stays queued and forced, and the next pass starts it (amended while planning).

### 5.2 Park and unpark

`ControlJob(park)` on a queued or held job sets `Parked`. `admitWith` skips a parked job like an over-cap one: it waits `parked by you` and is never the head. A held dependent that is parked stays parked when its prerequisite releases it. Parking a running job is refused: `job N is running: pause it instead`.

`ControlJob(unpark)` clears `Parked` and sets `Unparked` to now, and runs an admission pass. `QueuedSince` becomes the later of what it was (`Ready`, else `Submitted`) and `Unparked`, so an unparked job's starvation wait starts again; counted from its submit it would come back as a starving head at once and hold every other job back. Unparking a job that is not parked is refused: `job N is not parked`.

A parked job keeps its place (`Order`); `t` (move to front) still works on it.

### 5.3 Pause and resume

`ControlJob(pause)` on a running job sets `Control` to `pause`, and the control step (§6) carries it out: freeze the job's scope, then, when `SwapFree` covers its working set (the give-back's check, memory borrowing spec §7.2), write `memory.reclaim` for the working set (`CgroupReclaim`). The step's return sets `PausedAt`, `PauseReclaimed` and `Laddered` (the run's timing is distorted: it teaches floors only, cgroup spec §8.5).

Refused:

- a job that is not running: `job N is not running`;
- an uncontained job (no `Cgroup`): `job N runs uncontained: there is no scope to freeze`;
- an exclusive job: `job N is exclusive: it holds the host until it ends` (admission keeps an exclusive job's whole-host claim whatever its state, so a pause would free nothing; amended while planning);
- a job on a ladder rung or lent: `the ladder holds job N (squeezed)`, `… (paused)`, `job N is lent to give memory back`;
- a job already paused or with a control in flight: `job N is already paused`, `job N is pausing`, `job N is resuming`;
- an agent or run job: `job N is an agent job`.

`ControlJob(resume)` on a paused job sets `Control` to `resume`; the step thaws the scope, and its return adds the pause to `PausedSecs` and clears `PausedAt`. A resume is the owner's call and runs whatever the fit says; the ladder protects the host afterwards. Refused for a job that is not paused by the owner: `job N is not paused`.

A paused job's locks stay held: it will resume, and its port or shared state is still its own.

### 5.4 The owner of a control

`ControlJob` takes a `JobControl{Job, Owner, Action}`. `Owner` 0 is any job (the work view covers the host); a non-zero `Owner` must be the job's owner session, or the request is refused `job N is not this session's` — the phone acts on a session's jobs, as `ReadJobLog` reads only them.

## 6. Mechanics

### 6.1 The request

`ControlJob` is a request (`core/iface.go`, no `rpc:` comment), answered at once through its `Reply`; a refusal matches `core.ErrRefused`. Start, park and unpark change the record, save the queue and run `admitJobs`. Pause and resume set the intent and save; the reply means accepted, and the job shows the outcome.

### 6.2 The control step

`stepJobControls` runs after each sample, like `stepGiveBack`, and only while no confinement action is in flight (`q.laddering`, which the ladder and the give-back share): one call at a time on the bus. It takes the earliest job with a `Control` and spawns its call in the background (`spawnBackground`): pause is `Freeze`, then the reclaim when the swap check passes; resume is `Thaw`. Its delivery records the outcome (§5.3) and clears `Control`; a failed call sets `ControlErr`, clears `Control` and leaves the job as it was. A job that ended before its call returned is left alone.

### 6.3 The rung reconcile

The sampler decides a job's rung from its cgroup (`core/queue_watch.go`): frozen is `paused`, unless the job is lent. A paused job is the same exception: frozen and paused is no rung, so the ladder never restores (thaws) it. A paused job found thawed after its `PausedAt` was thawed outside loom: its pause ends as a resume's would, and `queue.unpaused_by_hand` is logged.

### 6.4 Cancel, restart and failures

- **Cancel** of a paused job thaws it first, then signals it, as a cancelled lent job is thawed first: a frozen process cannot act on its SIGTERM.
- **A daemon restart** keeps a pause: the scope stays frozen, the record keeps `PausedAt`, and the reconcile (§6.3) reads it as paused. A `Control` in flight at the restart is carried out again by the step.
- **No bus, or a call that fails,** leaves the job as it was, with `ControlErr` on its row (`pause failed: …`) and `queue.control_failed` logged. It is not a `Notice`: the owner is looking at the job when it fails.

## 7. The scheduler's other parts

### 7.1 The ladder

`Ladder`'s victims leave out paused jobs and jobs with a `Control` in flight: the first is already frozen, and squeezing either would race the step. Forced jobs join the starved head in going last: the order is "a job neither forced nor starved before one that is; then the most recently started".

### 7.2 Admission's counts

- **Greedy fit:** a paused job counts flat at its measured memory now (`Greedy.Used`, which falls after a reclaim; its prediction before a sample), with no cores and no I/O slot, until it resumes.
- **Claim fit** (fallback mode, or a confiner lost while a job is paused): a paused job counts at its memory hold or claim, with no cores and no I/O slot.
- **Forecast:** a paused job never ends in the simulation; the forecast says nothing of when the owner will resume it.
- **`Grant`:** passes over forced starts, and counts a paused job at no cores.
- **`Borrow` and `GiveBack`:** a forced job never borrows (it starts before the walk). `GiveBack` leaves a paused borrower out, as the ladder leaves paused jobs out: a requeue would lose the work the owner paused to keep, and a lend would freeze what is frozen. It is a give-back candidate again once resumed.

### 7.3 Time and learning

A paused job's progress stands still: `predictRunning` adds `PausedFor` to its expected end, as it adds `LentFor`, and `OverdueAfter`'s clock leaves paused time out. A run with a pause in it carries `Laddered`, so it teaches floors only. A forced run teaches as any run does.

## 8. What people see

- **Work view:** `s` starts the selected queued job now, after a confirmation that names its wait reason (`Start job 7478 now? It waits: needs ~12 GB at its peak (the host leaves 11 with no job running)`) and, when one runs, the exclusive job it will share the host with; `h` parks or unparks it; `p` pauses or resumes the selected running job. A parked row shows `parked`, a paused one `paused` (`paused, CPU only: swap short` when nothing was reclaimed), a forced one `forced`, one in flight `pausing…`/`resuming…`, and a failed call its `ControlErr`. `s`, `h` and `p` join `workModelKeys`, refused offline; the help and `USAGE.md`'s work view table list them.
- **`loom job list`:** a parked job is `queued` and its WAITS FOR column says `parked by you`; a paused one is `running` and its WAITS FOR column says `paused by you` (`paused by you, CPU only`).
- **Phone:** `POST /api/v1/sessions/{id}/jobs/{job}/{action}` with `action` one of `start`, `park`, `unpark`, `pause`, `resume` (any other is 404). A refusal is 409 with the model's text; a stopping daemon 503. The job JSON gains `parked`, `paused`, `pause_reclaimed`, `forced`, `control` and `control_error`. The job log screen shows the buttons the job's state allows (queued: Start now, Park or Unpark; running: Pause or Resume); Start now asks in a sheet that shows `waiting`.

## 9. Logs

`queue.job_started` gains `forced=true` for a forced start. New: `queue.controlled` (job, action, owner given or not) for each accepted request; `queue.paused` (job, reclaimed, the bytes reclaimed); `queue.unpaused` (job, paused seconds; the give-back logs `queue.resumed`); `queue.control_failed` (job, action, error); `queue.unpaused_by_hand` (job).

## 10. Failure handling

- Every refusal is the model's (`admit`-style preconditions in `core`), and neither the work view nor the web layer copies one: the phone and the TUI show its text.
- A force never passes a named lock, a paused admission or an unfinished prerequisite: those are about correctness, not room.
- A pause that cannot reclaim still pauses, and says so; one that cannot freeze changes nothing and says so.
- A freeze or thaw whose return was lost (the daemon stopped mid-call) leaves `Control` set, and the step carries it out again (§6.4): both are idempotent, and a repeated reclaim only finds less to push out.

## 11. Testing

### 11.1 Pure (`sched`)

Table tests, with both sides of every rule pinned: a forced job starts past the memory fit, the cores, the I/O slots, an exclusive job, the guard, the over-cap skip, sizing and the holdoff, counts for the jobs after it, and is never the head; it waits on a running job's lock. A parked job is skipped, never the head, and the next head is guarded; `QueuedSince` takes `Unparked` when it is the latest, and not when `Ready` or `Submitted` is. A paused job counts at its use with no cores or slot in both fits, never ends in the forecast, and adds its pause to its expected end. The ladder leaves paused and in-flight jobs out and takes forced and starved jobs last; `GiveBack` leaves a paused borrower out. `Grant` passes forced starts over. `PausedFor` sums.

### 11.2 Core, through the production path

With the fake confiner, through `ControlJob` and real records: each refusal and its text; start now starting a job the fit holds back and logging `forced`; park/unpark across a dependent's release; pause freezing and reclaiming, and CPU only with swap short; the rung reconcile leaving a frozen paused job without a rung, and ending a pause thawed by hand; cancel thawing first; a pause kept across `ReconcileAndRestore`; a failed freeze leaving `ControlErr`; the owner check. The new method's cases in `TestMethods_CoverCore`, `TestLoopForwardsEachMethodToItsNamesake` and `TestRequests_RefusalsMatchErrRefused`; the wire regenerated.

### 11.3 Surfaces

The work view's keys, confirmation, rows and offline refusals (`app/work_test.go`); the web route's actions, 404, 409 and 503, the job JSON fields (`internal/web`), and the app's buttons by state. `go test -tags e2e ./e2e/...` once, since the queue's lifecycle changes.

## 12. Known limits and later work

- No CLI controls: an agent cannot force, park or pause, by the owner's choice.
- No claim editing.
- An uncontained job cannot be paused; a SIGSTOP to its group would need the lock-before-signal proof and a reconcile of its own.
- A job the ladder or the give-back froze cannot be resumed by hand; the owner can cancel it.
- A downgrade forgets parks and pauses (§4).

## 13. Components

| Package | What changes |
|---|---|
| `sched` | `Record.Forced`, `Parked`, `Unparked`, `PausedAt`, `PausedSecs`, `PauseReclaimed`, `Control`, `ControlErr`; `PausedFor`; `QueuedSince`; forced starts and the park skip in `admitWith`; paused jobs in `claimFit`, `greedyFit`, the forecast, `Grant` and `GiveBack`; the ladder's victims and order |
| `core` | `JobControl`, `JobAction`, `ControlJob` (`iface.go`, `loop_core.go`, the request, its preconditions); `stepJobControls` and its delivery; the rung reconcile; cancel's thaw; `predictRunning` and overdue; `JobView` fields; logs |
| `core/rpc` | the generated method; `docs/specs/protocol.md` |
| `app`, `ui` | the work view's `s`, `h`, `p`, the confirmation, the row tags, `workModelKeys`, help |
| `internal/cli` | `loom job list`'s parked and paused rows |
| `internal/web` | the route, the job JSON fields, the app's buttons and sheet |
| docs | `USAGE.md` (work view table, the queue's order), `sched/README.md`, `core/README.md`, `internal/web/README.md`'s route table, the package `CLAUDE.md` rules each part adds |
