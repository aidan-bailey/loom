---
name: supervise
description: Run a plan under the supervised option with the flow roster, from a fresh session (/clear, then /flow:supervise <plan>). It checks the repo and the plan, dispatches plan-execution-supervisor, answers its questions from the spec, and writes the run's record.
argument-hint: <plan path>
disable-model-invocation: true
---

Run the plan at `$ARGUMENTS` under the `supervised` option, with the flow
roster. You are the manager. Start from a fresh session (`/clear` first): every
turn you take re-reads this whole conversation, and during the run your turns
are the supervisor's questions and reports, so the less this context holds,
the less each one costs. Keep your own turns short for the same reason.

1. Check the repo.
   - If `.claude/agents/plan-execution-supervisor.md` exists at the top level
     (`git rev-parse --show-toplevel`), this repo has its own roster: stop, and
     tell the owner to use the repo's own supervised command. Find it under
     the repo's `.claude/commands/` or `.claude/skills/` (the one that
     dispatches `flow:plan-execution-supervisor`) and name it.
   - Run `loom flow conf doctor`. On any FAIL, show the FAIL lines
     and stop.
2. Check the plan: `loom flow check-plan $ARGUMENTS`. If it fails, show
   the FAIL lines and stop. Its OK line names the spec.
3. Read the spec. It is the only record of the design you have: answer from
   it, not from memory.
4. Check the tree is clean (`/usr/bin/env git status --short`); if it isn't, show
   it and stop. Then record:
   - `BASE_SHA` (`git rev-parse HEAD`) and the start time (`date -u +%FT%TZ`);
   - the definitions: the line `loom flow conf roster` prints
     (`loom flow roster <12 hex>`);
   - the profile: the `Profile:` line of `loom flow conf show --for supervisor`;
   - the state directory: `loom flow conf state`.
5. Dispatch one `flow:plan-execution-supervisor` with the spec's path, the plan's
   path, `BASE_SHA` and the state file
   `<state directory>/agent-reports/supervisor-<plan file stem>.md`. Don't
   retype the plan, and don't give it a `name`: a named agent joins the session
   as a teammate, and a teammate's own agents can't hand their reports back to
   it. Keep the agent id the launch returns; you message the supervisor by it.
6. While it runs, answer the supervisor's questions with SendMessage to that
   id. A question arrives as a message, or as its report when none of its
   agents is running; either way your answer resumes it. When the spec
   settles a question, answer from it. When it doesn't, ask the owner,
   then write the answer into the spec and commit it by pathspec, so the next
   question finds it there. Stage nothing else, and start no other developer or
   `flow:verifier` in this worktree.
7. For progress, read the state file (updated after each story) and
   `git log --oneline <BASE_SHA>..HEAD` (one commit per task) rather than
   asking the supervisor, which costs a turn on each side. If it goes
   quiet, check those and `ps` before assuming it died. A fresh one,
   dispatched with the same inputs, resumes from the state file. The
   supervisor asks for one itself every third story, to keep its context
   small: a report that starts `REFRESH` is that. Dispatch the fresh one at
   once, without a name, and message the new id from then on. A report that
   reaches you from an agent an earlier supervisor dispatched is that agent's
   late result: forward it unchanged to the current supervisor with
   SendMessage.
8. When its final report arrives, note the end time (`date -u +%FT%TZ`), then
   read it and the whole-implementation review. Then write the run's record
   in the format `${CLAUDE_SKILL_DIR}/../../docs/records.md` gives, at
   `~/.loom/flow/records/<key>/<YYYY-MM-DD>-<plan stem>.md`, where
   `<plan stem>` is the plan's file name without its leading date and `.md`,
   and `<key>` is what `loom flow conf key` prints (make the
   directory if it is missing):
   - the header, with `- Start:` the start time from step 4 and `End:` the end
     time, `- Definitions:` from step 4 (run `loom flow conf roster`
     again, and append ` → <its output>` only when it differs from the
     start's: the roster changed during the run), `- Profile:` from step 4, and `- Design stage:` with the
     text after the label of the plan's `**Design stage:**` line, when the
     plan has one (`/flow:draft` writes it just above `## Stories`; find it
     with `grep -m1 '^\*\*Design stage:\*\*' <plan>`; a plan written by hand
     has none, and the record leaves the line out);
   - the cost: `loom flow costs session --since <start time> --until <end time>`,
     pasted whole (one output per manager session, by id, when the run spanned
     several);
   - the story rows from the state file, and notes. Facts only: retros are the
     owner's call, through `/flow:retro`.

   `~/.loom/flow/records/` is no repository: the record is not committed
   there. When the supervisor block's `Record copy in the repo:` names a directory,
   write the same file there too and commit it in this repo by pathspec.
9. Record the run's outcome in your memory, when you keep one (what was
   built, its commits, what is still open), and nothing the repo's docs can
   hold: the doc passes put the run's lessons in the repo. Then tell the owner
   what was built, what failed, and what needs them.
