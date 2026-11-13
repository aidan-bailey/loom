# Flow's pins

Pin changes from the move into loom on, 2026-11-05. Each entry goes under its agent's heading, or under "All agents": the date, the field, old → new, and the owner's instruction with its evidence. The history before the move is claude-flow's `docs/PROVENANCE.md` (archived).

## At the move (claude-flow d937675, 2026-11-05)

| Agent | Model | Effort | maxTurns |
|---|---|---|---|
| code-quality-reviewer | opus | high | — |
| drafter | sonnet | high | 200 |
| glance | haiku | medium | 25 |
| intermediate-dev | sonnet | high | 250 |
| junior-dev | haiku | xhigh | 120 |
| plan-execution-supervisor | opus | high | — |
| premise-check | haiku | high | 40 |
| recon | sonnet | medium | — |
| senior-dev | opus | high | 400 |
| spec-reviewer | opus | high | — |
| story-reviewer | opus | high | — |
| verifier | haiku | high | — |

## All agents

- 2026-11-11, the developers' preloaded `flow:habits` hardens its edit helpers: `put` writes through a temp file and keeps the file's mode, `edit` asserts one match, `new` refuses an existing file, `replace` checks a whole file's first line, heredocs are quoted, and every change goes through them (no bare `.replace`, `sed -i`, `cat >` or `open(path, "w")`); no pin changed. The owner accepted it in the 2026-11-11 retro, from 3,460 developer edits in 19 runs: scripted edits batch 3 to 5 replacements a turn and stay, but 21 to 25% of Python edits and 61% of `sed -i` edits had no match check, a whole-file write replaced a README with a stray line, and a direct write left a file truncated on a full disk.
- 2026-11-11, glance, recon, premise-check, story-reviewer, spec-reviewer, code-quality-reviewer and drafter no longer say Bash or grep output passes through rtk and can be abbreviated, and `flow:habits` and `docs/dispatch.md` no longer say `git status` is rtk-wrapped; they say instead that a long output arrives as a short preview and `grep -r` skips gitignored files. On the owner's instruction: rtk's hook is not installed, `rtk gain` shows 0 tokens saved over 20,553 commands since 2026-08-13, and agents typed `rtk proxy` 274 times in the retro's runs to dodge an abbreviation that wasn't happening.

- 2026-11-11, cache lifetime under the run engine: an agent job now runs with `FORCE_PROMPT_CACHING_5M=1` when its definition pins `cacheTtl: 5m` (`agentEnv`, `internal/cli/flow_agent_exec.go`); no pin changed. On the owner's instruction, from the 2026-11-11 retro: an engine agent is a headless main session, so all 428 agents of the 10 engine runs wrote at one hour whatever their pin, and repricing the `5m` types' writes at five minutes saves up to 18% of those runs' cost. Probed on Claude Code 2.1.322 with `flow:glance`: 11,249 tokens written at one hour without the variable, 11,248 at five minutes with it.

- 2026-11-11, effort under the run engine: an agent job now carries `--effort` with its definition's pin when the run names none (`agentEffort`, `internal/cli/flow_agent_exec.go`), on a resume too; no pin changed. On the owner's instruction, from the 2026-11-11 retro's transcript replay: `--agent` runs a headless agent at the account's effort, medium on Haiku, so in 7 of the 10 engine runs agents ran below their pin (52 of 88 first reviews and 40 of 72 first builds at medium, every junior-dev and premise-check at medium). Probed on Claude Code 2.1.322: `flow:junior-dev` (pinned xhigh) ran at medium, and at xhigh with `--effort xhigh`.

## code-quality-reviewer

## doc-writer

- 2026-11-10, a new agent at `model: sonnet`, `effort: high`, `maxTurns: 100`, `cacheTtl: 5m`, on the owner's instruction in the doc pass's design (`docs/superpowers/specs/2026-11-10-flow-doc-pass-design.md` §2). Not Haiku: the roster sends completeness and absence claims to Sonnet (`flow:recon`), and a wrong doc passes every gate. Not Opus: `lib/prices.toml` prices Opus 5.5 at twice Sonnet 5.5's input, cache writes and output. High, the drafter's effort for writing in a house style, for a retro to lower on evidence. `5m`: it never waits on a gate. Estimate: $0.15–0.35 a story, from the 2026-11-06 run's Sonnet fix rounds ($0.11–0.20).

## drafter

- 2026-11-13, four rules (`agents/drafter.md`, the draft skill's steps 2 and 4, `docs/dispatch.md`); no pin changed. On the owner's instruction, from the transcripts of the 8 `/flow:draft` sessions of 2026-11-10 to 2026-11-13 and their runs: about 95 defects in the plans' own code reached the run engine, 0.35 per 1,000 plan lines against the 2026-11-11 retro's 0.37. "Reuse, never copy": the Files line is an estimate, not a fence, and a file reuse needs is an entry line that can't hold (about 12 defects were copies or designs bent to stay inside Files, and every copier had read the original). The package rules first, with Read: 66 first-draft drafters made 7,366 Bash calls to 220 Reads, and 52 never loaded a package's CLAUDE.md, whose rules two stories broke. A **Drafts after:** entry line drafts a story that documents, tests or renders other stories' code once their drafts exist, and its drafter reads them (docs and end-to-end stories drafted beside their subjects guessed at them). `D/premises/S<id>.md`: what a draft relies on about another story's code and couldn't check goes to the seams recon, which now asks whether behaviour across stories matches as well as names (about 13 defects were behaviour across stories that names alone passed).
- 2026-11-11, the question round's file gains a fifth list, `## Size`: each story's subtasks, and a proposed split for one over 7 (`agents/drafter.md`, the draft skill's step 2); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-11-flow-spec-splitting-design.md`), from 526 stories in 51 plans: a story's size can't be predicted from its skeleton entry (r = 0.37 for files named), but the drafter has read the code it needs before it writes.
- 2026-11-11, a question round before drafting and no copied opening lines (`agents/drafter.md`, the draft skill's step 2, `docs/dispatch.md`); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-11-flow-names-contract-design.md`), from an 18-story `/flow:draft` run the same day: 6 of its 11 drafters stopped with NEEDS_CONTEXT, the 5 that didn't included the definers of what the askers asked about and drafted against the old contract, about 10 of the 11 drafts needed fix jobs, and 8 fix-job rows only copied an amended entry line. Each drafter now reports its names in `D/questions/` and waits for the manager's one settlement, and the join writes each story's opening lines from its entry.
- 2026-11-11, the body's rules for the text, two added ("Edit the smallest span", "A check can pass"); no pin changed. The owner accepted it in the 2026-11-11 retro. Evidence, from 19 run records: a later story's replace block undid an earlier story's work in 6 runs (dropped tests, a dropped test attribute, reverted doc text), and 8 stories in 7 runs carried a check that could not pass (a sweep matching its own replacement, a stale count, a flag in the wrong place). In the 10 engine runs, 44 of the manager's 66 questions were defects in the plan's own code: 0.37 per 1,000 plan lines, the figure the next retro compares.
- 2026-11-11, `/flow:draft` dispatches a drafter at `xhigh` only for a landmine marker, no longer for a shape the profile's notes describe (draft skill step 2, `docs/dispatch.md`); no pin changed. The owner accepted it the same day, from the retro's join of 146 first drafts to their stories' first reviews: note-triggered xhigh drafts (16 stories) cost $8.69 a story against $2.95 for high drafts in the same stage and drew 0.30 more Critical and Important findings a story; marker-triggered ones (12 stories) cost $4.38 against $2.52 and drew 0.49 fewer. Note-triggered drafts were $92 of the $118 xhigh premium over 19 design stages.
- 2026-11-11, the body's Reading section gained "Read in batches" (d7e13686), reverted the same day on the owner's instruction: its evidence was overstated. Drafters make 1.14 to 1.23 tool calls a request, but a reading-phase request already chains 3.3 read operations on average through Bash, and only 19% hold a single read; the batching simulation would have needed about 6.6 reads a turn, many dependent on the last.
- 2026-11-11, the body's "Tests can fail" rule gains a self-check: after each test, a `Fails without:` line naming the story code whose removal turns it red; no pin changed. The owner accepted it the same day, after the retro's transcript replay: 18 of the 82 Critical and Important findings extracted from supervised first reviews were a plan test that could not fail, 0.17 a story over 104 stories, the figure the next retro compares.

## glance

## intermediate-dev

## junior-dev

## plan-execution-supervisor

- 2026-11-11, removed with `/flow:supervise` (owner, 2026-11-11). `/flow:run`'s engine runs every plan: six engine runs came to $2.17 per 1,000 plan lines, against $4.38 for 18 supervised runs, and parallel pairs moved to the engine first (`docs/superpowers/specs/2026-11-11-flow-run-parallel-pairs-design.md`).

## plan-reviewer

- 2026-11-13, a new agent at `model: sonnet`, `effort: high`, no `maxTurns`, `cacheTtl: 5m`, on the owner's instruction in the plan review's design (`docs/superpowers/specs/2026-11-13-flow-plan-review-design.md` §2): one pass per story in `/flow:draft`, which must pay for itself. Evidence, from the 7 engine runs of 2026-11-11 to 2026-11-13 (152,028 plan lines): the 53 stories with a plan-code question took 65 fix rounds and $74.70 of fix and re-review, the 22 without one 4 rounds and $1.60; a first review by code-quality-reviewer averaged $1.10, a story-reviewer re-review $0.56. Wrong if, over the next 3 plans drafted with the review, in `loom flow costs records`' Plan code table: plan-code questions don't fall below 0.20 per 1,000 plan lines (0.35 over those 7 runs), drafting plus run costs more than $5.14 per 1,000 plan lines (the same runs, logged USD), or the plan review averages over $1.20 a story. The table's Draft copy column says which flow copy each draft session ran: judge only runs whose copy holds the review.

## premise-check

- 2026-11-13, a `generated` flag to settle: a type a step adds whose name a generated file already declares, a CLASH when the new type reaches the generator too (`agents/premise-check.md`, `lib/plan_premises.py`); no pin changed. On the owner's instruction, from the same transcripts: a story added two wire types whose names the generated bindings already exported, and the TypeScript build broke in the run.

## recon

- 2026-11-13, the seams recon's brief gains a question, does the behaviour across stories match (a status and its envelope, an order of events, what renders when), starting from the packet's unchecked premises (the draft skill's step 4, `docs/dispatch.md`, `lib/plan_packet.py`); no pin changed. On the owner's instruction, from the same transcripts: seams that checked names and signatures passed a mock of a bare 401 the other story enveloped, and tests of markup another story renders otherwise.

## risk-reviewer

- 2026-11-13, a new agent at `model: opus`, `effort: high`, no `maxTurns`, `cacheTtl: 5m`, on the owner's instruction in the plan review's design (`docs/superpowers/specs/2026-11-13-flow-plan-review-design.md` §2): reviews only the tasks `plan-reviewer` escalates. Evidence: about 12 of the ~95 plan-code defects in the 8 drafted plans of 2026-11-10 to 2026-11-13 were invented concurrency, atomicity, ordering or bounds logic (ids from `MAX(id)+1`, a read and a write in separate transactions, 500 outside calls with no timeout), and none held a landmine marker. Wrong if: as `plan-reviewer`'s entry.

## senior-dev

## spec-reviewer

## story-reviewer

## verifier
