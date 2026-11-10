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

## code-quality-reviewer

## doc-writer

- 2026-11-10, a new agent at `model: sonnet`, `effort: high`, `maxTurns: 100`, `cacheTtl: 5m`, on the owner's instruction in the doc pass's design (`docs/superpowers/specs/2026-11-10-flow-doc-pass-design.md` §2). Not Haiku: the roster sends completeness and absence claims to Sonnet (`flow:recon`), and a wrong doc passes every gate. Not Opus: `lib/prices.toml` prices Opus 5.5 at twice Sonnet 5.5's input, cache writes and output. High, the drafter's effort for writing in a house style, for a retro to lower on evidence. `5m`: it never waits on a gate. Estimate: $0.15–0.35 a story, from the 2026-11-06 run's Sonnet fix rounds ($0.11–0.20).

## drafter

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
