# Flow: a doc pass after each story

Status: design approved section by section 2026-11-10; awaiting review of this file.

A story's docs come only from its plan today: drafters write the doc edits into the stories as tasks, from the spec, before the code exists (a README task at a story's end, or one story that owns every doc edit). Nothing checks those edits against the code that landed after the review and fix rounds, and the CLAUDE.md lint checks the docs' structure, not their truth. This spec adds a doc pass to every story, run by a new pinned agent, `flow:doc-writer`, under both controllers: loom's run engine (`/flow:run`, `internal/flowrun`) and the supervisor (`/flow:supervise`, `flow:plan-execution-supervisor`).

## 1. Problem

- Plan-written doc edits are predictions. A fix round that changes a behaviour, a name or a flag leaves the plan's doc text describing the code before the fix, and no step looks again.
- What the plan left out stays out: a flag a developer added as a deviation, the README a new package needed.
- Lessons are lost. Loom's CLAUDE.md asks, at the end of non-trivial work, whether a mistake was made whose rule isn't captured anywhere yet. In a flow run those mistakes are the reviews' Critical and Important findings, and no step asks the question of them.
- Memories are the manager's, and no flow step reminds it to record the run's outcome.

## 2. Decisions

- **The pass complements the plan's doc tasks** (owner, 2026-11-10). Plans keep writing doc edits into their stories; the pass checks the docs against the story as it landed, and fixes what the change made false or left out. Rejected: replacing the plan's doc tasks (`/flow:draft`, the drafter and the skeleton format would change, and doc wording the design settles would have no home); splitting the docs between the plan and the pass by kind.
- **Both controllers run it** (owner, 2026-11-10): the engine as a step of `story()`, the supervisor as a step of its "Each story" list, so a plan behaves the same under either. Rejected: the engine only.
- **Memories stay the manager's** (owner, 2026-11-10). The pass never touches the owner's memory, which lives outside the worktree and git, where no gate checks a write and no revert undoes one. `/flow:run` and `/flow:supervise` end by telling the manager to record the run's outcome (§5.8). Rejected: `MEMORY:` report lines the manager writes at the end (new plumbing for an agent that sees only a diff and its reviews); the pass writing memory per story (its writes race the manager's own, and status lines churn on every story).
- **The pass records lessons as well as facts** (owner, 2026-11-10). Given the findings the story's fix rounds took, it writes a trap one of them exposed where the repo's CLAUDE.md says lessons go, by the repo's own bar: if unsure, skip. Rejected: facts only; each lesson as a question to the manager.
- **The pass's commits run the story gates again and get no review of their own** (owner, 2026-11-10). The dispatch guide already skips review for docs-only changes. The final anti-seam review's brief gains docs that contradict the code, so one Opus pass reads every doc the run changed. Rejected: a `flow:story-reviewer` re-review of every pass (about $0.29 a story by the 2026-11-06 record, doubling the pass's cost); no gates per story (a pass that broke the doc lint would fail the next story's gates, which would take the blame).
- **A new pinned agent, `flow:doc-writer`** (owner, 2026-11-10, approach A): a lane, a guard entry and pins of its own, and a row of its own in the record's cost by type, so `/flow:retro` can tune or drop it on evidence. Rejected: B, `flow:intermediate-dev` with a doc brief (its preloaded habits are about implementing plan code, its pins can't be tuned for docs, and its cost merges with the developers'); C, a script listing the doc lines that name a path, symbol or test name the diff touched, feeding an agent (no script finds the missing doc of new surface, since no doc names it yet, and symbol extraction is new code per language; §9).
- **Its pins: Sonnet, effort high, `maxTurns: 100`, `cacheTtl: 5m`** (owner, 2026-11-10).
  - Not Haiku, though twenty times cheaper: the roster sends completeness and absence claims ("no other doc names this") to `flow:recon` on Sonnet, not `flow:glance` on Haiku, and a wrong doc passes every gate.
  - Not Opus: `flow/lib/prices.toml` prices Opus 5.5 at twice Sonnet 5.5's input, cache writes and output (cache reads cost the same), and cache writes and output are most of an agent's cost in the records. Opus designs and reviews, and the final review is an Opus read of the docs by a model that isn't the writer's.
  - High, not medium: medium is recon's effort, for sweeps. The pass judges what is stale and writes in the repo's house style (in loom, the rule shape and the size budgets), which is the drafter's kind of work, at high. The retro lowers it if the records allow.
  - `5m`: the agent runs straight through and never waits on a gate.
  - Estimate: $0.15–0.35 a story. The 2026-11-06 run's Sonnet fix rounds with small briefs cost $0.11–0.20, and a pass also reads the story's diff and docs: about $1 on that four-story, $10.08 run.
- **The final round has no doc pass.** Its fixes are the run's kept Minors and seam fixes, and the anti-seam review's doc line puts any doc they falsify among the findings its fixers fix.
- **The guard checks the doc writer's Bash calls as a developer's, and doesn't confine its writes.** A Bash redirect or `sed -i` writes anywhere, whatever the guard does with Write and Edit, so the check that holds comes after the fact: `loom flow conf docs-check` over the pass's commits (§5.4).
- **A stray asks its own question kind**, `docs-strayed` (§5.5): it must take `accept` (keep a harmless change), which `agent-failed` doesn't.
- **A run started before this change runs without the pass.** `State.Docs`, set by `loom flow run start`, says whether a run has it, so a run resumed across the upgrade replays the procedure it started with.
- **The pass's findings join the kept findings** for the final round, whatever their severity, and carry no design flag: the spec already decided what the code should do, and a doc writer unsure what the spec means asks (`NEEDS_CONTEXT`).

## 3. Scope

In: the agent `flow:doc-writer` and its `flow/PROVENANCE.md` entry; `doc-writer` in `flow/lib/guarded-agents.txt`; `flow:habits`' description; the profile's `[docs]` table, the `doc-writer` role block and `flowconf.py docs-check`; the engine's docs step, `State.Docs`, `DocsBrief`, the report parser, the `docs-strayed` kind, the anti-seam brief's doc line and the record's columns; the doc writer's preamble, `DocsCheck` and `start`'s new fields in `internal/cli`; the supervisor's new step and its final review's doc line; the board's docs cell; the last step of `/flow:run` and `/flow:supervise` on memory; `/flow:retro`'s cost share; the docs (§7); tests.

Out: changes to `/flow:draft`, the drafter or the plan format; a doc pass in the final round; memory writes by any agent; a guard mode that confines the doc writer's writes; a profile switch that turns the pass off; `/flow:init` asking about docs that aren't Markdown (§9).

## 4. Concepts and names

- **The doc pass**: a story's step after its review and fix rounds and its PLAN-lines question: one `flow:doc-writer` run (retried on failure), then `docs-check` and the story gates when it committed.
- **Doc paths**: the files a doc pass may change: those a glob in the profile's `[docs] paths` matches and that aren't generated. `paths` defaults to `["*.md"]`. A glob matches as `generated` does in `flow/lib/plan_conformance.py` (`fnmatch` on the whole path or on the file's name), so `*.md` matches every Markdown file at any depth. Generated means `is_generated` holds for the path (the profile's `generated` globs and the common lockfiles).
- **A stray**: a path a doc pass's commits changed that isn't a doc path. In loom, `CHANGELOG.md` and `docs/specs/protocol.md` are Markdown but generated, so a change to either is a stray.
- **Step names**: `<run>-s<id>-docs<k>`, kind `docs` (`flowrun.KindDocs`).
- **Report lines**, before the `STATUS:` line:
  - `DOC: <path> <what changed and why>`, one per file the pass changed;
  - `LESSON: <path> <the trap>`, one per lesson it wrote;
  - `FINDING: <severity> <file>:<line> <what the code does against the spec or plan, and what they say>`, one per contradiction the reviews missed.

## 5. Behaviour

### 5.1 The doc writer

`flow/plugin/agents/doc-writer.md`, frontmatter:

```yaml
model: sonnet
effort: high
maxTurns: 100
tools: Bash, Read, Write, Edit, SendMessage
skills:
  - flow:habits
experimental:
  cacheTtl: 5m
```

Its description stays under `flow/lib/check-agents.sh`'s 400-character cap and ends, as every agent's does, "Pins set by the owner; change only on their instruction."

Its body gives the lane:

- It reads the story's diff (`base..head` from its brief), the story's plan section and its developer's `DEVIATION:` lines, and the spec where the diff leaves the intent unclear. The diff is what the docs follow.
- It works through, in order:
  1. docs the change made false, the plan's own doc edits included, which a fix round may have overtaken;
  2. docs the repo would expect for what the story added and no doc covers yet: a flag in the user guide, the README of a new package, a row in a registry the brief's block names;
  3. lessons (below);
  4. an existing diagram the change made false.
- A doc a later story's **Files** line names is that story's to edit: it leaves it, even when this story's change makes it false (a plan that leaves a doc stale until a later story says so in its header).
- It follows the repo's CLAUDE.md on what goes where (in loom: rules in a `CLAUDE.md` in the rule shape, facts in a `README.md`, procedures in `docs/claude/`), within the size budgets its lint enforces, moving context into a guide rather than raising a budget.
- **Lessons.** From the findings the brief lists: one that was fixed, shows a trap a future session would hit, and that no doc captures yet, it writes where the repo's CLAUDE.md says lessons go (in loom, a guide in `docs/claude/` with its `INDEX.md` line, or a rule in the rule shape). When the repo's CLAUDE.md names no place, it writes none. If unsure, it skips: undocumented beats noise.
- It never changes code or code comments, a generated file, the owner's memory, or a doc a later story owns; it never creates a diagram, and never rewords what is still true.
- When the landed code contradicts the spec or the plan, it documents nothing about that behaviour and reports a `FINDING:` line.
- Before it reports `DONE`, it runs the doc checks the repo's CLAUDE.md names, with the brief's prefix, through `loom flow wait` when one compiles (in loom, `go test ./tools/claudemd/`, and `go run ./tools/claudemd -v` for the budgets' headroom).
- It commits by pathspec in the repo's convention (in loom, `docs(<scope>): …`). `DONE` with no commits says nothing needed changing.

`flow:habits`' description names the doc writer among the agents that preload it. Its "Working from the plan or brief" section is about plan code, and the doc writer's definition says its brief carries none; its "Repo habits" (the prefix, the shared index, commits by pathspec, long steps through `loom flow wait`, no secrets) bind the doc writer as they bind the developers.

`flow/lib/guarded-agents.txt` gains the line `doc-writer developer`.

`flow/PROVENANCE.md` gains a `## doc-writer` heading with the entry: 2026-11-10, a new agent at `model: sonnet`, `effort: high`, `maxTurns: 100`, `cacheTtl: 5m`, on the owner's instruction (this design), with §2's evidence.

### 5.2 The brief

`DocsBrief` (`internal/flowrun/briefs.go`):

```text
Update the docs for Story <id> of the plan <plan>: its section is plan lines <line>-<end>. The spec is <spec>.
The story landed as <base>..<head>: that diff is what the docs follow.
The plan's header and the later stories' **Files** lines say which story owns which doc edit: leave a doc a later story's **Files** line names to that story.
Its developer's report, whose DEVIATION lines say where the code left the plan, and so where the plan's doc edits may have gone stale: <report path>
The Critical and Important findings its reviews sent to a fix round, for lessons, each with the report that found it:
- <severity> <file>:<line> <the fix> (<report path>)

[doc-writer]
<the block>
```

The developer's report is the story developer's last one, the one whose commits the gates took. The findings are the blocking findings of the story's reviews and re-reviews that went to a fix round, in the order the run met them. Both come from memoized reports, so the brief is the same on every replay. With no findings, the findings paragraph reads `Its reviews sent no Critical or Important finding to a fix round.`

The `[doc-writer]` block (`flowconf.py show --for doc-writer`, and in `show --for supervisor` with the other roles; `ROLES` gains `doc-writer`):

```text
Prefix for every check command: <prefix>
Doc paths (change only files these match): <paths>
Generated files (never edit them by hand, Markdown or not): <generated>
Idioms beyond CLAUDE.md (some say which doc follows which code):
    <idioms>
Design registries (a story that adds what one lists edits it too):
    <registries>
```

`flowrun.Blocks` gains `DocWriter`, which `loom flow run start` fills from the `[doc-writer]` block.

### 5.3 The engine's step

In `story()`, after `planLines` and before `commitsAt(p+":done", base)`, when `State.Docs` is true, `docs(s, p, base)`:

1. `s.Row.Status` becomes `documenting`, and `before` is `e.head`.
2. The doc writer runs at `p+"-docs"` with `DocsBrief` and its pinned effort, through a loop of `review`'s shape: a `NEEDS_CONTEXT` is answered as any agent's is; a `BLOCKED`, a turn cap, a missing report or a report that doesn't parse is a failed agent, whose retry carries the manager's decision.
3. A `DONE` report parses through `TagLine`: `DOC:` and `LESSON:` lines are counted, and `FINDING:` lines parse as the reviews' do, without the design flag. A `FINDING:` line that doesn't parse fails the agent, as a review that doesn't parse does.
4. `headAt(name)`. When HEAD moved, `Effects.DocsCheck(before, head)`, memoized as `docs:<name>`. Strays ask the `docs-strayed` question (§5.5). With none, or once the manager accepts them, the story gates run: `gateLoop(s, p, base)`, over the story's commits with the pass's included, with a fix after a failure as after any of the story's gates.
5. The findings append to `e.st.Kept`. The row's `Docs` and `Lessons` are the counts summed over the pass's attempts that reported `DONE`. A pass that changed nothing leaves a note (`<story>: the doc pass changed no docs`), and so do kept findings (`<story>: the doc pass kept N findings for the final round`).

`loom flow run start` sets `State.Docs`. A state without it, written by an older build, runs no doc pass, so its replay doesn't change.

`AntiSeamBrief` gains: docs the run changed that contradict the code are findings too, each with the doc's `file:line`.

`internal/flowrun/steps.go`: `KindDocs = "docs"`; the attempt pattern gains `docs`; `StepOf` refuses a final-round docs name, as it refuses a final dev name; `QuestionStep` reads a `strays:` key as the attempt it names, as it reads `failed:`.

### 5.4 `loom flow conf docs-check`

`flowconf.py docs-check --base B --head H` runs `git diff --name-only --no-renames B..H` in the repo and prints each changed path that isn't a doc path, one per line. A deleted doc is a doc path; a rename is checked by both names. It exits 0 with no output when there is none, 1 when it printed any, and 2 on an error (a revision git doesn't know, a profile that doesn't load).

The profile's `[docs]` table holds one key, `paths`, a list of globs. `validate` refuses another key and a value that isn't a list, as it does for every table, and `doctor` reports the globs in use.

The engine reaches the tool through `DocsCheck` on `internal/cli/flow_conf.go`'s `flowConf` and on the run's `Effects` adapter (`runFX`), as it reaches conformance.

### 5.5 The `docs-strayed` question

Key `strays:<name>`, kind `docs-strayed` (`flowrun.KindDocsStrayed`); its text names the strays, and its path is the doc writer's report. It takes `accept` (keep the changes: the gates and the final review judge them), `stop`, or a decision. A decision runs a fresh doc writer whose brief carries the earlier report, the decision and the strays, with the instruction to undo the strays' changes with inverse edits and commit them, since the habits forbid `revert` and `reset`. Its `DocsCheck` runs over `before..` its own head, so a full undo clears the strays. In `CheckAnswer`'s table: `KindDocsStrayed: {[]string{WordAccept, WordStop}, true}`. `loom flow run answer`'s help and the dispatch guide's list of question kinds name it.

### 5.6 The supervisor

`flow/plugin/agents/plan-execution-supervisor.md`:

- Setup step 1 names the `[doc-writer]` block among those that go into a role's briefs as they stand.
- "Each story" gains step 7, and updating the state file becomes step 8:

  > 7. Dispatch `flow:doc-writer` with the plan's path and the story's line range, the spec's path, the story's range (its base, and its head after the last fix round), the developer's deviation list, the Critical and Important findings its fix rounds took, each with its `file:line` and the reviewer's reason, and the `[doc-writer]` block. When it committed: run `loom flow conf docs-check --base <the head before it> --head <HEAD>`; for a stray, ask `main` whether to keep it, and otherwise send a fresh `flow:doc-writer` to undo it; then dispatch `flow:verifier` with the word "story" and the story's commits. Whether or not it committed, keep its FINDING lines with the Minors for the final fix round, and write its DOC and LESSON counts in the state file.

- "After the last story", step 2's anti-seam brief gains docs the run changed that contradict the code.
- The state file's story row gains Docs and Lessons.

### 5.7 The board and the record

- `internal/flowview/detail.go`'s `rowKinds` gains `flowrun.KindDocs`. In `internal/web/static/js/flow.mjs`, `KINDS.story` gains `"docs"` and `LABEL.docs` is `"doc"`. A run without the pass shows the cell empty. The cost bar counts the doc writer under `other`, as it does any role `ROLE_CLASS` doesn't name.
- `internal/flowrun/record.go`'s story table appends two columns, `Docs` and `Lessons`, `—` for a run without the pass. Nothing parses the table's columns. `flow/plugin/docs/records.md` documents them, and the supervisor's state file carries them for the manager to copy. The cost by type gives the doc writer its own row unchanged, since `agent_costs.py` groups by agent type.
- `/flow:retro`'s cost share adds the doc passes, paired with their outcome: their cost against their Docs and Lessons counts per story, and the doc findings the final review raised anyway.

### 5.8 The run's end

Step 5 of `/flow:run` and step 9 of `/flow:supervise` add: record the run's outcome in your memory (what was built, its commits, what is still open) and nothing the repo's docs can hold; the doc passes have put the run's lessons in the repo.

## 6. Failure handling

| Case | What happens |
|---|---|
| `NEEDS_CONTEXT` | a `needs-context` question; the answer resumes the doc writer |
| `BLOCKED`, a turn cap, no report, a report or `FINDING:` line that doesn't parse | an `agent-failed` question; `retry` or a decision runs a fresh doc writer with the earlier report |
| `DONE` with no commits | no `docs-check`, no gates; a note in the record |
| a stray | a `docs-strayed` question (§5.5) |
| the story gates fail after the pass | the gate loop's fix, and its question on a second failure, as after any of the story's gates |
| the pass's findings | kept for the final round's fixers and its re-review |
| `docs-check` exits 2 | the engine's error: the run stays running for `loom flow run resume`, as when conformance fails |
| a run started before this change | no doc pass (`State.Docs` false) |

## 7. Components

| Where | What changes |
|---|---|
| `flow/plugin/agents/doc-writer.md` | new (§5.1) |
| `flow/plugin/skills/habits/SKILL.md` | the description names the doc writer |
| `flow/lib/guarded-agents.txt` | `doc-writer developer` |
| `flow/lib/flowconf.py` | the `[docs]` table, the `doc-writer` role and block, `docs-check` |
| `flow/PROVENANCE.md` | the doc writer's pins |
| `flow/plugin/agents/plan-execution-supervisor.md` | §5.6 |
| `flow/plugin/skills/run/SKILL.md`, `flow/plugin/skills/supervise/SKILL.md` | memory at the end (§5.8) |
| `flow/plugin/skills/retro/SKILL.md` | the doc passes in the cost share |
| `flow/plugin/docs/dispatch.md` | a section "The doc pass"; a row in "Route to pinned agents"; the docs step and the `docs-strayed` kind in "The run engine"; the doc writer's `5m` in the cache-lifetime list |
| `flow/plugin/docs/records.md` | the story table's columns |
| `flow/README.md`, `flow/CLAUDE.md` | 13 pinned agents, the doc writer among them |
| `internal/flowrun/` | `KindDocs`, `KindDocsStrayed`, `State.Docs`, `Blocks.DocWriter`, the row's `Docs` and `Lessons`, the step in `story()`, `DocsBrief`, the report parser, `AntiSeamBrief`, `StepOf` and `QuestionStep`, `Record`'s columns, `Effects.DocsCheck`, `README.md`'s procedure |
| `internal/cli/` | `rolePreamble("doc-writer")`, `flowConf.DocsCheck`, `runFX.DocsCheck`, `start` filling `Blocks.DocWriter` and `State.Docs`, `answer`'s help |
| `internal/flowview/detail.go`, `internal/web/static/js/flow.mjs` | the docs cell |

## 8. Testing

- **Engine** (`internal/flowrun/engine_test.go`, fake effects):
  - a pass with commits runs `docs-check`, then the story gates;
  - a pass without commits runs neither;
  - a stray asks `docs-strayed`: `accept` goes on to the gates, and a decision runs a fresh doc writer whose brief names the strays;
  - a pass's `FINDING:` reaches the final round's fixers;
  - a state without `Docs` runs no pass;
  - `TestRun_ARestartAtEverySavePointSubmitsNothingTwice` and `TestStepOf_ParsesEveryNameTheEngineSubmits` run on fixtures that include doc passes.
- **Briefs**: `DocsBrief` names the range, the later-stories rule, the findings with their reports (and the no-findings sentence), and the block; `AntiSeamBrief` has the doc line.
- **Answers**: `CheckAnswer` for `docs-strayed`.
- **Report parsing**: `DOC:` and `LESSON:` counts; a `FINDING:` line plain and in a markdown wrapper; a `FINDING:` line that doesn't parse.
- **Record**: the two columns, and `—` without the pass.
- **cli**: `rolePreamble("doc-writer")`; `runFX.DocsCheck` maps exits 0, 1 and 2.
- **flowconf** (`flow/lib/flowconf_test.py`): the default paths; `[docs]` validation; `show --for doc-writer` and the supervisor's block list; `docs-check` on a docs-only range, a stray, a generated Markdown file, a deleted doc and a rename.
- **Agents**: `flow/lib/check-agents.sh` passes the new agent (`cacheTtl`, the description's cap, the guard list, the banned tokens).
- **Board**: the docs kind in a story row (`internal/flowview`).
- Stories that change `flow/` name `bash flow/lib/test.sh` in their Verify line, which the Go suite doesn't run (`flow/CLAUDE.md`).

## 9. Known limits and later work

- No gate and no per-story review checks the pass's prose; the final anti-seam review reads it once. The retro can add a review if the records show bad doc commits.
- Approach C's candidate list, if the retro finds passes missing stale mentions.
- A profile switch (`[docs] pass = false`) for a repo that wants no pass; until then, a repo with few docs pays for a near-empty pass on every story.
- Replacing the plan's doc tasks once the records show the pass reliable, which would shrink plans by their doc tasks.
- A guard mode confining the doc writer's Write and Edit to doc paths, if strays turn up.
- A doc pass in the final round.
- `/flow:init` asking about docs that aren't Markdown (diagram sources) when it drafts a profile.
