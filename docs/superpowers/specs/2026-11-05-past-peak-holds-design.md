# Work queue: holds that fall once a job is past its peaks

Status: design approved 2026-11-05.

Builds on [the work scheduler](2026-11-04-work-scheduler-design.md), [the smart scheduler](2026-11-05-smart-scheduler-design.md) and [job scaling](2026-11-05-job-scaling-design.md). The owner's idea: keep a sampled timeseries of each job's usage, so that once a running job is past its peaks the queue lets other jobs in.

## 1. Problem

A job's claim is fixed from admission to exit: the queue counts it at its learned peak (memory: the largest peak plus 15%; cores: 1.25 × the median busy cores) for the whole run, although most runs spend most of their time well below that peak. On fresco, the last 44 done jobs in `jobs.json` (2026-11-05):

- cores: 9,817 core-seconds used of 13,669 claimed (72%); the median job kept 0.57 of its claimed cores busy;
- memory: the median job peaked at 0.40 of its claimed GB, and peak × wall time was 18,414 GB-seconds of 28,227 claimed (65%), an upper bound on what the jobs used, since a job sits at its peak only briefly;
- waits: of the last 51 started jobs, 7 waited over 30 s and 1 over 5 minutes (a 12 GB claim, 7.6 minutes); the long waits were the memory-heavy claims.

`applyUsage` already samples every running job every 3 s, but it keeps only the latest values and the peak, so nothing knows when in a run its peak falls.

Success, read after a week of use (§11): fewer and shorter waits than this baseline, held GB-seconds well below claimed GB-seconds, and at most one or two broken holds a week.

## 2. Decisions

- **Admission only** (brainstorm option A). The timeseries drives admission; no graphs or sparklines. Rejected: graphs in the work view or the phone, alongside or as the main goal. The one visible change is the hold text (§7), without which nobody can see why a job was let in.
- **Conservative memory** (option A). A running job keeps reserving the most memory its own past runs used from this point to the end, plus a margin; memory is given back only where history says it won't be needed again, and any deviation returns the job to its full claim. Rejected: admitting against the expected concurrent use with a host-pressure brake and SIGSTOP of the newest job; admitting against the host's live free memory less a headroom (it races: a job admitted 3 s ago hasn't allocated yet).
- **Cores the same way** (option A): one mechanism and one stored profile for both. Rejected: counting running jobs at their measured busy cores with a CPU-pressure brake (busy cores read low on a contended host, so it admits more exactly when the host is overloaded); cores left as fixed claims.
- **A staircase per run, aligned by CPU-seconds, with a "seen its peak" guard** (option A). Rejected: full sampled curves aligned by the fraction of wall time (about ten times the storage, and wall time drifts with contention and with the cores a job got); two numbers per resource, the peak and the most after it (gives back only one step).
- **A switch.** `release_past_peaks` in the `scheduler` block, on by default.

## 3. Scope

In: the in-memory trace, the per-run profile and its storage, `sched.Hold`, holds in `Admit`, `Grant` and `Forecast`, broken holds, the switch, the hold text in the work view and the CLI, the queue-wait log line, docs.

Out (§12): graphs, confinement (cgroups), host-pressure brakes, overcommit beyond what history shows, warm and cold builds told apart, profiles for adopted runs.

## 4. Concepts and names

- **Progress `x`:** a run's cumulative CPU-seconds, as the usage sampler reads its session (on Linux each process's CPU with its reaped children's). It moves with work done, not with the wall clock, so contention and the cores a job got shift it less than they shift wall time.
- **Trace:** a running job's samples in memory, each a point (`x`, RSS in KB, busy cores since the previous sample), at most 256 points (§5.1). `queueState.traces`.
- **Staircase:** for one resource, the most a run still used from each point to its end, as a function of `x`. It never rises, so it is stored as at most 8 steps; each step is (`until`, `level`): the level holds for `x` below `until`.
- **Profile:** a run's two staircases (memory in KB, cores in milli-cores), the `x` at which it peaked in each resource, and the `x` at which it ended. `Run.Profile`, a `*sched.Profile`.
- **Profiled run:** one of a belief's runs carrying a profile.
- **Hold:** what a running job takes from the caps now: its claim, or less once released (§5.3). `queueState.holds`.
- **Release:** a hold's first fall below the claim.
- **Broken hold:** a released job whose usage rose above its hold (§5.4). `Record.HoldBroke`, copied to `Run.HoldBroke`.

## 5. Behaviour

### 5.1 The trace

`applyUsage` appends a point to the job's trace each time it reads the job's session: `x` (the session's CPU-seconds), its RSS and its busy cores since the last sample (`jobUsage.CPU`). A point whose `x` is below the previous point's (a process left the session with its CPU time) takes the previous `x`, so `x` never falls. When the trace holds 256 points, adjacent pairs merge into one with the later `x` and the higher RSS and cores, so the trace keeps every peak at half the resolution. The trace also notes the time of its last point; a gap of more than 30 s between points (failed or skipped samples) marks it incomplete.

A trace is dropped when its job settles or is removed. An adopted job (`Record.Adopted`) gets no trace: its first samples come partway through its run.

### 5.2 The profile

When a job settles, `recordRun` builds a profile (`sched.BuildProfile`) from its trace when the run is done, not adopted, and its trace is complete with at least one point. For each resource it takes the suffix maximum of the trace (the most from each point to the end), then compresses that staircase to at most 8 steps by merging, each time, the adjacent pair whose merge adds the least area; a merge keeps the higher level and the later `until`, so the compressed staircase is never below the real one at any `x`. Levels are rounded up (memory to whole MB, cores to whole milli-cores). The profile also records each resource's peak `x` (the first point at the trace's maximum) and the end `x` (the last point's).

The profile is stored on the run in `beliefs.json` as `profile`: `{"end": x, "mem": {"peak_at": x, "steps": [[until, kb], …]}, "cores": {"peak_at": x, "steps": [[until, milli], …]}}`. When a run is added to a belief, only the newest 5 runs keep their profiles; older runs lose the field. At the 1,000-belief limit this is about 1 MB. `beliefs.json`'s version does not change: an older loom ignores the field on read and drops it on write, losing profiles and nothing else.

A profile that fails validation on load (steps out of order or not falling, a level or `x` below 0, a peak or end inconsistent with the steps, more than 8 steps) is ignored for holds, logged once per signature (`queue.profile_invalid`), and kept in the file as it is.

### 5.3 The hold

`sched.Hold(rec, belief, trace, fresh, prev, policy)` returns where a running job's hold stands (`sched.HoldState`) and what it counts at (a `sched.Need`). It is pure, called on the loop by each admission pass for every running job (§6). The hold is the job's claim (`Claim.Need`) unless every rule below passes; rules 3 to 5 apply to memory and cores separately, so one resource can be released while the other is not.

1. **History.** The belief has at least 3 profiled runs (the newest 5 carry profiles), and none of its last 10 runs has `HoldBroke`.
2. **A complete trace and an ordinary claim.** The job was not adopted, its trace is complete, this pass's sample succeeded, learning is on (no `beliefsErr`), `Policy.ReleasePastPeaks` is true, and the claim is neither exclusive nor fully explicit (`--cpus` with `--mem`: the submitter asserted something history doesn't show).
3. **Seen its peak.** Let `A(x)` be the profiled runs whose end is past the job's current `x`. The job's own observed peak in the resource (the trace's maximum) has reached 90% of the lowest peak among the profiled runs, and `x` is past the peak `x` of every run in `A(x)`.
4. **Not outlasting.** `A(x)` is not empty. The envelope `E(x)` is the highest level, at `x`, among the staircases of the runs in `A(x)`: runs that ended before `x` drop out, since this run has already shown itself longer than them.
5. **Tracking.** The job's usage at its last 2 points is at or below `E(x)`, the envelope now.

When all pass, the resource's candidate is `E(x)` plus 15% for memory and plus 25% for cores, rounded up to whole GB and whole cores, at least 1, at most the claim. A job not yet released holds its claim until its candidate is below the claim; from then on its stored hold is the lower of its stored hold and the new candidate, so a stored hold only falls. After release, rules 3 and 5 no longer apply to that resource. In a pass where rule 1, 2 or 4 fails for a released job (a failed sample, the switch turned off, a job outlasting every profiled run), the job counts at its claim for that pass and its stored hold is kept; this is not a break, and it only stops new admissions. Rule 4 never passes again once it fails, since `x` never falls, so a job outlasting every profiled run holds its claim to the end.

The I/O slot and an exclusive claim are never released: they are held until the job ends.

### 5.4 A broken hold

On each sample, a released resource whose usage is above the job's hold breaks it: both resources' holds return to the claim for the rest of the run, `Record.HoldBroke` is set and saved with the record, and `queue.hold_broken` is logged with the job, the signature, the resource, the hold and the usage. `RunOf` copies `HoldBroke` to the run, so rule 1 keeps that signature from releasing for its next 10 runs.

What was handed out cannot be taken back: the jobs admitted on the released share keep running. The break's effect is to stop admitting, as a raised claim already does when free reads below 0.

## 6. Admission

`admitJobs` computes the holds (`computeHolds`) after `resizeQueued` and before `sched.Admit`, and passes them as `holds map[JobID]Need`. `Admit`, `Grant` and `Forecast` take the map and count a running job at its hold wherever they now count its claim; a running job with no entry counts at its claim. The rest is unchanged: free never reads below 0, the starvation guard, the order, the reserve of a grant (the caps less the running holds, the starting base claims and the default claim). `Forecast` keeps each running job's current hold until its expected end; holds only fall, so the forecast is pessimistic, and it is advisory.

A settle and a removal clear the job's entries in `queueState.traces` and `queueState.holds`. Holds are not persisted: a restarted daemon adopts its running jobs at their claims (rule 2).

`queue.hold_released` is logged once per job and resource at release, with the hold and the claim. At each start, `queue.job_started` is logged with the job, the signature and the seconds it waited since it joined the queue (`QueuedSince`), for §11.

## 7. What people see

- **`JobView.Holds`** (`sched.Need`, zero when the job holds its claim), and **`QueueView.Held`**, the sum of the running jobs' holds beside `Claimed`. The protocol reference is regenerated (`go test ./core/rpc -run TestProtocolReference -update`).
- **The work view.** A running job holding less than its claim says `holds 1c/2G` in its row's trailing detail (the claim column keeps its width). The header's CPU and MEM bars draw what is held, and their text reads `8/14 held · 12 claimed · 5.1 in use` while holds are below claims, and as now otherwise.
- **The CLI.** `loom job list` adds `holds 1c/2G` after the claim on a running job holding less than its claim.

No graphs, and nothing on the phone.

## 8. Configuration

`config.SchedulerSettings` gains `ReleasePastPeaks *bool` (`"release_past_peaks"`), absent meaning true; `sched.Resolve` copies it to `Policy.ReleasePastPeaks`. A value that does not decode as a boolean makes the block invalid, which pauses admission with the reason, as any field does. Turning it off returns every hold to its claim at the next pass. The work view's caps form doesn't show it, and a caps edit keeps the file's value (`setSchedulerCaps`, as for the `agent` block). `USAGE.md` documents it.

## 9. Components

| Where | What |
|---|---|
| `sched/profile.go` (new) | `Trace` (append, merge to 256, gap tracking), `Profile`, `Staircase`, `BuildProfile`, `Profile.Validate` |
| `sched/hold.go` (new) | `Hold`, its reasons, the envelope |
| `sched/belief.go` | `Run.Profile`, `Run.HoldBroke`; adding a run strips profiles past the newest 5 |
| `sched/job.go` | `Record.HoldBroke`, `Policy.ReleasePastPeaks` |
| `sched/policy.go`, `config/settings.go` | the switch |
| `sched/admit.go`, `sched/grant.go`, `sched/forecast.go` | the `holds` argument |
| `core/queue.go` | `queueState.traces`, `queueState.holds`, cleared with the job |
| `core/queue_watch.go` | `applyUsage` appends to the trace and checks for a broken hold |
| `core/queue_admit.go` | `computeHolds`; `queue.job_started` |
| `core/queue_beliefs.go` | `recordRun` attaches the profile |
| `core/queue_view.go` | `JobView.Holds`, `QueueView.Held` |
| `ui/work.go`, `internal/cli` | the hold text and the header bars |
| docs | `sched/README.md`, `docs/claude/the-work-queue.md` (rule 9's sizing gains the hold), `USAGE.md`, `docs/specs/protocol.md` (generated) |

## 10. Failure handling

Every case falls back to the claim or records nothing:

| Case | Result |
|---|---|
| This pass's sample failed | every running job counts at its claim for this pass; holds resume at the next good sample |
| Learning off (`beliefsErr`) | no profiles recorded; every job holds its claim |
| A profile fails validation | ignored for holds, logged once per signature, left in the file |
| A trace with a gap over 30 s | no profile for that run; no release for that job |
| Daemon restart | traces and holds lost; adopted jobs hold their claims, and their runs get no profile |
| A usage above a released hold | a broken hold (§5.4) |
| The switch unreadable | admission paused with the reason |

## 11. Testing

- **`sched`, unit:**
  - `BuildProfile`: the compressed staircase is never below the suffix maximum at any `x`, over random traces; at most 8 steps; a peak `x` and end `x` as defined; levels rounded up.
  - `Trace`: merging to 256 keeps the maximum of every resource; `x` never falls; a gap over 30 s marks it incomplete.
  - `Hold`: each of rules 1 to 5 on both sides, once just passing and once just failing, and each term of a compound rule (rules 2 and 3) mutation-checked separately; the margins and rounding; never above the claim, never below 1; a hold only falls; an exclusive claim and an I/O slot never released.
  - `Admit`, `Grant`, `Forecast` with holds: a hold below its claim starts a job that otherwise waits; a missing entry counts at the claim; a hold above what's free starts nothing new.
  - `Profile.Validate` on each invalid shape.
- **`core`**, with samples delivered through the pass (`m.Deliver(queuePassResult{…})`) to a real shim (`wqLaunch`) or a seeded record, and a real submit for the job a release admits:
  - a belief seeded with 3 profiled runs and a job (`sh -c` that allocates, frees and idles) releases its hold and lets a second job start that its claim kept out;
  - a released job whose usage rises breaks its hold, records `HoldBroke` on its run, and the signature's next job does not release;
  - an adopted job holds its claim;
  - the switch off holds every claim.
- **`ui`, `internal/cli`:** the hold text and the header's held line.
- **Gates:** the full suite, `-race`, e2e, the Windows build, the CLAUDE.md lint.

**After a week:** compare against §1's baseline: how often jobs waited and how long (`queue.job_started`), claimed against held GB-seconds, and the count of `queue.hold_broken`. More than one or two breaks a week means rule 3 needs tightening.

## 12. Known limits and later work

- **Warm and cold builds share a belief.** A cold build profiled only against warm runs can, when sampled during a lull at the very `x` where the warm runs had linked, pass rule 5 and release before its own link. The 2-sample dwell narrows this, and §5.4 limits it to one incident per signature. Later: the smart scheduler's §15 cold-build feature.
- **No confinement.** A broken hold can't take memory back. Later: per-job cgroups (`memory.high`), which would let a hold become a limit.
- **3 s samples** can miss a short spike, in the trace as in today's peak; the margins absorb it.
- **Graphs.** The trace could feed sparklines in the work view and the phone; the owner chose admission only.
- **Host pressure.** PSI or MemAvailable as a brake on admission, or to admit beyond history, is not used.

## 13. Planning amendments

Made while writing [the plan](../plans/2026-11-05-past-peak-holds.md), 2026-11-05:

1. **`Hold` returns no reason.** It returns the state and the need; the tests pin each rule by its outcome on both sides.
2. **Rule 5 compares the last 2 points with the envelope now, `E(x)`,** not with the envelope at each point's own progress: compared with their own, a point from before the drop passes trivially and the dwell is no dwell.
3. **Holds are refreshed after every sample (`deliverQueuePass`) and at every admission pass (`admitJobs`),** so the view follows a hold while nothing waits.
4. **A step is written as the pair `[until, level]`,** and every progress (a step's `until`, a peak's, the end) is rounded up to 0.1 CPU-second: later, so never lower.
5. **`loom job list` only:** there is no `loom job show`. The hold text is `Need.HoldText` (`holds 1c/2G`), shared by the work view and the CLI, as `Claim.Spared` is.
6. **The header shows `held` only while the held total is above 0 and below the claimed one,** so a view from a build without holds never reads `0/14 held`.
7. **An invalid profile is logged once at load (`loadBeliefs`) per signature.** Profiles made at run time are valid by construction.
8. **The core tests deliver synthetic samples through the pass** to a real shim or a seeded record, so a trace is exact; the job a release admits is a real submit.
9. **A caps edit keeps the file's `release_past_peaks`** (found by the full suite's run, added during execution): the caps form sends only its own fields, so without it every edit in the work view would turn a `false` back to the default.
