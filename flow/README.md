# flow

The agent flow: supervised plan execution with a pinned roster of subagents. Drafters write a plan's stories from its spec and skeleton (`/flow:draft`); a supervisor runs them story by story, dispatching a developer per story, a verifier for the gates, reviewers for each story and the whole, and a doc writer after each story's reviews (`/flow:supervise`); or loom's run engine runs them, a Go process that dispatches each step as a headless agent job and asks the manager every judgement (`/flow:run`, `loom flow run`, in [`../internal/flowrun`](../internal/flowrun)); each run leaves a record, and an owner-started retro turns the records into roster changes (`/flow:retro`). `/flow:init` drafts a repo's profile. The rules every controller follows are in [`plugin/docs/dispatch.md`](plugin/docs/dispatch.md); the records' format in [`plugin/docs/records.md`](plugin/docs/records.md).

Moved into loom from the private claude-flow repo at d937675 on 2026-11-05. Its history, its evidence document and its pin provenance before the move stay there. Design: [`../docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md`](../docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md).

## Layout

| Path | What |
|---|---|
| `plugin/.claude-plugin/plugin.json` | the plugin's manifest, name `flow` |
| `plugin/agents/` | the 13 pinned agents: `drafter`, `plan-execution-supervisor`, `junior-dev`, `intermediate-dev`, `senior-dev`, `doc-writer`, `verifier`, `spec-reviewer`, `code-quality-reviewer`, `story-reviewer`, `premise-check`, `recon`, `glance` |
| `plugin/skills/` | `init`, `draft`, `supervise`, `run` and `retro` (owner-started, `disable-model-invocation: true`) and `habits` (preloaded by the developers) |
| `plugin/docs/` | `plugin/docs/dispatch.md`, `plugin/docs/records.md` |
| `lib/` | the Python and bash tools, their tests (`*_test.py`, `lib/test.sh`), `lib/guarded-agents.txt`, `lib/banned-tokens.txt`, `lib/prices.toml` |
| `embed.go` | the Go package: the embed, `Write`, `RosterID`, `Guarded`, `GuardedMode`, `IsRoot` |
| `PROVENANCE.md` | pin changes from the move on |

## Names

Claude namespaces a plugin's names: the skills are `/flow:init`, `/flow:draft`, `/flow:supervise`, `/flow:run`, `/flow:retro` and `flow:habits`, and the agents `flow:<name>` (`flow:verifier`, …). Plans name a story's developer bare (`intermediate-dev`); `check_plan.py` also reads `flow:intermediate-dev`.

## A launch's copy

`session` writes a copy into each Claude launch's hooks folder, `<hooks folder>/flow/`, as `Write` lays it out: `plugin/`, `lib/` without its tests, and `ROSTER`, the roster ID (`loom flow roster <12 hex>`, the SHA-256 over `plugin/`'s files). The launch gets `--plugin-dir <hooks folder>/flow/plugin`, `LOOM_FLOW_ROOT=<hooks folder>/flow`, and a PreToolUse hook on Bash, Write and Edit running `loom flow guard`, which runs `lib/git_guard.py` for the agents `lib/guarded-agents.txt` lists. The `flow` setting (`config.json`, default on) turns all of it off.

## Tools

`loom flow <tool>` runs a script from the launch's root (`LOOM_FLOW_ROOT`), or outside a session from a temp copy of the binary's, with `FLOW_ROOT`, `FLOW_DATA` (`<global dir>/flow`) and `FLOW_ROSTER` set.

| Tool | Runs |
|---|---|
| `conf` | `flowconf.py`: key, status, show, state, drafts, vacuous, roster, gates, docs-check, doctor |
| `check-plan` | `check_plan.py` |
| `packet` | `plan_packet.py` |
| `join` | `plan_join.py` |
| `premises` | `plan_premises.py` |
| `conformance` | `plan_conformance.py` |
| `costs` | `agent_costs.py`: `session`, `records [FILE ...]` (a path, or a record's name in the home store: `<key>/<file>.md`), `run RUN [--json] [--session NAME=ID]` (the JSON is loom's flow board's), `design SPEC [--plan PLAN] [--json]` (the design session: the transcripts that wrote the spec, up to the plan's commit) |
| `doctor` | `flowconf.py doctor` |
| `check-agents` | `lib/check-agents.sh` |
| `wait start NAME -- CMD`, `wait wait (NAME \| --job N) [SECONDS]` | Go: a step as a job in loom's queue, and a bounded wait for it |
| `agent run ROLE < BRIEF`, `agent wait\|answer\|show\|list\|cancel` | Go: a flow agent run headless as an agent job in loom's queue, its report, and an answer that resumes it |
| `gates start (--story COMMIT… \| --final)`, `gates wait [RUN] [SECONDS]`, `gates cancel [RUN]` | Go: the gates as a chain of jobs, each step's verdict, and the resubmission after a final FAIL |
| `guard` | Go: the PreToolUse hook |
| `path FILE` | Go: the absolute path of a file in this session's plugin (inside a session only) |
| `import CHECKOUT` | Go: copy a claude-flow checkout's `records/` into `<global dir>/flow/records`, and say where its profiles go |

## Data

- A repo's profile is its own `<repo>/.claude/flow.toml`, committed there.
- Run records and retros: `<global dir>/flow/records/<key>/` and `<global dir>/flow/records/retros/` (`~/.loom/flow/…` by default), no repository.
- Words the agents and skills must not hold: `lib/banned-tokens.txt`, plus the owner's private `<global dir>/flow/banned-tokens.txt` when it exists.
