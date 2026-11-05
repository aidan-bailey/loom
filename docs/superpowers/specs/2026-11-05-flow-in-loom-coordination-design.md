# Flow in loom, part 2: coordination

Status: design approved section by section 2026-11-05; awaiting review of this file.

The second of three sub-projects that move the agent flow into loom (part 1, distribution: `docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md`, in main 0c828a92; its cutover is not done yet). This part gives flow loom's work queue directly: steps with visible job IDs, gate chains that keep their place in line, and waits that take no queue slot, in place of `agent-wait.sh`. Part 3, orchestration, moves the supervisor's bookkeeping into the daemon.

## 1. Problem

Flow's agents run every step that can take more than a few minutes through `loom flow wait`, which part 1 kept as the bash script `flow/lib/agent-wait.sh`: `start <name> -- <cmd>` detaches the command with `setsid`, wrapped in `loom job run` inside a loom session (flow d937675), and `wait <name>` blocks for at most 170 seconds inside the agent's turn (Claude Code moves a background subagent's foreground Bash call to the background at 180 seconds, and an agent that ends its turn to wait counts as not running). That costs three things:

1. **Hidden job IDs.** The agent never sees the job behind its step, so it can't use `--after`, `loom job cancel` or `loom job logs`, and each step keeps a `loom job run` follower process alive for its whole life.
2. **No chains.** The verifier runs a story's gates one at a time, queueing each only when the one before it ends: every step joins the back of the queue, and "stop at the first FAIL" is prompt discipline for a Haiku agent, as are exit codes read through pipes and vacuous output matched by eye.
3. **Polls as jobs.** Agents put polling loops through `start`, and the queue admitted each as a job with a real claim (the minimum, 1 core and 1 GB): on 2026-11-05 the scheduler's beliefs held six in prader-rs, such as `bash -c until grep -q "^clippy" /tmp/claude-1000/s13-2.out; do sleep 10; done` and `bash -c until [ -s …/tasks/b099fphsg.output ]; do sleep 10; done`. They waited on step logs and on a Claude background task's output.

## 2. Decisions

- **Scope: visible job IDs, story-gate chains and waits with no slot** (owner, 2026-11-05). Rejected: a scheduler prerequisite that releases its dependent however the prerequisite ends (the dependent-jobs spec's later work), since final gates are handled without it (below).
- **Story gates chain in order** (each `--after` the one before; a FAIL cancels the rest). Rejected: all gates at once with the wait cancelling the others on the first nonzero exit (the owner's proposal), since cargo gates in one worktree serialize on cargo's build-directory lock while each holds its queue claim, and the profiles already list their cheap checks first, so a chain stops before the heavy builds anyway; a profile marking gates that may run beside the chain (more to build and explain).
- **Final gates chain in order, and the wait resubmits the rest after a FAIL**, so every final gate runs, one at a time, in order, and the chain keeps its place unless something fails. Rejected: all final gates at once with no cancel (idle claims on cargo's lock); a run-after-any-end prerequisite in `sched` and `core`.
- **Agents wait only on jobs, and `start` refuses polls.** Rejected: a bounded wait on a file or a log line (it makes side channels easy to wait on); claim-free wait jobs in the scheduler (polls would stay as written).
- **The gate logic lives in a Go gate runner, `loom flow gates`** (approach A). It submits the chain, stops or resubmits after a FAIL and judges each verdict, so the verifier starts, waits and reports. Rejected: a generic chain inside `loom flow wait` that the verifier builds and judges itself (B, leaving the error-prone parts in the prompt); a chain object in the daemon (C, part 3's orchestration pulled forward).
- **`loom flow wait` keeps its name and its log paths, ported to Go**, as part 1 decided for every tool a later part ports: agents' call sites don't change.

## 3. Scope

In: `loom flow gates start|wait|cancel`; `loom flow wait start|wait` in Go; the poll refusal; the prompt changes in `flow:plan-execution-supervisor`, `flow:verifier`, `flow:habits` and `dispatch.md`; retiring `agent-wait.sh`; the tests; docs.

Out: any change to `sched` or `core` (the chain uses today's `--after`); the supervisor's own bookkeeping (part 3); the cutover of part 1, which is the owner's (part 1 spec §5.8).

## 4. Concepts and names

- **A gate run:** one `loom flow gates start` — a story's gates (`--story <commit>…`) or the final gates (`--final`) — and the jobs it submitted. Its name is `story-<first commit, short>` or `final`.
- **A step:** one line of the expansion `loom flow conf gates` prints, in order: `RUN <command>`, `SKIPPED <gate> (<why>)` or `FAIL <gate> (<why>)`. A `RUN` step becomes a job.
- **A generation:** the chain of jobs a run submitted at once. A final run that resubmits after a FAIL has more than one.
- **The run record:** `<state>/gates/<run>.json`, where `<state>` is what `loom flow conf state` prints (the profile's `paths.state`, else `<git-dir>/flow`, one per worktree); the step logs are `<state>/gates/<run>/<i>.log`.
- **A wait step:** one `loom flow wait start <name>`, recorded as `<state>/agent-wait/<name>.job` (its job ID) with its output copied to `<state>/agent-wait/<name>.log`.

## 5. Behaviour

### 5.1 `loom flow gates start (--story <commit>… | --final)`

- Runs the expansion (`conf gates` with the same arguments) and reads its lines. An expansion that fails to run exits 125 with its stderr.
- Submits each `RUN` line as a job through the daemon, in order: argv `bash`, `-c`, `<command>`; each job `--after` the one before it; no message when it ends (the verifier is the one waiting); the description `gate <i>/<n>: <command>`, cut to 60 characters; the submitter's environment, as `loom job submit` sends it; the claim the scheduler sizes.
- A story run chains only the `RUN` lines before the expansion's first `FAIL` line; the lines after it are SKIPPED (not run: gate *k* failed). A final run chains every `RUN` line, and the expansion's `FAIL` lines keep their place.
- Writes the run record (§5.4) and prints `started <run>: <n> steps, jobs <first>–<last>`, followed by the expansion's `SKIPPED` and `FAIL` lines.
- Refuses (exit 2) while any run in the worktree is still active, naming it: one run at a time per worktree, as the verifier's rules always had it.
- Starts nothing else: the verifier starts the profile's services itself, before this.

### 5.2 `loom flow gates wait [<run>] [<seconds>]`

- `<run>` defaults to the worktree's latest run; `<seconds>` to 170, and at most 170.
- Waits until the run has ended or the time is up, then prints the whole table, one line per step, in order: `<i> PASS|FAIL|FAIL (killed)|VACUOUS|SKIPPED <command>` with the reason or the log path beside a FAIL or VACUOUS, and for a step not yet ended `queued (3rd)`, `held after gate <k>` or `running <duration>`.
- Copies each ended step's `output.log` to `<state>/gates/<run>/<i>.log` as it ends, so the paths it names outlive the daemon's pruning of finished jobs.
- Judges each ended step from its job, never from an agent's reading:

| The job | Verdict |
|---|---|
| exit 0; `conf vacuous` on its log matches nothing (exit 1) | PASS |
| exit 0; a vacuous pattern matches (exit 0) | VACUOUS, with the matching line |
| exit 0; the vacuous check errs (exit 2) or the log can't be copied | FAIL (infra), with the reason |
| exit 1–128 | FAIL |
| killed by a signal, or exit above 128 | FAIL (killed) |
| cancelled, and the step before it in the generation ended other than done | SKIPPED (not run: gate *k* failed) |
| cancelled otherwise, lost, or could not start | FAIL (infra), with the queue's reason |
| unknown to the queue while the record says it hasn't ended | FAIL (infra): the queue no longer knows job *N* |

- In a final run, when step *i*'s job ends other than done (a FAIL, a FAIL (killed), or a job lost or never started), the daemon cancels *i+1…n*; the wait then submits those steps as a new generation, the first with no `--after`, and records it. It joins the back of the queue.
- Exits 0 when the run has ended with every step PASS or SKIPPED by the expansion, 1 when it has ended with any FAIL, FAIL (killed), FAIL (infra) or VACUOUS, 75 when it hasn't ended (call it again), 125 when it can't follow the run (§6), 2 on a usage error.
- A verifier made to hand back leaves the run going; its successor resumes it with `gates wait <run>`, resubmission included, since the record holds everything.

### 5.3 `loom flow gates cancel [<run>]`

Cancels the run's jobs that haven't ended and marks the record cancelled, for a supervisor abandoning a story or a verifier that finds a run wedged. Exit 0 when nothing is left running.

### 5.4 The run record

JSON, with a `schema` field: the kind, the commits, the expansion's lines, and per step its job ID in each generation, its verdict once judged and its log copy. Written whole to a temp file and renamed. A lock file beside it (`<run>.lock`, flock) serializes `start`, `wait` and `cancel` on one run, so two waits can't both resubmit.

### 5.5 `loom flow wait start <name> -- <command> [args…]`

- Validates `<name>` as today (`[A-Za-z0-9._-]+`) and refuses one whose job hasn't ended.
- Submits the command as one job, with no message when it ends and the description `<name>: <command>` cut to 60 characters, and writes `<name>.job`.
- Prints `started <name>: job <N>; log <state>/agent-wait/<name>.log`.
- Refuses, with exit 2 and the way to wait instead:
  - **a poll:** argv `sleep …`, or a `sh`, `bash` or `zsh` `-c` script holding an `until` or `while` loop whose body runs `sleep`: *a poll isn't a step, and it would hold a queue claim doing nothing; wait on what it watches with `loom flow wait wait <name>` or `--job <N>`; a step's output is already in its log.* A script that sleeps outside a loop, or loops without sleeping, is a step.
  - **a `loom …` command:** *already a job: follow it with `loom flow wait wait --job <N>`.*

### 5.6 `loom flow wait wait (<name> | --job <N>) [<seconds>]`

- `<seconds>` defaults to 170, at most 170. `--job <N>` follows any job the session can see, such as one an agent submitted with `loom job submit`; its log copy is `<state>/agent-wait/job-<N>.log`.
- Copies what the job's `output.log` gained since the last call to the step's log, then prints, as today: `done <name> exit <status>` and exits with the job's exit code (128 + N for a job killed by signal N); `running <name>: job <N> queued (3rd)|held after job <M>|running <duration>` and exits 75; `lost <name>: <the queue's reason>` for a job lost, cancelled or never started, and exits 70.
- Exits 125 when it can't follow the job (§6).

### 5.7 Prompt changes

Prompt text only: no agent's model, effort, maxTurns, tools or cache lifetime changes (`flow/CLAUDE.md`'s pin rule).

- `flow:plan-execution-supervisor`: steps 4 (per story) and final 1 stop running `conf gates`; the verifier's brief carries the kind, the story's commits and the `[verifier]` block.
- `flow:verifier`: start the services; `loom flow gates start --story <commits>` or `--final`; `loom flow gates wait` as a foreground call with a 600000 ms timeout, repeated until it exits other than 75; report the table, quoting each FAIL's output (trimmed) from the log it names. The rules on order, exit codes, vacuous matching and waiting go; "check-only" and "a FAIL (killed) is never rerun silently" stay.
- `flow:habits`: the wait shows the job ID, takes `--job <N>`, and refuses polls.
- `flow/plugin/docs/dispatch.md`: the verifier section and "An agent waits inside its turn" describe `gates` and the Go wait.

### 5.8 What retires

`flow/lib/agent-wait.sh`, `flow/lib/agent_wait_test.py`, and `doctor.py`'s `tail --pid` check, which only agent-wait needed. `conf gates` and `conf vacuous` stay: `gates start` and `gates wait` call them.

## 6. Failure handling

Fail closed: when the daemon, git or a file can't answer, change nothing and say so.

- **A chain that can't be submitted whole** (no daemon, a refused submit partway): `gates start` cancels the jobs it already submitted, writes no record and exits 125 with the reason. No half chain is left behind.
- **The daemon lost mid-wait:** exit 125, saying the jobs go on and to call `wait` again; jobs survive a daemon restart.
- **A record that can't be read or decoded:** exit 125; the file is kept as it is, never rewritten or guessed past.
- **A resubmission that fails:** exit 125; the record keeps the state, and the next `wait` tries again.
- **A job the queue no longer knows, or a log that can't be copied:** FAIL (infra) for that step, never PASS, since the vacuous check needs the log.
- **Windows:** refused with `sched.ErrUnsupported`, as every `loom job` command is.

## 7. Components

- `internal/cli/flow_gates.go` (new): `gates start|wait|cancel`, the record, the verdicts, the resubmission.
- `internal/cli/flow_wait.go` (new): the Go `wait start|wait` and the poll refusal; `flow.go`'s tool table maps `wait` to it in place of `agent-wait.sh`.
- Both reuse the `loom job` client: `submitJob`, the replica's job views (`core.JobView`: state, `Exit`, `CancelReason`, `After`), `loom job cancel`'s request, and the follower's log copying (`copyLog`), reading the view before the log.
- `flow/plugin/agents/plan-execution-supervisor.md`, `verifier.md`, `skills/habits/SKILL.md`, `docs/dispatch.md`: §5.7.
- `flow/lib/`: §5.8; `check-agents.sh` fails a prompt that still names `agent-wait`.
- Docs: `flow/README.md`, `USAGE.md` (`loom flow gates`), `internal/cli/CLAUDE.md` (the gates' fail-closed rules and their tests), `session/loom_context_flow.md` if it names the wait.

## 8. Testing

Go tests in `internal/cli` against the hermetic test daemon (`serveDaemon`, `sched.TestRunner`), each guard pinned from both sides:

- `gates start`: the chain's `--after` links in order; a story run stops at the expansion's first FAIL; the one-run-per-worktree refusal and the case it spares (a run that has ended); the rollback when a submit fails partway.
- `gates wait`: every verdict row of §5.2, FAIL (infra) for an erring vacuous check included; a story FAIL turning the rest SKIPPED; a final FAIL resubmitted as a new generation; 75 with the step's standing; a second process resuming from the record; two concurrent waits resubmitting once; the exit codes.
- `gates cancel`: the run's open jobs cancelled, the record marked.
- `wait start|wait`: the printed job ID; the log copy growing across calls; `--job <N>` on a job submitted another way; every exit code.
- The poll refusal: refused `sleep 5`, `bash -c 'until grep -q x f; do sleep 10; done'`, `sh -c 'while :; do sleep 1; done'`; accepted `cargo test sleep_tests`, `bash -c 'sleep 2 && make'`, `bash -c 'until make; do :; done'`; each term of the rule mutation-checked on its own.
- `TestLoomContext_NamesRealSessionFlags` knows `loom flow gates`; `flow/lib/test.sh` drops `agent_wait_test` and runs the new `check-agents.sh` check.

A live supervised run comes after part 1's cutover: agents see part 2 only once a build carrying both ships.

## 9. Known limits and later work

- A resubmitted final generation joins the back of the queue; a run-after-any-end prerequisite in the scheduler would keep its place.
- The poll refusal covers `loom flow wait start`; a poll submitted with `loom job submit` directly is still queued.
- The gates of a run share the worktree with nothing else by convention only: the supervisor keeps developers and the verifier apart, as before.
- Part 3: the supervisor's bookkeeping in the daemon; later, plan progress in the TUI and on the phone.
