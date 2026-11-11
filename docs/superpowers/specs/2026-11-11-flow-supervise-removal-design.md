# Flow: remove `/flow:supervise`

Status: scope agreed with the owner 2026-11-11 (the parallel pairs spec's §9); the owner said go.

`/flow:run`'s engine runs a plan for about half what the Opus supervisor cost: six engine runs came to $2.17 per 1,000 plan lines, against $4.38 for 18 supervised runs. Since the parallel pairs spec (`docs/superpowers/specs/2026-11-11-flow-run-parallel-pairs-design.md`) it also runs two stories at once. The supervisor was the only controller that did that before. This spec removes `/flow:supervise` and its agent, and points every reference at `/flow:run`.

## 1. Decisions

- **Remove outright** (owner, 2026-11-11). The skill and the agent go; typing `/flow:supervise` finds no skill. Rejected: a soft deprecation that keeps it working with a notice; a stub skill that says to use `/flow:run`.
- **Keep the option's label, "3. Supervised"** (owner, 2026-11-11). It also names a repo's own supervised option in its CLAUDE.md, which loom's context defers to, and the manager still supervises the engine. "Supervised plan" stays the name of a plan for that option.
- **Keep what still has a job:**
  - the profile's `supervisor` role (`loom flow conf show --for supervisor`), which `loom flow run start` reads, and `flowrun.Blocks.Supervisor`, which a run in progress has in its state;
  - flowview's readers of earlier supervised runs (`agent-reports/supervisor-<stem>.md`, records with no `Ended` line), since those state files and records stay on disk;
  - `/flow:retro`'s supervisor cost share, for earlier records;
  - `flow:verifier`, which any controller dispatches for gates;
  - the check in `/flow:init`, `/flow:draft` and `/flow:retro` for a repo with its own roster (`.claude/agents/plan-execution-supervisor.md`), which marks a repo with its own roster, not loom's agent.
- **No agent may list `Agent`.** `lib/check-agents.sh` allowed only the supervisor to, and now refuses it for every agent.
- **History stays.** `flow/PROVENANCE.md` keeps the agent's row under "At the move", and gains a line under its heading. The specs, plans and `CHANGELOG.md` are records of their time.

## 2. Scope

Removed:
- `flow/plugin/skills/supervise/`;
- `flow/plugin/agents/plan-execution-supervisor.md`;
- `lib/check-agents.sh`'s spawning list;
- the dispatch guide's supervisor-only text: the supervisor's run, its hand-over every third story, its `REFRESH`, the roster row, the `Agent` exception, and the supervisor examples in the hand-back, teammate, cache and wait sections.

Pointed at `/flow:run`, or at the engine:
- the dispatch guide's option 3, design-stage text, profile, picking, review and run-engine sections;
- loom's context (`session/loom_context_flow.md`);
- `/flow:draft`'s last line and its Design stage note;
- `/flow:init`'s intro;
- `records.md`;
- the agents that name the supervisor (doc-writer, senior-dev, drafter, story-reviewer, verifier) and `flow:habits`;
- `USAGE.md`, `flow/README.md`, `flow/CLAUDE.md`, `plugin.json`, `docs/ARCHITECTURE.md` and `internal/flowrun/README.md`;
- the docstrings and messages of `check_plan.py`, `plan_premises.py` and `flowconf.py`.

## 3. Testing

- `bash flow/lib/test.sh`: the tools' tests and the agent lint, now with 12 definitions.
- `go test ./flow/ ./session/ ./internal/cli/`: the embed, the loom context and the CLI.
- The CLAUDE.md lint.
- A grep finds `/flow:supervise` and `plan-execution-supervisor` only in history (`docs/superpowers/`, `CHANGELOG.md`, `flow/PROVENANCE.md`), in the own-roster checks, and in flowview's readers of earlier runs.
- An edited agent definition reaches a session only after a relaunch on a build with it, so no probe runs here.
