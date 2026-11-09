# internal/flowview

The flow visualisation's reader (spec `docs/superpowers/specs/2026-11-09-flow-visualisation-design.md`). It turns a worktree's flow files into plain values for the phone's flow tab, its fleet chip and its Flows screen. It holds no model and no UI, writes nothing, and runs no process but git (through `internalexec.GitCommand`); `core` calls it on job goroutines.

## What a flow is

A plan file, or a spec on its way to one. In a worktree the flows are the plans in the branch's own files (`branchFiles`: `git log --first-parent --no-merges --name-only <base>..HEAD` and `git status`) that hold the `plan_join` marker or a `**Spec:**` line, the plans the state dir's run states name, and each `specs/*-design.md` in the branch's files that no plan names or mentions (a flow in design).

Each stage (design, spec, draft, run, record) is read from what it leaves behind (`stagesOf`): the committed spec and plan, the drafts dir (`<paths.drafts, else .flow/drafts>/<stem>/`, its story drafts and its STATE.md), the plan's `**Design stage:**` line, the run states (`<paths.state, else <git-dir>/flow>/runs/*.json`), the supervisor's state file (`agent-reports/supervisor-<stem>.md`), and the record (`<records>/<key>/<YYYY-MM-DD>-<stem without its date>.md`, `<key>` as `RepoKey` gives it).

## Files

| File | Holds |
|---|---|
| `find.go` | `Summary`, `Find`, `gather` (one look at a worktree), the run states (`readRuns`), the spec, supervisor, drafts and record lookups |
| `stages.go` | the stage, state, status and document names; `Stage`, `stagesOf`, `furthest`, `statusOf` |
| `detail.go` | `FlowDetail`, `StoryView`, `StepView`, `GateLine`, `OtherRun`, the cells (`CellTodo` … `CellSkipped`), `Detail` and the board (`board`, `agentStep`, `gateStep`, `cells`) |
| `history.go` | `PastRun`, `History`: the records' headers, their `agent-costs` totals and their final rows' USD |
| `docs.go` | `Doc`, `DocLimit`, `ReadDoc` and `docPath`: the documents a flow opens |
| `costs.go` | `Priced` (`loom flow costs run --json`), `Costs`, `Comparison`, `CostsOf`: sums by story, agent and role, the whole flow, the projection and the comparison |
| `plan.go` | `planHeader`, `parsePlan`, `skeletonOf`, `phaseOf`, `readCapped`; the patterns copied from `check_plan.py` and `plan_join.py` |
| `paths.go` | `profilePaths` (a reader of `paths.state` and `paths.drafts` only), `stateDir`, `draftsDir`, `gitOut`, `gitRaw` |
| `branch.go` | `branchFiles`, `headTree` |
| `key.go` | `RepoKey` |

## Tests

Fixtures are real git repositories in temp dirs, with run states, agent and gate records written through `flowrun`'s own `Save`, `SaveAgent` and `SaveGate`. `TestFind_TheStages` walks the stage table row by row, `TestDetail_TheBoard` a parked run's cells and steps, `TestHistory_ReadsEachRecord` an engine and a supervised record, and `TestFind_LeavesTheWorktreeAsItWas` that a look changes nothing.
