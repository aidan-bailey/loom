# Dispatching the flow's agents

The definitions in the flow plugin's `agents/` (loom's `flow/plugin/agents/`)
encode what an *agent* must do.
This guide covers the other half: what the **controller** (the main session,
or the supervisor it dispatches) must do. Nothing enforces any of it; every
failure below is silent. The history and measurements behind these rules are
in claude-flow's archived `docs/evidence.md`, which no agent loads.

## Offering the execution choice

`superpowers:writing-plans` ends by offering two ways to execute the plan:
**Subagent-Driven** (a fresh subagent per task, reviewed between tasks) and
**Inline Execution**. In a repo with a flow profile whose CLAUDE.md defines no
supervised option of its own, it asks for the execution option before it
writes the plan, and offers a third (loom's context says so in every session):

**3. Supervised.** A skeleton now; `/clear`, then `/flow:draft <plan>`;
`/clear`, then `/flow:supervise <plan>`. `flow:drafter`s write the stories from the
skeleton ([the skeleton](#the-skeleton)), then a `flow:plan-execution-supervisor`
runs the plan story by story. It dispatches a developer per story, answers
their questions, escalates the rest to you, and reviews each story and the
whole.

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
talks to the owner, but sits idle while the plan runs. The supervisor (Opus)
routes work and answers from the spec and plan. The developers (Haiku or
Sonnet, by story) spend most of the tokens, and `flow:verifier` (Haiku) keeps build
logs out of every Opus context. Questions climb the same ladder — developer,
supervisor, manager, owner — and each rung costs more than the one below,
which is why the supervisor checks the plan's code against the tree before
the first story.

**The design stage and the run each start from a fresh session.** Every
manager turn re-reads the whole conversation. The drafting manager's turns are
its agents' reports, and the run manager's are the supervisor's questions and
reports, so a design conversation carried into either is paid for again on
each of them. A quiet spell longer than the cache lifetime (one hour on a
subscription, five minutes on usage credits) makes the next turn write that
whole context again. The design session therefore ends at a committed spec and
skeleton, and the owner clears twice: once before `/flow:draft`, once before
`/flow:supervise`. When the option is chosen, the design session:

1. Moves every decision the conversation made into the spec, since after the
   clear the spec is the only record of the design. That includes the limits
   a run would otherwise stop to ask about: for every outside service the
   feature calls, a rate budget, a timeout and what happens when it fails;
   and for every number an agent or a user supplies, a bound. A limit the
   spec never set holds a story up while the manager asks the owner.
2. Writes the plan skeleton ([the skeleton](#the-skeleton)): the header, the
   file structure, the names contract and the story list, and none of the
   stories' code. A plan is drafted from its spec, never generated from a
   prototype of the code.
3. Commits the spec and the skeleton by pathspec and hands over: the owner
   types `/clear`, then `/flow:draft <plan>`. The design session doesn't
   draft, check or review.

`/flow:draft` runs the rest of the design stage from a fresh session. It
dispatches the `flow:drafter`s ([drafting the stories](#drafting-the-stories)),
joins and checks the drafts, has one `flow:recon` check the seams between the
stories ([the seams check](#the-seams-check)), has fresh drafters fix what the
checks found ([fixing the plan](#fixing-the-plan)), writes the
`**Design stage:**` line, commits the plan by pathspec, and ends with "run
`/clear`, then `/flow:supervise <plan>`". It asks the owner only what the spec
and the names contract don't settle, writes each answer into the spec and
commits it at once, and reads the agents' reports, never the drafts.

`STATE.md`, in the plan's drafts directory, is its memory: a **Phase** line
(drafting, checking, seams, fixing or committed) saying what is done and what
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
no hand-back is dispatched again. A drafter on a drafting job is dispatched
again, fresh, to finish its draft; one on a fix job
gets its fix-job brief again, with the findings and "some of these may already
be fixed; fix what remains". A `flow:recon` or
`flow:premise-check` restarts unless its findings file shows it reported. This
prints the last hand-back in a transcript, or says there is none:

```bash
python3 -c 'import json,sys; m=[b["input"]["message"] for l in open(sys.argv[1]) for b in (json.loads(l).get("message") or {}).get("content") or [] if isinstance(b, dict) and b.get("type") == "tool_use" and b.get("name") == "SubagentHandback"]; print(m[-1] if m else "no hand-back: the agent had not finished")' <transcript>
```

`/flow:supervise` also runs a plan written by hand, as every plan was before
`/flow:draft`, once `loom flow check-plan` passes it.

`/flow:supervise` then runs the plan. It checks the repo with
`loom flow conf doctor` and the plan with `loom flow check-plan`; records `BASE_SHA`, the
start time, the roster ID (from `loom flow conf roster`)
and the profile; and dispatches one
`flow:plan-execution-supervisor` without a name
([why](#a-named-agent-is-a-teammate)). It answers the supervisor's questions
from the spec, asking the owner and writing the answer into the spec when the
spec doesn't settle one. It stages nothing and starts no other developer or
`flow:verifier` while the run goes on
([the git index is shared](#the-git-index-is-shared)). It ends by writing the
run's record to `~/.loom/flow/records/` ([`records.md`](records.md) gives the format),
with a copy in the repo when the profile asks for one. The record's header
copies the plan's `**Design stage:**` line, when it has one, so `/flow:retro`
reads the design stage and its run together. Facts only: the owner starts
retros with `/flow:retro`.

**The supervisor hands over every third story.** Its context grows with every
report it reads, and each wake re-reads all of it. After every third story it
completes, at the next wake with none of its agents running, it writes what
only its context holds into the state file and hands back `REFRESH`. The
manager dispatches a fresh supervisor, which resumes from the state file. The
fresh start caches the state file, the spec and the story list again, and
earns that back within a few wakes. A resumed agent reports to whoever
dispatched it, so the successor dispatches its own.

### The skeleton

The design session writes the skeleton beside the spec: what takes judgment,
and none of the stories' code. It holds, in order:

- the header: writing-plans' title, Goal, Architecture and Tech Stack, and the
  `**Spec:**` line;
- the file structure;
- the names contract: every name, type and signature one story defines for
  another, such as functions and their signatures, types and their fields,
  file paths, CLI flags, configuration keys and the messages tests match.
  The design session writes it, and after that only `/flow:draft`'s manager
  edits it;
- the story list, under the exact heading `## Stories`;
- the marker line, which ends it:
  `<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->`

Each list entry gives the story's id, a paragraph and its five opening lines
(**Developer**, **Files**, **Interfaces**, **Verify**, **Review**), which its
drafter copies into the story verbatim, then a **Reading** line, which it
doesn't copy: the spec sections the story needs, by § number or heading,
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
it with `effort: xhigh` for a story whose entry names a landmine shape, one of
the profile's `landmines.markers` or a shape its notes describe; a fix job
runs at high whatever the shape. The Agent
tool sets a per-call effort only when written instructions name a level, and
the skill is that instruction for drafters.

A drafter's brief holds the plan's and the spec's paths, the drafts directory
D, its story ids and their files, `D/S<id>.md`, and the drafter's profile
block (`loom flow conf show --for drafter`: the prefix, the story gates, the
generated globs, the landmine markers and notes, the idioms and the design
registries). It writes each task's commands from those values and never runs
them, checks its own quotes of the tree with `loom flow premises`, and reports
in at most 15 lines. A drafter that needs a name another story shares, which
the contract doesn't give, stops and reports NEEDS_CONTEXT, and never invents
one: the manager amends the contract and answers, and the answer resumes that
drafter. Once the drafters whose stories use an amended name have reported, a
fresh drafter per affected story fixes its draft. The manager never sends a
running drafter new work by message: a parent's message arrives framed as
another agent's request.

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
after the skeleton's marker line from the drafts, in story-list order, and
writes `D/join.json`: each story's first plan line and length. Joining twice
gives the same file. The join reads the story list only from above the marker,
doesn't take `STATE.md` for a draft, and fails on a missing or unlisted draft
and on a draft that holds the marker line outside a fence (a draft quotes it
inside one). Nothing edits a joined story in the plan file. The checks cite
plan lines and a fixer works in its draft, so `/flow:draft` converts: draft
line = plan line − the story's `start` in `join.json` + 1.

### The seams check

The design stage reviews no story. A per-story plan review once cost about as
much as the run it planned, and the run's own reviews still found defects in
plan code it had passed ([the part 2 spec](specs/2026-10-17-generic-agent-flow-part-2-design.md),
§2.2), so every story of a drafted plan gets the run's full review instead.
What the scripts can't check across stories goes to one `flow:recon`.
`loom flow packet <plan> --seams --rev <REV>`
writes `D/packets/seams.md`: every name one story's added code defines and
another story's code uses, cited to the draft lines that define and use it,
then every story's **Files** line. It refuses a plan that isn't the join of
its drafts ("the plan is out of date with its drafts; re-run loom flow join"),
so `/flow:draft` joins after every change before it writes the packet.

The recon reads the packet and the names contract, and answers two questions.
Do the contracts between stories match: is every name a story uses from
another defined there, with the same type, signature and visibility? Which
files outside every story's **Files** line list or count what the plan adds or
changes? It starts from the profile's `design.registries`, the places in the
repo that list or count what stories add: a generated region and the command
that regenerates it, a route or command table, a test that counts registered
items, a document that states a count. It doesn't re-check quotes of the tree,
which `flow:premise-check` settled, or review code. Its report goes to
`D/findings/seams.md`.

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
   finding that states its fix goes to the drafter as it stands. A fix job
   runs at the drafter's pinned high, whatever the story's shape: its fixes
   are decided before it starts, and the run's full review checks the story
   again (the owner, 2026-10-31; claude-flow's archived `PROVENANCE.md` has the evidence). A
   findings file bundles the story's `flow:premise-check` rows
   still open after the check stage's last run (the check stage runs once more
   after its own fix jobs: one more check, not a second fix round), and the
   seams findings that name the story.
3. **Join and check again:** `loom flow join`, `loom flow check-plan` and
   `loom flow premises`, with `flow:premise-check` on any new flag, and the seams
   check once more when a fix changed the contract.
4. **One round.** A finding still open after its fix round goes to the owner;
   there is no second automatic round.
5. **Record and commit.** `/flow:draft` prices its sessions with
   `loom flow costs session` unless `STATE.md`'s Design stage slot already holds
   the line, which it reuses (as after a session that died before the commit),
   then writes the `**Design stage:**` line into the skeleton's head, directly
   above `## Stories`, and into the slot. It joins and checks once more, and
   commits the plan by pathspec; each spec answer was committed as it was
   written.

No `**Plan review:**` line is written, so `loom flow check-plan` marks every story
`review: full`, and the run reviews each story in full.

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

No agent reads the profile. The supervisor runs
`loom flow conf show --for supervisor` once and pastes each role's block into
that role's briefs: a developer gets the prefix, the services (to start, as
written, before the tests that need them) and, on a deep story, the landmine
notes; the verifier gets the services and the vacuous patterns; and a
reviewer gets the prefix (it runs its check-only commands, such as the
formatter check, inside it), the notes and the idioms. Per story it hands the
verifier the story's commits, and the verifier's
`loom flow gates start --story <the story's commits>` expands `{packages}` to
the Cargo packages the story changed and the packages that depend on them;
after the last story the verifier runs `loom flow gates start --final`.
`/flow:draft` runs `loom flow conf show --for drafter`
once, which the supervisor's block leaves out, and pastes it into each
drafter's brief: the prefix, the story gates, the generated globs, the
landmine markers and notes, the idioms and the design registries.

## Picking a developer

| Task shape | Developer |
|------------|-----------|
| The plan gives the task's code in full and it lands in one or two files with no wiring between them; or a fully specified procedure | `flow:junior-dev` (Haiku @ xhigh) |
| The plan gives the code, and fitting it into the codebase spans several files — the usual output of brainstorming then writing-plans | `flow:intermediate-dev` (Sonnet @ high) |
| The last resort: a slice an `flow:intermediate-dev` failed twice, the second time with Opus's decision in its brief, or one the manager asks for | `flow:senior-dev` (Opus @ high) |

**Opus writes code only as a last resort.** Sonnet writes every story that
needs fitting, Haiku the fully specified small ones, and Opus designs and
reviews. What a story leaves to judgment is decided before a developer writes
it: in the plan, or by the supervisor or the manager when a developer reports
`BLOCKED` or NEEDS_CONTEXT. A reviewer that isn't the writer's model doesn't
share its blind spots. `flow:senior-dev` stays for the rare case Sonnet can't
handle, so a plan shouldn't assign it: `loom flow check-plan` warns when one does.

All three preload [one skill](#the-developers-share-one-preloaded-skill): the
plan's code is the spec, every deviation is listed, and a decision outside the
developer's lane goes back as a question to whoever dispatched it. A junior's
`BLOCKED` moves the slice to `flow:intermediate-dev`; an intermediate's goes back
to Opus for a decision, then to a fresh `flow:intermediate-dev`, and only if that
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
any effort: settle it first. The Agent tool sets a per-call effort only when
written instructions name a level, and the supervisor's step 1 under "Each
story" is that instruction. Per-call `effort` needs Claude Code 2.1.292 or later, and
`CLAUDE_CODE_EFFORT_LEVEL` overrides it.

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
| Writing a plan's stories from its skeleton, and fixing the drafts | `flow:drafter`, from `/flow:draft` |
| One story that `loom flow check-plan` marks `review: vouched`, and every re-review under `supervised` | `flow:story-reviewer` |
| Running the profile's gates (check-only, in the background) | `flow:verifier` |
| Plan-task implementation | a developer: `flow:junior-dev` or `flow:intermediate-dev`; `flow:senior-dev` only as a last resort |
| A whole plan, under the `supervised` option | `flow:plan-execution-supervisor` |
| Review outside the `supervised` option | `flow:spec-reviewer`, then `flow:code-quality-reviewer` |
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

**Do not dispatch a developer or the supervisor during a brainstorm.** The
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
  worktree. The rtk-wrapped `git status` and `git diff --cached` can hide
  them; the truth check is
  `/usr/bin/env git diff-index --cached --name-status HEAD`. Run it before
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
inline, and `SendMessage` is for answers and for the supervisor's questions to
the manager. A report sent with `SendMessage` as well arrives twice, in the
dispatcher's context, the most expensive one.

Questions travel the same way. A subagent that ends its turn without handing
back is told at once to hand back, and the call ends its run. So an agent
never asks and waits. It stops and reports NEEDS_CONTEXT with the question,
and the answer, sent with `SendMessage` to the id on its report, resumes it
with its context; its next report goes to the same dispatcher. An agent with a
child still running is not told to hand back, which is how the supervisor
waits for its developers. A child's completion notice can still wake a
dispatcher that has already handed back; it must then stop without reporting
again.

### A named agent is a teammate

Dispatched with a `name`, an agent joins the session as a teammate, not a
subagent, and its own dispatches can't hand back to it: their reports fail
with "the agent that spawned you is no longer running". Dispatched without a
name, a supervisor's agents report back to it. So `/flow:supervise` dispatches
the supervisor without a name and messages it by the agent id the launch
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
`VALID_TOOLS` in that script: a deliberate, reviewable act. Only
`flow:plan-execution-supervisor` may list `Agent`, since a subagent that lists it
can spawn subagents of its own, and a subagent definition ignores an
`Agent(type)` list, so any agent holding `Agent` could spawn any type.

## Review sequence

Under the `supervised` option the supervisor runs the reviews: one per story,
then one whole-implementation anti-seam pass by `flow:code-quality-reviewer` after
the last. `loom flow conformance` first maps the story's diff onto the plan:
which blocks landed as written, which didn't, and which changed lines no block
covers. `loom flow check-plan` prints a `review:` column per story. For a `vouched`
story, `flow:story-reviewer` (Opus @ high) reads just those, the developer's
deviations and tests, and the seams. For a `full` story (a light story, or any
story of a plan without a `**Plan review:**` line), `flow:code-quality-reviewer`
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
signal ended FAIL (killed), an infra failure. Never run the verifier while a
developer builds in the same worktree: both drive heavy builds.

`loom flow gates wait` waits at most 170 seconds a call, so the verifier waits
inside its turn ([why](#an-agent-waits-inside-its-turn)). The run's record is
in the state directory (`<state>/gates/<run>.json`): a verifier dispatched
after another was made to hand back finishes the run with
`loom flow gates wait <run>`, and `loom flow gates cancel <run>` ends one a
story abandons.

## The run engine (`/flow:run`)

`/flow:run <plan>` runs a plan drafted for `/flow:supervise` without the Opus
supervisor. `loom flow run start` queues an engine, a Go process running as a
`run` job, that takes the supervisor's mechanical steps itself:

- it dispatches each developer, fixer, reviewer and premise-check as a
  headless agent job (`loom flow agent`), with the briefs the supervisor
  wrote;
- it routes a story by its tier, its review column and the conformance
  markers;
- it runs the profile's services and the gates itself, so no
  `flow:verifier` is dispatched;
- it reads a review's `FINDING:`, `PLAN:` and `VERDICT:` lines, which the
  headless preamble asks reviewers for;
- it writes the run's record.

It asks the manager every judgement as a typed question: a NEEDS_CONTEXT,
an intermediate's BLOCKED, a design finding, a premise's DESIGN verdict,
findings a fix round left open, a second gate failure, a failed agent, a
tree that moved. The manager waits on `loom flow run wait` in a background
Bash, which costs no turns, and answers with `loom flow run answer`. 3c's
judge will answer what the spec settles.

The engine replays. Each step has a fixed name (`<run>-s3-dev2`) and a
memoized result in `<state>/runs/<run>.json`, so an engine lost mid-run
carries on, through `loom flow run resume`, without submitting a step twice.
Stories run one at a time. Pausing the manager's session spares the run's
jobs; killing it ends the run. Agents run with `--max-turns` from their
definitions, since `--agent` ignores `maxTurns`.

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
Claude Code hands back the work so far marked partial, and the supervisor
resumes the agent, or moves a junior story to `flow:intermediate-dev`.
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
agent pauses longer than that — the supervisor waiting on a story, or
`flow:verifier` sitting through a gate — its next request processes its whole
context again at the full input price. Every definition therefore chooses
`experimental.cacheTtl`, and `flow/lib/check-agents.sh` fails one that sets
nothing, so the choice stays explicit. The hour has a price: a one-hour cache
write costs twice the base input rate, against 1.25 times for five minutes, so
an agent that never pauses for five minutes pays more for nothing. `flow:glance`,
`flow:recon`, `flow:premise-check`, `flow:story-reviewer`, `flow:code-quality-reviewer`,
`flow:intermediate-dev`, `flow:spec-reviewer` and `flow:drafter` set `5m`; the supervisor,
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
wait for a background command counts as not running. A supervisor whose only
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
later woke its agent with a notification. If a supervisor is made to hand back
anyway, it names its agents still out in the state file and in its `REFRESH`
report, and the manager forwards their late reports to the successor.

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
