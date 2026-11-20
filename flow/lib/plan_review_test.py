"""Tests for plan_review.py. Run from lib/: python3 -m unittest -q plan_review_test.py"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("plan_review.py")
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}


def story(sid, review=None, code=None, form=None):
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
    if form:
        lines.append(f"- **Form:** {form}")
    lines += ["", f"### Task {sid}.1: Task", "", "text", ""]
    if code:
        lines += ["```rust", *code, "```", ""]
    return lines


def plan_text(*stories):
    """A joined plan of (id, Review value or None[, its fenced code lines[, its Form value]]) stories."""
    lines = ["# Plan", "", "**Spec:** `docs/spec.md`", "", "## Stories", ""]
    for sid, review, *rest in stories:
        code = rest[0] if rest else None
        form = rest[1] if len(rest) > 1 else None
        lines += story(sid, review, code, form)
    return "\n".join(lines)


PROFILE = 'schema = 1\n[landmines]\nmarkers = ["user_id = $", "cfg(not(feature"]\n'


class Repo:
    """A throwaway git repo holding a joined plan, its drafts directory and join.json."""

    def __init__(self, root, stories, join=True, profile="schema = 1\n"):
        self.root = Path(root)
        subprocess.run(["git", "-C", root, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
        (self.root / ".claude").mkdir()
        (self.root / ".claude" / "flow.toml").write_text(profile)
        (self.root / "docs").mkdir()
        (self.root / "docs" / "spec.md").write_text("# Spec\n")
        self.plan = self.root / "plans" / "plan.md"
        self.plan.parent.mkdir()
        self.plan.write_text(plan_text(*stories))
        self.drafts = self.root / ".flow" / "drafts" / "plan"
        self.drafts.mkdir(parents=True)
        for sid, *_ in stories:
            (self.drafts / f"S{sid}.md").write_text(f"## Story {sid}: Title {sid}\n\ntext {sid}\n")
        if join:
            (self.drafts / "join.json").write_text(json.dumps({"stories": []}))
        self.env = {**CLEAN_ENV, "FLOW_HOME": str(self.root / "flow-home")}

    def risk(self, sid, text):
        """Write Story sid's drafter risk file, D/risk/S<id>.md."""
        (self.drafts / "risk").mkdir(exist_ok=True)
        (self.drafts / "risk" / f"S{sid}.md").write_text(text)

    def run(self, script=SCRIPT, *extra):
        return subprocess.run(
            [sys.executable, str(script), *extra, str(self.plan)], capture_output=True, text=True, env=self.env, cwd=self.root
        )


class PlanReviewTest(unittest.TestCase):
    def repo(self, stories, join=True, profile="schema = 1\n"):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        return Repo(tmp.name, stories, join, profile)

    def test_the_bare_form_is_a_usage_error_naming_select(self):
        # It printed the **Plan review:** line before the owner removed it on 2026-11-20.
        r = self.repo([("1", None)])
        r.risk("1", "none\n")
        for plan in (str(r.plan), str(r.root / "missing.md")):
            with self.subTest(plan=plan):
                result = subprocess.run([sys.executable, str(SCRIPT), plan], capture_output=True, text=True, env=r.env, cwd=r.root)
                self.assertEqual((result.returncode, result.stdout), (2, ""))
                self.assertIn("usage: plan_review.py --select PLAN", result.stderr)
                self.assertIn("--select is required", result.stderr)
        self.assertEqual(r.run(SCRIPT, "--select").returncode, 0, "the same plan with --select")

    def test_select_fails_on_a_plan_with_no_story(self):
        r = self.repo([])
        result = r.run(SCRIPT, "--select")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("no `## Story` section", result.stderr)

    def test_select_skips_a_story_whose_risk_file_says_none_and_holds_no_marker(self):
        r = self.repo([("1", None, ["let rows = query(user_id = $1);"]), ("2", None), ("3", "light")], profile=PROFILE)
        r.risk("1", "none\n")
        r.risk("2", "\nnone\n\n")
        r.risk("3", "none\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout,
            "Story 1: review (landmines: user_id = $)\nStory 2: skip\nStory 3: skip\n1 of 3 stories to review: 1\n",
        )

    def test_select_reviews_a_story_whose_risk_file_lists_a_task(self):
        r = self.repo([("1", None), ("2", "light")], profile=PROFILE)
        r.risk("1", "- Task 1.1: allocates ids as MAX(id)+1\n- Task 1.3: retries the upload\n")
        r.risk("2", "- Task 2.1: caps the page at 500 rows\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout,
            "Story 1: review (risk: Task 1.1, Task 1.3)\nStory 2: review (risk: Task 2.1)\n2 of 2 stories to review: 1, 2\n",
        )

    def test_select_reviews_a_story_with_no_risk_file_or_one_it_cant_read(self):
        r = self.repo([("1", None), ("2", None), ("3", None), ("4", None)])
        r.risk("2", "")
        r.risk("3", "none\n- Task 3.1: locks the ledger\n")
        r.risk("4", "nothing risky here\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout,
            "Story 1: review (no risk file)\n"
            "Story 2: review (risk file is neither `none` nor a list)\n"
            "Story 3: review (risk file is neither `none` nor a list)\n"
            "Story 4: review (risk file is neither `none` nor a list)\n"
            "4 of 4 stories to review: 1, 2, 3, 4\n",
        )

    def test_select_gives_both_reasons_and_says_none_when_it_skips_every_story(self):
        r = self.repo([("1", None, ["#[cfg(not(feature = \"x\"))]"])], profile=PROFILE)
        r.risk("1", "- Task 1.1: writes the store\n")
        both = r.run(SCRIPT, "--select")
        self.assertEqual(both.stdout, "Story 1: review (landmines: cfg(not(feature; risk: Task 1.1)\n1 of 1 stories to review: 1\n")
        r = self.repo([("1", None)])
        r.risk("1", "none\n")
        skipped = r.run(SCRIPT, "--select")
        self.assertEqual(skipped.returncode, 0, skipped.stderr)
        self.assertEqual(skipped.stdout, "Story 1: skip\n0 of 1 stories to review: none\n")

    def test_select_fails_on_a_missing_draft_or_join_map(self):
        r = self.repo([("1", None), ("2", None)])
        (r.drafts / "S2.md").unlink()
        result = r.run(SCRIPT, "--select")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("Story 2 has no draft", result.stderr)
        r = self.repo([("1", None)], join=False)
        result = r.run(SCRIPT, "--select")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("join.json", result.stderr)

    def test_select_fails_without_a_usable_profile(self):
        r = self.repo([("1", None)], profile="schema = 1\n[landmines]\nmarkers = 3\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("landmines.markers", result.stderr)

    def test_select_skips_a_mechanical_and_a_brief_story(self):
        r = self.repo([("1", None, None, "mechanical (moves the limits)"), ("2", "light", None, "brief"), ("3", None)])
        r.risk("1", "none\n")
        r.risk("2", "none\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout,
            "Story 1: skip (mechanical)\nStory 2: skip (brief)\nStory 3: review (no risk file)\n1 of 3 stories to review: 3\n",
        )

    def test_select_fails_a_brief_story_whose_drafter_lists_a_risky_task(self):
        r = self.repo([("1", None), ("2", None, None, "brief (a flag)")])
        r.risk("1", "none\n")
        r.risk("2", "- Task 2.1: retries the upload\n- Task 2.3: caps the page\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn(
            "Story 2 is brief but its drafter lists Task 2.1, Task 2.3 (risky logic): "
            "make it full, or name each task in the Form line's reason",
            result.stderr,
        )

    def test_select_fails_a_mechanical_story_likewise(self):
        r = self.repo([("1", None, None, "mechanical"), ("2", None, None, "brief")])
        r.risk("1", "- Task 1.2: allocates ids\n")
        r.risk("2", "- Task 2.1: locks the ledger\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("Story 1 is mechanical but its drafter lists Task 1.2 (risky logic)", result.stderr)
        self.assertIn("Story 2 is brief but its drafter lists Task 2.1 (risky logic)", result.stderr)

    def test_select_passes_a_non_full_story_whose_reason_names_every_listed_task(self):
        r = self.repo([("1", None, None, "brief (Task 1.1 only reads a cap the store keeps)")])
        r.risk("1", "- Task 1.1: reads the page cap\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "Story 1: skip (brief)\n0 of 1 stories to review: none\n")
        r = self.repo([("1", None, None, "brief (Task 1.1 only reads a cap)")])
        r.risk("1", "- Task 1.1: reads the page cap\n- Task 1.2: writes the store\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 1)
        self.assertIn("lists Task 1.2 (risky logic)", result.stderr)
        self.assertNotIn("Task 1.1,", result.stderr)

    def test_select_skips_a_non_full_story_with_no_risk_file(self):
        r = self.repo([("1", None, None, "mechanical"), ("2", None, None, "brief")])
        r.risk("2", "nothing risky here\n")
        result = r.run(SCRIPT, "--select")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "Story 1: skip (mechanical)\nStory 2: skip (brief)\n0 of 2 stories to review: none\n")


if __name__ == "__main__":
    unittest.main()
