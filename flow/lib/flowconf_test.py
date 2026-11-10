"""Tests for flowconf.py. Run from lib/: python3 -m unittest -q flowconf_test.py"""

import os
import re
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flowconf  # noqa: E402

SCRIPT = Path(__file__).with_name("flowconf.py")
GOOD = textwrap.dedent(
    r"""
    schema = 1
    prefix = "nix develop --command"
    services = ["scripts/db.sh start"]
    generated = ["*.gen.rs"]

    [gates]
    story = ["cargo fmt --all -- --check", "cargo clippy {packages} -- -D warnings"]
    final = ["cargo test --workspace"]
    vacuous = ['0 passed; \d+ filtered out']

    [build]
    exclusive = ["cargo"]

    [landmines]
    markers = ["#[tokio::test"]
    notes = "A tokio test is single-threaded unless it says multi_thread."

    [review]
    idioms = "tracing, not println."

    [paths]
    state = ""
    records = ""
    """
)


def git(cwd: Path, *args: str) -> str:
    return subprocess.run(["git", "-C", str(cwd), *args], check=True, capture_output=True, text=True).stdout.strip()


def make_repo(path: Path) -> Path:
    """A git repository at `path` with one commit."""
    path.mkdir(parents=True)
    git(path, "init", "-q")
    (path / "README.md").write_text("x\n")
    git(path, "add", "README.md")
    git(path, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
    return path


class FlowconfTest(unittest.TestCase):
    def setUp(self):
        # A git hook's GIT_* variables would point the temp repos at the outer repository.
        env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        self.tmp = tempfile.TemporaryDirectory()
        base = Path(os.path.realpath(self.tmp.name))
        patcher = mock.patch.dict(os.environ, env, clear=True)
        patcher.start()
        self.addCleanup(patcher.stop)
        self.repo = make_repo(base / "repo")

    def tearDown(self):
        self.tmp.cleanup()

    def repo_profile(self, text: str = GOOD) -> Path:
        path = self.repo / ".claude" / "flow.toml"
        path.parent.mkdir(exist_ok=True)
        path.write_text(text)
        return path

    def refused(self, text: str) -> str:
        self.repo_profile(text)
        with self.assertRaises(flowconf.FlowError) as caught:
            flowconf.load_profile(self.repo)
        return str(caught.exception)

    def run_cli(self, *args: str, stdin: str | None = None) -> tuple[int, str, str]:
        result = subprocess.run([sys.executable, str(SCRIPT), *args], cwd=self.repo, capture_output=True, text=True,
                                input=stdin)
        return result.returncode, result.stdout, result.stderr

    def test_the_key_is_the_resolved_path_with_every_other_character_a_dash(self):
        self.assertEqual(flowconf.repo_key(self.repo), re.sub(r"[^A-Za-z0-9]", "-", str(self.repo)))

    def test_a_linked_worktree_shares_the_main_checkouts_key(self):
        linked = self.repo.parent / "linked"
        git(self.repo, "worktree", "add", "-q", "-b", "side", str(linked))
        self.assertEqual(flowconf.repo_key(linked), flowconf.repo_key(self.repo))

    def test_a_path_through_a_symlink_gives_its_targets_key(self):
        """git already resolves the symlink and realpath is belt-and-braces, so this pins the outcome, not the line."""
        alias = self.repo.parent / "alias"
        alias.symlink_to(self.repo)
        self.assertEqual(flowconf.repo_key(alias), flowconf.repo_key(self.repo))

    def test_the_repo_profile_is_read(self):
        path = self.repo_profile()
        profile = flowconf.load_profile(self.repo)
        self.assertEqual(profile.path, path)
        self.assertEqual(profile.prefix, "nix develop --command")
        self.assertEqual(profile.story_gates[1], "cargo clippy {packages} -- -D warnings")
        self.assertEqual(profile.vacuous, (r"0 passed; \d+ filtered out",))
        self.assertEqual(profile.markers, ("#[tokio::test",))
        self.assertRegex(profile.digest, r"^[0-9a-f]{12}$")

    def test_with_no_profile_the_cli_names_the_repo_file_and_flow_init(self):
        code, _, err = self.run_cli("show", "--for", "developer")
        self.assertEqual(code, 1)
        self.assertIn(str(self.repo / ".claude" / "flow.toml"), err)
        self.assertIn("/flow:init", err)

    def test_an_unknown_key_is_refused_by_name(self):
        self.assertIn("landmines.marker: unknown key", self.refused('schema = 1\n[landmines]\nmarker = ["x"]\n'))
        self.assertIn("gate: unknown key", self.refused('schema = 1\ngate = "x"\n'))
        self.assertIn("lint: unknown key", self.refused('schema = 1\n[lint]\nx = "y"\n'))

    def test_a_wrong_type_is_refused_by_name(self):
        self.assertIn("gates.story: must be a list of strings", self.refused('schema = 1\n[gates]\nstory = "cargo test"\n'))
        self.assertIn("prefix: must be a string", self.refused("schema = 1\nprefix = 3\n"))
        self.assertIn("gates: must be a table", self.refused('schema = 1\ngates = ["x"]\n'))

    def test_a_gate_may_be_a_table_naming_its_locks(self):
        self.repo_profile(
            'schema = 1\n[gates]\nstory = ["cargo test"]\n'
            'final = ["cargo test", { run = "pnpm e2e", locks = ["port:4300", "e2e/db"] }, { run = "true" }]\n'
        )
        profile = flowconf.load_profile(self.repo)
        self.assertEqual(profile.final_gates, ("cargo test", "pnpm e2e", "true"))
        self.assertEqual(profile.final_locks, ((), ("port:4300", "e2e/db"), ()))
        self.assertEqual(profile.story_gates, ("cargo test",))
        self.assertEqual(profile.story_locks, ((),))

    def test_a_malformed_gate_table_is_refused_by_name(self):
        def gate(table: str) -> str:
            return self.refused(f"schema = 1\n[gates]\nfinal = [{table}]\n")

        self.assertIn("gates.final: a gate table needs run, a string", gate('{ locks = ["port:1"] }'))
        self.assertIn("gates.final: a gate table needs run, a string", gate('{ run = "" }'))
        self.assertIn("gates.final: a gate table needs run, a string", gate("{ run = 3 }"))
        self.assertIn("gates.final: x: unknown key lock", gate('{ run = "x", lock = ["port:1"] }'))
        self.assertIn("gates.final: x: locks must be a list of strings", gate('{ run = "x", locks = "port:1" }'))
        self.assertIn("gates.final: x: lock 'port 1' is not", gate('{ run = "x", locks = ["port 1"] }'))
        self.assertIn("gates.final: x: lock port:1 is given twice", gate('{ run = "x", locks = ["port:1", "port:1"] }'))
        many = ", ".join(f'"l{i}"' for i in range(9))
        self.assertIn("gates.final: x: 9 locks; at most 8", gate(f'{{ run = "x", locks = [{many}] }}'))
        self.assertIn("gates.final: must be a list of strings", gate("3"))
        self.assertIn("gates.vacuous: must be a list of strings", self.refused("schema = 1\n[gates]\nvacuous = [{ run = 'x' }]\n"))

    def test_notes_and_idioms_take_a_list_of_strings_one_per_line(self):
        self.repo_profile('schema = 1\n[landmines]\nnotes = ["one", "two"]\n[review]\nidioms = ["a"]\n')
        profile = flowconf.load_profile(self.repo)
        self.assertEqual(profile.notes, "one\ntwo")
        self.assertEqual(profile.idioms, "a")

    def test_notes_that_are_neither_a_string_nor_a_list_of_strings_are_refused(self):
        message = "landmines.notes: must be a string or a list of strings"
        self.assertIn(message, self.refused("schema = 1\n[landmines]\nnotes = 3\n"))
        self.assertIn(message, self.refused('schema = 1\n[landmines]\nnotes = ["x", 3]\n'))

    def test_a_missing_schema_is_refused(self):
        self.assertIn("schema: missing", self.refused('prefix = ""\n'))

    def test_another_schema_is_refused(self):
        self.assertIn("schema: 2; this flow reads schema 1", self.refused("schema = 2\n"))

    def test_a_vacuous_pattern_that_is_no_regular_expression_is_refused(self):
        self.assertIn("gates.vacuous", self.refused("schema = 1\n[gates]\nvacuous = ['(']\n"))

    def test_invalid_toml_is_refused(self):
        self.assertIn("not valid TOML", self.refused("schema = \n"))

    def test_a_profile_that_cannot_be_used_still_fails_the_state_command(self):
        self.repo_profile('schema = 1\n[landmines]\nmarker = ["x"]\n')
        code, _, err = self.run_cli("state")
        self.assertEqual(code, 1)
        self.assertIn("landmines.marker", err)

    def test_each_role_block_holds_only_its_own_values(self):
        self.repo_profile()
        profile = flowconf.load_profile(self.repo)
        blocks = {role: flowconf.show(profile, role, self.repo) for role in flowconf.ROLES}
        self.assertIn("`nix develop --command`", blocks["developer"])
        self.assertIn("single-threaded", blocks["developer"])
        self.assertIn("scripts/db.sh start", blocks["developer"])
        self.assertIn("before the tests that need them", blocks["developer"])
        self.assertNotIn("tracing", blocks["developer"])
        self.assertNotIn("filtered out", blocks["developer"])
        self.assertIn("filtered out", blocks["verifier"])
        self.assertIn("scripts/db.sh start", blocks["verifier"])
        self.assertNotIn("single-threaded", blocks["verifier"])
        self.assertIn("tracing", blocks["reviewer"])
        self.assertIn("single-threaded", blocks["reviewer"])
        self.assertNotIn("scripts/db.sh", blocks["reviewer"])
        self.assertIn("`nix develop --command`", blocks["reviewer"])
        self.assertNotIn("filtered out", blocks["reviewer"])
        self.assertNotIn("tracing", blocks["verifier"])
        for role in flowconf.ROLES:
            self.assertIn(f"[{role}]", blocks["supervisor"])
        self.assertNotIn("[verifier]", blocks["developer"])
        self.assertIn(f"sha256:{profile.digest}", blocks["supervisor"])
        self.assertNotIn("[drafter]", blocks["supervisor"])

    def test_the_drafters_block_holds_what_a_drafter_writes_commands_from(self):
        self.repo_profile(GOOD + '[design]\nregistries = ["a route joins the table in routes.rs"]\n')
        block = flowconf.show(flowconf.load_profile(self.repo), "drafter", self.repo)
        self.assertTrue(block.startswith("[drafter]\n"), block)
        for value in ("`nix develop --command`", "`cargo clippy {packages} -- -D warnings`", "stands for the Cargo packages",
                      "`*.gen.rs`", "`#[tokio::test`", "single-threaded", "tracing, not println.",
                      "a route joins the table in routes.rs"):
            self.assertIn(value, block)
        for value in ("scripts/db.sh", "filtered out", "[developer]", "State directory"):
            self.assertNotIn(value, block)

    def test_the_packages_rule_shows_only_when_a_story_gate_holds_it(self):
        self.repo_profile('schema = 1\n[gates]\nstory = ["bash lib/test.sh"]\n')
        block = flowconf.show(flowconf.load_profile(self.repo), "drafter", self.repo)
        self.assertIn("`bash lib/test.sh`", block)
        self.assertIn("Design registries", block)
        self.assertNotIn("{packages}", block)

    def test_services_prints_each_raw_on_its_own_line(self):
        self.repo_profile('schema = 1\nservices = ["docker compose up -d db, cache", "echo `a`"]\n')
        code, out, err = self.run_cli("services")
        self.assertEqual(code, 0, err)
        self.assertEqual(out, "docker compose up -d db, cache\necho `a`\n")
        self.repo_profile("schema = 1\n")
        code, out, err = self.run_cli("services")
        self.assertEqual((code, out), (0, ""), err)

    def test_show_for_drafter_prints_its_block(self):
        self.repo_profile()
        code, out, err = self.run_cli("show", "--for", "drafter")
        self.assertEqual(code, 0, err)
        self.assertTrue(out.startswith("[drafter]\n"), out)

    def test_two_worktrees_get_different_default_state_directories(self):
        linked = self.repo.parent / "linked"
        git(self.repo, "worktree", "add", "-q", "-b", "side", str(linked))
        self.assertEqual(flowconf.state_dir(self.repo, None), self.repo / ".git" / "flow")
        self.assertNotEqual(flowconf.state_dir(linked, None), flowconf.state_dir(self.repo, None))

    def test_a_relative_state_path_is_under_the_worktree(self):
        self.repo_profile('schema = 1\n[paths]\nstate = "target/flow"\n')
        self.assertEqual(flowconf.state_dir(self.repo, flowconf.load_profile(self.repo)), self.repo / "target" / "flow")

    def test_state_makes_the_directory_and_prints_it(self):
        code, out, err = self.run_cli("state")
        self.assertEqual(code, 0, err)
        self.assertEqual(Path(out.strip()), self.repo / ".git" / "flow")
        self.assertTrue(Path(out.strip()).is_dir())

    def test_status_names_the_key_and_the_profile_or_none(self):
        key = flowconf.repo_key(self.repo)
        self.assertEqual(self.run_cli("status")[1].strip(), f"key {key}; profile none")
        path = self.repo_profile()
        self.assertEqual(self.run_cli("status")[1].strip(), f"key {key}; profile {path}")

    def test_key_prints_the_key(self):
        self.assertEqual(self.run_cli("key")[1].strip(), flowconf.repo_key(self.repo))

    def test_an_unknown_role_is_a_usage_error(self):
        self.assertEqual(self.run_cli("show", "--for", "wizard")[0], 2)

    def test_the_design_keys_default_to_nothing_and_the_drafts_directory_to_dot_flow(self):
        self.repo_profile()
        profile = flowconf.load_profile(self.repo)
        self.assertEqual((profile.drafts, profile.registries), ("", ""))
        self.assertEqual(flowconf.drafts_dir(self.repo, profile), self.repo / ".flow" / "drafts")
        self.assertEqual(flowconf.drafts_dir(self.repo, None), self.repo / ".flow" / "drafts")

    def test_a_drafts_path_and_registries_read_one_per_line(self):
        self.repo_profile('schema = 1\n[paths]\ndrafts = "notes/drafts"\n[design]\nregistries = ["routes in a.rs", "the count in b.md"]\n')
        profile = flowconf.load_profile(self.repo)
        self.assertEqual(profile.registries, "routes in a.rs\nthe count in b.md")
        self.assertEqual(flowconf.drafts_dir(self.repo, profile), self.repo / "notes" / "drafts")

    def test_the_design_keys_refuse_a_wrong_type_by_name(self):
        self.assertIn("design.registries: must be a string or a list of strings",
                      self.refused("schema = 1\n[design]\nregistries = 3\n"))
        self.assertIn("design.registry: unknown key", self.refused('schema = 1\n[design]\nregistry = "x"\n'))
        self.assertIn("paths.drafts: must be a string", self.refused("schema = 1\n[paths]\ndrafts = 3\n"))

    def test_a_drafts_path_that_leaves_the_worktree_is_refused(self):
        for value in ("/tmp/drafts", "../drafts", "~/drafts", ".", "notes/../../drafts", ".git/drafts"):
            message = self.refused(f'schema = 1\n[paths]\ndrafts = "{value}"\n')
            self.assertIn("paths.drafts", message, value)
            self.assertIn("below the worktree's top level", message, value)

    def test_state_also_makes_the_agent_reports_directory(self):
        code, out, err = self.run_cli("state")
        self.assertEqual(code, 0, err)
        self.assertTrue((Path(out.strip()) / "agent-reports").is_dir())

    def exclude_lines(self) -> list[str]:
        """The repo's info/exclude, line by line; a linked worktree shares it."""
        exclude = self.repo / ".git" / "info" / "exclude"
        return exclude.read_text().splitlines() if exclude.is_file() else []

    def test_drafts_makes_the_directory_keeps_it_out_of_git_and_prints_it(self):
        code, out, err = self.run_cli("drafts", "plan-a")
        self.assertEqual(code, 0, err)
        self.assertEqual(Path(out.strip()), self.repo / ".flow" / "drafts" / "plan-a")
        self.assertTrue(Path(out.strip()).is_dir())
        self.assertEqual(self.exclude_lines().count("/.flow/"), 1)
        self.assertEqual(self.run_cli("drafts", "plan-b")[0], 0)
        self.assertEqual(self.exclude_lines().count("/.flow/"), 1)
        (self.repo / ".flow" / "drafts" / "plan-b" / "S1.md").write_text("## Story 1: x\n")
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")

    def test_drafts_in_a_linked_worktree_excludes_in_the_common_directory(self):
        linked = self.repo.parent / "linked"
        git(self.repo, "worktree", "add", "-q", "-b", "side", str(linked))
        path = flowconf.make_drafts(linked, None, "p")
        self.assertEqual(path, linked / ".flow" / "drafts" / "p")
        self.assertTrue(path.is_dir())
        self.assertEqual(self.exclude_lines().count("/.flow/"), 1)
        self.assertEqual(git(linked, "status", "--porcelain"), "")
        self.assertEqual(git(self.repo, "check-ignore", ".flow/drafts/q"), ".flow/drafts/q")

    def test_drafts_adds_nothing_when_git_already_ignores_the_directory(self):
        self.repo_profile('schema = 1\n[paths]\ndrafts = "notes/drafts"\n')
        (self.repo / ".gitignore").write_text("notes/drafts/\n")
        before = self.exclude_lines()
        path = flowconf.make_drafts(self.repo, flowconf.load_profile(self.repo), "p")
        self.assertEqual(path, self.repo / "notes" / "drafts" / "p")
        self.assertTrue(path.is_dir())
        self.assertEqual(self.exclude_lines(), before)

    def test_drafts_refuses_to_ignore_a_directory_that_holds_tracked_files(self):
        self.repo_profile('schema = 1\n[paths]\ndrafts = "docs/drafts"\n')
        (self.repo / "docs").mkdir()
        (self.repo / "docs" / "a.md").write_text("x\n")
        git(self.repo, "add", "docs/a.md")
        before = self.exclude_lines()
        with self.assertRaises(flowconf.FlowError) as caught:
            flowconf.make_drafts(self.repo, flowconf.load_profile(self.repo), "p")
        self.assertIn("docs/ holds tracked files", str(caught.exception))
        self.assertEqual(self.exclude_lines(), before)
        self.assertFalse((self.repo / "docs" / "drafts").exists())

    def test_drafts_refuses_a_directory_a_repo_rule_un_ignores(self):
        (self.repo / ".gitignore").write_text("!/.flow/\n")
        with self.assertRaises(flowconf.FlowError) as caught:
            flowconf.make_drafts(self.repo, None, "p")
        self.assertIn("is still not ignored", str(caught.exception))
        self.assertFalse((self.repo / ".flow" / "drafts" / "p").exists())

    def test_drafts_reports_a_failing_check_ignore(self):
        real = subprocess.run

        def fake(cmd, *args, **kwargs):
            if "check-ignore" in cmd:
                return subprocess.CompletedProcess(cmd, 128, "", "fatal: broken\n")
            return real(cmd, *args, **kwargs)

        with mock.patch.object(flowconf.subprocess, "run", fake):
            with self.assertRaises(flowconf.FlowError) as caught:
                flowconf.make_drafts(self.repo, None, "p")
        self.assertIn("git check-ignore failed", str(caught.exception))
        self.assertFalse((self.repo / ".flow").exists())

    def test_drafts_refuses_a_path_or_a_file_name_for_the_stem(self):
        for stem in ("a/b", "plan.md", "", ".", ".."):
            code, _, err = self.run_cli("drafts", stem)
            self.assertEqual(code, 1, stem)
            self.assertIn("not a plan file stem", err)
        self.assertFalse((self.repo / ".flow").exists())

    def test_a_git_failure_is_one_line(self):
        # git reports an unknown revision in three lines on any machine.
        with self.assertRaises(flowconf.FlowError) as caught:
            flowconf.git(self.repo, "rev-parse", "no-such-rev")
        self.assertNotIn("\n", str(caught.exception))
        self.assertIn("ambiguous argument", str(caught.exception))

    def test_vacuous_matches_the_specs_own_pattern_with_python_re(self):
        self.repo_profile()
        code, out, err = self.run_cli("vacuous", "-", stdin="running 1 test\ntest result: ok. 0 passed; 12 filtered out\n")
        self.assertEqual(code, 0, err)
        self.assertEqual(out, "test result: ok. 0 passed; 12 filtered out\n")

    def test_vacuous_reads_a_file_and_exits_1_when_no_line_matches(self):
        self.repo_profile()
        output = self.repo.parent / "out.txt"
        output.write_text("test result: ok. 3 passed; 0 failed\n")
        code, out, _ = self.run_cli("vacuous", str(output))
        self.assertEqual((code, out), (1, ""))

    def test_vacuous_with_an_empty_pattern_list_exits_1(self):
        self.repo_profile("schema = 1\n[gates]\nvacuous = []\n")
        self.assertEqual(self.run_cli("vacuous", "-", stdin="0 passed; 1 filtered out\n")[0], 1)

    def test_vacuous_errors_exit_2(self):
        code, _, err = self.run_cli("vacuous", "-", stdin="x\n")
        self.assertEqual(code, 2)
        self.assertIn("/flow:init", err)
        self.repo_profile()
        code, _, err = self.run_cli("vacuous", str(self.repo.parent / "missing.txt"))
        self.assertEqual(code, 2)
        self.assertIn("missing.txt", err)
        self.repo_profile("schema = 2\n")
        self.assertEqual(self.run_cli("vacuous", "-", stdin="x\n")[0], 2)

    def test_roster_prints_the_id_loom_gave_the_run(self):
        with mock.patch.dict(os.environ, {"FLOW_ROSTER": "loom flow roster 0123456789ab"}):
            code, out, err = self.run_cli("roster")
        self.assertEqual(code, 0, err)
        self.assertEqual(out.strip(), "loom flow roster 0123456789ab")

    def test_roster_without_flow_roster_exits_2(self):
        os.environ.pop("FLOW_ROSTER", None)
        code, _, err = self.run_cli("roster")
        self.assertEqual(code, 2)
        self.assertIn("FLOW_ROSTER is not set", err)

    def commit_all(self) -> str:
        """Commit everything staged or tracked-and-changed, and return HEAD."""
        git(self.repo, "add", "-u")
        git(self.repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "c")
        return git(self.repo, "rev-parse", "HEAD")

    def test_doc_paths_default_to_markdown_and_take_the_profiles_globs(self):
        self.repo_profile()
        self.assertEqual(flowconf.load_profile(self.repo).doc_paths, ("*.md",))
        self.repo_profile('schema = 1\n[docs]\npaths = ["*.md", "docs/*.svg"]\n')
        self.assertEqual(flowconf.load_profile(self.repo).doc_paths, ("*.md", "docs/*.svg"))
        self.assertIn("docs.paths: must be a list of strings", self.refused('schema = 1\n[docs]\npaths = "*.md"\n'))
        self.assertIn("docs.globs: unknown key", self.refused('schema = 1\n[docs]\nglobs = ["*.md"]\n'))

    def test_glob_match_tries_the_whole_path_and_the_name(self):
        self.assertTrue(flowconf.glob_match("README.md", ("*.md",)))
        self.assertTrue(flowconf.glob_match("docs/claude/a.md", ("*.md",)), "fnmatch's * crosses /")
        self.assertTrue(flowconf.glob_match("pkg/README", ("README",)), "by its name")
        self.assertFalse(flowconf.glob_match("main.go", ("*.md",)))
        self.assertTrue(flowconf.is_generated("sub/go.sum", ()), "a common lockfile")
        self.assertTrue(flowconf.is_generated("CHANGELOG.md", ("CHANGELOG.md",)))
        self.assertFalse(flowconf.is_generated("README.md", ("CHANGELOG.md",)))

    def test_the_doc_writers_block_holds_what_a_doc_pass_needs(self):
        self.repo_profile(GOOD + '[design]\nregistries = ["a route joins the table in routes.rs"]\n')
        profile = flowconf.load_profile(self.repo)
        block = flowconf.show(profile, "doc-writer", self.repo)
        self.assertTrue(block.startswith("[doc-writer]\n"), block)
        for value in ("`nix develop --command`", "Doc paths (change only files these match): `*.md`", "`*.gen.rs`",
                      "tracing, not println.", "a route joins the table in routes.rs"):
            self.assertIn(value, block)
        for value in ("scripts/db.sh", "filtered out", "single-threaded", "cargo clippy"):
            self.assertNotIn(value, block)
        self.assertIn("[doc-writer]", flowconf.show(profile, "supervisor", self.repo))

    def test_docs_check_lists_what_a_range_changed_outside_the_doc_paths(self):
        self.repo_profile('schema = 1\ngenerated = ["CHANGELOG.md"]\n')
        base = git(self.repo, "rev-parse", "HEAD")
        (self.repo / "docs").mkdir()
        (self.repo / "docs" / "guide.md").write_text("g\n")
        (self.repo / "README.md").write_text("y\n")
        git(self.repo, "add", "docs/guide.md")
        head = self.commit_all()
        self.assertEqual(self.run_cli("docs-check", "--base", base, "--head", head)[:2], (0, ""))
        (self.repo / "main.go").write_text("package main\n")
        (self.repo / "CHANGELOG.md").write_text("c\n")
        git(self.repo, "add", "main.go", "CHANGELOG.md")
        head = self.commit_all()
        code, out, err = self.run_cli("docs-check", "--base", base, "--head", head)
        self.assertEqual(code, 1, err)
        self.assertEqual(sorted(out.splitlines()), ["CHANGELOG.md", "main.go"], "code, and a generated Markdown file")

    def test_docs_check_takes_a_deleted_doc_and_checks_a_rename_by_both_names(self):
        self.repo_profile("schema = 1\n")
        (self.repo / "NOTES.md").write_text("n\n")
        git(self.repo, "add", "NOTES.md")
        base = self.commit_all()
        git(self.repo, "rm", "-q", "NOTES.md")
        head = self.commit_all()
        self.assertEqual(self.run_cli("docs-check", "--base", base, "--head", head)[:2], (0, ""), "a deleted doc")
        git(self.repo, "mv", "README.md", "README.txt")
        head = self.commit_all()
        self.assertEqual(self.run_cli("docs-check", "--base", base, "--head", head)[:2], (1, "README.txt\n"))

    def test_docs_check_refuses_a_path_that_isnt_utf8(self):
        self.repo_profile("schema = 1\n")
        base = git(self.repo, "rev-parse", "HEAD")
        (self.repo / os.fsdecode(b"bad\xff.md")).write_text("x\n")
        subprocess.run(["git", "-C", str(self.repo), "add", "-A"], check=True, capture_output=True)
        head = self.commit_all()
        code, out, err = self.run_cli("docs-check", "--base", base, "--head", head)
        self.assertEqual((code, out), (2, ""), err)
        self.assertIn("isn't UTF-8", err)

    def test_docs_check_errors_exit_2(self):
        self.repo_profile("schema = 1\n")
        code, _, err = self.run_cli("docs-check", "--base", "no-such-rev", "--head", "HEAD")
        self.assertEqual(code, 2)
        self.assertIn("no-such-rev", err)
        self.repo_profile("schema = 2\n")
        self.assertEqual(self.run_cli("docs-check", "--base", "HEAD", "--head", "HEAD")[0], 2)
        self.assertEqual(self.run_cli("docs-check", "--base", "HEAD")[0], 2, "a usage error")


if __name__ == "__main__":
    unittest.main()
