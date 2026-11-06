"""Tests for plan_conformance.py. Run from lib/: python3 -m unittest -q plan_conformance_test.py"""

import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("plan_conformance.py")
# A git hook's GIT_* variables would point the temp repos at the outer repository.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
FENCE = "```"

PLAN = textwrap.dedent(
    f"""\
    # Plan

    **Spec:** `docs/spec.md`

    ## Story 1: Store

    - **Developer:** junior-dev
    - **Files:** `crate/src/lib.rs`, `crate/src/new.rs`
    - **Interfaces:** none
    - **Verify:** `cargo test`

    ### Task 1.1: Do things

    **Files:**
    - Modify: `crate/src/lib.rs`
    - Create: `crate/src/new.rs`

    In `crate/src/lib.rs`, current:

    {FENCE}rust
    pub fn old() -> u32 {{
        1
    }}
    {FENCE}

    Replace with:

    {FENCE}rust
    pub fn old() -> u32 {{
        2
    }}
    {FENCE}

    In `crate/src/lib.rs`, append:

    {FENCE}rust
    pub fn long_function_name(argument_one: u32, argument_two: u32) -> u32 {{ argument_one + argument_two }}
    {FENCE}

    Create `crate/src/new.rs`:

    {FENCE}rust
    //! The new module.

    pub fn first() -> u32 {{
        1
    }}

    pub fn second() -> u32 {{
        2
    }}

    pub fn third() -> u32 {{
        3
    }}
    {FENCE}

    Run:

    {FENCE}bash
    cargo test -p crate
    {FENCE}
    """
)

BASE_LIB = "pub fn old() -> u32 {\n    1\n}\n"
HEAD_LIB = textwrap.dedent(
    """\
    pub fn old() -> u32 {
        2
    }
    pub fn long_function_name(argument_one: u32, argument_two: u32) -> u32 {
        argument_one + argument_two
    }

    // Glue the plan never gave.
    pub fn glue() {}
    """
)
HEAD_NEW = textwrap.dedent(
    """\
    //! The new module.

    pub fn first() -> u32 {
        1
    }

    pub fn second() -> u32 {
        22
    }

    pub fn third() -> u32 {
        3
    }
    """
)


class PlanConformanceTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        # No test reads the real home store: FLOW_HOME points at a directory with no profiles.
        self.home = tempfile.TemporaryDirectory()
        self.env = {**CLEAN_ENV, "FLOW_HOME": self.home.name}
        self.write({"crate/src/lib.rs": BASE_LIB, "Cargo.lock": "a\n", "docs/spec.md": "# Spec\n", "docs/plan.md": PLAN,
                    ".claude/flow.toml": 'schema = 1\ngenerated = ["*.gen.rs"]\n'})
        self.git("init", "-q")
        self.commit("base")
        self.base = self.rev()

    def tearDown(self):
        self.tmp.cleanup()
        self.home.cleanup()

    def write(self, files):
        for path, text in files.items():
            (self.root / path).parent.mkdir(parents=True, exist_ok=True)
            (self.root / path).write_text(text)

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root), *args], check=True, capture_output=True, text=True,
                              env=CLEAN_ENV).stdout

    def commit(self, message):
        self.git("add", "-A")
        self.git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", message)

    def rev(self):
        return self.git("rev-parse", "HEAD").strip()

    def run_script(self, *args):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(self.root / "docs/plan.md"), "--story", "1", "--base", self.base, *args],
            capture_output=True, text=True, env=self.env,
        )
        return result.returncode, result.stdout

    def test_maps_the_story_onto_the_plan(self):
        self.write({"crate/src/lib.rs": HEAD_LIB, "crate/src/new.rs": HEAD_NEW, "Cargo.lock": "b\n", "docs/notes.md": "n\n"})
        self.commit("story 1")
        code, out = self.run_script()
        self.assertEqual(code, 1, out)
        # The replacement matches line for line; the rustfmt-wrapped append matches squeezed.
        self.assertIn("Plan code: 2 blocks landed as written, 1 with changes, 0 not at all", out)
        self.assertIn("crate/src/new.rs: 9 of 10 lines landed as written", out)
        # Left to read: the glue in lib.rs and the one changed line in new.rs.
        self.assertIn("crate/src/lib.rs: 8-9", out)
        self.assertIn("crate/src/new.rs: 8", out)
        self.assertIn("Generated, not listed: Cargo.lock (1 lines)", out)
        self.assertIn("doesn't name: docs/notes.md", out)
        self.assertNotIn("cargo test", out)

    def test_a_marker_in_an_uncovered_line_is_listed(self):
        # loom flow run gives a vouched story the full review when one is there (flow in loom part 3b).
        self.write({".claude/flow.toml": 'schema = 1\n[landmines]\nmarkers = ["first()", "glue()", "absent("]\n'})
        self.commit("markers")
        self.base = self.rev()
        self.write({"crate/src/lib.rs": HEAD_LIB, "crate/src/new.rs": HEAD_NEW})
        self.commit("story 1")
        _, out = self.run_script()
        self.assertIn("MARKERS: glue()\n", out, "only the uncovered glue holds one; first() is in a covered block")

    def test_no_marker_line_without_one(self):
        self.write({"crate/src/lib.rs": HEAD_LIB, "crate/src/new.rs": HEAD_NEW})
        self.commit("story 1")
        _, out = self.run_script()
        self.assertNotIn("MARKERS:", out)

    def test_a_file_matching_a_generated_glob_is_counted_not_listed(self):
        self.write({"crate/src/lib.rs": HEAD_LIB, "crate/src/new.rs": HEAD_NEW, "crate/src/schema.gen.rs": "one\ntwo\n"})
        self.commit("story 1")
        _, out = self.run_script()
        self.assertIn("crate/src/schema.gen.rs (2 lines)", out)
        self.assertNotIn("  crate/src/schema.gen.rs:", out)

    def test_a_repo_without_a_profile_is_a_usage_error(self):
        (self.root / ".claude" / "flow.toml").unlink()
        code, _ = self.run_script()
        self.assertEqual(code, 2)

    def test_a_story_built_exactly_as_planned_passes(self):
        exact_new = HEAD_NEW.replace("22", "2")
        exact_lib = HEAD_LIB.split("\n\n// Glue")[0] + "\n"
        self.write({"crate/src/lib.rs": exact_lib, "crate/src/new.rs": exact_new})
        self.commit("story 1")
        code, out = self.run_script()
        self.assertEqual(code, 0, out)
        self.assertIn("3 blocks landed as written, 0 with changes, 0 not at all", out)
        self.assertIn("(read these): 0 in 0 files", out)

    def test_an_unknown_story_is_a_usage_error(self):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(self.root / "docs/plan.md"), "--story", "9", "--base", self.base],
            capture_output=True, text=True, env=self.env,
        )
        self.assertEqual(result.returncode, 2)

    def test_go_sum_and_flake_lock_are_common_lockfiles(self):
        sys.path.insert(0, str(SCRIPT.parent))
        from plan_conformance import is_generated

        for path in ("go.sum", "tools/go.sum", "flake.lock"):
            self.assertTrue(is_generated(path, ()), path)
        self.assertFalse(is_generated("go.mod", ()))


if __name__ == "__main__":
    unittest.main()
