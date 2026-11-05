"""Tests for plan_join.py. Run from lib/: python3 -m unittest -q plan_join_test.py"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plan_join  # noqa: E402
from plan_join import MARKER, JoinError  # noqa: E402

SCRIPT = Path(__file__).with_name("plan_join.py")
FENCE = "`" * 3

SKELETON_LINES = [
    "# Plan",
    "",
    "## Stories",
    "",
    "- **Story 1** — first.",
    "  - **Developer:** junior-dev",
    "- **Story 2b** — second.",
    "  - **Developer:** junior-dev",
    "",
    MARKER,
]
SKELETON = "\n".join(SKELETON_LINES) + "\n"
DRAFT_1 = "## Story 1: First\n\n- **Developer:** junior-dev\n\n### Task 1.1: x\n"
DRAFT_2 = "## Story 2b: Second\n\ntext\n"
# The marker is plan line 10, line 11 is blank, Story 1 takes 5 lines from line 12,
# line 17 is blank and Story 2b takes 3 lines from line 18.
EXPECTED = SKELETON + "\n" + DRAFT_1.rstrip() + "\n\n" + DRAFT_2.rstrip() + "\n"
EXPECTED_MAP = {"1": [12, 5], "2b": [18, 3]}


class Tree:
    """A throwaway repo holding one plan, the repo's own profile and the plan's drafts directory."""

    def __init__(self, root, plan_text, drafts):
        self.root = Path(root)
        (self.root / ".git").mkdir()
        # The repo's own profile wins whole, so the home store never decides where the drafts are.
        (self.root / ".claude").mkdir()
        self.profile = self.root / ".claude" / "flow.toml"
        self.profile.write_text("schema = 1\n")
        self.plan = self.root / "plans" / "plan.md"
        self.plan.parent.mkdir()
        self.plan.write_text(plan_text)
        self.drafts = self.root / ".flow" / "drafts" / "plan"
        self.drafts.mkdir(parents=True)
        for sid, body in drafts.items():
            self.write(sid, body)

    def write(self, sid, body):
        (self.drafts / f"S{sid}.md").write_text(body)


class TreeCase(unittest.TestCase):
    def tree(self, plan_text=SKELETON, drafts=None):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        if drafts is None:
            drafts = {"1": DRAFT_1, "2b": DRAFT_2}
        return Tree(tmp.name, plan_text, drafts)


class StoryOrderTest(unittest.TestCase):
    def test_ids_come_in_list_order(self):
        self.assertEqual(plan_join.story_order(SKELETON), ["1", "2b"])

    def test_fenced_entries_and_later_sections_are_ignored(self):
        text = "\n".join(
            [
                "## Stories",
                "- **Story 1** — a",
                FENCE + "text",
                "- **Story 9** — fenced",
                FENCE,
                "",
                "## Other",
                "- **Story 8** — after the section",
            ]
        )
        self.assertEqual(plan_join.story_order(text), ["1"])

    def test_entry_forms_the_grammar_rejects_are_not_read(self):
        for line in (
            "- **Story 3:** a colon inside the bold",
            "- **Story 3**: a colon after it",
            "1. **Story 3** numbered",
            "  - **Story 3** indented",
            "* **Story 3** another bullet",
        ):
            self.assertEqual(plan_join.story_order("## Stories\n\n- **Story 1** — kept.\n" + line + "\n"), ["1"], line)

    def test_an_entry_label_at_the_end_of_the_line_is_read(self):
        text = "## Stories\n\n- **Story 1** — kept.\n- **Story 3**\n"
        self.assertEqual(plan_join.story_order(text), ["1", "3"])

    def test_a_heading_other_than_stories_is_not_the_list(self):
        for heading in ("### Stories", "## Story list"):
            with self.assertRaisesRegex(JoinError, "no ## Stories section"):
                plan_join.story_order(heading + "\n\n- **Story 1** — x.\n")

    def test_missing_section_fails(self):
        with self.assertRaisesRegex(JoinError, "no ## Stories section"):
            plan_join.story_order("# Plan\n- **Story 1** — x\n")

    def test_empty_section_fails(self):
        with self.assertRaisesRegex(JoinError, "lists no stories"):
            plan_join.story_order("## Stories\n\nprose only\n")

    def test_repeated_id_fails(self):
        text = "## Stories\n- **Story 1** — a\n- **Story 1** — b\n"
        with self.assertRaisesRegex(JoinError, "Story 1 is listed twice"):
            plan_join.story_order(text)


class JoinTest(TreeCase):
    def test_output_shape(self):
        t = self.tree()
        out, story_map = plan_join.join(SKELETON, t.drafts)
        self.assertEqual(out, EXPECTED)
        self.assertTrue(out.endswith("\n"))
        self.assertFalse(out.endswith("\n\n"))
        self.assertEqual(story_map, EXPECTED_MAP)

    def test_trailing_whitespace_is_stripped(self):
        t = self.tree(drafts={"1": DRAFT_1 + "\n\n  \n", "2b": DRAFT_2 + "   \n\n"})
        out, story_map = plan_join.join(SKELETON, t.drafts)
        self.assertEqual(out, EXPECTED)
        self.assertEqual(story_map, EXPECTED_MAP)

    def test_whatever_followed_the_marker_is_replaced(self):
        t = self.tree()
        out, _ = plan_join.join(SKELETON + "\nold generated text\n\n## Story 7: gone\n", t.drafts)
        self.assertEqual(out, EXPECTED)

    def test_text_up_to_the_marker_is_kept(self):
        t = self.tree()
        out, _ = plan_join.join(SKELETON, t.drafts)
        self.assertTrue(out.startswith(SKELETON))

    def test_order_follows_the_list_not_the_file_names(self):
        lines = list(SKELETON_LINES)
        lines[4], lines[6] = "- **Story 2b** — second.", "- **Story 1** — first."
        t = self.tree()
        out, story_map = plan_join.join("\n".join(lines) + "\n", t.drafts)
        self.assertLess(out.index("## Story 2b"), out.index("## Story 1:"))
        self.assertEqual(story_map, {"2b": [12, 3], "1": [16, 5]})

    def test_map_points_at_each_drafts_lines(self):
        t = self.tree()
        out, story_map = plan_join.join(SKELETON, t.drafts)
        out_lines = out.split("\n")
        for sid, body in {"1": DRAFT_1, "2b": DRAFT_2}.items():
            start, count = story_map[sid]
            draft_lines = body.rstrip().split("\n")
            self.assertEqual(count, len(draft_lines))
            for k, line in enumerate(draft_lines, 1):
                self.assertEqual(out_lines[start + k - 2], line)

    def test_leading_blank_lines_keep_the_drafts_line_numbers(self):
        t = self.tree(drafts={"1": "\n" + DRAFT_1, "2b": DRAFT_2})
        out, story_map = plan_join.join(SKELETON, t.drafts)
        out_lines = out.split("\n")
        self.assertEqual(story_map, {"1": [12, 6], "2b": [19, 3]})
        self.assertEqual(out_lines[11], "")
        self.assertEqual(out_lines[12], "## Story 1: First")

    def test_joining_twice_gives_the_same_text_and_map(self):
        t = self.tree()
        first, first_map = plan_join.join(SKELETON, t.drafts)
        second, second_map = plan_join.join(first, t.drafts)
        self.assertEqual(second, first)
        self.assertEqual(second_map, first_map)

    def test_files_that_are_not_story_drafts_are_ignored(self):
        # The state file, the join map, the findings and packets subdirectories, and a name with a second dot.
        t = self.tree()
        t.write("4.notes", "## Story 4: stale\n")
        (t.drafts / "STATE.md").write_text("# state\n")
        (t.drafts / "join.json").write_text("{}\n")
        (t.drafts / "findings").mkdir()
        (t.drafts / "findings" / "S1.md").write_text("findings\n")
        (t.drafts / "packets").mkdir()
        (t.drafts / "packets" / "seams.md").write_text("packet\n")
        out, _ = plan_join.join(SKELETON, t.drafts)
        self.assertEqual(out, EXPECTED)

    def test_no_marker_fails(self):
        t = self.tree()
        text = "\n".join(SKELETON_LINES[:-1]) + "\n"
        with self.assertRaisesRegex(JoinError, "no marker line"):
            plan_join.join(text, t.drafts)

    def test_a_marker_inside_a_fence_does_not_count(self):
        t = self.tree()
        text = "\n".join(SKELETON_LINES[:-1] + [FENCE + "text", MARKER, FENCE]) + "\n"
        with self.assertRaisesRegex(JoinError, "no marker line"):
            plan_join.join(text, t.drafts)

    def test_a_fenced_marker_beside_the_real_one_is_not_a_second(self):
        t = self.tree()
        text = "\n".join([FENCE + "text", MARKER, FENCE] + SKELETON_LINES) + "\n"
        out, _ = plan_join.join(text, t.drafts)
        self.assertTrue(out.startswith(text))

    def test_two_markers_fail(self):
        t = self.tree()
        with self.assertRaisesRegex(JoinError, "2 marker lines"):
            plan_join.join(SKELETON + MARKER + "\n", t.drafts)

    def test_listed_story_without_a_draft_fails(self):
        t = self.tree()
        (t.drafts / "S2b.md").unlink()
        with self.assertRaisesRegex(JoinError, "Story 2b is listed in the plan but S2b.md does not exist"):
            plan_join.join(SKELETON, t.drafts)

    def test_missing_drafts_directory_fails_the_same_way(self):
        t = self.tree()
        with self.assertRaisesRegex(JoinError, "S1.md does not exist"):
            plan_join.join(SKELETON, t.root / "nowhere")

    def test_draft_that_is_not_listed_fails(self):
        t = self.tree()
        t.write("3", "## Story 3: Extra\n")
        with self.assertRaisesRegex(JoinError, "S3.md is not in the plan's story list"):
            plan_join.join(SKELETON, t.drafts)

    def test_draft_for_another_story_fails(self):
        t = self.tree(drafts={"1": DRAFT_1, "2b": DRAFT_1})
        with self.assertRaisesRegex(JoinError, "S2b.md must start with '## Story 2b'"):
            plan_join.join(SKELETON, t.drafts)

    def test_draft_not_starting_with_its_heading_fails(self):
        t = self.tree(drafts={"1": "intro\n" + DRAFT_1, "2b": DRAFT_2})
        with self.assertRaisesRegex(JoinError, "S1.md must start with '## Story 1'"):
            plan_join.join(SKELETON, t.drafts)

    def test_empty_draft_fails(self):
        t = self.tree(drafts={"1": "\n  \n", "2b": DRAFT_2})
        with self.assertRaisesRegex(JoinError, "S1.md must start with '## Story 1'"):
            plan_join.join(SKELETON, t.drafts)

    def test_plan_without_a_stories_section_fails(self):
        t = self.tree()
        with self.assertRaisesRegex(JoinError, "no ## Stories section"):
            plan_join.join("# Plan\n" + MARKER + "\n", t.drafts)

    def test_a_draft_holding_the_marker_outside_a_fence_fails_the_first_join(self):
        t = self.tree(drafts={"1": DRAFT_1, "2b": DRAFT_2 + MARKER + "\n"})
        with self.assertRaisesRegex(JoinError, r"S2b.md holds the marker line outside a fence \(plan line 21\)"):
            plan_join.join(SKELETON, t.drafts)

    def test_a_draft_quoting_the_marker_in_a_fence_joins_twice(self):
        t = self.tree(drafts={"1": DRAFT_1, "2b": DRAFT_2 + FENCE + "text\n" + MARKER + "\n" + FENCE + "\n"})
        first, first_map = plan_join.join(SKELETON, t.drafts)
        second, second_map = plan_join.join(first, t.drafts)
        self.assertEqual(second, first)
        self.assertEqual(second_map, first_map)

    def test_a_draft_holding_a_stories_section_joins_twice(self):
        # Only the text up to the marker is read for the story list, so the draft's own section is inert.
        draft = DRAFT_2 + "\n## Stories\n\n- **Story 9** — quoted.\n"
        t = self.tree(drafts={"1": DRAFT_1, "2b": draft})
        first, first_map = plan_join.join(SKELETON, t.drafts)
        second, second_map = plan_join.join(first, t.drafts)
        self.assertIn("- **Story 9** — quoted.", first)
        self.assertEqual(second, first)
        self.assertEqual(second_map, first_map)

    def test_a_marker_above_the_story_list_fails_instead_of_dropping_it(self):
        t = self.tree()
        text = "# Plan\n\n" + MARKER + "\n\n## Stories\n\n- **Story 1** — first.\n- **Story 2b** — second.\n"
        with self.assertRaisesRegex(JoinError, "## Stories section must sit above the marker line"):
            plan_join.join(text, t.drafts)

    def test_an_unreadable_drafts_directory_fails_naming_it(self):
        if os.geteuid() == 0:
            self.skipTest("root reads any directory")
        t = self.tree()
        t.drafts.chmod(0o000)
        self.addCleanup(t.drafts.chmod, 0o755)
        with self.assertRaisesRegex(JoinError, r"plan cannot be read: .*[Pp]ermission denied"):
            plan_join.join(SKELETON, t.drafts)

    def test_draft_that_is_not_utf8_fails_naming_the_file(self):
        t = self.tree()
        (t.drafts / "S1.md").write_bytes(b"## Story 1: \xff\xfe\n")
        with self.assertRaisesRegex(JoinError, "S1.md cannot be read"):
            plan_join.join(SKELETON, t.drafts)


class LoadMapTest(TreeCase):
    def test_the_drafts_directory_defaults_to_dot_flow_drafts_under_the_repo_root(self):
        t = self.tree()
        self.assertEqual(plan_join.drafts_dir(t.plan).resolve(), t.root.resolve() / ".flow" / "drafts" / "plan")

    def test_the_drafts_directory_follows_the_profiles_drafts_path(self):
        t = self.tree()
        t.profile.write_text('schema = 1\n[paths]\ndrafts = "notes/drafts"\n')
        self.assertEqual(plan_join.drafts_dir(t.plan).resolve(), t.root.resolve() / "notes" / "drafts" / "plan")

    def test_missing_map_is_empty(self):
        t = self.tree()
        self.assertEqual(plan_join.load_map(t.drafts), {})

    def test_malformed_map_is_a_join_error(self):
        t = self.tree()
        for bad in ["{not json", "{}", '{"stories": [{"id": "1"}]}', '{"stories": 3}']:
            (t.drafts / "join.json").write_text(bad)
            with self.assertRaisesRegex(JoinError, "join.json is not a join map; re-run loom flow join"):
                plan_join.load_map(t.drafts)

    def test_map_is_read_back_as_tuples(self):
        t = self.tree()
        payload = {"stories": [{"id": "1", "start": 40, "lines": 120}, {"id": "2b", "start": 161, "lines": 7}]}
        (t.drafts / "join.json").write_text(json.dumps(payload))
        self.assertEqual(plan_join.load_map(t.drafts), {"1": (40, 120), "2b": (161, 7)})


def run_cli(*args):
    return subprocess.run(
        [sys.executable, str(SCRIPT), *[str(arg) for arg in args]], capture_output=True, text=True
    )


class CliTest(TreeCase):
    def test_rewrites_the_plan_and_writes_the_map(self):
        t = self.tree()
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        total = len(EXPECTED.splitlines())
        self.assertEqual(result.stdout, f"joined 2 stories into {t.plan}: {total} lines\n")
        self.assertEqual(t.plan.read_text(), EXPECTED)
        written = {"stories": [{"id": "1", "start": 12, "lines": 5}, {"id": "2b", "start": 18, "lines": 3}]}
        self.assertEqual((t.drafts / "join.json").read_text(), json.dumps(written, indent=1) + "\n")
        self.assertEqual(plan_join.load_map(t.drafts), {"1": (12, 5), "2b": (18, 3)})

    def test_joining_twice_leaves_the_plan_and_the_map_unchanged(self):
        t = self.tree()
        self.assertEqual(run_cli(t.plan).returncode, 0)
        plan_once = t.plan.read_text()
        map_once = (t.drafts / "join.json").read_text()
        self.assertEqual(run_cli(t.plan).returncode, 0)
        self.assertEqual(t.plan.read_text(), plan_once)
        self.assertEqual((t.drafts / "join.json").read_text(), map_once)

    def test_a_join_error_prints_fail_and_touches_nothing(self):
        t = self.tree()
        (t.drafts / "S2b.md").unlink()
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(
            result.stdout,
            f"FAIL [join] {t.plan}: Story 2b is listed in the plan but S2b.md does not exist\n",
        )
        self.assertEqual(t.plan.read_text(), SKELETON)
        self.assertFalse((t.drafts / "join.json").exists())

    def test_a_profile_flowconf_refuses_fails_the_join_and_touches_nothing(self):
        t = self.tree()
        t.profile.write_text("schema = 2\n")
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertTrue(result.stdout.startswith(f"FAIL [join] {t.plan}: "), result.stdout)
        self.assertIn("schema: 2", result.stdout)
        self.assertEqual(t.plan.read_text(), SKELETON)
        self.assertFalse((t.drafts / "join.json").exists())

    def test_drafts_option_names_another_directory(self):
        t = self.tree(drafts={})
        other = t.root / "other"
        other.mkdir()
        (other / "S1.md").write_text(DRAFT_1)
        (other / "S2b.md").write_text(DRAFT_2)
        result = run_cli(t.plan, "--drafts", other)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(t.plan.read_text(), EXPECTED)
        self.assertTrue((other / "join.json").is_file())
        self.assertFalse((t.drafts / "join.json").exists())

    def test_no_arguments_is_a_usage_error(self):
        result = run_cli()
        self.assertEqual(result.returncode, 2)
        self.assertIn("usage", result.stderr)

    def test_a_non_utf8_draft_prints_fail_and_touches_nothing(self):
        t = self.tree()
        (t.drafts / "S1.md").write_bytes(b"## Story 1: \xff\xfe\n")
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertTrue(result.stdout.startswith(f"FAIL [join] {t.plan}: "))
        self.assertIn("S1.md cannot be read", result.stdout)
        self.assertEqual(t.plan.read_text(), SKELETON)
        self.assertFalse((t.drafts / "join.json").exists())

    def test_a_non_utf8_plan_prints_fail(self):
        t = self.tree()
        t.plan.write_bytes(b"# Plan \xff\n")
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertTrue(result.stdout.startswith(f"FAIL [join] {t.plan}: "))

    def test_a_failed_map_write_leaves_the_plan_untouched(self):
        t = self.tree()
        (t.drafts / "join.json").mkdir()
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertTrue(result.stdout.startswith(f"FAIL [join] {t.plan}: "))
        self.assertEqual(t.plan.read_text(), SKELETON)

    def test_unreadable_plan_fails(self):
        t = self.tree()
        result = run_cli(t.root / "plans" / "missing.md")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertTrue(result.stdout.startswith("FAIL [join] "))
        self.assertTrue(result.stdout.endswith("No such file or directory\n"))


if __name__ == "__main__":
    unittest.main()
