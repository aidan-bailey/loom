# Flow doc pass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After each story's review and fix rounds, a new pinned agent, `flow:doc-writer`, brings the docs into line with the story as it landed, under both the run engine and the supervisor.

**Architecture:** The profile gains `[docs] paths` and a `doc-writer` role block, and `flowconf.py docs-check` lists a range's changes outside the doc paths. The engine's `story()` gains a `docs` step (a doc writer, then `docs-check` and the story gates when it committed), switched on per run by `State.Docs`. The supervisor gains the same step in prose. The board gains a `doc` cell.

**Tech Stack:** Go (`internal/flowrun`, `internal/cli`, `internal/flowview`), Python 3.11 (`flow/lib`), Markdown agent and skill definitions, Preact (`internal/web/static/js/flow.mjs`).

**Spec:** `docs/superpowers/specs/2026-11-10-flow-doc-pass-design.md`

Blocks tagged `<!-- block: NAME -->` are applied by name. Each "current" block is replaced exactly once; an edit that finds it zero or two times stops.

---

## File structure

| File | Task | Responsibility |
|---|---|---|
| `flow/lib/flowconf.py` | 1 | `[docs] paths`, `DOC_WRITER` in `ROLES` and its block, `glob_match`, `is_generated` (moved here), `doc_strays`, `docs-check` |
| `flow/lib/plan_conformance.py` | 1 | imports `is_generated` from `flowconf` |
| `flow/lib/doctor.py` | 1 | a `doc paths` line |
| `flow/lib/flowconf_test.py`, `flow/lib/doctor_test.py` | 1 | their tests |
| `flow/plugin/agents/doc-writer.md` | 2 | new agent |
| `flow/lib/guarded-agents.txt`, `flow/plugin/skills/habits/SKILL.md`, `flow/PROVENANCE.md` | 2 | guard, habits, pins |
| `flow/plugin/agents/plan-execution-supervisor.md` | 2 | the supervisor's doc pass |
| `flow/plugin/skills/{run,supervise,retro}/SKILL.md` | 2 | memory at the end; the retro's doc passes |
| `flow/plugin/docs/dispatch.md`, `flow/plugin/docs/records.md`, `flow/README.md`, `flow/CLAUDE.md` | 2 | docs |
| `internal/flowrun/{steps,answers,state,parse,briefs,engine,record}.go` | 3 | the engine's docs step |
| `internal/flowrun/*_test.go`, `internal/flowrun/testdata/*.golden` | 3 | its tests |
| `internal/flowrun/README.md`, `internal/flowrun/CLAUDE.md` | 3 | docs |
| `internal/cli/{flow_agent_exec,flow_conf,flow_run_fx,flow_run}.go` and tests | 3 | the preamble, `DocsCheck`, `start` |
| `internal/flowview/detail.go`, `internal/flowview/detail_test.go` | 4 | the docs kind; kinds before an attempted one are skipped |
| `internal/web/static/js/flow.mjs`, `tools/webshot/fixture/flow.json`, `USAGE.md` | 4 | the `doc` cell |

---

## Task 1: The profile's doc paths, the doc writer's block and `docs-check`

**Files:**
- Modify: `flow/lib/flowconf.py`, `flow/lib/plan_conformance.py`, `flow/lib/doctor.py`
- Test: `flow/lib/flowconf_test.py`, `flow/lib/doctor_test.py`

- [ ] **Step 1: Write the failing tests.** In `flow/lib/flowconf_test.py`, insert before `if __name__ == "__main__":` (keeping two blank lines before it):

<!-- block: t1-flowconf-tests -->
```python
    def commit_all(self) -> str:
        """Commit everything staged or tracked-and-changed, and return HEAD."""
        git(self.repo, "add", "-u")
        git(self.repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "c")
        return git(self.repo, "rev-parse", "HEAD")

    def test_doc_paths_default_to_markdown_and_take_the_profiles_globs(self):
        self.repo_profile()
        self.assertEqual(flowconf.load_profile(self.repo).doc_paths, ("*.md",))
        self.repo_profile('schema = 1\n[docs]\npaths = ["*.md", "docs/*.svg"]\n')
        self.assertEqual(flowconf.load_profile(self.repo).doc_paths, ("*.md", "docs/*.svg"))
        self.assertIn("docs.paths: must be a list of strings", self.refused('schema = 1\n[docs]\npaths = "*.md"\n'))
        self.assertIn("docs.globs: unknown key", self.refused('schema = 1\n[docs]\nglobs = ["*.md"]\n'))

    def test_glob_match_tries_the_whole_path_and_the_name(self):
        self.assertTrue(flowconf.glob_match("README.md", ("*.md",)))
        self.assertTrue(flowconf.glob_match("docs/claude/a.md", ("*.md",)), "fnmatch's * crosses /")
        self.assertTrue(flowconf.glob_match("pkg/README", ("README",)), "by its name")
        self.assertFalse(flowconf.glob_match("main.go", ("*.md",)))
        self.assertTrue(flowconf.is_generated("sub/go.sum", ()), "a common lockfile")
        self.assertTrue(flowconf.is_generated("CHANGELOG.md", ("CHANGELOG.md",)))
        self.assertFalse(flowconf.is_generated("README.md", ("CHANGELOG.md",)))

    def test_the_doc_writers_block_holds_what_a_doc_pass_needs(self):
        self.repo_profile(GOOD + '[design]\nregistries = ["a route joins the table in routes.rs"]\n')
        profile = flowconf.load_profile(self.repo)
        block = flowconf.show(profile, "doc-writer", self.repo)
        self.assertTrue(block.startswith("[doc-writer]\n"), block)
        for value in ("`nix develop --command`", "Doc paths (change only files these match): `*.md`", "`*.gen.rs`",
                      "tracing, not println.", "a route joins the table in routes.rs"):
            self.assertIn(value, block)
        for value in ("scripts/db.sh", "filtered out", "single-threaded", "cargo clippy"):
            self.assertNotIn(value, block)
        self.assertIn("[doc-writer]", flowconf.show(profile, "supervisor", self.repo))

    def test_docs_check_lists_what_a_range_changed_outside_the_doc_paths(self):
        self.repo_profile('schema = 1\ngenerated = ["CHANGELOG.md"]\n')
        base = git(self.repo, "rev-parse", "HEAD")
        (self.repo / "docs").mkdir()
        (self.repo / "docs" / "guide.md").write_text("g\n")
        (self.repo / "README.md").write_text("y\n")
        git(self.repo, "add", "docs/guide.md")
        head = self.commit_all()
        self.assertEqual(self.run_cli("docs-check", "--base", base, "--head", head)[:2], (0, ""))
        (self.repo / "main.go").write_text("package main\n")
        (self.repo / "CHANGELOG.md").write_text("c\n")
        git(self.repo, "add", "main.go", "CHANGELOG.md")
        head = self.commit_all()
        code, out, err = self.run_cli("docs-check", "--base", base, "--head", head)
        self.assertEqual(code, 1, err)
        self.assertEqual(sorted(out.splitlines()), ["CHANGELOG.md", "main.go"], "code, and a generated Markdown file")

    def test_docs_check_takes_a_deleted_doc_and_checks_a_rename_by_both_names(self):
        self.repo_profile("schema = 1\n")
        (self.repo / "NOTES.md").write_text("n\n")
        git(self.repo, "add", "NOTES.md")
        base = self.commit_all()
        git(self.repo, "rm", "-q", "NOTES.md")
        head = self.commit_all()
        self.assertEqual(self.run_cli("docs-check", "--base", base, "--head", head)[:2], (0, ""), "a deleted doc")
        git(self.repo, "mv", "README.md", "README.txt")
        head = self.commit_all()
        self.assertEqual(self.run_cli("docs-check", "--base", base, "--head", head)[:2], (1, "README.txt\n"))

    def test_docs_check_errors_exit_2(self):
        self.repo_profile("schema = 1\n")
        code, _, err = self.run_cli("docs-check", "--base", "no-such-rev", "--head", "HEAD")
        self.assertEqual(code, 2)
        self.assertIn("no-such-rev", err)
        self.repo_profile("schema = 2\n")
        self.assertEqual(self.run_cli("docs-check", "--base", "HEAD", "--head", "HEAD")[0], 2)
        self.assertEqual(self.run_cli("docs-check", "--base", "HEAD")[0], 2, "a usage error")
```

In `flow/lib/doctor_test.py`, add to `class DoctorTest`, after `test_a_ready_repo_passes`:

<!-- block: t1-doctor-test -->
```python
    def test_doc_paths_pass_when_a_tracked_file_matches_and_warn_when_none_does(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        _, out = self.doctor()
        self.assertIn("WARN doc paths: no tracked file is a doc path (`*.md`, less the generated files)", out)
        (self.repo / "README.md").write_text("x\n")
        subprocess.run(["git", "-C", str(self.repo), "add", "README.md"], check=True, env=CLEAN_ENV)
        _, out = self.doctor()
        self.assertIn("PASS doc paths: `*.md`", out)
```

- [ ] **Step 2: Run them and watch them fail.**

Run: `cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q flowconf_test.py doctor_test.py`
Expected: FAIL/ERROR on `doc_paths`, `glob_match`, the `doc-writer` block, `docs-check` and the doctor line.

- [ ] **Step 3: Implement.** In `flow/lib/flowconf.py`:

1. In the module docstring, after the two `flowconf.py gates` entries, insert:

<!-- block: t1-doc-usage -->
```text
    flowconf.py docs-check --base B --head H
                                   the paths B..H changed that aren't doc paths
                                   (docs.paths, default *.md, less the generated
                                   files), one per line; exit 0 when there is
                                   none, 1 when any, 2 on an error
```

   After "the landmine markers and notes, the idioms and the design registries." (the role blocks paragraph) add the sentence ` The doc writer (the doc pass's spec §5.2) gets the prefix, the doc paths, the generated globs, the idioms and the design registries.` In the last paragraph, `` `vacuous` and `roster` exit 2 on any`` becomes `` `vacuous`, `docs-check` and `roster` exit 2 on any``.
2. Add `import fnmatch` before `import hashlib`, and `import posixpath` after `import os`.
3. Replace `ROLES = ("supervisor", "developer", "verifier", "reviewer")` with:

<!-- block: t1-roles -->
```python
# The doc pass's agent (the doc pass's spec, 2026-11-10): the supervisor pastes its block into its briefs.
DOC_WRITER = "doc-writer"
ROLES = ("supervisor", "developer", "verifier", "reviewer", DOC_WRITER)
```

4. In `KEYS`, after the `"design"` entry, add `    "docs": {"paths": list},`.
5. After `MAX_LOCKS = 8`, add:

<!-- block: t1-consts -->
```python
# The files a doc pass may change when docs.paths says nothing: every Markdown file (the doc pass's spec, §4).
DEFAULT_DOC_PATHS = ("*.md",)
# Lockfiles a story regenerates rather than writes: their lines are counted, not listed. A repo
# adds its own generated files with its profile's `generated` globs.
COMMON_LOCKFILES = {"Cargo.lock", "pnpm-lock.yaml", "package-lock.json", "uv.lock", "go.sum", "flake.lock"}
```

6. In `Profile`, after `registries: str = ""`, add `    doc_paths: tuple[str, ...] = DEFAULT_DOC_PATHS`; in `validate`'s `return Profile(`, after `registries=…,`, add `        doc_paths=values.get("docs.paths", DEFAULT_DOC_PATHS),`.
7. After `vacuous_lines`, add:

<!-- block: t1-globs -->
```python
def glob_match(path: str, globs: tuple[str, ...]) -> bool:
    """Whether one of `globs` matches `path`, tried on the whole path and on the file's name. `fnmatch`'s `*`
    crosses `/`, so `*.md` matches every Markdown file at any depth."""
    name = posixpath.basename(path)
    return any(fnmatch.fnmatch(path, glob) or fnmatch.fnmatch(name, glob) for glob in globs)


def is_generated(path: str, globs: tuple[str, ...]) -> bool:
    """Whether a changed file is generated: a common lockfile, or a match for one of the profile's globs."""
    return posixpath.basename(path) in COMMON_LOCKFILES or glob_match(path, globs)


def doc_strays(top: Path, profile: Profile, base: str, head: str) -> list[str]:
    """The doc pass's spec, §5.4: the paths base..head changed that aren't doc paths, matched by no
    `docs.paths` glob, or generated. A rename counts by both its names, a deletion by its path."""
    out = git(top, "diff", "-z", "--name-only", "--no-renames", f"{base}..{head}", "--")
    return [p for p in out.split("\0") if p and (not glob_match(p, profile.doc_paths) or is_generated(p, profile.generated))]
```

8. In `role_block`, after the `if role == "reviewer":` branch's `return [...]`, add:

<!-- block: t1-role-block -->
```python
    if role == DOC_WRITER:
        return [
            f"Prefix for every check command: {prefix}",
            f"Doc paths (change only files these match): {_listed(profile.doc_paths)}",
            f"Generated files (never edit them by hand, Markdown or not): {_listed(profile.generated)}",
            "Idioms beyond CLAUDE.md (some say which doc follows which code):",
            *_indented(profile.idioms),
            "Design registries (a story that adds what one lists edits it too):",
            *_indented(profile.registries),
        ]
```

9. After `_vacuous_command`, add:

<!-- block: t1-docs-command -->
```python
def _docs_check_command(base: str, head: str, cwd: Path) -> int:
    """0 when base..head changed only doc paths, 1 when it printed the paths that aren't, 2 on an error."""
    try:
        top = toplevel(cwd)
        strays = doc_strays(top, load_profile(top), base, head)
    except FlowError as err:
        print(f"flowconf: {err}", file=sys.stderr)
        return 2
    for path in strays:
        print(path)
    return 1 if strays else 0
```

10. In `main`, after `kind.add_argument("--final", …)`, add:

<!-- block: t1-docs-parser -->
```python
    docs_parser = sub.add_parser("docs-check", help="the paths a doc pass changed that aren't doc paths")
    docs_parser.add_argument("--base", required=True, metavar="COMMIT", help="the head before the doc pass")
    docs_parser.add_argument("--head", required=True, metavar="COMMIT", help="the head after it")
```

    and before `elif args.command == "doctor":`, add:

<!-- block: t1-docs-dispatch -->
```python
        elif args.command == "docs-check":
            return _docs_check_command(args.base, args.head, cwd)
```

In `flow/lib/plan_conformance.py`, delete the `COMMON_LOCKFILES` comment and set and the `is_generated` function (the lines from `# Lockfiles a story regenerates` to its `return`), delete `import fnmatch`, and change `from flowconf import FlowError, load_profile  # noqa: E402` to `from flowconf import FlowError, is_generated, load_profile  # noqa: E402`.

In `flow/lib/doctor.py`, change the `from flowconf import …` line to also import `git`, `glob_match` and `is_generated`, and add after `registry_paths_missing`:

<!-- block: t1-doctor-check -->
```python
def doc_paths_problem(top: Path, profile: Profile) -> str | None:
    """Why no tracked file is a doc path, or None when one is: a doc pass would then have no doc to change."""
    tracked = git(top, "ls-files", "-z").split("\0")
    if any(p and glob_match(p, profile.doc_paths) and not is_generated(p, profile.generated) for p in tracked):
        return None
    globs = ", ".join(f"`{glob}`" for glob in profile.doc_paths) or "no globs"
    return f"no tracked file is a doc path ({globs}, less the generated files)"
```

and in `doctor`, after the design registries' `PASS` line:

<!-- block: t1-doctor-line -->
```python
        problem = doc_paths_problem(top, profile)
        if problem:
            lines.append(f"WARN doc paths: {problem}")
        else:
            lines.append("PASS doc paths: " + ", ".join(f"`{glob}`" for glob in profile.doc_paths))
```

- [ ] **Step 4: Run the tools' tests and the agent lint.**

Run: `bash flow/lib/test.sh`
Expected: `OK` from unittest, then the agent lint's pass line, exit 0.

- [ ] **Step 5: Commit.**

```bash
git add flow/lib/flowconf.py flow/lib/plan_conformance.py flow/lib/doctor.py flow/lib/flowconf_test.py flow/lib/doctor_test.py
git commit -m "feat(flow): doc paths, the doc writer's block and docs-check" -- flow/lib/flowconf.py flow/lib/plan_conformance.py flow/lib/doctor.py flow/lib/flowconf_test.py flow/lib/doctor_test.py
```

---

## Task 2: The doc writer, and the supervisor's doc pass

**Files:**
- Create: `flow/plugin/agents/doc-writer.md`
- Modify: `flow/lib/guarded-agents.txt`, `flow/plugin/skills/habits/SKILL.md`, `flow/PROVENANCE.md`, `flow/plugin/agents/plan-execution-supervisor.md`, `flow/plugin/skills/run/SKILL.md`, `flow/plugin/skills/supervise/SKILL.md`, `flow/plugin/skills/retro/SKILL.md`, `flow/plugin/docs/dispatch.md`, `flow/plugin/docs/records.md`, `flow/README.md`, `flow/CLAUDE.md`

- [ ] **Step 1: Write the agent.** `flow/plugin/agents/doc-writer.md`:

<!-- block: t2-agent -->
````markdown
---
name: doc-writer
description: Brings the docs into line with one story as it landed, after its review and fix rounds, for the run engine or the supervisor. It fixes what the change made false, adds what the docs would expect, and records a lesson a fixed finding exposed; it never touches code. Pins set by the owner; change only on their instruction.
model: sonnet
effort: high
maxTurns: 100
tools: Bash, Read, Write, Edit, SendMessage
skills:
  - flow:habits
experimental:
  cacheTtl: 5m
---

You update the docs for one story of a plan, after its review and fix rounds.
You work in the repository the session is in; its CLAUDE.md files are your
rules, and where one of them and this definition differ, the CLAUDE.md wins.
Your brief names the story's plan section, the spec, the story's range
(`<base>..<head>`), its developer's report, the Critical and Important findings
its reviews sent to a fix round, and the `[doc-writer]` block of the repo's
profile. It carries no code: the habits' rules for plan code don't apply to
you, and their repo habits do.

## What you read

The story's diff, `git diff <base>..<head>`, is what the docs follow: read it
first. Then read the story's plan section and the `DEVIATION:` lines of its
developer's report, which say where the code left the plan, and so where the
plan's own doc edits may have gone stale. Read the spec only where the diff
leaves the intent unclear. Find what the docs say about what the story changed
by searching (`git grep -n` for a path, a name, a flag or a test name) rather
than by reading whole docs.

## What you change

Work through these in order:

1. Docs the change made false, the plan's own doc edits included: a fix round
   may have changed a name, a flag or a behaviour after the plan wrote about it.
2. Docs the repo would expect for what the story added and no doc covers yet:
   a flag in the user guide, the README of a new package, a row in a registry
   your block's design registries name.
3. Lessons (below).
4. An existing diagram the change made false. Never create a diagram.

Change only files your block's doc paths match, and never a generated file,
whatever its extension. Never change code or its comments, the owner's memory,
or text that is still true: rewording what didn't need it costs a review and
teaches nothing. A doc a later story's **Files** line names is that story's to
edit: leave it, even when this story's change makes it false, since the plan's
header may say it stays stale until then.

Follow the repo's CLAUDE.md on what goes where (in loom: rules in a
`CLAUDE.md`, in the rule shape it sets; facts in a `README.md`; procedures in
`docs/claude/`), within the size budgets its lint enforces: when a file is at
its budget, move context into a guide rather than raise the budget.

## Lessons

A lesson is a trap that a fixed Critical or Important finding exposed, which a
future session would walk into, and which no doc captures yet. Read the
findings your brief lists, and the report each came from when its fix doesn't
say why. Write a lesson only where the repo's CLAUDE.md says lessons go (in
loom, a guide in `docs/claude/` with its line in `docs/claude/INDEX.md`, or a
rule in the rule shape in the `CLAUDE.md` of the package it binds); when it
names no place, write none. If you are unsure a finding is a lesson, skip it:
undocumented beats noise.

## When the code and the spec disagree

When the landed code contradicts the spec or the plan, document nothing about
that behaviour, and report it as a `FINDING:` line (below): the final round
fixes the code, and the docs follow it there.

## Before you report

Run the doc checks the repo's CLAUDE.md names, with your block's prefix,
through `loom flow wait` when one compiles (in loom, `go test ./tools/claudemd/`,
and `go run ./tools/claudemd -v` for the budgets' headroom), and see them pass.
Commit by pathspec in the repo's convention (in loom, `docs(<scope>): …`).

End your report with one line per file you changed,
`DOC: <path> <what changed and why>`; one per lesson,
`LESSON: <path> <the trap>`; and one per contradiction,
`FINDING: <Critical|Important|Minor> <file>:<line> <what the code does, and what the spec or plan says>`;
then your status. `DONE` with no commits says nothing needed changing. Report
`NEEDS_CONTEXT` with your question when what a doc should say turns on what
the spec means and you can't tell.
````

- [ ] **Step 2: Guard, habits, pins.**

In `flow/lib/guarded-agents.txt`, after `senior-dev developer`, add the line `doc-writer developer`.

In `flow/plugin/skills/habits/SKILL.md`, the description's `(junior-dev, intermediate-dev and senior-dev), which preload` becomes `(junior-dev, intermediate-dev and senior-dev) and the doc writer, which preload`, and the body's `These habits bind every developer subagent.` becomes `These habits bind every developer subagent and the doc writer.`

In `flow/PROVENANCE.md`, between the `## code-quality-reviewer` and `## drafter` headings, add:

<!-- block: t2-provenance -->
```markdown
## doc-writer

- 2026-11-10, a new agent at `model: sonnet`, `effort: high`, `maxTurns: 100`, `cacheTtl: 5m`, on the owner's instruction in the doc pass's design (`docs/superpowers/specs/2026-11-10-flow-doc-pass-design.md` §2). Not Haiku: the roster sends completeness and absence claims to Sonnet (`flow:recon`), and a wrong doc passes every gate. Not Opus: `lib/prices.toml` prices Opus 5.5 at twice Sonnet 5.5's input, cache writes and output. High, the drafter's effort for writing in a house style, for a retro to lower on evidence. `5m`: it never waits on a gate. Estimate: $0.15–0.35 a story, from the 2026-11-06 run's Sonnet fix rounds ($0.11–0.20).
```

- [ ] **Step 3: The supervisor.** In `flow/plugin/agents/plan-execution-supervisor.md`:

1. `Its `[developer]`, `[verifier]` and `[reviewer]` blocks go into those roles' briefs as they stand` becomes `Its `[developer]`, `[verifier]`, `[reviewer]` and `[doc-writer]` blocks go into those roles' briefs as they stand`, and after `the reviewer block, the prefix, the landmine notes and the idioms.` add ` The doc-writer block carries the prefix, the doc paths, the generated files, the idioms and the design registries.`
2. Replace `7. Update the state file.` with:

<!-- block: t2-supervisor-step -->
```markdown
7. Run the story's doc pass. Dispatch `flow:doc-writer` with the plan's path
   and the story's line range, the spec's path, the story's range (its base,
   and its head after the last fix round), the developer's deviation list,
   the Critical and Important findings its fix rounds took, each with its
   `file:line` and the reviewer's reason, and the `[doc-writer]` block. When
   it committed: run
   `loom flow conf docs-check --base <the head before it> --head <HEAD>`, which
   lists what it changed outside the doc paths; for each such path, ask `main`
   whether to keep it, and otherwise send a fresh `flow:doc-writer` to undo it
   with an inverse edit; then dispatch `flow:verifier` with the word "story"
   and the story's commits. Whether or not it committed, keep its FINDING
   lines with the Minors for the final fix round.
8. Update the state file.
```

3. In "After the last story", step 2's `units or limits that disagree across a boundary.` becomes `units or limits that disagree across a boundary, and docs the run changed that contradict the code.`
4. In "State file", `and whether the developer hit its turn cap` becomes `whether the developer hit its turn cap, and the doc pass's DOC and LESSON counts`.

- [ ] **Step 4: The skills.**

In `flow/plugin/skills/run/SKILL.md`, step 5's `Then tell the owner what was built, what failed, and what needs them.` becomes `Record the run's outcome in your memory, when you keep one (what was built, its commits, what is still open), and nothing the repo's docs can hold: the doc passes put the run's lessons in the repo. Then tell the owner what was built, what failed, and what needs them.`

In `flow/plugin/skills/supervise/SKILL.md`, step 9's `9. Tell the owner what was built, what failed, and what needs them.` becomes `9. Record the run's outcome in your memory, when you keep one (what was built, its commits, what is still open), and nothing the repo's docs can hold: the doc passes put the run's lessons in the repo. Then tell the owner what was built, what failed, and what needs them.`

In `flow/plugin/skills/retro/SKILL.md`, `- **Cost share**: developers, reviews, supervisor and manager.` becomes `- **Cost share**: developers, reviews, the doc writer, supervisor and manager.`, and after the "Rework by developer tier" bullet add:

<!-- block: t2-retro -->
```markdown
- **Doc passes**: the doc writer's cost against what it changed (each story's
  Docs and Lessons) and the doc findings the final review raised anyway. A
  pass that changes nothing on most stories, or whose docs the final review
  keeps correcting, is a candidate for another effort, another tier or none.
```

- [ ] **Step 5: The docs.**

In `flow/plugin/docs/dispatch.md`:

1. In "Route to pinned agents", after the row `| Review outside the `supervised` option | … |`, add `| A story's docs, after its review and fix rounds | `flow:doc-writer` |`.
2. Before `## The verifier runs gates check-only`, insert:

<!-- block: t2-dispatch-section -->
```markdown
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
```

3. In "The run engine": `it dispatches each developer, fixer, reviewer and premise-check as a` becomes `it dispatches each developer, fixer, reviewer, doc writer and premise-check as a`; `- it reads a review's `FINDING:`, `PLAN:` and `VERDICT:` lines, which the` becomes `- it reads a review's `FINDING:`, `PLAN:` and `VERDICT:` lines, and a doc writer's `DOC:`, `LESSON:` and `FINDING:` lines, which the`; and `a second gate failure, a failed agent, a tree that moved.` becomes `a second gate failure, a failed agent, a doc pass that changed what isn't a doc, a tree that moved.`
4. In "Every agent chooses its prompt-cache lifetime", `` `flow:intermediate-dev`, `flow:spec-reviewer` and `flow:drafter` set `5m` `` becomes `` `flow:intermediate-dev`, `flow:spec-reviewer`, `flow:drafter` and `flow:doc-writer` set `5m` ``.

In `flow/plugin/docs/records.md`, the Stories table's header and separator become:

<!-- block: t2-records-table -->
```markdown
   | Story | Developer | Tier | Review | Commits | Status | Deviations | BLOCKED, up a tier | Escalated to manager | Review findings | Turn cap hit | Docs | Lessons |
   |---|---|---|---|---|---|---|---|---|---|---|---|---|
```

and after the **Tier**/**Review** paragraph's last sentence (`carries them.`) add ` **Docs** and **Lessons** count the `DOC:` and `LESSON:` lines of the story's doc pass, `—` for a run without one.`

In `flow/README.md`: `a verifier for the gates and reviewers for each story and the whole` becomes `a verifier for the gates, reviewers for each story and the whole, and a doc writer after each story's reviews`; `the 12 pinned agents: `drafter`, `plan-execution-supervisor`, `junior-dev`, `intermediate-dev`, `senior-dev`, ` becomes `the 13 pinned agents: `drafter`, `plan-execution-supervisor`, `junior-dev`, `intermediate-dev`, `senior-dev`, `doc-writer`, `; and the `conf` row's `key, status, show, state, drafts, vacuous, roster, gates, doctor` becomes `key, status, show, state, drafts, vacuous, roster, gates, docs-check, doctor`.

In `flow/CLAUDE.md`, `12 pinned agents` becomes `13 pinned agents`.

- [ ] **Step 6: Lint the agents and the docs.**

Run: `bash flow/lib/test.sh && CGO_ENABLED=0 go test ./flow/ ./tools/claudemd/`
Expected: exit 0; `ok` for both packages.

- [ ] **Step 7: Commit.**

```bash
git add flow/plugin/agents/doc-writer.md
git commit -m "feat(flow): the doc writer, and a doc pass in the supervisor's stories" -- flow/plugin/agents/doc-writer.md flow/lib/guarded-agents.txt flow/plugin/skills/habits/SKILL.md flow/PROVENANCE.md flow/plugin/agents/plan-execution-supervisor.md flow/plugin/skills/run/SKILL.md flow/plugin/skills/supervise/SKILL.md flow/plugin/skills/retro/SKILL.md flow/plugin/docs/dispatch.md flow/plugin/docs/records.md flow/README.md flow/CLAUDE.md
```

---

## Task 3: The engine's doc pass, and its wiring in `loom flow run`

**Files:**
- Modify: `internal/flowrun/steps.go`, `answers.go`, `state.go`, `parse.go`, `briefs.go`, `engine.go`, `record.go`, `README.md`, `CLAUDE.md`
- Modify: `internal/cli/flow_agent_exec.go`, `internal/cli/flow_conf.go`, `internal/cli/flow_run_fx.go`, `internal/cli/flow_run.go`
- Test: `internal/flowrun/engine_test.go`, `steps_test.go`, `parse_test.go`, `answers_test.go`, `briefs_test.go`, `testdata/docs.golden`, `testdata/anti-seam.golden`, `testdata/record.golden`; `internal/cli/flow_run_test.go`, `internal/cli/flow_agent_test.go`

- [ ] **Step 1: Write the failing tests.**

`internal/flowrun/engine_test.go`: the fake learns the doc writer's commits and `DocsCheck`. Add the field `strays map[string][]string // DocsCheck's answer, by head` to `fakeFX` (after `markers`), `strays: map[string][]string{},` to `newFake`, and after `isDev`:

<!-- block: t3-fake-commits -->
```go
// commits says whether role's done report moves HEAD: a developer's, and
// a doc writer's.
func commits(role string) bool { return isDev(role) || role == "doc-writer" }
```

In `Agent`, `if isDev(role) && r.done() && !f.noCommit[name] {` becomes `if commits(role) && r.done() && !f.noCommit[name] {`; in `Resume`, `if isDev(f.roles[name]) && r.done() {` becomes `if commits(f.roles[name]) && r.done() {`. After `Premises`, add:

<!-- block: t3-fake-docscheck -->
```go
func (f *fakeFX) DocsCheck(base, head string) ([]string, error) {
	f.calls = append(f.calls, "docs-check "+base+".."+head)
	return f.strays[head], nil
}
```

Replace `script()` with a run that has doc passes, one with a stray the manager keeps:

<!-- block: t3-script -->
```go
// script is a two-story run with a question, a fix round, a kept Minor and
// doc passes: Story 1's keeps a finding, and Story 2's stray is accepted.
func script() (*State, *fakeFX, map[string]string) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which?"}
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 fix\nFINDING: Minor a.go:5 tidy\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "DOC: README.md the flag\nLESSON: docs/claude/x.md a trap\nFINDING: Important b.go:2 the spec says two\nSTATUS: DONE"}
	f.reports["r-s2-dev1"] = Report{Status: "BLOCKED"}
	f.gates["r-s2-gates1"] = GateResult{Ended: "failed", Failing: []GateStep{{Line: "go vet", Verdict: "FAIL"}}}
	f.reports["r-s2-review1"] = Report{Status: "failed", Why: "it ended with no result"}
	f.strays["h6"] = []string{"main.go"}
	st := newState(story("1", "intermediate-dev", "vouched"), story("2", "junior-dev", "full"))
	st.Docs, st.Blocks.DocWriter = true, "Doc paths (change only files these match): `*.md`"
	return st, f, map[string]string{"ctx:r-s1-dev1:1": "That one.", "failed:r-s2-review1": "Read the conformance first.", "strays:r-s2-docs1": "accept"}
}
```

Before `// script is a two-story run`, add the doc pass's tests:

<!-- block: t3-engine-tests -->
```go
// docState is a run with the doc pass (the doc pass's spec, 2026-11-10).
func docState(stories ...Story) *State {
	st := newState(stories...)
	st.Docs, st.Blocks.DocWriter = true, "Doc paths (change only files these match): `*.md`"
	return st
}

func TestRun_TheDocPass(t *testing.T) {
	t.Run("a pass with commits is checked, then gated", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "DOC: README.md the flag\n**DOC:** USAGE.md its key\nLESSON: docs/claude/x.md a trap\nSTATUS: DONE"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Equal(t, []string{
			"agent r-s1-dev1 intermediate-dev", "services", "gates r-s1-gates1", "agent r-s1-review1 story-reviewer",
			"agent r-s1-docs1 doc-writer", "docs-check h1..h2", "services", "gates r-s1-gates2",
			"services", "gates r-final-gates1", "agent r-final-review1 code-quality-reviewer",
		}, f.calls)
		assert.Equal(t, Row{Status: "done", Developer: "intermediate-dev", Base: "h0", Head: "h2", Commits: 2, Docs: 2, Lessons: 1}, st.Stories[0].Row)
		brief := f.briefs["r-s1-docs1"]
		assert.Contains(t, brief, "Update the docs for Story 1 of the plan p.md: its section is plan lines 100-199. The spec is s.md.")
		assert.Contains(t, brief, "The story landed as h0..h1")
		assert.Contains(t, brief, "/reports/r-s1-dev1")
		assert.Contains(t, brief, "Its reviews sent no Critical or Important finding to a fix round.")
		assert.Contains(t, brief, "[doc-writer]\nDoc paths")
	})
	t.Run("a pass with no commits runs neither", func(t *testing.T) {
		f := newFake()
		f.noCommit["r-s1-docs1"] = true
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.NotContains(t, strings.Join(f.calls, "\n"), "docs-check")
		assert.NotContains(t, f.calls, "gates r-s1-gates2")
		assert.Contains(t, st.Notes, "Story 1: the doc pass changed no docs")
		assert.Equal(t, "h1", st.Stories[0].Row.Head)
	})
	t.Run("the findings its fix rounds took reach its brief", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 guard it\nFINDING: Minor a.go:5 tidy\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		brief := f.briefs["r-s1-docs1"]
		assert.Contains(t, brief, "- Critical a.go:3 guard it (/reports/r-s1-review1)")
		assert.NotContains(t, brief, "tidy", "a Minor is no lesson")
		assert.Contains(t, brief, "The story landed as h0..h2", "after its fix round")
	})
	t.Run("a stray asks, and accept gates it", func(t *testing.T) {
		f := newFake()
		f.strays["h2"] = []string{"main.go", "go.sum"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, Question{Key: "strays:r-s1-docs1", Kind: KindDocsStrayed, Story: "1", At: "h2",
			Text: "r-s1-docs1 changed paths that aren't doc paths:\n- main.go\n- go.sum", Paths: []string{"/reports/r-s1-docs1"}}, *st.Question)
		require.NoError(t, st.Answer("accept", "m", FromSpec, ""))
		drive(t, st, f, nil)
		assert.Contains(t, f.submits(), "gates r-s1-gates2")
		assert.Contains(t, st.Notes, "Story 1: the manager kept the doc pass's changes to main.go, go.sum")
	})
	t.Run("a decision on a stray runs a fresh pass to undo it", func(t *testing.T) {
		f := newFake()
		f.strays["h2"] = []string{"main.go"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, map[string]string{"strays:r-s1-docs1": "Keep the docs, undo the code."})
		brief := f.briefs["r-s1-docs2"]
		assert.Contains(t, brief, "/reports/r-s1-docs1")
		assert.Contains(t, brief, "Keep the docs, undo the code.")
		assert.Contains(t, brief, "undo its changes to them with inverse edits, and commit that:\n- main.go")
		assert.Contains(t, f.calls, "docs-check h1..h3", "over the whole pass")
		assert.Contains(t, f.submits(), "gates r-s1-gates2")
	})
	t.Run("a finding joins the kept ones for the final round", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "FINDING: Important design a.go:7 the spec says three retries\nSTATUS: DONE"}
		f.noCommit["r-s1-docs1"] = true
		st := docState(story("1", "intermediate-dev", "vouched"))
		drive(t, st, f, nil)
		assert.Contains(t, f.briefs["r-final-fix1"], "Important a.go:7 the spec says three retries")
		assert.NotContains(t, f.briefs["r-final-fix1"], "(design)", "the spec already decided it")
		assert.Contains(t, st.Notes, "Story 1: the doc pass kept 1 findings for the final round")
	})
	t.Run("a FINDING it can't read fails the pass, and its retry goes on from its commits", func(t *testing.T) {
		f := newFake()
		f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "FINDING: Severe a.go:7 no\nSTATUS: DONE"}
		st := docState(story("1", "intermediate-dev", "vouched"))
		require.NoError(t, Run(st, f))
		require.Equal(t, Parked, st.Status)
		assert.Equal(t, "failed:r-s1-docs1", st.Question.Key)
		assert.Contains(t, st.Question.Text, "a FINDING line it can't read")
		require.NoError(t, st.Answer("retry", "m", FromSpec, ""))
		drive(t, st, f, nil) // a tree-moved question fails the test
		assert.Contains(t, f.briefs["r-s1-docs2"], "/reports/r-s1-docs1")
		assert.Contains(t, f.calls, "docs-check h1..h3")
	})
	t.Run("a run without Docs has no pass", func(t *testing.T) {
		f := newFake()
		st := newState(story("1", "intermediate-dev", "vouched"))
		st.Blocks.DocWriter = "Doc paths: `*.md`"
		drive(t, st, f, nil)
		assert.NotContains(t, strings.Join(f.calls, "\n"), "doc-writer")
		assert.Contains(t, Record(st, RecordInputs{}), "| no | — | — |")
	})
}
```

`internal/flowrun/steps_test.go`: in `TestStepOf`'s good names add `"r-s1-docs1": {Story: "1", Kind: KindDocs, K: 1},` and `"r-s2-b-docs3": {Story: "2-b", Kind: KindDocs, K: 3},`, and to its bad names add `"r-final-docs1"`. In `TestStepOf_ParsesEveryNameTheEngineSubmits`, the scenario struct gains `docs bool`, `st := newState(c.stories...)` gains a following line `st.Docs = c.docs`, and the map gains:

<!-- block: t3-steps-scenario -->
```go
		"a doc pass with a stray decided": {
			setup:   func(f *fakeFX) { f.strays["h2"] = []string{"main.go"} },
			stories: []Story{story("1", "intermediate-dev", "vouched")},
			answers: map[string]string{"strays:r-s1-docs1": "Undo it."},
			docs:    true,
		},
```

In `TestQuestionStep`, add `{Question{Key: "strays:r-s1-docs2", Story: "1"}, Step{Story: "1", Kind: KindDocs, K: 2}, true},`.

`internal/flowrun/parse_test.go`, after `TestParseReview`:

<!-- block: t3-parse-test -->
```go
func TestParseDocs(t *testing.T) {
	d, err := ParseDocs("Changed two.\nDOC: README.md the flag\n**DOC:** USAGE.md its key\nDOC:\nLESSON: docs/claude/x.md a trap\n" +
		"`FINDING: Important design a.go:7 the spec says three`\nSTATUS: DONE")
	require.NoError(t, err)
	assert.Equal(t, DocPass{Docs: 2, Lessons: 1, Findings: []Finding{{Severity: "Important", File: "a.go", Line: 7, Fix: "the spec says three"}}}, d,
		"an empty DOC line counts nothing, and the design flag is dropped")
	d, err = ParseDocs("nothing to change\nSTATUS: DONE")
	require.NoError(t, err)
	assert.Equal(t, DocPass{}, d)
	_, err = ParseDocs("FINDING: Severe a.go:7 no\nSTATUS: DONE")
	assert.ErrorContains(t, err, "a FINDING line it can't read")
}
```

`internal/flowrun/answers_test.go`: in `TestCheckAnswer`'s `takes` map, after `KindTreeMoved:     {WordContinue, WordStop},`, add `KindDocsStrayed:   {"text", WordAccept, WordStop},` (gofmt aligns the map); in `TestTakes`, add `assert.Equal(t, "a decision, accept or stop", Takes(KindDocsStrayed))`.

`internal/flowrun/briefs_test.go`: in `briefState`, after `st.Blocks.Reviewer = …`, add `st.Blocks.DocWriter = "Prefix for every check command: none\nDoc paths (change only files these match): `*.md`"`; in `TestBriefs`, after the anti-seam golden line, add:

<!-- block: t3-briefs-test -->
```go
	sent := []SentFinding{{Finding: Finding{Severity: "Critical", File: "a.go", Line: 3, Fix: "guard it"}, Report: "/state/agents/r-s2-review1/report.md"}}
	golden(t, "docs.golden", DocsBrief(st, s, "h1", "h6", "/state/agents/r-s2-dev1/report.md", sent))
	assert.Contains(t, DocsBrief(st, s, "h1", "h6", "/state/agents/r-s2-dev1/report.md", nil), "\nIts reviews sent no Critical or Important finding to a fix round.\n")
```

`internal/cli/flow_agent_test.go`, in `TestAgentArgv_RolePreamblesAndMaxTurns`: after `write("recon", "model: sonnet\n")` add `write("doc-writer", "model: sonnet\nmaxTurns: 100\n")`, and after the `recon` assertions:

<!-- block: t3-preamble-test -->
```go
	docs := argvOf("doc-writer")
	assert.Contains(t, docs, docWriterPreamble)
	assert.NotContains(t, docs, reviewPreamble)
	assert.NotContains(t, docs, developerPreamble)
	assert.Contains(t, docs, "\n--max-turns\n100\n")
```

`internal/cli/flow_run_test.go`: the fake claude writes Story 1's docs and nothing for the others. In `fakeRunClaude`'s comment, after `any other review approves;` add ` Story 1's doc writer commits a doc, any other doc writer changes nothing;`, and before `  *"Build Story 1"*)` add:

<!-- block: t3-fake-claude -->
```sh
  *"Update the docs for Story 1 "*) commit docs; result 'DOC: README.md the new flag\nSTATUS: DONE';;
  *"Update the docs for Story"*) result 'nothing to change\nSTATUS: DONE';;
```

`fakeRunTools.Show` ends its blocks with `"[reviewer]\nPrefix: none\n\n[doc-writer]\nPrefix for every check command: none\n"`, `fakeRunTools` gains `func (f fakeRunTools) DocsCheck(string, string) ([]string, error) { return nil, nil }`, and `runFixture`'s `agentFixture` call gains `"doc-writer"`. In `TestFlowRun_EndToEnd`, the expected agents, gates and Story 1's row become:

<!-- block: t3-e2e-asserts -->
```go
	assert.Equal(t, []string{id + "-s1-dev1", id + "-s1-review1", id + "-s1-fix1", id + "-s1-review2", id + "-s1-docs1",
		id + "-s2-dev1", id + "-s2-review1", id + "-s2-docs1", id + "-final-review1"}, st.Agents)
	assert.Equal(t, []string{id + "-s1-gates1", id + "-s1-gates2", id + "-s1-gates3", id + "-s2-gates1", id + "-final-gates1"}, st.Gates,
		"the fix and the doc pass were gated again")
	assert.True(t, st.Docs, "start switches the doc pass on when the profile prints its block")
```

and `| 1 | intermediate-dev | deep | vouched | 2 | done | 1 | 0 | 0 | 1/0/0 | no |` becomes `| 1 | intermediate-dev | deep | vouched | 3 | done | 1 | 0 | 0 | 1/0/0 | no | 1 | 0 |`.

- [ ] **Step 2: Run them and watch them fail.**

Run: `CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/cli/ -run 'TestRun_|TestStepOf|TestQuestionStep|TestParseDocs|TestCheckAnswer|TestTakes|TestBriefs|TestRecord|TestAgentArgv|TestFlowRun_'`
Expected: build failures naming `DocsCheck`, `KindDocs`, `KindDocsStrayed`, `State.Docs`, `DocPass`, `SentFinding`, `DocsBrief`, `docWriterPreamble`.

- [ ] **Step 3: Implement the engine.**

`internal/flowrun/steps.go`: add `KindDocs    = "docs"` to the kinds (gofmt aligns the block); the attempt pattern becomes `` `^(dev|gates|review|fix|docs)([1-9][0-9]*)$` ``; in `StepOf`, `if m == nil || m[1] == KindDev {` becomes `if m == nil || m[1] == KindDev || m[1] == KindDocs { // the final round has no developer and no doc pass`; in `QuestionStep`, `case "tree", "ctx", "failed", "blocked", "gates":` becomes `case "tree", "ctx", "failed", "blocked", "gates", "strays":`, and its comment's `(tree:, ctx:, failed:, blocked:, gates:)` becomes `(tree:, ctx:, failed:, blocked:, gates:, strays:)`.

`internal/flowrun/answers.go`: add `KindDocsStrayed   = "docs-strayed"` after `KindTreeMoved`, and to `takes`, `KindDocsStrayed:   {[]string{WordAccept, WordStop}, true},`.

`internal/flowrun/state.go`:
- In `State`, after `Blocks      Blocks                     `json:"blocks"``, add:

<!-- block: t3-state-docs -->
```go
	// Docs says the run has a doc pass after each story (the doc pass's
	// spec, 2026-11-10): loom flow run start sets it, so a run an older
	// build started replays without one.
	Docs bool `json:"docs,omitempty"`
```

- `Blocks` gains `DocWriter  string   `json:"doc_writer,omitempty"`` after `Supervisor`.
- `Row`'s `Status` comment becomes `// developing, gating, reviewing, fixing, documenting, done`, and after `Corrections` add `Docs        int      `json:"docs,omitempty"`    // its doc pass's DOC lines` and `Lessons     int      `json:"lessons,omitempty"` // and LESSON lines`.

`internal/flowrun/parse.go`: replace `ParseReview`'s FINDING branch body with a call to a shared parser, and add the doc pass's reader:

<!-- block: t3-parse-current -->
```go
		if v, ok := TagLine(line, "FINDING:"); ok {
			m := findingRE.FindStringSubmatch(v)
			if m == nil {
				return Review{}, fmt.Errorf("a FINDING line it can't read: %q", v)
			}
			n, _ := strconv.Atoi(m[4])
			r.Findings = append(r.Findings, Finding{Severity: m[1], Design: m[2] != "", File: m[3], Line: n, Fix: m[5]})
		} else if v, ok := TagLine(line, "PLAN:"); ok && v != "" {
```

becomes:

<!-- block: t3-parse-new -->
```go
		if v, ok := TagLine(line, "FINDING:"); ok {
			f, err := parseFinding(v)
			if err != nil {
				return Review{}, err
			}
			r.Findings = append(r.Findings, f)
		} else if v, ok := TagLine(line, "PLAN:"); ok && v != "" {
```

and after `ParseReview`:

<!-- block: t3-parse-docs -->
```go
// parseFinding reads a FINDING line's value.
func parseFinding(v string) (Finding, error) {
	m := findingRE.FindStringSubmatch(v)
	if m == nil {
		return Finding{}, fmt.Errorf("a FINDING line it can't read: %q", v)
	}
	n, _ := strconv.Atoi(m[4])
	return Finding{Severity: m[1], Design: m[2] != "", File: m[3], Line: n, Fix: m[5]}, nil
}

// DocPass is a doc writer's report lines (the doc pass's spec, §4): how
// many DOC and LESSON lines it wrote, and its FINDING lines.
type DocPass struct {
	Docs, Lessons int
	Findings      []Finding
}

// ParseDocs reads a doc writer's DOC, LESSON and FINDING lines. A FINDING
// line it can't read is an error, as in a review; a finding's design flag
// is dropped, since the spec already decided what the code should do.
func ParseDocs(text string) (DocPass, error) {
	var d DocPass
	for _, line := range strings.Split(text, "\n") {
		if v, ok := TagLine(line, "DOC:"); ok && v != "" {
			d.Docs++
		} else if v, ok := TagLine(line, "LESSON:"); ok && v != "" {
			d.Lessons++
		} else if v, ok := TagLine(line, "FINDING:"); ok {
			f, err := parseFinding(v)
			if err != nil {
				return DocPass{}, err
			}
			f.Design = false
			d.Findings = append(d.Findings, f)
		}
	}
	return d, nil
}
```

`internal/flowrun/briefs.go`: `AntiSeamBrief`'s first `Fprintf` is followed by `b.WriteString("Docs the run changed that contradict the code are findings too, each at the doc's file:line.\n")`; and after `AntiSeamBrief` add:

<!-- block: t3-briefs -->
```go
// SentFinding is a Critical or Important finding a review sent to a fix
// round, with the report that found it: what a doc pass reads for lessons.
type SentFinding struct {
	Finding
	Report string
}

// DocsBrief is a story's doc pass brief (the doc pass's spec, §5.2): the
// story as it landed, base..head, its developer's report, the findings
// its fix rounds took, and the [doc-writer] block.
func DocsBrief(st *State, s *Story, base, head, devReport string, sent []SentFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Update the docs for Story %s of the plan %s: its section is plan lines %d-%d. The spec is %s.\n", s.ID, st.Plan, s.Line, s.End, st.Spec)
	fmt.Fprintf(&b, "The story landed as %s..%s: that diff is what the docs follow.\n", base, head)
	b.WriteString("The plan's header and the later stories' **Files** lines say which story owns which doc edit: leave a doc a later story's **Files** line names to that story.\n")
	fmt.Fprintf(&b, "Its developer's report, whose DEVIATION lines say where the code left the plan, and so where the plan's doc edits may have gone stale: %s\n", devReport)
	if len(sent) == 0 {
		b.WriteString("Its reviews sent no Critical or Important finding to a fix round.\n")
	} else {
		b.WriteString("The Critical and Important findings its reviews sent to a fix round, for lessons, each with the report that found it:\n")
		for _, f := range sent {
			fmt.Fprintf(&b, "- %s (%s)\n", f.Finding, f.Report)
		}
	}
	fmt.Fprintf(&b, "\n[doc-writer]\n%s\n", st.Blocks.DocWriter)
	return b.String()
}

// undoStrays is what a doc pass's retry adds after the manager's decision
// on its strays: the paths to put back.
func undoStrays(strays []string) string {
	var b strings.Builder
	b.WriteString("\nThe earlier pass changed these paths, which aren't doc paths; unless the manager's decision says otherwise, undo its changes to them with inverse edits, and commit that:\n")
	for _, p := range strays {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}
```

`internal/flowrun/engine.go`:
- In `Effects`, after `Premises`, add:

<!-- block: t3-effects -->
```go
	// DocsCheck is the paths base..head changed that aren't doc paths
	// (loom flow conf docs-check): none when it changed only docs.
	DocsCheck(base, head string) ([]string, error)
```

- `engine` gains the field `sent    []SentFinding  // the story in hand's findings sent to a fix round, for its doc pass`.
- In `story()`, `e.cur, e.st.Phase = s.ID, "story "+s.ID` becomes `e.cur, e.st.Phase, e.sent = s.ID, "story "+s.ID, nil`, its comment becomes `// story runs one story: developer, gates, review, findings, doc pass (spec §5.3).`, and after the `planLines` call's `if` block add:

<!-- block: t3-story-call -->
```go
	if e.st.Docs {
		if err := e.docs(s, p, base, dev.Path); err != nil {
			return err
		}
	}
```

- In `findings()`, before `return e.fixRound(s, p, base, rev.Findings, nil, decision, plan)`, add `e.sendFindings(rev, rep)`; in `fixRound()`, before `findings, planFix, decision = rev.Findings, nil, a`, add `e.sendFindings(rev, rep)`.
- After `planLines`, add:

<!-- block: t3-engine-docs -->
```go
// sendFindings keeps rev's Critical and Important findings, which go to a
// fix round, for the story's doc pass.
func (e *engine) sendFindings(rev Review, rep Report) {
	for _, f := range blocking(rev.Findings) {
		e.sent = append(e.sent, SentFinding{Finding: f, Report: rep.Path})
	}
}

// docs runs story s's doc pass (the doc pass's spec, §5.3): a doc writer
// over the story as it landed, then docs-check and the story's gates when
// it committed. Its findings join the kept ones, for the final round.
func (e *engine) docs(s *Story, p, base, devReport string) error {
	s.Row.Status = "documenting"
	before := e.head
	brief := DocsBrief(e.st, s, base, before, devReport, e.sent)
	next := brief
	var found []Finding
	for {
		name := e.next(p + "-docs")
		r, d, err := e.docPass(name, next)
		if err != nil {
			return err
		}
		if d == nil {
			var earlier []string
			if r.done() {
				// Its lines don't parse, but it finished: its commits are its
				// own, and the retry goes on from them.
				if _, err := e.headAt(name); err != nil {
					return err
				}
				earlier = paths(r.Path)
			}
			decision, err := e.failed(name, r)
			if err != nil {
				return err
			}
			next = withEarlier(brief, earlier, decision)
			continue
		}
		s.Row.Docs, s.Row.Lessons = s.Row.Docs+d.Docs, s.Row.Lessons+d.Lessons
		for _, f := range d.Findings {
			if !slices.Contains(found, f) {
				found = append(found, f)
			}
		}
		head, err := e.headAt(name)
		if err != nil {
			return err
		}
		if head == before {
			e.note("%s: the doc pass changed no docs", storyName(s))
			break
		}
		strays, err := e.docsCheck(name, before, head)
		if err != nil {
			return err
		}
		if len(strays) > 0 {
			a, err := e.ask(Question{Key: "strays:" + name, Kind: KindDocsStrayed, Text: name + " changed paths that aren't doc paths:\n- " + strings.Join(strays, "\n- "), Paths: paths(r.Path)})
			if err != nil {
				return err
			}
			switch a {
			case WordStop:
				return errStopped
			case WordAccept:
				e.note("%s: the manager kept the doc pass's changes to %s", storyName(s), strings.Join(strays, ", "))
			default:
				next = withEarlier(brief, paths(r.Path), a) + undoStrays(strays)
				continue
			}
		}
		if err := e.gateLoop(s, p, base); err != nil {
			return err
		}
		break
	}
	if len(found) > 0 {
		e.st.Kept = append(e.st.Kept, found...)
		e.note("%s: the doc pass kept %d findings for the final round", storyName(s), len(found))
	}
	return nil
}

// docPass is doc writer run name's report and its lines, or a nil DocPass
// when the run failed: it didn't report done, or its lines don't parse,
// which r.Why then says.
func (e *engine) docPass(name, brief string) (Report, *DocPass, error) {
	r, err := e.agent(name, "doc-writer", "", brief)
	if err == nil {
		r, err = e.settle(name, r)
	}
	if err != nil || !r.done() {
		return r, nil, err
	}
	d, perr := ParseDocs(r.Text)
	if perr != nil {
		r.Why = perr.Error()
		return r, nil, nil
	}
	return r, &d, nil
}

// docsCheck is DocsCheck over base..head, memoized under name.
func (e *engine) docsCheck(name, base, head string) ([]string, error) {
	var strays []string
	if e.recall("docs:"+name, &strays) {
		return strays, nil
	}
	strays, err := e.fx.DocsCheck(base, head)
	if err != nil {
		return nil, err
	}
	return strays, e.remember("docs:"+name, strays)
}
```

`internal/flowrun/record.go`: import `strconv`; the story table's header and separator gain ` Docs | Lessons |` and `---|---|`; before its row's `Fprintf`, add:

<!-- block: t3-record -->
```go
		docs, lessons := "—", "—"
		if st.Docs {
			docs, lessons = strconv.Itoa(r.Docs), strconv.Itoa(r.Lessons)
		}
```

and the row's format gains ` %s | %s |` and its arguments `docs, lessons` after `cap`.

- [ ] **Step 4: Implement the CLI.**

`internal/cli/flow_agent_exec.go`, after `developerPreamble`:

<!-- block: t3-preamble -->
```go
// docWriterPreamble follows headlessPreamble for a doc writer's run (the
// doc pass's spec, §4): loom flow run counts these lines and keeps the
// findings for the final round.
const docWriterPreamble = "Before your STATUS line, end your report with one line `DOC: <path> <what changed and why>` per file you changed, " +
	"one line `LESSON: <path> <the trap>` per lesson you wrote, and one line `FINDING: <severity> <file>:<line> <what the code does, and what the spec or plan says>` " +
	"per place the code contradicts the spec or the plan, the severity being Critical, Important or Minor. Write none of these lines when there is none."
```

and `rolePreamble` gains `case "doc-writer": return headlessPreamble + " " + docWriterPreamble`.

`internal/cli/flow_conf.go`: `runTools` gains `DocsCheck(base, head string) ([]string, error) // flowconf.py docs-check: the paths that aren't docs`, and after `scriptTools.Conformance`:

<!-- block: t3-scripttools -->
```go
func (s scriptTools) DocsCheck(base, head string) ([]string, error) {
	out, _, err := s.run("flowconf.py", []int{0, 1}, "docs-check", "--base", base, "--head", head)
	if err != nil {
		return nil, err
	}
	var strays []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			strays = append(strays, l)
		}
	}
	return strays, nil
}
```

`internal/cli/flow_run_fx.go`, after `runFX.Premises`: `func (fx *runFX) DocsCheck(base, head string) ([]string, error) { return fx.tools.DocsCheck(base, head) }`.

`internal/cli/flow_run.go`, in `start`'s state: `Blocks` gains `DocWriter: blocks["doc-writer"],`, and the state gains `Docs: blocks["doc-writer"] != "",` with the comment `// a flow root from before the doc pass prints no [doc-writer] block`.

- [ ] **Step 5: Regenerate the goldens and read their diff.**

Run: `CGO_ENABLED=0 go test ./internal/flowrun/ -run 'TestBriefs|TestRecord' -update && git diff internal/flowrun/testdata/`
Expected: `anti-seam.golden` gains the doc line, `docs.golden` is new, and `record.golden`'s stories gain the doc pass (its commits, `Docs` and `Lessons` columns, the notes, the gates and the stray question); nothing else changes.

- [ ] **Step 6: Run the packages' tests.**

Run: `CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/cli/`
Expected: `ok` for both.

- [ ] **Step 7: Docs.** `internal/flowrun/README.md`: the kinds list in **Parking** gains `docs-strayed`; **Report lines** reads `` (`FINDING:`, `PLAN:`, `VERDICT:`, `DEVIATION:`, `DOC:`, `LESSON:` here; …) ``; **Effects** gains `docs-check`; the procedure's story steps gain `6. The doc pass, when the run has it (State.Docs): a doc writer over the story as it landed; when it committed, docs-check, where a stray asks, and the story's gates again; its findings join the kept Minors.` `internal/flowrun/CLAUDE.md`'s report-lines rule names `` `DOC:` and `LESSON:` `` with the others and `TestParseDocs` among its tests.

- [ ] **Step 8: Commit.**

```bash
git add internal/flowrun/testdata/docs.golden
git commit -m "feat(flowrun,cli): a doc pass after each story's review and fix rounds" -- internal/flowrun internal/cli/flow_agent_exec.go internal/cli/flow_conf.go internal/cli/flow_run_fx.go internal/cli/flow_run.go internal/cli/flow_run_test.go internal/cli/flow_agent_test.go
```

---

## Task 4: The board's doc cell

**Files:**
- Modify: `internal/flowview/detail.go`, `internal/web/static/js/flow.mjs`, `tools/webshot/fixture/flow.json`, `USAGE.md`
- Test: `internal/flowview/detail_test.go`

- [ ] **Step 1: Write the failing test.** In `internal/flowview/detail_test.go`, `TestDetail_TheBoard`'s three story cell maps gain `"docs": CellSkipped`, `"docs": CellSkipped` and `"docs": CellTodo`, and after it add:

<!-- block: t4-test -->
```go
// A doc pass has its cell, and a story gated again after it shows the kinds
// it went past without an attempt as skipped, whatever its status says (the
// doc pass's spec, §5.7).
func TestDetail_TheDocCell(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	st := runState("r", flowrun.Running,
		flowrun.Story{ID: "1", Developer: "intermediate-dev", Tier: "deep", Review: "vouched", Row: flowrun.Row{Status: "gating"}},
		flowrun.Story{ID: "2", Developer: "intermediate-dev", Tier: "deep", Review: "vouched", Row: flowrun.Row{Status: "done"}})
	st.Docs = true
	st.Agents = []string{"r-s1-dev1", "r-s1-review1", "r-s1-docs1", "r-s2-dev1", "r-s2-review1"}
	st.Gates = []string{"r-s1-gates1", "r-s1-gates2", "r-s2-gates1"}
	roles := map[string]string{"r-s1-review1": "story-reviewer", "r-s1-docs1": "doc-writer", "r-s2-review1": "story-reviewer"}
	for i, name := range st.Agents {
		role := roles[name]
		if role == "" {
			role = "intermediate-dev"
		}
		agentRec(t, state, name, role, "DONE", sched.JobID(i+1))
		memo(t, st, "agent:"+name, flowrun.Report{Status: "DONE"})
	}
	for i, name := range st.Gates {
		gateRec(t, state, name, "PASS", sched.JobID(i+10))
		memo(t, st, "gates:"+name, flowrun.GateResult{Ended: "passed"})
	}
	writeRun(t, state, st)

	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	require.Len(t, d.Stories, 2, "a run's rows are its state's stories")
	assert.Equal(t, map[string]string{"dev": CellPassed, "gates": CellPassed, "review": CellPassed, "fix": CellSkipped, "docs": CellPassed}, d.Stories[0].Cells,
		"its gates after the doc pass say gating; the fix it never had is skipped, not todo")
	assert.Equal(t, CellSkipped, d.Stories[1].Cells["fix"])
	assert.Equal(t, CellSkipped, d.Stories[1].Cells["docs"], "a done story with no doc pass")
}
```

- [ ] **Step 2: Run it and watch it fail.**

Run: `CGO_ENABLED=0 go test ./internal/flowview/ -run 'TestDetail_TheBoard|TestDetail_TheDocCell'`
Expected: FAIL: the maps have no `docs` key, and Story 1's `fix` is `todo`.

- [ ] **Step 3: Implement.** In `internal/flowview/detail.go`, `rowKinds` gains `flowrun.KindDocs` after `flowrun.KindFix`; `kindReached` gains `case "documenting": return 4` before `case "done":`; and in `cells`, before its loop, add:

<!-- block: t4-cells -->
```go
	// A kind before one with an attempt went by without one, whatever the
	// row's status says: a story gated again after its doc pass is gating.
	for i, kind := range kinds {
		if slices.ContainsFunc(steps, func(s StepView) bool { return s.Kind == kind }) {
			reached = max(reached, i)
		}
	}
```

In `internal/web/static/js/flow.mjs`, `story: ["dev", "gates", "review", "fix"]` becomes `story: ["dev", "gates", "review", "fix", "docs"]`, and `LABEL` gains `docs: "doc"`. In `tools/webshot/fixture/flow.json`, each of the five story rows' `cells` gains a `"docs"` entry after `"fix"`: `"passed"` for Stories 1 and 2, `"todo"` for Stories 3, 4 and 5. In `USAGE.md`, `its steps (dev, gate, review, fix)` becomes `its steps (dev, gate, review, fix, doc)`.

- [ ] **Step 4: Run the packages' tests.**

Run: `CGO_ENABLED=0 go test ./internal/flowview/ ./internal/web/ ./tools/webshot/`
Expected: `ok` for each.

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(flowview,web): the board's doc cell" -- internal/flowview/detail.go internal/flowview/detail_test.go internal/web/static/js/flow.mjs tools/webshot/fixture/flow.json USAGE.md
```

---

## Final verification

- [ ] `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")); [ -z "$out" ] || { echo "$out"; exit 1; }'`
- [ ] `CGO_ENABLED=0 go vet ./...`
- [ ] `CGO_ENABLED=0 go test ./...`
- [ ] `CC=clang CGO_ENABLED=1 go test -race -count=1 ./internal/flowrun/ ./internal/cli/ ./internal/flowview/`
- [ ] `bash flow/lib/test.sh`
- [ ] `go run ./tools/claudemd -v`, for the budgets of the CLAUDE.md files this plan edits
