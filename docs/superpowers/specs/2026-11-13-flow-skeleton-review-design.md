# Flow: a design review of the skeleton before drafting

Status: design approved by the owner 2026-11-13.

The 2026-11-13 trace of about 95 plan-code defects in 8 `/flow:draft` runs found the spec or the skeleton behind about 19 of them, 0.125 per 1,000 plan lines over 152,028: the drafters copied a flawed design faithfully. They fall three ways:

| Group | Cases | Covered today? |
|---|---|---|
| Design logic the spec or contract fixes, or leaves out | ~11: L1 S6 fixes "rotate, then mint", so a refused mint loses the rotated token; a1 S2's spec mandates a delete-before-copy and an hourly sweep that break its own "no process deletes another's staged bytes"; research-a S3 `MAX(id)+1` reuses ids a later story's cursor relies on; rb S1 and S2 make `edit` and `frame` async without saying where SQLite runs; rb S9's revision check is not atomic with its write; L1 S5 names one check point where three paths enter; holes in failure paths (L1 S8 unpair while the service runs, run2 S2 a pane left `starting`, run2 S8, research-a S16) | Only after drafting, by the plan review's `SPEC` findings and risk review (`docs/superpowers/specs/2026-11-13-flow-plan-review-design.md`): a fix job and a re-check per story the flaw reaches |
| Skeleton lines wrong against the tree | ~9: a Files line naming the wrong test file (run2 S7), a Reading line naming the wrong file (lsqb S7), a mandated kernel error variant against the kernel's CLAUDE.md (L1 S3), contract types that clash with generated exports (research-a S9), "mirroring `StrategyId`" (research-a S1) | Mostly, by the 2026-11-13 drafter rules (reuse over the Files line, no "mirror", Read the package CLAUDE.md), the premises `generated` flag and the question round's Entry lines |
| Spec text copied verbatim | 2: SNAP-only doc wording, a table mixing units (rmat) | Minor |

The review targets the first group. Caught before drafting, a flaw costs one contract amendment or one spec answer, not fix jobs across the stories it reaches.

## 1. Decisions (owner, 2026-11-13)

- **It runs in `/flow:draft`'s question round**, one reviewer in step 2a's batch beside the question-round drafters, so it adds no wall time, and the manager settles its findings in the wait's one pass. Not in the design session, whose cost is measured by hand and which follows loom's context text less reliably than a skill's steps.
- **A new agent, `flow:skeleton-reviewer`, at Opus and high effort**, like `flow:risk-reviewer`, whose questions it asks of the spec and contract: the cases it targets are designs Sonnet drafters and reviewers passed. At 5.5 prices a pass of about 30 requests comes to $1–2 a plan.
- **It is measured**: its cost joins the Design stage line and `loom flow costs records`' Plan code table.

## 2. Behaviour

### 2.1 `flow:skeleton-reviewer`

`model: opus`, `effort: high`, no `maxTurns`, tools `Bash, Read, SendMessage`, `cacheTtl: 5m`. Read-only, so it is not on `lib/guarded-agents.txt`.

It reads the spec whole, the skeleton above `## Stories` and every entry, then the code the names contract touches, by grep and line range, opening each package's CLAUDE.md with Read. It asks seven questions of the spec and the contract:

1. **Ordering.** For each sequence the spec or contract fixes, what state is left when a later step fails after an earlier one committed?
2. **Atomicity.** Each check-then-act, read-then-write and multi-step store change: one transaction, and which concurrent caller breaks it?
3. **Blocking and concurrency.** Where an async signature's blocking work runs (SQLite, file I/O); a sweep or background job against live data.
4. **Failure paths.** What state each refused, failed, timed-out or partial outcome leaves.
5. **Invariants.** Each invariant the spec states, against each mechanism it mandates.
6. **Identity and bounds.** Ids never reused, limits, and a check on every path into what it guards.
7. **Repo rules.** Each name the contract mandates, against the CLAUDE.md of the package it lands in and the names the tree or a generated file already holds.

It doesn't judge story sizes, names' exact forms or entry formats, which the question round and `loom flow check-plan --skeleton` settle. Its report, one line each, then the coverage line:

- `FINDING <Critical|Important> SPEC §<section>: <what it mandates or leaves out>; <the case that breaks>; <fix direction>`
- `FINDING <Critical|Important> CONTRACT <name>: …` or `FINDING <Critical|Important> ENTRY Story <id> <line>: …`
- `COVERAGE spec §… read; contract lines <n>; files: <paths>; CLAUDE.md: <paths opened>`

With nothing to report, `FINDING none` before the coverage line.

### 2.2 `/flow:draft` step 2

- **2a** dispatches one `flow:skeleton-reviewer` in the question round's batch, with the plan's path, the spec's path, `<REV>` and the drafter's profile block, and writes its report on arrival to `D/findings/skeleton.md`. A report with no `COVERAGE` line, or a reviewer that goes quiet, goes to a fresh reviewer with the same brief.
- **The wait** waits for it too, and settles its findings in the same pass as the questions files: a `CONTRACT` or `ENTRY` finding by amending the contract or the entry; a `SPEC` finding as a DESIGN verdict, by the spec where it decides and by the owner where it doesn't, the answer written into the spec and committed by pathspec at once; a finding judged wrong is closed with the reason written under it in `D/findings/skeleton.md`. 2b then resumes the drafters against the settled, reviewed contract.
- **Resume:** the in-flight kind `skeleton review` restarts unless `D/findings/skeleton.md` shows it reported.

### 2.3 Cost and measure

- Step 6 adds the skeleton-reviewer row's `USD` of `loom flow costs session`, and the Design stage line gains `skeleton review $<usd>` before `plan review $<usd>`. Its readers match only `$N logged`, `N plan lines`, `plan review $N` and the roster, so they are unaffected.
- `loom flow costs records`' Plan code table (`lib/plan_measures.py`) gains a **Skeleton review** column, `n/a` for a record without the clause, and the pooled row averages it over the runs that have it.
- The hand-over says what the skeleton review found and how each finding was settled (amended, answered by the spec, the owner's answer, closed with a reason).
- **Wrong if:** the next retro's transcript trace finds spec- and skeleton-caused defects at 0.06 or more per 1,000 plan lines (half of 0.125), or the Plan code table shows the review averaging over $3 a plan.

## 3. Components

| File | Change |
|---|---|
| `flow/plugin/agents/skeleton-reviewer.md` (new) | the agent |
| `flow/plugin/skills/draft/SKILL.md` | step 2a's batch and the wait; step 1's resume rule; step 6's pricing, the Design stage line's format and the hand-over; `STATE.md`'s findings files and in-flight kinds |
| `flow/plugin/docs/dispatch.md` | the drafting section: the skeleton review; the routing table's row; the skeleton section says `/flow:draft` reviews its design |
| `flow/lib/plan_measures.py`, `flow/lib/plan_measures_test.py` | the clause parsed; the column |
| `flow/README.md`, `flow/CLAUDE.md` | 15 agents; the table's column |
| `flow/PROVENANCE.md` | a new `## skeleton-reviewer` section: the pin, the evidence, Wrong if |

No Go changes: no Go code names the reviewer agents, and the plugin is embedded whole.

## 4. Testing

- `plan_measures_test.py`: a record whose Design stage line holds `skeleton review $1.50` gives the column; one without it gives `n/a`; the pooled cell averages only the runs that have it.
- `lib/check-agents.sh` passes the new agent (a description of at most 400 characters, `cacheTtl`, `SendMessage`).
- `bash flow/lib/test.sh`, then the Go `flow` and `tools/claudemd` tests.
