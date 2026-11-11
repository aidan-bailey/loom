"""Tests for agent_costs.py. Run from lib/: python3 -m unittest -q agent_costs_test.py"""

import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("agent_costs.py")


def record(msg_id, model, when, **usage):
    creation = usage.pop("creation", None)
    if creation is not None:
        usage["cache_creation"] = creation
    return json.dumps({"timestamp": when, "message": {"id": msg_id, "role": "assistant", "model": model, "usage": usage}})


def reply(msg_id, model, when, block, **usage):
    """A record that also logs one content block, as Claude Code does."""
    line = json.loads(record(msg_id, model, when, **usage))
    line["message"]["content"] = [block]
    return json.dumps(line)


def run(*args):
    result = subprocess.run([sys.executable, str(SCRIPT), *args], capture_output=True, text=True)
    return result.returncode, result.stdout, result.stderr


def totals(out):
    return json.loads(re.search(r"<!-- agent-costs (\{.*\}) -->", out).group(1))


class RunReportTest(unittest.TestCase):
    """`run`: a loom flow run's managers and headless agent runs (flow in loom part 3b)."""

    def test_prices_the_managers_and_each_agent_run_by_role(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            repo = root / "repo"
            repo.mkdir()
            env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
            subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True, env=env)
            state = repo / ".git" / "flow"
            (state / "runs").mkdir(parents=True)
            (state / "agents").mkdir()
            (state / "runs" / "r.json").write_text(json.dumps({
                "managers": ["msess"], "agents": ["r-s1-dev1", "r-s1-review1"],
                "start": "2026-11-06T10:00:00Z", "end": "2026-11-06T12:00:00Z"}))
            (state / "agents" / "r-s1-dev1.json").write_text(json.dumps({"role": "intermediate-dev", "session": "dsess"}))
            (state / "agents" / "r-s1-review1.json").write_text(json.dumps({"role": "story-reviewer", "session": "gone"}))
            cfg = root / "cfg"
            (cfg / "projects" / "p").mkdir(parents=True)
            (cfg / "projects" / "p" / "msess.jsonl").write_text(
                record("m1", "claude-opus-5-5", "2026-11-06T10:05:00Z", input_tokens=10, output_tokens=10))
            (cfg / "projects" / "p" / "dsess.jsonl").write_text("\n".join([
                record("d1", "claude-sonnet-5-5", "2026-11-06T10:10:00Z", input_tokens=10, output_tokens=10),
                record("d0", "claude-sonnet-5-5", "2026-11-06T09:00:00Z", input_tokens=10, output_tokens=10),  # before the run
            ]))
            result = subprocess.run([sys.executable, str(SCRIPT), "run", "r"], capture_output=True, text=True, cwd=repo,
                                    env={**env, "CLAUDE_CONFIG_DIR": str(cfg)})
            self.assertEqual(result.returncode, 0, result.stderr)
            data = totals(result.stdout)
            self.assertEqual(data["run"], "r")
            self.assertEqual(data["by_type"]["main"]["requests"], 1)
            self.assertEqual(data["by_type"]["intermediate-dev"]["requests"], 1, "only the run's window counts")
            self.assertNotIn("story-reviewer", data["by_type"], "an agent run without a transcript is skipped")
            self.assertIn("no transcript for agent run r-s1-review1", result.stderr)
            self.assertIn("| r-s1-dev1 | intermediate-dev |", result.stdout)
            managers = json.loads(re.search(r"<!-- agent-costs-managers (\{.*\}) -->", result.stdout).group(1))
            self.assertEqual(managers, {"msess": ["claude-opus-5-5"]})


class AgentCostsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.main = root / "sess.jsonl"
        opus = "claude-opus-5-5"
        self.main.write_text("\n".join([
            # The same message logged twice: the second, final record carries the full output.
            record("m1", opus, "2026-10-13T10:00:00Z", input_tokens=10, output_tokens=400, cache_read_input_tokens=100_000,
                   creation={"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 20_000}),
            record("m1", opus, "2026-10-13T10:00:01Z", input_tokens=10, output_tokens=1000, cache_read_input_tokens=100_000,
                   creation={"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 20_000}),
            record("m0", opus, "2026-10-13T09:00:00Z", input_tokens=1_000_000),  # before --since
            "not json",
        ]))
        subagents = root / "sess" / "subagents"
        subagents.mkdir(parents=True)
        haiku = "claude-haiku-5-5"
        (subagents / "agent-abc1234def.jsonl").write_text("\n".join([
            record("h1", haiku, "2026-10-13T10:05:00Z", input_tokens=5, output_tokens=100, cache_read_input_tokens=50_000,
                   creation={"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 10_000}),
            record("h2", haiku, "2026-10-13T10:06:00Z", input_tokens=5, output_tokens=100, cache_read_input_tokens=120_000),
            record("x1", "claude-unknown-9", "2026-10-13T10:07:00Z", input_tokens=1),
        ]))
        (subagents / "agent-abc1234def.meta.json").write_text(json.dumps({"agentType": "junior-dev", "description": "Story 1"}))

    def tearDown(self):
        self.tmp.cleanup()

    def test_output_logged_at_stream_start_is_raised_to_the_visible_text(self):
        sonnet = "claude-sonnet-5-5"
        path = Path(self.tmp.name) / "stream.jsonl"
        path.write_text("\n".join([
            reply("s1", sonnet, "2026-10-13T11:00:00Z", {"type": "thinking", "thinking": ""}, input_tokens=1, output_tokens=8),
            reply("s1", sonnet, "2026-10-13T11:00:00Z", {"type": "tool_use", "name": "SubagentHandback",
                  "input": {"message": "x" * 3985}}, input_tokens=1, output_tokens=8),
            # A final count above the visible text stands: the main session logs those.
            reply("s2", sonnet, "2026-10-13T11:01:00Z", {"type": "text", "text": "short"}, input_tokens=1, output_tokens=700),
        ]))
        code, out, _ = run("session", str(path))
        self.assertEqual(code, 0, out)
        total = totals(out)["total"]
        self.assertEqual(total["output"], 1000 + 700)  # 4,000 characters of tool input at 4 a token
        self.assertEqual(total["output_estimated"], 1)
        self.assertIn("Output for 1 requests is estimated", out)

    def test_prices_keep_the_documented_ratios(self):
        # Every model writes its cache at 1.25x (5m) and 2x (1h) base input. A cache read is
        # 0.1x, except Opus 5.5 (0.05x) and Fable 5.1 (0.025x). Sonnet 5.5's read was once
        # entered at 0.05x, halving every Sonnet agent's largest cost.
        sys.path.insert(0, str(SCRIPT.parent))
        import agent_costs

        reads = {"claude-opus-5-5": 0.05, "claude-fable-5-1": 0.025, "claude-sonnet-5-5": 0.1, "claude-haiku-5-5": 0.1}
        self.assertEqual(set(agent_costs.PRICES), set(reads))
        for model, (p_in, p_5m, p_1h, p_read, _) in agent_costs.PRICES.items():
            self.assertAlmostEqual(p_5m, 1.25 * p_in, msg=model)
            self.assertAlmostEqual(p_1h, 2 * p_in, msg=model)
            self.assertAlmostEqual(p_read, reads[model] * p_in, msg=model)

    def test_prices_each_message_once_by_type(self):
        code, out, err = run("session", str(self.main), "--since", "2026-10-13T09:30:00Z")
        self.assertEqual(code, 0, err)
        data = totals(out)
        main = data["by_type"]["main"]
        # m1 once, at its final values: 10*4 + 20,000*8 + 100,000*0.20 + 1,000*20 per million.
        self.assertEqual(main["requests"], 1)
        self.assertEqual(main["output"], 1000)
        self.assertAlmostEqual(main["usd"], 0.20004, places=8)
        junior = data["by_type"]["junior-dev"]
        # h1 under 100K: 5*0.10 + 10,000*0.20 + 50,000*0.01 + 100*0.50.
        # h2 over 100K, long-prompt prices: 5*0.50 + 120,000*0.05 + 100*2.50.
        self.assertEqual(junior["requests"], 3)
        self.assertEqual(junior["over_100k"], 1)
        self.assertEqual(junior["unpriced"], 1)
        self.assertAlmostEqual(junior["usd"], 0.0025505 + 0.0062525, places=9)
        self.assertIn("| abc1234 Story 1 | junior-dev |", out)
        self.assertIn("used a model with no price", out)

    def test_since_filter_keeps_earlier_requests_out(self):
        _, out, _ = run("session", str(self.main))
        self.assertEqual(totals(out)["by_type"]["main"]["requests"], 2)

    def test_records_total_across_runs(self):
        _, out, _ = run("session", str(self.main), "--since", "2026-10-13T09:30:00Z")
        record_a = Path(self.tmp.name, "2026-10-13-a.md")
        record_b = Path(self.tmp.name, "2026-10-14-b.md")
        record_a.write_text("# Run A\n\n" + out)
        record_b.write_text("# Run B\n\n" + out)
        code, report, err = run("records", str(record_a), str(record_b))
        self.assertEqual(code, 0, err)
        self.assertIn("Across 2 runs", report)
        self.assertRegex(report, r"\| main \| 2 \| 2 \| 2 \|")
        code, _, err = run("records", str(Path(self.tmp.name, "sess.jsonl")))
        self.assertEqual(code, 1)

    def test_records_sum_every_session_of_a_run(self):
        _, out, _ = run("session", str(self.main), "--since", "2026-10-13T09:30:00Z")
        doubled = 2 * totals(out)["total"]["usd"]
        record = Path(self.tmp.name, "2026-10-14-two-sessions.md")
        record.write_text("# Run\n\n## Session 1\n\n" + out + "\n## Session 2\n\n" + out)
        code, report, err = run("records", str(record))
        self.assertEqual(code, 0, err)
        self.assertIn("Across 1 run\n", report)
        self.assertRegex(report, r"\| main \| 1 \| 2 \| 2 \|")
        usd = f"${doubled:,.4f}" if doubled < 1 else f"${doubled:,.2f}"
        self.assertIn(f"| {record} | 2 | {usd} |", report)


class ThinkingEstimateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        sys.path.insert(0, str(SCRIPT.parent))
        import agent_costs

        cls.costs = agent_costs

    def test_ratio_is_measured_when_ten_steps_without_thinking_grow(self):
        # Ten steps without thinking each add 300 characters and grow the context by 100 tokens: 3.0.
        steps = [(i * 100, 300, False) for i in range(10)]
        steps += [(1000, 600, True), (1300, 0, False)]
        estimates, ratio = self.costs.thinking_estimate(steps)
        self.assertAlmostEqual(ratio, 3.0)
        # The thinking step grew 300 tokens; 600 characters at 3.0 explain 200 of them.
        self.assertEqual(estimates[10], 100)

    def test_ratio_defaults_below_ten_steps(self):
        steps = [(i * 100, 300, False) for i in range(9)]
        steps += [(900, 600, True), (1200, 0, False)]
        estimates, ratio = self.costs.thinking_estimate(steps)
        self.assertEqual(self.costs.DEFAULT_CHARS_PER_TOKEN, 2.2)
        self.assertEqual(self.costs.MIN_RATIO_STEPS, 10)
        self.assertEqual(ratio, 2.2)
        # 300 tokens of growth less 600 / 2.2 = 272.7 explained.
        self.assertEqual(estimates[9], 27)

    def test_ratio_ignores_steps_whose_context_shrinks(self):
        # A step followed by a smaller context (a compaction) is not a sample, however many characters it added.
        steps = [(i * 100, 300, False) for i in range(10)]
        steps += [(1000, 99_999, False), (200, 0, False)]
        _, ratio = self.costs.thinking_estimate(steps)
        self.assertAlmostEqual(ratio, 3.0)

    def test_an_implausible_measured_ratio_falls_back_to_the_default(self):
        # 50 characters for 100 tokens of growth is 0.5 a token, and 1,000 for 100 is 10: neither is believed.
        self.assertEqual((self.costs.MIN_CHARS_PER_TOKEN, self.costs.MAX_CHARS_PER_TOKEN), (1.5, 4.0))
        for chars in (50, 1000):
            steps = [(i * 100, chars, False) for i in range(10)] + [(1000, 0, False)]
            self.assertEqual(self.costs.thinking_estimate(steps)[1], 2.2, chars)
        steps = [(i * 100, 150, False) for i in range(10)] + [(1000, 0, False)]
        self.assertAlmostEqual(self.costs.thinking_estimate(steps)[1], 1.5)  # the band's edge is inside it

    def test_estimate_is_never_negative(self):
        # 2,200 characters explain 1,000 tokens; the context grew by only 100.
        estimates, _ = self.costs.thinking_estimate([(1000, 2200, True), (1100, 0, False)])
        self.assertEqual(estimates, [0, 0])

    def test_the_last_step_gets_nothing(self):
        estimates, ratio = self.costs.thinking_estimate([(1000, 0, True)])
        self.assertEqual(estimates, [0])
        self.assertEqual(ratio, 2.2)
        self.assertEqual(self.costs.thinking_estimate([]), ([], 2.2))

    def test_a_step_without_thinking_gets_nothing_however_much_it_grew(self):
        estimates, _ = self.costs.thinking_estimate([(1000, 0, False), (9000, 0, False)])
        self.assertEqual(estimates, [0, 0])


def tool_results(when, *contents):
    """A user record carrying one tool_result block per content (a string, or a list of text parts)."""
    blocks = [{"type": "tool_result", "tool_use_id": f"t{n}", "content": content} for n, content in enumerate(contents)]
    return json.dumps({"timestamp": when, "message": {"role": "user", "content": blocks}})


def thinking_transcript(path, block=None, **extra):
    """Two Sonnet requests. The first thinks and adds 1,100 characters; the context then grows by 800 tokens.

    The characters: 400 of text, 115 of tool input (json.dumps of {"command": "z" * 100}) and 585 of results,
    one result a string and one a list of text parts. At the default 2.2 they explain 500 tokens, so 300 are
    hidden thinking. The second request is the last, and also has a thinking block: it gets nothing.
    """
    sonnet = "claude-sonnet-5-5"
    first = dict(input_tokens=10, output_tokens=1, cache_read_input_tokens=900, cache_creation_input_tokens=90)
    second = dict(input_tokens=10, output_tokens=1, cache_read_input_tokens=1690, cache_creation_input_tokens=100)
    thinking = block or {"type": "thinking", "thinking": "", "signature": "sig"}
    first, second = {**first, **extra}, {**second, **extra}
    path.write_text("\n".join([
        reply("a1", sonnet, "2026-10-14T10:00:00Z", thinking, **first),
        reply("a1", sonnet, "2026-10-14T10:00:00Z", {"type": "text", "text": "t" * 400}, **first),
        reply("a1", sonnet, "2026-10-14T10:00:00Z", {"type": "tool_use", "id": "t0", "name": "Bash",
              "input": {"command": "z" * 100}}, **first),
        tool_results("2026-10-14T10:00:05Z", "r" * 300, [{"type": "text", "text": "r" * 285}]),
        reply("a2", sonnet, "2026-10-14T10:00:10Z", thinking, **second),
        reply("a2", sonnet, "2026-10-14T10:00:10Z", {"type": "text", "text": "done"}, **second),
    ]))


def user_string(when, text, **flags):
    """A user record whose content is a string, as a hand-back report or a task notice is logged."""
    return json.dumps({"timestamp": when, "message": {"role": "user", "content": text}, **flags})


def attachment(when, **body):
    return json.dumps({"timestamp": when, "type": "attachment", "attachment": body})


def two_opus_requests(path, logged, *between):
    """Two Opus requests, the first thinking, with 400 characters of text, then `between` records.

    The context grows 782 tokens, and 400 characters at the default 2.2 explain 182, so the estimate is 600.
    `logged` is the first request's output_tokens: 100 is its visible text alone (the stream-start shape),
    700 is a main-session final count that already holds 600 tokens of thinking.
    """
    opus = "claude-opus-5-5"
    thinking = {"type": "thinking", "thinking": "", "signature": "sig"}
    first = dict(input_tokens=10, output_tokens=logged, cache_read_input_tokens=10_000)
    second = dict(input_tokens=10, output_tokens=5, cache_read_input_tokens=10_782)
    path.write_text("\n".join([
        reply("m1", opus, "2026-10-14T10:00:00Z", thinking, **first),
        reply("m1", opus, "2026-10-14T10:00:00Z", {"type": "text", "text": "t" * 400}, **first),
        *between,
        reply("m2", opus, "2026-10-14T10:00:10Z", {"type": "text", "text": "ok"}, **second),
    ]))


class ThinkingCostTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = Path(self.tmp.name) / "think.jsonl"

    def tearDown(self):
        self.tmp.cleanup()

    def test_a_thinking_request_is_priced_at_the_output_rate(self):
        thinking_transcript(self.path)
        code, out, err = run("session", str(self.path))
        self.assertEqual(code, 0, err)
        total = totals(out)["total"]
        self.assertEqual(total["thinking"], 300)
        self.assertAlmostEqual(total["usd_thinking"], 300 * 10.0 / 1e6, places=9)  # Sonnet 5.5 output: $10 a million
        # The logged-only figures are untouched by the estimate: 515 characters of the first reply at 4 a token
        # is 129 output tokens, and the second reply's logged 1 stands.
        self.assertEqual(total["output"], 129 + 1)
        self.assertEqual(total["requests"], 2)

    def test_results_that_follow_a_request_count_toward_it(self):
        # Without the 585 result characters the same growth would leave 800 - 515 / 2.2 = 566 tokens.
        thinking_transcript(self.path)
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 300)

    def test_a_us_only_request_is_priced_at_the_us_factor(self):
        thinking_transcript(self.path, inference_geo="us")
        _, out, _ = run("session", str(self.path))
        total = totals(out)["total"]
        self.assertEqual(total["thinking"], 300)
        self.assertAlmostEqual(total["usd_thinking"], 300 * 10.0 / 1e6 * 1.1, places=9)

    def test_redacted_thinking_counts_as_thinking(self):
        thinking_transcript(self.path, block={"type": "redacted_thinking", "data": "opaque"})
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 300)

    def test_a_logged_final_count_already_holds_the_thinking(self):
        # The main session logs 700 output tokens for 100 of visible text: 600 are hidden thinking, which the
        # estimate (600) would book a second time.
        two_opus_requests(self.path, 700)
        _, out, _ = run("session", str(self.path))
        total = totals(out)["total"]
        self.assertEqual(total["thinking"], 0)
        self.assertEqual(total["usd_thinking"], 0)
        self.assertEqual(total["output"], 700 + 5)
        self.assertIn(f"| 0 | ${total['usd']:,.4f} |", out)  # incl. est. equals USD

    def test_a_logged_count_covering_part_of_the_estimate_leaves_the_rest(self):
        two_opus_requests(self.path, 400)  # 300 above the 100-token floor
        _, out, _ = run("session", str(self.path))
        total = totals(out)["total"]
        self.assertEqual(total["thinking"], 300)
        self.assertAlmostEqual(total["usd_thinking"], 300 * 20.0 / 1e6, places=9)

    def test_a_stream_start_count_leaves_the_whole_estimate(self):
        two_opus_requests(self.path, 8)  # raised to the 100-token floor: nothing of it is thinking
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 600)

    def test_a_synthetic_record_ends_the_results_that_belong_to_a_request(self):
        synthetic = json.dumps({"timestamp": "2026-10-14T10:00:02Z", "message": {
            "id": "syn", "role": "assistant", "model": "<synthetic>", "usage": {"input_tokens": 0}}})
        two_opus_requests(self.path, 8, synthetic, tool_results("2026-10-14T10:00:05Z", "r" * 1000))
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 600)  # the 1,000 characters are not m1's

    def test_a_result_outside_the_window_is_not_counted(self):
        # Logged out of order on purpose: the result is later than --until, though m2 is inside the window.
        two_opus_requests(self.path, 8, tool_results("2026-10-14T10:30:00Z", "r" * 1000))
        _, out, _ = run("session", str(self.path), "--until", "2026-10-14T10:00:20Z")
        self.assertEqual(totals(out)["total"]["thinking"], 600)

    def test_hand_back_reports_and_notices_count_toward_the_request_they_follow(self):
        # 220 characters of report and 220 of notice explain 200 of the 600.
        two_opus_requests(self.path, 8,
                          user_string("2026-10-14T10:00:03Z", "h" * 220, isMeta=True),
                          attachment("2026-10-14T10:00:04Z", type="queued_command", prompt="q" * 220,
                                     origin={"kind": "task-notification"}))
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 400)

    def test_an_attachment_outside_the_window_is_not_counted(self):
        # Logged out of order on purpose: later than --until, though m2 is inside the window.
        two_opus_requests(self.path, 8, attachment("2026-10-14T10:30:00Z", type="queued_command", prompt="q" * 1000))
        _, out, _ = run("session", str(self.path), "--until", "2026-10-14T10:00:20Z")
        self.assertEqual(totals(out)["total"]["thinking"], 600)

    def test_list_form_user_text_blocks_count_toward_the_request_they_follow(self):
        # 110 + 110 characters of text parts explain 100 of the 600.
        blocks = [{"type": "text", "text": "u" * 110}, {"type": "text", "text": "v" * 110}, {"type": "image"}]
        record = json.dumps({"timestamp": "2026-10-14T10:00:03Z", "message": {"role": "user", "content": blocks}})
        two_opus_requests(self.path, 8, record)
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 500)

    def test_prompt_snapshots_and_deferred_tool_records_are_skipped_but_hook_briefs_count(self):
        two_opus_requests(
            self.path, 8,
            attachment("2026-10-14T10:00:03Z", type="prompt_snapshot", prompt="p" * 5600, content="c" * 5600),
            attachment("2026-10-14T10:00:04Z", type="deferred_tools_record", content="d" * 5600),
            attachment("2026-10-14T10:00:05Z", type="hook_success", hookEvent="SessionStart", content="h" * 220),
        )
        _, out, _ = run("session", str(self.path))
        self.assertEqual(totals(out)["total"]["thinking"], 500)  # only the 220-character brief explains 100

    def test_session_start_deltas_after_the_first_request_count_toward_it(self):
        delta = dict(type="agent_listing_delta", addedTypes=["x" * 500], addedLines=["a" * 1100], isInitial=True)
        two_opus_requests(self.path, 8, attachment("2026-10-14T10:00:03Z", **delta))
        _, out, _ = run("session", str(self.path))
        sys.path.insert(0, str(SCRIPT.parent))
        import agent_costs

        counted = agent_costs.attachment_chars(delta)
        self.assertGreater(counted, 1100)
        self.assertLess(counted, 1100 + 100)  # the type names are not counted
        self.assertEqual(totals(out)["total"]["thinking"], round(782 - (400 + counted) / 2.2))

    def test_each_agents_ratio_is_carried_in_the_comment(self):
        # Eleven steps without thinking, each 100 characters of text and a 200-character user string: 3.0.
        lines = []
        for n in range(11):
            lines.append(reply(f"w{n}", "claude-opus-5-5", f"2026-10-14T10:00:{n:02d}Z", {"type": "text", "text": "t" * 100},
                               input_tokens=10, output_tokens=25, cache_read_input_tokens=100 * n))
            lines.append(user_string(f"2026-10-14T10:00:{n:02d}Z", "u" * 200))
        self.path.write_text("\n".join(lines))
        _, out, _ = run("session", str(self.path))
        ratio = totals(out)["chars_per_token"]["main session"]
        self.assertAlmostEqual(ratio, 3.0, places=1)

    def test_long_prompt_requests_are_priced_at_the_long_prompt_output_rate(self):
        haiku = "claude-haiku-5-5"
        thinking = {"type": "thinking", "thinking": "", "signature": "sig"}
        self.path.write_text("\n".join([
            reply("h1", haiku, "2026-10-14T10:00:00Z", thinking, input_tokens=1, cache_read_input_tokens=119_999),
            reply("h2", haiku, "2026-10-14T10:00:10Z", {"type": "text", "text": ""}, input_tokens=1,
                  cache_read_input_tokens=120_499),
        ]))
        _, out, _ = run("session", str(self.path))
        total = totals(out)["total"]
        self.assertEqual(total["thinking"], 500)  # the context grew 500 and the request added no characters
        self.assertAlmostEqual(total["usd_thinking"], 500 * 2.50 / 1e6, places=9)  # not Haiku's short-prompt $0.50

    def test_requests_after_the_window_are_left_out(self):
        thinking_transcript(self.path)
        _, out, _ = run("session", str(self.path), "--until", "2026-10-14T10:00:02Z")
        total = totals(out)["total"]
        self.assertEqual(total["requests"], 1)  # a2 is after the window, and so is the result
        self.assertEqual(total["thinking"], 0)  # the only request left is the last step


class ThinkingReportTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def test_the_report_shows_both_columns_and_labels_the_estimate(self):
        path = self.root / "think.jsonl"
        thinking_transcript(path)
        code, out, err = run("session", str(path))
        self.assertEqual(code, 0, err)
        self.assertIn("| Est. thinking | USD incl. est. |", out)
        total = totals(out)["total"]
        including = total["usd"] + total["usd_thinking"]
        self.assertGreater(total["usd_thinking"], 0)
        self.assertIn(f"| 300 | ${including:,.4f} |", out)  # the Total row of "Cost by type", ending the row
        self.assertRegex(out, r"\| main \| 1 \|.*\| 300 \| \$[0-9.,]+ \|")
        self.assertIn("not billing data", out)
        # The logged-only USD column is still there, beside the new one.
        self.assertIn(f"${total['usd']:,.4f}", out)

    def test_records_total_a_comment_written_before_the_new_keys(self):
        row = {"requests": 2, "input": 10, "output": 5, "cache_read": 0, "write_5m": 0, "write_1h": 0,
               "over_100k": 0, "unpriced": 0, "output_estimated": 0, "usd": 1.5}
        old = {"session": "old", "since": None, "until": None, "by_type": {"main": {"agents": 1, **row}}, "total": row}
        record = self.root / "2026-10-13-old.md"
        record.write_text("# Old run\n\n<!-- agent-costs " + json.dumps(old) + " -->\n")
        code, report, err = run("records", str(record))
        self.assertEqual(code, 0, err)
        self.assertIn("Across 1 run\n", report)
        self.assertRegex(report, r"\| main \| 1 \| 1 \| 2 \|")
        self.assertIn("| Est. thinking | USD incl. est. | USD per run |", report)
        self.assertRegex(report, r"\| \$1\.50 \| n/a \| n/a \| \$1\.50 \|")  # no estimate: not a measured zero
        self.assertIn("n/a where no run does", report)
        self.assertIn(f"| {record} | 1 | $1.50 |", report)

    def test_records_sum_the_estimate_only_of_runs_that_carry_one(self):
        path = self.root / "think.jsonl"
        thinking_transcript(path)
        _, out, _ = run("session", str(path))
        row = {"requests": 2, "input": 10, "output": 5, "cache_read": 0, "write_5m": 0, "write_1h": 0,
               "over_100k": 0, "unpriced": 0, "output_estimated": 0, "usd": 1.5}
        old = {"session": "old", "since": None, "until": None, "by_type": {"main": {"agents": 1, **row}}, "total": row}
        record_old, record_new = self.root / "2026-10-13-old.md", self.root / "2026-10-14-new.md"
        record_old.write_text("# Old\n\n<!-- agent-costs " + json.dumps(old) + " -->\n")
        record_new.write_text("# New\n\n" + out)
        code, report, err = run("records", str(record_old), str(record_new))
        self.assertEqual(code, 0, err)
        self.assertRegex(report, r"\| main \| 2 \| 2 \| 4 \|.*\| 300 \| \$[0-9.,]+ \|")

    def test_records_sum_the_new_keys_of_current_comments(self):
        path = self.root / "think.jsonl"
        thinking_transcript(path)
        _, out, _ = run("session", str(path))
        record = self.root / "2026-10-14-new.md"
        record.write_text("# New run\n\n" + out + "\n" + out)
        code, report, err = run("records", str(record))
        self.assertEqual(code, 0, err)
        self.assertRegex(report, r"\| main \| 1 \| 2 \| 4 \|.*\| 600 \| \$[0-9.,]+ \|")


class PricesAndHomeStoreTest(unittest.TestCase):
    def setUp(self):
        sys.path.insert(0, str(SCRIPT.parent))
        import agent_costs

        self.costs = agent_costs
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def write_record(self, key: str, name: str, usd: float) -> Path:
        row = {"requests": 1, "input": 1, "output": 1, "cache_read": 0, "write_5m": 0, "write_1h": 0,
               "over_100k": 0, "unpriced": 0, "output_estimated": 0, "usd": usd}
        data = {"session": name, "since": None, "until": None, "by_type": {"main": {"agents": 1, **row}}, "total": row}
        path = self.root / "records" / key / f"{name}.md"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(f"# {name}\n\n<!-- agent-costs {json.dumps(data)} -->\n")
        return path

    def test_prices_load_from_prices_toml(self):
        self.assertEqual(self.costs.load_prices()[0], self.costs.PRICES)
        table = self.root / "prices.toml"
        table.write_text('us_only = 1.5\n[models]\nclaude-test-1 = [1, 2, 3, 0.1, 5]\n'
                         '[long_prompt.claude-test-1]\nover = 10\nprices = [2, 4, 6, 0.2, 10]\n')
        prices, long_prompt, us_only = self.costs.load_prices(table)
        self.assertEqual(prices, {"claude-test-1": (1.0, 2.0, 3.0, 0.1, 5.0)})
        self.assertEqual(long_prompt, {"claude-test-1": (10, (2.0, 4.0, 6.0, 0.2, 10.0))})
        self.assertEqual(us_only, 1.5)

    def test_a_model_with_no_price_is_reported_against_prices_toml(self):
        transcript = self.root / "unknown.jsonl"
        transcript.write_text(record("m1", "claude-unknown-9", "2026-10-13T10:00:00Z", input_tokens=10, output_tokens=5) + "\n")
        code, out, err = run("session", str(transcript))
        self.assertEqual(code, 0, err)
        self.assertIn("1 requests used a model with no price in prices.toml; their cost is missing.", out)

    def test_records_from_two_repos_get_a_table_each_then_the_pooled_one(self):
        first = self.write_record("-repo-a", "2026-10-16-one", 1.0)
        second = self.write_record("-repo-b", "2026-10-16-two", 2.0)
        code, report, err = run("records", str(first), str(second))
        self.assertEqual(code, 0, err)
        self.assertLess(report.index("## -repo-a"), report.index("## -repo-b"))
        self.assertLess(report.index("## -repo-b"), report.index("## Pooled"))
        self.assertIn("Across 2 runs", report.split("## Pooled")[1])
        section_a = report.split("## -repo-a")[1].split("## -repo-b")[0]
        section_b = report.split("## -repo-b")[1].split("## Pooled")[0]
        self.assertIn("Across 1 run\n", section_a)
        self.assertIn("2026-10-16-one", section_a)
        self.assertNotIn("2026-10-16-two", section_a)
        self.assertIn("Across 1 run\n", section_b)
        self.assertIn("2026-10-16-two", section_b)
        self.assertNotIn("2026-10-16-one", section_b)

    def test_records_from_two_repos_whose_record_dirs_share_a_name_stay_apart(self):
        paths = []
        for repo, usd in (("repo-a", 1.0), ("repo-b", 2.0)):
            path = self.root / repo / "docs" / "agent-runs" / f"{repo}-run.md"
            path.parent.mkdir(parents=True)
            path.write_text(self.write_record("scratch", f"{repo}-run", usd).read_text())
            paths.append(path)
        code, report, err = run("records", *map(str, paths))
        self.assertEqual(code, 0, err)
        self.assertIn("## Pooled", report)
        body = report.split("## Pooled")[0]
        sections = body.split("## ")[1:]
        self.assertEqual(len(sections), 2, report)
        for section in sections:
            self.assertIn("Across 1 run\n", section)
        self.assertNotEqual(sections[0].splitlines()[0], sections[1].splitlines()[0])
        self.assertIn("Across 2 runs", report.split("## Pooled")[1])

    def test_prices_without_us_only_are_refused_naming_prices_toml(self):
        table = self.root / "prices.toml"
        table.write_text("[models]\nclaude-test-1 = [1, 2, 3, 0.1, 5]\n[long_prompt]\n")
        with self.assertRaisesRegex(ValueError, "prices.toml.*us_only"):
            self.costs.load_prices(table)

    def test_a_price_row_that_is_not_five_numbers_is_refused_naming_the_model(self):
        table = self.root / "prices.toml"
        table.write_text("us_only = 1.0\n[models]\nclaude-test-1 = [1, 2, 3, 0.1]\n[long_prompt]\n")
        with self.assertRaisesRegex(ValueError, "prices.toml.*claude-test-1"):
            self.costs.load_prices(table)
        table.write_text("us_only = 1.0\n[models]\nclaude-test-1 = [1, 2, 3, 0.1, 5]\n"
                         "[long_prompt.claude-test-1]\nover = 10\nprices = [2, 4]\n")
        with self.assertRaisesRegex(ValueError, "prices.toml.*claude-test-1"):
            self.costs.load_prices(table)

    def test_a_long_prompt_tier_missing_a_key_is_refused_naming_the_model(self):
        head = "us_only = 1.0\n[models]\nclaude-test-1 = [1, 2, 3, 0.1, 5]\n"
        table = self.root / "prices.toml"
        for tier in ("over = 10\n", "prices = [2, 4, 6, 0.2, 10]\n"):
            table.write_text(head + "[long_prompt.claude-test-1]\n" + tier)
            with self.assertRaisesRegex(ValueError, "prices.toml.*claude-test-1"):
                self.costs.load_prices(table)

    def test_a_long_prompt_tier_with_a_non_numeric_value_is_refused_naming_the_model(self):
        head = "us_only = 1.0\n[models]\nclaude-test-1 = [1, 2, 3, 0.1, 5]\n[long_prompt.claude-test-1]\n"
        table = self.root / "prices.toml"
        for tier in ('over = "lots"\nprices = [2, 4, 6, 0.2, 10]\n', 'over = 10\nprices = [2, 4, 6, 0.2, "x"]\n'):
            table.write_text(head + tier)
            with self.assertRaisesRegex(ValueError, "prices.toml.*claude-test-1"):
                self.costs.load_prices(table)

    def test_prices_without_a_models_or_long_prompt_table_are_refused(self):
        table = self.root / "prices.toml"
        table.write_text("us_only = 1.0\n[long_prompt]\n")
        with self.assertRaisesRegex(ValueError, "prices.toml.*models"):
            self.costs.load_prices(table)
        table.write_text("us_only = 1.0\n[models]\nclaude-test-1 = [1, 2, 3, 0.1, 5]\n")
        with self.assertRaisesRegex(ValueError, "prices.toml.*long_prompt"):
            self.costs.load_prices(table)

    def test_records_without_files_read_flow_data_and_leave_out_retros(self):
        self.write_record("-repo-a", "2026-10-16-one", 1.0)
        (self.root / "records" / "retros").mkdir()
        (self.root / "records" / "retros" / "retro-2026-10-16.md").write_text("# Retro\n")
        result = subprocess.run([sys.executable, str(SCRIPT), "records"], capture_output=True, text=True,
                                env={**os.environ, "FLOW_DATA": str(self.root)})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Across 1 run\n", result.stdout)
        self.assertNotIn("retro", result.stderr)

    def test_records_named_from_the_records_dir_are_files_not_options(self):
        # The retro lists records as <key>/<file>.md, and every key starts with '-' (feedback thread 21).
        self.write_record("-repo-a", "2026-10-16-one", 1.0)
        self.write_record("-repo-a", "2026-10-16-two", 2.0)
        names = ["-repo-a/2026-10-16-one.md", "-repo-a/2026-10-16-two.md"]
        elsewhere = self.root / "elsewhere"
        elsewhere.mkdir()
        for cwd in (self.root / "records", elsewhere):
            with self.subTest(cwd=cwd.name):
                result = subprocess.run([sys.executable, str(SCRIPT), "records", *names], capture_output=True,
                                        text=True, cwd=cwd, env={**os.environ, "FLOW_DATA": str(self.root)})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("Across 2 runs\n", result.stdout)

    def test_records_still_answers_help(self):
        code, out, _ = run("records", "--help")
        self.assertEqual(code, 0)
        self.assertIn("usage:", out)


if __name__ == "__main__":
    unittest.main()


def run_fixture(root: Path, agents: dict[str, dict], transcripts: dict[str, list[str]]):
    """A repo with run r (manager msess) naming agents (name: record), and transcripts (session: records) in a
    config dir; returns (repo, env)."""
    repo = root / "repo"
    repo.mkdir()
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True, env=env)
    state = repo / ".git" / "flow"
    (state / "runs").mkdir(parents=True)
    (state / "agents").mkdir()
    (state / "runs" / "r.json").write_text(json.dumps({
        "managers": ["msess"], "agents": list(agents), "start": "2026-11-06T10:00:00Z"}))
    for name, rec in agents.items():
        (state / "agents" / f"{name}.json").write_text(json.dumps(rec))
    cfg = root / "cfg"
    (cfg / "projects" / "p").mkdir(parents=True)
    for sid, lines in transcripts.items():
        (cfg / "projects" / "p" / f"{sid}.jsonl").write_text("\n".join(lines))
    return repo, {**env, "CLAUDE_CONFIG_DIR": str(cfg)}


class RunJSONTest(unittest.TestCase):
    """`run --json`: the board's figures (flow visualisation spec §3)."""

    TRANSCRIPTS = {
        "msess": [record("m1", "claude-opus-5-5", "2026-11-06T10:05:00Z", input_tokens=1000, output_tokens=100)],
        "dsess": [record("d1", "claude-sonnet-5-5", "2026-11-06T10:10:00Z", input_tokens=1000, output_tokens=100)],
        "rsess": [record("v1", "claude-opus-5-5", "2026-11-06T10:20:00Z", input_tokens=2000, output_tokens=200)],
    }

    def run_json(self, agents, *extra):
        with tempfile.TemporaryDirectory() as tmp:
            repo, env = run_fixture(Path(tmp), agents, self.TRANSCRIPTS)
            result = subprocess.run([sys.executable, str(SCRIPT), "run", "r", "--json", *extra], capture_output=True,
                                    text=True, cwd=repo, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)

    def test_rows_in_dispatch_order_with_their_running_flags(self):
        out = self.run_json({"r-s1-dev1": {"role": "intermediate-dev", "session": "dsess", "status": "DONE"},
                             "r-s1-review1": {"role": "code-quality-reviewer", "session": "rsess", "status": "running"}})
        self.assertEqual([a["name"] for a in out["agents"]], ["r-s1-dev1", "r-s1-review1"])
        self.assertEqual([a["running"] for a in out["agents"]], [False, True])
        self.assertEqual(out["agents"][0]["model"], "claude-sonnet-5-5")
        self.assertGreater(out["agents"][1]["usd"], out["agents"][0]["usd"])
        self.assertEqual(out["managers"][0]["session"], "msess")
        self.assertEqual(out["managers"][0]["role"], "manager")
        summed = sum(a["usd"] for a in out["agents"]) + out["managers"][0]["usd"]
        self.assertAlmostEqual(out["total"]["usd"], summed)
        self.assertIsNone(out["until"], "a run with no end is priced to now")

    def test_an_agent_with_no_session_is_listed_unpriced_and_a_hint_prices_it(self):
        agents = {"r-s1-dev1": {"role": "intermediate-dev", "status": "running"}}
        out = self.run_json(agents)
        self.assertIsNone(out["agents"][0]["usd"])
        self.assertTrue(out["agents"][0]["running"])
        out = self.run_json(agents, "--session", "r-s1-dev1=dsess")
        self.assertGreater(out["agents"][0]["usd"], 0)

    def test_a_hint_without_an_id_is_a_usage_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, env = run_fixture(Path(tmp), {}, self.TRANSCRIPTS)
            result = subprocess.run([sys.executable, str(SCRIPT), "run", "r", "--json", "--session", "r-s1-dev1"],
                                    capture_output=True, text=True, cwd=repo, env=env)
            self.assertEqual(result.returncode, 2)
            self.assertIn("NAME=ID", result.stderr)


class DesignTest(unittest.TestCase):
    """`design`: the design session's cost (flow visualisation spec §3)."""

    def test_prices_only_the_transcripts_that_wrote_the_spec_up_to_the_plans_commit(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            repo = root / "repo"
            (repo / "docs").mkdir(parents=True)
            env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
            env.update(GIT_AUTHOR_DATE="2026-11-09T12:00:00Z", GIT_COMMITTER_DATE="2026-11-09T12:00:00Z")
            git = ["git", "-C", str(repo), "-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false"]
            subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True, env=env)
            (repo / "docs" / "plan.md").write_text("# plan\n")
            subprocess.run([*git, "add", "docs/plan.md"], check=True, env=env)
            subprocess.run([*git, "commit", "-q", "-m", "plan"], check=True, env=env)
            spec = repo / "docs" / "x-design.md"
            write = {"type": "tool_use", "name": "Write", "input": {"file_path": str(spec), "content": "# x"}}
            cfg = root / "cfg"
            top = subprocess.run(["git", "-C", str(repo), "rev-parse", "--show-toplevel"], capture_output=True, text=True).stdout.strip()
            project = cfg / "projects" / re.sub(r"[^A-Za-z0-9]", "-", top)
            project.mkdir(parents=True)
            (project / "writer.jsonl").write_text("\n".join([
                reply("w1", "claude-opus-5-5", "2026-11-09T11:00:00Z", write, input_tokens=1000, output_tokens=100),
                record("w2", "claude-opus-5-5", "2026-11-09T13:00:00Z", input_tokens=1000, output_tokens=100),  # after the plan
            ]))
            (project / "other.jsonl").write_text(
                record("o1", "claude-opus-5-5", "2026-11-09T11:00:00Z", input_tokens=1000, output_tokens=100))
            result = subprocess.run([sys.executable, str(SCRIPT), "design", "docs/x-design.md", "--plan", "docs/plan.md", "--json"],
                                    capture_output=True, text=True, cwd=repo, env={**env, "CLAUDE_CONFIG_DIR": str(cfg)})
            self.assertEqual(result.returncode, 0, result.stderr)
            out = json.loads(result.stdout)
            self.assertEqual([s["session"] for s in out["sessions"]], ["writer"])
            self.assertEqual(out["until"], "2026-11-09T12:00:00+00:00")
            one = json.loads(subprocess.run([sys.executable, str(SCRIPT), "design", "docs/x-design.md", "--json"],
                                            capture_output=True, text=True, cwd=repo,
                                            env={**env, "CLAUDE_CONFIG_DIR": str(cfg)}).stdout)
            self.assertGreater(one["total"]["usd"], out["total"]["usd"], "without a plan, every request counts")
