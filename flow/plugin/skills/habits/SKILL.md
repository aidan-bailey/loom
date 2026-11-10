---
name: habits
description: The binding habits of the developer subagents (junior-dev, intermediate-dev and senior-dev) and the doc writer, which preload this skill through their skills field. Not for the main session or any other agent to invoke.
user-invocable: false
---

# Developer habits

These habits bind every developer subagent and the doc writer. You work in the repository the
session is in, and its CLAUDE.md files are its rules: where one of them and a
habit here differ, the CLAUDE.md wins. Your agent definition adds your lane:
which decisions are yours to make.

## Working from the plan or brief

- Follow the dispatching prompt exactly, and the plan slice it names when there
  is one; with no plan, the dispatch brief is your plan. Where the plan or brief
  gives code, that code is the spec: implement it as written, and don't
  redesign it.
- Put plan code in place from the plan file by its line numbers rather than
  typing it out: retyped code is paid for as output, takes longer and can
  drift from the plan. The lines between a block's fences are its code:

      plan = open("<plan path>").read().split("\n")
      def L(a, b):  # plan lines a..b, inclusive
          return "\n".join(plan[a - 1:b]) + "\n"
      def edit(path, old, new):
          s = open(path).read()
          assert s.count(old) == 1, (path, s.count(old))
          open(path, "w").write(s.replace(old, new))
      edit("src/x.py", L(120, 140), L(150, 175))  # a "current" block -> its replacement

  Run it as `python3 - <<'EOF'` … `EOF`. A new file is
  `open(path, "w").write(L(a, b))`; an addition is an `edit` of the line it
  goes beside. Then fix what doesn't fit with small edits, and type only what
  the plan elides or leaves to you.
- Run every check a step states, and compare the result with the one the plan
  expects. When a step says the new test fails, run it and watch it fail
  before writing the code that makes it pass. When your story has a **Verify**
  command, it is your done-check: see it pass before you report DONE.
- When the given code does not compile or does not fit the codebase — a
  renamed symbol, a changed signature, a missing import, a lint the plan
  missed — make the smallest change that keeps its intent, and list every
  deviation from the given text in your report: the file, what the plan
  said, what you wrote, and why.
- A decision outside your lane, or a change that would alter behaviour or an
  interface the plan or brief defines, is a question. Stop and report
  NEEDS_CONTEXT with the question and where you stopped: the answer comes
  back as a message that resumes you with your context. Don't send the
  question with SendMessage and wait; when your turn ends, Claude Code makes
  you hand back anyway. If you can't go on at all, report BLOCKED.
- Report honestly: DONE (built as written) | DONE_WITH_CONCERNS (any
  deviation, or anything you are unsure of) | BLOCKED | NEEDS_CONTEXT. Never
  silently produce work you are unsure about.

## Repo habits

- Run builds and tests with the prefix your brief names (`direnv exec .`, for
  example), and start the services it names before the tests
  that need them. Outside the prefix, a missing tool or library looks like a
  code error.
- Before committing, run the repo's formatter on the files you changed, then
  its check form: it must come back clean.
- The git index is shared with the controller and any sibling agent, and a bare
  `git commit` takes whatever anyone has staged. Stage ONLY the files you
  touched, by explicit path (never `git add -A`, `.`, or a directory), and
  `git add` every file you created. Commit by pathspec:
  `git commit -m "…" -- <your files>`. A new file in the tree can show up in the
  shared index as soon as it exists, so logs and scratch output go under the
  state directory (`loom flow conf state`) or
  another gitignored directory.
- Before every commit, read the index directly:
  `/usr/bin/env git diff-index --cached --name-status HEAD` (the rtk-wrapped
  `git status` can hide entries), then `git diff --cached --numstat`. An added
  file of yours showing `0` added and `0` deleted lines is a ghost holding the
  empty blob: re-`git add` that path. Leave paths you didn't create alone; the
  pathspec commit excludes them.
- Never rewrite history or discard shared state: no `git reset`,
  `git commit --amend`, `rebase`, `stash` or `clean`, no `checkout`/`switch` to
  another commit or branch, and no `checkout`/`restore` of a file — undo an edit
  of yours with the inverse edit. Other agents commit and edit in this worktree:
  a rewrite orphans shas they are working against, and a restore discards their
  uncommitted work along with yours. If your edits are already in HEAD with a
  clean tree, the controller committed them; report that sha as done. To fix a
  commit of yours, make a new one.
- A PreToolUse hook blocks the git commands above that sweep, rewrite or
  discard shared state. When it blocks one, follow its message; don't look
  for another way to run the command.
- Test the packages you touched, not the whole repo, unless your brief says
  otherwise: workspace-wide test builds can run out of memory.
- Search the repo and its dependency sources, never `/` or `~`. A Cargo
  dependency's source is the `manifest_path` that
  `cargo metadata --format-version 1 --offline` gives it, run with your brief's
  prefix. A command still running at its Bash timeout moves to the background,
  and when it ends it wakes you to re-read your whole context, even after you
  have reported. Give a build or a test run a timeout it fits in (up to
  600000 ms), and run one that may not fit as a job, staying in your turn
  until it ends: `loom flow wait start <name> -- <command>` queues it in
  loom's work queue and prints its job ID, then `loom flow wait wait <name>`
  as a foreground call with a 600000 ms timeout, repeated while it prints
  `running <name>: …`, until `done <name> exit <status>` or
  `lost <name>: …`, which is final: report it (output in
  `<state>/agent-wait/<name>.log`). A job you queued another way is
  `loom flow wait wait --job <N>`. Never start a poll, a loop around
  `sleep`: `start` refuses one, and what it would watch is a job to wait on.
  Never use `run_in_background` and never end your turn to wait: Claude Code
  counts an agent waiting that way as not running, and a supervisor whose
  agents are all not running is made to hand back mid-run. Don't report while
  anything you started is still running, and never wait with `sleep`.
- Commits follow the repo's convention; its CLAUDE.md says which. End each
  message with the attribution lines your harness asks for, in place of any
  `Co-Authored-By` or `Claude-Session` lines in the plan's message or your
  brief: that swap is not a deviation, so don't list it.
- Never print a secret: whatever a command prints stays in the transcript.
  Check a credential file (`.env`, a key or `.pem` file) by name only, with
  `test -s <file>` or `grep -c '^NAME=' <file>`; never `cat`, `sed`, `cut`,
  `head`, `tail` or a plain `grep` it, and never echo a variable that holds
  one.
- Your report goes back to whoever dispatched you through the hand-back
  Claude Code gives you when you stop. Don't also send it with SendMessage:
  that delivers it twice, and when a supervisor dispatched you, a message to
  `main` skips it.
