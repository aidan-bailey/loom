"""Tests for plan_skeleton.py. Run from lib/: python3 -m unittest -q plan_skeleton_test.py"""

import os
import re
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plan_skeleton  # noqa: E402
from plan_join import MARKER  # noqa: E402

_HOME = tempfile.TemporaryDirectory()


def setUpModule():
    os.environ["FLOW_HOME"] = _HOME.name


def tearDownModule():
    _HOME.cleanup()


def entry(sid, files="`a.py`", extra=()):
    """A well-formed story-list entry."""
    return [
        f"- **Story {sid}** — story {sid}.",
        "  - **Developer:** intermediate-dev",
        f"  - **Files:** {files}",
        "  - **Interfaces:** none",
        "  - **Verify:** `python3 -m unittest a_test.py`",
        *extra,
        "  - **Reading:** spec §1",
    ]


def skeleton(contract, stories=None, head=("# Plan", "", "**Spec:** `docs/spec.md`", "")):
    """A skeleton around contract lines, with Stories 1 to 5 unless `stories` gives the list's lines."""
    if stories is None:
        stories = [line for sid in "12345" for line in entry(sid)]
    return "\n".join([*head, "## Names contract", "", *contract, "", "## Stories", "", *stories, "", MARKER]) + "\n"


def lint(text, spec=True, spec_text="# Spec\n", profile=None):
    """lint's problems for a skeleton written into a throwaway repo, with its spec's text and an optional profile."""
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        (root / ".git").mkdir()
        if spec:
            (root / "docs").mkdir()
            (root / "docs" / "spec.md").write_text(spec_text)
        if profile is not None:
            (root / ".claude").mkdir()
            (root / ".claude" / "flow.toml").write_text(profile)
        plan = root / "plan.md"
        plan.write_text(text)
        problems, _, _ = plan_skeleton.lint(text, plan)
        return problems


def stories(n):
    """The story-list lines of Stories 1 to n."""
    return [line for sid in range(1, n + 1) for line in entry(str(sid))]


DELIVERY = textwrap.dedent(
    """\
    # Spec

    ## Delivery

    An epic of 3 parts.

    1. **Part 1: kernel.** Stage 0.
    2. **Part 2: markets.** Stages 1 and 2.
    3. **Part 3: identity.** Stage 3.

    ### Part 2 settlements (2026-11-14)

    1. **Part 9: not a part.** A settlement's own list.
    """
)
PART_HEAD = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Part:** 2 of 3", "")


def flagged(text, problems):
    """The stripped text of each plan line a `line <n>:` problem names."""
    lines = text.split("\n")
    return {lines[int(m.group(1)) - 1].strip() for p in problems if (m := re.match(r"line (\d+): ", p))}


class SpanRuleTest(unittest.TestCase):
    def problems(self, span, fields_here=False):
        return plan_skeleton.span_problems(span, fields_here)

    def test_an_elision_fails_and_a_range_passes(self):
        for span in ("delete_snapshot(..)", "open_tab(.., …)", "search(handles: &H, ...) -> Vec<Hit>", "Item { id, .. }"):
            self.assertTrue(self.problems(span), span)
        for span in ("slice[0..5]", "take(0..5)", "get(0..=3)"):
            self.assertEqual(self.problems(span), [], span)

    def test_a_bare_parameter_fails_and_a_typed_one_or_a_primitive_or_a_receiver_passes(self):
        self.assertEqual(self.problems("changed(scope_key, id: &Id)"), ["`changed(scope_key, id: &Id)` gives its parameter `scope_key` no type"])
        self.assertEqual(len(self.problems("subscribe(id, onEvent)")), 2)
        for span in ("f(x: u32) -> f64", "f(&self, cell: u32)", "f(&mut self)", "f(self)", "m(this)", "f(f64)",
                     "update(&Id, HeaderUpdate)", "list(running: &dyn Fn(&Id) -> bool, limit: u32)",
                     "on(cb: (x: number) => void, n: number)", "f()"):
            self.assertEqual(self.problems(span), [], span)

    def test_a_fieldless_type_fails_and_fields_opaque_a_tuple_or_nested_bullets_pass(self):
        self.assertTrue(self.problems("pub struct HubSet"))
        self.assertTrue(self.problems("export interface Props"))
        for span in ("pub struct S { a: u32 }", "pub struct Id(String)", "pub enum E { A, B }"):
            self.assertEqual(self.problems(span), [], span)
        self.assertEqual(self.problems("pub struct Cache", fields_here=True), [])

    def test_attributes_patterns_visibility_and_calls_are_not_signatures(self):
        for span in ('#[serde(tag = "kind")]', "@Component(x)", "<workspace>/cells/<cell>.parquet",
                     "pub(crate) fn path(&self, cell: u32) -> PathBuf", 'plan_launch("nope")', "retry(3)",
                     "Some(id)", "Ok(value)", "Err(e)"):
            self.assertEqual(self.problems(span), [], span)


class OwnerRuleTest(unittest.TestCase):
    def test_an_unowned_defining_line_fails(self):
        text = skeleton(["- `pub fn rows(id: u32) -> Vec<Row>`."])
        self.assertEqual(flagged(text, lint(text)), {"- `pub fn rows(id: u32) -> Vec<Row>`."})

    def test_an_owner_on_the_line_a_parent_bullet_or_the_introducing_line_passes(self):
        contract = [
            "- `pub fn a(x: u32)` (Story 1).",
            "- `Store` (Story 2), with:",
            "  - `get(id: u32) -> Row`;",
            "  continued text",
            "  - `put(row: Row)`.",
            "",
            "**crate-b** (Story 3)",
            "- `pub fn b(y: u32)`.",
            "  - `pub fn c(z: u32)`.",
            "",
            "**Story 4's interim arms.** Each is replaced later.",
            "- `pub fn d(w: u32)`.",
        ]
        self.assertEqual(lint(skeleton(contract)), [])

    def test_a_blank_line_ends_the_walk_and_an_unlisted_owner_does_not_count(self):
        contract = ["**crate-b** (Story 3)", "", "- `pub fn b(y: u32)`.", "- `pub fn c(z: u32)` (Story 9)."]
        text = skeleton(contract)
        self.assertEqual(flagged(text, lint(text)), {"- `pub fn b(y: u32)`.", "- `pub fn c(z: u32)` (Story 9)."})

    def test_a_line_without_a_signature_needs_no_owner(self):
        self.assertEqual(lint(skeleton(["Wire types use `#[serde(rename_all = \"camelCase\")]` and `u32` ids."])), [])


class ProposesRuleTest(unittest.TestCase):
    def test_proposes_waives_rule_one_and_names_the_owner(self):
        self.assertEqual(lint(skeleton(["- `pub struct CollectHandles` and `search(handles, …)` (Story 4 proposes)."])), [])

    def test_proposes_for_an_unlisted_story_fails(self):
        problems = lint(skeleton(["- `pub struct Handles` (Story 99 proposes)."]))
        self.assertEqual(len(problems), 2, problems)
        self.assertIn("(Story 99 proposes) names no story in the list", problems[0])


class HeadTest(unittest.TestCase):
    def test_a_good_skeleton_passes_with_its_counts(self):
        text = skeleton(["- `pub fn a(x: u32)` (Story 1).", "", "- `pub fn b(x: u32)` (Story 2)."])
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / ".git").mkdir()
            (Path(tmp) / "docs").mkdir()
            (Path(tmp) / "docs" / "spec.md").write_text("# Spec\n")
            self.assertEqual(plan_skeleton.lint(text, Path(tmp) / "plan.md"), ([], 5, 2))

    def test_a_missing_contract_heading_fails(self):
        text = skeleton([]).replace("## Names contract\n", "## Contract\n")
        self.assertTrue(any("no ## Names contract heading" in p for p in lint(text)))

    def test_a_contract_below_the_stories_fails(self):
        stories = [line for sid in "12345" for line in entry(sid)]
        text = "\n".join(["**Spec:** `docs/spec.md`", "", "## Stories", "", *stories, "", "## Names contract", "", MARKER]) + "\n"
        self.assertTrue(any("must sit above ## Stories" in p for p in lint(text)))

    def test_no_marker_and_two_markers_fail(self):
        text = skeleton([])
        self.assertTrue(any("no marker line" in p for p in lint(text.replace(MARKER + "\n", ""))))
        self.assertTrue(any("2 marker lines" in p for p in lint(text + MARKER + "\n")))

    def test_the_spec_line_must_name_a_file_that_exists(self):
        self.assertTrue(any("no **Spec:** line" in p for p in lint(skeleton([], head=("# Plan", "")))))
        self.assertTrue(any("the spec docs/spec.md does not exist" in p for p in lint(skeleton([]), spec=False)))

    def test_a_story_heading_or_a_task_heading_above_the_marker_fails(self):
        text = skeleton([], head=("**Spec:** `docs/spec.md`", "", "## Story 1: early", "", "### Section", ""))
        problems = lint(text)
        self.assertTrue(any("a ## Story heading" in p for p in problems), problems)
        self.assertTrue(any("a ### heading" in p for p in problems), problems)

    def test_a_contract_line_quoted_in_a_fence_is_not_checked(self):
        self.assertEqual(lint(skeleton(["```text", "- `f(x)`", "```"])), [])


class WaveTest(unittest.TestCase):
    def stories(self, *afters):
        """Stories 1 to 3, each with the Drafts after line `afters` gives it (None for none)."""
        listed = []
        for sid, after in zip("123", afters):
            listed += entry(sid, extra=() if after is None else (f"  - **Drafts after:** {after}",))
        return lint(skeleton([], stories=listed))

    def test_a_later_wave_passes(self):
        self.assertEqual(self.stories(None, None, "Stories 1 and 2"), [])

    def test_an_unknown_story_its_own_and_none_fail(self):
        problems = self.stories("Story 9", "Story 2", "")
        self.assertTrue(any("Story 1 entry" in p and "Story 9, which the list doesn't have" in p for p in problems), problems)
        self.assertTrue(any("Story 2 entry" in p and "its own story" in p for p in problems), problems)
        self.assertTrue(any("Story 3 entry" in p and "names no story" in p for p in problems), problems)

    def test_a_circle_fails_once(self):
        problems = self.stories("Story 3", "Story 1", "Story 2")
        circles = [p for p in problems if "wait in a circle" in p]
        self.assertEqual(len(circles), 1, problems)

    def test_a_repeated_line_fails(self):
        listed = entry("1") + entry("2", extra=("  - **Drafts after:** Story 1", "  - **Drafts after:** Story 1"))
        problems = lint(skeleton([], stories=listed))
        self.assertTrue(any("gives **Drafts after:** twice" in p for p in problems), problems)


class EntryTest(unittest.TestCase):
    def stories(self, *blocks):
        return lint(skeleton([], stories=[line for block in blocks for line in block]))

    def test_each_missing_line_fails(self):
        for name in ("Developer", "Files", "Interfaces", "Verify", "Reading"):
            block = [line for line in entry("1") if f"**{name}:**" not in line]
            problems = self.stories(block)
            self.assertTrue(any(f"Story 1 entry (line " in p and f"**{name}:**" in p for p in problems), (name, problems))

    def test_bad_values_fail_and_good_ones_pass(self):
        self.assertTrue(any("not one of" in p for p in self.stories([l.replace("intermediate-dev", "staff-dev") for l in entry("1")])))
        self.assertTrue(any("names no `backticked` path" in p for p in self.stories(entry("1", files="a.py"))))
        self.assertTrue(any("names no `backticked` command" in p for p in self.stories([l.replace("`python3 -m unittest a_test.py`", "run it") for l in entry("1")])))
        self.assertTrue(any("deep or light" in p for p in self.stories(entry("1", extra=["  - **Review:** shallow"]))))
        self.assertEqual(self.stories(entry("1", extra=["  - **Review:** light (a doc change)"]), [l.replace("intermediate-dev", "flow:junior-dev") for l in entry("2")]), [])

    def test_a_repeated_line_fails(self):
        self.assertTrue(any("gives **Files:** twice" in p for p in self.stories(entry("1", extra=["  - **Files:** `b.py`"]))))

    def test_an_entry_the_join_cannot_read_fails(self):
        for bad in ("- **Story 2:** title", "- **Story 2**: title", "1. **Story 2** — title", "  - **Story 2** — title"):
            problems = self.stories(entry("1"), [bad])
            self.assertTrue(any("the join reads an entry only as" in p for p in problems), (bad, problems))

    def test_an_empty_list_fails(self):
        self.assertTrue(any("lists no stories" in p for p in self.stories([])))


# A contract modelled on a real run's (2026-11-11), with its names replaced: what the lint must flag and what it must pass.
MODELLED = textwrap.dedent(
    """\
    Every name below is new unless marked "existing". Wire types use `#[serde(rename_all = "camelCase")]` and never `i64`: ids are `u32`.

    **notes-protocol** (Story 1)
    - `pub fn is_note_id(s: &str) -> bool`: `n` then 12 characters, beside `is_task_id`.
    - `ANCHOR_KINDS: [&str; 9]`, with `"note"` appended last.

    **notes-core**
    - **`anchor.rs` (Story 1):**
      - `pub struct NoteId(String)`, mirroring `TaskId`.
      - `SessionAnchor::Note { id: NoteId }`, with `SessionAnchor::note(id: NoteId)`.
    - **`store.rs` (Story 2):** `pub(crate) fn cell_path(&self, cell: u32) -> PathBuf`: `<workspace>/cells/<cell>.parquet`.
    - `pub enum NoteError { Gone, Invalid(String), Busy(String) }` (Story 2).
    - `pub struct NoteStore` (Story 2), with:
      - `create(name: Option<String>) -> Result<NoteId, NoteError>`;
      - `list(running: &dyn Fn(&NoteId) -> bool) -> Result<Vec<NoteSummary>, NoteError>`.
    - `pub enum SnapshotSource` (Story 2):
      - `Spot { pair: String }`;
      - `Perp { key: String }`.
    - `pub struct CellCache` (Story 3) (opaque).
    - `COLLAPSE_WINDOW = Duration::from_secs(300)` (Story 3).
    - `plan_launch("nope")` refuses a note anchor until Story 5 replaces it.
    - `pub struct HubSet` (Story 3).
    - `NotesHub::changed(scope_key, &NoteId)` (Story 3).
    - `publish(store: &NoteStore, id: &NoteId, running)` (Story 3).
    - `search(handles: &CollectHandles, …) -> Vec<Hit>` (Story 4).
    - `delete_snapshot(..)` (Story 4).
    - `run_stale(&self, cancel) -> u32` (Story 4).
    - `subscribe(id, onEvent)` (Story 5).
    - `pub struct CollectHandles` (Story 4 proposes).
    - `pub fn rows_for(id: &NoteId, window: Window) -> Vec<Row>`.
    """
).rstrip("\n").split("\n")
MODELLED_FLAGS = {
    "- `pub struct HubSet` (Story 3).",
    "- `NotesHub::changed(scope_key, &NoteId)` (Story 3).",
    "- `publish(store: &NoteStore, id: &NoteId, running)` (Story 3).",
    "- `search(handles: &CollectHandles, …) -> Vec<Hit>` (Story 4).",
    "- `delete_snapshot(..)` (Story 4).",
    "- `run_stale(&self, cancel) -> u32` (Story 4).",
    "- `subscribe(id, onEvent)` (Story 5).",
    "- `pub fn rows_for(id: &NoteId, window: Window) -> Vec<Row>`.",
}


class ModelledContractTest(unittest.TestCase):
    def test_the_modelled_contract_flags_exactly_its_gaps(self):
        text = skeleton(MODELLED)
        problems = lint(text)
        self.assertEqual(flagged(text, problems), MODELLED_FLAGS)
        # subscribe's two bare parameters are two problems.
        self.assertEqual(len(problems), len(MODELLED_FLAGS) + 1, problems)


class PartTest(unittest.TestCase):
    def test_a_part_the_delivery_list_holds_passes(self):
        self.assertEqual(lint(skeleton([], head=PART_HEAD), spec_text=DELIVERY), [])

    def test_a_split_spec_needs_a_part_line(self):
        problems = lint(skeleton([]), spec_text=DELIVERY)
        self.assertTrue(any("the spec delivers 3 parts: name this plan's part" in p for p in problems), problems)

    def test_a_part_line_needs_a_delivery_list(self):
        problems = lint(skeleton([], head=PART_HEAD))
        self.assertTrue(any("the spec has no ## Delivery section" in p for p in problems), problems)

    def test_a_part_outside_the_list_or_a_wrong_count_fails(self):
        for line in ("**Part:** 4 of 3", "**Part:** 2 of 4", "**Part:** 0 of 3"):
            head = ("# Plan", "", "**Spec:** `docs/spec.md`", line, "")
            problems = lint(skeleton([], head=head), spec_text=DELIVERY)
            self.assertTrue(any("doesn't match the spec's ## Delivery list of 3 parts" in p for p in problems), (line, problems))

    def test_a_part_line_that_is_not_n_of_m_fails(self):
        head = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Part:** two", "")
        problems = lint(skeleton([], head=head), spec_text=DELIVERY)
        self.assertTrue(any("must read `N of M`" in p for p in problems), problems)

    def test_a_delivery_list_out_of_order_fails(self):
        spec = DELIVERY.replace("2. **Part 2: markets.**", "3. **Part 3: markets.**")
        problems = lint(skeleton([], head=PART_HEAD), spec_text=spec)
        self.assertTrue(any("number its parts 1 to M in order" in p for p in problems), problems)

    def test_delivery_parts_stops_at_the_settlements(self):
        self.assertEqual(plan_skeleton.delivery_parts(DELIVERY), [(1, 1), (2, 2), (3, 3)])
        self.assertIsNone(plan_skeleton.delivery_parts("# Spec\n"))


class BudgetTest(unittest.TestCase):
    def test_the_default_budget_is_fifteen_stories(self):
        self.assertEqual(lint(skeleton([], stories=stories(15))), [])
        problems = lint(skeleton([], stories=stories(16)))
        self.assertTrue(any("16 stories, over the plan budget of 15" in p for p in problems), problems)

    def test_a_size_line_with_a_reason_lets_it_through_and_an_empty_one_does_not(self):
        reasoned = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Size:** one migration ties every stage", "")
        self.assertEqual(lint(skeleton([], stories=stories(16), head=reasoned)), [])
        empty = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Size:**", "")
        problems = lint(skeleton([], stories=stories(16), head=empty))
        self.assertTrue(any("the **Size:** line gives no reason" in p for p in problems), problems)

    def test_the_profile_sets_the_budget(self):
        self.assertEqual(lint(skeleton([], stories=stories(16)), profile="schema = 1\n[size]\nplan = 20\n"), [])
        problems = lint(skeleton([], stories=stories(4)), profile="schema = 1\n[size]\nplan = 3\n")
        self.assertTrue(any("4 stories, over the plan budget of 3" in p for p in problems), problems)

    def test_a_profile_the_flow_refuses_is_a_problem_not_a_crash(self):
        problems = lint(skeleton([]), profile="schema = 1\n[size]\nplan = 0\n")
        self.assertTrue(any("the profile can't be read" in p and "positive integer" in p for p in problems), problems)


if __name__ == "__main__":
    unittest.main()
