# Dependent jobs: a job that runs after another

Status: design approved 2026-11-05.

Builds on [the work scheduler](2026-11-04-work-scheduler-design.md), [the smart scheduler](2026-11-05-smart-scheduler-design.md) and [job scaling](2026-11-05-job-scaling-design.md).

## 1. Problem

A job holds its claim from its start to its end. A command whose phases need different amounts claims the largest for all of them: `cargo test` claims what its compile peaked at while its tests run, and the tests may run for many minutes on a fraction of it. Fresco's queue on 2026-11-05:

- Job 80 (`bash scripts/verify-affected.sh`) held 4 cores and 12 GB for 29 minutes. Its phases were a clippy build (8m41s), a nextest build and run, doctests, and the wasm and runtime gates; its 8.2 GB peak came from one of them.
- Agents already split builds from runs by hand: `cargo test -p prader-server --no-run` ran three times as a job of its own, and `cargo test -p kermit --features engine-duckdb,engine-nemo --no-run` once.
- The queue fills: 12 of the last 53 jobs waited over a minute to start. The longest waits that day came from an exclusive job draining the host, which splitting would not change.

The workaround today is `loom job run -- <build> && loom job submit -- <tests>` in a background Bash call. It orders the two and skips the tests when the build fails, but:
- the second job joins the back of the queue, so it can wait longer than the claim it gave back saves;
- the chain lives in the agent's shell and dies with it;
- the second step shares its signature with the cold command (`cargo test` after `cargo test --no-run` is the same signature as `cargo test` alone), so sizing gives it the compile's peak and the split saves nothing.

Success looks like this, checked from `jobs.json` and `beliefs.json` after a week of use:
- a dependent whose claim fits what its prerequisite freed starts within one queue gate interval (3 s) of the prerequisite's end;
- a dependent's learned memory claim is below the cold command's;
- a chain that succeeds sends its owner one message.

## 2. Decisions

- **Chains an agent writes** (brainstorm option A). The agent submits separate commands, each its own job with its own claim, belief, log and ID, linked by `--after N`. Rejected: phases inside one running job (`loom job phase <name>`, its claim dropping as phases pass), which would cover a gate script like `verify-affected.sh` but needs loom calls in scripts that CI shares; and phases alone.
- **One prerequisite per job, any number of dependents** (option A). Several shards can follow one build. Rejected: strict chains (one dependent each); a full graph, whose fan-in (`--after 14,15`) no case needs yet.
- **One language-agnostic sentence in loom's context** (option A, "make it language agnostic"). Agents judge when a split pays. Rejected: the flag alone, which agents would rarely find; splitting by default, which charges short suites two jobs and a first-sight advisor call.
- **A `held` state** (approach 1). Admission, the grant and the starvation guard see only queued jobs, so they stay as they are, and an older daemon never starts a held job. Rejected: a queued job that `Admit` skips until its prerequisite is done (`Grant` lends nothing while a queued job waits, so a waiting dependent would keep its own build from spare cores; the forecast and the place-in-line count would need exceptions; and an older daemon would start the dependent beside its build unless `jobs.json`'s version went up, which pauses an older daemon's whole queue); a follow-up stored on its prerequisite's record until it can run (no ID to wait on, cancel or chain a third step after).
- **The rest of §5 was approved with the design:** a dependent keeps its prerequisite's place in line, a finished prerequisite's place can't be borrowed, a prerequisite must have the same owner, failures cascade, a chain sends one message, a dependent's signature carries its prerequisite's, and a dependent that peaks at over twice its claim is told so.

## 3. Scope

In: `--after` on `submit` and `run`; the `held` state; release and the cascade; the place in line; `Record.Ready` and the starvation clock; the messages; the signature's prerequisite part and the beliefs file's version; the advisor's request; the forecast; the CLI lines; the work view; the checks at load; the context sentence; docs.

Out (§13): fan-in, running after a failure, another session's prerequisite, passing output paths between jobs, phases inside one job, splitting commands automatically, snapshotting the worktree for a chain.

## 4. Concepts and names

- **Prerequisite:** the job a job names with `--after N` (`Record.After`, `Submit.After`; 0 for none).
- **Dependent:** a job with a prerequisite. A job may be the prerequisite of several.
- **Held:** `sched.Held` (`"held"`), a dependent whose prerequisite hasn't ended. It isn't finished (`State.Finished` is false) and admission never sees it. The states become `held` → `queued` → `running` → `done` | `failed` | `cancelled` | `lost`; a job submitted without a prerequisite, or after one that is already done, starts at `queued`.
- **Waiting:** `sched.State.Waiting`, true for `queued` and `held`, for the places that treat both alike (cancel at once, move to the front, live sizing, the advisor's asks, the view's waiting jobs) (added while planning).
- **Release:** a held job becoming `queued` when its prerequisite ends `done`.
- **Cascade:** a prerequisite that ends `failed`, `cancelled` or `lost` cancels its held dependents, and each of those cancels its own.
- **Ready:** `Record.Ready`, when a job became `queued`: its submit, or its release. Records from earlier builds have none, and `Submitted` stands in.
- **Chain:** a prerequisite and the jobs that follow it.
- **Carried:** `sched.NoticeCarried` (`"carried"`), the fate of a prerequisite's message when its dependents' messages report it (§5.5).

## 5. Behaviour

### 5.1 Submitting

`loom job submit --after N` and `loom job run --after N` set `Submit.After`. `Submit.Validate` refuses a negative value (`--after is -2: it must be a job ID`). `submitJob` checks the prerequisite after the existing checks and before it sizes the claim, since the signature depends on it (§5.6), and refuses, creating nothing, when:

- no record of job N exists, whether it never did or retention dropped it: `there is no job 14`;
- job N's owner isn't the submitter's: `job 14 has another owner: --after takes a job with the same owner as the new one`. Owner 0 counts as an owner, so a job from a plain shell can follow only a job from a plain shell;
- job N ended other than `done`, named as the cascade names it (§5.4): `job 14 failed (exit 101), so a job after it would never run`, or `was killed by SIGSEGV`, `could not start`, `was cancelled`, `was lost` (amended while planning: the same words as the cascade's reasons).

Otherwise:
- when job N is `queued`, `held` or `running`, the new job is `held`, and its `Order` is job N's (§5.2);
- when job N is `done`, the new job is `queued` at once, with its own ID as its `Order` and `Ready` set to now, like any new job. A finished job's place can't be borrowed to jump the queue.

Owners resolve as today (`jobOwner`): a subagent shares its parent's launch, so its jobs and its parent's have one owner.

### 5.2 Place in line

- A held job's `Order` is its prerequisite's at submit, so a whole chain shares one `Order`, and jobs with equal `Order` go by ID.
- On release, a job's `Order` becomes the lower of its own and its prerequisite's current one. Moving a prerequisite to the front (`t`, `MoveJobToFront`) therefore carries its dependents along, and a held job moved to the front itself stays there.
- `MoveJobToFront` accepts a held job as well as a queued one, and sets the `Order` one below the lowest `Order` of every queued and held job, for the job and every job held down its chain, so the view lists the chain where it will run and a job someone moves to the front stays ahead of a chain released after the move (amended while planning: the chain moves with it).

### 5.3 Release and the starvation clock

- When a job settles `done` (`settledJob`), every held job naming it is released in the same loop step: `queued`, `Ready` set to now, and its `Order` as in §5.2. The admission pass that follows the settle (`afterSettle`) then considers it, usually first, and it usually fits, since it needs no more than its prerequisite just freed.
- `Admit`'s starvation guard measures the head's wait from `Ready` (from `Submitted` when `Ready` is zero), so a job released just now isn't a starving head because its chain began long ago.
- A queued job's wait is measured from `Ready` wherever one is shown: the message's "in the queue" (§5.5), `list`, the work view and the follower's start line. A held job shows its wait since submit (§6).
- `sched.Forecast` simulates releases: when a prerequisite's simulated end comes, its held jobs become queued in the simulation at that clock, with `Ready` and `Order` set as above, so a held job gets an expected start. A held job whose prerequisite gets no simulated start gets no entry, as a job past the forecast's bounds gets none today.
- `Grant` is unchanged: held jobs never appear in `Admit`'s `Waiting`, so a chain that waits on a build doesn't stop the grant from widening that build.

### 5.4 The cascade and cancelling

- When a job settles `failed`, `cancelled` or `lost`, every held job naming it is cancelled in the same loop step, with the reason by how it ended: `because job 14 failed (exit 101)`, `because job 14 was killed by SIGSEGV`, `because job 14 could not start`, `because job 14 was cancelled`, or `because job 14 was lost`. Each job cancelled that way cancels its own held dependents, `because job 15 was cancelled`.
- `CancelJob` on a held job ends it at once, as on a queued one, and the cascade follows.
- `cancelOwnerJobs` (a pause, a kill or a reset someone requests) first marks every job of the owners (no message, the reason), then ends the queued and held ones at once, so a cascade one of them sets off finds the jobs after it marked, and they keep the pause's reason and send nothing (amended while planning).
- `NotifyJob` accepts a held job: it hasn't ended.
- Release and the cascade happen within the settle, before any admission pass, so no pass sees a held job whose prerequisite has ended.

### 5.5 Messages

- **A prerequisite that succeeds while a job is held after it** sends nothing. Its notice fate is `carried`, with `NoticeWhy` `its result goes with job 15's` (or `with jobs 15 and 16`), which holds whether or not those jobs notify: one followed with `run` reports through its follower. A prerequisite with no held dependents when it ends sends its message as today.
- **A dependent's message** names its prerequisite in the first line: `[loom] job 15 "run tests" finished: exit 0 after 9m12s (2s in the queue, after job 14 "build tests", done in 3m10s).` A job with several dependents gets each one's message when that one ends.
- **A cascade sends one message per branch:**
  - The job that failed sends its own message, when it notifies, with a line after the first naming every job the cascade cancelled, in ID order: `Job 15 "run tests", which waited on it, was cancelled.` or `Jobs 15 "run tests" and 16 "bench", which waited on it, were cancelled.` Each job the cascade cancelled is then recorded `carried`, `its result goes with job 14's` (amended while planning).
  - When it doesn't notify (`--no-notify`, or followed with `run`), each job the cascade cancelled that notifies sends one, unless a job it waited on, directly or through others, already sent one for this cascade: `[loom] job 15 "run tests" was cancelled because job 14 "build tests" failed (exit 101).` It carries the failed job's log line and tail (at the cancelled job's `--tail`) and names the jobs the cascade cancelled after it. A job a message above it reports is recorded `carried` by that message's job.
- **A dependent that peaked at over twice its memory claim** gets a line after the first: `It peaked at 6.1 GB on a 2 GB claim: if it rebuilt what job 14 built, run job 14's output instead.` The peak is the larger of the sampled group peak and the command's max RSS, as `sched.RunOf` takes it; an exclusive claim or a job with no measured peak gets no line. Learning still records the peak, so the next claim covers it.
- Pauses, kills and resets send nothing, as today, and a notice the owner can't take is recorded undelivered, as today.

### 5.6 Sizing

- A held job is sized at submit as a queued one is (`sizeRecord`), `resizeQueued` re-sizes held jobs as well as queued ones, and `askForPrior` may mark a held job `asking`, so the advisor's call runs while the job waits. A released job still being sized waits as today ("being sized (asking the agent)").
- **The signature carries the prerequisite:** `sched.Signature.After` is the prerequisite's signature ID, or `none` when the prerequisite has none. `Signature.ID` hashes `[repo, folder, command, env]` when `After` is empty and `[repo, folder, command, env, after]` when it is set, so every existing signature keeps its ID. `cargo test` after `cargo test --no-run`, `cargo test` after `cargo build` and a cold `cargo test` are three beliefs.
- **`beliefs.json`'s version** is 2 while it holds a belief whose signature has an `After`, and 1 otherwise; `LoadBeliefs` reads both, and `BeliefsVersion` (the highest it reads) becomes 2. An older loom then reports a newer file and runs without learning, leaving the file alone; without the version it would refuse the whole file on an ID it computes differently, with a less useful reason.
- **The advisor's request** (`advisor.Job`) gains `after`: the prerequisite's command (as `Command` is given) and description. The prompt gains a line: a job with `after` runs right after that command in the same folder and usually reuses what it made, so its memory is often the cold command's run phase, not its build.
- **Display:** `loom job beliefs` shows such a command as `cargo test (after cargo test --no-run)`, and `loom job beliefs show` and the work view's `b` pager add an `after` line in the label column with the prerequisite's command, or its signature ID when that belief has gone.

### 5.7 Loading and older daemons

- `jobs.json` stays at version 1: `after` and `ready` are new fields and `held` a new state. An older daemon admits only `queued` jobs, so it never starts a held one, but it drops `after` and `ready` when it saves, and it can record a pause's cancel request on a held job without ending it.
- `loadQueue` checks the held jobs before `adoptJobs` (amended while planning: `adoptJobs`' settles end in `afterSettle`, which runs an admission pass, so a check after it would come after a pass), in ID order, so a chain resolves from its first job down, and the held jobs that carry a cancel request first (a release would otherwise take one back into the queue with its siblings); `adoptJobs` then settles through `settledJob`, so a prerequisite that ended while the daemon was down releases or cascades there. A held job is:
  - released when its prerequisite is `done`;
  - cancelled, cascading, when its prerequisite ended any other way;
  - cancelled `because its prerequisite is unknown` when `After` is 0 or names no record (an older daemon's save, or a store edited by hand);
  - cancelled with its recorded reason when a cancel was requested;
  - left held when its prerequisite is queued, held or running.
- A released job that an older daemon saved without `ready` falls back to `Submitted`, so the starvation guard may count its whole chain's time: at worst it becomes a starving head, which starts alone or holds the rest back until it does.

## 6. The CLI

- `--after N` on `submit` and `run`, in their help: "run after job N ends done; held until then, and cancelled if it ends any other way".
- `submit` prints `job 15 held: runs after job 14 (running)` for a held job, and today's `job 15 queued (…)` line for one submitted after a done job; the sized line follows as today.
- The follower (`run`, `wait`) prints `loom: job 15 held: runs after job 14 (queued)` when the job is held and again whenever the prerequisite's state changes, then today's queue lines. Its start line measures the wait from `Ready` (unchanged for every job but a released one, whose line reads `job 15 started 2s after job 14 ended`). A job the cascade cancelled ends the follower with exit 125 and `loom: job 15 was cancelled because job 14 failed (exit 101)`.
- `list` shows held jobs among the queued ones, in queue order, with state `held`, their wait since submit, and the reason `after job 14 (running)` (or `(queued)`, `(held)`). (`list` has no `--json`: a sentence on it went, amended while planning.)

## 7. The work view

- QUEUED lists held jobs in queue order, where they will run, with the reason `after job 14 (running)` and their forecast start.
- `x` on a job with held dependents confirms `Cancel job 14 "build tests" and job 15 that waits on it?` (or `and jobs 15 and 16 that wait on it?`).
- `t` on a held job moves it to the front (§5.2).
- A session's badge reads `⚙ queued` when its jobs are only queued or held.

## 8. Loom's Claude context

The work-queue bullet in `session/loom_context_shared.md` gains, after its sentence on sizing:

> When tests run for minutes after a heavy build, submit them as two jobs, the build and then the tests with `--after N`: the build's memory is freed while the tests run, the second job keeps the first's place in line, and one message reports both. Make the second job run what the first built rather than build it again (the test binaries, or an archive of them), since a rebuild there runs on a smaller claim.

`TestLoomContext_NamesRealSessionFlags` checks the `loom job` spans the context names, so `--after` must exist when the sentence lands.

## 9. Wire

- `sched.Submit.After` and `sched.Record.After` (`JobID`); `sched.Record.Ready` (`time.Time`, `omitzero`); `sched.Held`; `sched.NoticeCarried`; `sched.Signature.After` (`SignatureID`, `omitempty`); `core.JobView.After` and `.Ready`; `advisor.Job.After`.
- `sched.State.Waiting`; `sched.JoinAnd` (the exported `joinAnd`, for the cascade's and the cancel prompt's lists); `core.JobView.QueuedSince` (added while planning).
- No new method or event. The wire is JSON, so the fields are additions and `rpc.Protocol` is unchanged. `docs/specs/protocol.md` is regenerated (`go test ./core/rpc -run TestProtocolReference -update`) if it lists the changed types.

## 10. Components

| Where | What |
|---|---|
| `sched/job.go` | `Held`, `NoticeCarried`, `Submit.After` and its bound in `Validate`, `Record.After` and `.Ready` |
| `sched/admit.go` | the starvation guard reads `Ready` |
| `sched/forecast.go` | releases in the simulation |
| `sched/signature.go` | `Signature.After` and its place in `ID` |
| `sched/beliefs_store.go`, `sched/belief_text.go`, `sched/beliefs_view.go` | the version by content; the `after` line |
| `sched/advisor/request.go`, `sched/advisor/prompt.md` | `Job.After` and the prompt's line |
| `core/queue_ops.go` | `submitJob`'s checks, the held state and its `Order`; `cancelJob`, `moveJobToFront`, `notifyJob` and `cancelOwnerJobs` for held jobs |
| `core/queue_watch.go` | release and the cascade in `settledJob` |
| `core/queue_notice.go` | carried notices, the prerequisite in the first line, the cascade's line and messages, the over-claim line |
| `core/queue.go` | the load checks |
| `core/queue_beliefs.go`, `core/queue_advisor.go` | the signature's prerequisite, `resizeQueued` for held jobs, `after` in the advisor's request |
| `core/queue_view.go` | `JobView.After` and `.Ready`; the reason for held jobs; the forecast's input |
| `internal/cli/job.go`, `job_follow.go`, `job_list.go` | `--after`, the held line, the follower's lines, `list` |
| `app/work.go`, `app/work_badge.go`, `ui/work.go` | held rows, the cancel prompt, `t`, the badge |
| `session/loom_context_shared.md` | §8 |
| Docs | `USAGE.md` (`--after`, `held`, the view); `sched/README.md` (the state, `After`, `Ready`, the signature's prerequisite, the beliefs version); `sched/CLAUDE.md` (the starvation clock and the version rule, with their tests); `docs/claude/the-work-queue.md` (release and the cascade inside the settle, the load checks' place); `docs/specs/protocol.md` if regenerated |

## 11. Failure handling

All fail closed: nothing is guessed, and each says why.

| Situation | Result |
|---|---|
| `--after` a job that doesn't exist or was dropped | Refused: `there is no job 14` |
| `--after` a job of another owner | Refused, naming the rule |
| `--after` a job that failed, was cancelled or was lost | Refused: a job after it would never run |
| A negative `--after` | Refused by `Validate` |
| A prerequisite that ends other than `done` | Its held dependents cancelled, down the chain, with the reason |
| A held job whose prerequisite is unknown at load | Cancelled: `because its prerequisite is unknown` |
| A held job with a cancel request at load | Cancelled with its reason |
| An older daemon | Never starts a held job; drops `after` and `ready` on save; the next newer load resolves what it left |
| An older loom and a `beliefs.json` with prerequisite parts | Version 2: left untouched, learning off there, saying the file is newer |
| A dependent that rebuilds on a small claim | Not stopped (claims are admission-only); its message says it peaked over twice its claim, and learning raises the next claim |

## 12. Testing

- **`sched`:**
  - `Admit`: the guard counts from `Ready`, and from `Submitted` when it is zero; a held record is never admitted and never the head.
  - `Forecast`: a held job is released at its prerequisite's simulated end and gets a start; chains of three.
  - `Signature`: a known signature's ID is unchanged without `After`, and differs with it.
  - The beliefs store: the version written follows the prerequisite parts, both versions load, and a version 3 is left untouched.
  - `TestGrant`: a held job doesn't stop a grant.
  - `Validate`: a negative `After`.
- **`core`**, through the production path (a booted model, real shims):
  - each refusal of §5.1, and a job submitted after a done one starting queued at its own `Order`;
  - release: the dependent is ahead of a job submitted between the two, starts in the settle's admission pass, and has `Ready` set;
  - the cascade for a failed, a cancelled and a lost prerequisite, down a chain of three;
  - cancelling a held job directly; a pause and a kill ending held jobs with no message; `t` on a prerequisite carrying its dependents; `t` on a held job;
  - each message of §5.5: carried, the prerequisite in the first line, the cascade's line, the dependents' messages when the failed job doesn't notify (and no second one down the branch), several dependents, the over-claim line;
  - sizing while held: `resizeQueued` and an advisor call (the fake `claude`) made while the job waits;
  - each load check of §5.7, with a store saved as an older daemon would (no `after`).
- **`internal/cli`**, against an in-process daemon: the held line; the follower's lines through held, queued and running; its exit 125 after a cascade; `list`.
- **`app`/`ui`:** held rows and their reason, the cancel prompt naming dependents, `t` on a held job, the badge.
- **e2e:** no new test; the existing suite runs (`go test -tags e2e ./e2e/...`).

## 13. Known limits and later work

- **A dependent can still rebuild.** The agent keeps editing while a chain waits, and a second command with other flags, features or profile may recompile; claims are admission-only, so the rebuild runs on the dependent's claim. The over-claim line and learning are the answer here; snapshotting the worktree for a chain is out of scope.
- **A fast prerequisite gives up its place:** a dependent submitted after its prerequisite is done joins the back of the queue.
- **One prerequisite:** a job that needs two builds chains them. Fan-in (`--after 14,15`) can come when a case needs it.
- **Only after success:** a step that should run after a failure (cleanup, a coverage report) can't be expressed.
- **Same owner only:** a session can't wait on another's job.
- **No output passing:** the shared worktree is the only channel; a prerequisite's log or artifact paths aren't handed to its dependents.
- **Gate scripts stay one job:** phases inside a running job (a claim that drops as a script's phases pass) are later work.
