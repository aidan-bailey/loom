# internal/flowview

The flow visualisation's reader (spec `docs/superpowers/specs/2026-11-09-flow-visualisation-design.md`): `Find`, `Detail`, `History` and `ReadDoc` turn a worktree's flow files (plans, specs in design, drafts, `<state>/runs|agents|gates`, the repo's run records) into plain values. `core` calls it on job goroutines. Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Read, never write: no file, no directory, no git command that changes anything (`gitOut` runs only reads).** The model looks on every run step and Stop, in worktrees agents are working in; `loom flow conf state` and `drafts` make the dirs they print, so this package reads the profile's paths itself (`profilePaths`). **Enforced** by `TestFind_LeavesTheWorktreeAsItWas`.
- **Decode a run state only with `flowrun.Load`, and agent and gate records only with `flowrun.LoadAgent`/`LoadGate`.** A second decoder drifts from the writers' schemas, and a half-read state draws a wrong board. **Convention** — a board that disagrees with `loom flow run show`.
- **Say `unreadable: <why>` for a file you can't read or decode, derive nothing after it from that file, and list a run state or record you can't place rather than drop it.** A guessed stage or cell is the one thing the board must not show. **Enforced** by `TestFind_AnUnreadableRunStateSaysWhy`, `TestDetail_AnUnreadableAgentRecordMarksItsStep` and `TestHistory_AnUndecodableRecordIsListedUnreadable`.
- **Place steps only with `flowrun.StepOf` and `flowrun.QuestionStep`.** The names are the engine's (`engine.next`); a parser here goes grey on the engine's next new prefix. **Enforced** for the engine's own names by `TestStepOf_ParsesEveryNameTheEngineSubmits` (`internal/flowrun`); a parser here is **Convention**.
- **Build a document's path in `docPath` from the flow, never from the caller, and refuse every doc key it doesn't list.** `ReadFlowDoc` serves the phone; a path taken from the request reads any file the daemon can. **Enforced** by `TestReadDoc_OnlyTheFlowsOwnDocuments`.

## Pointers

- [`../flowrun/CLAUDE.md`](../flowrun/CLAUDE.md) — the run state and the records this package reads.
- [`../../core/CLAUDE.md`](../../core/CLAUDE.md) — the model that calls it.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
