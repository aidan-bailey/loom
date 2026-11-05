"""Tests for packages.py and `flowconf.py gates`. Run from lib/: python3 -m unittest -q packages_test.py"""

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flowconf  # noqa: E402
import packages  # noqa: E402

FLOWCONF = Path(__file__).with_name("flowconf.py")
# A workspace of three crates in which b depends on a.
CRATES = {
    "Cargo.toml": '[workspace]\nmembers = ["crates/a", "crates/b", "crates/c"]\nresolver = "2"\n',
    "crates/a/Cargo.toml": '[package]\nname = "a"\nversion = "0.1.0"\nedition = "2021"\n',
    "crates/a/src/lib.rs": "pub fn a() {}\n",
    "crates/b/Cargo.toml": '[package]\nname = "b"\nversion = "0.1.0"\nedition = "2021"\n\n[dependencies]\na = { path = "../a" }\n',
    "crates/b/src/lib.rs": "pub fn b() {\n    a::a()\n}\n",
    "crates/c/Cargo.toml": '[package]\nname = "c"\nversion = "0.1.0"\nedition = "2021"\n',
    "crates/c/src/lib.rs": "pub fn c() {}\n",
    "README.md": "x\n",
}


def metadata(root: Path) -> dict:
    """What `cargo metadata --format-version 1 --no-deps` reports for CRATES, trimmed to the keys read."""
    return {
        "packages": [
            {"name": "a", "manifest_path": str(root / "crates/a/Cargo.toml"), "dependencies": []},
            {"name": "b", "manifest_path": str(root / "crates/b/Cargo.toml"),
             "dependencies": [{"name": "a", "path": str(root / "crates/a"), "kind": None}]},
            {"name": "c", "manifest_path": str(root / "crates/c/Cargo.toml"),
             "dependencies": [{"name": "serde", "kind": None}]},
        ]
    }


# A git hook's GIT_* variables would point the temp repos at the outer repository.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}


def git(cwd: Path, *args: str) -> str:
    return subprocess.run(["git", "-C", str(cwd), *args], check=True, capture_output=True, text=True,
                          env=CLEAN_ENV).stdout.strip()


def write_crates(root: Path) -> None:
    for path, text in CRATES.items():
        (root / path).parent.mkdir(parents=True, exist_ok=True)
        (root / path).write_text(text)


class PackagesTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(os.path.realpath(self.tmp.name)) / "ws"
        write_crates(self.root)
        self.graph = packages.parse_metadata(metadata(self.root))

    def tearDown(self):
        self.tmp.cleanup()

    def args(self, *changed: str):
        return packages.package_args(list(changed), self.root, self.graph)

    def test_a_change_in_a_leaf_package_gives_that_package(self):
        self.assertEqual(self.args("crates/c/src/lib.rs"), ["-p", "c"])

    def test_a_change_in_a_dependency_adds_its_dependents(self):
        self.assertEqual(self.args("crates/a/src/lib.rs"), ["-p", "a", "-p", "b"])

    def test_dependents_are_transitive(self):
        root = self.root
        chain = {"packages": [
            {"name": "a", "manifest_path": str(root / "crates/a/Cargo.toml"), "dependencies": []},
            {"name": "b", "manifest_path": str(root / "crates/b/Cargo.toml"),
             "dependencies": [{"name": "a", "path": str(root / "crates/a")}]},
            {"name": "d", "manifest_path": str(root / "crates/d/Cargo.toml"),
             "dependencies": [{"name": "b", "path": str(root / "crates/b")}]},
        ]}
        graph = packages.parse_metadata(chain)
        self.assertEqual(packages.package_args(["crates/a/src/lib.rs"], root, graph), ["-p", "a", "-p", "b", "-p", "d"])
        self.assertEqual(packages.package_args(["crates/b/src/lib.rs"], root, graph), ["-p", "b", "-p", "d"])

    def test_a_package_manifest_belongs_to_its_package(self):
        self.assertEqual(self.args("crates/c/Cargo.toml"), ["-p", "c"])

    def test_a_workspace_wide_file_gives_the_workspace(self):
        self.assertEqual(self.args("Cargo.toml", "crates/c/src/lib.rs"), ["--workspace"])
        self.assertEqual(self.args("Cargo.lock"), ["--workspace"])

    def test_a_change_outside_every_package_gives_none(self):
        self.assertIsNone(self.args("README.md", "docs/notes.md"))

    def test_expand_applies_the_prefix_and_the_packages(self):
        lines = packages.expand(["cargo fmt --all -- --check", "cargo clippy {packages} -- -D warnings"],
                                "nix develop --command", lambda: ["-p", "c"])
        self.assertEqual(lines, ["RUN nix develop --command cargo fmt --all -- --check",
                                 "RUN nix develop --command cargo clippy -p c -- -D warnings"])

    def test_a_story_that_changed_no_package_skips_the_gates_that_need_one(self):
        lines = packages.expand(["cargo fmt --all -- --check", "cargo test {packages}"], "", lambda: None)
        self.assertEqual(lines, ["RUN cargo fmt --all -- --check",
                                 "SKIPPED cargo test {packages} (the story changed no Cargo package)"])

    def test_a_failing_metadata_call_fails_the_gates_and_never_skips_them(self):
        def broken():
            raise flowconf.FlowError("`cargo metadata` failed: no Cargo.toml")

        lines = packages.expand(["cargo test {packages}", "cargo doc {packages}"], "", broken)
        self.assertEqual(len(lines), 2)
        self.assertTrue(all(line.startswith("FAIL ") for line in lines), lines)
        self.assertIn("no Cargo.toml", lines[0])

    def test_gates_without_the_placeholder_never_resolve(self):
        def broken():
            raise AssertionError("resolved without a placeholder")

        self.assertEqual(packages.expand(["make check"], "", broken), ["RUN make check"])

    def test_the_real_cargo_metadata_gives_the_same_packages(self):
        if shutil.which("cargo") is None:
            self.skipTest("cargo is not on PATH")
        graph = packages.parse_metadata(packages.load_metadata(self.root, ""))
        self.assertEqual(graph, packages.parse_metadata(metadata(self.root)))
        self.assertEqual(packages.package_args(["crates/a/src/lib.rs"], self.root, graph), ["-p", "a", "-p", "b"])


class GatesCommandTest(unittest.TestCase):
    """`flowconf.py gates` in a repo, with a stand-in `cargo` on PATH that prints recorded metadata."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        base = Path(os.path.realpath(self.tmp.name))
        self.root = base / "ws"
        write_crates(self.root)
        (self.root / ".claude").mkdir()
        (self.root / ".claude" / "flow.toml").write_text(textwrap.dedent(
            """\
            schema = 1
            [gates]
            story = ["cargo fmt --all -- --check", "cargo test {packages}"]
            final = ["cargo test {packages}", "cargo doc --no-deps"]
            """
        ))
        git(self.root, "init", "-q")
        git(self.root, "add", "-A")
        self.commit("base")
        bin_dir = base / "bin"
        bin_dir.mkdir()
        metadata_file = base / "metadata.json"
        metadata_file.write_text(json.dumps(metadata(self.root)))
        fake = bin_dir / "cargo"
        fake.write_text(
            "#!/bin/sh\n"
            'case "$FAKE_CARGO_FAIL" in\n'
            '  "") ;;\n'
            '  multi) printf "error: could not read manifest at crates/x\\n\\nCaused by:\\n  no such file\\n" >&2; exit 101 ;;\n'
            "  silent) exit 7 ;;\n"
            '  *) echo "error: broken workspace" >&2; exit 101 ;;\n'
            "esac\n"
            'cat "$FAKE_METADATA"\n'
        )
        fake.chmod(fake.stat().st_mode | stat.S_IEXEC)
        self.env = {**CLEAN_ENV, "PATH": f"{bin_dir}{os.pathsep}{CLEAN_ENV['PATH']}",
                    "FAKE_METADATA": str(metadata_file), "FLOW_HOME": str(base / "flow-home")}

    def tearDown(self):
        self.tmp.cleanup()

    def commit(self, message: str) -> str:
        git(self.root, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", message)
        return git(self.root, "rev-parse", "HEAD")

    def change(self, path: str, text: str) -> str:
        (self.root / path).write_text(text)
        git(self.root, "add", "-A")
        return self.commit(f"change {path}")

    def gates(self, *args: str, **extra: str) -> tuple[int, str]:
        result = subprocess.run([sys.executable, str(FLOWCONF), "gates", *args], cwd=self.root,
                                capture_output=True, text=True, env={**self.env, **extra})
        return result.returncode, result.stdout + result.stderr

    def test_story_gates_name_the_packages_the_story_changed(self):
        sha = self.change("crates/a/src/lib.rs", "pub fn a() {\n}\n")
        code, out = self.gates("--story", sha)
        self.assertEqual(code, 0, out)
        self.assertEqual(out.splitlines(), ["RUN cargo fmt --all -- --check", "RUN cargo test -p a -p b"])

    def test_only_the_story_commits_count(self):
        first = self.change("crates/a/src/lib.rs", "pub fn a() {\n}\n")
        second = self.change("crates/c/src/lib.rs", "pub fn c() {\n}\n")
        self.assertEqual(self.gates("--story", second)[1].splitlines()[1], "RUN cargo test -p c")
        self.assertEqual(self.gates("--story", first, second)[1].splitlines()[1], "RUN cargo test -p a -p b -p c")

    def test_a_failing_metadata_call_exits_1_with_fail_lines(self):
        sha = self.change("crates/c/src/lib.rs", "pub fn c() {\n}\n")
        code, out = self.gates("--story", sha, FAKE_CARGO_FAIL="1")
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL cargo test {packages} (cannot expand {packages}:", out)
        self.assertIn("broken workspace", out)

    def test_a_multi_line_metadata_error_stays_on_one_fail_line(self):
        sha = self.change("crates/c/src/lib.rs", "pub fn c() {\n}\n")
        code, out = self.gates("--story", sha, FAKE_CARGO_FAIL="multi")
        self.assertEqual(code, 1, out)
        fails = [line for line in out.splitlines() if line.startswith("FAIL ")]
        self.assertEqual(len(fails), 1, out)
        self.assertEqual(len(out.splitlines()), 2, out)
        self.assertIn("error: could not read manifest at crates/x Caused by: no such file", fails[0])

    def test_a_silent_metadata_failure_names_the_exit_code(self):
        sha = self.change("crates/c/src/lib.rs", "pub fn c() {\n}\n")
        code, out = self.gates("--story", sha, FAKE_CARGO_FAIL="silent")
        self.assertEqual(code, 1, out)
        self.assertIn("exited 7)", out)

    def test_changed_files_reads_non_ascii_paths_as_they_are(self):
        sha = self.change("crates/c/src/h\u00e9llo.txt", "x\n")
        with mock.patch.dict(os.environ, CLEAN_ENV, clear=True):
            files = packages.changed_files(self.root, [sha])
        self.assertEqual(files, ["crates/c/src/h\u00e9llo.txt"])

    def test_changed_files_of_a_merge_commit_is_what_it_brought_in(self):
        main = git(self.root, "rev-parse", "--abbrev-ref", "HEAD")
        git(self.root, "switch", "-q", "-c", "side")
        self.change("crates/c/src/lib.rs", "pub fn c() {\n}\n")
        git(self.root, "switch", "-q", main)
        self.change("crates/a/src/lib.rs", "pub fn a() {\n}\n")
        git(self.root, "-c", "user.email=t@example.com", "-c", "user.name=t", "merge", "-q", "--no-ff", "-m", "merge", "side")
        head = git(self.root, "rev-parse", "HEAD")
        with mock.patch.dict(os.environ, CLEAN_ENV, clear=True):
            files = packages.changed_files(self.root, [head])
        self.assertEqual(files, ["crates/c/src/lib.rs"])

    def test_final_gates_run_over_the_workspace(self):
        code, out = self.gates("--final")
        self.assertEqual(code, 0, out)
        self.assertEqual(out.splitlines(), ["RUN cargo test --workspace", "RUN cargo doc --no-deps"])

    def test_one_of_story_or_final_is_required(self):
        self.assertEqual(self.gates()[0], 2)


if __name__ == "__main__":
    unittest.main()
