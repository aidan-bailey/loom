"""Tests for plan_packet.py. Run from lib/: python3 -m unittest -q plan_packet_test.py"""

import contextlib
import io
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

LIB = Path(__file__).resolve().parent
SCRIPT = LIB / "plan_packet.py"
sys.path.insert(0, str(LIB))
import plan_join  # noqa: E402
import plan_packet  # noqa: E402
from plan_premises import Tree, gather  # noqa: E402

# A git hook's GIT_* variables would point the temp repos at the outer repository.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
FENCE = "~~~"
SHORT = "".join(f"short{n}\n" for n in range(1, 6))
ROWS = "".join(f"row{n:03d}\n" for n in range(1, 9))
QUOTED = "fn quoted_anchor() {}\n"

# The plan is a skeleton plus two drafts, joined the way /flow-draft joins them.
SKELETON = textwrap.dedent(
    f"""\
    # Plan

    **Spec:** `docs/spec.md`

    ## Stories

    - **Story 1** — Prices.
      - **Developer:** junior-dev
      - **Files:** `src/rows.rs`, `src/short.rs`, `src/new.rs`
      - **Interfaces:** exposes `lookup_price`
      - **Verify:** `cargo test`
    - **Story 2** — Screen.
      - **Developer:** junior-dev
      - **Files:** `src/short.rs`
      - **Interfaces:** uses `lookup_price` from Story 1
      - **Verify:** `cargo test`

    {plan_join.MARKER}
    """
)

DRAFT_1 = textwrap.dedent(
    f"""\
    ## Story 1: Prices

    ### Task 1.1: Prices

    **Files:**
    - Modify: `src/rows.rs`
    - Create: `src/new.rs`

    In `src/rows.rs`, current:

    {FENCE}rust
    row003
    row004
    {FENCE}

    In `src/rows.rs`, replace the rows below, currently:

    {FENCE}rust
    row006
    row007
    {FENCE}

    with:

    {FENCE}rust
    row006
    changed
    {FENCE}

    Add to `src/new.rs`:

    {FENCE}rust
    /// Looks up the last price.
    /// Returns None when unknown.
    pub fn lookup_price(id: u32) -> Option<u32> {{
        private_helper(id)
    }}

    fn private_helper(id: u32) -> Option<u32> {{
        Some(id)
    }}
    {FENCE}
    """
)

DRAFT_2 = textwrap.dedent(
    f"""\
    ## Story 2: Screen

    ### Task 2.1: Screen

    **Files:**
    - Modify: `src/short.rs:2`

    {FENCE}rust
    pub fn show() -> Option<u32> {{
        ctx.private_helper(1);
        lookup_price(7)
    }}
    {FENCE}

    {FENCE}bash
    grep -r private_helper src/
    {FENCE}
    """
)

UNITS = textwrap.dedent(
    f"""\
    ## Story 1: Defs

    - **Files:** `src/short.rs`, `src/fresh.rs`, `src/ghost.rs`

    ### Task 1.1: Everything

    **Files:**
    - Modify: `src/short.rs`
    - Create: `src/fresh.rs`
    - Create: `src/fresh_test.py`

    Add to `src/fresh.rs`:

    {FENCE}rust
    pub fn real_def() {{}}

    pub fn wrapped_def(
        first: u32,
        second: u32,
    ) -> u32 {{
        first
    }}

    pub fn dotted_def() {{}}

    pub fn bare_def() {{}}

    pub struct Wide {{
        pub a: u32,
    }}
    {FENCE}

    Add to `src/fresh_test.py`:

    {FENCE}python
    def test_helper():
        pass

    class Tree:
        pass
    {FENCE}

    In `src/quoted.rs`, replace the line below, currently:

    {FENCE}rust
    fn quoted_anchor() {{}}
    {FENCE}

    with:

    {FENCE}rust
    fn replacement_def() {{}}
    {FENCE}

    In `src/short.rs`, replace `redefined_def`:

    {FENCE}rust
    fn redefined_def() {{}}
    {FENCE}

    In `src/short.rs`, current:

    {FENCE}rust
    short2
    {FENCE}

    Reading notes:

    {FENCE}markdown
    fn md_def() {{}}
    {FENCE}

    ## Story 2: Uses

    - **Files:** `src/short.rs`
    - **Interfaces:** nothing

    ### Task 2.1: Uses

    {FENCE}rust
    real_def();
    wrapped_def(1, 2);
    test_helper();
    Tree::new();
    quoted_anchor();
    replacement_def();
    redefined_def();
    md_def();
    x.dotted_def();
    {FENCE}

    {FENCE}bash
    cargo run Wide
    {FENCE}

    {FENCE}
    bare_def();
    {FENCE}

    ## Story 3: Own

    - **Files:** `src/short.rs`
    - **Interfaces:** nothing

    ### Task 3.1: Own

    Add to `src/own_test.py`:

    {FENCE}python
    def real_def():
        pass

    real_def()
    {FENCE}
    """
)

TS_PLAN = textwrap.dedent(
    f"""\
    ## Story 1: Format

    - **Files:** `web/price.ts`

    ### Task 1.1: Format

    **Files:**
    - Create: `web/price.ts`

    Create `web/price.ts`:

    {FENCE}ts
    export function formatPrice(cents: number): string {{
      return (cents / 100).toFixed(2);
    }}

    export const PRICE_LABEL = "Price";

    export interface PriceRow {{
      label: string;
      cents: number;
    }}
    {FENCE}

    ## Story 2: Screen

    - **Files:** `web/screen.ts`

    ### Task 2.1: Screen

    **Files:**
    - Create: `web/screen.ts`

    Create `web/screen.ts`:

    {FENCE}ts
    import {{ formatPrice, PriceRow }} from "./price";

    export function render(row: PriceRow): string {{
      return row.label + ": " + formatPrice(row.cents);
    }}
    {FENCE}
    """
)

PY_PLAN = textwrap.dedent(
    f"""\
    ## Story 1: Rows

    - **Files:** `app/rows.py`

    ### Task 1.1: Rows

    **Files:**
    - Create: `app/rows.py`

    Create `app/rows.py`:

    {FENCE}python
    class RowError(Exception):
        pass


    def load_rows(path):
        return path.read_text().splitlines()
    {FENCE}

    ## Story 2: Report

    - **Files:** `app/report.py`

    ### Task 2.1: Report

    **Files:**
    - Create: `app/report.py`

    Create `app/report.py`:

    {FENCE}python
    from app.rows import RowError, load_rows


    def report(path):
        try:
            return len(load_rows(path))
        except RowError:
            return 0
    {FENCE}
    """
)

# A rename whose "currently" block is quoted text only at a commit whose src/a.rs holds it.
REV_PLAN = textwrap.dedent(
    f"""\
    ## Story 1: Rename

    - **Files:** `src/a.rs`

    ### Task 1.1: Rename

    In `src/a.rs`, replace the line below, currently:

    {FENCE}rust
    fn old_name() {{}}
    {FENCE}

    with:

    {FENCE}rust
    fn new_name() {{}}
    {FENCE}

    ## Story 2: Callers

    - **Files:** `src/b.rs`

    ### Task 2.1: Callers

    {FENCE}rust
    old_name();
    new_name();
    {FENCE}
    """
)


def build(skeleton, drafts):
    """(the joined plan, {id: Span}) as plan_join gives them for a skeleton and its drafts."""
    with tempfile.TemporaryDirectory() as tmp:
        for sid, body in drafts.items():
            (Path(tmp) / f"S{sid}.md").write_text(body, encoding="utf-8")
        text, story_map, _ = plan_join.join(skeleton, Path(tmp))
    return text, story_map


PLAN, JOIN_MAP = build(SKELETON, {"1": DRAFT_1, "2": DRAFT_2})


def parsed(text):
    """The plan as plan_premises gathers it, with no path ignored."""
    return gather(text, lambda name: False)


def line_of(text, wanted):
    """The 1-based line of the first line that is `wanted` once stripped."""
    return next(n for n, line in enumerate(text.splitlines(), 1) if line.strip() == wanted)


def git(root, *args):
    return subprocess.run(
        ["git", "-C", str(root), "-c", "user.name=t", "-c", "user.email=t@example.com",
         "-c", "commit.gpgsign=false", *args],
        check=True, capture_output=True, text=True, env=CLEAN_ENV,
    ).stdout.strip()


def write_files(root, files):
    for rel, body in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body, encoding="utf-8")


def seam_rows(text):
    """The packet's rows: `name | defined <cites> | used <cites>`."""
    return [line for line in text.splitlines() if " | defined " in line]


class RepoCase(unittest.TestCase):
    """A throwaway git repo holding the fixture files, the plan and its join map."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.tmp.cleanup)
        cls.root = Path(cls.tmp.name).resolve()
        write_files(cls.root, {"src/short.rs": SHORT, "src/rows.rs": ROWS, "src/quoted.rs": QUOTED})
        git(cls.root, "init", "-q")
        git(cls.root, "add", "src")
        git(cls.root, "commit", "-q", "-m", "fixture")
        cls.plan = cls.root / "docs" / "plans" / "plan.md"
        cls.drafts = cls.root / ".flow" / "drafts" / "plan"
        write_files(cls.root, {"docs/plans/plan.md": SKELETON})
        write_files(cls.drafts, {"S1.md": DRAFT_1, "S2.md": DRAFT_2})
        # The real join writes the plan and join.json, as /flow-draft does.
        with contextlib.redirect_stdout(io.StringIO()):
            assert plan_join.main([str(cls.plan), "--drafts", str(cls.drafts)]) == 0
        assert cls.plan.read_text(encoding="utf-8") == PLAN


class UnitPlanCase(RepoCase):
    """Plans with no join map, for the rules that read the plan's code."""

    def write_plan(self, name, text):
        path = self.root / "docs" / "plans" / f"{name}.md"
        write_files(self.root, {f"docs/plans/{name}.md": text})
        return path

    def regions(self, text):
        tree = Tree(self.root, "HEAD")
        regions = plan_packet.Regions(gather(text, tree.ignored_root), tree)
        regions.walk()
        return regions

    def rows(self, name, text):
        return seam_rows(plan_packet.seams_packet(self.write_plan(name, text), "HEAD", self.root / "no-drafts"))


class CiteTest(unittest.TestCase):
    # Story 1: heading 5, entry 7-8, draft lines 2-5 at 9-12. Story 2: heading 14, entry 16-17, draft lines 2-3 at 18-19.
    MAP = {"1": plan_join.Span(8, 5, (7, 8)), "2": plan_join.Span(17, 3, (16, 17))}

    def test_a_plan_line_inside_a_draft_cites_the_draft_line(self):
        self.assertEqual(plan_packet.cite(9, self.MAP), "S1.md:2")
        self.assertEqual(plan_packet.cite(12, self.MAP), "S1.md:5")
        self.assertEqual(plan_packet.cite(19, self.MAP), "S2.md:3")

    def test_a_line_the_join_wrote_from_an_entry_cites_the_entry(self):
        self.assertEqual(plan_packet.cite(7, self.MAP), "the Story 1 entry (plan:7)")
        # Plan line 8 is also `start`: the entry wins, as no draft line sits there.
        self.assertEqual(plan_packet.cite(8, self.MAP), "the Story 1 entry (plan:8)")
        self.assertEqual(plan_packet.cite(16, self.MAP), "the Story 2 entry (plan:16)")

    def test_a_plan_line_outside_every_draft_cites_the_plan(self):
        for line in (4, 5, 6, 13, 14, 15, 20):
            self.assertEqual(plan_packet.cite(line, self.MAP), f"plan:{line}")
        self.assertEqual(plan_packet.cite(7, {}), "plan:7")

class DefinitionLinesTest(unittest.TestCase):
    def test_a_wrapped_signature_runs_to_the_line_that_opens_the_body(self):
        texts = ["pub async fn open_ledger(", "    path: &str,", "    retries: u32,", ") -> Result<Ledger, Error> {", "    body();", "}"]
        self.assertEqual(plan_packet.definition_lines(texts, 0), ([], texts[:4]))

    def test_a_where_clause_is_part_of_the_signature(self):
        texts = ["fn f<T>(x: T) -> T", "where", "    T: Clone,", "{", "    x", "}"]
        self.assertEqual(plan_packet.definition_lines(texts, 0)[1], texts[:4])

    def test_a_one_line_definition_does_not_swallow_the_next_line(self):
        texts = ["def f(x): return x", "", "y = 1"]
        self.assertEqual(plan_packet.definition_lines(texts, 0)[1], ["def f(x): return x"])
        texts = ["def f(x): return x", "def g(y):", "    pass"]
        self.assertEqual(plan_packet.definition_lines(texts, 0)[1], ["def f(x): return x"])

    def test_a_struct_keeps_its_fields_to_the_closing_brace(self):
        texts = ["pub struct Wide {", "    pub a: u32,", "", "    pub b: (u32, u32),", "}", "fn after() {}"]
        self.assertEqual(plan_packet.definition_lines(texts, 0)[1], texts[:5])

    def test_a_python_docstring_below_the_def_is_taken(self):
        texts = ["def drafts_dir(", "    plan: Path,", ") -> Path:", '    """The drafts', "    of a plan.", '    """', "    return plan"]
        self.assertEqual(plan_packet.definition_lines(texts, 0)[1], texts[:6])
        one = ["class Tree:", '    """A fixture."""', "    x = 1"]
        self.assertEqual(plan_packet.definition_lines(one, 0)[1], one[:2])
        none = ["def f():", "    return 1"]
        self.assertEqual(plan_packet.definition_lines(none, 0)[1], ["def f():"])

    def test_a_run_longer_than_the_cap_is_cut_and_says_so(self):
        texts = ["pub struct Big {", *[f"    pub f{i}: u32," for i in range(30)], "}"]
        shown = plan_packet.definition_lines(texts, 0)[1]
        self.assertEqual(shown[: plan_packet.SIGNATURE_LINES_MAX], texts[: plan_packet.SIGNATURE_LINES_MAX])
        self.assertEqual(shown[-1], "...")
        self.assertEqual(len(shown), plan_packet.SIGNATURE_LINES_MAX + 1)

    def test_docs_above_are_capped_and_stop_at_a_line_that_is_not_one(self):
        many = [f"/// line {i}" for i in range(plan_packet.DOC_LINES_MAX + 3)]
        docs, shown = plan_packet.definition_lines(["x = 1", *many, "pub fn f() {}"], len(many) + 1)
        self.assertEqual(docs, many[-plan_packet.DOC_LINES_MAX :])
        self.assertEqual(shown, ["pub fn f() {}"])
        docs, _ = plan_packet.definition_lines(["/// not mine", "let x = 1;", "/// mine", "# also mine", "pub fn f() {}"], 4)
        self.assertEqual(docs, ["/// mine", "# also mine"])

    def test_fenced_outgrows_the_backticks_in_its_content(self):
        out = plan_packet.fenced(["```x", "y"])
        self.assertEqual(out, ["````", "```x", "y", "````"])
        self.assertEqual(plan_packet.fenced(["plain"], "text"), ["```text", "plain", "```"])


class UseAndDefinitionRulesTest(UnitPlanCase):
    def test_uses_in_commands_and_prose_blocks_do_not_count_but_an_untagged_block_does(self):
        used = plan_packet.identifiers(parsed(UNITS), "2")
        self.assertIn("real_def", used)
        self.assertIn("bare_def", used)  # a block with no language is code
        self.assertNotIn("Wide", used)  # only in a bash block
        self.assertNotIn("grep", plan_packet.identifiers(parsed(f"## Story 1: A\n\n{FENCE}markdown\ngrep x\n{FENCE}\n"), "1"))

    def test_a_word_after_a_dot_is_not_a_use(self):
        used = plan_packet.identifiers(parsed(f'## Story 1: A\n\n{FENCE}python\nx = "".join(parts)\nself.cite(1)\ncite(2)\n{FENCE}\n'), "1")
        self.assertNotIn("join", used)
        self.assertEqual(used["cite"], 6)
        self.assertNotIn("dotted_def", plan_packet.identifiers(parsed(UNITS), "2"))

    def test_definitions_come_from_added_code_outside_test_files_only(self):
        regions = self.regions(UNITS)
        found = plan_packet.definitions(parsed(UNITS), "1", regions.blocks)
        self.assertEqual(
            sorted(found),
            ["Wide", "bare_def", "dotted_def", "real_def", "redefined_def", "replacement_def", "wrapped_def"],
        )
        with_tests = plan_packet.definitions(parsed(UNITS), "1", regions.blocks, tests=True)
        self.assertIn("test_helper", with_tests)
        self.assertIn("Tree", with_tests)
        self.assertNotIn("quoted_anchor", with_tests)  # quoted from src/quoted.rs: text the step replaces
        self.assertIn("redefined_def", with_tests)  # "Replace `X`:" before code the tree lacks: the new code itself
        self.assertNotIn("md_def", with_tests)  # a markdown block

    def test_a_replace_intro_makes_an_anchor_only_of_text_the_tree_or_an_earlier_block_holds(self):
        regions = self.regions(UNITS)
        quoted = regions.blocks[line_of(UNITS, "fn quoted_anchor() {}") - 1]
        redefined = regions.blocks[line_of(UNITS, "fn redefined_def() {}") - 1]
        self.assertEqual((quoted.role, quoted.target), ("anchor", "src/quoted.rs"))
        self.assertEqual(redefined.role, "new")

    def test_an_anchor_may_quote_an_earlier_block_of_the_plan(self):
        text = textwrap.dedent(
            f"""\
            ## Story 1: A

            ### Task 1.1: A

            Add to `src/new.rs`:

            {FENCE}rust
            fn earlier_def() {{}}
            {FENCE}

            In `src/new.rs`, replace the line below, currently:

            {FENCE}rust
            fn earlier_def() {{}}
            {FENCE}

            Replace `later`:

            {FENCE}rust
            fn later_def() {{}}
            {FENCE}
            """
        )
        regions = self.regions(text)
        roles = [regions.blocks[n].role for n in sorted(regions.blocks)]
        self.assertEqual(roles, ["new", "anchor", "new"])
        self.assertEqual(sorted(plan_packet.definitions(parsed(text), "1", regions.blocks)), ["earlier_def", "later_def"])

    def test_test_file_paths(self):
        for path in ("a/x_test.py", "test_x.py", "a/tests/x.rs", "web/x.test.ts", "web/y.spec.ts"):
            self.assertTrue(plan_packet.is_test_path(path), path)
        for path in ("src/x.py", "src/contest.py", "src/testing.rs", "x.ts"):
            self.assertFalse(plan_packet.is_test_path(path), path)

    def test_seams_list_only_the_names_another_story_really_uses(self):
        rows = self.rows("units", UNITS)
        # Story 3 defines `real_def` itself; Story 2 is the one user left.
        self.assertEqual([row.split(" | ")[0] for row in rows],
                         ["bare_def", "real_def", "redefined_def", "replacement_def", "wrapped_def"])
        row = next(row for row in rows if row.startswith("real_def "))
        self.assertEqual(row, f"real_def | defined plan:{line_of(UNITS, 'pub fn real_def() {}')} | used plan:{line_of(UNITS, 'real_def();')}")

    def test_seams_cross_typescript_stories(self):
        use = line_of(TS_PLAN, 'import { formatPrice, PriceRow } from "./price";')
        self.assertEqual(self.rows("ts", TS_PLAN), [
            f"PriceRow | defined plan:{line_of(TS_PLAN, 'export interface PriceRow {')} | used plan:{use}",
            f"formatPrice | defined plan:{line_of(TS_PLAN, 'export function formatPrice(cents: number): string {')} | used plan:{use}",
        ])

    def test_seams_list_what_a_declared_pairs_first_story_gives_its_second(self):
        plan = self.write_plan("ts-paired", "**Parallel:** 1+2\n\n" + TS_PLAN)
        packet = plan_packet.seams_packet(plan, "HEAD", self.root / "no-drafts")
        self.assertIn("## Declared pairs", packet)
        self.assertIn("- 1+2: names Story 1 defines that Story 2 uses: PriceRow, formatPrice", packet)

    def test_seams_have_no_pairs_section_without_the_line(self):
        packet = plan_packet.seams_packet(self.write_plan("ts-unpaired", TS_PLAN), "HEAD", self.root / "no-drafts")
        self.assertNotIn("## Declared pairs", packet)

    def test_seams_cross_python_stories(self):
        use = line_of(PY_PLAN, "from app.rows import RowError, load_rows")
        self.assertEqual(self.rows("py", PY_PLAN), [
            f"RowError | defined plan:{line_of(PY_PLAN, 'class RowError(Exception):')} | used plan:{use}",
            f"load_rows | defined plan:{line_of(PY_PLAN, 'def load_rows(path):')} | used plan:{use}",
        ])


class SeamsTest(RepoCase):
    def seams(self):
        return plan_packet.seams_packet(self.plan, "HEAD", self.drafts)

    def test_a_shared_name_is_listed_with_its_definition_and_use(self):
        text = self.seams()
        k = line_of(PLAN, "pub fn lookup_price(id: u32) -> Option<u32> {") - JOIN_MAP["1"].start + 1
        j = line_of(PLAN, "lookup_price(7)") - JOIN_MAP["2"].start + 1
        # `private_helper` is defined in Story 1 and named in Story 2 only after a dot and in a
        # command; `show` is defined and used nowhere else.
        self.assertEqual(seam_rows(text), [f"lookup_price | defined S1.md:{k} | used S2.md:{j}"])
        lines = text.splitlines()
        self.assertEqual(lines[1], f"Packet length: {len(lines)} lines")

    def test_the_header_carries_the_seams_readers_rules(self):
        self.assertIn(plan_packet.SEAMS_READING_RULES, self.seams())

    def test_every_stories_files_line_follows_the_rows(self):
        text = self.seams()
        self.assertIn("- Story 1: `src/rows.rs`, `src/short.rs`, `src/new.rs`", text)
        self.assertIn("- Story 2: `src/short.rs`", text)


class PremisesSectionTest(RepoCase):
    def premises(self, files):
        """The packet's section of unchecked premises, with `files` written under the drafts' premises/ (then removed)."""
        made = []
        for name, body in files.items():
            path = self.drafts / "premises" / name
            path.parent.mkdir(exist_ok=True)
            path.write_text(body)
            made.append(path)
        try:
            text = plan_packet.seams_packet(self.plan, "HEAD", self.drafts)
        finally:
            for path in made:
                path.unlink()
        return text.split("## Premises the drafters couldn't check", 1)[1].split("## Files line of every story", 1)[0]

    def test_each_storys_premises_are_listed_in_story_order(self):
        section = self.premises({
            "S2.md": "- S2.md:3: Story 1's lookup answers None for an unknown id (Story 1)\n",
            "S1.md": "none\n",
        })
        self.assertIn(plan_packet.PREMISES_RULE, section)
        self.assertIn("### Story 2", section)
        self.assertIn("- S2.md:3: Story 1's lookup answers None for an unknown id (Story 1)", section)
        self.assertNotIn("### Story 1", section)

    def test_no_premises_say_so(self):
        self.assertIn("(none: no premises/S<id>.md names one)", self.premises({}))
        self.assertIn("(none: no premises/S<id>.md names one)", self.premises({"S1.md": "- none\n"}))

    def test_a_premises_file_is_no_draft_to_the_join(self):
        (self.drafts / "premises").mkdir(exist_ok=True)
        (self.drafts / "premises" / "S1.md").write_text("none\n")
        try:
            out, _, _ = plan_join.join(self.plan.read_text(encoding="utf-8"), self.drafts)
        finally:
            (self.drafts / "premises" / "S1.md").unlink()
        self.assertEqual(out, PLAN)


class CliTest(RepoCase):
    def run_cli(self, *args, plan=None):
        # A FLOW_HOME with no profiles, so the default drafts directory is the plan's own.
        env = {**CLEAN_ENV, "FLOW_HOME": str(self.root / "no-flow-home")}
        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(plan or self.plan), *args], capture_output=True, text=True, env=env
        )
        return result.returncode, result.stdout, result.stderr

    def copy_drafts(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        copy = Path(tmp.name) / "drafts"
        shutil.copytree(self.drafts, copy, ignore=shutil.ignore_patterns("packets"))
        return copy

    def test_seams_writes_the_packet_into_the_plans_drafts_directory(self):
        packet = self.drafts / "packets" / "seams.md"
        packet.unlink(missing_ok=True)
        code, out, err = self.run_cli("--seams")
        self.assertTrue(out.startswith(f"wrote {packet}: "), out + err)
        self.assertEqual(code, 0, out + err)
        text = packet.read_text(encoding="utf-8")
        self.assertEqual(out.strip(), f"wrote {packet}: {len(text.splitlines())} lines")
        self.assertIn(" | defined S1.md:", text)
        # The rev is resolved once and printed as the commit it names.
        self.assertIn(f"Written against the tree at {git(self.root, 'rev-parse', 'HEAD')}.", text)

    def test_without_a_join_map_a_definition_is_cited_to_the_plan(self):
        elsewhere = self.root / "elsewhere"
        code, out, err = self.run_cli("--seams", "--drafts", str(elsewhere))
        self.assertEqual(code, 0, out + err)
        n = line_of(PLAN, "pub fn lookup_price(id: u32) -> Option<u32> {")
        text = (elsewhere / "packets" / "seams.md").read_text(encoding="utf-8")
        self.assertIn(f"lookup_price | defined plan:{n} | used plan:", text)

    def test_rev_reads_the_tree_at_that_commit(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            write_files(root, {"src/a.rs": "fn old_name() {}\n", "docs/plans/plan.md": REV_PLAN})
            git(root, "init", "-q")
            git(root, "add", "src")
            git(root, "commit", "-q", "-m", "one")
            write_files(root, {"src/a.rs": "fn other() {}\n"})
            git(root, "add", "src")
            git(root, "commit", "-q", "-m", "two")
            plan, out_dir = root / "docs" / "plans" / "plan.md", root / "out"
            # At HEAD~1 the "currently" block is in src/a.rs, so it defines nothing; at HEAD it is new code.
            for rev, names in (("HEAD~1", ["new_name"]), ("HEAD", ["new_name", "old_name"])):
                code, out, err = self.run_cli("--seams", "--rev", rev, "--drafts", str(out_dir), plan=plan)
                self.assertEqual(code, 0, out + err)
                text = (out_dir / "packets" / "seams.md").read_text(encoding="utf-8")
                self.assertEqual([row.split(" | ")[0] for row in seam_rows(text)], names, rev)
                self.assertIn(f"Written against the tree at {git(root, 'rev-parse', rev)}.", text)

    def test_usage_errors_exit_two(self):
        for args in ([], ["--bogus"], ["--rev", "HEAD"], ["--story", "1"]):
            code, out, err = self.run_cli(*args)
            self.assertEqual(code, 2, f"{args}: {out}{err}")
        code, out, err = self.run_cli("--seams", plan=self.root / "docs" / "nope.md")
        self.assertEqual(code, 2, out + err)

    def test_a_rev_that_is_no_commit_exits_two(self):
        code, out, err = self.run_cli("--seams", "--rev", "no-such-rev")
        self.assertEqual(code, 2, out + err)
        self.assertIn("--rev no-such-rev is not a commit", err)

    def test_a_malformed_join_map_fails_the_packet(self):
        drafts = self.copy_drafts()
        (drafts / "join.json").write_text("{", encoding="utf-8")
        code, out, err = self.run_cli("--seams", "--drafts", str(drafts))
        self.assertEqual(code, 1, out + err)
        self.assertEqual(out, f"FAIL [packet] {self.plan}: {drafts / 'join.json'} is not a join map; re-run loom flow join\n")

    def test_a_plan_out_of_date_with_its_drafts_is_refused(self):
        drafts = self.copy_drafts()
        with (drafts / "S2.md").open("a", encoding="utf-8") as handle:
            handle.write("\nA line added after the join.\n")
        code, out, err = self.run_cli("--seams", "--drafts", str(drafts))
        self.assertEqual(code, 1, out + err)
        self.assertEqual(out, f"FAIL [packet] {self.plan}: the plan is out of date with its drafts; re-run loom flow join\n")
        self.assertFalse((drafts / "packets").exists())

    def test_a_skeleton_only_edit_that_shifts_the_starts_is_refused(self):
        # A line added above the marker still joins to itself, but every story now starts a line later.
        edited = self.root / "docs" / "plans" / "edited.md"
        edited.write_text(PLAN.replace(f"\n{plan_join.MARKER}\n", f"\nAn amended contract line.\n{plan_join.MARKER}\n", 1))
        code, out, err = self.run_cli("--seams", "--drafts", str(self.drafts), plan=edited)
        self.assertEqual(code, 1, out + err)
        self.assertEqual(out, f"FAIL [packet] {edited}: the plan is out of date with its drafts; re-run loom flow join\n")

    def test_a_plan_that_cannot_be_decoded_fails_the_packet(self):
        bad = self.root / "docs" / "plans" / "bad.md"
        bad.write_bytes(b"## Story 1: \xff\xfe\n")
        code, out, err = self.run_cli("--seams", plan=bad)
        self.assertEqual(code, 1, out + err)
        self.assertTrue(out.startswith(f"FAIL [packet] {bad}: "), out)

    def test_a_packet_that_cannot_be_written_fails_the_packet(self):
        blocker = self.root / "blocker"
        blocker.write_text("a file where the drafts directory should be\n", encoding="utf-8")
        code, out, err = self.run_cli("--seams", "--drafts", str(blocker))
        self.assertEqual(code, 1, out + err)
        self.assertTrue(out.startswith(f"FAIL [packet] {self.plan}: "), out)

    def test_a_profile_flowconf_refuses_fails_the_packet(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            write_files(root, {"src/a.rs": "fn a() {}\n", ".claude/flow.toml": "schema = 2\n", "docs/plans/plan.md": REV_PLAN})
            git(root, "init", "-q")
            git(root, "add", "src")
            git(root, "commit", "-q", "-m", "one")
            plan = root / "docs" / "plans" / "plan.md"
            code, out, err = self.run_cli("--seams", plan=plan)
            self.assertEqual(code, 1, out + err)
            self.assertTrue(out.startswith(f"FAIL [packet] {plan}: "), out)
            self.assertIn("schema: 2", out)


if __name__ == "__main__":
    unittest.main()
