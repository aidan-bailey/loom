"""Tests for check_plan.py. Run from lib/: python3 -m unittest -q check_plan_test.py"""

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_plan  # noqa: E402

SCRIPT = Path(__file__).with_name("check_plan.py")
# A git hook's GIT_* variables would point the temp repos at the outer repository.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
# A Rust and TypeScript workspace's landmine markers, as realistic fixtures.
MARKERS = (
    "#[tokio::test", "cfg(not(feature", "user_id", "#[tauri::command", "specta", "MigrationTrait",
    ".route(", "to_f64(", "from_f64(", "Cap::", "SessionKey", "WebFetch", "reqwest", "connect_async",
)
PROFILE = (
    "schema = 1\n[build]\nexclusive = [\"cargo\"]\n[landmines]\nmarkers = ["
    + ", ".join(f'"{marker}"' for marker in MARKERS)
    + "]\n"
)

GOOD = textwrap.dedent(
    """\
    # Example plan

    **Spec:** `docs/spec.md`, approved.

    ## Story 1: Store

    - **Developer:** junior-dev
    - **Files:** `a/store.rs`, `a/tests/store.rs`
    - **Interfaces:** exposes `Store::get`
    - **Verify:** `cargo nextest run -p a store`

    ### Task 1.1: Write the store

    ```rust
    ## Story 9: inside a fence, so not a story
    ```

    ## Story 2: UI

    - **Developer:** `intermediate-dev`
    - **Files:** `ui/panel.ts`
    - **Interfaces:** uses `Store::get` from Story 1
    - **Verify:** `pnpm test panel`

    ### Task 2.1: Panel

    ## Story 3: Route

    - **Developer:** intermediate-dev
    - **Files:** `a/route.rs`, `a/store.rs`
    - **Interfaces:** none
    - **Verify:** `cargo nextest run -p a route`

    ### Task 3.1: Route
    """
)


def run(text: str, spec: bool = True, profile: str | None = PROFILE, as_json: bool = False) -> tuple[int, str]:
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(["git", "-C", tmp, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
        if profile is not None:
            Path(tmp, ".claude").mkdir()
            Path(tmp, ".claude", "flow.toml").write_text(profile)
        if spec:
            Path(tmp, "docs").mkdir()
            Path(tmp, "docs", "spec.md").write_text("# Spec\n")
        plan = Path(tmp, "plans", "plan.md")
        plan.parent.mkdir()
        plan.write_text(text)
        # No test reads the real home store: FLOW_HOME points at a directory with no profiles.
        env = {**CLEAN_ENV, "FLOW_HOME": str(Path(tmp, "flow-home"))}
        result = subprocess.run(
            [sys.executable, str(SCRIPT), *(["--json"] if as_json else []), str(plan)],
            capture_output=True, text=True, env=env, cwd=tmp
        )
        return result.returncode, result.stdout


class CheckPlanTest(unittest.TestCase):
    def test_good_plan_passes_and_lists_parallel_safe_pairs(self):
        code, out = run(GOOD)
        self.assertEqual(code, 0, out)
        self.assertIn("3 stories; spec docs/spec.md", out)
        # 1 and 3 share a/store.rs; 1+2 and 2+3 are disjoint with one exclusive build each.
        self.assertIn("at most one exclusive build): 1+2, 2+3", out)

    def test_missing_field_fails(self):
        code, out = run(GOOD.replace("- **Interfaces:** none\n", ""))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 3", out)
        self.assertIn("missing **Interfaces:**", out)

    def test_unknown_developer_fails(self):
        code, out = run(GOOD.replace("**Developer:** junior-dev", "**Developer:** staff-dev"))
        self.assertEqual(code, 1, out)
        self.assertIn("'staff-dev'", out)

    def test_a_developer_named_as_its_plugin_agent_passes(self):
        code, out = run(GOOD.replace("**Developer:** junior-dev", "**Developer:** flow:junior-dev"))
        self.assertEqual(code, 0, out)
        code, out = run(GOOD.replace("**Developer:** junior-dev", "**Developer:** flow:staff-dev"))
        self.assertEqual(code, 1, out)
        self.assertIn("'staff-dev'", out)

    def test_senior_dev_passes_with_a_warning(self):
        code, out = run(GOOD.replace("**Developer:** junior-dev", "**Developer:** senior-dev"))
        self.assertEqual(code, 0, out)
        self.assertIn("WARN Story 1: names senior-dev, the last resort", out)

    def test_task_outside_any_story_fails(self):
        code, out = run("### Task 0: orphan\n\n" + GOOD)
        self.assertEqual(code, 1, out)
        self.assertIn("outside any story", out)

    def test_plan_without_stories_fails(self):
        code, out = run("# Plan\n\n**Spec:** `docs/spec.md`\n\n### Task 1: one\n")
        self.assertEqual(code, 1, out)
        self.assertIn('no "## Story" headings', out)

    def test_story_without_tasks_fails(self):
        code, out = run(GOOD.replace("### Task 2.1: Panel\n", ""))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 2", out)
        self.assertIn("no ### task headings", out)

    def test_missing_spec_line_fails(self):
        code, out = run(GOOD.replace("**Spec:** `docs/spec.md`, approved.\n", ""))
        self.assertEqual(code, 1, out)
        self.assertIn("no **Spec:** line", out)

    def test_spec_that_does_not_exist_fails(self):
        code, out = run(GOOD, spec=False)
        self.assertEqual(code, 1, out)
        self.assertIn("docs/spec.md does not exist", out)

    def test_design_line_counts_as_the_spec(self):
        code, out = run(GOOD.replace("**Spec:**", "**Design:**"))
        self.assertEqual(code, 0, out)

    def test_paths_are_compared_after_normalising(self):
        # Story 2 names Story 1's file as ./a/store.rs: the pair must not be parallel-safe.
        code, out = run(GOOD.replace("`ui/panel.ts`", "`./a/store.rs`"))
        self.assertEqual(code, 0, out)
        self.assertNotIn("1+2", out)

    def test_reports_the_plan_review_line(self):
        _, out = run(GOOD)
        self.assertIn("Plan review: none recorded", out)
        reviewed = GOOD.replace("approved.\n", "approved.\n\n**Plan review:** 2026-10-14, code-quality-reviewer and recon, every story\n")
        code, out = run(reviewed)
        self.assertEqual(code, 0, out)
        self.assertIn("Plan review: 2026-10-14, code-quality-reviewer and recon, every story", out)

    def test_two_building_stories_never_pair(self):
        code, out = run(GOOD.replace("`pnpm test panel`", "`cargo test -p ui`"))
        self.assertEqual(code, 0, out)
        self.assertIn("at most one exclusive build): none", out)

    def test_exclusive_builds_come_from_the_profile(self):
        both = PROFILE.replace('exclusive = ["cargo"]', 'exclusive = ["cargo", "pnpm"]')
        code, out = run(GOOD, profile=both)
        self.assertEqual(code, 0, out)
        self.assertIn("at most one exclusive build): none", out)
        code, out = run(GOOD, profile="schema = 1\n")
        self.assertEqual(code, 0, out)
        self.assertIn("at most one exclusive build): 1+2, 2+3", out)
        self.assertRegex(out, r"Story 1\s+junior-dev\s+2 files\s+no build")

    def test_a_plan_outside_any_repo_finds_the_profile_of_the_repo_it_runs_in(self):
        # The plan has no repo ancestor, so the repo is the one holding the working directory,
        # found from a subdirectory as well as from its top.
        with tempfile.TemporaryDirectory() as repo, tempfile.TemporaryDirectory() as elsewhere:
            subprocess.run(["git", "-C", repo, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
            Path(repo, ".claude").mkdir()
            Path(repo, ".claude", "flow.toml").write_text(PROFILE)
            Path(repo, "docs").mkdir()
            Path(repo, "docs", "spec.md").write_text("# Spec\n")
            Path(repo, "sub").mkdir()
            plan = Path(elsewhere, "plan.md")
            plan.write_text(GOOD)
            env = {**CLEAN_ENV, "FLOW_HOME": str(Path(elsewhere, "flow-home"))}
            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(plan)], capture_output=True, text=True, env=env,
                cwd=Path(repo, "sub"),
            )
            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertIn("3 stories; spec docs/spec.md", result.stdout)

    def test_a_plan_in_another_repo_than_the_working_directory_fails(self):
        # One profile per session: the plan's repo and the cwd's repo must share a main checkout.
        with tempfile.TemporaryDirectory() as plan_repo, tempfile.TemporaryDirectory() as cwd_repo:
            for repo in (plan_repo, cwd_repo):
                subprocess.run(["git", "-C", repo, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
            Path(plan_repo, ".claude").mkdir()
            Path(plan_repo, ".claude", "flow.toml").write_text(PROFILE)
            Path(plan_repo, "docs").mkdir()
            Path(plan_repo, "docs", "spec.md").write_text("# Spec\n")
            plan = Path(plan_repo, "plan.md")
            plan.write_text(GOOD)
            env = {**CLEAN_ENV, "FLOW_HOME": str(Path(cwd_repo, "flow-home"))}
            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(plan)], capture_output=True, text=True, env=env, cwd=cwd_repo
            )
            self.assertEqual(result.returncode, 1, result.stdout)
            self.assertIn("FAIL [repo]", result.stdout)
            self.assertIn(f"FAIL [repo] {plan}: the plan is in {os.path.realpath(plan_repo)} but this session is in {os.path.realpath(cwd_repo)}", result.stdout)
            self.assertIn("keep the plan in the repo it changes", result.stdout)

    def test_a_plan_whose_repo_has_no_profile_fails(self):
        code, out = run(GOOD, profile=None)
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [profile]", out)
        self.assertIn("no flow profile", out)


FENCE = "`" * 3


def tiered(*stories, review=None):
    """A plan of (id, Review value or None, code) stories, optionally with a Plan review line."""
    lines = ["# Tiered plan", "", "**Spec:** `docs/spec.md`", ""]
    if review is not None:
        lines += [f"**Plan review:** {review}", ""]
    for sid, value, code in stories:
        lines += [
            f"## Story {sid}: Title {sid}",
            "",
            "- **Developer:** junior-dev",
            f"- **Files:** `f{sid}.py`",
            "- **Interfaces:** none",
            f"- **Verify:** `python3 -m unittest -q f{sid}_test.py`",
        ]
        if value is not None:
            lines.append(f"- **Review:** {value}")
        lines += ["", f"### Task {sid}.1: Task", "", FENCE + "rust", *code.split("\n"), FENCE, ""]
    return "\n".join(lines)


def line_of(text, needle):
    """The 1-based line of the first line of `text` that is exactly `needle`."""
    return text.split("\n").index(needle) + 1


class JsonTest(unittest.TestCase):
    """`--json`, which loom flow run reads (flow in loom part 3b)."""

    def test_gives_each_story_its_lines_and_fields(self):
        code, out = run(GOOD.replace("- **Files:** `a/route.rs`, `a/store.rs`", "- **Files:** `a/route.rs`, `a/store.rs`\n- **Review:** light (one route)"), as_json=True)
        self.assertEqual(code, 0, out)
        got = json.loads(out)
        self.assertEqual(got["spec"], "docs/spec.md")
        self.assertIsNone(got["plan_review"])
        stories = got["stories"]
        self.assertEqual([s["id"] for s in stories], ["1", "2", "3"])
        lines = GOOD.splitlines()
        self.assertEqual(stories[0]["line"], lines.index("## Story 1: Store") + 1)
        self.assertEqual(stories[0]["end"], lines.index("## Story 2: UI"), "the line before the next story")
        self.assertEqual(stories[2]["end"], len(lines) + 1, "the plan's last line")
        self.assertEqual(stories[1]["developer"], "intermediate-dev", "backticks and flow: trimmed")
        self.assertEqual(stories[2]["tier"], "light")
        self.assertEqual(stories[0]["review"], "full", "no Plan review line vouches for nothing")
        self.assertEqual(stories[0]["verify"], "`cargo nextest run -p a store`")
        self.assertEqual(stories[1]["interfaces"], "uses `Store::get` from Story 1")
        self.assertEqual(stories[0]["landmines"], [])

    def test_a_story_ends_before_a_later_h2(self):
        code, out = run(GOOD + "\n## Execution notes\n\nlater\n", as_json=True)
        self.assertEqual(code, 0, out)
        lines = (GOOD + "\n## Execution notes\n\nlater\n").splitlines()
        self.assertEqual(json.loads(out)["stories"][2]["end"], lines.index("## Execution notes"))

    def test_a_failing_plan_prints_its_fail_lines(self):
        code, out = run(GOOD.replace("- **Interfaces:** none\n", ""), as_json=True)
        self.assertEqual(code, 1, out)
        self.assertTrue(out.startswith("FAIL "), out)

    def test_json_takes_one_plan(self):
        result = subprocess.run([sys.executable, str(SCRIPT), "--json"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)


class ReviewLineTest(unittest.TestCase):
    def test_parse_reads_tier_and_reason(self):
        text = tiered(("1", "deep", "x"), ("2", "light (docs only)", "x"), ("3", "light", "x"))
        stories, problems, _ = check_plan.parse(text)
        self.assertEqual(problems, [])
        self.assertEqual([s.tier for s in stories], ["deep", "light", "light"])
        self.assertEqual([s.reason for s in stories], ["", "docs only", ""])

    def test_missing_review_line_means_deep(self):
        stories, _, _ = check_plan.parse(tiered(("1", None, "x")))
        self.assertNotIn("Review", stories[0].fields)
        self.assertEqual(stories[0].tier, "deep")
        self.assertEqual(stories[0].reason, "")
        code, out = run(tiered(("1", None, "user_id")))
        self.assertEqual(code, 0, out)

    def test_field_re_reads_the_required_and_optional_fields_only(self):
        for line in ("- **Review:** light", "- **Developer:** x"):
            self.assertIsNotNone(check_plan.FIELD_RE.match(line), line)
        self.assertIsNone(check_plan.FIELD_RE.match("- **Owner:** x"))

    def test_unparsable_review_value_fails(self):
        for value in ("maybe", "deep (unclosed", "", "deep light"):
            text = tiered(("1", value, "x"))
            code, out = run(text)
            self.assertEqual(code, 1, f"{value!r}: {out}")
            line = line_of(text, "## Story 1: Title 1")
            self.assertIn(
                f"Story 1 (line {line}): **Review:** must be deep or light, optionally with a reason in parentheses",
                out,
            )

    def test_a_nested_parenthesis_stays_in_the_reason(self):
        stories, problems, _ = check_plan.parse(tiered(("1", "light (renames only (no logic))", "x")))
        self.assertEqual(problems, [])
        self.assertEqual((stories[0].tier, stories[0].reason), ("light", "renames only (no logic)"))

    def test_a_review_line_after_the_first_task_is_ignored(self):
        text = tiered(("1", None, "x")).replace("### Task 1.1: Task", "### Task 1.1: Task\n\n- **Review:** light")
        stories, problems, _ = check_plan.parse(text)
        self.assertEqual(problems, [])
        self.assertNotIn("Review", stories[0].fields)
        self.assertEqual(stories[0].tier, "deep")

    def test_unparsable_review_value_counts_as_deep(self):
        stories, _, _ = check_plan.parse(tiered(("1", "maybe", "x")))
        self.assertEqual(stories[0].tier, "deep")
        self.assertEqual(stories[0].reason, "")


class MarkerFloorTest(unittest.TestCase):
    def test_parse_still_returns_the_triple_and_collects_fenced_lines(self):
        result = check_plan.parse(GOOD)
        self.assertEqual(len(result), 3)
        stories, problems, spec = result
        self.assertEqual(problems, [])
        self.assertEqual(spec, "docs/spec.md")
        # Story 1's fence holds one line, a fake heading; the fence lines themselves are not code.
        self.assertEqual(stories[0].code, [(15, "## Story 9: inside a fence, so not a story")])
        self.assertEqual(stories[1].code, [])

    def test_fenced_code_under_a_later_non_story_section_belongs_to_no_story(self):
        text = tiered(("1", None, "alpha")) + "\n## Appendix\n\n" + FENCE + "rust\nuser_id\n" + FENCE + "\n"
        stories, _, _ = check_plan.parse(text)
        self.assertEqual([code for _, code in stories[0].code], ["alpha"])

    def test_code_is_collected_per_story_with_plan_lines(self):
        text = tiered(("1", None, "alpha\nbeta"), ("2", "light", "gamma"))
        stories, _, _ = check_plan.parse(text)
        self.assertEqual(stories[0].code, [(line_of(text, "alpha"), "alpha"), (line_of(text, "beta"), "beta")])
        self.assertEqual(stories[1].code, [(line_of(text, "gamma"), "gamma")])

    def test_markers_in_gives_the_first_line_of_each_marker_in_marker_order(self):
        text = tiered(("1", "light", "reqwest::get(url)\nuser_id one\nuser_id two\n#[tokio::test]"))
        story = check_plan.parse(text)[0][0]
        self.assertEqual(
            check_plan.markers_in(story, MARKERS),
            [
                ("#[tokio::test", line_of(text, "#[tokio::test]")),
                ("user_id", line_of(text, "user_id one")),
                ("reqwest", line_of(text, "reqwest::get(url)")),
            ],
        )
        quiet = check_plan.parse(tiered(("1", "light", "nothing here")))[0][0]
        self.assertEqual(check_plan.markers_in(quiet, MARKERS), [])

    def test_markers_come_from_the_profile(self):
        # user_id is a landmine under PROFILE; under a profile with no markers the same story passes.
        story = tiered(("1", "light", "let owner = user_id;"))
        self.assertEqual(run(story)[0], 1)
        code, out = run(story, profile="schema = 1\n")
        self.assertEqual(code, 0, out)

    def test_the_markers_a_story_holds_are_listed(self):
        code, out = run(tiered(("1", "deep", "let owner = user_id;\n#[tokio::test]")))
        self.assertEqual(code, 0, out)
        self.assertRegex(out, r"Story 1\s+.*review: full  landmines: #\[tokio::test, user_id")

    def test_light_story_with_a_marker_fails(self):
        text = tiered(("1", "light", "let owner = user_id;"))
        code, out = run(text)
        self.assertEqual(code, 1, out)
        story_line = line_of(text, "## Story 1: Title 1")
        marker_line = line_of(text, "let owner = user_id;")
        self.assertIn(
            f"Story 1 (line {story_line}): light, but its code has user_id (line {marker_line}): "
            "make it deep, or name the marker in the Review line's reason",
            out,
        )

    def test_a_reason_naming_the_marker_silences_it(self):
        code, out = run(tiered(("1", "light (user_id only in a doc comment)", "let owner = user_id;")))
        self.assertEqual(code, 0, out)

    def test_a_reason_for_one_marker_leaves_the_others(self):
        text = tiered(("1", "light (user_id only in a doc comment)", "let owner = user_id;\nreqwest::get(url)"))
        code, out = run(text)
        self.assertEqual(code, 1, out)
        self.assertIn(f"has reqwest (line {line_of(text, 'reqwest::get(url)')})", out)
        self.assertNotIn("has user_id", out)

    def test_a_deep_story_may_hold_markers(self):
        for value in ("deep", "deep (per-user store)", None):
            code, out = run(tiered(("1", value, "let owner = user_id;")))
            self.assertEqual(code, 0, f"{value!r}: {out}")

    def test_a_marker_outside_a_code_fence_is_ignored(self):
        text = tiered(("1", "light", "plain")).replace("### Task 1.1: Task", "### Task 1.1: user_id prose")
        code, out = run(text)
        self.assertEqual(code, 0, out)


class PlanReviewTierTest(unittest.TestCase):
    def test_review_tiers_reads_both_lists(self):
        review = "2026-10-15; code-quality-reviewer on seams; deep: 1, 6; recon on seams; light: 2 3,7"
        self.assertEqual(check_plan.review_tiers(review), ({"1", "6"}, {"2", "3", "7"}))

    def test_review_tiers_is_none_for_a_line_without_tokens(self):
        self.assertIsNone(check_plan.review_tiers(None))
        self.assertIsNone(check_plan.review_tiers("2026-10-14, code-quality-reviewer and recon, every story"))

    def test_review_tiers_with_one_token_leaves_the_other_empty(self):
        self.assertEqual(check_plan.review_tiers("deep: 1"), ({"1"}, set()))
        self.assertEqual(check_plan.review_tiers("recon on seams; light: 4, 5"), (set(), {"4", "5"}))

    def test_vouched_follows_the_line(self):
        stories = [check_plan.Story("1", 1), check_plan.Story("2", 2)]
        self.assertEqual(check_plan.vouched(None, stories), {"1": False, "2": False})
        self.assertEqual(check_plan.vouched("every story, written before tiers", stories), {"1": True, "2": True})
        stories[1].fields["Review"] = "light"
        # A tokenless line vouches for deep stories only: a light story needs the full review.
        self.assertEqual(check_plan.vouched("every story, written before tiers", stories), {"1": True, "2": False})
        stories[1].fields.pop("Review")
        self.assertEqual(check_plan.vouched("deep: 1; light: 2", stories), {"1": True, "2": False})
        self.assertEqual(check_plan.vouched("light: 1, 2", stories), {"1": False, "2": False})

    def test_output_columns_give_the_tier_and_whether_the_review_vouches(self):
        review = "2026-10-15; deep: 1, 3; light: 2"
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), ("3", None, "x"), review=review))
        self.assertEqual(code, 0, out)
        self.assertRegex(out, r"Story 1\s+junior-dev\s+1 files\s+no build\s+deep\s+review: vouched")
        self.assertRegex(out, r"Story 2\s+junior-dev\s+1 files\s+no build\s+light\s+review: full")
        self.assertRegex(out, r"Story 3\s+junior-dev\s+1 files\s+no build\s+deep\s+review: vouched")

    def test_a_line_written_before_tiers_vouches_for_every_story(self):
        review = "2026-10-14, code-quality-reviewer and recon, every story"
        code, out = run(tiered(("1", "deep", "x"), ("2", None, "x"), review=review))
        self.assertEqual(code, 0, out)
        self.assertRegex(out, r"Story 1\s+.*deep\s+review: vouched")
        self.assertRegex(out, r"Story 2\s+.*deep\s+review: vouched")

    def test_a_light_story_under_a_line_without_tiers_fails(self):
        review = "2026-10-14, code-quality-reviewer and recon, every story"
        text = tiered(("1", "deep", "x"), ("2", "light (docs)", "x"), review=review)
        code, out = run(text)
        self.assertEqual(code, 1, out)
        self.assertIn(
            f"Story 2 (line {line_of(text, '## Story 2: Title 2')}): light, but the Plan review line lists no "
            "deep:/light: tiers: list every story under one",
            out,
        )
        self.assertNotIn("Story 1 (", out)

    def test_a_case_typo_in_the_tier_tokens_still_parses(self):
        self.assertEqual(check_plan.review_tiers("Deep: 1; Light: 2"), ({"1"}, {"2"}))
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), review="2026-10-15; Deep: 1; LIGHT: 2"))
        self.assertEqual(code, 0, out)
        self.assertRegex(out, r"Story 1\s+.*deep\s+review: vouched")
        self.assertRegex(out, r"Story 2\s+.*light\s+review: full")

    def test_review_tiers_keeps_ids_with_letters(self):
        self.assertEqual(check_plan.review_tiers("deep: 1a, B2; light: 3-x"), ({"1a", "B2"}, {"3-x"}))

    def test_review_tiers_strips_one_trailing_dot(self):
        self.assertEqual(check_plan.review_tiers("deep: 1, 7."), ({"1", "7"}, set()))
        code, out = run(tiered(("1", "deep", "x"), ("7", "light", "x"), review="2026-10-15; deep: 1; light: 7."))
        self.assertEqual(code, 0, out)

    def test_a_token_that_is_no_story_id_is_reported(self):
        for token in ("1-3", "`2`"):
            review = f"2026-10-15; deep: 1; light: {token}"
            code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), review=review))
            self.assertEqual(code, 1, f"{token!r}: {out}")
            self.assertIn(
                f"the Plan review line lists {token} under light:, which is no story id "
                "(list ids one at a time, on one physical line: no ranges, punctuation or backticks)",
                out,
            )
            self.assertIn("Story 2: the Plan review line lists it under neither deep: nor light:", out)

    def test_no_plan_review_line_means_the_full_review(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x")))
        self.assertEqual(code, 0, out)
        self.assertIn("Plan review: none recorded", out)
        self.assertRegex(out, r"Story 1\s+.*deep\s+review: full")
        self.assertRegex(out, r"Story 2\s+.*light\s+review: full")

    def test_a_story_listed_under_the_wrong_tier_fails(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), review="2026-10-15; deep: 1, 2"))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 2: the Plan review line lists it as deep, its Review line says light", out)
        self.assertNotIn("Story 1: the Plan review line", out)

    def test_a_story_with_no_review_line_listed_light_says_so(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", None, "x"), review="2026-10-15; deep: 1; light: 2"))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 2: the Plan review line lists it as light, its tier is deep (no Review line)", out)

    def test_a_story_with_an_unparsable_review_value_is_not_also_compared(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", "maybe", "x"), review="2026-10-15; deep: 1; light: 2"))
        self.assertEqual(code, 1, out)
        self.assertIn("**Review:** must be deep or light", out)
        self.assertNotIn("the Plan review line lists it as", out)

    def test_a_story_in_neither_list_fails(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", None, "x"), review="2026-10-15; deep: 1"))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 2: the Plan review line lists it under neither deep: nor light:", out)
        self.assertNotIn("Story 1: the Plan review line", out)

    def test_a_story_in_both_lists_fails(self):
        code, out = run(tiered(("1", "deep", "x"), ("2", "light", "x"), review="2026-10-15; deep: 1, 2; light: 2"))
        self.assertEqual(code, 1, out)
        self.assertIn("Story 2: the Plan review line lists it under both deep: and light:", out)
        self.assertNotIn("Story 1: the Plan review line", out)


if __name__ == "__main__":
    unittest.main()


def paired(line: str) -> str:
    """GOOD with `line` below its Spec line."""
    return GOOD.replace("**Spec:** `docs/spec.md`, approved.\n", f"**Spec:** `docs/spec.md`, approved.\n\n{line}\n")


class ParallelLineTest(unittest.TestCase):
    """The `**Parallel:**` line (parallel pairs spec §5.1)."""

    def test_a_declared_adjacent_safe_pair_passes(self):
        code, out = run(paired("**Parallel:** 1+2, 2+3"))
        self.assertEqual(code, 0, out)
        self.assertIn("Declared pairs (the second story's developer runs in a lane): 1+2, 2+3", out)

    def test_without_the_line_every_story_runs_in_order(self):
        code, out = run(GOOD)
        self.assertEqual(code, 0, out)
        self.assertIn("Declared pairs (the second story's developer runs in a lane): none, so every story runs in order", out)

    def test_json_marks_each_laned_story(self):
        code, out = run(paired("**Parallel:** 2+3."), as_json=True)
        self.assertEqual(code, 0, out)
        self.assertEqual([s.get("lane", False) for s in json.loads(out)["stories"]], [False, False, True])

    def test_each_failing_entry(self):
        cases = {
            "**Parallel:** 1+3": "Story 3 must be the story right after Story 1",
            "**Parallel:** 2+9": "names Story 9, which the plan doesn't have",
            "**Parallel:** 1-2": "the **Parallel:** entry '1-2' is not two story ids joined by +",
            "**Parallel:** 1+2, 1+2": "the **Parallel:** pair 1+2 is declared twice",
            "**Parallel:** 1+2\n**Parallel:** 2+3": "a second **Parallel:** line",
        }
        for line, want in cases.items():
            with self.subTest(line=line):
                code, out = run(paired(line))
                self.assertEqual(code, 1, out)
                self.assertIn(want, out)

    def test_a_pair_sharing_a_file_fails(self):
        text = paired("**Parallel:** 2+3").replace("- **Files:** `ui/panel.ts`", "- **Files:** `ui/panel.ts`, `a/route.rs`")
        code, out = run(text)
        self.assertEqual(code, 1, out)
        self.assertIn("the **Parallel:** pair 2+3: both stories' **Files** name a/route.rs", out)

    def test_a_pair_that_both_run_an_exclusive_build_fails(self):
        code, out = run(paired("**Parallel:** 2+3").replace("`pnpm test panel`", "`cargo test -p ui`"))
        self.assertEqual(code, 1, out)
        self.assertIn("the **Parallel:** pair 2+3: both stories' **Verify** run an exclusive build", out)

    def test_a_line_below_the_first_story_is_not_read(self):
        code, out = run(GOOD.replace("### Task 3.1: Route", "**Parallel:** 1+3\n\n### Task 3.1: Route"))
        self.assertEqual(code, 0, out)
        self.assertIn("runs in a lane): none", out)


SKELETON_TEXT = textwrap.dedent(
    """\
    # Plan

    **Spec:** `docs/spec.md`

    ## Names contract

    - `pub fn get(id: u32) -> Row` (Story 1).

    ## Stories

    - **Story 1** — store.
      - **Developer:** junior-dev
      - **Files:** `a/store.rs`
      - **Interfaces:** exposes `get`
      - **Verify:** `cargo nextest run -p a store`
      - **Reading:** spec §1

    <!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
    """
)


def run_skeleton(text: str, *extra: str) -> tuple[int, str, str]:
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(["git", "-C", tmp, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
        Path(tmp, "docs").mkdir()
        Path(tmp, "docs", "spec.md").write_text("# Spec\n")
        plan = Path(tmp, "plan.md")
        plan.write_text(text)
        env = {**CLEAN_ENV, "FLOW_HOME": str(Path(tmp, "flow-home"))}
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--skeleton", str(plan), *extra], capture_output=True, text=True, env=env, cwd=tmp
        )
        return result.returncode, result.stdout.replace(str(plan), "PLAN"), result.stderr


class SkeletonCliTest(unittest.TestCase):
    def test_a_good_skeleton_passes_without_a_profile(self):
        code, out, _ = run_skeleton(SKELETON_TEXT)
        self.assertEqual((code, out), (0, "OK: PLAN: skeleton; 1 stories; contract 1 lines\n"))

    def test_each_problem_is_one_fail_line(self):
        code, out, _ = run_skeleton(SKELETON_TEXT.replace("get(id: u32)", "get(id)").replace("  - **Reading:** spec §1\n", ""))
        self.assertEqual(code, 1)
        self.assertEqual(out.splitlines(), [
            "FAIL [skeleton] PLAN: Story 1 entry (line 11): no **Reading:** line: the drafter reads what it names",
            "FAIL [skeleton] PLAN: line 7: `pub fn get(id) -> Row` gives its parameter `id` no type",
        ])

    def test_skeleton_takes_one_plan(self):
        code, _, err = run_skeleton(SKELETON_TEXT, "other.md")
        self.assertEqual(code, 2)
        self.assertIn("usage", err)
