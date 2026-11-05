"""Tests for check-agents.sh. Run from lib/: python3 -m unittest -q check_agents_test.py"""

import os
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

LIB = Path(__file__).resolve().parent
# A git hook's GIT_* variables must not leak into the lint's subprocess.
CLEAN_ENV = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
AGENT = textwrap.dedent(
    """\
    ---
    name: dev
    description: A test developer. Pins set by the owner; change only on their instruction.
    model: haiku
    effort: high
    maxTurns: 10
    tools: Bash, Read, Write, Edit, SendMessage
    skills:
      - flow:habits
    experimental:
      cacheTtl: 5m
    ---

    You build what the brief says.
    """
)
SKILL = textwrap.dedent(
    """\
    ---
    name: habits
    description: Test habits.
    user-invocable: false
    ---

    Read the index with git diff-index before a pathspec commit; never git commit --amend.
    """
)
DRAFTER = textwrap.dedent(
    """\
    ---
    name: drafter
    description: A test drafter. Pins set by the owner; change only on their instruction.
    model: sonnet
    effort: high
    maxTurns: 10
    tools: Bash, Read, Write, Edit, SendMessage
    experimental:
      cacheTtl: 5m
    ---

    You write plan text in your drafts directory, and never commit.
    """
)
COMMAND = textwrap.dedent(
    """\
    ---
    name: cmd
    description: An owner-started command.
    disable-model-invocation: true
    ---

    Do what the owner asked.
    """
)


class CheckAgentsTest(unittest.TestCase):
    """The lint run from a copy of flow's layout: lib/, plugin/agents/ and plugin/skills/."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / "lib").mkdir()
        for name in ("check-agents.sh", "banned-tokens.txt"):
            shutil.copy(LIB / name, self.root / "lib" / name)
        (self.root / "lib" / "guarded-agents.txt").write_text("# test\ndev developer\n")
        # FLOW_DATA, where the owner's private banned list lives: none unless a test writes one.
        self.data = Path(self.tmp.name, "data")
        self.data.mkdir()
        (self.root / "plugin" / "agents").mkdir(parents=True)
        (self.root / "plugin" / "skills" / "habits").mkdir(parents=True)
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT)
        (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(SKILL)

    def tearDown(self):
        self.tmp.cleanup()

    def lint(self) -> tuple[int, str]:
        env = {**CLEAN_ENV, "FLOW_DATA": str(self.data)}
        result = subprocess.run(["bash", str(self.root / "lib" / "check-agents.sh")], capture_output=True, text=True, env=env)
        return result.returncode, result.stdout + result.stderr

    def test_a_valid_roster_passes(self):
        code, out = self.lint()
        self.assertEqual(code, 0, out)

    def test_an_inline_skills_list_is_preloaded(self):
        # The habits skill is not owner-started, so it passes only when the inline form marks it preloaded.
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT.replace("skills:\n  - flow:habits\n", "skills: [flow:habits]\n"))
        code, out = self.lint()
        self.assertEqual(code, 0, out)

    def test_a_description_over_400_characters_fails(self):
        line = "description: A test developer. Pins set by the owner; change only on their instruction."
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT.replace(line, "description: " + "x" * 401))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("dev.md: description is 401 characters", out)

    def test_a_long_skill_description_fails(self):
        (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(SKILL.replace("Test habits.", "y" * 401))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("SKILL.md: description is 401 characters", out)

    def test_a_banned_word_in_a_body_fails(self):
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT.replace("what the brief says", "what nix develop gives you"))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [banned]", out)
        self.assertIn("nix develop", out)

    def test_a_preload_of_a_missing_skill_fails(self):
        shutil.rmtree(self.root / "plugin" / "skills" / "habits")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("preloads 'habits'", out)

    def test_a_banned_word_in_a_skill_fails(self):
        (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(SKILL + "\nRun nix develop first.\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [banned]", out)
        self.assertIn("SKILL.md", out)

    def test_a_missing_banned_tokens_file_fails(self):
        (self.root / "lib" / "banned-tokens.txt").unlink()
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [banned]", out)

    def test_a_missing_skills_dir_fails(self):
        shutil.rmtree(self.root / "plugin" / "skills")
        code, out = self.lint()
        self.assertNotEqual(code, 0, out)
        self.assertIn("FAIL [dir]", out)

    def test_a_listed_drafter_passes_without_the_index_rules(self):
        (self.root / "lib" / "guarded-agents.txt").write_text("dev developer\ndrafter drafter\n")
        (self.root / "plugin" / "agents" / "drafter.md").write_text(DRAFTER)
        code, out = self.lint()
        self.assertEqual(code, 0, out)

    def test_an_owner_started_skill_that_sets_disable_model_invocation_passes(self):
        (self.root / "plugin" / "skills" / "cmd").mkdir()
        (self.root / "plugin" / "skills" / "cmd" / "SKILL.md").write_text(COMMAND)
        code, out = self.lint()
        self.assertEqual(code, 0, out)

    def test_a_quoted_or_commented_disable_model_invocation_counts(self):
        (self.root / "plugin" / "skills" / "cmd").mkdir()
        for form in ('"true"', "'true'", "true  # owner-started", "true   "):
            line = f"disable-model-invocation: {form}\n"
            (self.root / "plugin" / "skills" / "cmd" / "SKILL.md").write_text(COMMAND.replace("disable-model-invocation: true\n", line))
            code, out = self.lint()
            self.assertEqual(code, 0, f"{form}: {out}")

    def test_a_hash_with_no_space_before_it_is_not_a_comment(self):
        (self.root / "plugin" / "skills" / "cmd").mkdir()
        line = "disable-model-invocation: true#x\n"
        (self.root / "plugin" / "skills" / "cmd" / "SKILL.md").write_text(COMMAND.replace("disable-model-invocation: true\n", line))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [invocation]", out)
        self.assertIn("cmd/SKILL.md", out)

    def test_a_preloaded_skill_with_a_quoted_or_commented_disable_model_invocation_fails(self):
        for form in ("true", '"true"', "true  # owner-started"):
            skill = SKILL.replace("user-invocable: false\n", f"disable-model-invocation: {form}\n")
            (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(skill)
            code, out = self.lint()
            self.assertEqual(code, 1, f"{form}: {out}")
            self.assertIn("blocks preloading", out)

    def test_an_owner_started_skill_the_model_could_start_fails(self):
        (self.root / "plugin" / "skills" / "cmd").mkdir()
        (self.root / "plugin" / "skills" / "cmd" / "SKILL.md").write_text(COMMAND.replace("disable-model-invocation: true\n", ""))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [invocation]", out)
        self.assertIn("cmd/SKILL.md", out)


    def test_an_agent_that_sets_hooks_fails(self):
        text = AGENT.replace("---\n\nYou build", "hooks:\n  PreToolUse: []\n---\n\nYou build")
        (self.root / "plugin" / "agents" / "dev.md").write_text(text)
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("sets hooks:", out)

    def test_an_editing_agent_the_guarded_list_misses_fails(self):
        (self.root / "lib" / "guarded-agents.txt").write_text("# none\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("doesn't list it", out)

    def test_a_guarded_agent_without_its_file_fails(self):
        (self.root / "lib" / "guarded-agents.txt").write_text("dev developer\nghost developer\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("lists 'ghost'", out)

    def test_an_unknown_guard_mode_fails(self):
        (self.root / "lib" / "guarded-agents.txt").write_text("dev reviewer\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("has mode 'reviewer'", out)

    def test_a_missing_guarded_list_fails(self):
        (self.root / "lib" / "guarded-agents.txt").unlink()
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("guarded-agents.txt does not exist", out)

    def test_a_listed_developer_without_the_index_rules_fails(self):
        (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(SKILL.replace("git diff-index", "git diff"))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [git]", out)

    def test_a_preload_without_the_flow_prefix_fails(self):
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT.replace("  - flow:habits", "  - habits"))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("namespaced name", out)

    def test_a_word_from_the_private_list_fails(self):
        (self.data / "banned-tokens.txt").write_text("kumquat\n")
        (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(SKILL + "\nThe kumquat repo.\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("kumquat", out)

    def test_a_banned_word_in_a_doc_fails(self):
        (self.root / "plugin" / "docs").mkdir()
        (self.root / "plugin" / "docs" / "guide.md").write_text("Run nix develop first.\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("guide.md", out)

if __name__ == "__main__":
    unittest.main()
