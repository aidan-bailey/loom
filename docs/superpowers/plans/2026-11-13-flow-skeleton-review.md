# Flow skeleton review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Review the design a plan's spec and names contract fix before `/flow:draft` drafts them, with a new Opus `flow:skeleton-reviewer` in the question round, and measure its cost.

**Architecture:** A new read-only agent file asks seven design questions of the spec and skeleton. The draft skill dispatches it in step 2a's batch and settles its findings in the wait; step 6 prices it into a new `skeleton review $<usd>` clause of the Design stage line, which `plan_measures.py` reads into a Skeleton review column of `loom flow costs records`' Plan code table.

**Tech Stack:** Markdown agent and skill files, Python 3 (`re`, `unittest`), `bash flow/lib/test.sh`.

**Spec:** `docs/superpowers/specs/2026-11-13-flow-skeleton-review-design.md`

**Rules for whoever executes this** (from `CLAUDE.md` and `flow/CLAUDE.md`):
- Stage and commit by path only (`git add <paths>`, `git commit -- <paths>`), never `-a`, `-A` or `.`.
- Scratch files go under `/tmp`, never in the worktree.
- Heavy work goes through `loom job run`/`submit`; the flow suite is `bash flow/lib/test.sh` (unit tests plus the agent lint).
- The new agent's pin is the owner's (Opus, high, 2026-11-13) and is recorded in `flow/PROVENANCE.md`; no other pin changes.
- Keep client repos' names out of `flow/`.

---

### Task 1: The Skeleton review column

**Files:**
- Modify: `flow/lib/plan_measures.py` (docstring, `SKELETON_RE`, `RunMeasures.skeleton_usd`, `measure`, `row`, `table`)
- Test: `flow/lib/plan_measures_test.py`

- [ ] **Step 1: Write the failing tests**

In `flow/lib/plan_measures_test.py`, add to `MeasureTest`:

```python
    def test_the_skeleton_reviews_cost_is_read_apart_from_the_plan_reviews(self):
        text = RECORD.replace("; plan review $6.00;", "; skeleton review $1.50; plan review $6.00;")
        m = plan_measures.measure("r", text, 40.0)
        self.assertEqual(m.skeleton_usd, 1.5)
        self.assertEqual(m.review_usd, 6.0)
```

and in `test_a_design_stage_line_without_the_clauses_gives_none` add, after `self.assertIsNone(m.review_usd)`:

```python
        self.assertIsNone(m.skeleton_usd)
```

In `TableTest.test_a_row_per_run_and_a_pooled_row`, the three expected rows gain a last cell, ` n/a |`:

```python
        self.assertIn("| a | aaaaaaaaaaaa | cccccccccccc | 10,000 | 2 | 0.20 | $20.00 | $40.00 | $6.00 | $22.00 | 3 | 1 | $6.00 | $2.00 | n/a |", out)
        self.assertIn("| b | n/a | cccccccccccc | n/a | 2 | n/a | n/a | $10.00 | n/a | $22.00 | 3 | 1 | n/a | n/a | n/a |", out)
        # Pooled: the per-1,000 figures only over a (the run with plan lines); questions and dollars over both.
        self.assertIn("| Pooled (2 runs) | | | 10,000 | 4 | 0.20 | $20.00 | $50.00 | $6.00 | $44.00 | 6 | 2 | $6.00 | $2.00 | n/a |", out)
```

and add to `TableTest`:

```python
    def test_the_pooled_skeleton_review_is_the_average_a_plan_over_the_runs_with_it(self):
        def skeleton(usd):
            return RECORD.replace("; plan review $6.00;", f"; skeleton review ${usd}; plan review $6.00;")
        runs = [plan_measures.measure("a", skeleton("1.50"), 40.0), plan_measures.measure("b", RECORD, 10.0),
                plan_measures.measure("c", skeleton("2.50"), 30.0)]
        out = plan_measures.table(runs)
        self.assertIn("| Skeleton review |", out)
        self.assertTrue(any(line.startswith("| a |") and line.endswith("| $1.50 |") for line in out.splitlines()))
        self.assertTrue(any(line.startswith("| b |") and line.endswith("| n/a |") for line in out.splitlines()))
        pooled = next(line for line in out.splitlines() if line.startswith("| Pooled"))
        self.assertTrue(pooled.endswith("| $2.00 a plan |"), pooled)
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `cd flow/lib && python3 -m unittest -q plan_measures_test`
Expected: errors `AttributeError: 'RunMeasures' object has no attribute 'skeleton_usd'`, and the table tests fail on the missing last cell.

- [ ] **Step 3: Implement the column**

In `flow/lib/plan_measures.py`:

In the docstring, replace

```
review's USD (`<n> plan lines`, `$<n> logged` and `plan review $<n>` in the Design stage line); its
```

with

```
review's and the skeleton review's USD (`<n> plan lines`, `$<n> logged`, `plan review $<n>` and
`skeleton review $<n>` in the Design stage line); its
```

and replace

```
story. A run without a figure shows n/a; each pooled ratio uses only the runs with both its figures.
```

with

```
story, and the skeleton review's USD a plan, which its own provenance entry judges. A run without a
figure shows n/a; each pooled ratio uses only the runs with both its figures, and the pooled skeleton
review is the average over the runs that have one.
```

After `REVIEW_RE`, add:

```python
SKELETON_RE = re.compile(r"skeleton review \$([0-9][0-9,]*(?:\.[0-9]+)?)")
```

In `RunMeasures`, after `review_usd: float | None`, add:

```python
    skeleton_usd: float | None = None
```

In `measure`, replace

```python
    lines, logged, review = found(PLAN_LINES_RE, stage), found(LOGGED_RE, stage), found(REVIEW_RE, stage)
```

with

```python
    lines, logged, review = found(PLAN_LINES_RE, stage), found(LOGGED_RE, stage), found(REVIEW_RE, stage)
    skeleton = found(SKELETON_RE, stage)
```

and after `review_usd=number(review) if review else None,` add:

```python
        skeleton_usd=number(skeleton) if skeleton else None,
```

Replace `row` with:

```python
def row(name: str, draft_copy: str, run_copy: str, lines: int | None, questions: int, draft: float | None,
        run: float, review: float | None, fix: float, stories: int, vouched: int, skeleton: str) -> str:
    rate = per_thousand(questions, lines)
    spend = per_thousand(None if draft is None else draft + run, lines)
    each = None if review is None or not stories else review / stories
    return (f"| {name} | {draft_copy} | {run_copy} | {'n/a' if lines is None else f'{lines:,}'} | {questions} | "
            f"{'n/a' if rate is None else f'{rate:.2f}'} | {usd(draft)} | {usd(run)} | {usd(spend)} | {usd(fix)} | "
            f"{stories} | {vouched} | {usd(review)} | {usd(each)} | {skeleton} |")
```

In `table`, replace the header and separator lines

```python
        "| Run | Draft copy | Run copy | Plan lines | Plan-code questions | per 1,000 lines | Drafting | Run "
        "| Drafting + run per 1,000 lines | Fix + re-review on them | Stories | Vouched | Plan review | per story |",
        "|---|---|---|" + "---:|" * 11,
```

with

```python
        "| Run | Draft copy | Run copy | Plan lines | Plan-code questions | per 1,000 lines | Drafting | Run "
        "| Drafting + run per 1,000 lines | Fix + re-review on them | Stories | Vouched | Plan review | per story "
        "| Skeleton review |",
        "|---|---|---|" + "---:|" * 12,
```

replace the per-run append

```python
        lines.append(row(r.name, r.draft_copy or "n/a", r.run_copy or "n/a", r.plan_lines, r.questions, r.draft_usd,
                         r.run_usd, r.review_usd, r.fix_usd, r.stories, r.vouched))
```

with

```python
        lines.append(row(r.name, r.draft_copy or "n/a", r.run_copy or "n/a", r.plan_lines, r.questions, r.draft_usd,
                         r.run_usd, r.review_usd, r.fix_usd, r.stories, r.vouched, usd(r.skeleton_usd)))
```

before the pooled `lines.append(`, add:

```python
    skeletons = [r.skeleton_usd for r in runs if r.skeleton_usd is not None]
    skeleton = f"{usd(sum(skeletons) / len(skeletons))} a plan" if skeletons else "n/a"
```

and in the pooled row replace its last line

```python
        f"{usd(sum(r.review_usd or 0 for r in reviewed) if reviewed else None)} | {usd(each)} |"
```

with

```python
        f"{usd(sum(r.review_usd or 0 for r in reviewed) if reviewed else None)} | {usd(each)} | {skeleton} |"
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `cd flow/lib && python3 -m unittest -q plan_measures_test agent_costs_test`
Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
git add flow/lib/plan_measures.py flow/lib/plan_measures_test.py
git commit -m "feat(flow): the Plan code table gains the skeleton review's cost" -- flow/lib/plan_measures.py flow/lib/plan_measures_test.py
```

---

### Task 2: The agent and the draft skill

**Files:**
- Create: `flow/plugin/agents/skeleton-reviewer.md`
- Modify: `flow/plugin/skills/draft/SKILL.md` (description; step 1's resume rule; step 2a and the wait; step 6's pricing, the Design stage format and the hand-over; `STATE.md`)
- Modify: `flow/plugin/docs/dispatch.md` (the skeleton section; the drafting section; the routing table)

- [ ] **Step 1: Write the agent**

Create `flow/plugin/agents/skeleton-reviewer.md`:

```markdown
---
name: skeleton-reviewer
description: Reviews the design a plan's spec and skeleton fix before /flow:draft drafts them: ordering, atomicity, blocking work, failure paths, invariants, ids and bounds, and repo rules for the names the contract mandates. Runs in the question round. Read-only. Opus at high by the owner's instruction (2026-11-13). Pins set by the owner; change only on their instruction.
model: opus
effort: high
tools: Bash, Read, SendMessage
experimental:
  cacheTtl: 5m
---

You review the design of a plan before any story of it is drafted. You work
in the repository the session is in; its CLAUDE.md files are your rules. A
design session wrote the spec and the skeleton: the names contract (every
name, type and signature one story defines for another), above
`## Stories`, and an entry per story below it. `/flow:draft` is about to have
drafters write every story faithfully against them, so a flaw in the design
reaches each story that follows it: in the 8 plans drafted from 2026-11-10 to
2026-11-13, about 19 of the ~95 defects in the plans' own code were the
spec's or the skeleton's, copied as written.

The dispatch gives the plan's path (the skeleton), the spec's path, `<REV>`
and the drafter's profile block. Read the spec whole and the skeleton whole,
in reads of up to 2,000 lines, then the code the contract touches at `<REV>`
(`git show <REV>:<path>`, by grep and line range), and, with the Read tool,
the CLAUDE.md of each package the contract puts a name in: Claude Code loads
a directory's rules only when a file under it is Read.

Ask of the spec and the contract:

1. **Ordering.** For each sequence they fix, what state is left when a later
   step fails after an earlier one committed? (A refresh token rotated before
   a mint that can refuse is lost.)
2. **Atomicity.** Each check-then-act, read-then-write and multi-step store
   change: is it one transaction, and which concurrent caller breaks it?
3. **Blocking and concurrency.** Where does an async signature's blocking
   work run (SQLite, file I/O, a subprocess)? What does a sweep, timer or
   background job do to data a live request holds?
4. **Failure paths.** What state does each refused, failed, timed-out or
   partial outcome leave, and what clears it?
5. **Invariants.** Does each invariant the spec states hold against each
   mechanism it mandates?
6. **Identity and bounds.** Can an id be reused (`MAX(id)+1` after a
   delete)? Is every limit stated? Does each check sit on every path into
   what it guards?
7. **Repo rules.** Does a name the contract mandates break the rules of the
   package it lands in, or clash with a name the tree or a generated file
   already holds?

Report only what breaks, naming the interleaving, input or path. Out of
scope: story sizes, names' exact forms and entry formats (the question round
and `loom flow check-plan --skeleton` settle those), the code the drafters
will write, and style.

Severity: Critical when the design loses or corrupts data, leaks across users
or hangs; Important when it breaks under a named interleaving, input or
failure.

## Report

One line each, then the coverage line:

- `FINDING <Critical|Important> SPEC §<section>: <what it mandates or leaves out>; <the case that breaks>; <fix direction>`
- `FINDING <Critical|Important> CONTRACT <name>: <what it fixes or leaves out>; <the case that breaks>; <fix direction>`
- `FINDING <Critical|Important> ENTRY Story <id> <line>: <what it says>; <the case that breaks>; <fix direction>`
- `COVERAGE spec §<sections> read; contract lines <n>; files: <paths>; CLAUDE.md: <paths opened>`

With nothing to report, write `FINDING none` before the coverage line.

You are read-only: report, never edit a file. `Bash` is for inspection only:
no `git add`/`commit`/`checkout`/`restore`/`stash`/`reset`/`clean`, no
redirection into the repo. Don't run builds or tests. Confirm every
`file:line` you cite by reading it: a long grep output arrives as a short
preview, and `grep -r` skips gitignored files. Search the repo and its
dependency sources, never `/` or `~`.

Your report goes back to whoever dispatched you through the hand-back Claude
Code gives you when you finish. Don't also send it with SendMessage: that
delivers it twice into the dispatcher's context. A question you can't settle
goes in your report too.
```

Run: `bash flow/lib/check-agents.sh`
Expected: `OK: agent definitions passed (15 files)`.

- [ ] **Step 2: The draft skill's description**

In `flow/plugin/skills/draft/SKILL.md`, replace

```
description: Draft a supervised plan from its committed spec and skeleton with the flow roster, from a fresh session (/clear, then /flow:draft <plan>): drafters settle the names contract in a question round, then write the stories; …
```

with

```
description: Draft a supervised plan from its committed spec and skeleton with the flow roster, from a fresh session (/clear, then /flow:draft <plan>): drafters settle the names contract in a question round as Opus reviews the design, then write the stories; it joins and checks them, a recon checks the seams and reviewers each story, fresh drafters fix what they find, and it commits the plan for /flow:run.
```

(The whole line is replaced: `lib/check-agents.sh` caps a skill's description at 400 characters, and this one is 396.)

- [ ] **Step 3: Step 1's resume rule**

Replace

```
     `start` from `D/join.json`), plus "some of these may already be fixed in
     `D/S<id>.md`; fix what remains". A `flow:recon` or `flow:premise-check` restarts
     fresh unless its findings file shows it reported.
```

with

```
     `start` from `D/join.json`), plus "some of these may already be fixed in
     `D/S<id>.md`; fix what remains". A `flow:recon`, `flow:premise-check` or
     `flow:skeleton-reviewer` restarts fresh unless its findings file shows it
     reported.
```

- [ ] **Step 4: Step 2a and the wait**

Replace

```
     settled contract".
   - **The wait.** Wait until every drafter of the batch has reported, and
     settle everything in one pass: read the questions files; write each
     decision into the names contract, choosing where a user's proposal and a
```

with

```
     settled contract". In the same batch, dispatch one
     `flow:skeleton-reviewer` with the plan's path, the spec's path, `<REV>`
     and the drafter's profile block, and write its report on arrival to
     `D/findings/skeleton.md`. A report with no `COVERAGE` line, or a
     reviewer that goes quiet, goes to a fresh reviewer with the same brief;
     never message the quiet one.
   - **The wait.** Wait until every drafter of the batch and the skeleton
     reviewer have reported, and settle everything in one pass: read the
     questions files and `D/findings/skeleton.md`; settle each skeleton
     finding, a `CONTRACT` or `ENTRY` one by amending the contract or the
     entry, a `SPEC` one as a DESIGN verdict (by the spec where it decides,
     else by the owner, the answer written into the spec and committed by
     pathspec at once), and one you judge wrong by writing your reason under
     it in that file; write each
     decision into the names contract, choosing where a user's proposal and a
```

- [ ] **Step 5: Step 6 and `STATE.md`**

Replace

```
     `USD incl. est.` of each **Total** row, and the `USD` of the
     plan-reviewer and risk-reviewer rows for the plan review's cost. Note
```

with

```
     `USD incl. est.` of each **Total** row, the `USD` of the
     skeleton-reviewer row for the skeleton review's cost, and that of the
     plan-reviewer and risk-reviewer rows for the plan review's. Note
```

Replace

```
   - Set the **Phase** to committed. Tell the owner what was drafted, what
     the checks, the seams recon and the plan review found, the seams
```

with

```
   - Set the **Phase** to committed. Tell the owner what was drafted, what
     the skeleton review found and how each finding was settled, what
     the checks, the seams recon and the plan review found, the seams
```

Replace

```
`**Design stage:** /flow:draft session <ids>, <start> to <end> UTC: $<logged> logged, $<incl. est.> with estimated thinking; <n> plan lines, $<logged per 1,000 lines> per 1,000 lines; plan review $<plan-reviewer and risk-reviewer USD>; <loom flow conf roster's output>.`
```

with

```
`**Design stage:** /flow:draft session <ids>, <start> to <end> UTC: $<logged> logged, $<incl. est.> with estimated thinking; <n> plan lines, $<logged per 1,000 lines> per 1,000 lines; skeleton review $<skeleton-reviewer USD>; plan review $<plan-reviewer and risk-reviewer USD>; <loom flow conf roster's output>.`
```

Replace

```
    - <D/findings/premises.md, seams.md, review-S<id>.md, S<id>.md, seams-changed.md, seams-rerun.md or wave-S<id>.md: which agent (or the manager), and when>
```

with

```
    - <D/findings/skeleton.md, premises.md, seams.md, review-S<id>.md, S<id>.md, seams-changed.md, seams-rerun.md or wave-S<id>.md: which agent (or the manager), and when>
```

Replace

```
    - <agent id>: <questions | drafting | waiting on Stories <ids> | review | risk review | re-check | fix | seams re-check | wave fix> <story ids>
```

with

```
    - <agent id>: <questions | skeleton review | drafting | waiting on Stories <ids> | review | risk review | re-check | fix | seams re-check | wave fix> <story ids>
```

- [ ] **Step 6: The dispatch guide**

In `flow/plugin/docs/dispatch.md`, in the skeleton section, replace

```
The design session writes the skeleton beside the spec: what takes judgment,
and none of the stories' code. It holds, in order:
```

with

```
The design session writes the skeleton beside the spec: what takes judgment,
and none of the stories' code. `/flow:draft`'s question round reviews its
design before anything is drafted ([drafting the stories](#drafting-the-stories)).
It holds, in order:
```

In the drafting section, replace

```
which the manager accepts or rejects in its settlement. The manager waits for every drafter, settles
everything in one pass (contract, entries, the owner's answers in the spec),
```

with

```
which the manager accepts or rejects in its settlement. In the same batch one
`flow:skeleton-reviewer` (Opus @ high) reviews the design the spec and the
names contract fix: ordering when a later step fails, atomicity, where an
async signature's blocking work runs, failure paths, the spec's invariants
against the mechanisms it mandates, ids and bounds, and the repo's rules for
the names the contract mandates. About 19 of the ~95 defects in the plans'
own code drafted from 2026-11-10 to 2026-11-13 were the spec's or the
skeleton's, copied faithfully ("rotate, then mint" lost a token; async
signatures said nothing of where SQLite runs), and the plan review catches
them only after drafting, a fix job per story they reach. Its report goes to
`D/findings/skeleton.md`. The manager waits for every drafter and the
reviewer, settles everything in one pass (contract, entries, the skeleton
review's findings, the owner's answers in the spec),
```

In the routing table, after the row

```
| Reviewing each story's plan code in `/flow:draft`, and re-checking its fixes | `flow:plan-reviewer`; the tasks it escalates to `flow:risk-reviewer` |
```

add

```
| Reviewing the design a spec and skeleton fix, in `/flow:draft`'s question round | `flow:skeleton-reviewer` |
```

- [ ] **Step 7: Run the agent lint**

Run: `bash flow/lib/check-agents.sh`
Expected: `OK: agent definitions passed (15 files)`.

- [ ] **Step 8: Commit**

```bash
git add flow/plugin/agents/skeleton-reviewer.md flow/plugin/skills/draft/SKILL.md flow/plugin/docs/dispatch.md
git commit -m "feat(flow): a skeleton reviewer checks the design in /flow:draft's question round" -- flow/plugin/agents/skeleton-reviewer.md flow/plugin/skills/draft/SKILL.md flow/plugin/docs/dispatch.md
```

---

### Task 3: README, CLAUDE.md, provenance, gates

**Files:**
- Modify: `flow/README.md` (the agents row; the `costs` row)
- Modify: `flow/CLAUDE.md` (the agent count)
- Modify: `flow/PROVENANCE.md` (a new `## skeleton-reviewer` section between `## senior-dev` and `## spec-reviewer`)

- [ ] **Step 1: README and CLAUDE.md**

In `flow/README.md`, replace

```
| `plugin/agents/` | the 14 pinned agents: `drafter`, `junior-dev`, `intermediate-dev`, `senior-dev`, `doc-writer`, `verifier`, `spec-reviewer`, `code-quality-reviewer`, `story-reviewer`, `plan-reviewer`, `risk-reviewer`, `premise-check`, `recon`, `glance` |
```

with

```
| `plugin/agents/` | the 15 pinned agents: `drafter`, `junior-dev`, `intermediate-dev`, `senior-dev`, `doc-writer`, `verifier`, `spec-reviewer`, `code-quality-reviewer`, `story-reviewer`, `skeleton-reviewer`, `plan-reviewer`, `risk-reviewer`, `premise-check`, `recon`, `glance` |
```

and in the `costs` row replace

```
and the plan review's USD per story, from `plan_measures.py`)
```

with

```
the plan review's USD per story and the skeleton review's USD a plan, from `plan_measures.py`)
```

In `flow/CLAUDE.md`, replace `14 pinned agents` with `15 pinned agents`.

- [ ] **Step 2: Provenance**

In `flow/PROVENANCE.md`, between the `## senior-dev` section and `## spec-reviewer`, add:

```
## skeleton-reviewer

- 2026-11-13, a new agent at `model: opus`, `effort: high`, no `maxTurns`, `cacheTtl: 5m`, on the owner's instruction in the skeleton review's design (`docs/superpowers/specs/2026-11-13-flow-skeleton-review-design.md`): one pass per plan in `/flow:draft`'s question round over the design the spec and the names contract fix (ordering, atomicity, blocking work, failure paths, invariants, ids and bounds, repo rules), its findings settled in the wait. Evidence, from the transcripts of the 8 `/flow:draft` sessions of 2026-11-10 to 2026-11-13: about 19 of the ~95 plan-code defects came from the spec or the skeleton, 0.125 per 1,000 plan lines over 152,028, about 11 of them design logic the spec or contract fixed or left out (a token rotated before a mint that can refuse, a spec invariant broken by two mechanisms it mandates, `MAX(id)+1` ids, async signatures silent on where SQLite runs, failure paths left open), which the drafters copied and the plan review catches only after drafting. Opus, as `risk-reviewer`, whose questions it asks of the design: these were designs Sonnet drafters and reviewers passed. Estimate: $1–2 a plan at 5.5 prices. `5m`: it never waits on a gate. **Wrong if:** the next retro's transcript trace finds spec- and skeleton-caused defects at 0.06 or more per 1,000 plan lines, or `loom flow costs records`' Plan code table shows the review averaging over $3 a plan.

```

- [ ] **Step 3: Run the gates**

Run: `loom job run -d "flow suite (skeleton review)" -- bash flow/lib/test.sh`
Expected: the unit tests `OK` and `OK: agent definitions passed (15 files)`.

Run: `loom job run -d "flow embed and claudemd go tests" -- env CGO_ENABLED=0 go test ./flow/... ./tools/claudemd/...`
Expected: `ok` for each package.

- [ ] **Step 4: Commit**

```bash
git add flow/README.md flow/CLAUDE.md flow/PROVENANCE.md
git commit -m "docs(flow): the skeleton reviewer in the README, CLAUDE.md and provenance" -- flow/README.md flow/CLAUDE.md flow/PROVENANCE.md
```
