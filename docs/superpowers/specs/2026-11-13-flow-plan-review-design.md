# Flow: a plan review in `/flow:draft` that pays for itself

Status: design approved section by section 2026-11-13; this file awaits the owner's review.

No stage reviews a story's plan code before the run. Premise-check verifies the plan's quotes of the tree, and the seams recon checks names and behaviour between stories, but nearly every plan-code defect that reaches the run sits inside one story. The run's Opus reviewer is the first to see it, and each defect then costs a manager question, a fix round, a re-review and a gate run. This spec adds one review pass to `/flow:draft`: Sonnet reads every story, escalates the tasks whose code invents risky logic to Opus, and a narrow re-check confirms the fixes. A deep story whose exact committed text was reviewed clean is vouched, so the run reviews it with the cheaper `flow:story-reviewer` that the engine already supports.

## 1. Problem

Evidence: the 7 `/flow:run` engine runs of 2026-11-11 to 2026-11-13 with costs in their records, in two client repos, 75 stories and 152,028 plan lines. Design stage $410.10 ($2.70 per 1,000 plan lines), run $374.40 ($2.46 per 1,000).

- **Plan-code defects drive the run's fix rounds.** The 53 stories with a "Reviews of Story N found problems in the plan's own code" question took 65 fix rounds and $74.70 in fix and re-review, $1.41 a story. The 22 stories without one took 4 fix rounds and $1.60. Escaped defects cost the runs about $0.50 per 1,000 plan lines, about $0.80 a defect, plus a share of the managers' $37.80 and the wall time of each extra round and gate run.
- **The rate didn't move with rule changes.** About 95 defects, 0.35 per 1,000 plan lines, against the 2026-11-11 retro's 0.37. By cause (overlapping): a spec or skeleton flaw copied faithfully (~19), unchecked `Fails without:` and count claims (~16), behaviour across stories (~13), invented concurrency, atomicity, ordering or bounds logic (~12), copying instead of reuse (~12), not reading code or dropping what it implied (~16), planning's own fix jobs (~11). Items 1 to 5 of the same analysis (committed c28cdcbe..0578a74b) address reuse, package rules, waves, cross-story premises and two lints; nothing reviews a story's logic or its tests.
- **The first review is the expensive one.** A run's first review of a story by `flow:code-quality-reviewer` averaged $1.10; a re-review by `flow:story-reviewer` averaged $0.56. The engine already gives a story `check-plan` marks `review: vouched` the story-reviewer for its first review (`internal/flowrun/engine.go:1025`), but `/flow:draft` writes no `**Plan review:**` line, so no story is ever vouched.
- **The old review cost too much.** A client repo's earlier design stage ran a per-story plan review loop that cost $4.0 to $4.8 per 1,000 plan lines, about as much as the run, and the 2026-10-15 retro found run reviews catching defects in plan code it had passed in 3 of 8 runs (`~/.claude/flow/docs/specs/2026-10-17-generic-agent-flow-part-2-design.md` §2.2). That is why the dispatch guide says "The design stage reviews no story".

## 2. Decisions

- **The review pays for itself** (owner, 2026-11-13). It must cost about $1 a story or less: one pass, never a review loop. Rejected: an Opus review of every deep story (catches more, costs more than it saves); tests only (covers only the ~16 unchecked-claim defects).
- **Sonnet triages, Opus reviews what it flags** (owner, 2026-11-13). Every story gets the Sonnet pass, which escalates tasks whose code invents logic in risky areas. Rejected: the skeleton marking risky stories (decided before any code exists); landmine markers alone (narrow: they would have missed most of the escaped logic defects, which held none).
- **A reviewed deep story is vouched** (owner, 2026-11-13). Vouching is what makes the review pay: about $0.30 to $0.50 a story at the run's first review, since a vouched first review reads more than a re-review's fix range. The engine's safety valves stay: full review on DONE_WITH_CONCERNS or a landmine marker in lines no plan block covers, the story-reviewer's **Plan** lines, and the final whole-implementation review. Rejected: vouching none (break-even at best); vouching only stories Opus reviewed whole (few qualify).
- **Fixed stories get one narrow re-check** (owner, 2026-11-13). A fresh reviewer reads only the diff since the review and the findings; anything still open un-vouches the story. Rejected: un-vouching every fixed story (loses about half the saving); trusting the fix job (the pattern behind ~11 escaped defects).
- **Vouching is mechanical.** A deep story is vouched only when the draft text it lands with is byte-identical to a text reviewed clean, decided by a script from snapshot files, so a late change un-vouches it and a resumed session gets the same answer.
- **The pins** (owner, 2026-11-13): `flow:plan-reviewer` Sonnet @ high, `flow:risk-reviewer` Opus @ high.

## 3. Scope

In: the plan review step in `/flow:draft` (steps 4 to 6), two agents, the snapshot files, `loom flow plan-review`, a `full:` list in the `**Plan review:**` line, and the docs.

Out: any change to the run engine (it already reads `review: vouched`); a review of the skeleton or spec before drafting; a per-repo switch to turn the review off (added only if the first measurements in §8 say small plans don't earn it back).

## 4. Behaviour

### 4.1 Where it runs

Step 4 becomes **Seams and plan review**. After step 3's last check, the manager dispatches in one batch the seams recon (unchanged) and one `flow:plan-reviewer` per story, all against the same join and `<REV>`. As a plan reviewer's report arrives with `ESCALATE` lines, the manager dispatches one `flow:risk-reviewer` for that story. Each report is written on arrival to `D/findings/review-S<id>.md` (the risk reviewer's appended).

Step 5 bundles each story's open findings (premise-check, seams, review) into one fix job, as today, so the review adds no fix wave. After the fix jobs and step 3's re-run, step 5 runs the re-check (§4.4). Step 6 writes the `**Plan review:**` line (§4.6) beside the Design stage line, joins, checks and commits.

A `SPEC` finding is the manager's, as a DESIGN verdict is today: settled from the spec, or by the owner with the answer written into the spec and committed at once. A finding the manager rejects as wrong is recorded with its reason in the story's review file and doesn't block the story. When step 5 re-runs step 4 because a fix changed the contract, only the seams recon runs again: the re-check covers what the fixes changed.

### 4.2 `flow:plan-reviewer` (Sonnet @ high, read-only)

One per story. The brief gives the plan's path, the story's id and plan line range (`start` and `lines` from `D/join.json`), the spec's path, `<REV>`, and the drafter's profile block. The names contract sits above the plan's `## Stories`.

It reads the story's plan lines against the tree and checks:

1. **Tests.** Each test fails when the code it covers is wrong. Each `Fails without:` line names a change the test catches, reasoned through, not assumed. Count claims ("9 tests", "41 in total") match. The story's **Verify** command runs the tests it names (a feature gate, a filter that matches nothing).
2. **Reuse.** Code the story writes that the tree or an earlier story already has: a helper, constant, error mapping, fixture or test harness.
3. **The repo's rules.** Every CLAUDE.md on the path to the story's **Files** directories, opened with the Read tool, and the profile's landmine notes.
4. **Triage.** For each task whose code invents logic for store writes or id allocation, transactions, concurrency (locks, tasks, channels, ordering), calls to outside services (timeouts, retries, limits), bounds (caps, overflow, pagination) or auth and tenancy, one `ESCALATE` line. A task that only calls existing code for these isn't escalated.

Out of scope: names and behaviour between stories (the seams recon's), quotes of the tree (premise-check's), and design the spec settles. When the plan's code follows the spec or the story's entry faithfully and that is what's wrong, it reports a `SPEC` line.

Report, one line each, then a coverage line naming the plan lines read and the CLAUDE.md files opened:

- `FINDING <Critical|Important|Minor> plan:<n>: <defect>; <fix direction>`
- `ESCALATE Task <x.y> plan:<a>-<b>: <the risk, in one clause>`
- `SPEC plan:<n>: <what the spec or entry mandates, and why it breaks>`

### 4.3 `flow:risk-reviewer` (Opus @ high, read-only)

One per story with `ESCALATE` lines. The brief is the plan reviewer's, plus those lines. It reviews only the escalated tasks: does the logic hold under concurrency, failure, retries, partial writes and limits; does a test exercise the risky path and fail when it is wrong. It reports `FINDING` and `SPEC` lines in §4.2's format and a coverage line naming the tasks it read.

### 4.4 The re-check

After step 5's fix jobs and step 3's re-run, every story whose draft differs from its `D/reviewing/` snapshot (`cmp`) gets one re-check, whatever changed it: a review fix, a seams fix or a COSMETIC fix. The re-checker is a fresh `flow:plan-reviewer`, or a fresh `flow:risk-reviewer` when a risk reviewer's finding was among those fixed (both when both). Its brief holds `diff -u D/reviewing/S<id>.md D/rechecking/S<id>.md`, the story's review findings and the draft's path. It reports `FIXED <finding>` or `OPEN <finding>: <why>` for each Critical and Important finding, and `FINDING` lines for anything new in the changed spans. Its report is appended to `D/findings/review-S<id>.md`. There is no second round: an `OPEN` or a new Critical or Important finding leaves the story unvouched, and the run reviews it in full.

### 4.5 Snapshots and vouching

Snapshot directories under `D`, written by the manager with the commands the skill gives:

| When | Command |
|---|---|
| a story's review is dispatched | `mkdir -p D/reviewing && cp D/S<id>.md D/reviewing/S<id>.md` |
| its review is clean: no Critical, Important or `SPEC` finding still open, and its risk review (if any) clean | `mkdir -p D/reviewed && cp D/reviewing/S<id>.md D/reviewed/S<id>.md` |
| its re-check is dispatched | `mkdir -p D/rechecking && cp D/S<id>.md D/rechecking/S<id>.md` |
| its re-check is clean | `cp D/rechecking/S<id>.md D/reviewed/S<id>.md` |

A story with only Minor findings is clean; its Minors join its fix job, and the fix then needs a re-check to stay vouched. The join reads only `D/S*.md` at the top level, so the snapshot directories are no drafts to it.

### 4.6 `loom flow plan-review <plan>` and the `**Plan review:**` line

A new script, `flow/lib/plan_review.py`, registered in `internal/cli/flow.go`'s `flowTools`. It finds `D` with `plan_join.drafts_dir`, reads the stories and their tiers with `check_plan`'s parser, and prints one line:

`**Plan review:** /flow:draft plan review; deep: 1, 6; light: 2, 3; full: 4`

- `deep:` lists each deep story whose `D/reviewed/S<id>.md` is byte-identical to `D/S<id>.md`: vouched.
- `light:` lists every light story. A light story is never vouched; the run always reviews it in full.
- `full:` lists every other deep story.

An empty list is left out. It exits non-zero, printing nothing on stdout, when `D` or `join.json` is missing or a story has no draft.

`check-plan` accepts the `full:` list. Every story sits in exactly one of `deep:`, `light:` and `full:`; `full:` holds only deep stories; an id that is no story fails, as today. `vouched` stays "listed under `deep:`". A line without tiers, and a line with only `deep:` and `light:`, keep today's meaning.

The manager writes the line into the skeleton's head directly above `## Stories`, beside the Design stage line, in step 6, then joins and runs `check-plan` once more before the commit.

### 4.7 State, resume and cost

`STATE.md` gains the phase `reviewing` and a **Plan review** column in the story table: pending, clean, findings, re-check, vouched or full. A resumed `/flow:draft` re-dispatches the review of a story with a `D/reviewing/` snapshot and no `D/findings/review-S<id>.md`, and a re-check with a `D/rechecking/` snapshot and no re-check verdict in that file. `loom flow plan-review` reads only files, so a resume never changes which stories are vouched.

The reviewers run inside the `/flow:draft` session, so the Design stage line includes their cost. The hand-over states the review's own cost: the plan-reviewer and risk-reviewer rows of `loom flow costs session`.

## 5. Failure handling

- **A review report with no coverage line, or none of the expected line forms**, goes to a fresh reviewer with the same brief; the story stays unvouched until a report parses.
- **A reviewer that goes quiet** is replaced by a fresh one with the same brief, as a quiet drafter is; never messaged.
- **A fix that a later step changes again** after its re-check makes the draft differ from `D/reviewed/`, so the story lands `full:`. Nothing is re-checked twice.
- **A story the review never reached** (a session that died mid-step, the owner stopping early) has no `D/reviewed/` file and lands `full:`: fail closed.
- **A `SPEC` finding the owner hasn't answered** blocks the commit, as an open DESIGN verdict does.
- **`loom flow plan-review` failing** stops step 6 with its message; the manager doesn't write the line by hand.

## 6. Components

| File | Change |
|---|---|
| `flow/plugin/agents/plan-reviewer.md` (new) | §4.2, and the re-check job of §4.4; Sonnet @ high; Bash, Read, SendMessage |
| `flow/plugin/agents/risk-reviewer.md` (new) | §4.3, and the re-check job of §4.4; Opus @ high; Bash, Read, SendMessage |
| `flow/lib/plan_review.py` (new) | §4.6's line from the snapshots |
| `flow/lib/check_plan.py` | the `full:` list: `TIER_RE`, `review_tiers`, `tier_problems` |
| `internal/cli/flow.go` | a `plan-review` row in `flowTools` |
| `flow/plugin/skills/draft/SKILL.md` | step 4's review dispatch and escalation; step 5's bundling, re-check and snapshots; step 6's line; the `reviewing` phase and the Plan review column; the intro's "the run's full review checks the story again" |
| `flow/plugin/agents/drafter.md` | its fix-job text, where it says the run's full review checks the story again |
| `flow/plugin/docs/dispatch.md` | "The design stage reviews no story" replaced by the plan review; the agent table's rows for the two reviewers; the review sequence's vouched stories |
| `flow/README.md` | `plan-review` in the tool table; `check-plan`'s `full:` list |
| `flow/PROVENANCE.md` | entries for the two new agents with their pins, on the owner's instruction, with §1's evidence and §8's "Wrong if" |

Tests that enumerate the flow tools or the plugin's agents (the CLI's flow command tests, the loom context's flag check, the agent lint) are found and updated while planning.

## 7. Testing

`bash flow/lib/test.sh` runs the Python tests and the agent lint; `CGO_ENABLED=0 go test ./internal/cli/` the Go row.

- `plan_review_test.py`: a deep story with an identical `D/reviewed/` copy is listed under `deep:`; a one-byte difference, or no copy, lists it under `full:`; a light story is under `light:` whatever its snapshots; empty lists are left out; a missing `D`, `join.json` or draft exits non-zero with nothing on stdout; the printed line passes `check-plan` on the joined plan, and `check-plan --json` marks exactly the `deep:` ids vouched.
- `check_plan_test.py`: a `full:` deep story passes and isn't vouched; a light story under `full:` fails; a story under `full:` and `deep:` fails; an unknown id under `full:` fails; lines without tiers and with only `deep:`/`light:` keep their results.
- The two agents and the skill's text: the agent lint (`lib/check-agents.sh`) and a read-through against §4.

## 8. Measures

The provenance entries' "Wrong if", for the next retro, over the next 3 plans drafted with the review, against §1's 7 runs:

| Measure | Baseline | Wrong if |
|---|---|---|
| Plan-code questions per 1,000 plan lines | 0.35 | not below 0.20 |
| Drafting plus run, per 1,000 plan lines (logged USD, `loom flow costs records`' Plan code table) | $5.14 | higher |
| The review's own cost (plan-reviewer and risk-reviewer rows) | none | above $1.20 a story on average |

The retro first checks which plugin copy each run's sessions used: a session keeps the copy it launched with, and the deployed loom lags main.

## 9. Known limits and later work

- The review sees plan text and the tree, not built code: a defect that shows only when the code compiles or runs still reaches the run, as the old review's misses did.
- The triage is Sonnet's judgment. An unescalated risky task gets no Opus look before the run, and a vouched story's first run review skips its plan code; the safety valves in §2 are what remain.
- Over-escalation raises the cost toward the old review's. §8's cost measure catches it; the cure is a tighter triage brief, not a cap.
- Light stories are reviewed but never vouched, so their review earns back only fix rounds.
- It reaches runs only after the deployed loom is rebuilt and sessions relaunch.
