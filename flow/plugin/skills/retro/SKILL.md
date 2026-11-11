---
name: retro
description: A retro over the agent flow's run records from every repo, proposing roster and profile changes for the owner to accept or reject. Owner-started only, /flow:retro (or /flow:retro dry-run to stop after reading cost and outcome).
argument-hint: [dry-run]
disable-model-invocation: true
---

# Flow retro

A retro turns the run records in `~/.loom/flow/records/` into decisions
about the flow roster and the repo profiles. The owner starts it;
nothing runs one automatically. It ends in a retro file and a short list of
proposed changes the owner accepts or rejects. Never change an agent
definition or a profile without their yes. The roster is loom's `flow/plugin/`,
so run a retro in a loom session on loom's own repo: its roster changes are
commits there.

If the session's repo has its own roster
(`.claude/agents/plan-execution-supervisor.md` at its top level), stop: that
roster has its own retro. Find the repo's own retro command under its
`.claude/commands/` or `.claude/skills/`, and name it.

## 1. Gather

1. List the records no retro covers: every `~/.loom/flow/records/<key>/*.md`
   (not `records/retros/`) that no `<!-- flow-retro covered: [...] -->` line in
   `~/.loom/flow/records/retros/retro-*.md` names. With none left, stop and
   say so.
   - Fewer than three runs is anecdote, not evidence: say so, and ask whether
     to go on.
   - When every run comes from one repo, say so: that is narrow evidence for a
     change that applies in every repo.
2. Total their costs:
   `loom flow costs records <the records>`.
   It prints each repo's table, then the pooled one; when every record is from
   one repo, it prints that one table.
3. Read each record's header, story table and notes. Group the runs by the
   roster hash on their `- Definitions:` lines: runs with the same hash ran the
   same roster. A run marked `+dirty`, or one whose line has a `→`, is a roster
   of its own. Never pool across rosters without saying so. Records whose
   `- Part:` lines name one spec are the parts of one epic: compare them part
   by part, and total them with `loom flow costs epic <spec>`.
4. Total the covered records the same way, as a baseline. Show it beside the
   new runs, never pooled with them.

## 2. Read cost and outcome together

Cost alone rewards the agent that does nothing, so pair every cost figure with
the rework it caused. Label each repo by the basename of the path its key
encodes: the key is the main checkout's absolute path with every character
other than a letter or digit made a `-`, so the name is the path's last
directory, hyphens and all. A hyphenated key can't be decoded by splitting it,
so walk from `/`: at each level, take the directory entry whose `-`-encoded
name continues the key, and go into it. When the walk fails (the repo moved or
was deleted), show the raw key.

- **Cost share**: developers, reviews, the doc writer, the supervisor (in records from before 2026-11-11) and the manager. A review on
  Opus can outweigh the story it checks, and the manager's own turns
  often outweigh every subagent together.
- **Rework by developer tier**: deviations, BLOCKED stories moved up a tier,
  escalations to the manager, review findings and turn-cap hits, per story. A
  cheap tier whose stories come back BLOCKED or full of findings costs more
  than the tier above it once the rework is counted.
- **Doc passes**: the doc writer's cost against what it changed (each story's
  Docs and Lessons) and the doc findings the final review raised anyway. A
  pass that changes nothing on most stories, or whose docs the final review
  keeps correcting, is a candidate for another effort, another tier or none.
- **Cache**: the share of an agent type's cost in one-hour cache writes. One
  that rarely pauses for five minutes pays double for writes it never reuses,
  which makes it a candidate for `cacheTtl: 5m`. The baseline retro,
  `records/retros/retro-2026-10-15.md`, describes how to count idle gaps from
  the transcripts.
- **Haiku's price step**: `Over 100K` counts requests priced at five times
  the normal rate. Stories that cross it are too big for `flow:junior-dev`, or
  should be split.
- **Effort**: a tier with almost no findings or deviations may run a step
  cheaper; one with many needs more effort or the next tier.
- **Design and run per 1,000 plan lines**: a record whose header has a
  `- Design stage:` line ran a plan `/flow:draft` drafted. Add that line's
  logged dollars to the run's own (its USD in `loom flow costs records`' run
  list) and divide by the plan lines the line names, per 1,000, and show the
  design stage's own figure per 1,000 lines beside it. A record without one
  ran a plan written by hand, whose writing cost no record holds: show its
  run alone, per 1,000 lines of its plan
  (`git -C <the repo> show <base>:<plan path> | wc -l`, `<base>` being the
  start of its Commits range; when that fails, say the figure is unknown),
  and say so. Neither figure holds the design
  session's own cost.

With `dry-run` as the argument, stop here: show the tables and this reading,
and propose nothing.

## 3. Propose

List each proposed change with the record rows that support it and what
would show it was wrong, at one of two levels:
- **roster**: flow's agents, so the change applies in every repo;
- **profile**: one repo's gates, landmines or idioms.

A repo can't override a flow agent: a repo's own `.claude/agents/<name>.md` is
another agent beside `flow:<name>`, so a change is a roster or a profile change. Ask the owner which changes to apply, and
apply only those.

## 4. Record

1. Apply each accepted change: the agent file under loom's
   `flow/plugin/agents/`, or the profile.
2. For each accepted roster change, add one line under the agent's heading (or
   "All agents") in loom's `flow/PROVENANCE.md`: the date, the owner's
   instruction and its evidence. The lines it adds name agents, never a repo. A
   profile change is recorded only in the retro file below, and may also carry
   a dated comment in the profile itself.
3. Write `~/.loom/flow/records/retros/retro-<YYYY-MM-DD>.md` (`-2`, `-3`, …
   after the date when that file exists): the runs
   covered, the totals, each change accepted or rejected with its evidence,
   and what to watch next time. End it with
   `<!-- flow-retro covered: ["<key>/<record file>", …] -->`, naming every
   record Gather step 1 listed.
4. Run `bash flow/lib/test.sh` with a 600000 ms timeout. If it fails, leave
   the files uncommitted and show the owner its FAIL lines. Otherwise commit
   in loom's repo by pathspec, one commit holding `flow/PROVENANCE.md` and the
   changed agent files: `git add -- <paths>`, then
   `git commit -m "chore(flow): retro <YYYY-MM-DD>" -- <paths>`. The retro
   file is not committed: `~/.loom/flow/records/` is no repository.
   A change to a repo's own `.claude/flow.toml` is committed in that repo,
   separately.

## Traps

- The dollar figures are API list prices. On a subscription they compare
  agents; they are not a bill.
- Compare like with like. A junior story and an intermediate story are different
  work, so compare tiers by rework, not raw cost.
- One expensive run is not a trend, and one cheap run is not a success.
- A pooled average can hide a tier that fails in one repo: read the per-repo
  tables first.
- A record's cost section covers the run's time window, so a side question
  the manager answered during the run lands in `main`.
