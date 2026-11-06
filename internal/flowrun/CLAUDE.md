# internal/flowrun

Flow's run engine (flow in loom part 3b): `Run` drives a plan's stories through headless flow agents and gate runs, one step at a time, and parks the run on every judgement for the manager; the run's state (`State`, `<state>/runs/<id>.json`), the question kinds and their answers (`CheckAnswer`), the report-line parsers (`ParseReview`, `PremiseVerdicts`), the briefs and the record. Every side effect goes through `Effects`, which `internal/cli` implements over the daemon. Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Make every engine decision from the state's inputs and memoized results only, and give every step a name from `next` and a memo key.** A restarted engine replays the procedure from the top, and a decision on anything else (a clock, a HEAD read outside `headAt`, a map's order) takes another path on replay and submits a step twice or skips one, silently. **Enforced** by `TestRun_ARestartAtEverySavePointSubmitsNothingTwice`, which crashes the engine at every save.
- **Check the tree only before a fresh dispatch** (`checkTree` inside `agent`, after `AgentStarted`). An agent followed after a restart has moved HEAD with its own commits, and checking it parks a run that is fine. **Enforced** by `TestRun_AFollowedAgentSkipsTheTreeCheck`.
- **Reset the derived fields in `Run` (the rows, the notes, the kept Minors, the phase) and rebuild them on each pass.** Otherwise they double on every restart, and the record counts twice. **Enforced** by `TestRun_ARestartRebuildsTheRowsOnce`.
- **Read a report only through its lines (`STATUS:` in 3a, `FINDING:`, `PLAN:`, `VERDICT:`, `DEVIATION:` here), and treat a review that doesn't parse as a failed agent.** A guessed severity ships a Critical unfixed. **Enforced** by `TestParseReview` and `TestRun_FindingsRoutes`.

## Pointers

- [`../cli/CLAUDE.md`](../cli/CLAUDE.md) — `loom flow run` and the `Effects` adapter.
- [`../../flow/CLAUDE.md`](../../flow/CLAUDE.md) — the agents and the tools the engine drives.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
