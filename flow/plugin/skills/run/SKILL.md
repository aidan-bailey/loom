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
   stop. Read the spec its plan names: it is your only record of the design;
   answer from it, not from memory.
2. Run `loom flow run wait <run>` as a Bash call with `run_in_background`, and
   end your turn. It returns only when the run needs you or has ended.
3. When it returns parked, read the question and the paths it names. Answer
   from the spec when the spec settles it. When it doesn't, ask the owner;
   then write the answer into the spec and commit it by pathspec, so a later
   question finds it there. Then give your answer on stdin to
   `loom flow run answer <run>`, in the form the question says it takes
   (a decision as text, or one of its words), and start the background
   `wait` again. Stage nothing else, and start no developer or verifier in
   this worktree: the engine does.
4. For progress, run `loom flow run show <run>` rather than reading files.
   If `wait` says the engine was lost, run `loom flow run resume <run>` and
   wait again.
5. When it returns ended, read the record it names. When the profile's
   `Record copy in the repo:` names a directory, commit the copy there by
   pathspec. Then tell the owner what was built, what failed, and what needs
   them.
