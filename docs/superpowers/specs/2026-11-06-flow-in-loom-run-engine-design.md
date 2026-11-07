# Flow in loom, part 3b: the run engine

Status: design approved section by section 2026-11-06; awaiting review of this file.

Part 3 of moving the agent flow into loom is orchestration, in three parts: 3a, the daemon runs a flow agent headless as a job (`docs/superpowers/specs/2026-11-06-flow-in-loom-agent-runs-design.md`, in main at bc8c7d59); **3b**, a run engine that drives a plan's stories through such agents and writes the run's record, asking the manager every question (this spec); 3c, an Opus judge that answers what the spec and plan settle, and a reworked `/flow:supervise`. Each part works without the next.

## 1. Problem

A supervised run today is an Opus `flow:plan-execution-supervisor` subagent dispatching every developer, verifier and reviewer as its own subagents (3a's spec, §1, has the retro's figures: supervisors were 23% of the spend over 11 runs, about four per run, handing over every third story and forwarding late reports by hand). Most of what the supervisor does is mechanical: route a story by its tier, write a brief from a template, run the gates, pick the reviewer, route the findings by severity, keep the state file, and let the manager copy its rows into the record. A few steps need judgement: answering a NEEDS_CONTEXT, deciding what an intermediate-dev's BLOCKED left open, a design-level finding, a premise's DESIGN verdict.

3b moves the mechanical part into a deterministic Go engine on 3a's agent jobs and part 2's gate runner, and sends every judgement to the manager. 3c then puts an Opus judge in front of the manager.

## 2. Decisions

- **The manager starts and answers a run through a new owner-typed skill, `/flow:run`** (owner, 2026-11-06). `/flow:supervise` keeps the subagent supervisor until 3c merges the two. Rejected: reworking `/flow:supervise` in 3b (no fallback while the engine is new); a CLI with no skill until 3c.
- **Stories run in plan order, one at a time** (owner, 2026-11-06). Parallel-safe pairs are later work. Rejected: running pairs in 3b (a more complex engine for what a few runs used).
- **The engine is a job of a new `run` kind** (owner, 2026-11-06, approach A). It runs on its own until it needs the manager, then saves the question and exits; an answer queues it again. It survives the manager's session closing or pausing and the daemon restarting. The daemon learns a job kind and nothing about flow. Rejected: B, the engine in the manager's background shell (the run advances only while that session runs); C, the engine in the daemon's `core` (new `Core` methods, wire views, generated code and a protocol bump before the engine is proven; the daemon can later read the run's state file for visibility).
- **The engine runs the gates itself**, starting the profile's services first as the verifier did, through part 2's gate runner, and no `flow:verifier` is dispatched: the verdicts are already the tool's (each job's own exit, the profile's vacuous check), and the verifier only relayed them.
- **Reviews report their findings as lines the engine parses**, as 3a's reports end with `STATUS:`: the headless preamble asks a reviewer for `FINDING:`, `PLAN:` and `VERDICT:` lines (§5.6). Agent definitions don't change.
- **Every judgement goes to the manager in 3b**, as a typed question (§5.4). No failure is retried automatically.

## 3. Scope

In: the `run` job kind in `sched` and `core`; the engine (`internal/flowrun`) and its state file; `loom flow run start|engine|wait|answer|show|list|cancel|resume`; the `/flow:run` skill; the reviewers' report lines in the headless preamble; the turn-cap reading in 3a's report reader (and `--max-turns` if the probe needs it); `loom flow costs run`; the record written by the engine; the work view's `run` rows; a probe; tests; docs.

Out: the judge and the reworked `/flow:supervise` (3c); parallel-safe pairs; automatic retries; runs in the TUI and on the phone beyond the job's row; Windows, where `loom job` refuses.

## 4. Concepts and names

- **A run:** one plan executed by the engine in one worktree, from `loom flow run start` to its end. Its ID is the plan's file name without `.md`, with `-2`, `-3`, … appended when a run of that plan already exists in the worktree.
- **The run state:** `<state>/runs/<run>.json` (schema 1), written only under `<state>/runs/runs.lock`. `<state>` is what `loom flow conf state` prints.
- **An engine job:** a job whose `Kind` is `run`, running `loom flow run engine <run>` until the run parks or ends.
- **Parked:** the run waits for the manager's answer to its open question; no engine job runs.
- **A step:** one agent run, gate run, conformance or premises call the engine makes, recorded in the state with its intent before it starts and its job or result after.
- **The agents' names:** `<run>-premise<k>`, `<run>-s<N>-dev<k>`, `<run>-s<N>-fix<k>`, `<run>-s<N>-review<k>`, `<run>-final-review<k>`, `<run>-final-fix<k>`, where `<k>` counts from 1 within its story, or within the run for the premise and final agents. They are 3a's agent-run IDs, so `<state>/agents/<name>.json` holds each one's record.

## 5. Behaviour

### 5.1 `loom flow run start <plan>`

- Refuses outside a session (no `LOOM_FLOW_ROOT`) with exit 125. Refuses with exit 2 on a dirty tree, when `loom flow conf doctor` reports a FAIL or the plan fails `loom flow check-plan` (printing their FAIL lines), and while another run in this worktree hasn't ended.
- Writes the run state:
  - the plan's and the spec's paths (the spec from check-plan's OK line), `BASE_SHA` (`HEAD`), the start time (UTC), the roster line (`loom flow conf roster`), the profile line (`loom flow conf show --for supervisor`'s `Profile:`), and the plan's `**Design stage:**` text when it has one;
  - the manager's session ID, from the starting environment's `CLAUDE_CODE_SESSION_ID`;
  - one row per story from check-plan's output: ID, Developer, tier, review column (`vouched` or `full`), landmines, plan line range, Verify line;
  - the `[developer]`, `[verifier]`, `[reviewer]` and `[supervisor]` blocks of `loom flow conf show --for supervisor`, as they stand.
- Queues the engine job and prints `started <run>, job <N>`.

### 5.2 The `run` job kind

- `sched.Submit` and `Record` take `Kind: "run"`. `Validate` refuses a run job with a claim, `Exclusive`, `IO` or `After`, as it does an agent job.
- `Admit` starts a run job at once: it adds nothing to `used`, never waits for the caps, the advisor or the agent limit, and is never the head or a starving job. Build and agent admission are unchanged.
- It is never sized. The shim, adoption, `output.log`, cancel, retention and the sampler are unchanged.
- `core.JobView.Kind` shows it; `loom job list` and the work view show `run` with no claim.
- Only `loom flow run` submits one. At most one runs per worktree: `start`, `answer` and `resume` refuse while the run's engine job hasn't ended.

### 5.3 The engine

The engine (`loom flow run engine <run>`, hidden) takes `runs.lock`, loads the state, and works one step at a time until the run parks or ends. Before each step it writes the step's intent; after submitting, the job ID; after the step ends, its result. A restarted engine finds the last step: one with a job waits on that job; one with only an intent looks the agent run up by its name (3a refuses a name twice) or the gate run by its record, and submits only when neither exists. So no step is submitted twice.

Before each agent dispatch the engine checks that `HEAD` is the head it last recorded and the tree is clean; otherwise the run parks as `tree-moved`. An answer takes the manager's own commits with it: when the tree is clean, `HEAD` descends from the head the engine recorded at the question, and every commit since touches only the run's spec and plan, `loom flow run answer` records that `HEAD` and the engine takes it as its head (amended after the first real runs, 2026-11-06, which parked four times on the manager's own spec commits). Any agent's NEEDS_CONTEXT parks as `needs-context`, and its answer resumes that agent. A BLOCKED from an agent other than a developer parks as `agent-failed`.

**Services and gates.** Every gate run is preceded by the profile's services: each command in the `[verifier]` block, once, as written, run by the engine in the worktree with `sh -c`, one after another. A service that exits non-zero is a FAIL (infra), and the gates are not started. The gates then run through part 2's gate runner, whose verdicts (each job's own exit, the profile's vacuous check) the engine takes as they are.

**Before the first story.** `loom flow premises <plan> --rev BASE_SHA`. Flagged lines go to `flow:premise-check`, one agent per three stories with flags. Its report lines (`plan:LINE | VERDICT | … | the fix`) are parsed: a COSMETIC verdict becomes a correction line in the briefs of the stories whose ranges hold the line; DESIGN and UNSURE park the run as `premise-design` before any story starts.

**Each story**, in plan order:

1. **The developer.** The engine records the story base (`HEAD`) and dispatches `flow:<Developer>` with the developer brief (§5.5). An intermediate-dev on a story with landmines runs at `xhigh`.
2. **The developer's report:**
   - NEEDS_CONTEXT parks the run as `needs-context`; the answer resumes the same agent (`loom flow agent answer`).
   - DONE or DONE_WITH_CONCERNS with no new commits on the story's range parks as `agent-failed` ("reported done with no commits"). A fixer's is noted instead: it can find its findings fixed already, and the gates and re-review after every fix round judge that (amended after the first real run, 2026-11-06).
   - A junior-dev's BLOCKED or turn cap: a fresh intermediate-dev with the same brief and the junior's report.
   - An intermediate-dev's turn cap: resumed once with "Continue from where you stopped."; a second turn cap parks as `agent-failed`.
   - An intermediate-dev's BLOCKED parks as `blocked-decision`; the decision goes to a fresh intermediate-dev in its brief. When that one is BLOCKED too, a `flow:senior-dev` gets both reports, without a question. A senior-dev's BLOCKED parks as `blocked-decision`, and its answer goes to a fresh senior-dev.
   - DONE or DONE_WITH_CONCERNS: the engine records the story head, the commits (`git log` over the range) and the report's path.
3. **The gates.** The story's gate chain (part 2's `start --story` over the story's commits). On FAIL, FAIL (killed) or VACUOUS, a fresh intermediate-dev gets the fix-gates brief, then the gates run again. A second failure, or a FAIL (infra), parks as `gate-failure`.
4. **The review.** `loom flow conformance <plan> --story <id> --base <story base> --head <story head>`, written to a file under `<state>/runs/<run>/`. The reviewer: `flow:story-reviewer` for `vouched`; `flow:code-quality-reviewer` for `full`, and for a vouched story whose developer reported DONE_WITH_CONCERNS or whose uncovered lines hold one of the profile's landmine markers. The review brief (§5.5).
5. **The findings** (§5.6):
   - All Minor: kept in the state for the final round.
   - Any Critical or Important: when one is tagged `design`, the run parks as `design-finding` first, and the decision goes into the fix brief. A fresh intermediate-dev at its pinned high gets every finding, Minors included; then the story's gates run again, then `flow:story-reviewer` re-reviews the fix's range against the findings.
   - Findings still Critical or Important after the re-review, and any `PLAN:` line from either review, park as `unresolved-findings`.
6. The state's row is brought up to date.

**After the last story:**

1. The final gate chain (`start --final`; every final gate runs). A FAIL, FAIL (killed) or VACUOUS goes to a fresh intermediate-dev with the failing steps, then the final gates run again; a second failure, or a FAIL (infra), parks as `gate-failure`.
2. One `flow:code-quality-reviewer` over `BASE_SHA..HEAD` with the anti-seam brief.
3. Its findings and the kept Minors, grouped by file (a finding joins the group that shares a file with it), go to one fresh intermediate-dev per group, one group after another. Then the final gates again, then one `flow:story-reviewer` re-review of the round's range against all its findings. Findings still Critical or Important park as `unresolved-findings`. This round runs for Minors alone too.
4. The run ends `done`, and the engine writes the record (§5.7).

### 5.4 Questions and answers

A parked run holds one open question: an ID, a kind, the story, the text, and the paths to read (the asking agent's report, the review, the gate log). The kinds, and the answers each takes:

| Kind | Raised when | Answer |
|---|---|---|
| `needs-context` | an agent reports NEEDS_CONTEXT | text: resumes that agent |
| `blocked-decision` | an intermediate-dev with no decision in its brief, or a senior-dev, reports BLOCKED | text: the decision, to a fresh agent of that tier; `stop` |
| `design-finding` | a Critical or Important finding is tagged `design` | text: the decision, into the fix brief; `accept`; `stop` |
| `premise-design` | a premise verdict is DESIGN or UNSURE | text: a correction for the affected stories' briefs; `stop` |
| `unresolved-findings` | findings still Critical or Important after a fix round, or a `PLAN:` line | text: the decision, to a fresh fix round and re-review; `accept`; `stop` |
| `gate-failure` | a gate fails twice, or FAIL (infra) | text: the decision, to a fresh fix; `retry` (the gates again as they stand); `accept`; `stop` |
| `agent-failed` | an agent run fails (§6), or a second turn cap | `retry` (a fresh agent, same brief); `stop` |
| `tree-moved` | `HEAD` or the tree differs from what the engine recorded | `continue` (record the current `HEAD`; refused while the tree is dirty); `stop` |

- An answer that is exactly one of the words is that word; `answer` refuses (exit 2) a word, or text, the kind doesn't take.
- `accept` moves on and records what was left open. `stop` ends the run `stopped`.
- `answer` records the answer and the answering session's ID (`CLAUDE_CODE_SESSION_ID`, added to the run's manager sessions when new) and queues the engine.

**`/flow:run <plan>`** (`flow/plugin/skills/run/SKILL.md`, owner-typed, `disable-model-invocation: true`), from a fresh session:

1. `loom flow run start <plan>`; on a refusal, show its lines and stop. Read the spec it names: it is the manager's only record of the design.
2. `loom flow run wait <run>` with Bash's `run_in_background`, and end the turn.
3. When `wait` exits parked: read the question and its paths, answer from the spec when it settles it; otherwise ask the owner, write the answer into the spec and commit it by pathspec. Then `loom flow run answer <run>` with the answer on stdin, and `wait` again in the background.
4. When `wait` exits ended: commit the record's in-repo copy by pathspec when the profile's `Record copy in the repo:` names a directory, and tell the owner what was built, what failed and what needs them.

### 5.5 The briefs

Fixed templates in `internal/flowrun`, each written to `<state>/runs/<run>/briefs/<agent name>.md` and passed to `loom flow agent run` on stdin (3a's 48 KiB limit; long inputs go in as paths):

- **Developer:** the plan's path and the story's line range (never the tasks retyped), the spec's path, the premise corrections, the `[developer]` block (its landmine notes on a deep story only), "commit once per task" and "run the story's **Verify** command before you report DONE". No `Co-Authored-By` or `Claude-Session` lines.
- **Escalated developer** (intermediate after a junior, senior after two intermediates): the developer brief plus the earlier reports' paths and, when there is one, the manager's decision.
- **Fix-gates:** the failing steps (command, verdict, log path), the story's plan line range and commit range, the `[developer]` block.
- **Fix-findings:** each finding with its `file:line` and fix, any decision, the story's plan line range and commit range, the `[developer]` block.
- **Review:** the conformance output's path, the story's range, the plan's line range, the developer's report path (with its deviation list), the story's **Interfaces** line and those of the earlier stories it uses, the `[reviewer]` block.
- **Re-review:** the fix's range and the findings it was given.
- **Anti-seam:** `BASE_SHA..HEAD`, the `[reviewer]` block, and the brief: contracts between stories, values one story produces that no other reads, units or limits that disagree across a boundary.
- **Premise-check:** the flagged lines for its stories and `BASE_SHA`.

### 5.6 The reviewers' report lines

3a's headless preamble gains, for `flow:story-reviewer` and `flow:code-quality-reviewer` runs, an instruction to end the report, before its `STATUS:` line, with:

```
FINDING: <Critical|Important|Minor> [design] <file>:<line> <the fix>
PLAN: <a problem in the plan's own code>
VERDICT: <APPROVED|CHANGES_REQUESTED>
```

one `FINDING:` per issue and one `PLAN:` per plan problem (none of either when there are none), with `design` when the fix needs a decision the spec doesn't make. The engine reads only these lines (markdown `*` and backticks trimmed, as for `STATUS:`). A review with no `VERDICT:` line, or a `FINDING:` line it can't parse, is an `agent-failed`. Routing goes by the `FINDING:` lines; the verdict is recorded.

### 5.7 The record and its cost

When the run ends (`done`, `stopped` or `cancelled`), the engine writes `~/.loom/flow/records/<key>/<YYYY-MM-DD>-<plan stem without its leading date>.md` (`<key>` as `loom flow conf key` prints it), in `flow/plugin/docs/records.md`'s format, and the same file in the profile's in-repo record directory when one is named (the manager commits that copy):

- **Header:** Plan (with its story count), Spec, Commits (`BASE_SHA..HEAD` with the count), Manager (each session's model and ID), Start and End, Definitions (the start's roster line, with ` → <id>` when `loom flow conf roster` differs at the end), Profile, Design stage (when the plan has one), and `- Ended: done|stopped|cancelled`.
- **Cost:** `loom flow costs run <run>` (below), pasted whole.
- **Stories:** the table, from the state's rows.
- **Notes:** every question and its answer, every deviation the developers listed, every escalation the engine made on its own (junior to intermediate, to senior), and each story's and the final gates' results.

**`loom flow costs run <run>`** (`agent_costs.py run`) prices each agent run's own transcript by role, found from its session ID and config dir in `<state>/agents/<name>.json`, and each manager session over the run's window, and prints the tables and the `<!-- agent-costs … -->` comment `session` prints, so `costs records` and `/flow:retro` read the record unchanged. It reports each manager session's model, which the header uses.

## 6. Failure handling

Fail closed: when the daemon, Claude, git or a file can't answer, change nothing and say so.

- **An agent run that fails** (3a's `failed`: no usable report, killed, lost, `is_error`, a usage limit): the run parks as `agent-failed`, with 3a's reason and, for a usage limit, the reset time when Claude's result names one. A turn cap is not a failure: 3a's reader records the result's `subtype`, an `error_max_turns` result ends the agent run `TURN_CAP`, and 3a's `answer` resumes a run at `TURN_CAP` as it does one at NEEDS_CONTEXT or BLOCKED (§5.3).
- **The engine job lost** (killed, the host rebooted, the job lost): `wait` says so with `resume` as the way on, and exits 1. `resume` queues the engine, which carries on from the last step (§5.3).
- **The daemon restarting** while the engine runs: the shim adopts it. The daemon lost during `wait`: exit 125, and the engine goes on.
- **A run state that can't be read**, or of another schema: refused (125) and left as it is.
- **A refused submit** (an agent run or a gate chain): the run parks as `agent-failed` or `gate-failure` with the refusal.
- **`cancel`:** cancels the engine job, its agent run and its gate run, and ends the run `cancelled`; the record is written.
- **`answer` while the engine runs, or on a run not parked:** refused (exit 2).
- **Windows:** refused, as every `loom job` command is.

### `loom flow run` commands

- `start <plan>`: §5.1.
- `wait [<run>]`: the worktree's latest run by default; no time cap (it is meant for a background Bash). Returns when the run parks (printing the question, its kind, its paths and the answers it takes) or ends (the outcome, the stories done and the record's path). Exits 0 parked or ended `done` or `stopped`; 1 ended `cancelled`, or the engine lost; 125 when it can't follow.
- `answer <run> < text`: §5.4.
- `show [<run>]`: the phase, the story rows, the open question and the step in flight.
- `list`: this worktree's runs, one line each: ID, phase, stories done of all, open question kind.
- `cancel [<run>]` and `resume [<run>]`: above.

## 7. Components

- **`internal/flowrun` (new):** the run state and its file, the step logic, the brief templates, the parser for the reviewers' and premise-check's lines, and the answer check. Every side effect goes through one interface (submit and answer an agent run and wait on it, start and wait on a gate run, read `HEAD` and the tree, conformance, premises), so it is tested without a daemon.
- **`internal/cli`:**
  - `flow_run.go` (new): the commands, and the adapter from the interface to 3a's agent code and part 2's gates code;
  - `flow_agent*.go`: the reviewers' preamble lines, the result's `subtype` and the `TURN_CAP` status (which `answer` resumes), and `--max-turns` if the probe needs it.
- **`sched`:** `Kind: "run"`, `Validate`'s rule and `Admit`'s branch.
- **`core`:** no sizing for a run job; `JobView.Kind` already carries it.
- **`ui`:** the work view's `run` rows.
- **`flow/`:** `plugin/skills/run/SKILL.md`; `lib/agent_costs.py`'s `run`; `plugin/docs/records.md`'s `Ended:` line; a section on the engine in `plugin/docs/dispatch.md`; `README.md`.
- **Docs:** rules in `sched/CLAUDE.md` and `internal/cli/CLAUDE.md` (and a package `CLAUDE.md` for `internal/flowrun`); `USAGE.md`; `docs/claude/the-work-queue.md`.

## 8. Testing

Each guard is pinned from both sides, and each is mutation-checked.

- **`internal/flowrun`**, table tests over a fake interface:
  - every branch of §5.3 and §6: each report status by tier, a DONE with no commits, a non-developer's BLOCKED, the escalations, the turn caps, a failing service, gate failures (first, second, infra), the reviewer choice (vouched, full, DONE_WITH_CONCERNS, a landmine marker in uncovered lines), findings (all Minor, Critical, `design`, still unresolved, `PLAN:`), the final round's grouping, the premise verdicts, `tree-moved`;
  - a restart at every saved point submits nothing twice;
  - each brief template against a golden file;
  - the line parsers, and every answer each kind refuses and takes.
- **`sched`:** `Validate` for the run kind; `Admit` tables (a full host and a full agent limit still start a run job; builds and agents unchanged); the store round-trip.
- **`internal/cli`**, end to end on the hermetic test daemon, with a fake `claude` scripted per role from its brief and a test profile with fake gates: a two-story plan that parks on a NEEDS_CONTEXT, is answered, passes its gates, gets a Critical finding, its fix and re-review, runs the final round and writes its record; `cancel`; the engine job killed and `resume`d, with no agent run duplicated; every refusal (outside a session, a dirty tree, a run not ended, `answer` on a running run, a word the kind doesn't take).
- **`flow/lib/test.sh`:** `costs run`, and the agent lint over the new skill.
- **A probe** (opt-in real Claude, beside 3a's): whether `--agent` applies the definition's `maxTurns` and reports `error_max_turns` at the cap (if not, the engine passes `--max-turns` from the definition), and whether a reviewer under the preamble ends with parseable `FINDING:` and `VERDICT:` lines.

## 9. Known limits and later work

- Stories run one at a time; parallel-safe pairs are later work.
- Every judgement waits for the manager; 3c's judge answers what the spec and plan settle.
- Nothing is retried automatically, a usage limit included.
- A headless agent can't be steered mid-turn (3a).
- Runs in the TUI and on the phone, from the run state, are later work.
