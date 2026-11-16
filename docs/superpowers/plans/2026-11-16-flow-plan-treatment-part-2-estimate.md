# Flow plan treatment, part 2: the estimate — Implementation Plan

> **For agentic workers:** executed inline by the session that wrote it (owner, 2026-11-16: "Go part 2"). Five work packages, each one commit; steps use checkbox (`- [ ]`) syntax. As in part 1, the packages give the behaviour, names and tests, and code only where its exact form is the decision.

**Goal:** The design session prices a skeleton with `loom flow estimate`, the owner approves a `**Budget:**` line, and the draft session, the run record and `loom flow costs records` measure each stage and form against it.

**Architecture:** `plan_measures.py` learns each record's per-story actuals (by form) and its budget; a new `plan_estimate.py` reads those through it and prices a skeleton's stories by form from the records or the priors in `estimates.toml`; `agent_costs.py session --plan` attributes a draft session's cost to forms for the Design stage line; the run engine copies the Budget line and gives each story's form in the record; `records` gains an Estimate table.

**Tech Stack:** Python 3 (`flow/lib`, unittest), Go (`internal/flowrun`, `internal/cli`), Markdown.

**Spec:** `docs/superpowers/specs/2026-11-16-flow-plan-treatment-design.md` §5, §6 and its Part 2 settlements.

---

## File structure

| File | Change |
|---|---|
| `flow/lib/estimates.toml` | the priors: mechanical and brief multiples, the skeleton review a plan, the plan review a full story |
| `flow/lib/plan_measures.py` | a record's budget, Form column, per-story run USD; a Design stage line's `budget` and `forms:` |
| `flow/lib/plan_skeleton.py`, `check_plan.py` | the Budget line's shape |
| `flow/lib/plan_estimate.py` (new) | `loom flow estimate <plan>` |
| `flow/lib/agent_costs.py` | `session --plan PLAN`: the `forms:` segment; `records`: the Estimate table |
| `internal/cli/flow.go` | the `estimate` tool |
| `internal/cli/flow_run.go`, `internal/flowrun/state.go`, `record.go` | `State.Budget`; the record's `- Budget:` line and Form column |
| `flow/plugin/skills/draft/SKILL.md`, `flow/plugin/docs/dispatch.md`, `flow/plugin/docs/records.md`, `session/loom_context_flow.md`, `flow/README.md`, `USAGE.md` | the proposal step, the Design stage line, the record, the commands |

## WP1: the priors, the Budget line and the record measures

**Files:** `flow/lib/estimates.toml`, `flow/lib/plan_skeleton.py`, `flow/lib/check_plan.py`, `flow/lib/plan_measures.py` and their tests.

- `estimates.toml` gains `[mechanical] draft = 0.3, run = 0.7`, `[brief] draft = 0.25, run = 1.3`, `[skeleton_review] usd_per_plan = 5.14`, `[plan_review] usd_per_full_story = 2.37`, each with its reason; `[estimate] min_runs = 3` and `min_stories = 3`.
- `BUDGET_RE = re.compile(r"^\*\*Budget:\*\*\s*draft \$([0-9][0-9,]*(?:\.[0-9]+)?); run \$([0-9][0-9,]*(?:\.[0-9]+)?) \(estimate (\d{4}-\d{2}-\d{2})\)$")`, in `check_plan.py`. `plan_skeleton.lint` fails a `**Budget:**` line in the head that doesn't match (`the **Budget:** line must read draft $<X>; run $<Y> (estimate YYYY-MM-DD)`) and a second one; `check_plan.budget_line(text)` returns `(draft, run, date)` or None, read before the first story.
- `plan_measures`: `RunMeasures` gains `budget_draft`, `budget_run` (from the record's `- Budget:` line, the Budget line's text after its label), `forms_draft: dict[str, tuple[int, float]]` (from the Design stage line's `forms:` segment: form → stories, USD) and `forms_run: dict[str, tuple[int, float]]` (the Stories table's Form column, absent meaning full, and each story's run USD: its `-s<id>-` agent rows plus an even share of the rest).

Tests: `plan_skeleton_test`: `test_a_budget_line_in_shape_passes`, `test_a_budget_line_out_of_shape_fails`, `test_two_budget_lines_fail`; `check_plan_test`: `test_budget_line_reads_the_figures`; `plan_measures_test`: `test_a_record_gives_its_budget_and_forms`, `test_a_record_without_forms_counts_every_story_full`, `test_unowned_run_costs_spread_evenly`.

- [ ] Tests red, then code, then `bash flow/lib/test.sh`; commit `feat(flow): the Budget line, the estimate's priors, and each record's actuals by form`.

## WP2: `loom flow estimate`

**Files:** `flow/lib/plan_estimate.py`, `flow/lib/plan_estimate_test.py`, `internal/cli/flow.go`.

- `plan_estimate.py PLAN [--records DIR …]`: reads the skeleton's entries (`plan_join.entries`, so it prices a skeleton before any draft), each entry's Form (`check_plan.FORM_VALUE_RE`), and the records: `--records` for tests, else the repo's (`flowconf.flow_data()/records/<repo_key>`) when it holds 3 or more engine runs, else every engine run in the home store.
- Full: draft a story = Σ(drafting − skeleton review − plan review) ÷ Σ stories, run a story = Σ run ÷ Σ stories, over the records with each figure. Mechanical and brief: their measured figures once the records hold 3 of their stories (`forms_draft`, `forms_run`), else the prior × full. Skeleton review: the records' mean once 3 hold it, else the prior. Plan review: per full story, likewise.
- Output: a header (the plan, the date, the basis: `4 engine runs of this repo` or `23 engine runs, pooled`), a table `| Story | Form | Draft | Run | As mechanical | As brief |` (the last two only for a full story, `draft / run`), the totals (`Draft $X: stories $a, skeleton review $b, plan review $c`; `Run $Y`), the design session's spend (`agent_costs.design_json(spec, None)`; `Design so far: unknown (<reason>)` when it raises), each figure's basis (`runs` or `prior`), and the line to paste: `**Budget:** draft $X; run $Y (estimate YYYY-MM-DD)`, figures rounded to whole dollars.
- No records at all: priors alone, every figure labelled `prior`, full from the pooled figures in `estimates.toml`'s `[full] draft = 5.49, run = 4.50` (the 23 runs to 2026-11-15). No usable profile: exit 1, like `check-plan`.
- `flow.go`: `{name: "estimate", script: "plan_estimate.py", short: "Price a plan skeleton's stories by form, stage by stage, for its Budget line"}`.

Tests: `test_full_figures_come_from_the_records`, `test_a_form_uses_its_prior_until_three_stories_ran`, `test_the_repo_s_records_win_from_three_runs`, `test_no_records_prices_from_the_priors`, `test_a_full_story_shows_what_the_other_forms_cost`, `test_the_budget_line_passes_the_skeleton_check`, `test_no_profile_fails`.

- [ ] Tests red, code, `bash flow/lib/test.sh`; commit `feat(flow): loom flow estimate prices a skeleton by form for its Budget line`.

## WP3: the draft session's actuals

**Files:** `flow/lib/agent_costs.py`, `flow/lib/agent_costs_test.py`, `flow/plugin/skills/draft/SKILL.md`.

- `session --plan PLAN`: after the tables, a line `Forms: forms: full 4 stories $20.10, mechanical 10 stories $15.30`: the plan's stories and forms from `check_plan.parse`, each drafter row (type `flow:drafter`) given to the stories its label names (`S<id>`, `S<a>-S<b>` meaning both, `Story <id>`, several split evenly), the skeleton, plan and risk reviewer rows left out, every other row (main included) spread evenly over the stories. A drafter naming no story is spread too.
- SKILL.md step 6: run each session's pricing with `--plan $ARGUMENTS` and copy its `forms:` segment, summed over the sessions, into the Design stage line; and when the plan's head has a Budget line, add `budget $<its draft figure>` and give the draft's actual against it in the hand-over. The line's template gains `; budget $<draft budget>; <forms segment>` before the roster, each left out when absent.

Tests: `test_session_plan_gives_the_forms_segment`, `test_a_drafter_naming_two_stories_splits_evenly`, `test_the_reviewers_go_to_no_form`.

- [ ] Tests red, code, `bash flow/lib/test.sh`; commit `feat(flow): the draft session's cost by form, for the Design stage line`.

## WP4: the run record

**Files:** `internal/cli/flow_run.go`, `internal/flowrun/state.go`, `internal/flowrun/record.go`, `internal/flowrun/record_test.go` and its golden, `flow/plugin/docs/records.md`.

- `State.Budget string \`json:"budget,omitempty"\``, set at start by `budgetLine(text)` (the plan's `**Budget:**` line before its first story, its text after the label). `Record` prints `- Budget: <it>` after the Part line when set, and the Stories table gains a **Form** column after Review (`full` for an empty form).
- `records.md`: the Budget line and the Form column.

Tests: the record golden gains a Budget line and the column; `TestBudgetLine` beside `partLine`'s test.

- [ ] Tests red, code, goldens rewritten and read, `go test ./internal/flowrun/... ./internal/cli -run 'Record|BudgetLine|PartLine'`; commit `feat(flowrun): the run record copies the plan's Budget line and gives each story's form`.

## WP5: the Estimate table and the docs

**Files:** `flow/lib/plan_measures.py`, `flow/lib/plan_measures_test.py`, `flow/lib/agent_costs.py`, `flow/plugin/docs/dispatch.md`, `session/loom_context_flow.md`, `flow/README.md`, `USAGE.md`.

- `plan_measures.estimate_table(runs)`: `## Estimate`, a row per run with a budget (`| Run | Draft budget | Draft | Draft error | Run budget | Run | Run error |`, the error as a signed percentage), then per form the stories with actuals and the draft and run USD a story; `records_report` appends it when any run has a budget or forms.
- `dispatch.md` "When the option is chosen": a step between the skeleton and the commit, the proposal (Form lines with reasons, `loom flow estimate`, the owner's edits and approval, the Budget line); `session/loom_context_flow.md`: the same in one clause; `flow/README.md` and `USAGE.md`: the `estimate` tool and `costs session --plan`.

Tests: `test_the_estimate_table_gives_each_budgeted_run_and_each_form`, `test_no_budget_no_table`.

- [ ] Tests red, code, `bash flow/lib/test.sh`, `go test ./internal/cli -run LoomContext ./tools/claudemd/...`; commit `feat(flow): the records' Estimate table, and the design session's proposal step`.

## Final verification

- [ ] `bash flow/lib/test.sh`; `go test ./flow/... ./internal/flowrun/... ./internal/cli/... ./tools/claudemd/...`; `go vet` and `gofmt -l` on the changed Go files.
- [ ] `loom flow estimate` (the worktree's `plan_estimate.py`) on A5's skeleton in prader-rs, read-only, with its stories as written and again with every story mechanical, against the real records.
