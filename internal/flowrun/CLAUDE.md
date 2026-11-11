# internal/flowrun

Flow's run engine (flow in loom part 3b): `Run` drives a plan's stories through headless flow agents and gate runs, one step at a time, and parks the run on every judgement for the manager; the run's state (`State`, `<state>/runs/<id>.json`), the question kinds and their answers (`CheckAnswer`), the report-line parsers (`ParseReview`, `PremiseVerdicts`), the briefs and the record. Every side effect goes through `Effects`, which `internal/cli` implements over the daemon. Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Make every engine decision from the state's inputs and memoized results only, and give every step a name from `next` and a memo key.** A restarted engine replays the procedure from the top, and a decision on anything else (a clock, a HEAD read outside `headAt`, a map's order) takes another path on replay and submits a step twice or skips one, silently. **Enforced** by `TestRun_ARestartAtEverySavePointSubmitsNothingTwice`, which crashes the engine at every save.
- **Check the tree only before a fresh dispatch** (`checkTree` inside `agent`, after `AgentStarted`). An agent followed after a restart has moved HEAD with its own commits, and checking it parks a run that is fine. **Enforced** by `TestRun_AFollowedAgentSkipsTheTreeCheck`.
- **Reset the derived fields in `Run` (the rows, the notes, the kept Minors, the phase) and rebuild them on each pass.** Otherwise they double on every restart, and the record counts twice. **Enforced** by `TestRun_ARestartRebuildsTheRowsOnce`.
- **Let a mid-run plan edit move only what the briefs read (`State.Restory`: the ranges, `Verify`, `Interfaces`, `Landmines`), and place premise flags by the stories' ranges at the base (`storyAtLine`, `baseRange`).** A replay walks a story's memoized reports by its developer and review, and plan_premises numbers its flags by the plan at the base: a moved role takes another path past memoized steps, and a moved range hands a correction to the wrong story, silently. **Enforced** by `TestRestory` and `TestRun_RestoriedRangesReachTheNextBriefs`.
- **Read a report only through its lines (`STATUS:` in 3a, `FINDING:`, `PLAN:`, `VERDICT:`, `DEVIATION:`, `DOC:` and `LESSON:` here), each through `TagLine` (which `internal/cli`'s `statusLines` shares), and treat a review, or a doc pass, whose lines don't parse as a failed agent.** A guessed severity ships a Critical unfixed, and a reader of its own that trims markdown off both ends cuts a value's code span and fails a bold tag (`**VERDICT:** APPROVED`). **Enforced** by `TestParseReview`, `TestParseDocs`, `TestTagLine`, `TestStatusLines` and `TestRun_FindingsRoutes`.
- **Run engine code only under the engine's lock, let go of it only in a waiting effect or the join (`wait`), and return at once, touching nothing, when `wait` says the run closed (`errClosed`).** A lane track runs beside the main track, and `Run` returns at the first outcome while a track still waits on a job; a mutation outside the lock races the other track's save, and one after the close writes over a parked run's state. **Enforced** by `TestRun_ALaneQuestionParksWhileItsPartnerWorks`, `TestRun_ARunClosedBeforeItsLaneStartsMakesNoLane` and `TestRun_APairRestartedAtEverySaveSubmitsNothingTwice` under `-race`.

## Pointers

- [`../cli/CLAUDE.md`](../cli/CLAUDE.md) — `loom flow run` and the `Effects` adapter.
- [`../../flow/CLAUDE.md`](../../flow/CLAUDE.md) — the agents and the tools the engine drives.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
