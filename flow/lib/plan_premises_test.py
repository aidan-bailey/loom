"""Tests for plan_premises.py. Run from lib/: python3 -m unittest -q plan_premises_test.py"""

import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("plan_premises.py")
# A git hook's GIT_* variables would point the temp repos at the outer repository.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}

LIB_RS = textwrap.dedent(
    """\
    pub mod a;
    pub mod b;

    pub fn existing() -> u32 {
        1
    }

    #[cfg(test)]
    mod tests {
        const T0: u64 = 5;
        #[test]
        fn it_works() {}
    }
    """
)

FENCE = "```"

PLAN = textwrap.dedent(
    f"""\
    # Plan

    **Spec:** `docs/README.md`

    ## Story 1: Store

    - **Developer:** junior-dev
    - **Files:** `crate/src/lib.rs`, `crate/src/new.rs`
    - **Interfaces:** none
    - **Verify:** `cargo test`

    ### Task 1.1: Do things

    **Files:**
    - Modify: `crate/src/lib.rs:4-6`, `:40` (past the end)
    - Create: `crate/src/new.rs`, `crate/src/a.rs`
    - Test: `crate/src/gone.rs`

    In `crate/src/lib.rs`, current:

    {FENCE}rust
    pub fn existing() -> u32 {{
        1
    }}
    {FENCE}

    Replace with:

    {FENCE}rust
    pub fn existing() -> u32 {{
        2
    }}

    pub fn existing_too() -> u32 {{
        3
    }}
    {FENCE}

    In `crate/src/lib.rs`'s test module, append:

    {FENCE}rust
        const T0: u64 = 5;
    {FENCE}

    Current (lines 2-3):

    {FENCE}rust
    pub mod a;
    ...
    pub fn existing() -> u32 {{
    {FENCE}

    In `crate/src/lib.rs`, current:

    {FENCE}rust
    pub fn vanished() {{}}
    {FENCE}

    In `docs/README.md`, current fragment:

    {FENCE}markdown
    the fourteen tools fragment
    {FENCE}

    Prose names `crate/src/new.rs`, `src/a.rs:2`, `crate/src/missing.rs`, `registry-1.0.0/src/x.rs` and `target/debug/x.rs`.

    ## Story 2: Later

    - **Developer:** junior-dev
    - **Files:** `crate/src/new.rs`
    - **Interfaces:** none
    - **Verify:** `cargo test`

    ### Task 2.1: Uses Story 1's text

    In `crate/src/lib.rs`, current:

    {FENCE}rust
    pub fn existing_too() -> u32 {{
        3
    }}
    {FENCE}
    """
)


class PlanPremisesTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        files = {
            "crate/src/lib.rs": LIB_RS,
            "crate/src/a.rs": "pub fn a() {}\npub fn b() {}\n",
            "docs/README.md": "# Readme\nOne long line with the fourteen tools fragment inside it.\n",
            "Cargo.toml": "[workspace]\n",
            ".gitignore": "target/\nbuild/\n",
        }
        for path, text in files.items():
            (self.root / path).parent.mkdir(parents=True, exist_ok=True)
            (self.root / path).write_text(text)
        self.git("init", "-q")
        self.git("add", "-A")
        self.git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
        self.plan = self.root / "docs" / "plan.md"
        self.plan.write_text(PLAN)

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, *args):
        subprocess.run(["git", "-C", str(self.root), *args], check=True, capture_output=True, env=CLEAN_ENV)

    def run_script(self, *args):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(self.plan), *args], capture_output=True, text=True, env=CLEAN_ENV
        )
        return result.returncode, result.stdout

    def test_flags_each_kind_of_broken_premise(self):
        code, out = self.run_script()
        self.assertEqual(code, 1, out)
        self.assertIn("lines    `crate/src/lib.rs:40` but crate/src/lib.rs has 13 lines", out)
        self.assertIn("exists   `crate/src/a.rs` is to be created but already exists", out)
        self.assertIn("missing  `crate/src/gone.rs` does not exist", out)
        self.assertIn("defined  adds `T0` (:10), already defined in crate/src/lib.rs", out)
        self.assertIn("current  block starting `pub fn vanished() {}` is not in crate/src/lib.rs", out)
        self.assertIn("missing  `crate/src/missing.rs` does not exist", out)
        self.assertIn("Story 1 (plan:5): ", out)
        self.assertIn("6 flagged", out)

    def test_premises_that_hold_are_not_flagged(self):
        _, out = self.run_script()
        flags = "\n".join(line for line in out.splitlines() if line.startswith("  plan:"))
        # The replaced function, the new one beside it, the elided block, the prose
        # fragment, a planned file, a suffix path and an outside path all hold.
        for quiet in ("existing_too", "pub mod a", "fourteen", "crate/src/new.rs", "`src/a.rs:2`", "registry-1.0.0", "target/"):
            self.assertNotIn(quiet, flags, quiet)
        self.assertIn("1 current blocks away from the lines the plan names", out)
        self.assertIn("1 prose paths outside the repo's directories", out)

    def test_current_text_an_earlier_step_adds_is_not_flagged(self):
        code, out = self.run_script("--stories", "2")
        self.assertEqual(code, 0, out)
        self.assertIn("Story 2 (plan:", out)
        self.assertNotIn("Story 1", out)
        self.assertIn("1 quoting text an earlier step adds", out)

    def test_rev_reads_the_tree_at_a_commit(self):
        # Lengthen the file in the working tree only: line 40 exists there, not at HEAD.
        (self.root / "crate/src/lib.rs").write_text(LIB_RS + "\n" * 40)
        _, working = self.run_script("--stories", "1")
        _, at_head = self.run_script("--stories", "1", "--rev", "HEAD")
        self.assertNotIn("`crate/src/lib.rs:40`", working)
        self.assertIn("`crate/src/lib.rs:40`", at_head)

    def test_a_file_under_a_directory_git_ignores_is_never_flagged(self):
        # The fixture's .gitignore names build/ and not out/: each repo's ignore rules decide,
        # not a list of build directories in the script.
        files = "\n- Modify: `build/gen.rs`\n- Modify: `out/gen.rs`\n"
        heading = "### Task 2.1: Uses Story 1's text\n"
        self.plan.write_text(PLAN.replace(heading, heading + files))
        _, out = self.run_script("--stories", "2")
        self.assertNotIn("build/gen.rs", out)
        self.assertIn("missing  `out/gen.rs` does not exist", out)

    def test_a_current_block_whose_intro_names_an_ignored_root_falls_back_to_the_task_file(self):
        # `target/` is build output: the intro's path is no premise, so the block is looked up in the
        # task's own file, where it is.
        self.plan.write_text(textwrap.dedent(
            f"""\
            # Plan

            ## Story 1: One

            - **Developer:** junior-dev
            - **Files:** `crate/src/lib.rs`
            - **Interfaces:** none
            - **Verify:** `cargo test`

            ### Task 1.1: Look

            - Modify: `crate/src/lib.rs`

            In `target/debug/gen.rs`, current:

            {FENCE}rust
            pub fn existing() -> u32 {{
                1
            }}
            {FENCE}
            """
        ))
        code, out = self.run_script()
        self.assertEqual(code, 0, out)
        self.assertNotIn("target/", out)

    def test_a_directory_with_tracked_files_is_not_ignored_whatever_its_rules_say_of_the_rest(self):
        # `docs/*` with `!docs/plans/` ignores everything in docs/ but the plans: the probe on
        # `docs/x` alone would call the whole directory build output.
        (self.root / ".gitignore").write_text("target/\nbuild/\ndocs/*\n!docs/plans/\n")
        (self.root / "docs/plans").mkdir()
        (self.root / "docs/plans/a.md").write_text("# A\n")
        self.git("add", "-A")
        self.git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "plans")
        heading = "### Task 2.1: Uses Story 1's text\n"
        self.plan.write_text(PLAN.replace(heading, heading + "\n- Modify: `docs/plans/gone.md`\n"))
        _, out = self.run_script("--stories", "2")
        self.assertIn("missing  `docs/plans/gone.md` does not exist", out)

    def test_a_missing_plan_is_a_usage_error(self):
        self.plan.unlink()
        code, _ = self.run_script()
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
