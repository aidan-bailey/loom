# Flow's pins

Pin changes from the move into loom on, 2026-11-05. Each entry goes under its agent's heading, or under "All agents": the date, the field, old → new, and the owner's instruction with its evidence. The history before the move is claude-flow's `docs/PROVENANCE.md` (archived).

## At the move (claude-flow d937675, 2026-11-05)

| Agent | Model | Effort | maxTurns |
|---|---|---|---|
| code-quality-reviewer | opus | high | — |
| drafter | sonnet | high | 200 |
| glance | haiku | medium | 25 |
| intermediate-dev | sonnet | high | 250 |
| junior-dev | haiku | xhigh | 120 |
| plan-execution-supervisor | opus | high | — |
| premise-check | haiku | high | 40 |
| recon | sonnet | medium | — |
| senior-dev | opus | high | 400 |
| spec-reviewer | opus | high | — |
| story-reviewer | opus | high | — |
| verifier | haiku | high | — |

## All agents

- 2026-11-11, cache lifetime under the run engine: an agent job now runs with `FORCE_PROMPT_CACHING_5M=1` when its definition pins `cacheTtl: 5m` (`agentEnv`, `internal/cli/flow_agent_exec.go`); no pin changed. On the owner's instruction, from the 2026-11-11 retro: an engine agent is a headless main session, so all 428 agents of the 10 engine runs wrote at one hour whatever their pin, and repricing the `5m` types' writes at five minutes saves up to 18% of those runs' cost. Probed on Claude Code 2.1.322 with `flow:glance`: 11,249 tokens written at one hour without the variable, 11,248 at five minutes with it.

- 2026-11-11, effort under the run engine: an agent job now carries `--effort` with its definition's pin when the run names none (`agentEffort`, `internal/cli/flow_agent_exec.go`), on a resume too; no pin changed. On the owner's instruction, from the 2026-11-11 retro's transcript replay: `--agent` runs a headless agent at the account's effort, medium on Haiku, so in 7 of the 10 engine runs agents ran below their pin (52 of 88 first reviews and 40 of 72 first builds at medium, every junior-dev and premise-check at medium). Probed on Claude Code 2.1.322: `flow:junior-dev` (pinned xhigh) ran at medium, and at xhigh with `--effort xhigh`.

## code-quality-reviewer

## doc-writer

- 2026-11-10, a new agent at `model: sonnet`, `effort: high`, `maxTurns: 100`, `cacheTtl: 5m`, on the owner's instruction in the doc pass's design (`docs/superpowers/specs/2026-11-10-flow-doc-pass-design.md` §2). Not Haiku: the roster sends completeness and absence claims to Sonnet (`flow:recon`), and a wrong doc passes every gate. Not Opus: `lib/prices.toml` prices Opus 5.5 at twice Sonnet 5.5's input, cache writes and output. High, the drafter's effort for writing in a house style, for a retro to lower on evidence. `5m`: it never waits on a gate. Estimate: $0.15–0.35 a story, from the 2026-11-06 run's Sonnet fix rounds ($0.11–0.20).

## drafter

- 2026-11-11, the body's rules for the text, two added ("Edit the smallest span", "A check can pass"); no pin changed. The owner accepted it in the 2026-11-11 retro. Evidence, from 19 run records: a later story's replace block undid an earlier story's work in 6 runs (dropped tests, a dropped test attribute, reverted doc text), and 8 stories in 7 runs carried a check that could not pass (a sweep matching its own replacement, a stale count, a flag in the wrong place). In the 10 engine runs, 44 of the manager's 66 questions were defects in the plan's own code: 0.37 per 1,000 plan lines, the figure the next retro compares.
- 2026-11-11, the body's Reading section gains "Read in batches"; no pin changed. The owner accepted it the same day, from the retro's replay of 75 first drafts: drafters made 1.14 to 1.23 tool calls a request, re-reading their context on each, and context re-reads were over half of a draft's cost. A simulation of two reads a turn before the first write saved 16% of those drafts' $328, three a turn 22%; both are ceilings. The next retro compares tool calls a request.
- 2026-11-11, the body's "Tests can fail" rule gains a self-check: after each test, a `Fails without:` line naming the story code whose removal turns it red; no pin changed. The owner accepted it the same day, after the retro's transcript replay: 18 of the 82 Critical and Important findings extracted from supervised first reviews were a plan test that could not fail, 0.17 a story over 104 stories, the figure the next retro compares.

## glance

## intermediate-dev

## junior-dev

## plan-execution-supervisor

## premise-check

## recon

## senior-dev

## spec-reviewer

## story-reviewer

## verifier
