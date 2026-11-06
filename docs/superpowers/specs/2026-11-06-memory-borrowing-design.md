# Work queue: borrowing memory

Status: design approved 2026-11-06; built with `borrow` off, switched on by the owner after the greedy-mode read (§2, §12).

Builds on [cgroup confinement and greedy admission](2026-11-05-cgroup-greedy-scheduler-design.md), whose ladder already stops and requeues a job, but only when memory runs short. The owner's ask: "most scheduling jobs can be stopped and started again"; let the scheduler use that to change a job's allocation and run more jobs at once, in the pursuit of full resource allocation efficiency.

## 1. Problem

Greedy admission counts every running job at its worst case: the most of its profiled curves plus the 15% margin, or flat at its claim when its command has fewer than 3 curves, or when it is uncontained. Claims are well above use:

- Across 554 runs over 20 s before the greedy work, the median peak memory was 57% of the claim; in the cgroup spec's sample of 50 jobs (its §1), 84% of the job-seconds spent queued (3,790 of 4,510) would have fit by measured use.
- Only 33% of runs have an earlier run of the same signature, so most jobs have no curves and count flat at their claim for their whole run; the family fallback being built on `aidanb/resource-utilisation` raises that to 59% for sizing, but curves stay per signature.
- On 2026-11-06 job 903 (4c/13G) waited over 24 minutes behind job 901 (4c/13G) with "needs ~13 GB at its peak (forecast leaves 0)" while the host had 16 GB available.

Job 901 ran uncontained because every confiner the daemon made died with its probe (fixed in 99ca3622): **greedy admission and the ladder have never run for real**, so there is no greedy-mode baseline yet. This design ships switched off for that reason (§12).

Success, read after a week with `borrow` on, against the greedy-mode baseline: less of the queued job-seconds spent waiting on the memory forecast, fewer starts waiting over 5 minutes, and a cost (wall-seconds lost to give-back requeues, time spent lent) well below the wait saved.

## 2. Decisions

The owner's choices in the brainstorm, with what was rejected:

- **Reallocate memory, by optimistic admission.** Rejected: elastic core width (restart a job at a new width: each job ends sooner, but concurrency barely moves, since CPU already overcommits); time-slicing by freezing (frees CPU, not memory); doing both.
- **Every job is eligible but `--no-requeue` ones**, matching the ladder's requeue default. Rejected: only commands known to keep their work across a stop; an opt-in flag; only short jobs.
- **Borrow the slack.** Today's fit stays the firm check; a job that fails it may start as a borrower against what the firm jobs leave unused now, and borrowers give memory back first. Rejected: one fit at typical peaks; an overbook factor on today's forecast; filling to measured use for every job.
- **Give back by the work at stake:** requeue a borrower that has done little, freeze one that has done much and push its memory into compressed swap. Rejected: always requeue; always freeze and swap; today's ladder aimed at borrowers first.
- **Approach A: a borrowing pass beside admission and a give-back machine beside the ladder,** both pure, leaving `AdmitGreedy`'s budget rule and the ladder as they are. Rejected: folding borrowing into `AdmitGreedy` and `Ladder` (bends rules their tests pin and couples routine give-backs to the ladder's emergency timings); leaving the give-back to the kernel through a low `MemoryHigh` on running borrowers (a running job faults its pages back as fast as they go, and `MemoryLow` protection for firm jobs is void on this host: `user@1000.service` and its parents have `memory.low` 0).
- **Ships off; the owner turns it on after the read** (§12). The D-Bus fix (99ca3622) went first, as its own commit.

Made while writing this spec, open to planning: no new borrower starts while any job is lent (§6.4, stricter than "while a lent job that fits waits"); swap room is checked against the working set uncompressed (§7.2), since a zram device's size is its uncompressed capacity; give-backs run whenever a borrower runs, with `borrow` off or in fallback mode too (§6.1).

## 3. Scope

In: the borrower state; the borrow fit and `sched.Borrow`; becoming firm; the tight state and `sched.GiveBack`; requeue and lend as give-backs; resume; `CgroupReclaim`; the skip paths in the ladder, its holdoff and the rung reconcile; the record, view and wire fields; the followers' lines, the work view and `loom job list`; the end message; `scheduler.borrow`; logs; docs.

Out (§14): changing a job's cores, CPU time-slicing, preemption by new arrivals, `MemoryLow` protection, checkpointing, a learned restart cost, borrowing in the forecast, borrowing in fallback mode.

## 4. Concepts and names

- **Firm job:** a running job that is not a borrower. Every job is firm unless it started by the borrow fit.
- **Borrower:** a running contained job started by the borrow fit (§5.2) that has not yet become firm (§5.3). `Record.Borrower`.
- **Lent:** a borrower frozen with its memory pushed to swap (§7.2), until it resumes. `Record.LentAt` is when, zero otherwise.
- **Give-back:** a borrower's requeue or lend, made because memory is tight.
- **Stake:** how long a borrower has run in its current attempt, less the time it spent lent.
- **Tight:** headroom (`MemAvailable`) below the reserve plus 2 GB, the ladder's calm margin. The ladder's **short** (headroom below the reserve, or the slice's memory pressure at 10% or more) is unchanged and comes later.
- **Borrow fit, firm fit:** §5.2; the firm fit is today's greedy memory fit (cgroup spec §7.2).

## 5. Admission

### 5.1 Who may borrow

A queued job may borrow when all hold: greedy mode is on and `borrow` is set; it is not `--no-requeue`, exclusive or an agent job; it has been requeued fewer than `MaxRequeues` times; its previous attempt did not end by a give-back requeue (`Record.NoBorrow`, set by the requeue: such an attempt runs firm only, so it can't bounce on the same collision).

### 5.2 The borrow fit

`sched.Borrow` runs in each admission pass after `AdmitGreedy` and before `Grant`, on the jobs `AdmitGreedy` left waiting on the memory fit alone: `Decision` reports them (`Decision.OnMemory`), which are the jobs that passed the holdoff, exclusive, CPU and I/O checks and failed only "needs ~N GB at its peak". It walks them in queue order with first fit; when the starvation guard holds the line, only the starved head may borrow.

A candidate starts as a borrower when, from now to its expected end, the sum of these stays within the budget:

1. each running firm job at its measured working set plus the 15% margin (an uncontained one at its session's RSS plus the margin);
2. each running borrower at its prediction (`predictRunning`, unchanged), lent ones included;
3. the candidate at its own prediction (`predictNew`, unchanged: its curves, or flat at its claim);
4. borrowers started earlier in the pass, at their predictions.

Borrowers lend to no one: only firm jobs' unused forecast is borrowed, so what borrowers can be made to give back is bounded by how far firm jobs grow back toward their predictions. Borrowing also needs: memory not tight, no ladder holdoff, and no job lent (§6.4). The CPU fit and I/O slots apply as in the cgroup spec's §7.3, counting jobs started earlier in the pass, firm and borrowed.

`Grant` then runs on the pass's starts with borrowers left out: a borrower gets no spare cores.

### 5.3 Becoming firm

At each pass, a running, unlent borrower becomes firm when the firm fit holds with it in: every running job, it included, at its prediction (as `AdmitGreedy` counts them), within the budget until its expected end. It is then a firm job for good: never given back, still the ladder's like any job.

### 5.4 Unchanged

`AdmitGreedy` and its rule that it never starts a job past the budget; the starvation guard; `--exclusive`; dependent jobs; holds; fallback admission; `ForecastGreedy`, which stays firm-only (a queued job may start as a borrower before its forecast start).

## 6. Giving back

### 6.1 The machine

`sched.GiveBack` is a pure function beside `Ladder`, run after each usage sample while any borrower runs, whatever `borrow` says and in fallback mode too (where lending needs the bus, so every give-back is a requeue, §10): the time, the headroom, the reserve, the swap room, and each running borrower (ID, start, stake, working set, lent at, whether the borrow fit holds for it now) in; at most one action out; its state (the last give-back's time) kept between passes. The ladder runs first, and only one confinement action is in flight at a time, the ladder's and the give-back's alike (the model's `laddering` flag covers both).

### 6.2 When and who

While memory is tight and an unlent borrower runs, the machine acts on the most recently started unlent borrower, then waits 10 s before the next give-back so the headroom shows the last one.

### 6.3 Requeue or lend

- **Stake under 2 minutes: requeue.** The scope's `cgroup.kill`, then today's requeue path (cgroup spec §8.4): same ID, owner, order and place, files to `attempt-<n>/`, a floor-only run. It sets `NoBorrow` on the next attempt, adds 1 to `GaveBack`, does not count toward `MaxRequeues`, and sends no message.
- **Stake of 2 minutes or more: lend,** unless the swap room is short (§7.2), when it is requeued as above.

### 6.4 Lent jobs

A lent job stays `running`. The firm fit and the borrow fit count it at its prediction from its progress (`predictRunning`: its remaining curves, or flat at its claim), so jobs arriving later can't take the room it waits for, and while any job is lent no new borrower starts. Its progress stands still while it is frozen, so its expected end moves by the time it has spent lent (`LentSecs` plus the current lend); otherwise a flat prediction would lapse a minute after its usual end and stop holding its room.

### 6.5 Resume

A lent job resumes (`Thaw`) when the borrow fit holds for it from its own progress, memory is not tight, and 30 s have passed since it was lent: the earliest `LentAt` first, one per pass. The thaw adds the lend's length to `LentSecs` and clears `LentAt`. Its pages fault back in as it touches them. It is a running borrower again and can become firm (§5.3).

## 7. Mechanics

### 7.1 Lending

`Freeze` on the scope (D-Bus), then `CgroupReclaim` writes `"<working set bytes> swappiness=200"` to the scope's `memory.reclaim`, off the loop: compressing a 10 GB working set takes seconds. A write the kernel answers with `EAGAIN` reclaimed less than asked and counts as done; one it refuses as invalid is retried without `swappiness=` (the live contract checks which form this kernel takes, §11.3). The lend counts as applied when the freeze returned; the reclaim's result is logged.

`CgroupReclaim` lives beside `CgroupKill` and shares its refusal of anything but a loom job's scope.

### 7.2 Swap room

A borrower is lent only when the host's `SwapFree` (`/proc/meminfo`) is at least its working set: a zram device's size is its uncompressed capacity. On fresco (zram, zstd, 15 GB) a lent 10 GB job holds about 2.7 GB of RAM in zram at the 3.7:1 seen on 2026-11-06. With no swap, reclaim could only drop page cache, so `SwapFree` of 0 makes every give-back a requeue.

### 7.3 State and the skip paths

Give-back state lives in its own record fields, never in `Rung`: `Borrower`, `LentAt`, `LentSecs` (seconds lent in this attempt, for the stake and the end message), `GaveBack` (give-back requeues across attempts, carried by `requeueJob` like `Requeues`) and `NoBorrow` (set by `requeueJob` on the attempt after a give-back requeue). A lent job is frozen, so three paths that read a freeze as the ladder's must skip it:

- `ladderHoldoff` counts only jobs with a `Rung`, so lent jobs don't hold all admission;
- `stepLadder` leaves lent jobs out of the ladder's input, whose climb has no step for them;
- `applyUsage`'s rung reconcile reads a frozen cgroup with `LentAt` set as lent, not `paused`.

### 7.4 Learning

A lent run's timing and curve are distorted: it sets `Laddered`, so it teaches memory floors only. A give-back requeue teaches a floor, as the ladder's does.

## 8. What people see

- **The work view:** a running borrower's state reads `borrowing`, a lent one `lent (memory) 3m`; the greedy header adds `· borrowed 6 GB`, the sum of the running borrowers' predicted peaks; a job's detail line says `restarted after 1m12s to lend memory back` or `lent 4m`.
- **`loom job list`:** the same state text; `--json` carries `JobView.Borrower`, `LentAt`, `GaveBack` and `QueueView.BorrowedKB`.
- **Followers (`run`, `wait`):** on stderr, `loom: job 903 started after 6s on borrowed memory`, `loom: job 903 lent its memory back: paused`, `loom: job 903 resumed`, `loom: job 903 restarted to lend memory back; following its next attempt`. Becoming firm prints nothing.
- **Messages:** a give-back sends none; the end message adds `(lent memory back for 4m)` or `(restarted once to lend memory back)`. The ladder's requeue message is unchanged.
- **Loom's Claude context:** unchanged; the followers' lines explain a pause or restart where an agent sees it.

## 9. Configuration

The global `config.json` `scheduler` block gains `borrow` (bool, default `false`): borrowing and give-backs, in greedy mode only. A caps-form edit keeps it, as every key it doesn't show (`setSchedulerCaps`). Turning it off stops new borrowing; running borrowers carry on and a lent job still resumes. The thresholds are `sched` constants: the 2 GB tight margin (the ladder's calm margin), the 2-minute stake, 10 s between give-backs, and 30 s from a lend to a resume.

## 10. Failure handling

- **A freeze that fails:** nothing is assumed; the next pass requeues that borrower instead (`cgroup.kill` is a file write and needs no bus).
- **A reclaim that fails or frees too little:** the job stays lent, frozen; if memory is still tight after 10 s, the next borrower gives back.
- **A thaw that fails:** logged, shown on the header's error line (`LadderErr`), retried each pass.
- **Fallback mode while borrowers run:** no new borrowing; running borrowers carry on and, while memory is tight, are requeued through `cgroup.kill`; lent jobs wait for the bus to thaw them, and the header says so.
- **A borrower whose shim could not join its scope** runs uncontained, stops being a borrower (nothing could give it back), and counts flat at its claim like any uncontained job; `queue.borrowed_uncontained` is logged.
- **A daemon restart:** the record's `LentAt` and the cgroup's `frozen` decide (§7.3): both, lent; frozen alone, the ladder's `paused`; `LentAt` alone (thawed by hand), a running borrower with `LentAt` cleared.
- **A cancel, or the owner's session pausing or ending:** a lent job is thawed before the SIGTERM, as a paused one is (cgroup spec §15, 16).
- **A downgrade:** older looms ignore the new fields; one with the ladder reads a lent job as `paused` and thaws it once memory is calm; one older than the ladder leaves it frozen (the cgroup spec's known limit).

## 11. Testing

### 11.1 Pure

Table tests, with both sides of every threshold pinned: `Borrow` (firm jobs at measured use and borrowers at prediction; each exclusion of §5.1, `NoBorrow` among them; only the starved head under the guard; no borrowing while tight, under a holdoff or while a job is lent; jobs started earlier in the pass counting, CPU and I/O included; `Decision.OnMemory` only); becoming firm; `GiveBack` (tight on both sides; the youngest unlent borrower; the 2-minute stake on both sides; swap room on both sides; the 10 s wait; resume's 30 s, its fit and tight conditions, oldest first, one per pass).

### 11.2 Core, through the production path

A booted model, real shims via `TestRunner`, `MemAvailable` and `SwapFree` fixed, and the fake confiner gaining `Reclaim` (recording calls, able to fail): a borrower starts; tightness requeues a young one with `NoBorrow` on its next attempt and no message; tightness lends an older one, which resumes when the fit holds; a lent job stays out of `ladderHoldoff` and the ladder's input, and the rung reconcile keeps it lent; a restart reconciles each case of §10; a cancel thaws first; a failed freeze becomes a requeue; `setSchedulerCaps` keeps `borrow`.

### 11.3 Live

`-tags systemd_live`, the plan's first story: a test scope holding about 200 MB of anonymous memory with a checksum is frozen; `memory.reclaim` is written (with `swappiness=`, then without if refused); `memory.current` falls and swap use rises; after a thaw the process carries on and its checksum holds.

## 12. Turning it on

`borrow` ships off. Once the daemon has run a build with 99ca3622 for at least three days, read from `serve.log` and `jobs.json`: mean `waited_s`, starts that waited over 5 minutes, the share of queued job-seconds whose wait was "needs ~N GB at its peak", and `queue.ladder` events, confirming from `started.json` that jobs ran contained. The owner decides on that read whether to turn `borrow` on; the week after, compare against it with the borrower logs (§13).

## 13. Logs

`queue.borrowed` (job, predicted peak, budget), `queue.gave_back` (job, `requeue` or `lend`, headroom, working set, stake), `queue.resumed` (job, seconds lent), `queue.firm` (job), `queue.reclaim_failed` (job, error) and `queue.borrowed_uncontained` (job).

## 14. Known limits and later work

- **Cores stay fixed at start.** Restarting a job at a new width (elastic cores) is left for later; give-back requeues restart at the same claim.
- **Stake is wall time.** What a restart loses depends on the tool (cargo and go keep finished units, nextest starts over); a restart cost learned per command from requeued attempts is later work.
- **No preemption by arrivals.** A new firm job never displaces a running borrower; only growth by the jobs already running makes memory tight.
- **The forecast ignores borrowing,** so a queued job can start before its forecast start.
- **Swap on disk:** the design assumes zram; on disk swap a lend costs I/O and a resume pages back slowly.
- **Fallback mode never borrows.**

## 15. Components

| unit | change |
|---|---|
| `sched/borrow.go` (new) | `Borrow`, the borrow fit, becoming firm |
| `sched/giveback.go` (new) | `GiveBack`, its input, state and actions |
| `sched/greedy.go` | `Decision.OnMemory` from the memory check |
| `sched/cgroup.go` | `CgroupReclaim` |
| `sched/job.go` | `Borrower`, `LentAt`, `LentSecs`, `GaveBack`, `NoBorrow` |
| `sched/policy.go`, `config/settings.go` | `borrow` |
| `core/queue_admit.go` | `Borrow` after `AdmitGreedy`; borrowers out of `Grant`; starting a borrower |
| `core/queue_ladder.go` | the give-back driver beside `stepLadder`; lend, requeue and resume; the skip paths; `requeueJob` carrying `GaveBack` and setting `NoBorrow` |
| `core/queue_greedy.go` | `predictRunning`'s expected end moved by time lent |
| `core/queue_watch.go` | the rung reconcile's lent case; `SwapFree` |
| `core/queue_notice.go` | the end message's clause |
| `core/queue_view.go`, `core/rpc` | the view fields; the protocol reference regenerated |
| `internal/cli`, `ui/work.go` | §8 |
| docs | `USAGE.md`, `sched/README.md`, `docs/claude/the-work-queue.md`, and the `sched/CLAUDE.md`/`core/CLAUDE.md` rules for `Borrow`, `GiveBack` and `CgroupReclaim` |

The family fallback on `aidanb/resource-utilisation` touches `core/queue_greedy.go`, `core/queue_view.go` and `sched.Record` too; whichever lands second rebases. Its vocabulary is "family"; "borrow" is this design's.
