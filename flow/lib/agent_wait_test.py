"""Tests for agent-wait.sh. Run from lib/: python3 -m unittest -q agent_wait_test.py"""

import json
import os
import re
import shutil
import signal
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("agent-wait.sh")

# A `loom` that records each call's arguments as a JSON line and runs what follows `--`.
FAKE_LOOM = """#!/usr/bin/env bash
python3 -c 'import json, sys; print(json.dumps(sys.argv[1:]))' "$@" >> "$FAKE_LOOM_CALLS"
while (( $# )) && [[ $1 != -- ]]; do shift; done
shift
exec "$@"
"""


def outside_loom():
    """This process's environment without LOOM_LAUNCH, so no test queues a job on a real loom daemon."""
    return {k: v for k, v in os.environ.items() if k != "LOOM_LAUNCH"}


class AgentWaitTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.env = {**outside_loom(), "AGENT_WAIT_DIR": self.tmp.name}
        self.calls = Path(self.tmp.name, "loom-calls")

    def tearDown(self):
        for pid_file in Path(self.tmp.name).glob("*.pid"):
            try:
                os.kill(int(pid_file.read_text()), signal.SIGKILL)
            except (ProcessLookupError, ValueError):
                pass
        self.tmp.cleanup()

    def run_script(self, *args, env=None):
        return subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True,
                              env=env or self.env, timeout=60)

    def loom_env(self):
        """The environment of a loom session whose `loom` is FAKE_LOOM."""
        bin_dir = Path(self.tmp.name, "bin")
        bin_dir.mkdir(exist_ok=True)
        fake = bin_dir / "loom"
        fake.write_text(FAKE_LOOM)
        fake.chmod(0o755)
        return {**self.env, "LOOM_LAUNCH": "test-launch", "FAKE_LOOM_CALLS": str(self.calls),
                "PATH": f"{bin_dir}{os.pathsep}{self.env['PATH']}"}

    def loom_calls(self):
        if not self.calls.exists():
            return []
        return [json.loads(line) for line in self.calls.read_text().splitlines()]

    def test_the_default_wait_ends_before_claude_codes_180_second_cap(self):
        # Claude Code 2.1.316 moves a background subagent's foreground Bash call to the background
        # after 180 s; a wait that outlives the cap no longer ends inside the call.
        defaults = re.findall(r"secs=\$\{2:-(\d+)\}", SCRIPT.read_text())
        self.assertEqual(len(defaults), 1, defaults)
        self.assertLess(int(defaults[0]), 180)

    def test_wait_reports_the_commands_exit_status_and_log(self):
        started = self.run_script("start", "job", "--", "bash", "-c", "echo hello; exit 3")
        self.assertEqual(started.returncode, 0, started.stderr)
        result = self.run_script("wait", "job", "20")
        self.assertEqual(result.returncode, 3)
        self.assertEqual(result.stdout.strip(), "done job exit 3")
        self.assertEqual(Path(self.tmp.name, "job.log").read_text().strip(), "hello")

    def test_wait_says_running_until_the_command_ends(self):
        self.run_script("start", "slow", "--", "timeout", "3", "tail", "-f", "/dev/null")
        first = self.run_script("wait", "slow", "1")
        self.assertEqual((first.returncode, first.stdout.strip()), (75, "running slow"))
        second = self.run_script("wait", "slow", "20")
        self.assertEqual((second.returncode, second.stdout.strip()), (124, "done slow exit 124"))

    def test_the_command_outlives_the_call_that_started_it(self):
        self.run_script("start", "bg", "--", "timeout", "2", "tail", "-f", "/dev/null")
        pid = int(Path(self.tmp.name, "bg.pid").read_text())
        os.kill(pid, 0)  # still alive after start returned
        self.assertEqual(self.run_script("wait", "bg", "20").returncode, 124)

    def test_a_name_still_running_is_refused(self):
        self.run_script("start", "dup", "--", "timeout", "10", "tail", "-f", "/dev/null")
        again = self.run_script("start", "dup", "--", "true")
        self.assertEqual(again.returncode, 2)
        self.assertIn("still running", again.stderr)

    def test_a_command_killed_before_it_records_a_status_is_lost(self):
        self.run_script("start", "victim", "--", "timeout", "30", "tail", "-f", "/dev/null")
        pid = int(Path(self.tmp.name, "victim.pid").read_text())
        os.killpg(os.getpgid(pid), signal.SIGKILL)
        time.sleep(0.5)
        result = self.run_script("wait", "victim", "5")
        self.assertEqual(result.returncode, 70)
        self.assertTrue(result.stdout.startswith("lost victim"))

    def test_outside_a_repo_without_a_wait_dir_it_names_the_missing_state_directory(self):
        with tempfile.TemporaryDirectory() as cwd, tempfile.TemporaryDirectory() as flow_home:
            env = {k: v for k, v in outside_loom().items() if not k.startswith("GIT_") and k != "AGENT_WAIT_DIR"}
            env["FLOW_HOME"] = flow_home
            result = subprocess.run(["bash", str(SCRIPT), "start", "x", "--", "true"], capture_output=True,
                                    text=True, env=env, cwd=cwd, timeout=60)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("no state directory", result.stderr)

    def test_bad_arguments_are_refused(self):
        self.assertEqual(self.run_script("start", "x", "true").returncode, 2)
        self.assertEqual(self.run_script("start", "../x", "--", "true").returncode, 2)
        self.assertEqual(self.run_script("wait", "never-started").returncode, 2)
        self.assertEqual(self.run_script("wait", "x", "soon").returncode, 2)
        self.assertEqual(self.run_script("frobnicate").returncode, 2)

    def test_without_an_override_the_log_lands_in_the_repos_state_directory(self):
        repo = Path(self.tmp.name, "repo")
        repo.mkdir()
        env = {k: v for k, v in outside_loom().items() if k != "AGENT_WAIT_DIR" and not k.startswith("GIT_")}
        subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True, env=env)
        env["FLOW_HOME"] = str(Path(self.tmp.name, "flow-home"))

        def run(*args):
            return subprocess.run(["bash", str(SCRIPT), *args], cwd=repo, capture_output=True, text=True,
                                  env=env, timeout=60)

        started = run("start", "job", "--", "bash", "-c", "echo hi")
        self.assertEqual(started.returncode, 0, started.stderr)
        self.assertEqual(run("wait", "job", "20").stdout.strip(), "done job exit 0")
        self.assertEqual(Path(repo, ".git", "flow", "agent-wait", "job.log").read_text().strip(), "hi")

    def test_inside_loom_the_command_runs_as_a_queued_job(self):
        # A step started outside loom's queue runs beside the jobs the queue admitted, on cores and
        # memory the queue thinks are free (2026-11-05: 103 of one repo's 169 steps did).
        env = self.loom_env()
        started = self.run_script("start", "job", "--", "bash", "-c", "echo hello; exit 3", env=env)
        self.assertEqual(started.returncode, 0, started.stderr)
        result = self.run_script("wait", "job", "20", env=env)
        self.assertEqual((result.returncode, result.stdout.strip()), (3, "done job exit 3"))
        self.assertEqual(Path(self.tmp.name, "job.log").read_text().strip(), "hello")
        self.assertEqual(self.loom_calls(),
                         [["job", "run", "-d", "job: bash -c echo hello; exit 3", "--",
                           "bash", "-c", "echo hello; exit 3"]])

    def test_the_jobs_description_is_one_line_of_at_most_60_characters(self):
        env = self.loom_env()
        self.run_script("start", "gate", "--", "sh", "-c", "echo first\necho " + "x" * 80, env=env)
        self.run_script("wait", "gate", "20", env=env)
        [call] = self.loom_calls()
        self.assertEqual(call[2:4], ["-d", "gate: sh -c echo first echo " + "x" * 32])
        self.assertEqual(len(call[3]), 60)

    def test_outside_loom_the_command_runs_directly(self):
        env = self.loom_env()
        del env["LOOM_LAUNCH"]
        self.run_script("start", "job", "--", "bash", "-c", "echo hi", env=env)
        self.assertEqual(self.run_script("wait", "job", "20", env=env).stdout.strip(), "done job exit 0")
        self.assertEqual(Path(self.tmp.name, "job.log").read_text().strip(), "hi")
        self.assertEqual(self.loom_calls(), [])

    def test_without_loom_on_the_path_the_command_runs_directly(self):
        tools = Path(self.tmp.name, "tools")
        tools.mkdir()
        for tool in ("bash", "setsid", "mkdir", "rm", "cat", "mv", "sleep", "tail", "timeout"):
            os.symlink(shutil.which(tool), tools / tool)
        env = {**self.loom_env(), "PATH": str(tools)}
        self.run_script("start", "job", "--", "bash", "-c", "echo hi", env=env)
        self.assertEqual(self.run_script("wait", "job", "20", env=env).stdout.strip(), "done job exit 0")
        self.assertEqual(Path(self.tmp.name, "job.log").read_text().strip(), "hi")
        self.assertEqual(self.loom_calls(), [])

    def test_a_command_that_is_already_a_loom_job_is_not_queued_twice(self):
        # Agents told to use loom's queue already write `start <name> -- loom job run ...`; queueing
        # that again would hold a claim while the inner job waits for its own.
        env = self.loom_env()
        fake = str(Path(self.tmp.name, "bin", "loom"))
        for name, loom in (("bare", "loom"), ("path", fake)):
            self.run_script("start", name, "--", loom, "job", "run", "-d", name, "--", "bash", "-c", "echo once",
                            env=env)
            self.assertEqual(self.run_script("wait", name, "20", env=env).stdout.strip(), f"done {name} exit 0")
            self.assertEqual(Path(self.tmp.name, f"{name}.log").read_text().strip(), "once")
        self.assertEqual(self.loom_calls(), [["job", "run", "-d", "bare", "--", "bash", "-c", "echo once"],
                                             ["job", "run", "-d", "path", "--", "bash", "-c", "echo once"]])


if __name__ == "__main__":
    unittest.main()
