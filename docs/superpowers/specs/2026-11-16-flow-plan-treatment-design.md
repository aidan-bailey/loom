# Flow: the planner proposes how each story is written, and what the plan will cost

Status: design approved section by section 2026-11-16; this file awaits the owner's review.

Every story of a supervised plan is treated alike today: a Sonnet drafter writes its complete code, the seams recon and premise-check read it, the plan review reads it when its drafter flags risky logic, and the run builds it from that code. A plan that only moves code pays for all of it. This spec gives each story a form (full code, mechanical, or brief) that the design session proposes with a per-stage cost estimate, and the owner edits and approves before anything is drafted. The approved figures are recorded against what each stage spends, so that a later part can enforce them once the estimate has proved itself.

## 1. Problem

Evidence: prader-rs's workspace store part A5 ("the host's half"), designed and drafted 2026-11-16, 14 stories, almost all moving items between crates and rewiring their uses.

- **Before any code ran it cost $171.69:** $40.45 for the design session (of which $12.25 was a merge-conflict resolution) and $131.24 for `/flow:draft`, still in flight at that figure. That is $8.59 per 1,000 plan lines, against $2.57 to $4.53 for parts A1 to A4 of the same epic.
- **The plan carries the code it moves.** 15,283 plan lines, 7,766 of them inside code fences, most of them existing code reproduced at its new place. Each layer reads it again: the drafter copying it (8 drafters, $61.40, about 90 sequential reads each, contexts growing to about 450K tokens), the plan review checking the copy ($33.18), the fixers it sent ($12.38), then the run's developer and reviewer.
- **The review found mostly what the compiler finds.** Of its 36 Critical and Important findings, about 18 were unused or duplicate imports, visibility mismatches and broken intra-doc links, which the developers' habits already fix in place as deviations; about 13 concerned the plan's own tests and self-check greps; 2 changed behaviour. Commit 92847b5c already limits the plan review to stories whose drafter lists risky logic or whose code holds a landmine marker.
- **Across 23 engine runs, drafting cost more than running:** $1,301.92 against $1,065.46 over 237 stories, $5.49 and $4.50 a story (`loom flow costs records`, Plan code table). No one saw A5's cost coming: the flow has no estimate, and the owner first sees a stage's cost in its hand-over.

## 2. Decisions

- **A plan-wide budget** (owner, 2026-11-16). After the skeleton, the planner estimates each stage's cost and the owner approves a budget. Rejected: a per-story mode with no figures (the owner can't weigh it); separate knobs per check (too many choices for what is one judgment per story).
- **The menu holds three forms: full, mechanical and brief** (owner, 2026-11-16). Full is today's plan code; mechanical lists moves and renames without copying code; brief describes new code without writing it. Rejected: full code only (dropping checks alone leaves A5's $61 of drafting); full and mechanical only (the owner kept brief for routine new code).
- **The planner proposes each story's form, the owner edits** (owner, 2026-11-16). The flow never changes a form: a form is a judgment of the story's safety as well as its price. Rejected: a floor the flow may lift; the flow choosing forms to fit the budget.
- **On an overrun, drop the optional steps, then park** (owner, 2026-11-16), and the optional steps are the draft reviews (skeleton review, plan reviews, risk reviews, re-checks), the seams re-check and its wave, and the whole-plan review. Gates, drafting, each story's run review and the doc pass are never dropped. This is part 3's rule (§9); parts 1 and 2 only record.
- **Measure before enforcing** (owner, 2026-11-16). Parts 1 and 2 ship the forms and the estimate; enforcement waits until budgeted plans show the estimate holds. Rejected: shipping enforcement at once (its thresholds would be guesses, and a guessed threshold that drops a review is a silent quality cut); moving the draft stage into the Go engine (sturdier, but a rebuild of `/flow:draft`).

## 3. Scope

In: the **Form** entry line through the skeleton check, the join, `loom flow check-plan`, the drafter, `/flow:draft` and the run engine's review brief; `loom flow estimate` and its priors; the design session's proposal step and the **Budget** line; estimate against actual in the hand-over, the run record and `loom flow costs records`; the dispatch guide, loom's session context text and `flow/PROVENANCE.md`.

Out: enforcing the budget (part 3, designed later); any pin change (every agent keeps its model and effort; a brief story's developer stays at high); a new agent.

## 4. The forms

Each skeleton entry gains a sixth opening line, `- **Form:** full | mechanical | brief`, optionally followed by a reason in parentheses. The join writes it into the story's opening lines with the other five, and `loom flow check-plan --json` gives each story's `form`. A story without the line is `full`, so every plan written before this reads unchanged.

| | full | mechanical | brief |
|---|---|---|---|
| For | code that invents logic for store writes or id allocation, transactions, concurrency, outside calls, bounds or auth; a story holding a landmine marker | changes to where code lives or what it is called, not what it does: moves, renames, visibility changes, import rewiring, deleting dead code | new code that is routine and well covered by the gates: plumbing a flag or a field through, a message, docs, tests of existing behaviour |
| Plan text | today's: the complete code and tests | per task, a table of the items it moves or renames (the item, its path now, its new path, its new visibility), the command that lists the uses to rewrite (a grep), and the gate; fenced code only for glue too small to describe, such as a re-export | the behaviour, the contract names it defines and uses, and each test by name with what it asserts and its `Fails without:` line; no implementation code |
| The drafter reads | the code it copies or edits | the items' signatures and their use sites, never their bodies | the spec and the contract |
| `/flow:draft` checks | premises, seams, the plan review when `--select` marks the story | premises, seams; no plan review | seams; no plan review |
| The run's review | today's: `vouched` or `full` | the full review, with a move-aware brief (§4.2) | the full review |

### 4.1 Rules

- `loom flow check-plan` prints a `WARN [lint]` when a mechanical story's fenced code passes a cap (40 lines a task to start, kept in `flow/lib/estimates.toml` beside the priors): a sign it copies code it should only list. A warning, not a FAIL, like the other lints.
- A brief story whose drafter's `D/risk/S<id>.md` lists a task is a FAIL of `loom flow plan-review --select`: risky logic needs full code, and the manager takes it to the owner, who makes the story full or accepts the brief with a reason on its Form line.
- A mechanical story is never vouched, and neither is a brief one: there is no plan code to vouch for. `loom flow plan-review` lists both under `full:`.
- The drafter's risk file is still written for every form; for a mechanical story it is `none` unless the drafter finds the story invents logic, which it reports as an entry line that can't hold (the story should be full).

### 4.2 The move-aware review brief

For a mechanical story the engine's first review is `flow:code-quality-reviewer`'s, as for any story not vouched (`internal/flowrun/engine.go:1041-1043`), and its brief adds: read the story's diff with `git diff --color-moved=plain --color-moved-ws=allow-indentation-change <base>..<head>`; check that each item the story's tables name arrived unchanged except for the visibility and paths the table gives; and review only the lines that are not moves. A move diff otherwise shows every moved line twice, deleted and added, and the reviewer reads both.

## 5. The proposal and the estimate

When the design session has written the skeleton and `loom flow check-plan --skeleton` passes, it proposes the treatment before it commits:

1. It writes each entry's **Form** line, with a one-line reason.
2. It runs `loom flow estimate <plan>`, which prints:
   - a row a story: its id, form, estimated draft USD and estimated run USD;
   - the stage totals: draft (its fixed costs plus the stories) and run (the stories, the final review and the doc passes);
   - the design session's spend so far (`loom flow costs design <spec>`), shown as spent, not budgeted;
   - for each full story, its draft and run USD as mechanical and as brief, so the owner sees what a change of form buys;
   - each figure's basis: the number of runs it comes from, or `prior`.
3. It shows the owner the forms with their reasons and the estimate, and takes the owner's edits; it rewrites the Form lines and runs the estimate again until the owner approves.
4. It writes `**Budget:** draft $<X>; run $<Y> (estimate <YYYY-MM-DD>)` into the skeleton's head, beside the `**Spec:**` line, and runs `loom flow check-plan --skeleton` again.

`loom flow check-plan --skeleton` checks each Form line's value and the Budget line's shape (one line, two dollar figures, a date). A skeleton without a Budget line passes, so a plan written before this does.

### 5.1 The cost model

- **Full stories** are priced from the run records: drafting and run USD a story, from the repo's own records when it has at least 3 engine runs, else the pooled records ($5.49 and $4.50 a story over the 23 runs to 2026-11-15).
- **Fixed draft costs** come from the records too: the manager, the seams recon and the skeleton review a plan. The plan review is priced at its USD a story for each full story, the only form it reads.
- **Mechanical and brief stories** start from priors: multiples of the full story's figures, kept in `flow/lib/estimates.toml` with the reason for each. The starting priors are 0.3× to draft and 0.7× to run for mechanical, and 0.25× to draft and 1.3× to run for brief (a developer writing the code takes more fix rounds). A form's prior gives way to its measured figures once 3 of its stories have run.
- `loom flow estimate` prices in logged USD, the basis of the Design stage line and the records.

## 6. Estimate against actual

So that part 3 has numbers to trust:

- `/flow:draft`'s Design stage line gains `budget $<X>` and `forms: <form> <n> stories $<draft USD>, …`, the draft USD of each form from the `loom flow costs session` rows whose descriptions name its stories (an agent naming two stories splits its cost evenly between them). Its hand-over gives the draft's actual against its budget.
- The run record copies the plan's Budget line, and its Stories table gains a **Form** column. A story's run USD is the sum of its agent rows (`…-s<N>-…`).
- `loom flow costs records` gains an Estimate table: per run, the draft and run estimates against their actuals, and per form the drafting and run USD a story. `loom flow estimate` reads the same figures.

## 7. Failure cases

- **A skeleton with no Form lines:** every story is full.
- **A skeleton with no Budget line:** `/flow:draft` and the run work as today, and the hand-over says `no budget`.
- **Records written before forms existed:** their stories count as full.
- **No records at all:** `loom flow estimate` prices from the priors alone and labels every figure `prior`.
- **No usable profile:** `loom flow estimate` fails, as `loom flow check-plan` does.

## 8. Testing

- Python (`bash flow/lib/test.sh`): the estimate's pricing over fixture records, the repo-or-pooled choice and the priors; `check-plan --skeleton` on the Form and Budget lines; the join writing the Form line; `check-plan --json`'s `form`; the mechanical code-cap warning; `plan-review --select` failing a brief story with a listed risky task; the Estimate table; the Design stage line's parsing.
- Go: the engine reads each story's form, and a mechanical story's first review brief carries the move-aware instructions.

## Delivery

An epic of 3 parts, each a plan merged to main before the next part is designed.

1. **Part 1: the forms.** §4: the Form line through the skeleton check, the join, `loom flow check-plan` (`--json`'s `form`, the mechanical cap warning), the drafter's three forms, `/flow:draft`'s checks per form and `plan-review --select`'s brief-story FAIL, and the engine's move-aware review brief; the dispatch guide and `flow/PROVENANCE.md`.
2. **Part 2: the estimate.** §5 and §6: `loom flow estimate` and `flow/lib/estimates.toml`, the design session's proposal step and the Budget line, the Design stage line's budget and forms, the record's Budget line and Form column, the Estimate table, and loom's session context text (`session/loom_context_flow.md`). It assumes part 1's Form line.
3. **Part 3: enforcement.** Designed in its own session once 2 or 3 budgeted plans show each stage's estimate within ±30% of its actual. On an overrun: drop the stage's remaining optional steps (§2), recording each, then park and ask the owner to raise the budget or stop when the required work alone won't fit.
