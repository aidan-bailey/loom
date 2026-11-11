---
name: draft
description: Draft a supervised plan from its committed spec and skeleton with the flow roster, from a fresh session (/clear, then /flow:draft <plan>): drafters settle the names contract in a question round, then write the stories; it joins and checks them, one recon checks the seams, fresh drafters fix what the checks find, and it commits the plan for /flow:run.
argument-hint: <plan path>
disable-model-invocation: true
---

Draft the plan at `$ARGUMENTS`: a skeleton the design session committed beside
its spec, holding the header with its `**Spec:**` line, the file structure,
the names contract, the `## Stories` list and the marker line, and no
`## Story` heading. You are the manager: you decide, and agents write and
read. Start from a fresh session (`/clear` first): every turn you take re-reads
this conversation, so keep each turn short, and work from the agents' reports,
never from the drafts. You write only the names contract, the story entries, the plan's header,
`STATE.md`, the per-story findings files and the spec's answers, and never a
story's code. Ask the owner only what the spec and the contract don't settle,
and write each answer into the spec, committed by pathspec as soon as it is
written, in the repo's commit convention, never with `--no-verify`; on a hook
failure, show its output and stop.

Below, `D` is the plan's drafts directory, which step 1 makes.

Dispatch every agent without a `name`: a named agent joins the session as a
teammate, and its own hand-backs then fail. Dispatch a batch in one message,
end your turn, and take each report as it arrives. Drafters are fresh agents,
never forks: a fork re-reads this whole conversation on every request. Every
fix job goes to a fresh `flow:drafter`, and its brief holds the draft
`D/S<id>.md`, its findings, the plan's path (the names contract sits above its
`## Stories`), the spec's path and the drafter's profile block; it runs at the
drafter's pinned high, whatever the story's shape: its fixes are decided
before it starts, and the run's full review checks the story again. Steps 2,
3 and 5 name only the findings. Never
send a running drafter new work by message: it arrives framed as another
agent's request. Only your answer to a drafter's own NEEDS_CONTEXT report, and
step 2's message that ends the question round, go to a drafter, and each
resumes it.

1. **Start or resume.**
   - If `.claude/agents/plan-execution-supervisor.md` exists at the top level
     (`git rev-parse --show-toplevel`), this repo has its own roster: stop,
     and name the repo's own drafting command (under its `.claude/commands/`
     or `.claude/skills/`). If `$ARGUMENTS` isn't under that top level, stop:
     the plan belongs to another worktree or repo.
   - Run `loom flow conf doctor`. On any FAIL, show the FAIL lines
     and stop.
   - Run `loom flow check-plan --skeleton $ARGUMENTS`. On any FAIL, show the
     FAIL lines and stop: the owner fixes the skeleton in a design session,
     or marks the line `(Story N proposes)`.
   - Make D: `loom flow conf drafts <plan file stem>` makes it,
     keeps it out of git and prints its path.
   - Check the tree is clean (`/usr/bin/env git status --short`). A resumed
     run (`D/STATE.md` exists) may show the plan itself modified, since the
     join rewrites it; anything else, show it and stop.
   - If `D/STATE.md` exists, read it and carry on from its **Phase** line.
     Before you dispatch again an agent it lists in flight, check `ps` and the
     files the agent writes: its report may be on its way, or its file done.
     An agent that finished after a `/clear` handed its report to the cleared
     conversation, and its transcript keeps it: the input of the
     `SubagentHandback` call at the end of
     `~/.claude/projects/<project>/<old session id>/subagents/agent-<id>.jsonl`.
     Read it there, with the command in `${CLAUDE_SKILL_DIR}/../../docs/dispatch.md`'s
     resume rules, and dispatch again only an agent that has no hand-back.
     Go by the job its **In flight** line names. A drafter on a questions job
     has reported when its `D/questions/S<first id>.md` exists, its hand-back
     aside: dispatch again, fresh, only a drafter without one, with step 2a's
     brief. When drafting starts in a session other than the one that
     dispatched the round (after a `/clear`), its drafters can't be resumed:
     dispatch each fresh with step 2b's drafting brief. A drafter on a drafting job
     is dispatched again, fresh, with step 2's drafting brief plus "finish
     `D/S<id>.md` without redoing what is there". A drafter on a fix job (step
     2's amendment fixes, step 3's fix jobs, step 5's fixers) is dispatched
     again, fresh, with its fix-job brief as the intro gives it, with that
     step's findings (for step 5, `D/findings/S<id>.md` and the story's
     `start` from `D/join.json`), plus "some of these may already be fixed in
     `D/S<id>.md`; fix what remains". A `flow:recon` or `flow:premise-check` restarts
     fresh unless its findings file shows it reported. Otherwise record `REV`
     (`git rev-parse HEAD`) and the start time (`date -u +%FT%TZ`), and write
     `STATE.md` (below).
   - Either way, add this session's id to `STATE.md`'s **Sessions** line: the
     output of `echo "$CLAUDE_CODE_SESSION_ID"`. After a `/clear` it is the
     current session's, and the stem of the transcript `loom flow costs
     session <id>` reads. Take it from neither of two lookalikes: the
     claude.ai id in the commit-trailer reminder (`session_01…`), which no
     transcript has, and the id in a `/tmp/…/<id>/tasks/` output path, which
     is keyed by the process's first id, from before any `/clear`.
   - Read the spec (the plan's `**Spec:**` line) and the skeleton, and run
     `loom flow conf show --for drafter` once: its output is the
     drafter's profile block.
2. **Questions, then drafting.** One `flow:drafter` per one or two stories,
   all in one parallel batch. Give each the plan's path, the spec's path, D,
   its story ids and their files `D/S<id>.md`, and the drafter's profile
   block. Dispatch with `effort: xhigh` a drafter whose story's entry names
   one of the block's landmine markers; this sentence is the instruction the
   Agent tool needs to set a per-call effort. Every other drafter runs at its
   pinned high, a story whose shape only the block's notes describe included.
   - **2a. Questions.** The brief adds: "Question round: write only
     `D/questions/S<first id>.md`, then report." The drafter reads what its
     entries need and writes the file's five lists (its definition gives
     them); its hand-back is the path and each list's count. A questions file
     that isn't the five lists goes to a fresh drafter with the same brief;
     don't guess its content. A drafter that drafts in the round keeps its
     `D/S<id>.md`: its 2b message adds "finish `D/S<id>.md` against the
     settled contract".
   - **The wait.** Wait until every drafter of the batch has reported, and
     settle everything in one pass: read the questions files; write each
     decision into the names contract, choosing where a user's proposal and a
     definer's differ; amend the entries; settle each proposed split, by
     replacing the entry with its two halves and moving each contract owner
     (`Story <id>`) to the half that defines the name, or by rejecting it
     with its reason in the story's row of `STATE.md`; take to the owner
     only what the spec doesn't settle (see the intro); then run
     `loom flow check-plan --skeleton $ARGUMENTS` until it passes. Record in
     each story's row of `STATE.md` what was settled for it. When accepted
     splits put the plan over its story budget, the check fails: take it to
     the owner, who gives a `**Size:**` reason or stops `/flow:draft` to cut
     the spec into an epic in a design session; never cut one yourself.
   - **2b. Drafting.** Resume every drafter of the batch with SendMessage to
     the id on its report: "The contract is settled. Re-read `## Names
     contract` and your entries, and draft.", then the contract lines that
     changed among those its questions file raised or its stories own. A
     drafter whose split was accepted drafts both halves; if it already held
     two stories, dispatch a fresh drafter with step 2's drafting brief for
     the new half.
   - After the round, a drafter that finds a gap in the contract stops and
     reports NEEDS_CONTEXT. Amend the names contract (in the skeleton, above
     `## Stories`), run the skeleton check again, then answer it with
     SendMessage to the id on its report.
   - When an amendment changes a name that other drafts use, wait until their
     drafters have reported, then give each affected draft a fix job whose
     findings are the amendment. An amended entry needs no fix job: the join
     writes each story's opening lines from its entry.
3. **Join and check.** Run, in order:
   - `loom flow join $ARGUMENTS`, which writes the plan from the
     skeleton's head, each story's opening lines from its entry, and
     `D/S*.md` (never `STATE.md`) and writes
     `D/join.json`. It fails on a missing or unlisted draft, and on a draft
     that holds the marker line outside a fence;
   - `loom flow check-plan $ARGUMENTS`;
   - `loom flow premises $ARGUMENTS --rev <REV>`.

   A join or `loom flow check-plan` failure goes to a fresh drafter as a fix job,
   its findings the failure text, unless it names an entry: fix that in the
   skeleton. The checks cite plan lines (`line <n>`,
   `plan:<n>`), and a fixer works in its draft: give it draft line
   k = n − start + 1, with `start` the story's `start` in `D/join.json`. A
   finding on a line inside a story's `entry` in `D/join.json` is the
   entry's: amend the entry, never a draft.
   Send what `loom flow premises` flags to one `flow:premise-check` per batch of
   flags, one per few stories when there are many, each with the plan's path,
   `<REV>` and its flagged lines, and append each report on arrival to
   `D/findings/premises.md`. A COSMETIC verdict becomes a fix job,
   its lines given as draft lines k. A DESIGN verdict is yours to settle, and
   the owner's when the spec doesn't settle it: write the owner's answer into
   the spec and commit the spec by pathspec at once (see the intro). When
   `loom flow check-plan` finds a landmine marker in a light story, you decide: make
   the story deep,
   or keep it light with a reason in its Review line that names the marker,
   and record which in `STATE.md`. Change the story's entry under
   `## Stories`; the next join writes it into the story. Give each
   draft one fix job for everything this step found in it. A `**Parallel:**`
   failure is yours, not a drafter's: drop the pair from the skeleton's line
   (its stories then run in order), and name it in the hand-over.

   After these fix jobs, run this step once more: the join, `loom flow check-plan`,
   `loom flow premises`, and a `flow:premise-check` on any new flags. Step 5 bundles
   that run's rows. It is one more check, not a second fix round.
4. **Seams.** Run `loom flow packet $ARGUMENTS --seams --rev <REV>`.
   It writes `D/packets/seams.md`, and fails with "the plan is out of date
   with its drafts; re-run loom flow join" when the plan isn't the join of its
   drafts: join (step 3) after every change to a draft or to the skeleton,
   before the packet. Dispatch one `flow:recon` with the packet's path, the plan's
   path (the names contract is above its `## Stories`), the design registries
   from the drafter's block, and two questions:
   - Do the contracts between stories match? Every name a story uses from
     another must be defined there, with the same name, type, signature and
     visibility.
   - Which files outside every story's **Files** line list or count what the
     plan adds or changes? Start from the design registries.
   - When the skeleton has a `**Parallel:**` line: does each declared pair's
     second story use anything its first creates? The packet's Declared pairs
     section lists the names; check files, flags and behaviour too.

   It doesn't re-check quotes of the tree (premise-check settled those) or
   review code. Write its report on arrival to `D/findings/seams.md`.
5. **Fix.** Read the findings, never the drafts.
   - Write each cross-story decision into the names contract. A design
     question the spec doesn't settle goes to the owner: write the answer into
     the spec and commit the spec by pathspec at once, so a resumed run finds
     a clean tree.
   - A finding that names a story's entry (the seams packet cites one as
     `the Story <id> entry`) is yours: amend the entry, and join again.
   - Name in the contract each text a later story quotes from an earlier one
     (its "current" block). Every fixer leaves a named text unchanged, so
     parallel fixers can't break a later story's quote.
   - Before the fixers start, write `D/findings/S<id>.md` for each story with
     findings: that story's premise-check rows still open after step 3's last
     run (they cite that run's join) and the seams findings that name it. A
     COSMETIC row step 3 already fixed isn't bundled again. List each file in
     `STATE.md`'s **Findings files**.
   - Dispatch one fresh `flow:drafter` per story with findings, to fix its draft in
     place. Its findings are the path of that story's `D/findings/S<id>.md`
     and the story's `start` from `D/join.json`, so that it turns `plan:<n>`
     into draft lines.
   - Then run step 3 again, and step 4 too when a fix changed the contract.
   - A finding still open after its fix round goes to the owner: there is no
     second automatic round.
   - Drop from the `**Parallel:**` line a pair the seams findings refute, and
     name it in the hand-over.
6. **Commit and hand over.**
   - Price the stage, unless `STATE.md`'s **Design stage** slot is already
     filled (a session died before the commit): then reuse that line and
     don't re-price. To price, run
     `loom flow costs session <id> --since <start>` for each
     session id on `STATE.md`'s **Sessions** line, and add up the `USD` and
     `USD incl. est.` of each **Total** row. Note the end time
     (`date -u +%FT%TZ`) and the plan's line count (`wc -l < $ARGUMENTS`).
   - Write the Design stage line (below) into `STATE.md`'s **Design stage**
     slot and into the skeleton's head, directly above `## Stories`. Then run
     step 3's `loom flow join` and `loom flow check-plan` again, and fix what fails
     before the commit.
   - Commit the plan by pathspec, and nothing else, in the repo's commit
     convention (its CLAUDE.md): `git commit -m "<message>" -- <plan>`. Every
     spec answer is already committed (steps 3 and 5). Never pass
     `--no-verify`: when the pre-commit hook fails the commit, show its
     output and stop.
   - Set the **Phase** to committed. Tell the owner what was drafted, what
     the checks and the seams recon found, and anything still open, and end
     with: run `/clear`, then `/flow:run <plan>`.

The Design stage line is one physical line:

`**Design stage:** /flow:draft session <ids>, <start> to <end> UTC: $<logged> logged, $<incl. est.> with estimated thinking; <n> plan lines, $<logged per 1,000 lines> per 1,000 lines.`

It leaves out the design session's own cost, which the owner measures by
hand. `/flow:run`'s engine copies it into the run's record.

Write each findings file with the Write or Edit tool, or with a quoted heredoc
(`<<'EOF'`): the reports are full of backticks, which an unquoted heredoc runs
as commands. They live in `D/findings/`, a subdirectory, because a top-level
`S<id>-x.md` would match the join's draft pattern.

When a drafter goes quiet, check its draft file and `ps`, then dispatch a fresh
drafter with the brief of the job it was on: step 2a's in the question round,
else step 2's drafting brief plus "finish `D/S<id>.md` without redoing
what is there"; never message the quiet one. A premise only a live check can
settle goes to a `flow:recon`, with read-only requests (GET only).

`STATE.md` lets a fresh `/flow:draft` on the same plan resume. Rewrite it whole
after every batch of dispatches and every report: `mkdir -p D/findings`, then a
quoted heredoc (`cat > D/STATE.md <<'EOF'`). The heredoc's text and its closing
`EOF` line start at column 0 (the block below is indented four spaces only to
set it off here; indented, the heredoc never terminates):

    # Draft state: <plan file stem>
    **Phase:** <questions | drafting | checking | seams | fixing | committed>. Done: <what>. Next: <what>.
    **Started:** <start time>. **REV:** <sha>. **Sessions:** <session id of each /flow:draft session>.
    **Design stage:** <the line step 6 writes; none until then>

    | Story | Tier | Effort | Drafter | Deviations settled | Findings (premises, seams) | Fixer |
    |---|---|---|---|---|---|---|

    ## Findings files
    - <D/findings/premises.md, seams.md or S<id>.md: which agent (or the manager), and when>

    ## Open questions
    - <question, or none>

    ## In flight
    - <agent id>: <questions | drafting | fix> <story ids>
    EOF
