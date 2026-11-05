"""Tests for git_guard.py. Run from lib/: python3 -m unittest -q git_guard_test.py"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("git_guard.py")
sys.path.insert(0, str(SCRIPT.parent))
import git_guard  # noqa: E402


# A git hook's GIT_* variables would point the temp repos at the outer repository.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}


def run_hook(payload: dict, drafter: bool = False, project_dir: str | None = None) -> tuple[int, str]:
    env = {k: v for k, v in CLEAN_ENV.items() if k != "CLAUDE_PROJECT_DIR"}
    if project_dir is not None:
        env["CLAUDE_PROJECT_DIR"] = project_dir
    argv = [sys.executable, str(SCRIPT)] + (["--drafter"] if drafter else [])
    result = subprocess.run(argv, input=json.dumps(payload), capture_output=True, text=True, env=env)
    return result.returncode, result.stderr


def guard(command: str, cwd: str = "/", drafter: bool = False) -> tuple[int, str]:
    return run_hook({"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": cwd,
                     "tool_input": {"command": command}}, drafter)


def write(tool: str, path: str, root: str, drafter: bool = True) -> tuple[int, str]:
    return run_hook({"hook_event_name": "PreToolUse", "tool_name": tool, "cwd": "/",
                     "tool_input": {"file_path": path}}, drafter, project_dir=root)


def make_repo(root: str, profile: str = "schema = 1\n") -> str:
    """`root` as a git repository whose .claude/flow.toml is `profile`: the guard takes the drafts directory from it."""
    subprocess.run(["git", "init", "-q", root], check=True, capture_output=True, env=CLEAN_ENV)
    Path(root, ".claude").mkdir()
    Path(root, ".claude", "flow.toml").write_text(profile)
    return root


class DeveloperGitGuardTest(unittest.TestCase):
    def assertAllowed(self, command, cwd="/"):
        code, err = guard(command, cwd)
        self.assertEqual(code, 0, f"blocked {command!r}: {err}")

    def assertBlocked(self, command, cwd="/", says=""):
        code, err = guard(command, cwd)
        self.assertEqual(code, 2, f"allowed {command!r}")
        self.assertIn(says, err)

    def test_everyday_commands_pass(self):
        for command in [
            "git status",
            "/usr/bin/env git diff-index --cached --name-status HEAD",
            "git diff --cached --numstat",
            "git log --oneline -3 | cat",
            "git -C /tmp log",
            "git add -N src/new.rs",
            'git commit -m "fix: x" -- src/a.rs src/b.rs',
            "cargo nextest run -p example-core",
            'echo "git reset is banned"',
        ]:
            self.assertAllowed(command)

    def test_heredoc_commit_message_is_not_a_command(self):
        self.assertAllowed("git commit -q -F - -- a.rs <<'EOF'\nnever git reset or git push\nEOF")

    def test_history_and_shared_state_commands_are_blocked(self):
        for command, says in [
            ("git reset --soft HEAD~1", "git reset"),
            ("git -C /tmp reset", "git reset"),
            ("rtk git stash list", "git stash"),
            ("FOO=1 git clean -fd", "git clean"),
            ("cd x && git rebase main", "git rebase"),
            ("git checkout -- a.rs", "git checkout"),
            ("git switch -c other", "git switch"),
            ("git restore a.rs", "git restore"),
            ("git push", "git push"),
        ]:
            self.assertBlocked(command, says=says)

    def test_commits_that_sweep_or_rewrite_are_blocked(self):
        self.assertBlocked('git commit --amend -m "x" -- a.rs', says="--amend")
        self.assertBlocked('git commit -am "x" -- a.rs', says="git commit -am")
        self.assertBlocked('git commit -m "x"', says="pathspec")
        self.assertBlocked('git commit -m "x" --', says="pathspec")

    def test_sweeping_adds_are_blocked(self):
        for command in ["git add -A", "git add --all", "git add -u", "git add .", "git add :/"]:
            self.assertBlocked(command, says="git add")

    def test_a_directory_add_is_blocked_and_a_file_add_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            Path(tmp, "src").mkdir()
            Path(tmp, "src", "a.rs").write_text("")
            self.assertBlocked("git add src", cwd=tmp, says="whole directory")
            self.assertAllowed("git add src/a.rs", cwd=tmp)

    def test_unparseable_command_falls_back_to_a_search(self):
        self.assertBlocked("git reset 'unbalanced", says="git reset")
        self.assertAllowed("echo 'unbalanced")

    def test_non_bash_and_garbage_input_pass(self):
        result = subprocess.run([sys.executable, str(SCRIPT)], input="not json",
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)

    def test_developer_writes_are_not_confined(self):
        code, err = write("Write", "/anywhere/a.rs", root="/", drafter=False)
        self.assertEqual(code, 0, err)


class DrafterGuardTest(unittest.TestCase):
    def assertAllowed(self, command, cwd="/"):
        code, err = guard(command, cwd, drafter=True)
        self.assertEqual(code, 0, f"blocked {command!r}: {err}")

    def assertBlocked(self, command, says=""):
        code, err = guard(command, drafter=True)
        self.assertEqual(code, 2, f"allowed {command!r}")
        self.assertIn(says, err)
        self.assertIn("drafter-guard", err)

    def test_reading_commands_pass(self):
        for command in [
            "git log --oneline -3",
            "git -C /x show HEAD:src/a.rs",
            "git --no-pager diff main...HEAD -- src",
            "/usr/bin/env git diff-index --cached --name-status HEAD",
            "rtk git grep -n fn_name",
            "git blame -L 10,20 src/a.rs",
            "git status --short",
            "git rev-parse --show-toplevel",
            'python3 "$HOME/.claude/flow/lib/plan_premises.py" .flow/drafts/p/S1.md',
            "nix develop --command cargo metadata --format-version 1 --offline",
            "sed -n 1,40p src/a.rs | cat",
        ]:
            self.assertAllowed(command)

    def test_every_git_write_is_blocked(self):
        for command, says in [
            ("git add -N src/new.rs", "git add"),
            ('git commit -m "x" -- src/a.rs', "git commit"),
            ("git rm src/a.rs", "git rm"),
            ("git mv a.rs b.rs", "git mv"),
            ("git apply fix.diff", "git apply"),
            ("git tag v1", "git tag"),
            ("git branch -D other", "git branch"),
            ("git worktree add ../x", "git worktree"),
            ("git stash list", "git stash"),
            ("git config user.name x", "git config"),
            ("git log && git reset --hard", "git reset"),
        ]:
            self.assertBlocked(command, says=says)

    def test_unparseable_command_falls_back_to_a_search(self):
        self.assertBlocked("git commit 'unbalanced", says="git commit")
        self.assertAllowed("git log 'unbalanced")

    def test_writes_land_only_under_the_drafts_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = make_repo(tmp)
            drafts = Path(root, ".flow", "drafts")
            for tool in ("Write", "Edit"):
                code, err = write(tool, str(drafts / "plan" / "S1.md"), root)
                self.assertEqual(code, 0, err)
            code, err = write("Write", ".flow/drafts/plan/fix/slice-1.md", root)
            self.assertEqual(code, 0, err)
            for path in [
                str(Path(root, "docs", "plan.md")),
                str(drafts / ".." / ".." / "docs" / "plan.md"),
                str(Path(root, ".flow", "draftsX", "a.md")),
                str(Path(root, ".flow", "brainstorm", "a.html")),
                "/tmp/elsewhere.md",
            ]:
                code, err = write("Edit", path, root)
                self.assertEqual(code, 2, f"allowed {path}")
                self.assertIn("writes only under", err)

    def test_a_relative_path_is_read_against_the_worktrees_top_level(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = make_repo(tmp)
            Path(root, "sub").mkdir()
            sub = str(Path(root, "sub"))
            code, err = write("Write", ".flow/drafts/plan/S1.md", sub)
            self.assertEqual(code, 0, err)
            code, _ = write("Write", "sub/a.md", sub)
            self.assertEqual(code, 2)

    def test_writes_are_allowed_under_pythonsafepath(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = make_repo(tmp)
            payload = {"hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": "/",
                       "tool_input": {"file_path": str(Path(root, ".flow", "drafts", "plan", "S1.md"))}}
            env = {**CLEAN_ENV, "CLAUDE_PROJECT_DIR": root, "PYTHONSAFEPATH": "1"}
            result = subprocess.run([sys.executable, str(SCRIPT), "--drafter"], input=json.dumps(payload),
                                    capture_output=True, text=True, env=env, cwd=tmp)
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_a_symlink_out_of_the_drafts_directory_is_blocked(self):
        with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryDirectory() as outside:
            root = make_repo(tmp)
            drafts = Path(root, ".flow", "drafts")
            drafts.mkdir(parents=True)
            (drafts / "escape").symlink_to(outside)
            code, _ = write("Write", str(drafts / "escape" / "a.md"), root)
            self.assertEqual(code, 2)

    def test_writes_follow_the_profiles_drafts_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = make_repo(tmp, 'schema = 1\n[paths]\ndrafts = "notes/drafts"\n')
            code, err = write("Write", str(Path(root, "notes", "drafts", "plan", "S1.md")), root)
            self.assertEqual(code, 0, err)
            code, err = write("Write", str(Path(root, ".flow", "drafts", "plan", "S1.md")), root)
            self.assertEqual(code, 2)
            self.assertIn("notes/drafts", err)

    def test_from_a_subdirectory_the_drafts_directory_is_the_worktrees(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = make_repo(tmp)
            Path(root, "sub").mkdir()
            code, err = write("Write", str(Path(root, ".flow", "drafts", "plan", "S1.md")), str(Path(root, "sub")))
            self.assertEqual(code, 0, err)

    def test_a_write_is_blocked_when_the_drafts_directory_cannot_be_told(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = make_repo(tmp, "schema = 2\n")
            code, err = write("Write", str(Path(root, ".flow", "drafts", "plan", "S1.md")), root)
            self.assertEqual(code, 2)
            self.assertIn("can't be told", err)
            self.assertIn("schema: 2", err)
        code, err = write("Write", "/nowhere-xyz/.flow/drafts/plan/S1.md", "/nowhere-xyz")
        self.assertEqual(code, 2)
        self.assertIn("can't be told", err)


class MessageTest(unittest.TestCase):
    def test_messages_name_the_flow_habits_skill_and_not_loom(self):
        _, err = guard("git switch -c other")
        self.assertIn("The flow:habits skill forbids it.", err)
        self.assertNotIn("loom", err)


if __name__ == "__main__":
    unittest.main()
