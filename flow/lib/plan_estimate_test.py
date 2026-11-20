"""Tests for plan_estimate.py. Run from lib/: python3 -m unittest -q plan_estimate_test.py"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import flowconf  # noqa: E402
import plan_skeleton  # noqa: E402

SCRIPT = Path(__file__).with_name("plan_estimate.py")
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
MARKER = "<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->"
HEADER = ("| Agent | Type | Model | Effort | Requests | Input | Output | Cache read | Write 5m | Write 1h | Over 100K "
          "| USD | Est. thinking | USD incl. est. |\n|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")


def skeleton(*forms):
    """A skeleton whose stories 1..n have these Form values (None: no Form line)."""
    lines = ["# Plan", "", "**Spec:** `docs/spec.md`", "", "## Names contract", "", "none", "", "## Stories", ""]
    for n, form in enumerate(forms, 1):
        lines += [f"- **Story {n}** — story {n}.", "  - **Developer:** intermediate-dev", f"  - **Files:** `f{n}.py`",
                  "  - **Interfaces:** none", "  - **Verify:** `python3 -m unittest f_test.py`"]
        if form is not None:
            lines.append(f"  - **Form:** {form}")
        lines.append("  - **Reading:** spec §1")
    return "\n".join(lines + ["", MARKER]) + "\n"


def record(draft, run, forms, skeleton_usd=None, review_usd=None, forms_draft=None, risk_usd=None):
    """An engine run record: drafting and run USD, its stories' forms, and the Design stage line's segments.

    review_usd is a draft's `plan review $` from before 2026-11-20 (the plan and risk reviewers), risk_usd
    its `risk review $` since (the risk reviewer alone).
    """
    stage = f"/flow:draft session s, a to b UTC: ${draft:.2f} logged, ${draft:.2f} with estimated thinking; 1,000 plan lines, $1.00 per 1,000 lines"
    if skeleton_usd is not None:
        stage += f"; skeleton review ${skeleton_usd:.2f}"
    if review_usd is not None:
        stage += f"; plan review ${review_usd:.2f}"
    if risk_usd is not None:
        stage += f"; risk review ${risk_usd:.2f}"
    if forms_draft:
        stage += "; forms: " + ", ".join(f"{form} {n} {'story' if n == 1 else 'stories'} ${usd:.2f}" for form, (n, usd) in forms_draft.items())
    stage += "; loom flow roster aaaaaaaaaaaa."
    rows = "".join(f"| {n} | intermediate-dev | deep | full | {form} | 1 |\n" for n, form in enumerate(forms, 1))
    comment = json.dumps({"by_type": {}, "total": {"usd": run}})
    return (f"# Run: r\n\n- Plan: p.md ({len(forms)} stories)\n- Design stage: {stage}\n- Ended: done\n"
            f"- Engine: loom flow run (flow in loom part 3b)\n\n## Cost\n\n### Cost by agent\n\n{HEADER}\n"
            f"<!-- agent-costs {comment} -->\n\n## Stories\n\n| Story | Developer | Tier | Review | Form | Commits |\n"
            f"|---|---|---|---|---|---|\n{rows}\n## Notes\n")


class Repo:
    def __init__(self, root, plan_text, profile="schema = 1\n"):
        self.root = Path(root)
        subprocess.run(["git", "-C", root, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
        if profile is not None:
            (self.root / ".claude").mkdir()
            (self.root / ".claude" / "flow.toml").write_text(profile)
        (self.root / "docs").mkdir()
        (self.root / "docs" / "spec.md").write_text("# Spec\n")
        self.plan = self.root / "plans" / "plan.md"
        self.plan.parent.mkdir()
        self.plan.write_text(plan_text)
        self.data = self.root / "flow-data"
        self.env = {**CLEAN_ENV, "FLOW_DATA": str(self.data), "CLAUDE_CONFIG_DIR": str(self.root / "claude"),
                    "FLOW_HOME": str(self.root / "flow-home")}

    def records(self, key, *texts):
        directory = self.data / "records" / key
        directory.mkdir(parents=True, exist_ok=True)
        for k, text in enumerate(texts, len(list(directory.glob("*.md")))):
            (directory / f"run-{k}.md").write_text(text)

    @property
    def key(self):
        return flowconf.repo_key(self.root)

    def run(self):
        return subprocess.run([sys.executable, str(SCRIPT), "--date", "2026-11-16", str(self.plan)],
                              capture_output=True, text=True, env=self.env, cwd=self.root)


class EstimateTest(unittest.TestCase):
    def repo(self, plan_text, profile="schema = 1\n"):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        return Repo(tmp.name, plan_text, profile)

    def test_full_figures_come_from_the_records(self):
        r = self.repo(skeleton(None, "full"))
        r.records(r.key, *(record(30.0, 15.0, ["full"] * 3) for _ in range(3)))
        result = r.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        out = result.stdout
        self.assertIn("3 engine runs of this repo", out)
        self.assertIn("| 1 | full | $10.00 | $5.00 | $3.00 / $3.50 | $2.50 / $6.50 |", out)
        # Stories $20, the skeleton review's prior $5.14 and the plan review's prior $0.78 for each of 2 full stories.
        self.assertIn("Draft: $26.70 (stories $20.00, skeleton review $5.14, plan review $1.56 for 2 full stories)", out)
        self.assertIn("Run: $10.00", out)
        self.assertIn("skeleton review prior; plan review prior", out)
        self.assertIn("**Budget:** draft $27; run $10 (estimate 2026-11-16)", out)

    def test_the_review_figures_come_from_the_records_once_three_hold_them(self):
        r = self.repo(skeleton(None))
        r.records(r.key, *(record(36.0, 15.0, ["full"] * 3, skeleton_usd=3.0, risk_usd=3.0) for _ in range(3)))
        out = r.run().stdout
        # Drafting less both reviews is $30 over 3 stories; the risk review is $1 a full story.
        self.assertIn("| 1 | full | $10.00 | $5.00 |", out)
        self.assertIn("Draft: $14.00 (stories $10.00, skeleton review $3.00, plan review $1.00 for 1 full story)", out)
        self.assertIn("skeleton review from 3 runs; risk review from 3 runs", out)

    def test_the_plan_review_is_measured_only_from_the_risk_reviewers_records(self):
        r = self.repo(skeleton(None))
        # Three drafts from before 2026-11-20: their plan review also priced flow:plan-reviewer.
        r.records(r.key, *(record(36.0, 15.0, ["full"] * 3, skeleton_usd=3.0, review_usd=3.0) for _ in range(3)))
        out = r.run().stdout
        # Their review still comes off their drafting ($30 over 3 stories), but prices nothing: the prior does.
        self.assertIn("| 1 | full | $10.00 | $5.00 |", out)
        self.assertIn("Draft: $13.78 (stories $10.00, skeleton review $3.00, plan review $0.78 for 1 full story)", out)
        self.assertIn("skeleton review from 3 runs; plan review prior", out)
        # Two of the risk reviewer's alone are fewer than min_runs.
        r.records(r.key, *(record(34.5, 15.0, ["full"] * 3, skeleton_usd=3.0, risk_usd=1.5) for _ in range(2)))
        out = r.run().stdout
        self.assertIn("Draft: $13.78 (stories $10.00, skeleton review $3.00, plan review $0.78 for 1 full story)", out)
        self.assertIn("skeleton review from 5 runs; plan review prior", out)
        # A third: $4.50 over their 9 full stories, the older records left out.
        r.records(r.key, record(34.5, 15.0, ["full"] * 3, skeleton_usd=3.0, risk_usd=1.5))
        out = r.run().stdout
        self.assertIn("Draft: $13.50 (stories $10.00, skeleton review $3.00, plan review $0.50 for 1 full story)", out)
        self.assertIn("skeleton review from 6 runs; risk review from 3 runs", out)

    def test_a_form_uses_its_prior_until_three_stories_ran(self):
        r = self.repo(skeleton("mechanical"))
        two = record(30.0, 30.0, ["full", "mechanical", "mechanical"],
                     forms_draft={"full": (1, 10.0), "mechanical": (2, 4.0)})
        r.records(r.key, two, record(30.0, 15.0, ["full"] * 3), record(30.0, 15.0, ["full"] * 3))
        out = r.run().stdout
        self.assertIn("mechanical prior", out)
        # Full: draft (10 + 30 + 30) / 7 = $10.00, run (10 + 15 + 15) / 7.
        self.assertIn("| 1 | mechanical | $3.00 |", out)
        r.records(r.key, record(30.0, 30.0, ["full", "mechanical"], forms_draft={"full": (1, 26.0), "mechanical": (1, 4.0)}))
        out = r.run().stdout
        self.assertIn("mechanical from 3 stories", out)
        # Mechanical: draft (4 + 4) / 3 stories, run (10 + 10 + 15) / 3.
        self.assertIn("| 1 | mechanical | $2.67 | $11.67 |", out)

    def test_the_repos_records_win_from_three_runs(self):
        r = self.repo(skeleton(None))
        r.records("-other-repo", *(record(60.0, 30.0, ["full"] * 3) for _ in range(3)))
        r.records(r.key, *(record(30.0, 15.0, ["full"] * 3) for _ in range(2)))
        out = r.run().stdout
        self.assertIn("5 engine runs, pooled", out)
        self.assertIn("| 1 | full | $16.00 | $8.00 |", out)
        r.records(r.key, record(30.0, 15.0, ["full"] * 3))
        out = r.run().stdout
        self.assertIn("3 engine runs of this repo", out)
        self.assertIn("| 1 | full | $10.00 | $5.00 |", out)

    def test_no_records_prices_from_the_priors(self):
        r = self.repo(skeleton(None, "brief"))
        result = r.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        out = result.stdout
        self.assertIn("no engine runs: every figure is a prior", out)
        self.assertIn("| 1 | full | $5.49 | $4.50 |", out)
        self.assertIn("| 2 | brief | $1.37 | $5.85 |", out)

    def test_a_full_story_shows_what_the_other_forms_cost_and_the_others_dont(self):
        r = self.repo(skeleton(None, "mechanical"))
        out = r.run().stdout
        self.assertIn("| 1 | full | $5.49 | $4.50 | $1.65 / $3.15 | $1.37 / $5.85 |", out)
        self.assertIn("| 2 | mechanical | $1.65 | $3.15 | | |", out)

    def test_the_budget_line_passes_the_skeleton_check(self):
        r = self.repo(skeleton(None, "mechanical"))
        line = next(l for l in r.run().stdout.splitlines() if l.startswith("**Budget:**"))
        text = r.plan.read_text().replace("**Spec:** `docs/spec.md`\n", f"**Spec:** `docs/spec.md`\n{line}\n", 1)
        problems, _, _ = plan_skeleton.lint(text, r.plan)
        self.assertEqual(problems, [])

    def test_the_design_sessions_spend_is_shown_as_spent(self):
        out = self.repo(skeleton(None)).run().stdout
        self.assertIn("Design so far: $0.00, spent, not budgeted", out)

    def test_no_profile_fails(self):
        result = self.repo(skeleton(None), profile=None).run()
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("no flow profile", result.stderr)

    def test_a_bad_form_fails(self):
        result = self.repo(skeleton("partial")).run()
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("Story 1's **Form:** must be full, mechanical or brief", result.stderr)


if __name__ == "__main__":
    unittest.main()
