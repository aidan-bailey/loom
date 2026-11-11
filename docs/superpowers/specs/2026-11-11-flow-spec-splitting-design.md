# Flow: splitting a spec into an epic

Status: design approved section by section 2026-11-11; awaiting review of this file.

A spec too big for one plan becomes an **epic**: its `## Delivery` section names parts, and each part is a plan of its own, designed, drafted and run after the previous part has merged to main. The skeleton lint enforces a story budget per plan and checks a part's `**Part:**` line against the spec; the question round's drafters estimate each story's size and propose splitting an oversized one before anyone drafts; records, costs and the flow view see the parts as one epic. It formalises what the owner did by hand for split-prader-server, which had no tooling.

The pinned vocabulary spec (`2026-11-11-flow-task-vocabulary-design.md`) will call a plan a Story and today's stories Tasks. This spec uses today's names; "epic" is new and collides with nothing, and the profile key it adds (`[size] plan`) keeps its meaning through that rename.

## 1. Evidence

From the plans and run records on the machine, 2026-11-11 (prader-rs and loom; 526 stories in 51 plans, 52 run records):

- **Drafting cost per line falls with plan size.** Plans of 40,000 to 49,000 lines drafted at $2.11 to $2.38 per 1,000 lines; the three under 5,000 lines cost $4.56 to $9.05. The research-page `/flow:draft` run (18 stories, about 45,000 lines, about $100) is on that curve. Splitting does not lower drafting cost; each part adds a design session, a recon, a seams check and a fix round.
- **Run cost per line rises for the largest plans.** The three split-prader-server parts (18,000 to 20,000 lines each) ran at $2.64 to $3.45 per 1,000 lines; the largest plans with a known length (22,000 to 37,000 lines) at $4.38 to $5.62, with final reviews of $4 to $9. A small, mixed sample: suggestive, not proof.
- **Story count predicts a plan's size** (r = 0.82 between stories and plan lines; 0.78 for the files their Files lines name). **Nothing at skeleton time predicts a story's size** (r = 0.37 for files named, 0.25 for subtasks).
- **Story size:** a median of 4 subtasks (`### Task` sections; p90 7, max 21) at a median 447 lines each; a median story runs 1,638 lines (p90 3,670). Plans over 15 stories mostly ran at $9 to $10 a story; the split parts held 9, 13 and 15.
- **The precedent:** split-prader-server was one spec and three plans, each merged to main before the next was written; each later plan began with a fresh recon at the new main, and its design session appended "Plan N settlements (date)" to the spec, which hold where they disagree with earlier sections.

## 2. Decisions

All by the owner, 2026-11-11.

- **Scope: plans and story size.** Splitting a spec into several plans, and a check on story size. Rejected: plans only; story size only.
- **Goals:** later parts drafted against the code as it landed; each run's risk and review bounded; earlier merges; lower cost where the run side allows it.
- **Parts are designed just in time.** The spec names every part's scope, but only part 1's skeleton is written at first; each later part's skeleton comes from a design session after the previous part merges. Rejected: every skeleton up front (later parts designed against plan text); up front with a refresh step.
- **The story budget fails the skeleton lint, with an escape hatch**: more stories than `[size] plan` (15 by default) fails unless a `**Size:**` line gives the reason. Rejected: a warning only; a failure with no override.
- **Story size goes through the question round**, where a drafter has read the code its story needs.
- **Approach A: conventions plus checks**, no new artefact. An epic file listing parts and their status (B) and a `/flow:epic` skill that drives the parts (C) are left for later.

## 3. The epic's spec

A spec that splits has a `## Delivery` section; one without it is a single plan, as today.

```markdown
## Delivery

An epic of 3 parts, each a plan merged to main before the next part is designed.

1. **Part 1: kernel and ops.** Stages 0 and 1: …
2. **Part 2: markets, trading and ledger.** Stages 2 to 4: …
3. **Part 3: identity, kalshi and backtest.** Stages 5 to 8: …
```

- Each part is a numbered item `N. **Part N: <title>.** <scope>`: what it delivers, and what it may assume earlier parts landed. The numbers run 1 to M in merge order.
- Each later part's design session appends `### Part N settlements (YYYY-MM-DD)` under `## Delivery` before it writes that part's skeleton: what its recon at the new main found and what it decided with the owner. Where a settlements section and an earlier section disagree, the settlements hold.
- A later part's session may re-cut the parts not yet started (merge, split or reorder them), updating the list and recording why in its settlements. A merged part stays as it landed.
- The first design session decides whether to split; the story budget's FAIL makes it decide, and the Delivery section records the cut.

## 4. The part's skeleton and the checks

A part's skeleton is today's skeleton plus one line beside its `**Spec:**` line:

```markdown
**Spec:** `docs/superpowers/specs/2026-11-05-split-server-design.md`
**Part:** 2 of 3
```

By convention its file is `<date>-<epic stem>-part-<n>.md`; nothing enforces the name.

`loom flow check-plan --skeleton` (`plan_skeleton.py`) adds:

- **The part.** A skeleton whose spec has a `## Delivery` section needs a `**Part:** N of M` line; N must be a part the list holds and M its count. A Part line whose spec has no Delivery section fails, and so does a missing Part line when the spec splits ("the spec delivers 3 parts: name this plan's part in a **Part:** line").
- **The story budget.** A skeleton listing more stories than the profile's `[size] plan` (15 when unset) fails unless its head has a `**Size:**` line with a reason, such as `**Size:** 18 stories as one plan: every stage shares one migration`. The FAIL names both ways out: split the spec into an epic in its `## Delivery` section, or give the reason. The budget applies to each part.

`loom flow conf doctor` checks that `[size] plan`, when set, is a positive integer. The full `check-plan` passes the Part line on: `--json` gains `"part": [N, M]` when the plan has one.

## 5. Story size in the question round

- **A fifth list in the questions file, `## Size`** (the drafter's question-round job): for each of its stories, the subtasks it will write, as a count with one line on what they are. For a story over 7 subtasks (the p90 of 526 past stories; a fixed number in the drafter's prompt), a proposed split: two entries, `<id>` and `<id>b`, each with its Files, Interfaces, Verify and Review lines and the contract names it owns. The hand-back's counts include this list's.
- **The manager settles splits in the wait**, with the names. An accepted split replaces the entry with two, moves each contract owner (`Story <id>`) to the half that defines the name, and reruns `check-plan --skeleton`; the proposing drafter drafts both halves, or, if it already held two stories, a fresh drafter takes the new half with the drafting brief. A rejected split is recorded with its reason in the story's row of `STATE.md`.
- **Splits that push the plan over its budget** fail the lint, and the manager takes it to the owner: accept a `**Size:**` reason, or stop `/flow:draft` and cut the spec into an epic in a design session. The manager never makes an epic itself.
- Suffix ids leave every other id and contract reference intact; the join, the checks and the run already accept ids such as `2b`.

## 6. Between parts

- **Part 1** runs as a single plan does today: the first design session writes the spec with its Delivery section and only part 1's skeleton, lints it and commits both; then `/flow:draft` and `/flow:run`.
- **The hand-over.** When a run ends on a plan whose Part line is N of M with N < M, `/flow:run` tells the owner: merge this part to main, then start a design session for part N+1 from the spec's Delivery section.
- **Part N+1's design session** is the owner's, on a branch cut from main after part N merged. The dispatch guide's new "Epics" section gives its steps:
  1. Read the spec, its Delivery section and the settlements so far.
  2. Run a fresh recon at the new main, scoped to part N+1 and to what earlier parts landed: the names, files and seams it builds on, and what came out other than the spec said.
  3. Append `### Part N+1 settlements (date)`: the recon's findings, the decisions made with the owner, and any re-cut of later parts (with the list, and M, updated).
  4. Write the skeleton with `**Part:** N+1 of M`, run `check-plan --skeleton` until it passes, and commit the spec and the skeleton by pathspec; then `/clear` and `/flow:draft`.
- **`session/loom_context_flow.md`** gains one sentence: when the spec has a `## Delivery` section, the supervised option writes only the next part's skeleton, and a later part's design session follows the dispatch guide's Epics section.

## 7. Records, costs and the flow view

- **Records.** The header gains `- Part: N of M of <spec path>`, copied from the plan as the Design stage line is (`records.md` documents it). The retro skill says once that records sharing a Part line's spec are one epic, to compare part by part and to sum.
- **`loom flow costs design SPEC --plan PLAN`** for a part after the first: the window opens at the previous part's last commit to its plan file (the `/flow:draft` commit that wrote its Design stage line) and closes at this part's adding commit; the sessions the previous parts' Design stage lines and run records name are left out. So part 2's design cost holds neither part 1's design session nor its drafting and run managers, though all of them wrote the spec. Part 1, and a spec without a Delivery section, keep today's rule.
- **`loom flow costs epic SPEC`** (new): one row per part (design, `/flow:draft` and run cost, plan lines, $ per 1,000 lines) and the epic's total, from the plans' Design stage lines and the records' Part lines. It is the measure of whether splitting paid.
- **The flow view and the PWA.** A plan with a Part line shows "part N of M" beside its title; a spec in design with a Delivery section shows "epic, M parts". Both come from text the view already reads.

## 8. Failure handling

Each fails closed with one sentence:

- a Part line whose spec has no Delivery section, whose N isn't listed, or whose M disagrees with the list;
- a missing Part line when the spec splits;
- over the story budget with no `**Size:**` line, or an empty one;
- a `[size] plan` that isn't a positive integer (a doctor FAIL);
- a Delivery list numbered other than 1 to M in order.

A questions file whose `## Size` list is missing or malformed goes back to a fresh drafter, as any questions file that isn't the full set of lists. A split the manager can't place (its halves name files the story's entry doesn't) is rejected with that reason. `loom flow costs design` for a part whose previous part has no plan in the repo says so and uses today's rule rather than guess a window.

## 9. Components

| File | Change |
|---|---|
| `flow/lib/plan_skeleton.py` | the part checks and the story budget |
| `flow/lib/check_plan.py` | `--json`'s `"part"` |
| `flow/lib/flowconf.py` | the `[size] plan` key and its doctor check |
| `flow/lib/agent_costs.py` | the part's design window; the `epic` subcommand |
| `flow/plugin/agents/drafter.md` | the `## Size` list and its split proposal |
| `flow/plugin/skills/draft/SKILL.md` | settling splits in the wait; over budget goes to the owner |
| `flow/plugin/skills/run/SKILL.md` | the hand-over naming the next part |
| `flow/plugin/skills/retro/SKILL.md` | records of one epic |
| `flow/plugin/docs/dispatch.md` | the Delivery section, the Part and Size lines, the story budget, the Epics section |
| `flow/plugin/docs/records.md` | the Part line |
| `internal/flowrun` | the record's Part line |
| `internal/flowview`, `internal/web/static` | the part and epic labels |
| `session/loom_context_flow.md` | the epic sentence |
| `flow/README.md`, `flow/PROVENANCE.md` | `costs epic` and the lint's checks; the drafter's Size list (no pin changed) |

`loom flow costs` and `loom flow check-plan` pass their arguments to their scripts, so `costs epic` needs no Go change.

## 10. Testing

`bash flow/lib/test.sh`, `CGO_ENABLED=0 go test ./...` and `go run ./tools/claudemd` run them.

- `plan_skeleton_test.py`: each part check, red and green; the budget at its limit passes, one over fails, one over with a reason passes, an empty reason fails, with the profile's value and the default.
- `check_plan_test.py`: `--json`'s `"part"`, present and absent.
- `flowconf` tests: `[size] plan` read, defaulted and refused by the doctor.
- `agent_costs` tests: a part 2 design window that leaves out part 1's design, drafting and run sessions, from fixture transcripts and records; `costs epic` sums the parts, and a part with no record shows its design and drafting costs only.
- `internal/flowrun` and `internal/flowview` tests: the record's Part line; the "part N of M" and "epic, M parts" labels.
- The prompts: the agent lint, `TestLoomContext_NamesRealSessionFlags` for a command the context names, and a read-through of the drafter's Size list, the draft skill's settling and the Epics section against §5 and §6.

After the first epic, `loom flow costs epic` against the split-prader-server parts and the large single plans of §1 says whether splitting paid.

## 11. Known limits and later work

- The budget counts stories, which predicts a plan's size well and a story's size not at all; story size rests on the drafter's estimate of its subtasks, made after reading the code but before writing it.
- `costs design`'s exclusion relies on the Design stage lines and run records naming their sessions; a design session that ran in the same Claude session as an earlier part's drafting is priced with that drafting.
- The epic's state (which parts have merged) is read from the plans and records, not stored; an epic file (approach B) or a `/flow:epic` skill (approach C) may come later.
- When the vocabulary spec lands, each part becomes a Story and the story budget counts Tasks; `[size] plan` and the Part line keep their meaning.
