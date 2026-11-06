# Flow in loom, part 3a: headless agent runs

Status: design approved section by section 2026-11-06; awaiting review of this file.

Part 3 of moving the agent flow into loom (part 1, distribution, and part 2, coordination, are in main at 2f104c3c) is orchestration, and the owner split it in three: **3a**, the daemon runs a flow agent headless as a job (this spec); **3b**, a run engine that drives a plan's stories through such agents and writes the run's record, asking the manager every question; **3c**, an Opus judge that answers what the spec and plan settle, and a reworked `/flow:supervise`. Each part works without the next.

## 1. Problem

A supervised run today is a tree of subagents in one Claude session: the manager dispatches an Opus `flow:plan-execution-supervisor`, which dispatches every developer, verifier and reviewer as its own subagents and keeps a markdown state file. Over the 11 runs of the 2026-11-03 retro:

- Supervisors were 23% of the spend ($199), across 46 supervisor agents, about four per run. Each supervisor hands over to a fresh one every third story, because every report it receives grows the context it re-reads on each wake (one went from 33K to 319K tokens over six stories).
- Claude Code's hand-back rules shape the run. An agent whose turn ends with none of its own agents running must hand back, so the supervisor hands over mid-run, names its agents still out, and the manager forwards their late reports to the successor by hand.
- The run's state lives in an agent's context and a markdown file. Nothing outside the session can see a run's progress, and the manager copies the state file's rows into the run's record by hand.

Part 3's goals (owner, 2026-11-06): cut the supervisor's cost, survive hand-backs, hold each run's state as data for later visibility, and write the run's record without the manager. The chosen approach is C: the daemon runs each agent as a headless `claude -p` process and drives the run itself. No subagent tree means no hand-backs, and the engine (3b) holds the state. 3a is the foundation: the daemon runs one flow agent headless, reads its report, and resumes it with an answer.

## 2. Decisions

- **Approach C: the daemon runs agents headless** (owner, 2026-11-06). Rejected: B, a next-step engine in the daemon with a thin supervisor subagent executing its steps, which keeps the subagent tree and its hand-backs and late reports; A, run state in the daemon with today's supervisor, which barely cuts cost.
- **Part 3 splits into 3a, 3b and 3c, built in that order, with 3a gated by a probe.** Rejected: one spec for all of part 3; designing the engine first on an unverified runner.
- **An agent run is a job of its own kind.** It keeps the job machinery: the record, the shim, adoption across daemon restarts, `output.log`, cancel, retention and the work view. It is exempt from the host caps and the advisor, and admitted by a per-account concurrency limit instead. An agent is a mostly idle process that waits on the API and on builds it queues itself, so a claim would let agents starve their own builds when several runs go at once, and the scarce resource is the account's usage, not cores. Rejected: a plain queue job with a 1-core, 1 GB claim (that starvation); a process kind outside `sched` (persistence, adoption, logs, cancel and the view built again); a job the advisor sizes.
- **The account is the starting session's**, taken from the submitter's environment as every job's is. Rejected: always the default account; an `--account` choice per run.
- **The permission mode is the session's**: loom's `claude_permission_mode`, with the user's settings and sandbox, plus loom's git guard. What would prompt is denied headless, and an agent that needs it reports BLOCKED. Rejected: `dontAsk` with the agent's tools allowed (only the git guard between an agent and the machine); the same inside Claude Code's sandbox (builds needing `~/.cargo`, sccache or nix may break).
- **The agent run's record lives in the worktree's state dir**, written by `loom flow agent` under a lock, as part 2's gate runs are; the daemon knows only the jobs. Flow stays out of `core`. Rejected: agent runs in the daemon's model (new `Core` methods, wire views and generated code, and the daemon learning flow); deciding in 3b.
- **One report schema for every role**, with the report's text as the agents write it today. 3b may add structure per role (a review's findings).
- **Agent definitions don't change**; a fixed preamble before the brief tells a headless agent how to report and ask.

## 2a. Amendments from planning and the probe (2026-11-06)

They win where the sections below differ. Planning (`docs/superpowers/plans/2026-11-06-flow-in-loom-agent-runs.md`):
1. The setting is `scheduler.agent_jobs`: `scheduler.agent` already names the advisor.
2. No `rpc.Protocol` bump: an optional field is compatible.
3. The probe runs as the default account, which has a login.
4. An agent job's environment drops the parent Claude session's variables (`CLAUDECODE`, `CLAUDE_CODE_SESSION_ID`, its messaging socket and token, `CLAUDE_EFFORT`, `LOOM_MOD_SOCKET` and the rest the plan lists).
5. Agent jobs get no parallelism hints.
6. `Grant` passes agent jobs over.
7. Agent jobs wait while admission is paused.
8. The permission mode is the global config's.

The probe (`TestRealClaude_FlowAgentRun`, Claude Code 2.1.318):

9. **`--agent` turns `--json-schema` off**, so the report is text: the preamble asks the agent to end it with `STATUS: <status>`, and on NEEDS_CONTEXT a `QUESTION: <question>` line; loom reads the last STATUS line and the QUESTION after it, and a report without one is `failed`. This is §5.1's planned fallback for premise 4.
10. **`--agent`'s main thread doesn't preload the definition's `skills:`**, which subagents do. Loom inlines each preloaded `flow:` skill's `SKILL.md` body into the first prompt, after the preamble and before the brief; a resume needs none, since it is in the session's context. A preloaded skill outside the flow plugin is refused.
11. **The account's enabled plugins are turned off** (owner, 2026-11-06): `--settings` sets each plugin the account's and the project's settings enable to false. Their SessionStart hooks injected instructions no subagent gets (superpowers, the explanatory style). The flow plugin, the git guard, permissions, sandbox and CLAUDE.md stay. A settings file that can't be read refuses the run (125).
12. **The guard needs no fallback:** the hook sees `agent_type` `flow:<role>` for a `--agent` main thread, so `LOOM_FLOW_AGENT` is set for the record only.

## 3. Scope

In: the probe; the `agent` job kind in `sched` and `core` and its `scheduler.agent_limit` setting; `loom flow agent run|wait|answer|show|list|cancel`; the agent-run record; the command an agent job runs; the guard's `LOOM_FLOW_AGENT` fallback when the probe needs it; the work view's and `loom job list`'s display of agent jobs; tests; docs.

Out: the run engine and the run's record (3b); the judge and the reworked `/flow:supervise` (3c); visibility of agent runs in the TUI and on the phone beyond the job's row; structured per-role reports; agents on Windows, where `loom job` refuses.

## 4. Concepts and names

- **An agent job:** a job whose `Kind` is `agent`, running one headless Claude process for one flow agent: the first run, or a resume with an answer.
- **An agent run:** one `loom flow agent run` and every agent job after it on the same Claude session. Its ID is `--name`, else `<role>-<n>`.
- **The agent-run record:** `<state>/agents/<id>.json`, with its brief at `<state>/agents/<id>/brief.md`. `<state>` is what `loom flow conf state` prints.
- **The account key:** an agent job's `CLAUDE_CONFIG_DIR`, as its environment holds it (`""` for the default account), recorded at submit.
- **The report:** the agent's final answer, in the report schema (§5.6).

## 5. Behaviour

### 5.1 The probe

An opt-in real-Claude test, `TestRealClaude_FlowAgentRun` (`session/`, beside part 1's `TestRealClaude_FlowPluginContract`), runs a scratch plugin agent as an agent job would, in a temp git repo on a private config dir: `claude -p --agent flowprobe:probe-dev --plugin-dir <scratch> --settings <a PreToolUse hook> --output-format stream-json --verbose --json-schema <the report schema>`. It checks seven premises:

1. The plugin agent runs as the main agent with its pins: the stream's `init` or `result` names its model, and a tool outside its list is unavailable.
2. The PreToolUse hook fires headless, and its exit 2 blocks the call. The probe records the `agent_type` the hook sees for a `--agent` main thread: the namespaced name, or empty.
3. The agent's preloaded skill reaches it.
4. The `result` event carries output that matches the schema after several tool turns.
5. `--resume <session> -p "<answer>"` continues with the agent's context, the same agent and its pins.
6. Under the default permission mode, a routine command (`git status`, `ls`) runs headless, and an action Claude would ask about is denied rather than hung.
7. The transcript is written under the config dir's `projects/`, where `loom flow costs` reads it.

A failed premise changes the design:
- **Premise 2:** the guard reads `LOOM_FLOW_AGENT` (§5.4).
- **Premise 4:** the report is read from the last assistant message's status line.
- **Premise 1, 5 or 6:** the design goes back to the owner before any other package is built.

The probe's findings are recorded in the plan's execution notes.

### 5.2 The agent job kind

- **Submission.** `sched.Submit` and `Record` gain `Kind` (`""` for a build job, `"agent"`) and `Record.AccountKey`. `Submit.Validate` refuses an agent job with a claim, `Exclusive`, `IO` or `After`, with texts written there. `loom job submit` and `run` offer no kind: only `loom flow agent` submits one.
- **Admission.** `Admit` keeps agent jobs apart. A running agent adds nothing to `used`. A queued agent is never the head, never starves, and never waits for the caps. It starts when fewer than `Policy.AgentLimit` agent jobs with its account key are running; otherwise it waits with the reason `waits for an agent slot (N of N running on this account)`. Build jobs' admission is unchanged.
- **The limit.** `Policy.AgentLimit` comes from the `scheduler` block's `agent_limit`: default 4, bounds 1–16. `Resolve` fails closed outside them.
- **No sizing.** An agent job is never sized: `submitJob` skips `Size`, the advisor and beliefs, and grants and past-peak holds pass it over.
- **What stays.** The shim, adoption across restarts, `output.log`, cancel, retention and the usage sampler are unchanged. The sampler still measures the agent's session, so the commands it runs directly show as its load.
- **Display.** `core.JobView` gains `Kind`. `loom job list` and the work view show an agent job's kind and its wait reason, and show no claim.
- **Compatibility.** `Kind` and `AccountKey` are `omitempty` in `jobs.json` and on the wire, and `rpc.Protocol` is bumped (`TestProtocolReference` regenerates the reference). An older loom reading the store treats an agent job as a build job with the default claim, which is safe.

### 5.3 `loom flow agent`

- **`run <role> [--effort E] [--name N] < brief`**
  - `<role>` must name an agent in the launch's flow plugin (`plugin/agents/<role>.md`).
  - The brief is stdin; an empty one, or one over 48 KiB, is refused (exit 2), since a job's argv is capped at 64 KiB.
  - Outside a session (no `LOOM_FLOW_ROOT`) it refuses (125).
  - It writes the brief, queues an agent job (§5.4) with no message when it ends, writes the record, and prints `started <id>: flow:<role>, job <N>`.
  - A name whose run hasn't ended is refused (exit 2).
- **`wait [<id>] [<seconds>]`**
  - `<id>` defaults to the worktree's latest run; `<seconds>` to 170, and at most 170.
  - It waits until the run's current job ends or the time is up, reading the job's view before its log.
  - When the job has ended, it reads `output.log`'s stream-json: the session ID from the `system`/`init` event, and from the `result` event the structured report, `total_cost_usd`, `num_turns` and `is_error`. It records them.
  - It prints one line:
    - `done <id>: <STATUS>`, followed on a NEEDS_CONTEXT by `question: <question>`, and `report: <state>/agents/<id>/report.md`;
    - `running <id>: job <N> <standing>`;
    - `failed <id>: <why>`;
    - `cancelled <id>`.
  - Exits 0 when the run ended with a report (any status), 75 while it runs, 1 when it ended without a usable report (`failed` or `cancelled`), and 125 when it can't follow.
- **`answer <id> < text`**
  - Only for a run at NEEDS_CONTEXT or BLOCKED; otherwise exit 2.
  - It queues a new agent job that resumes the run's Claude session with the text as the prompt (§5.4), adds the job to the record, and sets the run running.
- **`show [<id>]`:** the record, in full.
- **`list`:** this worktree's runs, one line each: ID, role, status, jobs, cost.
- **`cancel [<id>]`:** cancels the run's current job if it hasn't ended, and marks the run cancelled.

### 5.4 The command an agent job runs

```
<claude> -p "<preamble>\n\n<brief>" --agent flow:<role>
  --plugin-dir $LOOM_FLOW_ROOT/plugin --settings '<the guard hook>'
  [--permission-mode <mode>] [--effort <E>]
  --output-format stream-json --verbose --json-schema '<the report schema>'
```

A resume adds `--resume <session>`, and its prompt is the answer.

- **`<claude>`** is the first word of the Claude program in the global `config.json` (`claudeProgram()`), the one sessions launch.
- **`--plugin-dir`** is the submitting session's flow root, so the agent runs the prompts and tools of the session that started it.
- **`--settings`** holds one PreToolUse entry on `Bash|Write|Edit` running `<this loom binary> flow guard`, the git guard sessions get, from the same builder (`hooks`). It has no status hooks, mod or memory dir.
- **`--permission-mode`** is loom's `claude_permission_mode` setting, passed as a session's is: nothing for `default`, so Claude's own default (`auto`) and the user's settings and sandbox apply.
- **The environment** is the submitter's (`os.Environ()`, as `submission` sends it), plus `LOOM_FLOW_AGENT=<role>`. The guard falls back to `flow:<LOOM_FLOW_AGENT>` when the hook's `agent_type` is empty, if probe premise 2 needs it. The hints add `LOOM_JOB`.
- **Session persistence stays on**, so the run can be resumed and `loom flow costs` can read it.
- **The preamble** is fixed: *You run headless as a loom agent job: nobody can message you, and SendMessage reaches no one. Your final answer is your report, in the given schema: status DONE, DONE_WITH_CONCERNS or BLOCKED, or NEEDS_CONTEXT with your question, whose answer resumes you. Run long steps through `loom flow wait`, as your rules say.*

### 5.5 The agent-run record

JSON with `schema: 1`: the ID, role, effort, cwd, the brief's path, the Claude session ID, the jobs (each with its ID and whether it started the run or answered it, with the answer's text), the status (`running`, `DONE`, `DONE_WITH_CONCERNS`, `NEEDS_CONTEXT`, `BLOCKED`, `failed`, `cancelled`), the open question, the report (also written to `report.md`), `cost_usd` and turns summed over the jobs, and why for a failure. It is written whole (a temp file renamed) under `<state>/agents/agents.lock`. A record that can't be read or is of another schema is refused and left as it is.

### 5.6 The report schema

```json
{"type": "object", "required": ["status", "report"], "additionalProperties": false,
 "properties": {
   "status": {"enum": ["DONE", "DONE_WITH_CONCERNS", "NEEDS_CONTEXT", "BLOCKED"]},
   "report": {"type": "string"},
   "question": {"type": "string"}}}
```

`question` is required in practice for NEEDS_CONTEXT: a NEEDS_CONTEXT report without one is `failed` ("a question was asked but not given").

## 6. Failure handling

Fail closed: when the daemon, Claude or a file can't answer, change nothing and say so.

- **Refused, exit 2:** an unknown role, an empty or oversized brief, a name whose run hasn't ended, and `answer` on a run not at NEEDS_CONTEXT or BLOCKED.
- **Refused, exit 125:** no flow root, or a submit the daemon refuses. No record is written.
- **A job that ends without a usable report** (killed, lost, never started, `is_error`, no `result` event, a structured output that breaks the schema, or a NEEDS_CONTEXT with no question): the run is `failed`, with why. The transcript and `output.log` are kept.
- **A cancelled job:** the run is `cancelled`.
- **A resume job the daemon refuses:** exit 125, and the record stays at NEEDS_CONTEXT or BLOCKED.
- **A record that can't be read:** exit 125, and the file is left as it is.
- **The daemon lost mid-wait:** exit 125; the job goes on, and `wait` follows it again.
- **Windows:** refused, as every `loom job` command is.

## 7. Components

- **The probe:** `session/claude_flow_agent_realclaude_test.go` (new, opt-in).
- **`sched`:**
  - `job.go`: `Submit.Kind`, `Record.Kind`, `Record.AccountKey`, and `Validate`'s agent rules.
  - `admit.go`: the agent branch of `Admit`.
  - `policy.go`: `Policy.AgentLimit` and `Resolve`'s bound.
- **`config`:** the `scheduler` block's `agent_limit`.
- **`core`:** the queue's submit (no sizing for an agent job, the account key), `JobView.Kind`, and the wait reason.
- **`core/rpc`:** the protocol bump and the regenerated reference.
- **`internal/cli`:**
  - `flow_agent.go` (new): the commands, the record, the argv builder and the stream-json reader;
  - `flow.go`: attach the commands, and the guard's `LOOM_FLOW_AGENT` fallback;
  - `job_list.go`: the kind column.
- **`session/hooks`:** a guard-only settings builder beside `SettingsJSON`.
- **`ui`:** the work view's agent rows.
- **Docs:**
  - `sched/CLAUDE.md` and `internal/cli/CLAUDE.md`: rules;
  - `docs/claude/the-work-queue.md`: the agent kind;
  - `USAGE.md`: `loom flow agent` and `agent_limit`;
  - `flow/README.md`.

## 8. Testing

Each guard is pinned from both sides:

- **`sched`:**
  - `Validate`: an agent job with a claim, `Exclusive`, `IO` or `After` is refused, and a bare one passes.
  - `Admit` tables:
    - agents are outside the caps: a full host still starts an agent, and a running agent leaves a build's room as it was;
    - the per-account limit holds the fifth agent of an account back and lets another account's through;
    - an agent is never the head or a starving job;
    - build-only tables are unchanged.
  - `Resolve`: `agent_limit`'s default and bounds.
  - The store round-trips `Kind` and `AccountKey`.
- **`core`:** an agent submit never reaches the advisor or `Size`; the job's view carries `Kind` and the wait reason.
- **`internal/cli`**, with a fake `claude` (a script that writes stream-json `init` and `result` lines from its arguments), run as real agent jobs on the hermetic test daemon:
  - run → wait → `done … DONE`;
  - NEEDS_CONTEXT → `answer` → a resume job whose argv holds `--resume <session>` and the answer;
  - `failed` for a missing `result`, an `is_error` result, output that breaks the schema, and a NEEDS_CONTEXT without a question;
  - cancel;
  - every refusal;
  - the argv builder's flags, settings JSON, permission mode and environment.
- **The guard's fallback**, if premise 2 needs it: a guarded `LOOM_FLOW_AGENT` with an empty `agent_type` is checked, and an unguarded one passes.
- **The probe** (§5.1), run before any other package is built.

## 9. Known limits and later work

- Builds an agent runs directly are not admitted against the caps, as today; the prompts keep heavy steps on `loom flow wait`. The sampler shows them as the agent's load.
- An agent job's own usage limits are the account's: a run that hits a usage limit fails like any job, and 3b decides whether to retry.
- A headless agent can't be steered mid-turn; only an answer to its question resumes it.
- 3b: the run engine (stories, routing, gates through `loom flow gates`, reviews, fix rounds, the run's record). 3c: the judge and the reworked `/flow:supervise`. Later: agent runs and plan progress in the TUI and on the phone.
