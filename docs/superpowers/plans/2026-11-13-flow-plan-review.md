# Flow plan review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/flow:draft` reviews every story's plan code once before the commit (Sonnet, escalating risky tasks to Opus), re-checks fixes narrowly, and vouches deep stories whose committed text was reviewed clean, so the run gives them the cheaper story-reviewer.

**Architecture:** Two new read-only agents (`flow:plan-reviewer`, `flow:risk-reviewer`) dispatched from the draft skill's step 4; snapshot copies under the drafts directory record what was reviewed; a new script, `loom flow plan-review`, turns the snapshots into the `**Plan review:**` line; `check-plan` learns a `full:` list for deep stories the review didn't vouch for. The run engine already gives `review: vouched` stories `flow:story-reviewer` (`internal/flowrun/engine.go:1025`) and needs no change.

**Tech Stack:** Python 3 (flow/lib, unittest), Go (one row in `internal/cli/flow.go`), Markdown prompts (flow/plugin).

**Spec:** `docs/superpowers/specs/2026-11-13-flow-plan-review-design.md`

**Execution:** inline in the design session (owner's choice, 2026-11-13). Heavy commands go through `loom job run`. Commit by pathspec only.

---

## File structure

| File | Responsibility |
|---|---|
| `flow/lib/check_plan.py` (modify) | parse and lint the `full:` list of a `**Plan review:**` line |
| `flow/lib/check_plan_test.py` (modify) | the `full:` tests; existing tier assertions take the 3-tuple |
| `flow/lib/plan_review.py` (create) | the `**Plan review:**` line from `D/reviewed/` snapshots |
| `flow/lib/plan_review_test.py` (create) | its tests, including that its line passes `check-plan` |
| `internal/cli/flow.go` (modify) | `plan-review` in `flowTools` |
| `flow/plugin/agents/plan-reviewer.md` (create) | Sonnet pass and re-check |
| `flow/plugin/agents/risk-reviewer.md` (create) | Opus pass on escalated tasks and re-check |
| `flow/plugin/skills/draft/SKILL.md` (modify) | steps 4 to 6, the intro, `STATE.md` |
| `flow/plugin/agents/drafter.md` (modify) | the fix-job sentence |
| `flow/plugin/docs/dispatch.md` (modify) | the plan review section, fixing, routing table, review sequence |
| `flow/README.md`, `flow/PROVENANCE.md` (modify) | tool table, agent list, pins |

---

### Task 1: `check-plan` accepts a `full:` list

**Files:**
- Modify: `flow/lib/check_plan.py` (docstring lines 26-31, `TIER_RE` line 119, `review_tiers`, `vouched`, `tier_problems`)
- Test: `flow/lib/check_plan_test.py` (class `PlanReviewTierTest`)

- [ ] **Step 1: Update the existing tier assertions and add the failing tests**

In `PlanReviewTierTest`, change every `review_tiers` expectation to a 3-tuple (the third set is `full:`):

```python
    def test_review_tiers_reads_both_lists(self):
        review = "2026-10-15; code-quality-reviewer on seams; deep: 1, 6; recon on seams; light: 2 3,7"
        self.assertEqual(check_plan.review_tiers(review), ({"1", "6"}, {"2", "3", "7"}, set()))
```

```python
    def test_review_tiers_with_one_token_leaves_the_other_empty(self):
        self.assertEqual(check_plan.review_tiers("deep: 1"), ({"1"}, set(), set()))
        self.assertEqual(check_plan.review_tiers("recon on seams; light: 4, 5"), (set(), {"4", "5"}, set()))
```

```python
        self.assertEqual(check_plan.review_tiers("Deep: 1; Light: 2"), ({"1"}, {"2"}, set()))
```

```python
        self.assertEqual(check_plan.review_tiers("deep: 1a, B2; light: 3-x"), ({"1a", "B2"}, {"3-x"}, set()))
```

```python
        self.assertEqual(check_plan.review_tiers("deep: 1, 7."), ({"1", "7"}, set(), set()))
```

Change the two "neither" assertions (in `test_a_token_that_is_no_story_id_is_reported` and `test_a_story_in_neither_list_fails`) to:

```python
            self.assertIn("Story 2: the Plan review line lists it under none of deep:, light: and full:", out)
```

```python
        self.assertIn("Story 2: the Plan review line lists it under none of deep:, light: and full:", out)
```

Add, at the end of `PlanReviewTierTest`:

```python
    def test_review_tiers_reads_a_full_list(self):
        self.assertEqual(check_plan.review_tiers("deep: 1; light: 2; full: 3, 4"), ({"1"}, {"2"}, {"3", "4"}))
        self.assertEqual(check_plan.review_tiers("Full: 5"), (set(), set(), {"5"}))

    def test_vouched_leaves_a_full_story_unvouched(self):
        stories = [check_plan.Story("1", 1), check_plan.Story("2", 2)]
        self.assertEqual(check_plan.vouched("deep: 1; full: 2", stories), {"1": True, "2": False})

    def test_a_deep_story_under_full_passes_and_gets_the_full_review(self):
        review = "/flow:draft plan review; deep: 1; light: 2; full: 3"
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), ("3", None, "x"), review=review))
        self.assertEqual(code, 0, out)
        self.assertRegex(out, r"Story 1\s+.*deep\s+review: vouched")
        self.assertRegex(out, r"Story 2\s+.*light\s+review: full")
        self.assertRegex(out, r"Story 3\s+.*deep\s+review: full")

    def test_a_light_story_under_full_fails(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), review="deep: 1; full: 2"))
        self.assertEqual(code, 1, out)
        self.assertIn(
            "Story 2: the Plan review line lists it under full:, which holds only deep stories, "
            "and its Review line says light",
            out,
        )
        self.assertNotIn("Story 1: the Plan review line", out)

    def test_a_story_under_deep_and_full_fails(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", "deep", "x"), review="deep: 1, 2; full: 2"))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 2: the Plan review line lists it under both deep: and full:", out)

    def test_an_unknown_id_under_full_fails(self):
        code, out = run(tiered(("1", "deep", "x"), review="deep: 1; full: 9"))
        self.assertEqual(code, 1, out)
        self.assertIn("the Plan review line lists 9 under full:, which is no story id", out)
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `loom job run -d "check_plan tier tests" -- sh -c 'cd flow/lib && python3 -m unittest -q check_plan_test.PlanReviewTierTest'`
Expected: FAIL (3-tuple mismatches, the new messages missing).

- [ ] **Step 3: Implement**

`TIER_RE` and a list of the names:

```python
TIER_RE = re.compile(r"\b(deep|light|full):([^;]*)", re.IGNORECASE)
# A Plan review line's lists: `deep:` the vouched deep stories, `light:` the light ones, `full:` the deep
# stories a review didn't vouch for (/flow:draft's plan review writes it).
TIER_LISTS = ("deep", "light", "full")
```

`review_tiers`:

```python
def review_tiers(review: str | None) -> tuple[set[str], set[str], set[str]] | None:
    """The story ids after `deep:`, `light:` and `full:` in a Plan review line.

    Each list runs to the next `;` or the end of the line, its ids separated by commas and
    spaces, one trailing `.` stripped from each. None when the line has none of the tokens (a line
    written before tiers) or is missing.
    """
    if review is None:
        return None
    found = TIER_RE.findall(review)
    if not found:
        return None
    ids: dict[str, set[str]] = {name: set() for name in TIER_LISTS}
    for tier, rest in found:
        ids[tier.lower()].update(token.removesuffix(".") for token in re.split(r"[,\s]+", rest) if token.removesuffix("."))
    return ids["deep"], ids["light"], ids["full"]
```

`vouched`: replace `deep, _ = tiers` with `deep = tiers[0]`.

`tier_problems`, from `known = …` to the end of the function:

```python
    known = {story.sid for story in stories}
    for name, ids in zip(TIER_LISTS, tiers):
        for token in sorted(ids - known):
            problems.append(
                f"the Plan review line lists {token} under {name}:, which is no story id "
                "(list ids one at a time, on one physical line: no ranges, punctuation or backticks)"
            )
    for story in stories:
        listed = [name for name, ids in zip(TIER_LISTS, tiers) if story.sid in ids]
        if not listed:
            problems.append(f"Story {story.sid}: the Plan review line lists it under none of deep:, light: and full:")
            continue
        if len(listed) > 1:
            names = " and ".join(f"{name}:" for name in listed)
            problems.append(f"Story {story.sid}: the Plan review line lists it under {'both ' if len(listed) == 2 else ''}{names}")
            continue
        if "Review" in story.fields and not REVIEW_VALUE_RE.match(story.fields["Review"]):
            continue  # its Review value already fails the check
        says = f"its Review line says {story.tier}" if "Review" in story.fields else f"its tier is {story.tier} (no Review line)"
        if listed == ["full"]:
            if story.tier != "deep":
                problems.append(f"Story {story.sid}: the Plan review line lists it under full:, which holds only deep stories, and {says}")
        elif listed != [story.tier]:
            problems.append(f"Story {story.sid}: the Plan review line lists it as {listed[0]}, {says}")
    return problems
```

The module docstring's tier paragraph (lines 26-31) becomes:

```
the profile's landmine markers fails the check unless its reason names the marker. A Plan review line
may list the deep and light story ids as `deep: 1, 6; light: 2, 3`, and the deep stories the review
didn't vouch for as `full: 4`: a story it lists under the wrong tier, under more than one list or under
none fails the check, as does a token under any list that is no story id
(ids are listed one at a time, on one physical line), and so does a light story under a line that
lists no tiers at all. The output says per story whether the plan review vouches for it (a deep story
under `deep:`, or a deep story of a line written before tiers).
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `loom job run -d "check_plan tests" -- sh -c 'cd flow/lib && python3 -m unittest -q check_plan_test'`
Expected: OK.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(flow): check-plan reads a full: list of deep stories the plan review didn't vouch for" -- flow/lib/check_plan.py flow/lib/check_plan_test.py
```

---

### Task 2: `loom flow plan-review`

**Files:**
- Create: `flow/lib/plan_review.py`, `flow/lib/plan_review_test.py`
- Modify: `internal/cli/flow.go:38` (after the `premises` row)

- [ ] **Step 1: Write the failing tests**

`flow/lib/plan_review_test.py`:

```python
"""Tests for plan_review.py. Run from lib/: python3 -m unittest -q plan_review_test.py"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plan_review  # noqa: E402

SCRIPT = Path(__file__).with_name("plan_review.py")
CHECK_PLAN = Path(__file__).with_name("check_plan.py")
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}


def story(sid, review=None):
    lines = [
        f"## Story {sid}: Title {sid}",
        "",
        "- **Developer:** junior-dev",
        f"- **Files:** `f{sid}.py`",
        "- **Interfaces:** none",
        f"- **Verify:** `python3 -m unittest -q f{sid}_test.py`",
    ]
    if review:
        lines.append(f"- **Review:** {review}")
    return lines + ["", f"### Task {sid}.1: Task", "", "text", ""]


def plan_text(*stories):
    """A joined plan of (id, Review value or None) stories."""
    lines = ["# Plan", "", "**Spec:** `docs/spec.md`", "", "## Stories", ""]
    for sid, review in stories:
        lines += story(sid, review)
    return "\n".join(lines)


class Repo:
    """A throwaway git repo holding a joined plan, its drafts directory and join.json."""

    def __init__(self, root, stories, join=True):
        self.root = Path(root)
        subprocess.run(["git", "-C", root, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
        (self.root / ".claude").mkdir()
        (self.root / ".claude" / "flow.toml").write_text("schema = 1\n")
        (self.root / "docs").mkdir()
        (self.root / "docs" / "spec.md").write_text("# Spec\n")
        self.plan = self.root / "plans" / "plan.md"
        self.plan.parent.mkdir()
        self.plan.write_text(plan_text(*stories))
        self.drafts = self.root / ".flow" / "drafts" / "plan"
        self.drafts.mkdir(parents=True)
        for sid, _ in stories:
            (self.drafts / f"S{sid}.md").write_text(f"## Story {sid}: Title {sid}\n\ntext {sid}\n")
        if join:
            (self.drafts / "join.json").write_text(json.dumps({"stories": []}))
        self.env = {**CLEAN_ENV, "FLOW_HOME": str(self.root / "flow-home")}

    def review(self, sid, text=None):
        """Mark Story sid reviewed clean: a copy of its draft, or of `text`, under reviewed/."""
        (self.drafts / "reviewed").mkdir(exist_ok=True)
        body = text if text is not None else (self.drafts / f"S{sid}.md").read_text()
        (self.drafts / "reviewed" / f"S{sid}.md").write_text(body)

    def run(self, script=SCRIPT, *extra):
        return subprocess.run(
            [sys.executable, str(script), *extra, str(self.plan)], capture_output=True, text=True, env=self.env, cwd=self.root
        )


class PlanReviewTest(unittest.TestCase):
    def repo(self, stories, join=True):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        return Repo(tmp.name, stories, join)

    def test_a_deep_story_reviewed_byte_for_byte_is_vouched(self):
        r = self.repo([("1", None), ("2", "deep (store)")])
        r.review("1")
        r.review("2")
        result = r.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "**Plan review:** /flow:draft plan review; deep: 1, 2\n")

    def test_a_one_byte_difference_or_no_copy_lists_a_deep_story_under_full(self):
        r = self.repo([("1", None), ("2", None), ("3", None)])
        r.review("1")
        r.review("2", text=(r.drafts / "S2.md").read_text() + " ")
        result = r.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "**Plan review:** /flow:draft plan review; deep: 1; full: 2, 3\n")

    def test_a_light_story_is_never_vouched(self):
        r = self.repo([("1", None), ("2", "light (docs)")])
        r.review("1")
        r.review("2")
        result = r.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "**Plan review:** /flow:draft plan review; deep: 1; light: 2\n")

    def test_an_empty_list_is_left_out(self):
        r = self.repo([("1", None)])
        result = r.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "**Plan review:** /flow:draft plan review; full: 1\n")

    def test_a_missing_join_map_fails_with_nothing_on_stdout(self):
        r = self.repo([("1", None)], join=False)
        result = r.run()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("join.json", result.stderr)

    def test_a_missing_draft_fails_with_nothing_on_stdout(self):
        r = self.repo([("1", None), ("2", None)])
        (r.drafts / "S2.md").unlink()
        result = r.run()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("Story 2 has no draft", result.stderr)

    def test_a_plan_with_no_story_fails(self):
        r = self.repo([])
        result = r.run()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("no `## Story` section", result.stderr)

    def test_its_line_passes_check_plan_and_vouches_exactly_the_deep_list(self):
        r = self.repo([("1", None), ("2", "light"), ("3", None)])
        r.review("1")
        r.review("2")
        line = r.run().stdout.strip()
        r.plan.write_text(r.plan.read_text().replace("## Stories\n", f"{line}\n\n## Stories\n", 1))
        result = r.run(CHECK_PLAN, "--json")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        reviews = {s["id"]: s["review"] for s in json.loads(result.stdout)["stories"]}
        self.assertEqual(reviews, {"1": "vouched", "2": "full", "3": "full"})

    def test_line_joins_the_label_and_the_non_empty_lists(self):
        self.assertEqual(
            plan_review.line({"deep": ["1"], "light": [], "full": ["2", "3"]}),
            "**Plan review:** /flow:draft plan review; deep: 1; full: 2, 3",
        )


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "plan_review tests" -- sh -c 'cd flow/lib && python3 -m unittest -q plan_review_test'`
Expected: FAIL with `ModuleNotFoundError: No module named 'plan_review'`.

- [ ] **Step 3: Implement `flow/lib/plan_review.py`**

```python
#!/usr/bin/env python3
"""The `**Plan review:**` line of a plan /flow:draft reviewed.

usage: plan_review.py PLAN

/flow:draft's plan review (the dispatch guide's) copies each story's draft to D/reviewing/S<id>.md
when its review starts, and to D/reviewed/S<id>.md once that review, or its one re-check, is clean.
A deep story is vouched only when D/reviewed/S<id>.md is byte-identical to D/S<id>.md, the text the
join puts in the plan, so any change after the review un-vouches it. This prints the line the
manager writes above the skeleton's `## Stories`:

    **Plan review:** /flow:draft plan review; deep: 1, 6; light: 2, 3; full: 4

`deep:` lists the vouched deep stories, `light:` every light story (the run always reviews a light
story in full) and `full:` every other deep story; an empty list is left out. The stories and their
tiers come from the joined PLAN, D is the plan's drafts directory.

Exit 0 with the line on stdout; 1, with the reason on stderr and nothing on stdout, when the plan
holds no story, D/join.json is missing (D itself, or a plan never joined) or a story has no draft;
2 on a usage error.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_plan  # noqa: E402
from flowconf import FlowError  # noqa: E402
from plan_join import drafts_dir  # noqa: E402

LABEL = "/flow:draft plan review"


class ReviewError(Exception):
    """What keeps the line from being written; the message is a sentence."""


def tiers(text: str, drafts: Path) -> dict[str, list[str]]:
    """The plan's story ids under `deep` (vouched), `light` and `full`, in plan order."""
    stories, _problems, _spec = check_plan.parse(text)
    if not stories:
        raise ReviewError("the plan holds no `## Story` section: join it first")
    if not (drafts / "join.json").is_file():
        raise ReviewError(f"{drafts / 'join.json'} is missing: join the plan first")
    lists: dict[str, list[str]] = {"deep": [], "light": [], "full": []}
    for story in stories:
        draft = drafts / f"S{story.sid}.md"
        if not draft.is_file():
            raise ReviewError(f"Story {story.sid} has no draft {draft}")
        if story.tier == "light":
            lists["light"].append(story.sid)
            continue
        reviewed = drafts / "reviewed" / f"S{story.sid}.md"
        clean = reviewed.is_file() and reviewed.read_bytes() == draft.read_bytes()
        lists["deep" if clean else "full"].append(story.sid)
    return lists


def line(lists: dict[str, list[str]]) -> str:
    """The `**Plan review:**` line: the label, then each non-empty list in deep, light, full order."""
    parts = [LABEL, *(f"{name}: {', '.join(ids)}" for name, ids in lists.items() if ids)]
    return "**Plan review:** " + "; ".join(parts)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_review.py", description="Print the Plan review line of a plan /flow:draft reviewed.")
    parser.add_argument("plan", type=Path, help="the joined plan")
    args = parser.parse_args(argv)
    plan: Path = args.plan
    try:
        result = line(tiers(plan.read_text(encoding="utf-8"), drafts_dir(plan)))
    except (OSError, UnicodeDecodeError, FlowError, ReviewError) as err:
        print(f"plan_review.py: {plan}: {err}", file=sys.stderr)
        return 1
    print(result)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
```

- [ ] **Step 4: Register the tool**

In `internal/cli/flow.go`, after the `premises` row:

```go
	{name: "plan-review", script: "plan_review.py", short: "Print the Plan review line of a plan /flow:draft reviewed"},
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `loom job run -d "plan_review tests" -- sh -c 'cd flow/lib && python3 -m unittest -q plan_review_test'`
Expected: OK.
Run: `loom job run -d "flow cli tests" -- env CGO_ENABLED=0 go test ./internal/cli/ -run 'TestFlowTool|TestLoomContext'`
Expected: ok (`TestFlowTool_EveryToolStartsUnderIsolatedPython` runs `plan-review --help` under `-I`).

- [ ] **Step 6: Commit**

```bash
git add -- flow/lib/plan_review.py flow/lib/plan_review_test.py
git commit -m "feat(flow,cli): loom flow plan-review prints the Plan review line from the review snapshots" -- flow/lib/plan_review.py flow/lib/plan_review_test.py internal/cli/flow.go
```

---

### Task 3: the two reviewer agents

**Files:**
- Create: `flow/plugin/agents/plan-reviewer.md`, `flow/plugin/agents/risk-reviewer.md`

- [ ] **Step 1: Write `flow/plugin/agents/plan-reviewer.md`**

````markdown
---
name: plan-reviewer
description: Reviews one story's plan text in /flow:draft before the plan is committed (its tests and Fails without lines, copies of code that exists, the repo's rules) and escalates tasks that invent risky logic to flow:risk-reviewer; also re-checks a fixed story's diff. Read-only. Sonnet at high by the owner's instruction (2026-11-13). Pins set by the owner; change only on their instruction.
model: sonnet
effort: high
tools: Bash, Read, SendMessage
experimental:
  cacheTtl: 5m
---

You review one story of a plan that `/flow:draft` is drafting, before anything
is built. You work in the repository the session is in; its CLAUDE.md files
are your rules. The run copies this story's code into the tree, and a deep
story you pass is reviewed there only where the developer departs from it, so
a defect you miss in the plan's own code can reach main. Your job is the
defects a reviewer can see in the plan text and the tree, and the tasks that
need a stronger reviewer.

The dispatch gives the plan's path, the story's id and its plan lines, the
spec's path, the commit the drafts were written against (`<REV>`) and the
drafter's profile block (the prefix, the gates, the landmine notes). The names
contract sits above the plan's `## Stories`. Read the story's lines in slices
(`sed -n 'A,Bp' <plan>`), never the whole plan, and the code they touch at
`<REV>` (`git show <REV>:<path>`).

First, with the Read tool, read whole every CLAUDE.md on the path from the
repo root to each directory the story's **Files** line names
(`git ls-files -- '*CLAUDE.md'` lists them). Bash output never loads a
directory's CLAUDE.md, and its rules bind the story's code.

Then check, in order:

1. **Tests.** Each test the story writes fails when the code it covers is
   wrong: reason through the assertion against the code, don't assume it.
   Each `Fails without:` line names a change the test catches; one whose
   change the test survives is Important. A count the story states ("9
   tests", "41 in total") matches the tests it writes plus those that already
   exist. The story's **Verify** command runs the tests it names: a feature
   gate (`#![cfg(feature = …)]`, a test-only feature) or a filter that
   matches nothing makes it run none.
2. **Reuse.** Code the story writes that the tree or an earlier story already
   has: a helper, a constant, an error mapping, a fixture, a test harness.
   `grep -rn` the package and the code the story calls. A copy is Important;
   name what to call instead.
3. **The repo's rules** on the story's lines: its CLAUDE.md rules and the
   profile's landmine notes.
4. **Triage.** Write an `ESCALATE` line for each task whose code invents
   logic for store writes or id allocation; transactions and atomicity;
   concurrency (locks, tasks, channels, ordering); calls to outside services
   (timeouts, retries, limits); bounds (caps, overflow, pagination); or auth
   and tenancy. A task that only calls existing code for these isn't
   escalated. Don't review that logic yourself beyond checks 1 to 3: a
   stronger reviewer reads what you escalate.

Out of scope: names and behaviour between stories (the seams recon's), the
plan's quotes of the tree (premise-check settled them), and design the spec
settles. When the story's code follows the spec or its entry faithfully and
that is what's wrong, report it as `SPEC`: the manager settles it, with the
owner when the spec doesn't.

Severity: Critical when the code would break the build, lose or corrupt data,
or break a rule the repo enforces; Important when a test can't fail, a claim
is false, or code copies what exists; Minor for the rest. Report only what you
read at a `plan:<n>` you cite.

## The re-check

When the dispatch says re-check, it gives the story's earlier findings and a
`diff -u` command over two snapshots of its draft. Run it, and read only the
changed spans, with enough around them to judge. For each Critical and
Important finding answer `FIXED` or `OPEN`, then check the changed spans as
above and report anything new as a `FINDING`. Review nothing outside the diff.

## Report

One line each, then the coverage line:

- `FINDING <Critical|Important|Minor> plan:<n>: <defect>; <fix direction>`
- `ESCALATE Task <x.y> plan:<a>-<b>: <the risk, in one clause>`
- `SPEC plan:<n>: <what the spec or entry mandates, and why it breaks>`
- on a re-check: `FIXED <finding>` or `OPEN <finding>: <why>`
- `COVERAGE plan:<a>-<b> read; CLAUDE.md: <paths opened>; tests checked: <n>`

With nothing to report, write `FINDING none` before the coverage line.

You are read-only: report, never edit a file. `Bash` is for inspection only:
no `git add`/`commit`/`checkout`/`restore`/`stash`/`reset`/`clean`, no
redirection into the repo. Don't run builds or tests. Confirm every
`plan:<n>` and `file:line` you cite by reading it: a long grep output arrives
as a short preview, and `grep -r` skips gitignored files. Search the repo and
its dependency sources, never `/` or `~`.

Your report goes back to whoever dispatched you through the hand-back Claude
Code gives you when you finish. Don't also send it with SendMessage: that
delivers it twice into the dispatcher's context. A question you can't settle
goes in your report too.
````

- [ ] **Step 2: Write `flow/plugin/agents/risk-reviewer.md`**

````markdown
---
name: risk-reviewer
description: Reviews the tasks of one story's plan text that flow:plan-reviewer escalated, in /flow:draft before the plan is committed: whether invented logic for store writes, transactions, concurrency, outside calls, bounds or auth holds, and whether a test exercises it; also re-checks those findings' fixes. Read-only. Opus at high by the owner's instruction (2026-11-13). Pins set by the owner; change only on their instruction.
model: opus
effort: high
tools: Bash, Read, SendMessage
experimental:
  cacheTtl: 5m
---

You review the riskiest tasks of one story of a plan that `/flow:draft` is
drafting, before anything is built. You work in the repository the session is
in; its CLAUDE.md files are your rules. A `flow:plan-reviewer` has read the
whole story and escalated the tasks whose code invents logic where a defect
compiles, passes single-user tests and still breaks: store writes and ids,
transactions, concurrency, outside calls, bounds, auth and tenancy.

The dispatch gives the plan's path, the story's id and its plan lines, the
spec's path, `<REV>`, the drafter's profile block and the plan reviewer's
`ESCALATE` lines, each naming a task, its plan lines and the risk. Read only
those tasks' lines (`sed -n 'A,Bp' <plan>`) and the code they call or change
at `<REV>` (`git show <REV>:<path>`).

For each escalated task, check:

1. **The logic holds** when two callers race; when a step fails halfway (a
   write that lands without its pair, a retry that repeats a side effect);
   when an outside service is slow, down or answers with an error; at the
   limits (the empty case, the cap, overflow, the last page); and across
   users or permissions where the task touches them. Name the interleaving or
   the input that breaks it.
2. **A test exercises the risky path** and fails when it is wrong. A
   concurrency test on a single-threaded runtime, or a failure test that
   never injects the failure, passes vacuously.
3. **The design it follows holds.** When the task follows the spec or its
   entry faithfully and that is what breaks, report it as `SPEC`.

Out of scope: the rest of the story (the plan reviewer read it), names and
behaviour between stories, and the plan's quotes of the tree.

Severity: Critical when the logic loses or corrupts data, leaks across users,
or hangs; Important when it breaks under a named interleaving or input, or a
test of the risky path can't fail; Minor for the rest.

## The re-check

When the dispatch says re-check, it gives the story's earlier findings and a
`diff -u` command over two snapshots of its draft. Run it, and read only the
changed spans, with enough around them to judge. For each Critical and
Important finding of yours answer `FIXED` or `OPEN`, then check the changed
spans as above and report anything new as a `FINDING`.

## Report

One line each, then the coverage line:

- `FINDING <Critical|Important|Minor> plan:<n>: <defect>; <fix direction>`
- `SPEC plan:<n>: <what the spec or entry mandates, and why it breaks>`
- on a re-check: `FIXED <finding>` or `OPEN <finding>: <why>`
- `COVERAGE Tasks <x.y, …> read`

With nothing to report, write `FINDING none` before the coverage line.

You are read-only: report, never edit a file. `Bash` is for inspection only:
no `git add`/`commit`/`checkout`/`restore`/`stash`/`reset`/`clean`, no
redirection into the repo. Don't run builds or tests. Confirm every
`plan:<n>` and `file:line` you cite by reading it: a long grep output arrives
as a short preview, and `grep -r` skips gitignored files. Search the repo and
its dependency sources, never `/` or `~`.

Your report goes back to whoever dispatched you through the hand-back Claude
Code gives you when you finish. Don't also send it with SendMessage: that
delivers it twice into the dispatcher's context. A question you can't settle
goes in your report too.
````

- [ ] **Step 3: Run the agent lint**

Run: `bash flow/lib/check-agents.sh`
Expected: no FAIL line (descriptions under 400 characters, tools verified, cacheTtl set, no banned token).

- [ ] **Step 4: Commit**

```bash
git add -- flow/plugin/agents/plan-reviewer.md flow/plugin/agents/risk-reviewer.md
git commit -m "feat(flow): plan-reviewer and risk-reviewer agents for /flow:draft's plan review" -- flow/plugin/agents/plan-reviewer.md flow/plugin/agents/risk-reviewer.md
```

---

### Task 4: the draft skill runs the review

**Files:**
- Modify: `flow/plugin/skills/draft/SKILL.md`

- [ ] **Step 1: The description** (frontmatter) becomes:

```
description: Draft a supervised plan from its committed spec and skeleton with the flow roster, from a fresh session (/clear, then /flow:draft <plan>): drafters settle the names contract in a question round, then write the stories; it joins and checks them, one recon checks the seams and reviewers review each story, fresh drafters fix what they find, and it commits the plan for /flow:run.
```

- [ ] **Step 2: The intro.** "You write only the names contract, the story entries, the plan's header, `STATE.md`, the per-story findings files and the spec's answers" gains ", the review snapshots" after "the per-story findings files". "before it starts, and the run's full review checks the story again." becomes "before it starts, and step 5's re-check or the run's full review checks the story again."

- [ ] **Step 3: Step 4.** The heading `4. **Seams.**` becomes `4. **Seams and plan review.**`. After "Write its report on arrival to `D/findings/seams.md`." append, as a new paragraph inside step 4:

```
   In the same batch, dispatch one `flow:plan-reviewer` per story. Before each,
   copy the story's draft, the text its reviewer sees:
   `mkdir -p D/reviewing && cp D/S<id>.md D/reviewing/S<id>.md`. Its brief
   holds the plan's path, the story's id and plan lines (its `line` and `end`
   in `loom flow check-plan --json $ARGUMENTS`), the spec's path, `<REV>` and
   the drafter's profile block. Write each report on arrival to
   `D/findings/review-S<id>.md`. When it holds `ESCALATE` lines, dispatch one
   `flow:risk-reviewer` for the story with the same brief and those lines,
   and append its report to the same file. A report with no `COVERAGE` line,
   or a reviewer that goes quiet, goes to a fresh reviewer with the same
   brief; never message the quiet one. A `SPEC` finding is yours, as
   a DESIGN verdict is. A finding you judge wrong is closed with your reason
   written under it in the review file. A story's review is clean when no
   Critical, Important or `SPEC` finding of its review or its risk review is
   open; then `mkdir -p D/reviewed && cp D/reviewing/S<id>.md D/reviewed/S<id>.md`.
   On a resume, a story with a `D/reviewing/` copy and no review file is
   reviewed again.
```

- [ ] **Step 4: Step 5.** In the bullet "Before the fixers start, write `D/findings/S<id>.md` …", "and the seams findings that name it." becomes "the seams findings that name it, and its review's open findings, Minor ones included." Replace the bullet "Then run step 3 again, and step 4 too when a fix changed the contract." with:

```
   - Then run step 3 again, and step 4's seams recon too when a fix changed
     the contract; the plan review doesn't run again.
   - Re-check each story whose draft differs from its review copy
     (`cmp -s D/reviewing/S<id>.md D/S<id>.md` fails), whatever changed it:
     `mkdir -p D/rechecking && cp D/S<id>.md D/rechecking/S<id>.md`, then one
     fresh `flow:plan-reviewer`, or a fresh `flow:risk-reviewer` when a risk
     reviewer's finding was among those fixed (both when both). Its brief is
     the review's plus the story's review findings, the draft's path and
     "re-check: `diff -u D/reviewing/S<id>.md D/rechecking/S<id>.md`". Append
     each report to `D/findings/review-S<id>.md`. When every Critical and
     Important finding is FIXED and no new one is Critical or Important:
     `cp D/rechecking/S<id>.md D/reviewed/S<id>.md`. There is no second
     re-check, and an open review finding doesn't go to the owner: the run
     reviews that story in full. On a resume, a story with a `D/rechecking/`
     copy and no re-check report in its review file is re-checked again.
```

- [ ] **Step 5: Step 6.** After the pricing bullet, before "Write the Design stage line", insert:

```
   - Run `loom flow plan-review $ARGUMENTS`; on a failure, show its message
     and stop. Its output is the `**Plan review:**` line: a deep story is
     vouched only when its draft is byte-identical to its `D/reviewed/` copy.
```

"Write the Design stage line (below) into `STATE.md`'s **Design stage** slot and into the skeleton's head, directly above `## Stories`." becomes "Write the Design stage line (below) into `STATE.md`'s **Design stage** slot, and it and the Plan review line into the skeleton's head, directly above `## Stories`." In the last bullet, "what the checks and the seams recon found" becomes "what the checks, the seams recon and the plan review found, which stories the review vouched, its cost (the plan-reviewer and risk-reviewer rows of `loom flow costs session`)".

- [ ] **Step 6: `STATE.md`.** The Phase line's list becomes `<questions | drafting | checking | seams | reviewing | fixing | committed>`; the table header becomes `| Story | Tier | Effort | Drafter | Deviations settled | Findings (premises, seams, review) | Plan review | Fixer |` with `|---|---|---|---|---|---|---|---|`; add under the table: "Plan review: pending, clean, findings, re-check, vouched or full." The In flight line becomes `- <agent id>: <questions | drafting | waiting on Stories <ids> | review | risk review | re-check | fix> <story ids>`. The findings-files line becomes `- <D/findings/premises.md, seams.md, review-S<id>.md or S<id>.md: which agent (or the manager), and when>`.

- [ ] **Step 7: Lint and commit**

Run: `bash flow/lib/check-agents.sh` — expected no FAIL (the skill's description stays under 400 characters).

```bash
git commit -m "feat(flow): /flow:draft reviews each story, re-checks fixes and writes the Plan review line" -- flow/plugin/skills/draft/SKILL.md
```

---

### Task 5: dispatch guide, drafter, README, provenance

**Files:**
- Modify: `flow/plugin/docs/dispatch.md`, `flow/plugin/agents/drafter.md`, `flow/README.md`, `flow/PROVENANCE.md`

- [ ] **Step 1: `dispatch.md`, the seams section.** Replace its first paragraph's opening ("The design stage reviews no story. … gets the run's full review instead.\nWhat the scripts can't check across stories goes to one `flow:recon`.") with "What the scripts can't check across stories goes to one `flow:recon`."

- [ ] **Step 2: `dispatch.md`, a new subsection** after the seams section's last paragraph ("… Its report goes to `D/findings/seams.md`."), before `### Fixing the plan`:

```markdown
### The plan review

Nothing else reviews a story's own code before the run, and nearly every
plan-code defect that reaches it sits inside one story: in the 7 engine runs
of 2026-11-11 to 2026-11-13, the 53 stories with a plan-code question took 65
fix rounds and $74.70 of fix and re-review, the 22 without one 4 rounds and
$1.60. A per-story review loop once cost about as much as the run it planned,
so this review is one pass that must pay for itself (the owner, 2026-11-13;
loom's `flow/PROVENANCE.md` has the evidence). In the same batch as the seams
recon, one `flow:plan-reviewer` (Sonnet @ high) per story checks its tests and
`Fails without:` lines, copies of code that exists and the repo's rules, and
escalates each task whose code invents logic for store writes, transactions,
concurrency, outside calls, bounds or auth to a `flow:risk-reviewer` (Opus @
high), which reads only those tasks. Either may report a `SPEC` finding: code
that follows a spec or entry that is itself wrong, settled like a DESIGN
verdict. Findings join the story's fix job. After the fixes, a story whose
draft changed since its review gets one re-check of the diff, never a loop.

`/flow:draft` copies each draft to `D/reviewing/` as its review starts and to
`D/reviewed/` once the review or its re-check is clean, and
`loom flow plan-review <plan>` writes the `**Plan review:**` line from those
copies: a deep story is listed under `deep:`, vouched, only when its draft is
byte-identical to its `D/reviewed/` copy; every other deep story goes under
`full:`, and every light story under `light:`. A vouched story's first run
review is `flow:story-reviewer`'s, which reads only what departs from the plan.
```

- [ ] **Step 3: `dispatch.md`, Fixing the plan.** In step 2, "are decided before it starts, and the run's full review checks the story\n   again (the owner, 2026-10-31;" becomes "are decided before it starts, and a re-check or the run's full review\n   checks the story again (the owner, 2026-10-31;"; "and the\n   seams findings that name the story." becomes "the seams findings\n   that name the story, and its plan review's open findings." Step 3 gains a sentence: "Then a story whose draft changed since its plan review gets its one re-check." Step 5's "then writes the `**Design stage:**` line into the skeleton's head" becomes "then writes the `**Design stage:**` line and `loom flow plan-review`'s `**Plan review:**` line into the skeleton's head". Replace the paragraph "No `**Plan review:**` line is written, so `loom flow check-plan` marks every story\n`review: full`, and the run reviews each story in full." with "`loom flow check-plan` then marks each story `review: vouched` or `review: full` from that line."

- [ ] **Step 4: `dispatch.md`, the routing table.** After the `flow:premise-check` row add:

```markdown
| Reviewing each story's plan code in `/flow:draft`, and re-checking its fixes | `flow:plan-reviewer`; the tasks it escalates to `flow:risk-reviewer` |
```

- [ ] **Step 5: `dispatch.md`, Review sequence.** "For a `full` story (a light story, or any\nstory of a plan without a `**Plan review:**` line)" becomes "For a `full` story (a light story, a deep story the plan review\nlisted under `full:`, or any story of a plan without a `**Plan review:**` line)".

- [ ] **Step 6: `drafter.md`.** "are decided before it starts, and the run's full review checks the story\n  again." becomes "are decided before it starts, and a re-check or the run's full review\n  checks the story again."

- [ ] **Step 7: `README.md`.** The agents row: "the 12 pinned agents: `drafter`, …, `glance`" becomes "the 14 pinned agents: `drafter`, `junior-dev`, `intermediate-dev`, `senior-dev`, `doc-writer`, `verifier`, `spec-reviewer`, `code-quality-reviewer`, `story-reviewer`, `plan-reviewer`, `risk-reviewer`, `premise-check`, `recon`, `glance`". In the tool table, the `check-plan` row's "a `pub` Rust item's doc link to an item the plan keeps private;" gains after it " a `**Plan review:**` line's `deep:`, `light:` and `full:` lists;"; after the `premises` row add:

```markdown
| `plan-review` | `plan_review.py`: the `**Plan review:**` line from `/flow:draft`'s review copies (`D/reviewed/`) |
```

- [ ] **Step 8: `PROVENANCE.md`.** Add the two agents to the alphabetical headings, `## plan-reviewer` after `## plan-execution-supervisor` and `## risk-reviewer` after `## recon`, each with one entry:

```markdown
## plan-reviewer

- 2026-11-13, a new agent at `model: sonnet`, `effort: high`, no `maxTurns`, `cacheTtl: 5m`, on the owner's instruction in the plan review's design (`docs/superpowers/specs/2026-11-13-flow-plan-review-design.md` §2): one pass per story in `/flow:draft`, which must pay for itself. Evidence, from the 7 engine runs of 2026-11-11 to 2026-11-13 (152,028 plan lines): the 53 stories with a plan-code question took 65 fix rounds and $74.70 of fix and re-review, the 22 without one 4 rounds and $1.60; a first review by code-quality-reviewer averaged $1.10, a story-reviewer re-review $0.56. Wrong if, over the next 3 plans drafted with the review: plan-code questions don't fall below 0.20 per 1,000 plan lines (0.35 before), design stage plus run costs more than $5.16 per 1,000 plan lines, or the review averages over $1.20 a story. Check which plugin copy each run's sessions used first.
```

```markdown
## risk-reviewer

- 2026-11-13, a new agent at `model: opus`, `effort: high`, no `maxTurns`, `cacheTtl: 5m`, on the owner's instruction in the plan review's design (`docs/superpowers/specs/2026-11-13-flow-plan-review-design.md` §2): reviews only the tasks `plan-reviewer` escalates. Evidence: about 12 of the ~95 plan-code defects in the 8 drafted plans of 2026-11-10 to 2026-11-13 were invented concurrency, atomicity, ordering or bounds logic (ids from `MAX(id)+1`, a read and a write in separate transactions, 500 outside calls with no timeout), and none held a landmine marker. Wrong if: as `plan-reviewer`'s entry.
```

- [ ] **Step 9: Lint and commit**

Run: `bash flow/lib/check-agents.sh` — expected no FAIL (no banned token in the guide).

```bash
git commit -m "docs(flow): the plan review in the dispatch guide, drafter, README and provenance" -- flow/plugin/docs/dispatch.md flow/plugin/agents/drafter.md flow/README.md flow/PROVENANCE.md
```

---

### Task 6: verification

- [ ] **Step 1: The flow suite and the agent lint**

Run: `loom job run -d "flow lib tests" -- bash flow/lib/test.sh`
Expected: `OK`, no FAIL line.

- [ ] **Step 2: Go**

Run: `loom job run -d "go tests flow cli claudemd" -- env CGO_ENABLED=0 go test ./flow/ ./internal/cli/ ./tools/claudemd/`
Expected: ok for each.

- [ ] **Step 3: gofmt**

Run: `gofmt -l internal/cli/flow.go`
Expected: no output.

- [ ] **Step 4: Read-through** of the skill's steps 4 to 6 against spec §4.1 to §4.7, and of the dispatch guide's new section against §4.5 and §4.6.
