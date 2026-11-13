# Dispatching the flow's agents

The definitions in the flow plugin's `agents/` (loom's `flow/plugin/agents/`)
encode what an *agent* must do.
This guide covers the other half: what the **controller** (the main session,
or loom's run engine) must do. Nothing enforces any of it; every
failure below is silent. The history and measurements behind these rules are
in claude-flow's archived `docs/evidence.md`, which no agent loads.

## Offering the execution choice

`superpowers:writing-plans` ends by offering two ways to execute the plan:
**Subagent-Driven** (a fresh subagent per task, reviewed between tasks) and
**Inline Execution**. In a repo with a flow profile whose CLAUDE.md defines no
supervised option of its own, it asks for the execution option before it
writes the plan, and offers a third (loom's context says so in every session):

**3. Supervised.** A skeleton now; `/clear`, then `/flow:draft <plan>`;
`/clear`, then `/flow:run <plan>`. `flow:drafter`s write the stories from the
skeleton ([the skeleton](#the-skeleton)), then loom's run engine runs the plan
story by story ([the run engine](#the-run-engine-flowrun)). It dispatches a
developer per story, reviews each story and the whole, and asks you what the
spec doesn't settle.

A plan for it names its spec in a `**Spec:**` (or `**Design:**`) line before
the first story, and groups its tasks into `## Story` sections. Each story
opens with four lines before its first `###` task, and may add a fifth:

    - **Developer:** junior-dev | intermediate-dev (senior-dev only as a last resort)
    - **Files:** `every/file.py`, `the/story/creates-or-edits.ts`
    - **Interfaces:** what it exposes to later stories and uses from earlier ones, or none
    - **Verify:** `the command that proves the story done`
    - **Review:** deep | light, either optionally followed by a reason in parentheses

A story without a **Review** line is deep. Each opening line is one physical
line, because `loom flow check-plan` reads only a field's first line, and the plan
holds no `###` heading outside a `## Story` section.

`loom flow check-plan <plan>` checks that shape,
confirms the spec exists, lists the pairs of stories that may run at once
(disjoint files, at most one exclusive build), fails a light story whose code
holds a landmine marker its Review reason doesn't name, and prints each story's
tier, whether a plan review vouches for it (`review: vouched` or `review: full`)
and the landmine markers its code holds. The markers and the exclusive builds
come from the repo's profile.
`loom flow premises <plan>` checks what the plan
quotes of the tree: the files its tasks modify and the lines they name, every
"current" block a step replaces, and the items a step adds beside existing ones.
It prints only what fails, and `flow:premise-check` (Haiku) settles each flag by
reading the flagged plan lines and the code they name. What no script checks
goes two ways: the contracts between stories and the files outside the plan
that must change with it go to the design stage's
[seams check](#the-seams-check), and behaviour the plan's code gets wrong goes
to the per-story and final reviews. For the developer tiers, see [Picking a
developer](#picking-a-developer).

The tiers spend each model where it pays. The manager holds the judgment and
talks to the owner, but sits idle while the plan runs. Loom's run engine, a
Go process, routes the work and spends no tokens. The developers (Haiku or
Sonnet, by story) spend most of the tokens, and the reviewers (Opus) judge.
Questions climb a ladder — developer, manager, owner — and each rung costs
more than the one below, which is why the engine has `loom flow premises`
check the plan's code against the tree before the first story.

**The design stage and the run each start from a fresh session.** Every
manager turn re-reads the whole conversation. The drafting manager's turns are
its agents' reports, and the run manager's are the engine's questions, so a
design conversation carried into either is paid for again on
each of them. A quiet spell longer than the cache lifetime (one hour on a
subscription, five minutes on usage credits) makes the next turn write that
whole context again. The design session therefore ends at a committed spec and
skeleton, and the owner clears twice: once before `/flow:draft`, once before
`/flow:run`. When the option is chosen, the design session:

1. Moves every decision the conversation made into the spec, since after the
   clear the spec is the only record of the design. That includes the limits
   a run would otherwise stop to ask about: for every outside service the
   feature calls, a rate budget, a timeout and what happens when it fails;
   and for every number an agent or a user supplies, a bound. A limit the
   spec never set holds a story up while the manager asks the owner.
2. Writes the plan skeleton ([the skeleton](#the-skeleton)): the header, the
   file structure, the names contract and the story list, and none of the
   stories' code. A plan is drafted from its spec, never generated from a
   prototype of the code. It runs `loom flow check-plan --skeleton <plan>`
   until it passes: the names contract's rules and the story list
   ([the skeleton](#the-skeleton)). When the spec splits into an epic, it
   writes only the next part's skeleton ([epics](#epics)).
3. Commits the spec and the skeleton by pathspec and hands over: the owner
   types `/clear`, then `/flow:draft <plan>`. The design session doesn't
   draft, check or review.

`/flow:draft` runs the rest of the design stage from a fresh session. It
checks the skeleton, settles the names contract with the `flow:drafter`s in a
question round and has them draft ([drafting the stories](#drafting-the-stories)),
joins and checks the drafts, has one `flow:recon` check the seams between the
stories ([the seams check](#the-seams-check)), has fresh drafters fix what the
checks found ([fixing the plan](#fixing-the-plan)), writes the
`**Design stage:**` line, commits the plan by pathspec, and ends with "run
`/clear`, then `/flow:run <plan>`". It asks the owner only what the spec
and the names contract don't settle, writes each answer into the spec and
commits it at once, and reads the agents' reports, never the drafts.

`STATE.md`, in the plan's drafts directory, is its memory: a **Phase** line
(questions, drafting, checking, seams, fixing or committed) saying what is done and what
comes next; a line with the start time, `REV` and the session ids; the **Design
stage** slot, filled when the line is written; one row per story (tier, effort,
drafter, deviations settled, findings, fixer); the findings files; the open
questions; and the agents in flight, with their ids and jobs. A fresh
`/flow:draft` on the same plan resumes from it. After a `/clear` the previous
session's agents keep running, and their reports go to the cleared
conversation. So before it dispatches again an agent that `STATE.md` lists in
flight, a resume checks `ps` and the files the agent writes, and looks for its
report in its transcript. An agent that finished after the `/clear` handed its
report back through a `SubagentHandback` call, and the call's input stays in
`~/.claude/projects/<project>/<old session id>/subagents/agent-<id>.jsonl`
(`ls -d ~/.claude/projects/*/<old session id>` finds the project), so the
resume reads it there instead of paying for the job again; only an agent with
no hand-back is dispatched again. A drafter of the question round has reported
when its questions file exists, and only one without a file is dispatched
again; drafting that starts after a `/clear` dispatches its drafters fresh. A drafter on a drafting job is dispatched
again, fresh, to finish its draft; one on a fix job
gets its fix-job brief again, with the findings and "some of these may already
be fixed; fix what remains". A `flow:recon` or
`flow:premise-check` restarts unless its findings file shows it reported. This
prints the last hand-back in a transcript, or says there is none:

```bash
python3 -c 'import json,sys; m=[b["input"]["message"] for l in open(sys.argv[1]) for b in (json.loads(l).get("message") or {}).get("content") or [] if isinstance(b, dict) and b.get("type") == "tool_use" and b.get("name") == "SubagentHandback"]; print(m[-1] if m else "no hand-back: the agent had not finished")' <transcript>
```

`/flow:run` also runs a plan written by hand, as every plan was before
`/flow:draft`, once `loom flow check-plan` passes it. `loom flow run start`
checks the repo with `loom flow conf doctor` and the plan with
`loom flow check-plan`, and the engine ([the run engine](#the-run-engine-flowrun))
writes the run's record to `~/.loom/flow/records/` ([`records.md`](records.md)
gives the format), with a copy in the repo when the profile asks for one. The
record's header copies the plan's `**Design stage:**` line, when it has one,
so `/flow:retro` reads the design stage and its run together. Facts only: the
owner starts retros with `/flow:retro`.

### The skeleton

The design session writes the skeleton beside the spec: what takes judgment,
and none of the stories' code. It holds, in order:

- the header: writing-plans' title, Goal, Architecture and Tech Stack, the
  `**Spec:**` line, and, when stories may overlap under `/flow:run`, a
  `**Parallel:**` line ([parallel pairs](#parallel-pairs)); for a part of an
  epic, a `**Part:** N of M` line ([epics](#epics)); for a plan over the
  story budget, a `**Size:**` line with its reason;
- the file structure;
- the names contract: every name, type and signature one story defines for
  another, such as functions and their signatures, types and their fields,
  file paths, CLI flags, configuration keys and the messages tests match.
  The design session writes it, and after that only `/flow:draft`'s manager
  edits it. It sits under the exact heading `## Names contract`, above
  `## Stories`;
- the story list, under the exact heading `## Stories`;
- the marker line, which ends it:
  `<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->`

The contract's rules are what lets every drafter write against it at once:

1. **Every shared name is written in full.** A function or method gives
   every parameter's name and type and its return type. A type lists its
   fields with their types, inline (`{ a: T, b: U }`, or a tuple's
   `(T, U)`) or in bullets nested under it, or says `(opaque)` when no other
   story reads its fields. No `..`, `...` or `…` stands for parameters or
   fields.
2. **Every line that defines a name names its owning story:** `Story <id>`
   on the line, on a bullet it is nested under, or on the unindented line
   that introduces its list (`**crate-a** (Story 1)`).
3. **A line may hand its exact form on:** `(Story N proposes)` waives rule 1
   on that line, and the question round asks Story N's drafter for the form.

`loom flow check-plan --skeleton <plan>` checks them, with the head (the
`**Spec:**` line, the headings, the marker) and each entry's lines, and prints
one FAIL line a problem. Its rules are heuristics over backticked spans: a
contract written as an expression draws a false flag, and the cure is to type
it. It also fails a skeleton listing more stories than the profile's
`[size] plan` (15 when unset) unless a `**Size:**` line says why it stays one
plan, and a `**Part:**` line that disagrees with the spec's `## Delivery`
list, or is missing when the spec has one, and a **Drafts after:** line that
names no listed story, its own story, or a circle of stories. Story count predicts a plan's size
well (r = 0.82 over 51 past plans), and plans over 15 stories mostly ran at
$9 to $10 a story against $3 to $4 for the three parts of a split spec.

Each list entry gives the story's id, a paragraph and its five opening lines
(**Developer**, **Files**, **Interfaces**, **Verify**, **Review**), which the
join writes into the story (a draft holds none of them), then a **Reading**
line, which only the drafter reads: the spec sections the story needs, by § number or heading,
never by line number (the spec's lines move while `/flow:draft` writes answers
into it), and the file and recon line ranges it needs. The drafter reads the
skeleton above `## Stories`, its entry and what that line names, and reads
further only for what those lack. Each opening line, in the story list and in
every story, is one physical line, because `loom flow check-plan` reads only a
field's first line. The join parses the list by its grammar, so an entry is a
`-` bullet at column 0 whose bold label is `**Story <id>**`, followed by a
space or the end of the line, never by a colon, inside the bold or after it;
its paragraph and six lines are indented below it:

    ## Stories

    - **Story 1** — <title>.

      <the paragraph: what the story does and why>

      - **Developer:** intermediate-dev
      - **Files:** `lib/a.py`, `lib/a_test.py`
      - **Interfaces:** what it exposes to later stories and uses from earlier ones, or none
      - **Verify:** `the command`
      - **Review:** deep
      - **Reading:** spec §4, §6.2; `lib/b.py:10-80`

An entry may add a **Drafts after:** line, which, like **Reading**, only the
drafter reads: `- **Drafts after:** Story 3, Story 6`. Give it to a story
whose code documents, tests or renders what other stories add (a docs story,
an end-to-end story, a UI story over another story's service): its drafter
drafts once those stories' drafts exist, and writes against their code
rather than their contract lines. Drafted beside its subjects, such a story
guessed at their behaviour: in the runs of 2026-11-10 to 2026-11-12, docs
tables missed the tests later stories added, and a test expected a box
another story always renders.

The skeleton names the code a story reuses, on its **Reading** line, never
code to mirror or copy: entries that said "mirroring `X`" or "copied where its
shape fits" got copies, and the run's reviews asked for them to be shared.

`- **Story 1:** title`, `- **Story 1**: title`, `1. **Story 1**`, an indented
bullet and a heading other than `## Stories` (`### Stories`, `## Story list`)
are not read, and the join fails with "the plan has no ## Stories section" or
"the ## Stories section lists no stories", or, when other entries parse,
"S<id>.md is not in the plan's story list". The skeleton holds no `## Story`
heading, and no `###` heading either: `loom flow check-plan` fails on a `###`
heading above the first story, so the skeleton's sections are `##` or plain
text.

### Drafting the stories

`/flow:draft` dispatches one `flow:drafter` per one or two stories, all in one
parallel batch and without a name. Drafters are fresh agents, never forks: a
fork re-reads its parent's whole context on every request, while a fresh agent
starts small. A drafter runs on Sonnet at high, and `/flow:draft` dispatches
it with `effort: xhigh` for a story whose entry names one of the profile's
`landmines.markers`, as a developer is; a shape only its notes describe, and a
fix job, run at high. Notes sent drafters to xhigh until the 2026-11-11 retro:
those stories cost three times as much to draft and drew more review findings
than their stage mates. The Agent
tool sets a per-call effort only when written instructions name a level, and
the skill is that instruction for drafters.

A drafter's brief holds the plan's and the spec's paths, the drafts directory
D, its story ids and their files, `D/S<id>.md`, and the drafter's profile
block (`loom flow conf show --for drafter`: the prefix, the story gates, the
generated globs, the landmine markers and notes, the idioms and the design
registries). It writes each task's commands from those values and never runs
them, checks its own quotes of the tree with `loom flow premises`, and reports
in at most 15 lines.

Before drafting, it reads, with the Read tool, every CLAUDE.md on the path to
the directories its **Files** lines name: Claude Code loads a directory's
CLAUDE.md when a file under it is Read, never for Bash output, and drafters
read code through Bash (7,366 Bash calls to 220 Reads in 66 drafters of
2026-11-10 to 2026-11-13, of which 52 never loaded a package's CLAUDE.md). It reuses
what the tree has rather than copying it, and a file reuse needs goes in its
report as an entry line that can't hold, which the manager adds to the
entry's **Files** line: the Files line is the design session's estimate, not
a fence. What its draft relies on about another story's code and it couldn't
check, it writes to `D/premises/S<id>.md`, for the seams recon.

The batch drafts in two phases. In the question round, each drafter reads what
its entries need and writes only `D/questions/S<first id>.md`: the names its
stories use from others and those they define for others that the contract
doesn't give exactly (each with its proposed form, a clash with the tree's
names included), its `(Story N proposes)` lines with their forms, and the
entry lines that can't hold, and each story's size: the subtasks it will
need, with a proposed split for a story over 7 (the p90 of 526 past stories),
which the manager accepts or rejects in its settlement. The manager waits for every drafter, settles
everything in one pass (contract, entries, the owner's answers in the spec),
runs the skeleton check, then resumes every drafter with the lines that
changed. Every drafter waits, one that asked nothing included: a name it
defines may change in the settlement. A story with a **Drafts after:** line
is resumed later, once the stories it names have reported their drafts, and
its drafter reads those drafts. A gap that shows only while drafting
takes the old path: the drafter stops and reports NEEDS_CONTEXT, the manager
amends the contract and answers, and once the drafters whose stories use an
amended name have reported, a fresh drafter per affected story fixes its
draft. An amended entry costs nothing: the join writes it. The manager never
sends a running drafter new work by message: a parent's message arrives framed
as another agent's request.

D is `<drafts>/<plan stem>/` under the worktree's top level, where `<drafts>`
is the profile's `paths.drafts`, `.flow/drafts` by default: inside the
worktree, because in auto mode a drafter's Write and Edit need no permission
prompt there. Default mode asks before any edit, there as anywhere.
`loom flow conf drafts <plan stem>` makes D and, when git doesn't ignore it, adds
`/<the path's first directory>/` once to the file
`git rev-parse --git-path info/exclude` names. In a linked worktree that file
is the common directory's, so one line covers every worktree and no tracked
file changes. It refuses a first directory of `.git`, and refuses when that
directory holds tracked files. A profile may point `paths.drafts` at a
directory the repo already ignores.

The drafts are the plan's source until the commit. Every change, a fix
included, is made in a draft, and
`loom flow join <plan>` regenerates everything
after the skeleton's marker line, in story-list order: each story is its
draft's heading, its entry's opening lines, and the rest of its draft (a block
of opening lines a draft copied is dropped, with a note). It writes
`D/join.json`: each story's `start`, `lines`, and `entry`, the plan lines of
its opening lines. Joining twice
gives the same file. The join reads the story list only from above the marker,
doesn't take `STATE.md` for a draft, and fails on a missing or unlisted draft
and on a draft that holds the marker line outside a fence (a draft quotes it
inside one). Nothing edits a joined story in the plan file. The checks cite
plan lines and a fixer works in its draft, so `/flow:draft` converts: draft
line = plan line − the story's `start` in `join.json` + 1. A finding inside a
story's `entry` lines is the entry's, which the manager amends.

### The seams check

What the scripts can't check across stories goes to one `flow:recon`.
`loom flow packet <plan> --seams --rev <REV>`
writes `D/packets/seams.md`: every name one story's added code defines and
another story's code uses, cited to the draft lines that define and use it,
the premises the drafters wrote they couldn't check (`D/premises/S<id>.md`),
then every story's **Files** line. It refuses a plan that isn't the join of
its drafts ("the plan is out of date with its drafts; re-run loom flow join"),
so `/flow:draft` joins after every change before it writes the packet.

The recon reads the packet and the names contract, and answers three questions,
and a fourth when the plan declares pairs.
Do the contracts between stories match: is every name a story uses from
another defined there, with the same type, signature and visibility? Does the
behaviour across them match: where a story's code or test mocks, documents or
asserts another story's behaviour (a status and its envelope, an order of
events, what renders when, what a value means), does that story's code do it,
starting from the packet's unchecked premises? Names alone passed seams in
which a mock answered a bare 401 the other story enveloped, and a test
expected a box another story always renders. Which
files outside every story's **Files** line list or count what the plan adds or
changes? Does each declared pair's second story use anything its first
creates? The packet's Declared pairs section lists the names; files, flags and
behaviour no name shows are the recon's to find. It starts from the profile's `design.registries`, the places in the
repo that list or count what stories add: a generated region and the command
that regenerates it, a route or command table, a test that counts registered
items, a document that states a count. It doesn't re-check quotes of the tree,
which `flow:premise-check` settled, or review a story's code on its own. Its report goes to
`D/findings/seams.md`.

### The plan review

Nothing else reviews a story's own code before the run, and nearly every
plan-code defect that reaches it sits inside one story: in the 7 engine runs
of 2026-11-11 to 2026-11-13, the 53 stories with a plan-code question took 65
fix rounds and $74.70 of fix and re-review, the 22 without one 4 rounds and
$1.60. A per-story review loop once cost about as much as the run it planned,
so this review is one pass that must pay for itself (the owner, 2026-11-13;
loom's `flow/PROVENANCE.md` has the evidence). In the same batch as the seams
recon, one `flow:plan-reviewer` (Sonnet @ high) per story checks its tests and
`Fails without:` lines, copies of code that exists and the repo's rules, and
escalates each task whose code invents logic for store writes, transactions,
concurrency, outside calls, bounds or auth to a `flow:risk-reviewer` (Opus @
high), which reads only those tasks. Either may report a `SPEC` finding: code
that follows a spec or entry that is itself wrong, settled like a DESIGN
verdict. Findings join the story's fix job. After the fixes, a story whose
draft changed since its review gets one re-check of the diff, never a loop.

`/flow:draft` copies each draft to `D/reviewing/` as its review starts and to
`D/reviewed/` once the review or its re-check is clean, and
`loom flow plan-review <plan>` writes the `**Plan review:**` line from those
copies: a deep story is listed under `deep:`, vouched, only when its draft is
byte-identical to its `D/reviewed/` copy; every other deep story goes under
`full:`, and every light story under `light:`. A vouched story's first run
review is `flow:story-reviewer`'s, which reads only what departs from the plan.

### Fixing the plan

1. **Settle what spans stories first.** `/flow:draft` reads the findings,
   never the drafts, and decides every cross-story name, type and seam they
   touch, writing each decision into the names contract. A design question
   the spec doesn't settle goes to the owner, and the answer into the spec,
   committed at once. Opus decides; a `flow:drafter` writes, since Opus writes
   code only as a last resort. Name there, too, each text a later story quotes
   from an earlier one (its "current" block). Every fixer leaves a named text
   unchanged, so fixers working at once can't break a later story's quote.
2. **Fix each story in place.** One fresh `flow:drafter` per story with findings
   edits `D/S<id>.md` and nothing else, so no two agents edit one file. It gets
   its story's findings, as the path of its findings file (the recon's seam
   findings go to the stories they name), the story's `start` from
   `D/join.json`, the contract, the spec and the drafter's profile block. A
   finding that states its fix goes to the drafter as it stands. A finding on
   a story's opening lines goes to its entry, which the manager amends; no
   fixer copies it. A fix job
   runs at the drafter's pinned high, whatever the story's shape: its fixes
   are decided before it starts, and a re-check or the run's full review
   checks the story again (the owner, 2026-10-31; claude-flow's archived `PROVENANCE.md` has the evidence). A
   findings file bundles the story's `flow:premise-check` rows
   still open after the check stage's last run (the check stage runs once more
   after its own fix jobs: one more check, not a second fix round), the seams
   findings that name the story, and its plan review's open findings.
3. **Join and check again:** `loom flow join`, `loom flow check-plan` and
   `loom flow premises`, with `flow:premise-check` on any new flag, and the seams
   check once more when a fix changed the contract. Then a story whose draft
   changed since its plan review gets its one re-check.
4. **One round.** A finding still open after its fix round goes to the owner;
   there is no second automatic round.
5. **Record and commit.** `/flow:draft` prices its sessions with
   `loom flow costs session` unless `STATE.md`'s Design stage slot already holds
   the line, which it reuses (as after a session that died before the commit),
   then writes the `**Design stage:**` line and `loom flow plan-review`'s
   `**Plan review:**` line into the skeleton's head, directly above
   `## Stories`, and the first into the slot. It joins and checks once more, and
   commits the plan by pathspec; each spec answer was committed as it was
   written.

`loom flow check-plan` then marks each story `review: vouched` or `review: full`
from that line.


### Epics

A spec too big for one plan is an epic: a `## Delivery` section names its
parts, each a plan merged to main before the next part is designed, so each
later part is designed and drafted against the code as it landed.

    ## Delivery

    An epic of 3 parts, each a plan merged to main before the next part is designed.

    1. **Part 1: kernel and ops.** Stages 0 and 1: …
    2. **Part 2: markets, trading and ledger.** Stages 2 to 4: …

Each item is `N. **Part N: <title>.** <scope>`, numbered 1 to M in merge
order. The first design session writes the spec with this section and only
part 1's skeleton, with `**Part:** 1 of M` beside its `**Spec:**` line. When a
run ends on part N of M with N below M, the owner merges it and starts part
N+1's design session, on a branch cut from main, which:

1. reads the spec, its Delivery section and the settlements so far;
2. runs a fresh recon at the new main, scoped to part N+1 and to what earlier
   parts landed: the names, files and seams it builds on, and what came out
   other than the spec said;
3. appends `### Part N+1 settlements (YYYY-MM-DD)` under `## Delivery`: the
   recon's findings and the decisions made with the owner, which hold where
   they disagree with earlier sections, and any re-cut of the parts not yet
   started (a merged part stays as it landed);
4. writes the skeleton with `**Part:** N+1 of M`, runs
   `loom flow check-plan --skeleton <plan>` until it passes, and commits the
   spec and the skeleton by pathspec.

A run's record copies the Part line, `loom flow costs design` prices a later
part's design session without earlier parts' sessions, and
`loom flow costs epic <spec>` gives each part's design, drafting and run
cost.
## The repo's profile

Each repo the flow runs in has a profile: `.claude/flow.toml` in the repo,
committed there. Its key is the main checkout's path made into a name
(`loom flow conf key`). `/flow:init` drafts it and `loom flow conf doctor`
checks it. It holds the command prefix, the services
to start, the story and final gates, the patterns of a run that tested
nothing, the exclusive builds, the landmine markers and notes, the review
idioms, the globs of generated files `loom flow conformance` ignores
(`generated`), the state, record-copy and drafts paths, and the design
registries: the places outside a story's own files that list or count what
stories add, which the drafters and the seams check read. Repo rules stay in
the repo's CLAUDE.md, which every subagent loads.

No agent reads the profile. `loom flow run start` runs
`loom flow conf show --for supervisor` once (the role keeps the name of the
controller that first read it), and the engine pastes each role's block into
that role's briefs: a developer gets the prefix, the services (to start, as
written, before the tests that need them), the generated globs (to
regenerate and commit what its change alters, never to edit by hand) and,
on a deep story, the landmine notes; and a reviewer gets the prefix (it runs its check-only commands, such
as the formatter check, inside it), the notes and the idioms. The engine runs
the gates itself: per story `loom flow gates start --story <the story's
commits>`, which expands `{packages}` to the Cargo packages the story changed
and the packages that depend on them, and after the last story
`loom flow gates start --final`. A controller that dispatches `flow:verifier`
gives it the `[verifier]` block: the services and the vacuous patterns.
`/flow:draft` runs `loom flow conf show --for drafter`
once, which the `supervisor` block leaves out, and pastes it into each
drafter's brief: the prefix, the story gates, the generated globs, the
landmine markers and notes, the idioms and the design registries.

## Picking a developer

| Task shape | Developer |
|------------|-----------|
| The plan gives the task's code in full and it lands in one or two files with no wiring between them; or a fully specified procedure | `flow:junior-dev` (Haiku @ xhigh) |
| The plan gives the code, and fitting it into the codebase spans several files — the usual output of brainstorming then writing-plans | `flow:intermediate-dev` (Sonnet @ high) |
| The last resort: a slice an `flow:intermediate-dev` failed twice, the second time with the manager's decision in its brief, or one the manager asks for | `flow:senior-dev` (Opus @ high) |

**Opus writes code only as a last resort.** Sonnet writes every story that
needs fitting, Haiku the fully specified small ones, and Opus designs and
reviews. What a story leaves to judgment is decided before a developer writes
it: in the plan, or by the manager when a developer reports
`BLOCKED` or NEEDS_CONTEXT. A reviewer that isn't the writer's model doesn't
share its blind spots. `flow:senior-dev` stays for the rare case Sonnet can't
handle, so a plan shouldn't assign it: `loom flow check-plan` warns when one does.

All three preload [one skill](#the-developers-share-one-preloaded-skill): the
plan's code is the spec, every deviation is listed, and a decision outside the
developer's lane goes back as a question to whoever dispatched it. A junior's
`BLOCKED` moves the slice to `flow:intermediate-dev`; an intermediate's goes to
the manager for a decision, then to a fresh `flow:intermediate-dev`, and only if that
fails as well to `flow:senior-dev`. Outside a plan, dispatch a developer with a
complete brief; the brief takes the plan's place. The plan's code doubles as
the reviewer's yardstick, which is why cheaper models are safe writing it.

`flow:intermediate-dev` runs at high. Dispatch it with `effort: xhigh` when
`loom flow check-plan` lists landmines for the story: shapes that compile and pass a
naive suite, which the profile's `landmines.markers` names and its notes
explain. A fix round runs at high whatever the story's shape: its brief
carries the decided fixes, and `flow:story-reviewer` re-reviews it (the owner,
2026-10-31). A landmine in lines the plan doesn't cover, and a developer report of
DONE_WITH_CONCERNS, also bring in the combined `flow:code-quality-reviewer` pass
(see [Review sequence](#review-sequence)). A seam
between packages whose design the plan leaves open is no developer's call at
any effort: settle it first. The engine starts such a story's developer job
with `--effort xhigh`. A session dispatching through the Agent tool sets a
per-call effort only when written instructions name a level; per-call
`effort` needs Claude Code 2.1.292 or later, and `CLAUDE_CODE_EFFORT_LEVEL`
overrides it.

## Route to pinned agents, not built-ins

The built-in agent types (`Explore`, `Plan`, `general-purpose`) have no pinned
model: they inherit the **session's**, so from an expensive session an
"Explore" dispatch runs at that session's prices. The pinned roster exists to
divert that spend:

| Work shape | Dispatch |
|------------|----------|
| A quick lookup with one answer: where X is defined, what Y returns, which file handles Z, what the spec says about W, a summary of one file | `flow:glance` |
| Enumeration sweeps, `file:line` inventories, verifying a claim against source, absence claims, live-endpoint probes | `flow:recon` |
| Settling the plan premises `loom flow premises` flags (missing paths, stale "current" blocks, clashing definitions) | `flow:premise-check` |
| Reviewing each story's plan code in `/flow:draft`, and re-checking its fixes | `flow:plan-reviewer`; the tasks it escalates to `flow:risk-reviewer` |
| Writing a plan's stories from its skeleton, and fixing the drafts | `flow:drafter`, from `/flow:draft` |
| One story that `loom flow check-plan` marks `review: vouched`, and every re-review under `supervised` | `flow:story-reviewer` |
| Running the profile's gates (check-only, in the background) | `flow:verifier` |
| Plan-task implementation | a developer: `flow:junior-dev` or `flow:intermediate-dev`; `flow:senior-dev` only as a last resort |
| Review outside the `supervised` option | `flow:spec-reviewer`, then `flow:code-quality-reviewer` |
| A story's docs, after its review and fix rounds | `flow:doc-writer` |
| Design, planning, prod ops, anything judgment-maximal | stays in the main session |

**Never fork a briefed worker.** A `fork` starts with its parent's whole
context, re-reads it on every request, and runs at the parent's model and
effort whatever `model` says, while a fresh agent starts at a few tens of
thousands of tokens. Fork only when the brief would have to be the
conversation itself.

**Read inline only what you need to keep.** Whatever you read stays in your
context and is re-read on every later turn: a 5K-token file read at turn 50
of a 180-turn session is re-read 130 times, about 650K cached tokens or 13
cents on Opus, against about a cent for a `flow:glance` that hands back three
lines. So when you would read files only to answer a question, send
`flow:glance`; the earlier in a long session, the stronger the case. Read inline
when you need the text itself (you are about to edit it) or when the answer
is one short command's output, such as a `git log` or a single grep. Larger
jobs — sweeps, inventories, anything that must be complete — go to `flow:recon`,
never to Explore, which runs at the session's model. Corollary: don't ack an
agent that has already delivered and gone idle, and keep relay turns terse —
every coordination turn is the expensive part of the dispatch.

## Dispatching during a brainstorm

Agents get pulled into design conversations without any skill asking for it:
the controller reaches for one mid-discussion. Two things make that different
from every other dispatch in this guide.

**There is no gate upstream of a spec.** `flow:spec-reviewer` checks a diff against
its task, `flow:code-quality-reviewer` checks craftsmanship, the verifier checks
the build — all of them verify work *against the spec*, so none can report
that the spec was wrong. Cheap tiers are safe downstream because something
checks them; at design time nothing does, and an error propagates into the
plan and every task cut from it. That asymmetry is why `flow:recon` runs on Sonnet
at **medium** rather than low.

Safe to reach for mid-design:

- `flow:recon` — the workhorse. When a discussion turns on "does this API actually
  behave that way?", dispatch one rather than raising the session's effort.
- `flow:glance` — for lookups. A premise the design rests on goes to `flow:recon`
  instead: `flow:glance` runs on Haiku, and nothing downstream would catch its
  mistake.
- `flow:verifier` — executing gate commands doesn't depend on the conversation.

**Do not dispatch a developer during a brainstorm.** The
developers assume a plan task and are told to commit (format, follow the
commit convention, stage what they touched), and they share the controller's
git index, so an exploratory "could this even work?" spike can land a commit
in the middle of a design conversation. If a design question turns on how
existing code behaves, dispatch `flow:recon`. `flow:spec-reviewer` and
`flow:code-quality-reviewer` don't apply: both need a real `BASE_SHA..HEAD_SHA`,
which doesn't exist yet.

## Dispatch by plan reference, not inlined text

Point the agent at a **line range in the plan file** (`docs/plans/foo.md` lines
120–184), not a retyped summary of the task. Retyping is where scope silently
drifts: the controller paraphrases, the paraphrase loses a constraint, and
`flow:spec-reviewer` then verifies the implementation against the *paraphrase* it
was also handed — so the two agents agree with each other and both are wrong.
A line range makes the plan the single source of truth for developer and
reviewer alike.

Developers put plan code in place by line range too. The `flow:habits` skill
gives them a short Python script that copies a block from the plan file into
the source file, so the code never passes through the model's output, and it
lands exactly as written, which is what `loom flow conformance` matches.

## The git index is shared

Subagents run against the controller's working tree and its **one** git index.
Consequences:

- A subagent's `git commit` commits everything staged, including whatever the
  controller or a sibling agent staged. The agent prompts forbid `git add -A`
  for this reason; the controller must likewise not leave unrelated files
  staged across a dispatch.
- Run two developers at once in one worktree only when their files are
  disjoint and at most one of them runs an exclusive build. Pathspec commits
  keep their commits apart, but build directories and the machine's RAM are
  shared, and the out-of-memory killer stops concurrent heavy builds.
- If a subagent commits work that wasn't its own, the controller recovers with
  `git reset --soft HEAD~1` and stages again deliberately — the objects are
  intact. Developers never do this themselves.
- The index can grow **ghost entries**: newly created paths (editor
  atomic-write temp files, occasionally the real target path itself) get
  staged as the empty blob `e69de29…` while the content sits only in the
  worktree. `git status` shows one as an ordinary added file; the truth check
  is `git diff-index --cached --name-status HEAD`. Run it before
  every commit, and commit by pathspec (`git commit -m … -- <files>`), never a
  bare `git commit` after a directory-scoped `git add`: a directory add is the
  shape that sweeps a ghost into the tree as a 0-byte file. For NEW files the
  name-status check is not enough, because a ghost shows as a
  legitimate-looking `A` while holding the empty blob. After staging your own
  new files, run `git diff --cached --numstat` and confirm every added path
  reports its real line count; an entry showing `0	0` for a file you know has
  content is a ghost that a fresh `git add` of the explicit path fixes.
- A dev shell that evaluates a flake can mark the worktree's untracked files
  intent-to-add in the shared index, so logs belong under the state directory,
  and the `diff-index` check above runs before a pathspec commit.
- **Never rewrite history from a subagent.** The controller also commits in
  this worktree, and it may commit *your* in-tree edits. A clean tree with
  your edits already in HEAD means the controller committed them: report the
  sha; don't `git reset --soft` and `--amend` to "reclaim" them. That rewrite
  orphans shas that reviewers were dispatched against. The `flow:habits`
  skill all three developers preload forbids `reset`, `--amend`, `rebase`,
  `stash`, `clean` and `checkout`/`restore`, and carries the index checks
  above; `flow/lib/check-agents.sh` fails an editing agent whose body and preloaded
  skills drop either.

## Reports come back through the hand-back

When a subagent finishes, Claude Code delivers its final report to whoever
dispatched it through a hand-back call the harness requires
(`SubagentHandback`). Long reports arrive whole that way, so they come back
inline, and `SendMessage` is for answers. A report sent with `SendMessage` as well arrives twice, in the
dispatcher's context, the most expensive one.

Questions travel the same way. A subagent that ends its turn without handing
back is told at once to hand back, and the call ends its run. So an agent
never asks and waits. It stops and reports NEEDS_CONTEXT with the question,
and the answer, sent with `SendMessage` to the id on its report, resumes it
with its context; its next report goes to the same dispatcher. An agent with a
child still running is not told to hand back, which is how a dispatcher
waits for its own agents. A child's completion notice can still wake a
dispatcher that has already handed back; it must then stop without reporting
again.

### A named agent is a teammate

Dispatched with a `name`, an agent joins the session as a teammate, not a
subagent, and its own dispatches can't hand back to it: their reports fail
with "the agent that spawned you is no longer running". Dispatched without a
name, an agent's own dispatches report back to it. So dispatch an agent that
dispatches others without a name, and message it by the agent id the launch
returns. A teammate can't name its own dispatches either.

If a dispatch goes quiet, suspect the agent before the channel: check `ps`
and `git log`.

## Tool allowlists fail open, then silently narrow

A `tools:` frontmatter entry naming a tool the harness does not provide is
**dropped without error**: no warning at load, no failure at dispatch. `Grep`
and `Glob` are not tools in this harness (search runs through `Bash`), so a
definition listing them runs without them, and one that leaves out
`SendMessage` can't answer.

Verify a name before adding it: `ToolSearch` with `select:<Name>` returns the
schema for a tool that exists and nothing for one that doesn't. Don't copy
`tools:` lines from plugin agent definitions written against a different
harness build.

`flow/lib/check-agents.sh` (run by `flow/lib/test.sh`) fails on any tool name outside
its verified allowlist, a missing `SendMessage`, an unpinned or invalid
`model:` or `effort:`, a `name:` that doesn't match the filename, a preloaded
skill that is missing or can't be preloaded, a definition without an explicit
`experimental.cacheTtl`, an editing agent without the shared-index rules
(`diff-index` and `--amend`, read from the body together with its preloaded
skills), the git-guard hook or a `maxTurns` cap, a description over 400
characters, a skill no agent preloads that doesn't set
`disable-model-invocation: true` (an owner's command the model could start),
and any word from `lib/banned-tokens.txt`. The `flow:drafter`, whose guard runs
with `--drafter` and which never commits, needs no shared-index rules, but its
guard must run on a `Bash|Write|Edit` matcher. Adding a genuinely new
tool means verifying it with `ToolSearch select:` and then extending
`VALID_TOOLS` in that script: a deliberate, reviewable act. No agent may list
`Agent`: a subagent that lists it can spawn subagents of its own, and a
subagent definition ignores an `Agent(type)` list, so any agent holding
`Agent` could spawn any type.

## Review sequence

Under the `supervised` option the run engine runs the reviews: one per story,
then one whole-implementation anti-seam pass by `flow:code-quality-reviewer` after
the last. `loom flow conformance` first maps the story's diff onto the plan:
which blocks landed as written, which didn't, and which changed lines no block
covers. `loom flow check-plan` prints a `review:` column per story. For a `vouched`
story, `flow:story-reviewer` (Opus @ high) reads just those, the developer's
deviations and tests, and the seams. For a `full` story (a light story, a deep story the plan review
listed under `full:`, or any story of a plan without a `**Plan review:**` line), `flow:code-quality-reviewer`
does a combined pass: the plan's code, quality and the seam. A `vouched` story
gets that pass too when the developer reported DONE_WITH_CONCERNS or the
changed lines no plan block covers hold one of the profile's landmine
markers. Neither is a downgrade of the other.
Fixes go to a fresh `flow:intermediate-dev` briefed with the findings, never back
to the developer that built the story: a resumed developer re-reads its whole
context on every request. Re-reviews go to `flow:story-reviewer`, with the fix's
range and the findings; what is still Critical or Important after one goes to
the manager. The sequence below is for the other options.

1. `flow:spec-reviewer` first — the cheaper pass, and there is no point grading
   the craftsmanship of the wrong feature. Hand it the same plan line range,
   the developer's report, **and the `BASE_SHA..HEAD_SHA` range** (real shas,
   not "the last commit"): its prompt scopes the diff to the commits named in
   the dispatch, so a dispatch without shas leaves it guessing from `git log`.
   Tell it explicitly not to trust the report.
2. `flow:code-quality-reviewer` only after spec compliance passes. It needs a
   `BASE_SHA..HEAD_SHA` range; give it real shas, not "the last commit".
3. Both are read-only. They report; the developer fixes; re-dispatch the
   reviewer to confirm. Reviewers hold no memory of a prior pass, so a
   re-review dispatch must re-supply the range and the prior findings.

**Review depth is keyed to the change's stakes, never the session's model.**
A defect merged from a cheap session costs exactly what one merged from an
expensive session costs, so a cheap session's dispatch gets the same pinned
reviewer tiers. Vary depth by *skipping*, not downgrading: a weakened review
still emits a green checkmark that downstream reports will trust at full
strength.

- Trivial tooling or docs changes: skip the review sequence; the controller
  reads its own diff.
- Medium-stakes mechanical work: `flow:spec-reviewer` alone may suffice; say so in
  the dispatch record.
- Substantial changes: the full sequence, whatever the session's model.
- A genuinely low-stakes one-off may pass an explicit `model` override on the
  Agent call, a visible per-dispatch act. The Agent call takes `effort` too,
  but never lower a reviewer's effort with it: that is the downgrade this
  section rules out.

**Escalate on observed signal; never downgrade.** The stakes tier sets a
*floor*, chosen before the diff exists. Review difficulty is a property of the
diff, so what the controller learns during execution is worth more than its
up-front estimate. Any of these fires → step **up** one rung (skip →
`flow:spec-reviewer` alone → full sequence → whole-implementation anti-seam pass),
never down:

- the developer reported `DONE_WITH_CONCERNS` rather than `DONE`;
- the diff holds one of the profile's landmine markers: each marks a shape
  that compiles and passes a naive suite;
- outside the `supervised` option only: `flow:spec-reviewer` found issues, so its
  judgment on this task is already suspect, and so is its craftsmanship.

Escalating is *un-skipping*, which is why it needs no effort override: the
lever is which agents run, and adding one is visible in the dispatch record.
It also corrects an anti-correlation in the floor. A task whose up-front
estimate reads "mechanical" is exactly what goes to `flow:junior-dev`, so a floor
set purely by prediction applies the thinnest review to the output of the
cheapest developer, which is where a vacuous test is likeliest to survive.

## The doc pass

After a story's review and fix rounds and its PLAN-lines question,
`flow:doc-writer` (Sonnet @ high) brings the docs into line with the story as
it landed. The plan's own doc edits are predictions written before the code
existed: a fix round can overtake them, and what the plan left out stays out.
The doc writer reads the story's diff, its plan section, its developer's
deviations and the Critical and Important findings its fix rounds took. It
fixes the docs the change made false and adds those the repo would expect for
what the story added, leaving a doc a later story's **Files** line names to
that story; writes a lesson where the repo's CLAUDE.md says lessons go, when a
fixed finding shows a trap no doc captures, and skips it when unsure; updates
a diagram the change made false and creates none; and where the code
contradicts the spec or the plan it documents nothing and reports a `FINDING:`
line, which joins the Minors kept for the final round.

It never touches code, generated files or the owner's memory, which the
manager updates at the run's end. When it committed,
`loom flow conf docs-check --base <the head before it> --head <HEAD>` lists
the paths it changed that aren't doc paths (the profile's `[docs] paths`,
`*.md` by default, less its `generated`), and the story gates run again. Its
commits get no review of their own, like any docs-only change; the final
anti-seam review reads every doc the run changed.

## The verifier runs gates check-only

`flow:verifier` (Haiku @ high) runs the gates through `loom flow gates` — per
story `start --story <the story's commits>`, after the last story
`start --final` — and reports the table `loom flow gates wait` prints, each
step PASS, FAIL, FAIL (killed), FAIL (infra), VACUOUS or SKIPPED. Dispatch it
in the background to keep build logs out of the controller's context while
the compile wall-clock burns. Profile gates are check-only by definition: a
formatter appears in its check form, and drift is a FAIL for a developer to
apply. `start` queues the gates in loom's work queue as a chain, each job
after the one before, so the chain keeps its place in line and runs one gate
at a time. A story's chain stops at its first FAIL; after a final gate fails,
`wait` queues the gates after it again, so every final gate runs and one pass
shows every failing CI job. The verdicts are the tool's: it reads each job's
own exit, runs the profile's vacuous check on each output, and calls a step a
signal ended FAIL (killed), an infra failure, and one whose log says a port
was already in use FAIL (infra), since no fix frees a port another process
holds. A gate the profile writes as a table with `locks` takes those locks in
the work queue, so it never runs beside another job naming one. Never run the verifier while a
developer builds in the same worktree: both drive heavy builds.

`loom flow gates wait` waits at most 170 seconds a call, so the verifier waits
inside its turn ([why](#an-agent-waits-inside-its-turn)). The run's record is
in the state directory (`<state>/gates/<run>.json`): a verifier dispatched
after another was made to hand back finishes the run with
`loom flow gates wait <run>`, and `loom flow gates cancel <run>` ends one a
story abandons.

## The run engine (`/flow:run`)

`/flow:run <plan>` runs a drafted plan, or one written by hand that
`loom flow check-plan` passes. `loom flow run start` queues an engine, a Go
process running as a `run` job, that takes every mechanical step itself:

- it dispatches each developer, fixer, reviewer, doc writer and
  premise-check as a headless agent job (`loom flow agent`), with briefs it
  writes from the plan and the profile's blocks;
- it routes a story by its tier, its review column and the conformance
  markers;
- it runs the profile's services and the gates itself, so no
  `flow:verifier` is dispatched;
- it reads a review's `FINDING:`, `PLAN:` and `VERDICT:` lines and a doc
  writer's `DOC:`, `LESSON:` and `FINDING:` lines, which the headless
  preamble asks for;
- it writes the run's record.

It asks the manager every judgement as a typed question: a NEEDS_CONTEXT,
an intermediate's BLOCKED, a design finding, a premise's DESIGN verdict,
findings a fix round left open, a second gate failure, a failed agent, a
doc pass that changed what isn't a doc, a tree that moved. The manager waits on `loom flow run wait` in a background
Bash, which costs no turns, and answers with `loom flow run answer --from
spec|owner`: the record counts the questions the spec settled, which a
judge (3c, not built) could have answered, against the ones that needed the
owner. A failed agent's question takes a decision, which its retry's brief
carries. A plan the manager edits and commits with an answer moves the
stories' line ranges for the later briefs; a run can't add, remove or
reorder its stories. `start` refuses a `claude_permission_mode` other than
`auto` or `bypassPermissions`, in which headless agents can't write.

The engine replays. Each step has a fixed name (`<run>-s3-dev2`) and a
memoized result in `<state>/runs/<run>.json`, so an engine lost mid-run
carries on, through `loom flow run resume`, without submitting a step twice.
Stories run in plan order; a declared pair's second story develops in a
lane beside its first ([parallel pairs](#parallel-pairs)), and a question
from either parks the run while the other's agent works on. The tree check
takes commits that touch only the spec and the plan, as `answer` does. A
gate run that leaves only files the profile's `generated` globs match
changed (a test that rewrites a stale bindings file) gets them committed by
the engine, hooks skipped, and noted in the record; a failed one then runs
once more before a fixer, since the stale copy may have been all it failed
on. Any other change a gate run leaves parks the next step as a tree that
moved.
Pausing the manager's session spares the run's jobs; killing it ends the run. Agents run with `--max-turns` from their
definitions, since `--agent` ignores `maxTurns`.

### Parallel pairs

A plan may let `/flow:run` overlap two stories: `**Parallel:** 3+4, 7+8`,
one line before the first story. In each pair the second story's developer
runs in a lane, a worktree the engine makes at the first story's base under
`<global dir>/flow/lanes/`, while the first story runs whole in the session's
worktree. When the first is done, the engine picks the lane's commits onto
HEAD and runs the second from its gates on, as any story. Pairs may chain
(`3+4, 4+5`); one lane runs at a time.

The design session declares a pair only when the second story uses nothing
the first creates (no type, function, file or flag the first story's
**Interfaces** exposes) and builds from a fresh checkout: a lane has no
untracked files. `loom flow check-plan` fails a pair that isn't adjacent,
shares a file or holds an exclusive build in both stories, and the seams
check lists what the first story defines that the second uses. A lane left
dirty, or whose commits don't apply, asks the manager `lane`: `retry`
after fixing it by hand, `redo` to build the story again in the session's
worktree, or `stop`. A lane is removed only once its commits are on the
branch.

## The developers' git guard and turn caps

The `flow:habits` skill's git rules are backed by a hook. Claude ignores
`hooks` in a plugin agent, so the guard is loom's: every Claude session loom
launches with flow runs `loom flow guard` before each Bash, Write and Edit
call, and for the agents `lib/guarded-agents.txt` lists it runs
`lib/git_guard.py` on the call (`--drafter` for the drafter). It exits 2, which
blocks the call and hands the agent the reason,
when a command is `git reset`, `rebase`, `stash`, `clean`, `checkout`,
`switch`, `restore` or `push`; a commit with `--amend`, `-a` or no
`-- <paths>`; or an add of `-A`, `-u`, `.` or a directory. Heredoc commit
messages are skipped. A guard that can't run (no flow root, no `python3`, a
crash) blocks a guarded agent's call too, and `loom flow conf doctor` checks
the guard before a run. Its tests run in `flow/lib/test.sh`.

The `flow:drafter`'s frontmatter runs the same guard with `--drafter`, on Bash,
Write and Edit: only read-only git gets through, and Write and Edit only under
the drafts directory flowconf names for the worktree (the profile's
`paths.drafts`, `.flow/drafts` by default). A write whose directory can't be
told, outside a worktree or under a profile flowconf refuses, is blocked. A
Bash redirect could still write anywhere; the drafter's definition forbids it.

The guard is a session-level hook loom writes into each launch's settings,
not an agent's frontmatter hook, so it runs in any folder, workspace trust or
not.

`maxTurns` caps each developer: 120 for `flow:junior-dev`, 250 for
`flow:intermediate-dev`, 400 for `flow:senior-dev`. These are first guesses. At the cap
the agent stops with `TURN_CAP`, and the engine resumes an intermediate once,
or moves a junior story to `flow:intermediate-dev`.
`flow/lib/check-agents.sh` fails a definition that edits files without a cap,
or that `lib/guarded-agents.txt` doesn't list.

## The developers share one preloaded skill

`flow:junior-dev`, `flow:intermediate-dev` and `flow:senior-dev` take their binding habits
from `the flow:habits skill`, which each lists under
`skills:`: how to work from the plan (its code is the spec, every deviation is
listed, a decision outside the lane is asked) and the repo habits (the
prefix, the shared index, report delivery). Claude Code injects a preloaded
skill's full text at startup, so the habits cost no tool call and can't be
skipped the way a "read this doc first" pointer can. Each body keeps only its
lane: which decisions are its own. Edit the habits in the skill, once. If the
preload ever fails, each body tells its agent to Read the skill file before
anything else.

The harness skips a preloaded skill that is missing or sets
`disable-model-invocation: true` with only a debug-log warning, so
`flow/lib/check-agents.sh` fails both. The skill's description sits in every
session's skill listing — Claude Code has no preload-only setting — so it says
not to invoke it directly, and `user-invocable: false` keeps it out of the `/`
menu.

## Test an edited definition from a later turn

An edited agent definition reaches dispatches from the next user turn, not
the turn that made the edit, so `flow/lib/check-agents.sh` is the only check an
edit gets in that turn. To see what an agent actually starts with, dispatch it
from a later turn with a no-tools probe that asks it to quote a line only the
new version has. A session runs the copy of the plugin its launch was given,
so an edit in loom's `flow/` reaches it only once a build with the edit
relaunches it.

## Every agent chooses its prompt-cache lifetime

Claude Code gives a subagent's requests a five-minute prompt cache, even on a
subscription; only the main conversation gets an hour by default. When an
agent pauses longer than that — `flow:verifier` sitting through a gate, or
an agent waiting on the owner's answer — its next request processes its whole
context again at the full input price. Every definition therefore chooses
`experimental.cacheTtl`, and `flow/lib/check-agents.sh` fails one that sets
nothing, so the choice stays explicit. The hour has a price: a one-hour cache
write costs twice the base input rate, against 1.25 times for five minutes, so
an agent that never pauses for five minutes pays more for nothing. `flow:glance`,
`flow:recon`, `flow:premise-check`, `flow:story-reviewer`, `flow:code-quality-reviewer`,
`flow:intermediate-dev`, `flow:spec-reviewer`, `flow:drafter` and `flow:doc-writer` set `5m`;
`flow:senior-dev`, `flow:verifier` and `flow:junior-dev` set `1h`. The cost of `5m` is a cold
resume: an agent resumed after five minutes idle writes its whole context
again, which a `flow:drafter` seldom pays, since every fix job goes to a fresh
one, except when a NEEDS_CONTEXT answer waits on the owner.
`/flow:retro` measures both from the run records, and loom's `flow/PROVENANCE.md`
records each change to a lifetime.

Claude Code ignores `1h` while the subscription is drawing on usage credits,
and the `subagentPromptCacheTtl` setting (or
`CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL`) overrides every definition at once.

## A command that outlives its timeout wakes its agent later

A Bash call that runs past its timeout (two minutes unless the call sets one,
ten at most) isn't killed. Claude Code moves it to the background ("Command
did not complete within its 120s timeout and was moved to the background")
and wakes the agent when it ends, even after the agent has reported. That wake
re-reads the agent's whole context, and writes it all again if the cache has
expired. So every agent searches only the repo and its dependency sources
(for Cargo, `cargo metadata --format-version 1 --offline`, run with the
profile's prefix, gives each dependency's `manifest_path`), gives a long build
a timeout it fits in, and doesn't report while anything it started is still
running.

## An agent waits inside its turn

Claude Code makes an agent hand back when its turn ends with none of its own
agents running and no background command of its own ("[handback-send-enforce]
Your report has not been delivered"), and an agent that has ended its turn to
wait for a background command counts as not running. A dispatcher whose only
agent out is a verifier waiting that way is made to hand back mid-run.

So the verifier and the developers wait in bounded foreground calls: the
verifier through `loom flow gates wait`, a developer through `loom flow wait`
for any step that can take more than a few minutes.
`start <name> -- <command>` queues the command in loom's work queue and prints
its job ID, and `wait <name>`, a foreground call that blocks for at most 170
seconds inside a 600000 ms Bash timeout, prints `done <name> exit <status>`
when the job ends, `running <name>: job <N> …` with where it stands when it
should be called again, or `lost <name>: …` for a job that ended without an
exit of its own. Each call copies what the job's output gained to
`<state>/agent-wait/<name>.log`, and `wait --job <N>` follows a job queued any
other way. That also keeps a step alive past the Bash tool's ten-minute
ceiling. The wait stays under three minutes because Claude Code 2.1.316 moves
a background subagent's foreground Bash call to the background once it has
run 180 seconds, a cap set on Anthropic's side: in the 2026-11-03 retro's runs
it moved 717 waits of 540 seconds, and each moved wait left a process that
later woke its agent with a notification.

Every step is a job, never a process beside the queue: on 2026-11-05, 103 of
one repo's 169 steps ran detached on cores and memory the queue counted as
free, and their compiles landed in the shared sccache server, outside every
job. `start` refuses a poll, `sleep` or a shell loop around `sleep`, since a
queued poll holds a claim doing nothing (six did that day): wait on the step
it watches. It refuses a `loom …` command too, which is already a job to wait
on with `--job <N>`.

## Effort is inherited when unpinned

An agent definition with no `effort:` key inherits the **session's** effort, not
a cheap default. A mechanical agent dispatched from an xhigh session runs at
xhigh, quietly erasing the cost saving that justified routing to it. Every
definition pins effort explicitly; keep it that way when adding another.
`flow/lib/check-agents.sh` reports the live number of definitions on every run, so
don't write a count of the roster in prose.
