# Flow: a names contract settled before drafting

Status: design approved section by section 2026-11-11; awaiting review of this file.

`/flow:draft` dispatches every drafter at once against the skeleton's names contract, and a drafter that needs a shared name the contract lacks stops with NEEDS_CONTEXT. Only the drafters that *use* a shared name stop. The drafters that *define* one write their own, so the amendments the manager makes for the askers contradict drafts already written, and each of those drafts takes a fix job. This spec makes the contract complete in form before drafting (a skeleton lint), settles every cross-story name in one question round before any drafter writes, and has the join write each story's opening lines from the skeleton, so an amended entry never needs a fix job.

## 1. Problem

Evidence: an 18-story plan in a client repo, drafted 2026-11-11 by 11 drafters (Sonnet 5.5, high), $62.47 of drafting by the time the last drafters were still writing, against $0.20 to $1.46 a story for the earlier `/flow:draft` runs on the machine. Its drafts average about 105 KB a story, against 10 to 16 KB in earlier plans of the same repo, so most of the cost is size; per KB of draft the run was no dearer than earlier ones. The waste is in the fix jobs:

- **The askers and the definers diverged.** 6 of the 11 drafters stopped with NEEDS_CONTEXT within 1 to 10 minutes of dispatch, and the manager amended the contract and answered them within 9 minutes. The other 5 never asked, and among them were the definers of exactly what the askers asked about: the story that owns the snapshot rows (asked about by the drafters of three other stories), the story that owns the frontend workspace's seams (asked about by two), the story that owns `RunHandles`. They drafted against the old contract or invented their own names; one put `pickerOpen` on the session where the amendment put it on the workspace. `STATE.md` planned fix jobs for about 10 of the 11 drafts.
- **The contract was incomplete in form.** `ResearchHubs::changed(scope_key, &ResearchId)` couldn't be implemented (the publish needs the store and the jobs), `publish(…, running)` named an undefined `running`, `CollectHandles` listed its fields in prose, and `search(handles, …)`, `run_stale(…, cancel)`, `delete_snapshot(..)`, `open_tab(.., …)` and `subscribe(id, onEvent)` left parameters untyped or elided.
- **The contract left shared pieces unowned**: who writes a snapshot row and the JSON of its `span`, the snapshot tables' columns, the collector's shared commit helper, the workspace's seams, the IPC method list, the test ids.
- **A contract name clashed with the tree**: `sources_search` already belonged to another tool family, and two existing tests forbid one name in two families.
- **Copied opening lines.** Each draft copies its story's opening lines from the skeleton entry, so every entry the manager widened (a file added to **Files**, a **Verify** that runs one more test) became a fix job whose only finding was "copy the new line": 8 of the fix-job rows held one.

The design session's five recon agents ($8.30) checked facts about the tree, not the contract.

## 2. Decisions

- **Fix both the contract's form and the drafting protocol** (owner, 2026-11-11: options 1 and 3 together). A skeleton lint makes the contract complete in form; a question round settles what the lint can't see before anyone drafts. Rejected: one contract-audit agent at the end of the design session (it writes no code, so it misses the gaps that show only while writing, and definers would still invent).
- **A contract line may hand its exact form to a story's drafter** with `(Story N proposes)` (owner, 2026-11-11, option B). The lint waives that line's incomplete forms, and the question round sends it to Story N's drafter. Rejected: strict only (the design session would guess types it hasn't read); warnings only (nothing would change).
- **The join writes each story's opening lines from its skeleton entry** (owner, 2026-11-11, option A). Drafts hold no copy, so amending an entry costs nothing at any stage. Rejected: keeping the copies and catching entry problems in the question round (entry problems found while drafting would still cost a fix job each).
- **Every drafter waits for the round's end**, including one that asks nothing: a name it defines may change in the manager's settlement.
- **Each line that defines a name names its owning story.** The lint needs the owner to route a `(Story N proposes)` line, and the question round needs it to tell a definer which of its names others depend on.
- **Question reports are files**, `D/questions/S<first story id>.md`, not only hand-backs. A file survives a `/clear`, so a resumed `/flow:draft` sees who reported without reading old transcripts, and the join ignores subdirectories of D as it ignores `D/findings/`. (A refinement made while writing this spec; the approved design had the reports as hand-backs.)
- **The full `check-plan` doesn't check the contract.** A plan written by hand has none; only `--skeleton` checks it.
- **No pin changes.** The question round is a new job for `flow:drafter` at its pinned model and effort.

## 3. Scope

In: the skeleton's contract rules and entry checks (`check_plan.py --skeleton`); the join's opening lines and `join.json`'s `entry`; the question round in `/flow:draft` (step 1's lint, step 2's two phases, the `questions` phase of `STATE.md`, the resume rule); the drafter's question-round job and its no-copy rule; the dispatch guide's skeleton, drafting and fixing sections; one sentence in `session/loom_context_flow.md`; `flow/README.md`; a `flow/PROVENANCE.md` entry; tests.

Out: a check of contract names against the tree (the question round's drafters grep for clashes); a seams check of drafts against the contract (the seams recon already compares the stories with each other); a second question round; changes to the run (`/flow:run`, `/flow:supervise`), whose joined plan looks as it does today.

## 4. Behaviour

### 4.1 The contract's rules

The skeleton's names contract sits under the exact heading `## Names contract`, above `## Stories`. In it:

1. **Every shared name is written in full.** A function or method gives every parameter's name and type and its return type. A type lists its fields with their types, inline (`{ a: T, b: U }`, or a tuple's `(T, U)`) or in the bullets nested under it, or says `(opaque)` when no other story reads its fields. No `..`, `...` or `…` stands for parameters or fields.
2. **Every line that defines a name names its owning story**: `Story <id>` on the line or on a bullet it is nested under, with `<id>` a story in the list.
3. **A line may hand its exact form on**: `(Story N proposes)`, with N in the list, waives rule 1 on that line and names its owner.

### 4.2 `loom flow check-plan --skeleton <plan>`

It reads the plan above the marker line and fails, one `FAIL` line each, on:

- **The heading.** No `## Names contract`, or no marker line.
- **Rule 1**, over every backticked span in the contract section:
  - a `..`, `...` or `…` inside a span's parentheses or braces (not a range such as `0..5` or `..=`);
  - a parameter of a span shaped `name(args)` that is a lone identifier starting in lower case (`scope_key`, `onEvent`), unless it is a primitive type (`bool`, `f64`, `u32`, `usize`, `string`, `number`, `int`, `float64`, …) or a receiver (`self`, `&self`, `&mut self`, `this`);
  - a declared `struct`, `class`, `interface` or `enum` with no `{` in its span, no tuple form, no `(opaque)` on its line and no bullet nested under it.

  Not checked as signatures: attributes and decorators (a span starting `#[` or `@`); a span holding a placeholder in angle brackets (`<name>`), which states a pattern; a visibility such as `pub(crate)`; a call, known by a string or number literal among its arguments or by the name `Some`, `Ok` or `Err`.
- **Rule 2**: a line with a signature or type declaration whose line and enclosing bullets name no listed story.
- **Rule 3**: a `(Story N proposes)` whose N isn't listed.
- **The entries**: each `## Stories` entry has its **Developer**, a **Files** line naming a backticked path, an **Interfaces** line, a **Verify** line naming a backticked command, a **Review** line (when present) of deep or light with an optional reason, and a **Reading** line; the entry grammar the join reads (`- **Story <id>**` at column 0).

It passes with `OK: <plan>: skeleton; <n> stories; contract <m> lines`. Exit codes are `check-plan`'s.

Measured on the client run's committed skeleton with a prototype of these rules: 24 flags on 12 of the contract's 219 lines, of which 23 are real (every shortened signature a drafter later asked about, and a store type that needs `(opaque)`) and one is not (`SessionAnchor::research(id)`, an expression; typing it costs one edit). Without the exemptions above the prototype flagged 36, 12 of them false.

The design session runs it until it passes before it commits the skeleton (the dispatch guide's design-session steps, and one sentence in `session/loom_context_flow.md`). `/flow:draft` runs it in step 1, beside `loom flow conf doctor`, and stops on a failure; it runs it again after each amendment of the contract or the entries.

### 4.3 The question round

`/flow:draft`'s step 2 has two phases.

**2a. Questions.** One `flow:drafter` per one or two stories, all in one parallel batch, at the pins and effort the skill gives drafting today. The brief is the drafting brief plus: question round; read what your entries need; write only `D/questions/S<first id>.md`. The file holds four lists, each `none` when empty:

1. the names its stories use from another story that the contract doesn't give exactly, or that can't work against the code (a clash with an existing name included), each with its proposed exact form;
2. the names its stories define that another story uses (another entry's **Interfaces** line or paragraph names it, or the spec says another story reads it) and that the contract doesn't give exactly, each with its proposed exact form;
3. each contract line marked `(Story <one of its ids> proposes)`, with its exact form;
4. each entry line that can't hold: a file the story must also touch, a **Developer** that doesn't fit, a **Verify** that misses its tests.

Its hand-back is at most 5 lines: the file's path and the count in each list.

**The wait.** The manager waits until every drafter of the batch has reported. Then, in one pass, it writes each decision into the contract, choosing where a user's proposal and a definer's differ; amends the entries; takes to the owner only what the spec doesn't settle, writing each answer into the spec and committing it at once; and runs `check-plan --skeleton` again. `STATE.md`'s rows record what was settled.

**2b. Drafting.** The manager resumes every drafter of the batch with SendMessage: the contract is settled; re-read `## Names contract` and your entries, and draft. The message lists the contract lines that changed among those the drafter's file raised or its stories own. A drafter whose cache lapsed during the wait writes its context again on the resume.

**After the round**, a gap that shows only while drafting takes today's path: NEEDS_CONTEXT, an amendment, the answer, and a fix job for each draft that used the old name once its drafter has reported.

**`STATE.md`** gains the phase `questions`, before `drafting`, and each **In flight** line names its job: questions, drafting or fix.

**Resuming.** A drafter whose questions file exists has reported, whether or not its hand-back reached the session. A resume in the `questions` phase dispatches again, fresh, only the drafters without a file. When the drafting phase starts in a session other than the one that dispatched the round (after a `/clear`), the manager dispatches each drafter fresh with the drafting brief, since a cleared session's agents report to the cleared conversation.

A drafter that goes quiet in the round is handled as a quiet drafter is today: check its file and `ps`, then dispatch a fresh one with the same brief.

### 4.4 The join writes the opening lines

A draft opens with `## Story <id>: <title>`, then its paragraph and tasks; it holds no **Developer**, **Files**, **Interfaces**, **Verify** or **Review** line. `loom flow join`:

- reads each entry's opening lines from the `## Stories` list above the marker, in the entry's order, without **Reading**, and de-indents them to column 0;
- writes each story as its draft's heading, a blank line, the entry's opening lines, a blank line, and the draft from its second line on (a blank second line is not doubled), so the joined plan is shaped as today and `check-plan`, the run, the packets and the flow view read it unchanged;
- drops a block of copied opening lines at the head of a draft's body (a draft written before this change) and prints `S<id>.md: dropped its copied opening lines; joined the entry's`;
- still fails on a draft whose heading id doesn't match its file, a missing or unlisted draft, and the marker line outside a fence; and now on an entry without its required opening lines.

`join.json` keeps `start` and `lines` converting body text: draft line k ≥ 2 sits at plan line `start + k - 1`, so a fixer's conversion (k = n − start + 1) is unchanged. A new `entry: [first, last]` gives the plan lines of the inserted block. A check finding that cites those lines goes to the story's entry, which the manager amends, never to a fix job. The seams packet's citation of a line in that block names the entry.

The drafter's rule "copy each story's opening lines verbatim" becomes "report an entry line that can't hold"; the fix-job finding "an entry line to copy again" and step 3's "have the draft's fix job copy the new line" go.

## 5. Failure handling

- **A skeleton that fails the lint** stops `/flow:draft` in step 1 with the `FAIL` lines, as the doctor's FAILs do; the owner fixes it in a design session, or marks the line `(Story N proposes)`.
- **A questions file that doesn't parse as four lists** goes back to a fresh drafter with the same brief; the manager doesn't guess its content.
- **A drafter that drafts in the round** (writes `D/S<id>.md`): the manager keeps the file, settles the contract, and resumes the drafter with the drafting brief plus "finish `D/S<id>.md` against the settled contract".
- **A settlement that changes a name a drafter of the round didn't raise and doesn't own** reaches it through the resume message's re-read of the contract, and the seams check after the join catches what it misses.
- **A join that drops copied lines** says so in its output; the entry is the declared source, so no text a drafter was allowed to change is lost.

## 6. Components

| File | Change |
|---|---|
| `flow/lib/check_plan.py` | `--skeleton`: the contract rules (§4.2) and the entry checks |
| `flow/lib/plan_join.py` | opening lines from the entries, the dropped-copies note, `entry` in `join.json`, `load_map` reading it |
| `flow/lib/plan_packet.py` | the map check and citations with `entry` |
| `flow/plugin/skills/draft/SKILL.md` | step 1's lint; step 2's question round and resume; the `questions` phase; the copy-line text goes |
| `flow/plugin/agents/drafter.md` | the question-round job; no copied opening lines; its description names the round |
| `flow/plugin/docs/dispatch.md` | the skeleton's contract rules and its lint in the design-session steps; drafting with the question round; the join and `join.json`; fixing without copy-line jobs |
| `session/loom_context_flow.md` | the design session runs `loom flow check-plan --skeleton <plan>` until it passes before committing |
| `flow/README.md` | `check-plan --skeleton` and the join's opening lines |
| `flow/PROVENANCE.md` | an "All agents" entry: the drafter's question round and no-copy rule, no pin changed, on the owner's instruction, with §1's evidence |

`internal/cli`'s `loom flow check-plan` passes its arguments to `check_plan.py`, so `--skeleton` needs no Go change. A concurrent branch removes `/flow:supervise` and edits `session/loom_context_flow.md` and the dispatch guide; whichever merges second resolves the overlap.

## 7. Testing

`bash flow/lib/test.sh` runs them, with the agent lint.

- `check_plan_test.py`, both sides of every rule: `..` in a signature fails, a range passes; a bare parameter fails, `f64`, `&self` and `this` pass, camelCase fails; a fieldless struct fails, `(opaque)`, inline fields, a tuple struct and nested field bullets pass; an attribute, a `<name>` pattern, `pub(crate)` and a call with a literal pass; a defining line with no owner fails, an owner on an enclosing bullet passes; `(Story N proposes)` waives its line, `(Story 99 proposes)` with no Story 99 fails; a missing heading or marker fails; each entry check, red and green.
- A fixture modelled on the client run's contract lines, anonymised (no client names: `lib/check-agents.sh` checks the banned tokens), holding the lines the lint must flag and the lines it must pass, with its expected flags.
- `plan_join_test.py`: the opening lines come from the entry; copied lines are dropped and reported; an entry without its fields fails; `start` converts body lines and `entry` brackets the inserted block; joining twice gives the same file.
- `plan_packet_test.py`: a citation inside the inserted block names the entry; the map check passes on a fresh join.
- The drafter's and the skill's text: the agent lint (`lib/check-agents.sh`) and a read-through against §4.3.

Afterwards the first plan drafted on the new flow is compared with §1's run: fix jobs per story, and the **Design stage** line's dollars per 1,000 plan lines.

## 8. Known limits and later work

- The lint checks form, not truth: an omission (a writer nobody named) and a clash with the tree pass it, and the question round is what catches them.
- The parameter rule is a heuristic over backticked spans. A contract written as an expression rather than a signature can draw a false flag; the cure is to type it.
- Rule 2 accepts any `Story <id>` on the line or an enclosing bullet, including one that names a later replacer rather than the owner.
- Drafting starts after the slowest drafter's questions, about 10 minutes in §1's run.
- The saving is unmeasured until the comparison in §7.
