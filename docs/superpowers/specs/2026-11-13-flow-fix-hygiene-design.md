# Flow: fixes that break what they don't touch

Status: design approved by the owner 2026-11-13.

The 2026-11-13 trace of about 95 plan-code defects in 8 `/flow:draft` runs found about 11 that planning's own fixes introduced. Item 6's re-check (`docs/superpowers/specs/2026-11-13-flow-plan-review-design.md`) reads only a fixed story's diff, and step 5 re-runs the seams recon only when a fix changes the names contract, so most of the 11 would still pass. They fall four ways:

| Pattern | Cases | Why nothing catches it today |
|---|---|---|
| A fix breaks its own story outside the lines it edited | 3: run2 S2 (the fixer changed `type()` and left a test expecting the old behaviour, though its own grep printed it), rmat S7 (a seams fix contradicted the same story's "estimate"), rb S14 (switched to Story 12's `linePath`, kept the old expected path) | The fixer fixes only what it was given; the re-check reads only the diff |
| A fix or amendment changes behaviour another story relies on | 2–3: research-a S8 (two parallel fixers on one seams finding: S8 dropped its scanner and kept a test S3's scanner fails), L1 S4 (an amended error; S4's mock never re-checked), rb S14 | The seams recon re-runs only when a fix changes the contract |
| The manager's own finding or amendment was wrong | 5: a finding pinned the span ("edit only lines 137-141", so a bullet landed under the wrong heading), one framed one direction of a symmetric dedup (run2 S4), an amendment picked one story's version and the fixer deleted correct code (research-a S15), an amended Verify and an amended claim (rb S4, S6) | Nothing reviews what the manager writes |
| A fixer flagged a clash and nobody answered | 1 (rb S7) | Nothing requires an answer |

At the measured $1.41 an escaped defect costs a run, the 11 cost about $15 over the 7 engine runs, so the remedy is rule text and one script, with model cost only when a fix touches what another story uses.

## 1. Decisions (owner, 2026-11-13)

- **Consumers are found mechanically.** A `--changed` mode of the seams packet lists the shared names step 5's fixes touched, from the review snapshots every story already has, and a scoped recon checks only those. Not the fixers' own greps (run2 S2's fixer printed the test it then left), and not a full recon after every fix round (a recon's cost on every draft, whether or not a fix touched shared code).
- **What the scoped recon finds goes to one wave of fix jobs in the same round**, one fresh drafter per story its findings name, provider or consumer. The wave runs once and never chains: a wave fixer that would change a name or behaviour another story uses reports it unapplied, and the manager takes it to the owner. A full recon re-run (when a fix changed the contract) feeds the same wave, where its findings used to go straight to the owner.
- **The rest is rule text**: fixers bring their own draft along, the re-check follows the uses of what changed, the manager's findings state results rather than spans, and nothing a fixer raises goes unanswered.

## 2. Behaviour

### 2.1 `loom flow packet --seams --changed`

`plan_packet.py --seams --changed` writes `D/packets/seams-changed.md`. It requires what the seams packet does (the plan is the join of its drafts, else `FAIL [packet] … re-run loom flow join`), and every story's review snapshot `D/reviewing/S<id>.md`: a story without one fails with `FAIL [packet] <plan>: Story <id> has no review snapshot D/reviewing/S<id>.md` and exit 1.

For each story it diffs `D/reviewing/S<id>.md` against `D/S<id>.md` (difflib opcodes) and turns the changed draft lines into plan lines (n = start + k − 1, `start` from `D/join.json`); a pure deletion marks the draft line at its position. A changed line inside a story's code block belongs to the nearest definition at or above it in that block (the names the seams packet's `definitions` finds, test files included), so a body edit counts as well as a signature edit; lines above a block's first definition belong to none. It lists:

- **a provider changed**: a name a changed line belongs to, defined in a non-test file, that another story uses (the seams packet's `used`, less that story's own definitions);
- **a consumer changed**: a name a changed line uses (a whole word, as the seams packet reads uses) that another story defines in a non-test file and this story doesn't define.

Each row is `name | defined <cite> | used <cites> | changed in S<x>.md:<a>-<b>, …`, cites as the seams packet writes them (draft lines, since the packet needs the join map). A section `## Changed spans` gives each changed story's changed draft lines, and the reading rules tell the recon to check only the listed rows. With no row, the names section reads `(none)`. The command prints `wrote <path>: <n> lines, <k> shared names changed`; a change that touches no shared name is `0 shared names changed`.

### 2.2 Step 5's order

1. Write the findings files and dispatch the fixers, as now, with the finding rules of §2.4.
2. Run step 3 again, as now.
3. **The seams re-check.** When a fix changed the contract, run step 4's seams packet and recon, as now. Otherwise run `loom flow packet $ARGUMENTS --seams --changed --rev <REV>`; when it reports `k > 0`, dispatch one `flow:recon` with that packet's path, the plan's path and two questions: do the listed pairs' contracts still match, and does the behaviour across them? Write its report to `D/findings/seams-changed.md` (the full re-run's to `D/findings/seams-rerun.md`).
4. **The wave.** For each story the findings name, write `D/findings/wave-S<id>.md` and dispatch one fresh `flow:drafter` with the fix-job brief, that file and the story's `start`, and "wave job". Then run step 3 once more.
5. **Settle what the fixers raised** (§2.4), from both rounds' reports.
6. **Re-check** each story whose draft differs from its review copy, as now; coming after the wave, it covers the wave's edits.
7. A finding still open goes to the owner, as now.

The hand-over gains one line: `Seams re-check: <k> shared names changed, <n> findings, <m> wave jobs` (or `Seams re-check: full recon, …` when the contract changed). `STATE.md` gains the in-flight kinds `seams re-check` (a recon, restarted unless its findings file shows it reported) and `wave fix` (a fix job, dispatched again fresh with its wave findings file and "some of these may already be fixed").

### 2.3 Fixers and the re-check

- **A fix job fixes what its fixes make false in its own draft** (`agents/drafter.md`). Before reporting, the fixer greps its draft for each name whose code or behaviour it changed, and brings the uses along: tests' expected values, `Fails without:` lines, docs, counts and callers. A use in another story's draft is the seams re-check's.
- **A wave job** (the brief says so) changes no name or behaviour another story uses: such a fix goes in its report, unapplied, with why.
- **The re-check follows uses** (`agents/plan-reviewer.md`, `agents/risk-reviewer.md`). Besides the diff, the reviewer greps the draft for the names the changed spans define or now call differently, and checks each use outside the diff still holds (a test's expected value, a doc line, a count). It reviews nothing else outside the diff.

### 2.4 The manager's findings

In steps 3 and 5 (and the dispatch guide's fixing section):

- A finding states the defect and the result it needs, never the lines to edit or a "nothing else" fence.
- A defect with two sides names both: both directions of a match, both stories of a duplicate.
- A decision between two stories' versions (a duplicate, a clash) names what each story keeps and what each drops, and goes into both stories' findings files in the same round.
- Each finding a fixer left unapplied with its reason, and each clash a fixer reports, is settled: closed with the reason written in the findings file, given to the wave, or taken to the owner. None is left unanswered.

Step 2's rule widens: when an amendment changes a name, or what a named thing does, that other drafts use, each affected draft gets a fix job.

## 3. Components

| File | Change |
|---|---|
| `flow/lib/plan_packet.py` | `--changed`: the snapshot diff, the attribution, the two kinds of row, the count |
| `flow/lib/plan_packet_test.py` | §4's tests |
| `flow/plugin/skills/draft/SKILL.md` | step 2's rule; steps 3 and 5's finding rules; step 5's order; `STATE.md`'s kinds; the hand-over line |
| `flow/plugin/agents/drafter.md` | the fix-job rule; the wave rule |
| `flow/plugin/agents/plan-reviewer.md`, `flow/plugin/agents/risk-reviewer.md` | the re-check follows uses |
| `flow/plugin/docs/dispatch.md` | the fixing section: the seams re-check, the wave, the finding rules |
| `flow/README.md` | the `packet` row names `--changed` |
| `flow/PROVENANCE.md` | dated entries under drafter, plan-reviewer, risk-reviewer and recon; no pin changes |

`loom flow packet` passes its flags to the script (`internal/cli/flow.go`, `DisableFlagParsing`), so Go doesn't change.

## 4. Testing

- `plan_packet_test.py`, on a synthetic plan of three stories with drafts and review snapshots:
  - a body edit in a provider's definition lists the stories that use the name;
  - a changed consumer line naming another story's definition is listed, a use in a test file included;
  - a deletion inside a definition is attributed to it;
  - a change that touches no shared name gives `0 shared names changed` and `(none)`;
  - unchanged drafts give 0;
  - a missing snapshot fails with exit 1;
  - a plan out of date with its drafts fails.
- `bash flow/lib/test.sh` (the suite and the agent lint).

## 5. Measure

The provenance entry is wrong if the next retro's transcript trace finds planning's own fixes introducing defects at or above half of the 2026-11-13 rate: 11 in 152,028 plan lines is 0.07 per 1,000, so 0.035. The hand-over line gives the retro each draft's re-check counts.

## 6. Not covered

- Amended entry lines (a Verify, a bless command: rb S4): the join writes them, so no draft snapshot shows them.
- Amendments at run time (run2 S1): the run engine's.
- Fixes in steps 2 and 3: they come before step 4, whose full recon and review see them.
