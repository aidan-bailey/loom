# Remove /flow:supervise: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. The owner chose inline execution (2026-11-11).

**Goal:** Remove `/flow:supervise` and `flow:plan-execution-supervisor`, and point every reference at `/flow:run`.

**Spec:** `docs/superpowers/specs/2026-11-11-flow-supervise-removal-design.md`

**Architecture:** Two deletions, one lint change, and prose edits. No Go or Python behaviour changes except `check-agents.sh`'s `Agent` refusal and one check-plan message.

**Tech stack:** Markdown prompts, bash, Python.

**Rules:** commit by path only; compiling commands through `loom job`; run `bash flow/lib/test.sh` after changing `flow/`.

---

### Task 1: Delete the skill and the agent, and close the `Agent` exception

**Files:**
- Delete: `flow/plugin/skills/supervise/SKILL.md`, `flow/plugin/agents/plan-execution-supervisor.md`
- Modify: `flow/lib/check-agents.sh`, `flow/PROVENANCE.md`

- [ ] `git rm -q flow/plugin/skills/supervise/SKILL.md flow/plugin/agents/plan-execution-supervisor.md`
- [ ] In `check-agents.sh`:
  - Replace the `SPAWNING_AGENTS` comment and array with: `# No flow agent may list Agent: a subagent definition ignores an Agent(type, …) list, so one holding Agent could spawn developers, or a general-purpose agent at the session's model, from inside a plan run.`
  - Make the tools check refuse `Agent` for every definition: `fail tools "$file: lists Agent, and no flow agent may spawn agents — Agent(type) lists are ignored in subagent definitions, so it could spawn any type"`.
  - Reword the `VALID_TOOLS` comment: Agent stays a verified tool name, so a definition that lists it gets this message, not "phantom".
- [ ] In `PROVENANCE.md`, under `## plan-execution-supervisor`, add: `- 2026-11-11, removed with /flow:supervise (owner, 2026-11-11). /flow:run's engine runs every plan: six engine runs came to $2.17 per 1,000 plan lines, against $4.38 for 18 supervised runs, and parallel pairs moved to the engine first (docs/superpowers/specs/2026-11-11-flow-run-parallel-pairs-design.md).`
- [ ] Run `bash flow/lib/test.sh`. Expected: OK, with 12 agent definitions passing.

### Task 2: Point every reference at /flow:run

**Files:** `flow/plugin/docs/dispatch.md`, `flow/plugin/docs/records.md`, `flow/plugin/skills/{draft,init,habits}/SKILL.md`, `flow/plugin/agents/{doc-writer,senior-dev,drafter,story-reviewer,verifier}.md`, `flow/plugin/.claude-plugin/plugin.json`, `session/loom_context_flow.md`, `USAGE.md`, `flow/README.md`, `flow/CLAUDE.md`, `docs/ARCHITECTURE.md`, `internal/flowrun/README.md`, `flow/lib/check_plan.py`, `flow/lib/plan_premises.py`, `flow/lib/flowconf.py`

- [ ] **dispatch.md**:
  - The controller is the main session or loom's run engine.
  - Option 3 ends `/clear`, then `/flow:run <plan>`, and the engine runs the plan.
  - Tiers: the engine routes, so questions climb developer, manager, owner, and `loom flow premises` checks the plan's code against the tree.
  - The fresh-session text names the engine's questions and `/flow:run`. The `/flow:draft` hand-over ends with `/flow:run`.
  - The paragraph on running a hand-written plan says `/flow:run`. The `/flow:supervise` run paragraph becomes a short paragraph on `loom flow run start` and the record. Delete the "hands over every third story" paragraph.
  - Profile: `loom flow run start` reads `show --for supervisor` (the role keeps its name), and the engine runs the gates itself.
  - Picking: the manager decides, and the engine passes `--effort xhigh`.
  - Delete the roster row. The brainstorm rule drops "or the supervisor". The hand-back and teammate examples become general.
  - No agent lists `Agent`. The engine runs the reviews.
  - The run engine section stands alone, without "drafted for /flow:supervise" and "the briefs the supervisor wrote".
  - maxTurns: the engine resumes an intermediate once or moves a junior up. The cache and wait sections drop the supervisor examples and the `REFRESH` sentence.
- [ ] **Skills and agents**:
  - `/flow:draft`'s description and last line end with `/flow:run`, and the engine copies the Design stage line.
  - `/flow:init`'s intro says `/flow:run`.
  - `flow:habits` and `verifier` say "a dispatcher", where they now say "a supervisor".
  - doc-writer's description says "for the run engine".
  - senior-dev's description says "the manager's decision".
  - drafter: "the run's last resort", and "the run dispatches its developer at xhigh".
  - story-reviewer: "for the manager".
- [ ] **records.md**: the engine writes the record and its in-repo copy, and its stories come from the run's state.
- [ ] **loom context**: drop `/flow:supervise` from the skill lists. Option 3 says "then `/flow:run <plan>`: drafters write the stories, then loom's run engine runs them story by story with the pinned roster, and asks the manager what the spec doesn't settle". The hand-written-plan sentence says `/flow:run`.
- [ ] **USAGE.md**: drop the `/flow:supervise` bullet, make `/flow:run` the runner, and change "the five" to "the four". The tools table says "`/flow:draft` and the run engine run".
- [ ] **flow/README.md**, **flow/CLAUDE.md**, **plugin.json**, **ARCHITECTURE.md**, **flowrun README**: four skills and 12 agents, and the run engine runs plans.
- [ ] **Python docstrings and message**: `check_plan.py` names `loom flow run`, and its no-Spec message says "the run engine and a fresh manager". `plan_premises.py`: "the run engine passes its base commit". `flowconf.py` comments: the `supervisor` role is the run's block, which `loom flow run start` reads.
- [ ] Run `bash flow/lib/test.sh`, `loom job run -- sh -c 'CGO_ENABLED=0 go test -count=1 ./flow/ ./session/ ./internal/cli/ ./tools/claudemd/'`, and the CLAUDE.md lint.
- [ ] Grep: `grep -rn "flow:supervise\|plan-execution-supervisor" --exclude-dir=vendor --exclude-dir=.git .` leaves only history (`docs/superpowers/`, `CHANGELOG.md`, `flow/PROVENANCE.md`), the own-roster checks, and flowview's readers.
- [ ] Commit both tasks.

## Execution notes
