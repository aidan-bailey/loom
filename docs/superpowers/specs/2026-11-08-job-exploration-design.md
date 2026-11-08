# Job exploration: wider runs on a quiet host, to learn how commands scale

Status: design approved 2026-11-08.

Amends [job scaling](2026-11-05-job-scaling-design.md) §5.3 (grants) and builds on [the scaling fit](2026-11-07-job-scaling-fit-design.md), whose §8 notes that the fit needs a second width that nothing produces. Everything else in both stands.

## 1. Problem

The owner asked on 2026-11-08 whether jobs use the resources they are given. Over the 1,429 runs since 2026-11-05 that teach cores, a median cargo run kept 51% of its cores busy, a Go run 51% (39% since 11-07) and others 61%. Much of that is genuine (serial links, cache hits, a slow last crate), but one part is a loop the scheduler can't leave:

- Job 2584 ran `cargo nextest run --all-features --workspace` (14,400 tests) at 2 test threads on a 16-core host at load 4. Its cgroup kept 0.4–0.6 cores busy in the test phase.
- Its one earlier run (job 2389) had 2 cores from the advisor and kept 0.97 busy: the tests wait on Postgres, which runs outside the job's cgroup.
- `ScalingOf`'s one-run p assumes a 1-core run keeps a core busy, so 0.97 busy at 2 cores reads as p = 0 (`SCALES 0%`), the ideal width as 1, and `SpareWidth` as 0. No grant ever widens it, so every rerun is at 2 again.
- The fit (2026-11-07) would correct p from busy cores at two widths, and the running daemon has it, but a command that only ever runs at one width never gets a second.

80 beliefs are in this state (runs at one width only, p below 0.2, a median run of 2 minutes or more), 29 of them cargo. Some are honestly serial (`check-claude-md.sh` keeps 1.00 core busy), so not all 80 are wrong; the scheduler can't tell which.

Room to look is plentiful. Since 2026-11-07, 92% of job starts had at least 8 of the 14 claimable cores free, and in 87% of minutes 4 cores or fewer were claimed. Yet only 82 of those 1,429 runs (6%) had spare cores, since `SpareWidth` lends only to commands already known to scale.

## 2. Decisions

- **Climb until flat** (owner's choice). A command explores while its runs have one width, or while its two widest widths predict a gain from going wider; it stops when they don't. A command's first run keeps its advisor-sized claim. Rejected: one-width commands only; exploring first runs too (their memory is a guess).
- **Quiet means the queue and the host** (owner's choice, refined in design review). Today's grant gate (nothing waits but over-cap jobs, the reserve kept), plus the host's measured idle cores. A CPU-pressure threshold was offered first and set aside: the host read 11.7% "some" pressure at load 4 of 16, so a low threshold would block exploring on a mostly idle box, and idle cores are also the room the step needs. Rejected: the queue gate alone (more points, some taken under contention); exploring with jobs waiting (breaks the rule that a grant never keeps a waiting job waiting).
- **Double the widest width, smaller when the room is less** (owner's choice, with the owner's amendment). The step is 2× the widest width run, shrinking with the room to no less than ⌈1.5 × widest⌉. Two runs of one command at one width differ in busy cores by a median of 1.10× and a p75 of 1.30× (937 pairs of runs of 60 s or more), so a smaller step mostly measures noise. Rejected: all free cores (a serial command reserves cores it never uses, and its memory claim grows with them); one core up (2 and 3 are hard to tell apart).
- **Inside `Grant`** (owner's choice). A pure `ExploreWidth` beside `SpareWidth`; `Grant` widens to it on a quiet host. Rejected: a separate explore pass with its own budget (duplicates Grant's pool arithmetic); raising the base claim in `Size` (sized at submit, quiet at start, so a raised claim holds cores on a busy box).
- **The signature split is a separate fix** (owner's choice). A flow gate retried at the head of a new chain has no `After`, so its signature, and its history, differ from the same gate's with `After` (job 2593 overran its 4-core claim at 14.3 cores because its overrunning history sat in the other belief). Exploration's points only add up in one belief, but this spec doesn't change signatures.

## 3. Concepts and names

- **A width**, **busy(w)**, **a pair's p** and **f(c) = (1 − p) + p/c** are as in the fit spec §3: widths come from the window's runs that teach cores and are not exclusive.
- **The widest width (`Scaling.Widest`):** the most cores any of those runs had; 0 with none. `ScalingOf` already computes it (`most`).
- **The top pair's p (`Scaling.Top`):** with 2 widths or more, the pair's p of the two widest, each at its median busy cores; unset otherwise.
- **The explore target (`ExploreTarget(b)`):** the width a quiet host would try for belief b, or 0 when b explores nothing:
  - 0 when b has no width, or the command overruns (the hints don't set its width);
  - with one width, 2 × widest, held to the CPU cap;
  - with 2 widths or more, 2 × widest held to the cap when f(widest) ÷ f(2 × widest) ≥ `floorGain` (1.25) under the top pair's p; else 0 (flat);
  - 0 when the held target is no wider than the widest.
- **The smallest step (`MinExploreWidth(widest)`):** ⌈`minExploreStep` × widest⌉ with `minExploreStep` = 1.5, and at least widest + 1.
- **`ExploreWidth(r, b)`:** `ExploreTarget(b)` for a job `SpareWidth` could grant (cores learned from its own runs, memory not explicit, not exclusive, not held to the cap), and 0 for any other.
- **Idle cores (`HostIdle`):** the host's mean idle logical CPUs over the last `idleWindow` (30 s), counting `idle` and `iowait` from `/proc/stat`'s aggregate `cpu` line; known or unknown.
- **The explore room:** for a starting job, the fewest of: the claim pool `Grant` has left (the caps less the running jobs' holds, the starting jobs' base claims and the reserve), the idle cores less the reserve's cores and less what earlier explores in the pass took, both as extra cores past the job's base claim.

## 4. Behaviour

### 4.1 `ScalingOf`

Unchanged, except that it also reports `Widest` and, with 2 widths or more, `Top`. It stays a pure function of the belief.

### 4.2 `Grant`

`Grant` takes one more input, the host's idle cores (known or not), and `Policy.Explore`. Its gate is unchanged: nothing is lent while a queued job waits for anything but the caps, agent and run jobs are passed over, and the reserve is kept.

For each starting job, in the decision's order:

1. **Explore**, when `Policy.Explore` is on, the idle cores are known and `ExploreWidth(r, b)` is above the job's base cores. The width t runs from the target down to the larger of `MinExploreWidth(widest)` and base + 1, bounded by base + the explore room. The first t whose memory (`spareMemory`) fits what the pool has left is granted: t − base cores and its memory, marked explored. The pool, the memory left and the idle room shrink by what it took.
2. **Otherwise grant as today:** `SpareWidth`'s ideal width within the claim pool, never marked explored.

`Grant` stays a pure function of its arguments and reports, for each job it widens, the spare claim and whether it explored.

### 4.3 The host's idle cores

The model reads `/proc/stat` on `tickQueue`, at most once per `queueInterval`, whether or not jobs wait, so a submit to an empty queue finds a reading. It keeps the readings of the last minute. The idle cores are (Δidle + Δiowait) ÷ Δtotal × the number of `cpuN` lines, between the newest reading and the newest one at least `idleWindow` older. They are unknown, and nothing explores, when no reading is that old (the daemon's first 30 s), when a read or parse fails, when a counter went backwards, and on a host without `/proc/stat`. One file read on the loop, as `greedyInputs` reads `MemAvailable`.

### 4.4 What a run teaches

An explore is a grant: its extra cores go into `Record.Spare` and the claim (`rec.Claim.CPUs` includes them; the hint and the CPU weight follow), as in job scaling §5.3. `Record.Explored` marks it, and `RunOf` copies it to `Run.Explored`. Learning reads neither: `largestPeak` counts the run's memory as a run with spare cores, and `ScalingOf`, the fit, `Size`'s floor and `Usual` read its width as any grant's. A failed, cancelled or laddered explore teaches no width, so the next quiet run explores again.

The window is the last 10 runs: after about 10 runs at the base width the explored width leaves it, the command is back to one width, and the next quiet run explores again. That rechecks a command whose code has changed.

### 4.5 Examples

- `cargo nextest run --all-features --workspace`, 2 cores, busy 0.97: target 4. On a quiet host it runs at 2+2c. If 4 keeps about 2 busy, the pair's p is about 1, `Size`'s floor raises its base to 4 (the default claim), and the next quiet run explores 8.
- Two jobs running and an agent's direct cargo build leave 9 idle cores; the reserve is 4 cores. A 2-core candidate's room is 9 − 4 = 5 extra, so it takes its full step to 4. With 5 idle cores the room is 1, the width 3 = ⌈1.5 × 2⌉. With 4 idle cores the room is 0: no explore, and the ordinary grant applies.
- `bash scripts/check-claude-md.sh`, 1.00 busy at 2: explores 4 once; at 4 it keeps 1.00 busy, the top pair's p is 0, and it is flat until its 4-core run leaves the window.

## 5. Configuration

`scheduler.explore` (bool) in the global config, default on, read into `Policy.Explore` by `Resolve`, as `scheduler.borrow` is. No settings-menu row in this spec.

## 6. Surfaces

- **Work view and `loom job list`:** the claim shows `2+2c/4G` as grants do. A running explorer's wait column reads `exploring 4 cores (was 2)`.
- **`loom job submit` / `run`'s sized line:** for a job whose `ExploreWidth` is wider than its `SpareWidth`, `; explores 4 cores when idle` takes the place of `; up to N cores when idle`.
- **The scaling line** (`loom job beliefs show`, the `b` pager) gains a clause: `· next quiet run explores 4 cores` for a candidate; `· no gain expected past 4 cores, checked again once its 4-core runs leave the window` for a flat one (the widest width named).
- **Log:** `queue.explored` with `job`, `signature`, `from` (base cores), `to` (the width), `idle_cores` and `room`.
- **Messages:** unchanged; the end message already shows the claim with its spare cores.
- **Stores:** `Explored` is `omitempty` on `Record` (`jobs.json`) and `Run` (`beliefs.json`). Neither store rejects unknown fields, so no version changes; an older loom drops the flag when it rewrites a store.

## 7. Components

| unit | change |
|---|---|
| `sched/scaling.go` | `Scaling.Widest`, `Scaling.Top`, `ExploreTarget`, `MinExploreWidth`, `minExploreStep` |
| `sched/grant.go` | `ExploreWidth`; `Grant`'s idle-cores input, the explore step, the explored mark in its result |
| `sched/hostcpu*.go` (new) | the `/proc/stat` reading (Linux) and `ErrUnsupported` elsewhere; the pure idle-cores window |
| `sched/job.go`, `sched/belief.go` | `Record.Explored`, `Run.Explored`, `RunOf` |
| `sched/policy.go`, `config/settings.go` | `scheduler.explore`, `Policy.Explore` |
| `core/queue_admit.go` | the readings on `tickQueue`, the input to `Grant`, `startJob` marking the record, `queue.explored` |
| `core/queue_view.go`, `internal/cli/job_sizing.go`, `sched/belief_text.go` | the wait column, the sized line, the scaling line |
| `sched/README.md`, `USAGE.md`, `docs/claude/the-work-queue.md` | the explore rule, `scheduler.explore` |
| `sched/CLAUDE.md` | `ExploreWidth` joins the pure functions in the `Size`…`Grant` rule; with 397 bytes of headroom, any longer rule goes to `docs/claude/the-work-queue.md` |

## 8. Failure modes

- **An explored run fails, is cancelled or is laddered:** it teaches no width, and the next quiet run explores again. A gate that fails every time explores on every quiet run; that costs idle cores only.
- **Memory past the claim at the wider width:** the claim already grows with the width (`spareMemory`); the ladder and borrowing act as today.
- **The host gets busy during an explore:** the run keeps its cores, as any grant does; the next pass explores nothing while idle cores are short, and CPU is shared by weight.
- **No idle-cores reading:** nothing explores; ordinary grants are unchanged.

## 9. Testing

- `TestScalingOf`: `Widest` with one width and with several; `Top` only with 2 widths or more.
- `TestExploreTarget`: one width gives 2×; a gaining top pair is a candidate and a flat one is not; an overrunner gives 0; held to the cap; widest at the cap gives 0.
- `TestExploreWidth`: explicit, family, agent and default cores, explicit memory, exclusive and held-to-cap jobs give 0.
- `TestGrant`, new cases: a quiet host explores at 2×; room between 1.5× and 2× gives the smaller width; room under 1.5× falls back to the ordinary grant; a waiting job blocks explores; the reserve is kept; unknown idle cores and `explore` off give none; memory that can't hold a width lowers it; two explorers in one pass share the idle room.
- The idle-cores window: idle plus iowait; the 30 s window; too few or too young readings, and a counter that went backwards, read unknown; the parser on a real `/proc/stat` sample.
- `TestRunOf` carries `Explored`; `TestResolve` covers `explore`'s default and off.
- `core`: a pass on an idle queue with idle cores explores, writes `Explored` and the hint, and logs `queue.explored`; a pass with a job waiting explores nothing.
- `TestFormatBelief_Scaling` and the sized line's tests: the new clauses.

## 10. Measure

Read from `beliefs.json` and `serve.log` a week after the daemon runs this. Baselines taken 2026-11-08:

- Beliefs with 2 widths or more: 107 of 862 with core-teaching runs (12%). Expect it to rise.
- The 80 beliefs stuck at one width (p below 0.2, median run of 2 minutes or more): expect fewer, and `cargo nextest run --all-features --workspace` (prader-rs) to claim 4 cores or more.
- Waits: job starts' `waited_s` had median 0 and p90 10 s, with 52 of 1,905 starts over 5 minutes. Expect no worse.
- `queue.explored`: how many explores ran, and how many were followed by a higher base claim.

## 11. Known limits

- **One run at a new width can be an outlier** (a cold cache); later runs at the floored width correct the median, as in the fit spec §8.
- **Work outside the job is invisible:** Postgres serving a test run, like sccache's server before `SCCACHE_CLIENT_SIDE`, does work the job's busy cores don't count. Exploring measures the job's own speedup only.
- **Contention mid-run still lowers busy cores;** the host gate only keeps an explore from starting into it.
- **Tools that ignore the hints** (vitest, esbuild, `ng build`) are overrunners and never explore; their width is not the scheduler's to set.
