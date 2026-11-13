"""Tests for doctor.py and `flowconf.py doctor`. Run from lib/: python3 -m unittest -q doctor_test.py"""

import os
import shlex
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import doctor  # noqa: E402

CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
GUARD = Path(__file__).with_name("git_guard.py")
FLOWCONF = Path(__file__).with_name("flowconf.py")


class ChecksTest(unittest.TestCase):
    def test_programs_of_names_every_program_a_gate_runs(self):
        cases = {
            "cargo nextest run {packages}": ["cargo-nextest"],
            "cargo +nightly fmt --all -- --check": ["rustup", "cargo-fmt"],
            "cargo clippy {packages} -- -D warnings": ["cargo-clippy"],
            "cargo test --workspace": ["cargo"],
            "RUST_LOG=off uv run pytest": ["uv"],
            "python3 a.py && diff -u x y": ["python3", "diff"],
            """bash -c 'cargo clippy -- -D warnings && cargo nextest run --workspace && RUSTDOCFLAGS="-D warnings" cargo doc --no-deps'""": [
                "bash",
                "cargo-clippy",
                "cargo-nextest",
                "cargo",
            ],
            """bash -c 'for m in a b; do [ -e "$m" ] || continue; cargo hack check 2>/dev/null || exit 1; done'""": [
                "bash",
                "cargo-hack",
            ],
            "bash -c 'cd py && uv sync && uv run pytest tests/ -v'": ["bash", "uv"],
            "cargo +1.91 check 2>&1 | tee x": ["rustup", "cargo", "tee"],
        }
        for gate, programs in cases.items():
            self.assertEqual(doctor.programs_of(gate), programs, gate)

    def test_programs_of_is_none_when_it_cannot_tell(self):
        for gate in (
            "echo 'unbalanced",
            "bash -c '$TOOL run'",
            "bash -c 'case x in y) true;; esac'",
            "bash -c 'for m in a b'",
        ):
            self.assertIsNone(doctor.programs_of(gate), gate)

    def test_missing_programs_lists_only_the_absent_ones(self):
        self.assertEqual(doctor.missing_programs("", ["sh", "no-such-program-xyz"]), ["no-such-program-xyz"])

    def test_a_failing_prefix_is_reported(self):
        self.assertIsNotNone(doctor.prefix_works("false"))
        self.assertIsNone(doctor.prefix_works(""))

    def test_a_prefix_that_cannot_start_is_reported_not_raised(self):
        self.assertIsNotNone(doctor.prefix_works("no-such-prefix-xyz"))
        self.assertIsNotNone(doctor.prefix_works("'unbalanced"))
        self.assertIsNone(doctor.missing_programs("no-such-prefix-xyz", ["sh"]))
        self.assertIsNone(doctor.missing_programs("'unbalanced", ["sh"]))


class CopyLineTest(unittest.TestCase):
    def test_no_line_without_the_installed_roster(self):
        with mock.patch.dict(os.environ, {"FLOW_ROSTER": "loom flow roster aaaaaaaaaaaa"}, clear=False):
            os.environ.pop("FLOW_INSTALLED_ROSTER", None)
            self.assertIsNone(doctor.copy_line())

    def test_pass_when_the_copies_match(self):
        same = {"FLOW_ROSTER": "loom flow roster aaaaaaaaaaaa", "FLOW_INSTALLED_ROSTER": "loom flow roster aaaaaaaaaaaa"}
        with mock.patch.dict(os.environ, same):
            self.assertEqual(doctor.copy_line(), "PASS flow copy")

    def test_warn_naming_both_when_they_differ(self):
        differ = {"FLOW_ROSTER": "loom flow roster aaaaaaaaaaaa", "FLOW_INSTALLED_ROSTER": "loom flow roster bbbbbbbbbbbb"}
        with mock.patch.dict(os.environ, differ):
            line = doctor.copy_line()
        self.assertTrue(line.startswith("WARN flow copy: this session runs loom flow roster aaaaaaaaaaaa, "
                                        "the installed loom has loom flow roster bbbbbbbbbbbb:"), line)


class DoctorTest(unittest.TestCase):
    """`flowconf.py doctor` in a repo, with a flow root (FLOW_ROOT) holding the guard, so a FAIL in a test
    comes only from the check the test names."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        base = Path(os.path.realpath(self.tmp.name))
        self.repo = base / "repo"
        self.repo.mkdir()
        subprocess.run(["git", "-C", str(self.repo), "init", "-q"], check=True, env=CLEAN_ENV)
        self.flow = base / "flow"
        (self.flow / "plugin").mkdir(parents=True)
        (self.flow / "ROSTER").write_text("loom flow roster 0123456789ab\n")
        self.guard = self.flow / "lib" / "git_guard.py"
        self.guard.parent.mkdir()
        self.guard.symlink_to(GUARD)
        self.env = {**CLEAN_ENV, "FLOW_ROOT": str(self.flow)}

    def tearDown(self):
        self.tmp.cleanup()

    def profile(self, text: str) -> None:
        (self.repo / ".claude").mkdir(exist_ok=True)
        (self.repo / ".claude" / "flow.toml").write_text(text)

    def doctor(self) -> tuple[int, str]:
        result = subprocess.run([sys.executable, str(FLOWCONF), "doctor"], cwd=self.repo, capture_output=True,
                                text=True, env=self.env)
        return result.returncode, result.stdout + result.stderr

    def test_a_ready_repo_passes(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("doctor: 0 FAIL", out)

    def test_doc_paths_pass_when_a_tracked_file_matches_and_warn_when_none_does(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        _, out = self.doctor()
        self.assertIn("WARN doc paths: no tracked file is a doc path (`*.md`, less the generated files)", out)
        (self.repo / "README.md").write_text("x\n")
        subprocess.run(["git", "-C", str(self.repo), "add", "README.md"], check=True, env=CLEAN_ENV)
        _, out = self.doctor()
        self.assertIn("PASS doc paths: `*.md`", out)

    def test_a_gate_table_is_checked_by_its_command(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\nfinal = [{ run = "no-such-program-xyz", locks = ["port:4300"] }]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL gate programs: not found: no-such-program-xyz", out)

    def test_a_gate_program_found_only_inside_the_prefix_passes(self):
        bin_dir = self.repo / "tool-bin"
        bin_dir.mkdir()
        stub = bin_dir / "only-in-prefix-xyz"
        stub.write_text("#!/bin/sh\nexit 0\n")
        stub.chmod(0o755)
        self.assertNotIn(str(bin_dir), self.env.get("PATH", "").split(os.pathsep))
        prefix = f"env PATH={shlex.quote(str(bin_dir))}:{os.environ['PATH']}"
        self.profile(f'schema = 1\nprefix = "{prefix}"\n[gates]\nstory = ["only-in-prefix-xyz --check"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("PASS gate programs", out)

    def test_a_gate_it_cannot_parse_fails_naming_it(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true", "echo \'unbalanced"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL gate programs:", out)
        self.assertIn("echo 'unbalanced", out)

    def test_a_prefix_whose_program_is_absent_fails(self):
        self.profile('schema = 1\nprefix = "no-such-prefix-xyz"\n[gates]\nstory = ["sh -c true"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL prefix:", out)
        self.assertNotIn("Traceback", out)

    def test_a_gate_program_missing_inside_the_prefix_fails(self):
        self.profile('schema = 1\n[gates]\nstory = ["no-such-program-xyz --check"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL gate programs: not found: no-such-program-xyz", out)
        self.assertIn("PASS guard", out)
        self.assertIn("PASS profile", out)

    def test_a_gate_program_missing_inside_a_bash_chain_fails(self):
        self.profile("schema = 1\n[gates]\nstory = [\"bash -c 'true && no-such-program-xyz --flag'\"]\n")
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL gate programs: not found: no-such-program-xyz", out)

    def test_a_failing_prefix_fails(self):
        self.profile('schema = 1\nprefix = "false"\n[gates]\nstory = ["sh -c true"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL prefix:", out)

    def test_an_unwritable_state_directory_fails(self):
        blocker = self.repo / "afile"
        blocker.write_text("x")
        self.profile(f'schema = 1\n[gates]\nstory = ["sh -c true"]\n[paths]\nstate = "{blocker}/state"\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL state:", out)

    def test_a_profile_without_story_gates_fails(self):
        self.profile("schema = 1\n")
        _, out = self.doctor()
        self.assertIn("FAIL story gates:", out)

    def test_a_missing_profile_fails(self):
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL profile: no flow profile", out)

    def test_a_missing_guard_fails(self):
        self.guard.unlink()
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL guard:", out)

    def test_a_missing_flow_root_fails(self):
        self.env["FLOW_ROOT"] = str(self.repo / "nowhere")
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL flow root:", out)
        self.assertIn("no plugin, lib, ROSTER", out)

    def test_a_flow_root_passes(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        _, out = self.doctor()
        self.assertIn("PASS flow root", out)

    def track(self, rel: str) -> None:
        """A file git tracks in the repo: staged is enough for `git ls-files`."""
        path = self.repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("x\n")
        subprocess.run(["git", "-C", str(self.repo), "add", "--", rel], check=True, env=CLEAN_ENV)

    def test_the_default_drafts_directory_passes_and_doctor_makes_nothing(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n')
        exclude = self.repo / ".git" / "info" / "exclude"
        before = exclude.read_text() if exclude.is_file() else None
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("PASS drafts", out)
        self.assertIn("PASS design registries", out)
        self.assertFalse((self.repo / ".flow").exists())
        self.assertEqual(exclude.read_text() if exclude.is_file() else None, before)

    def test_a_drafts_directory_whose_first_directory_holds_tracked_files_fails(self):
        self.track("docs/a.md")
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[paths]\ndrafts = "docs/drafts"\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL drafts:", out)
        self.assertIn("docs/ holds tracked files", out)

    def test_a_drafts_directory_the_repo_ignores_passes_beside_tracked_files(self):
        self.track("docs/a.md")
        (self.repo / ".gitignore").write_text("docs/drafts/\n")
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[paths]\ndrafts = "docs/drafts"\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("PASS drafts", out)

    def test_a_rule_covering_a_plans_drafts_directory_passes_though_not_the_root(self):
        self.track("docs/a.md")
        (self.repo / ".gitignore").write_text("docs/drafts/*/\n")
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[paths]\ndrafts = "docs/drafts"\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("PASS drafts", out)

    def test_a_rule_that_un_ignores_a_plans_drafts_directory_fails(self):
        self.track("docs/a.md")
        (self.repo / ".gitignore").write_text("docs/drafts/*\n!docs/drafts/*/\n")
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[paths]\ndrafts = "docs/drafts"\n')
        code, out = self.doctor()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL drafts:", out)

    def test_a_registry_naming_a_missing_path_is_a_warning(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[design]\n'
                     'registries = ["a route joins `known_routes()` in `server/tests/routes.rs`, and its count"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("WARN design registries: `server/tests/routes.rs` names nothing in this worktree", out)
        self.assertNotIn("known_routes", out)

    def test_a_missing_path_named_twice_warns_once_and_a_url_is_no_path(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[design]\n'
                     'registries = ["`a/b.rs` and `a/b.rs` and `https://x/y`"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertEqual(out.count("WARN design registries"), 1, out)
        self.assertNotIn("https", out)

    def test_paths_outside_the_worktree_and_placeholders_are_not_checked(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[design]\nregistries = ['
                     '"`/etc/x/y` and `~/notes/z` and `<crate>/src/lib.rs` and `{dir}/a.rs` and `#anchor`",'
                     ' "`docs/spec.md#section` in the spec"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertEqual(out.count("WARN design registries"), 1, out)
        self.assertIn("`docs/spec.md` names nothing", out)

    def test_an_unbalanced_backtick_does_not_shift_the_pairing_of_the_next_line(self):
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[design]\nregistries = ['
                     '"a stray ` here", "the table in `a/b.rs`"]\n')
        code, out = self.doctor()
        self.assertIn("WARN design registries: `a/b.rs` names nothing", out)

    def test_registries_whose_paths_exist_pass(self):
        (self.repo / "server" / "tests").mkdir(parents=True)
        (self.repo / "server" / "tests" / "routes.rs").write_text("x\n")
        self.profile('schema = 1\n[gates]\nstory = ["sh -c true"]\n[design]\n'
                     'registries = ["the table in `server/tests/routes.rs:10-40`", "the count in `docs/*.md`"]\n')
        code, out = self.doctor()
        self.assertEqual(code, 0, out)
        self.assertIn("PASS design registries", out)
        self.assertNotIn("WARN design registries", out)


if __name__ == "__main__":
    unittest.main()
