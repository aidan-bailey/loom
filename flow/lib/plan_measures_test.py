"""Tests for plan_measures.py. Run from lib/: python3 -m unittest -q plan_measures_test.py"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plan_measures  # noqa: E402

HEADER = ("| Agent | Type | Model | Effort | Requests | Input | Output | Cache read | Write 5m | Write 1h | Over 100K "
          "| USD | Est. thinking | USD incl. est. |\n|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")


def agent(name, usd):
    return f"| {name} | intermediate-dev | claude-sonnet-5-5 | high | 1 | 1 | 1 | 1 | 0 | 0 | 0 | ${usd} | 0 | ${usd + 1} |\n"


STAGE = ("- Design stage: /flow:draft session 7e7a8520-e18b-42d8-9b20-6eb9f39a2788, 2026-11-12T07:57:50Z to "
         "2026-11-12T09:07:19Z UTC: $20.00 logged, $20.50 with estimated thinking; 10,000 plan lines, $2.00 per "
         "1,000 lines; plan review $6.00; loom flow roster aaaaaaaaaaaa.\n")
RECORD = (
    "# Run: r\n\n- Plan: p.md (3 stories)\n- Definitions: loom flow roster bbbbbbbbbbbb → loom flow roster cccccccccccc\n"
    + STAGE
    + "- Ended: done\n- Engine: loom flow run (flow in loom part 3b)\n\n## Cost\n\n### Cost by agent\n\n" + HEADER
    + agent("r-s1-dev1", 2) + agent("r-s1-review1", 1) + agent("r-s1-fix1", 3) + agent("r-s1-review2", 4)
    + agent("r-s2-fix1", 5) + agent("r-s3-fix1", 7)
    + "\n## Session 2\n\n### Cost by agent\n\n" + HEADER + agent("r-s3-review2", 8)
    + "\n## Stories\n\n| Story | Developer | Tier | Review | Commits |\n|---|---|---|---|---|\n"
    "| 1 | intermediate-dev | deep | vouched | 2 |\n| 2 | junior-dev | light | full | 1 |\n| 3 | intermediate-dev | deep | full | 4 |\n"
    "\n## Notes\n\n"
    "- Asked (unresolved-findings, Story 1): Reviews of Story 1 found problems in the plan's own code: - a\n"
    "- Asked (design-finding, Story 2): A review of Story 2 found a design-level issue\n"
    "- Asked (unresolved-findings, Story 3): Reviews of Story 3 found problems in the plan's own code: - b - c\n"
    "- Asked (unresolved-findings, Story 3): After a fix round, a re-review of Story 3 still finds Critical issues\n"
)


FORMED = (
    RECORD.replace("; plan review $6.00; loom flow roster", "; plan review $6.00; budget $25; forms: full 2 stories $9.00, mechanical 1 story $3.00; loom flow roster")
    .replace("- Ended: done\n", "- Budget: draft $25; run $50 (estimate 2026-11-12)\n- Ended: done\n")
    .replace("| Story | Developer | Tier | Review | Commits |\n|---|---|---|---|---|\n",
             "| Story | Developer | Tier | Review | Form | Commits |\n|---|---|---|---|---|---|\n")
    .replace("| 1 | intermediate-dev | deep | vouched | 2 |", "| 1 | intermediate-dev | deep | vouched | full | 2 |")
    .replace("| 2 | junior-dev | light | full | 1 |", "| 2 | junior-dev | light | full | mechanical | 1 |")
    .replace("| 3 | intermediate-dev | deep | full | 4 |", "| 3 | intermediate-dev | deep | full | full | 4 |")
)


class FormMeasureTest(unittest.TestCase):
    def test_a_record_gives_its_budget_and_forms(self):
        m = plan_measures.measure("r", FORMED, 40.0)
        self.assertEqual((m.budget_draft, m.budget_run), (25.0, 50.0))
        self.assertEqual(m.forms_draft, {"full": (2, 9.0), "mechanical": (1, 3.0)})
        # Rows by story: s1 2+1+3+4 = 10, s2 5, s3 7+8 = 15; the unowned 40 - 30 = 10 spreads 10/3 a story.
        share = 10 / 3
        self.assertEqual(set(m.forms_run), {"full", "mechanical"})
        self.assertEqual(m.forms_run["full"][0], 2)
        self.assertAlmostEqual(m.forms_run["full"][1], 10 + 15 + 2 * share)
        self.assertEqual(m.forms_run["mechanical"][0], 1)
        self.assertAlmostEqual(m.forms_run["mechanical"][1], 5 + share)

    def test_a_record_without_forms_counts_every_story_full(self):
        m = plan_measures.measure("r", RECORD, 40.0)
        self.assertEqual((m.budget_draft, m.budget_run), (None, None))
        self.assertEqual(m.forms_draft, {})
        self.assertEqual(m.forms_run["full"][0], 3)
        self.assertAlmostEqual(m.forms_run["full"][1], 40.0, msg="every row, owned or spread, lands on a full story")

    def test_unowned_run_costs_spread_evenly(self):
        text = RECORD.replace(agent("r-s2-fix1", 5), agent("r-s2-fix1", 5) + agent("manager abc", 6) + agent("r-premise1", 1))
        m = plan_measures.measure("r", text, 37.0)
        self.assertAlmostEqual(m.forms_run["full"][1], 37.0)


class MeasureTest(unittest.TestCase):
    def test_an_engine_record_gives_each_figure(self):
        m = plan_measures.measure("r", RECORD, 40.0)
        self.assertEqual(m.draft_copy, "aaaaaaaaaaaa")
        self.assertEqual(m.run_copy, "cccccccccccc", "the last copy of a run that changed copies")
        self.assertEqual(m.plan_lines, 10000)
        self.assertEqual(m.questions, 2, "only the plan's-own-code questions")
        self.assertEqual(m.draft_usd, 20.0)
        self.assertEqual(m.run_usd, 40.0)
        # Story 1: fix1 3 + review2 4; Story 3: fix1 7 + review2 8 (the second session's table); never Story 2's.
        self.assertEqual(m.fix_usd, 22.0)
        self.assertEqual((m.stories, m.vouched), (3, 1))
        self.assertEqual(m.review_usd, 6.0)

    def test_the_skeleton_reviews_cost_is_read_apart_from_the_plan_reviews(self):
        text = RECORD.replace("; plan review $6.00;", "; skeleton review $1.50; plan review $6.00;")
        m = plan_measures.measure("r", text, 40.0)
        self.assertEqual(m.skeleton_usd, 1.5)
        self.assertEqual(m.review_usd, 6.0)

    def test_a_design_stage_line_without_the_clauses_gives_none(self):
        text = RECORD.replace("; plan review $6.00; loom flow roster aaaaaaaaaaaa.", ".")
        m = plan_measures.measure("r", text, 40.0)
        self.assertIsNone(m.review_usd)
        self.assertIsNone(m.skeleton_usd)
        self.assertIsNone(m.draft_copy)
        self.assertEqual(m.plan_lines, 10000)

    def test_a_record_without_a_design_stage_line_has_no_plan_lines(self):
        m = plan_measures.measure("r", RECORD.replace(STAGE, ""), 40.0)
        self.assertIsNone(m.plan_lines)
        self.assertIsNone(m.draft_usd)

    def test_a_record_the_engine_didnt_write_gives_nothing(self):
        self.assertIsNone(plan_measures.measure("r", RECORD.replace("- Engine: loom flow run", "- Engine: other"), 1.0))


class TableTest(unittest.TestCase):
    def test_a_row_per_run_and_a_pooled_row(self):
        a = plan_measures.measure("a", RECORD, 40.0)
        b = plan_measures.measure("b", RECORD.replace(STAGE, ""), 10.0)
        out = plan_measures.table([a, b])
        self.assertIn("## Plan code", out)
        self.assertIn("| a | aaaaaaaaaaaa | cccccccccccc | 10,000 | 2 | 0.20 | $20.00 | $40.00 | $6.00 | $22.00 | 3 | 1 | $6.00 | $2.00 | n/a |", out)
        self.assertIn("| b | n/a | cccccccccccc | n/a | 2 | n/a | n/a | $10.00 | n/a | $22.00 | 3 | 1 | n/a | n/a | n/a |", out)
        # Pooled: the per-1,000 figures only over a (the run with plan lines); questions and dollars over both.
        self.assertIn("| Pooled (2 runs) | | | 10,000 | 4 | 0.20 | $20.00 | $50.00 | $6.00 | $44.00 | 6 | 2 | $6.00 | $2.00 | n/a |", out)

    def test_the_pooled_skeleton_review_is_the_average_a_plan_over_the_runs_with_it(self):
        def skeleton(usd):
            return RECORD.replace("; plan review $6.00;", f"; skeleton review ${usd}; plan review $6.00;")
        runs = [plan_measures.measure("a", skeleton("1.50"), 40.0), plan_measures.measure("b", RECORD, 10.0),
                plan_measures.measure("c", skeleton("2.50"), 30.0)]
        out = plan_measures.table(runs)
        self.assertIn("| Skeleton review |", out)
        self.assertTrue(any(line.startswith("| a |") and line.endswith("| $1.50 |") for line in out.splitlines()))
        self.assertTrue(any(line.startswith("| b |") and line.endswith("| n/a |") for line in out.splitlines()))
        pooled = next(line for line in out.splitlines() if line.startswith("| Pooled"))
        self.assertTrue(pooled.endswith("| $2.00 a plan |"), pooled)


if __name__ == "__main__":
    unittest.main()
