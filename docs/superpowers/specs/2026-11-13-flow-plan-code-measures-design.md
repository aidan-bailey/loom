# Flow: plan-code measures in the records, and a stale-copy warning

Status: design approved by the owner 2026-11-13.

The plan review's provenance entry (`flow/PROVENANCE.md`, plan-reviewer, 2026-11-13) judges it by three figures over the next three reviewed plans: plan-code questions per 1,000 plan lines, drafting plus run USD per 1,000 plan lines, and the review's USD per story. Today those come from scratch scripts. And the 2026-11-13 analysis found that none of 8 `/flow:draft` sessions ran the question round its rules described, because a session keeps the flow copy it launched with and nothing said so. This spec makes the figures a table of `loom flow costs records`, records which copy the draft session ran, and warns a session whose copy isn't the installed loom's.

## 1. Decisions (owner, 2026-11-13)

- **Every figure comes from the run record alone**, so a retro needs no transcripts: the run's cost tables, its `Asked` lines, its Stories table, its Definitions line and the Design stage line the engine copies in.
- **Logged USD throughout**, the Design stage line's basis. The provenance entry's baseline is recomputed with the tool.
- **The Design stage line gains two clauses**, `plan review $<usd>` and the draft session's `loom flow roster <hex>`. Its readers match only `$N logged` and `N plan lines` (`agent_costs.py`, `internal/flowview/costs.go`), so they are unaffected.
- **A stale copy is a WARN, never a FAIL.** A relaunch helps only once the daemon runs the installed build, since the daemon writes each launch's copy from its own build; a FAIL would strand a session behind an old daemon.

## 2. Behaviour

### 2.1 The Plan code table

`loom flow costs records` appends `## Plan code` after its cost tables when any record was written by the run engine (`- Engine: loom flow run`). One row per such run, then a pooled row:

| Column | Source |
|---|---|
| Run | the record's file stem |
| Draft copy | `loom flow roster <hex>` in the Design stage line, else n/a |
| Run copy | the last `loom flow roster <hex>` of the Definitions line |
| Plan lines | `<n> plan lines` in the Design stage line, else n/a |
| Plan-code questions | the `- Asked (unresolved-findings, Story N): Reviews of Story N found problems in the plan's own code` lines |
| per 1,000 lines | questions ÷ plan lines × 1,000 |
| Drafting | `$<n> logged` in the Design stage line |
| Run | the record's agent-costs comments' total USD |
| Drafting + run per 1,000 lines | (drafting + run) ÷ plan lines × 1,000 |
| Fix + re-review on them | the USD of every Cost by agent row named `…-s<N>-fix<k>` or `…-s<N>-review<k>` with k ≥ 2, for each story N with a plan-code question, across every session's table |
| Stories, Vouched | the Stories table's rows, and those whose Review cell is `vouched` |
| Plan review | `plan review $<n>` in the Design stage line, else n/a |
| per story | plan review ÷ stories |

The pooled row sums each column over the runs that have it; each pooled ratio uses only the runs with both of its figures.

### 2.2 The Design stage line

`/flow:draft` step 6 writes:

`**Design stage:** /flow:draft session <ids>, <start> to <end> UTC: $<logged> logged, $<incl. est.> with estimated thinking; <n> plan lines, $<logged per 1,000 lines> per 1,000 lines; plan review $<usd>; loom flow roster <hex>.`

`plan review` sums the plan-reviewer and risk-reviewer rows of `loom flow costs session`; the roster is `loom flow conf roster`'s output.

### 2.3 The stale-copy warning

- `loom flow` passes every script `FLOW_INSTALLED_ROSTER`, this binary's `flow.RosterID()`, beside `FLOW_ROSTER` (the copy's).
- `loom flow conf doctor` prints `WARN flow copy: this session runs <copy>, the installed loom has <installed>: …` when both are set and differ, `PASS flow copy` when they match, and no line when `FLOW_INSTALLED_ROSTER` is unset (an older binary).
- `loom flow run start` prints the same line on stderr when the session copy's ROSTER differs from `flow.RosterID()`, and goes on. It is the installed binary, so it warns even in a session launched before this change.
- `/flow:draft` step 1 tells the owner about a `WARN flow copy` line and goes on unless told to stop.

## 3. Components

| File | Change |
|---|---|
| `flow/lib/plan_measures.py` (new) | `measure` (one record's figures) and `table` |
| `flow/lib/agent_costs.py` | `records_report` measures each engine record and appends the table |
| `flow/lib/doctor.py` | `copy_line`, called after the guard check |
| `internal/cli/flow.go` | `copyWarning`; `FLOW_INSTALLED_ROSTER` in `flowScript`'s environment |
| `internal/cli/flow_run.go` | `run start` prints `copyWarning` |
| `flow/plugin/skills/draft/SKILL.md` | step 1's WARN line; step 6's two clauses; the line's format |
| `flow/README.md` | `costs records`' Plan code table |
| `flow/PROVENANCE.md` | plan-reviewer's baseline, recomputed with the table |

## 4. Testing

- `plan_measures_test.py`: a synthetic engine record with two sessions' Cost by agent tables, a Stories table with a vouched row, plan-code and other `Asked` lines and a Design stage line with both new clauses gives each figure; a record without the clauses gives n/a; a record without the Engine line gives none; the pooled row skips a run without plan lines.
- `agent_costs_test.py`: `records` prints the Plan code table for an engine record and none for the existing synthetic records.
- `doctor_test.py`: `copy_line` for both set and equal, both set and different, and the installed one unset.
- Go: `TestFlowTool_PassesTheInstalledRoster` (a script sees `FLOW_INSTALLED_ROSTER` = `flow.RosterID()`), and `TestCopyWarning` (empty for this binary's roster and for none, the line for another).
- Then the table over the 7 engine runs of 2026-11-11 to 2026-11-13 gives the baseline the provenance entry quotes.
