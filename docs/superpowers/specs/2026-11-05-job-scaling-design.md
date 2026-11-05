# Smart scheduler: how jobs scale

Status: design approved 2026-11-05.

Builds on [the smart scheduler](2026-11-05-smart-scheduler-design.md), whose §15 left elastic cores for later: "grant more cores when the host is idle, and learn how a job's time scales with them."

## 1. Problem

The smart scheduler learns a job's cores as ⌈1.25 × its median busy cores⌉, held to the default claim's 4, and takes a job's duration to be the same at any number of cores. Fresco's `beliefs.json` shows what that misses (busy cores are CPU time over wall time):

| command | cores | busy |
|---|---|---|
| `vitest run` | 4 | 7.8 |
| the wasm build (`cargo build … --target wasm32-unknown-unknown`, then `wasm-bindgen` and `wasm-opt`) | 3 | 2.7 |
| `go test -race ./...` | 4 | 2.2 |
| `cargo nextest run -p prader-app-core --lib catalog::` | 2, then 4 | 1.7, then 1.2 |
| `go test -tags e2e ./e2e/... -count=1 -v` | 3 | 0.5 |

- A job that scales gets 4 cores even on an idle host. The wasm build kept 2.7 of its 3 busy, and a cold cargo build scales far past 4.
- A command that ignores the hint variables takes what it likes. vitest sizes its workers from the host's cores, so the queue counts 4 cores for a job that keeps 7.8 busy.
- A usual duration is a median over runs at whatever cores they had, so the ETA is wrong whenever the claim changes.

Success looks like this, checked from `beliefs.json` after a week of use:
- an overrunning command claims at least the cores it keeps busy;
- a run with spare cores peaks within its memory claim (95% or better, as before) and ends sooner than the same command's runs without them;
- a job of the default claim or less, submitted while a run with spare cores holds them and nothing else waits, starts at once.

## 2. Decisions

- **Scaling drives cores** (brainstorm option "cores where they help"). The scheduler learns how each command's time scales with cores. Commands that scale take spare cores, overrunners are counted at what they take, and durations are predicted at the cores a job gets. Rejected: counting overrunners only; showing a figure only; learning which jobs slow each other down when they run side by side.
- **Spare cores, keeping a reserve** (option "spare cores, keep a reserve"). A job is admitted on its base claim. At start, when no other job waits, it takes spare cores up to its ideal width, always leaving the default claim free for the next arrival. Rejected: spare cores without a reserve, since the next arrival then waits for the whole run; the ideal width as the claim, since a wide job waits for cores and then blocks the jobs behind it; a range granted while others wait.
- **Overrunners are counted at what they take** (option "count what it takes"): ⌈1.25 × median busy⌉ cores, past the default claim and up to the cap, and no spare cores. Rejected: adding `GOMAXPROCS` to the hints, which changes how every Go job runs; flagging only.
- **Amdahl from the aggregates, growth bounded** (option "Amdahl, growth bounded"). Each done run's CPU time, wall time and cores give a parallel fraction, and the median is taken over the window. The ideal width is the most cores at which efficiency stays above 50%, and at most twice the most cores any run had. Rejected: a sampled CPU profile, which is more code and coarse for short jobs (§14); a hill climb with no model, which can't predict durations at other core counts and shrinks jobs that have serial phases.
- **Base claims keep the approved rules.** Only an overrunner's cores change, and a run with spare cores teaches a base claim's memory only as a floor scaled to its cores.
- **The advisor isn't asked how a command scales.** A command never seen gets no spare cores on its first run; every worktree of a repo shares its beliefs, so this is rare. The advisor's reconcile note covers an overrun (§5.5).

## 3. Scope

In: `sched.ScalingOf`; overrunners' cores; `sched.Grant` and the memory it gives; runs with spare cores as floors; durations, overdue marks, the forecast and surprises at a job's cores; the overrun surprise and its prompt line; the `Spare` fields; the claim text; `submit`'s sized line and the follower's start line; the SCALES column; the scaling line in `beliefs show` and the `b` pager; the context sentence; docs.

Out (§14): sampled CPU profiles, a learned memory-per-core figure, changing a running job's cores, confinement, the advisor's estimate of scaling, time-aware admission.

## 4. Concepts and names

- **Busy cores:** a done run's CPU time over its wall time.
- **A run's cores:** the cores its claim had when it started, spare cores included (`Run.Claim.CPUs`). An exclusive run has none, and teaches nothing about scaling.
- **Overrun:** a command whose done runs keep more than 1.25 × their cores busy, by the median of busy ÷ cores: the hint variables don't reach it.
- **Parallel fraction `p`:** the share of a command's work that runs in parallel, as in Amdahl's law. A run at c cores keeping b busy gives (1 − 1/b) ÷ (1 − 1/c).
- **Speed factor:** f(c) = (1 − p) + p/c. A job's wall time at c cores is proportional to it.
- **Ideal width:** the most cores at which the job's efficiency, 1 ÷ (c(1 − p) + p), stays above 50%.
- **Base claim:** the claim sizing gives a job (smart scheduler spec §5.4), the one admission checks.
- **Spare cores:** cores, and the memory they need, added to a base claim when the job starts. `Record.Spare` (a `sched.Claim`) holds what was added; `Run.Spare` holds the spare cores.
- **Grant:** the step that gives starting jobs spare cores, `sched.Grant`.
- **Reserve:** what a grant always leaves free: the policy's default claim, its cores and its memory.
- **`sched.Scaling`:** a belief's `Overruns`, `Busy` (the median busy cores), `Known`, `Parallel` (`p`), `Runs` (the runs `p` came from) and `Ideal`.

## 5. Behaviour

### 5.1 What a belief learns

`sched.ScalingOf(b)`, a pure function, reads the done runs with CPU time among the belief's last 10 runs, leaving out exclusive runs:

- **Overrun** when the median of their busy ÷ cores is above 1.25. `Busy` is the median of their busy cores.
- **`p`**, unless the belief overruns: each of these runs with 2 cores or more gives (1 − 1/b) ÷ (1 − 1/c), held to 0–1, and `p` is their median. With no such run `p` is unknown (`Known` is false), and nothing below that needs it applies.
- **Ideal width:** ⌈(2 − p) ÷ (1 − p)⌉ − 1 for `p` below 1, at least 1, and at most twice the most cores any of these runs had; for `p` of 1, that bound alone.

The constants (1.25, 50%, 2×) are `sched`'s, not settings. On fresco's beliefs:

| belief | `p` | base cores | ideal |
|---|---|---|---|
| `go test -race ./...`: 4 cores, 2.2 busy | 72% | 3 | 4 |
| the wasm build: 3 cores, 2.7 busy | 95% | 4 | 6 (twice 3) |
| `cargo nextest … catalog::`: 2 cores 1.7 busy, 4 cores 1.2 busy | 51% | 2 | 3 |
| the e2e suite: 3 cores, 0.5 busy | 0% | 1 | 1 |
| `vitest run`: 4 cores, 7.8 busy | overruns | 10 | — |

### 5.2 Base claims

`sched.Size` keeps the smart scheduler's rules (its spec §5.4) with two changes:

- **An overrunner's cores** are ⌈1.25 × `Busy`⌉, held to 1 and the cap rather than the default claim's cores: vitest claims 10.
- **A run that had spare cores** teaches a base claim's memory as a floor, its peak scaled by the claim's cores over the run's, while the last 10 runs include a full peak of a run without spare cores: a 10-core run that peaked at 9 GB raises a 4-core claim to at least 5 GB (3.6 GB with the margin) and never sets it alone. With no such full peak, its peak counts as it is, since fewer cores need no more memory (amended while planning: with floors alone, a command whose recent runs all had spare cores would lose its learned memory and ask the advisor for a prior). Its cores and I/O teach as any done run's do.

### 5.3 The grant

In an admission pass, `sched.Grant` runs on the jobs `Admit` starts, before their starts are recorded, and returns each one's spare cores and memory. It grants nothing:

- while a queued job is left waiting, other than one over the caps. A job being sized counts as waiting, and so does every job behind a starved head;
- to a job whose cores aren't learned (they are explicit, exclusive, the agent's or the default), whose memory is explicit or held to the cap, whose `p` is unknown, which overruns, or whose ideal width is at most its base cores.

The pool is the caps minus the running jobs' claims, the starting jobs' base claims and the reserve. In queue order, each job takes the most cores up to its ideal width whose memory fits what is left of the pool, and the pool shrinks by what it took. A job's memory at t cores, for base b cores and base memory m GB, is ⌈m × t ÷ b⌉, lowered to ⌈1.15 × the largest peak⌉ of the done runs that had t cores or more when there is one, but never below m nor above the cap.

The job starts with its base claim plus what it was granted. The hint variables carry the total; the record's claim is the total, which admission counts while the job runs; `Record.Spare` holds what was added, and the run records its spare cores in `Run.Spare`.

On fresco (caps of 14 cores and 22 GB, a default claim of 4 cores and 6 GB), the wasm build starts on an idle host with a base claim of 4 cores and 2 GB and an ideal width of 6. The pool is 14 − 4 − 4 = 6 cores and 22 − 2 − 6 = 14 GB, so it takes 2 cores and 1 GB and runs as `4+2c/3G`. A `go test -race` submitted next (3 cores, 2 GB) fits the reserve and starts at once.

### 5.4 Durations, the forecast and overdue marks

- **`Usual(b, cores)`** scales each duration run's wall time by f(cores) ÷ f(its cores) before taking the median and the P90. It scales nothing when `cores` is 0, when `p` is unknown, when the belief overruns, and for an exclusive run. The prior's duration stands in unscaled. **`OverdueAfter(b, cores)`** uses the scaled P90.
- The view asks at a queued job's base cores and at a running job's total, and so does the forecast. The forecast runs no grant (amended while planning): a grant happens only when no queued job is left waiting except over the caps, and the forecast leaves out jobs being sized, so a simulated grant could never move a forecast start. The host's fallback duration and the advisor's request ask at 0.

### 5.5 Surprises

- **Duration:** a run's wall time is compared with the median of the belief's duration runs scaled to the run's cores.
- **Overrun:** a done run with CPU time that kept more than 1.25 × its cores busy surprises a belief that doesn't overrun yet, on its first run too: "it kept 7.8 cores busy on a 4-core claim: the hint variables don't reach it". The reconcile prompt gains a line asking the agent to name the flag or variable that sets such a command's parallelism, when it knows one, and its note says so.

## 6. The CLI

- **A claim with spare cores** reads base+spare, `4+2c/3G`, wherever a claim is printed: `list`, the follower, `beliefs`.
- **`submit` and `run`** add the ideal width to the sized line when a grant could apply: `sized 4c/2G (learned from 1 run); usually 34s; up to 6 cores when idle`.
- **The follower's start line** names the spare cores: `job 21 started after 6s with 2 spare cores (4+2c/3G)`.
- **`loom job beliefs`** gains a SCALES column after CLAIM: `72%`, `0%`, `overruns`, or `-` while `p` is unknown.
- **`loom job beliefs show`** gains a scaling line in the belief text's label column: `scaling   95% parallel over 1 run · efficient up to 6 cores · ~34s at 4, ~24s at 6` (at the base claim's cores and the ideal width); `scaling   ignores the hint variables · keeps 7.8 cores busy, claims 10` for an overrunner (", claims N" only when there is a claim); `scaling   unknown until a run with 2 cores or more` otherwise.
- `--json` carries `JobView.Spare` and `BeliefView.Scaling`.

## 7. The work view

- The claim column widens by one for `4+2c/3G` (`workClaimW` 12).
- The `b` pager shows the scaling line of §6.

## 8. Loom's Claude context

The work-queue paragraph's sizing sentence becomes: "The scheduler sizes each job from what the same command used before, and lends idle cores to jobs that scale, so pass `--cpus`, `--mem` or `--io` only to override it; use `--exclusive` for benchmarks."

## 9. Wire and files

`JobView.Spare` (a `sched.Claim`), `Run.Spare` (inside a `BeliefView`'s belief) and `BeliefView.Scaling` (a `sched.Scaling`) are added, with `JobView.Ideal`, the width a grant could widen a queued job to (`sched.SpareWidth`), 0 when none could, for the sized line (added while planning). `docs/specs/protocol.md` is regenerated. `jobs.json` and `beliefs.json` gain optional `spare` fields and keep their versions: an older build ignores them and counts a granted job's total claim as its claim.

## 10. Components

| unit | change |
|---|---|
| `sched/scaling.go` (new) | `Scaling`, `ScalingOf`, the speed factor and the ideal width |
| `sched/grant.go` (new) | `SpareWidth`, `Grant`, and a job's memory at t cores |
| `sched/size.go` | overrunners' cores; runs with spare cores as scaled floors |
| `sched/durations.go` | `Usual` and `OverdueAfter` take the cores |
| `sched/surprise.go` | the scaled duration check; the overrun surprise |
| `sched/belief.go`, `sched/job.go` | `Run.Spare`, `Record.Spare`, `RunOf`, the claim text |
| `sched/beliefs_view.go`, `sched/belief_text.go` | `BeliefView.Scaling`; the scaling line |
| `sched/advisor/prompt.md` | the reconcile line on overruns |
| `core/queue_admit.go` | `Grant` after `Admit`; `startJob` applies it |
| `core/queue_view.go` | `JobView.Spare`; durations at a job's cores |
| `internal/cli` | the claim text, the sized and start lines, SCALES, the scaling line |
| `ui/work.go` | the claim column's width |
| `session/loom_context_shared.md` | the sizing sentence |

## 11. Failure handling

- **Learning off** (smart scheduler spec §5.3): there is no belief, so no scaling and no grant, and durations are as before.
- **Admission paused:** nothing starts, so nothing is granted.
- **A record queued by a build before sizing** has no learned cores and gets no grant.
- **A grant whose memory doesn't fit** gives fewer cores, or none.
- **A cap lowered while a granted job runs** leaves its claim as it is, like any running claim.
- `ScalingOf` and `Grant` read no file and run no process: a wrong figure costs a slower or a wider job, never a blocked loop.

## 12. Testing

- **`sched`:**
  - `TestScalingOf`: overrun at the median; a run's `p` held to 0–1; the median; unknown with only 1-core runs; exclusive runs left out; the ideal width at 50% and its 2× bound; `p` of 1.
  - `TestGrant`: nothing while a job waits, over-cap jobs aside; a job being sized and a starved head block it; the reserve; queue order; the memory rule and its fit; each case that gets nothing.
  - New cases in `TestSize` (an overrunner past the default up to the cap; a spare run as a scaled floor), `TestUsual` and `TestOverdueAfter` (at other cores) and `TestSurprise` (the scaled duration; an overrun on a first run; none for a belief that already overruns).
- **`core`:** a pass on an idle queue grants, writing the total into the record and into `spec.json`'s hint, and `Spare` into the record and then its run; a pass with a job left waiting grants nothing; a resize never touches a running claim.
- **`internal/cli` and `ui`:** `4+2c/3G`, the sized and start lines, SCALES, the scaling line, the claim column.
- No test calls Claude.

## 13. Build order

Two packages, each leaving the build green with its docs:

1. **The model:** `ScalingOf`, overrunners' cores, `Grant` and its memory, spare runs as floors, durations, the forecast, surprises and the reconcile prompt's line, the fields, the core wiring and the wire.
2. **The surfaces:** the claim text, the sized and start lines, SCALES, the scaling line, the work view, the context sentence, and the docs.

## 14. Known limits and later work

- **Width limits are invisible.** Aggregates can't tell a job that keeps 6 of 10 cores busy throughout from one with serial phases: both read as about 90% parallel, and the ideal width can be wider than the job can use. Later: the usage sampler reads CPU time too, so a run records how long it kept its cores busy.
- **Memory at a new width is modelled, not measured.** A grant's memory grows in proportion to its cores, which assumes more cores never use less memory and over-claims a job whose memory is mostly fixed. A spare run's scaled floor can under-claim a base run whose worst case was only seen with spare cores; the 15% margin absorbs some of that. Later: a memory-per-core figure learned from runs at several widths.
- **A grant lasts the whole run.** The hint variables are set at exec, so spare cores stay with the job until it ends, and beyond the reserve a job submitted meanwhile waits for them. Later: per-job cgroups or cpusets that can change a running job's share.
- **Overrunners are counted, not confined.** Nothing stops vitest taking 16 cores while it claims 10.
- **No spare cores on a first run,** since the advisor isn't asked how a command scales.
- **Warm and cold builds share one `p`.** A warm build is mostly serial and a cold one mostly parallel; the median blends them.
