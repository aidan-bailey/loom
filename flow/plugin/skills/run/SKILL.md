---
name: run
description: Run a plan with loom's run engine from a fresh session (/clear, then /flow:run <plan>). The engine drives every story through headless flow agents and gates, and you, the manager, answer each question it parks on, from the spec or by asking the owner. It writes the run's record itself.
argument-hint: <plan path>
disable-model-invocation: true
---

Run the plan at `$ARGUMENTS` with loom's run engine. You are the manager.
Start from a fresh session (`/clear` first): every turn you take re-reads this
whole conversation, and your turns are the engine's questions, so keep them
short.

1. Run `loom flow run start $ARGUMENTS`. If it refuses, show its lines and
   stop. (It refuses unless `claude_permission_mode` is `auto` or
   `bypassPermissions`, since its agents run headless.) Read the spec its
   plan names: it is your only record of the design; answer from it, not
   from memory.
2. Run `loom flow run wait <run>` as a Bash call with `run_in_background`, and
   end your turn. It returns only when the run needs you or has ended.
3. When it returns parked, read the question and the paths it names. Answer
   from the spec, the plan or the code when they settle it. When they don't,
   ask the owner; then write the answer into the spec and commit it by
   pathspec, so a later question finds it there. Commit before you answer:
   `answer` takes commits that touch only the spec and the plan with it, and
   any other commit parks the run's next step as `tree-moved`. Give your
   answer on stdin to `loom flow run answer <run> --from spec` (you settled
   it without the owner) or `--from owner` (you asked the owner), in the form the question
   says it takes (a decision as text, or one of its words): the record counts
   them, to show how many questions a judge could have answered. A failed
   agent takes a decision too: it is retried with your decision in its
   brief. When it left no report, the path to read is its job's
   `output.log` (stream JSON; a `result` event, when there is one, holds
   its final message). When you edit the plan, `answer` re-reads its
   stories' line ranges for the later briefs, and refuses a plan check-plan
   fails or one that adds, removes or reorders stories. Then start the
   background `wait` again. Stage nothing else, and start no developer
   or verifier in this worktree: the engine does.
4. For progress, run `loom flow run show <run>` rather than reading files.
   If `wait` or `show` says the engine was lost, read the cause its line
   ends with (the engine's last log line): when it is outside the run (a
   full disk, a missing tool), clear it or tell the owner first, since a
   resume would hit it again. Then run `loom flow run resume <run>` and wait
   again. `wait` joins a restarted daemon again by itself; if it exits 125,
   it couldn't follow the run, which goes on: start the background `wait`
   again.
5. When it returns ended, read the record it names. When the profile's
   `Record copy in the repo:` names a directory, commit the copy there by
   pathspec. Record the run's outcome in your memory, when you keep one (what
   was built, its commits, what is still open), and nothing the repo's docs
   can hold: the doc passes put the run's lessons in the repo. Then tell the
   owner what was built, what failed, and what needs them.
