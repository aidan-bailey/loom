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
