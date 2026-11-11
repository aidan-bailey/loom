#!/usr/bin/env python3
"""Exact token usage and API-price cost for a Claude Code session's agents.

Claude Code keeps every agent's transcript on disk: the main conversation at
<projects>/<project>/<session>.jsonl, and each subagent at
<projects>/<project>/<session>/subagents/agent-<id>.jsonl beside a .meta.json
naming its type. Every assistant message records its request's usage — input,
output, cache reads and cache writes split by lifetime — and its model. This
script prices that usage and groups it by agent and by agent type. A message's
usage repeats on each content block it was logged with, so each message id
counts once, at its largest values. A subagent's transcript logs output tokens
as the reply starts (8 tokens for a 16,655-character report, 2026-10-13), so a
message's output is never counted below its visible text at 4 characters a
token, and the report says how many requests were estimated. Retained thinking
is logged as a signature with no text, so it is estimated separately from each
request's context growth (see thinking_estimate) into its own columns, and the
logged-only totals stay so records before and after compare. Where a transcript
logs a request's final output count it already holds that request's thinking, so
only the estimate beyond it is booked. It reads files only and costs no model tokens.

    agent_costs.py session [SESSION] [--since ISO] [--until ISO]
    agent_costs.py records [FILE ...]
    agent_costs.py run RUN [--json] [--session NAME=ID ...]
    agent_costs.py design SPEC [--plan PLAN] [--json]

`session` prints markdown tables and closes with an `<!-- agent-costs {...} -->`
comment carrying the same totals as JSON. SESSION is a session id, a session
transcript (.jsonl) or its directory; without one, the newest session of the
current worktree. `records` reads those comments from run records and totals
them across runs, for a retro: without FILEs, every record in the home store
($FLOW_DATA/records/<key>/*.md). Records from more than one repo directory get
a table each, then a pooled one. A run that spanned several manager sessions
carries one comment per session, and its sessions are summed into one run. `run` prices a loom flow
run (flow in loom part 3b): its manager sessions as `main` and each headless agent run by its role,
over the run's window, read from the run's state file and the agent records it names; a second
`<!-- agent-costs-managers {...} -->` comment gives each manager session's models. `run --json` prints the
same figures as one object for loom's flow board: a row per agent run (priced to now while the run has no end,
marked running while its record says so, usd null with no transcript) and per manager session; `--session`
names the session of an agent run whose record has none yet. `design` prices the design session: the
transcripts in the worktree's project dir that wrote SPEC, up to the commit that added PLAN.

Prices are Anthropic API list prices, read from prices.toml beside this script;
on a subscription read them as relative weights for comparing agents, not as a bill.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from dataclasses import asdict, dataclass, fields
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from flowconf import flow_data  # noqa: E402

import tomllib  # noqa: E402  (after flowconf, whose Python-version message comes first)

PRICES_FILE = Path(__file__).with_name("prices.toml")


def load_prices(
    path: Path = PRICES_FILE,
) -> tuple[dict[str, tuple[float, ...]], dict[str, tuple[int, tuple[float, ...]]], float]:
    """The price table, the long-prompt tiers and the US-only multiplier, from prices.toml: the one
    place to update them. Prices are USD per million tokens: input, 5-minute cache write, 1-hour
    cache write, cache read, output."""
    data = tomllib.loads(path.read_text())
    for key in ("us_only", "models", "long_prompt"):  # long_prompt may be an empty table, but it must be there
        if key not in data:
            raise ValueError(f"{path}: no `{key}` (prices.toml needs it; nothing defaults)")

    def row(model: str, values: object) -> tuple[float, ...]:
        wrong = (f"{path}: the price row of {model} must be 5 numbers "
                 "(input, 5-minute cache write, 1-hour cache write, cache read, output)")
        if not isinstance(values, list) or len(values) != 5:
            raise ValueError(wrong)
        try:
            return tuple(float(x) for x in values)
        except (TypeError, ValueError):
            raise ValueError(wrong) from None

    prices = {name: row(name, values) for name, values in data["models"].items()}

    def tier_of(model: str, tier: object) -> tuple[int, tuple[float, ...]]:
        if not isinstance(tier, dict) or "over" not in tier or "prices" not in tier:
            raise ValueError(f"{path}: the long_prompt tier of {model} needs `over` and `prices`")
        over = tier["over"]
        if isinstance(over, bool) or not isinstance(over, int):
            raise ValueError(f"{path}: the long_prompt `over` of {model} must be an integer token count")
        return over, row(model, tier["prices"])

    long_prompt = {name: tier_of(name, tier) for name, tier in data["long_prompt"].items()}
    return prices, long_prompt, float(data["us_only"])


PRICES, LONG_PROMPT, US_ONLY = load_prices()
COMMENT_RE = re.compile(r"<!-- agent-costs (\{.*?\}) -->", re.S)
CHARS_PER_TOKEN = 4  # visible-output floor: characters of reply text per output token
# Characters added per token of context growth, measured 2026-10-14 on steps without thinking (2.1-2.4).
DEFAULT_CHARS_PER_TOKEN = 2.2
MIN_RATIO_STEPS = 10  # steps without thinking an agent needs before its own ratio replaces the default
# A measured ratio outside this band (characters per token) means the steps' text is only part of what entered
# the context, so it is not believed and the default stands.
MIN_CHARS_PER_TOKEN, MAX_CHARS_PER_TOKEN = 1.5, 4.0
# Attachment fields whose strings enter the context, wherever they sit in the attachment. A `*_delta`
# attachment (mcp_instructions, agent_listing, deferred_tools) is counted whole, less its name lists.
ATTACHMENT_TEXT_KEYS = frozenset({"text", "content", "prompt", "snippet"})
# Attachments that log tool schemas or prompt text (about 5.6K characters under prompt/content) the model
# did not read as conversation.
ATTACHMENT_SKIPPED_TYPES = frozenset({"prompt_snapshot", "deferred_tools_record"})


@dataclass
class Usage:
    """Token counts and cost summed over requests."""

    requests: int = 0
    input: int = 0
    output: int = 0
    cache_read: int = 0
    write_5m: int = 0
    write_1h: int = 0
    over_100k: int = 0
    unpriced: int = 0
    output_estimated: int = 0
    usd: float = 0.0
    thinking: int = 0  # estimated hidden output tokens, from context growth; not in `output`
    usd_thinking: float = 0.0  # `thinking` at the model's output rate; not in `usd`

    def add(self, other: Usage) -> None:
        for f in fields(self):
            setattr(self, f.name, getattr(self, f.name) + getattr(other, f.name))


def price_of(model: str) -> tuple[str, tuple[float, ...]] | None:
    for name, prices in PRICES.items():
        if model.startswith(name):
            return name, prices
    return None


def is_long_prompt(model: str, prompt: int) -> bool:
    """Whether a prompt of this many tokens is priced at the model's long-prompt tier."""
    priced = price_of(model)
    return bool(priced and priced[0] in LONG_PROMPT and prompt > LONG_PROMPT[priced[0]][0])


def tier_prices(model: str, long_prompt: bool) -> tuple[float, ...] | None:
    """A model's prices at the tier a request's prompt length put it in (None for an unpriced model)."""
    priced = price_of(model)
    if priced is None:
        return None
    name, prices = priced
    return LONG_PROMPT[name][1] if long_prompt and name in LONG_PROMPT else prices


def geo_factor(usage: dict) -> float:
    """The price multiplier of a request's inference geography."""
    return US_ONLY if usage.get("inference_geo") == "us" else 1.0


def request_usage(model: str, usage: dict) -> Usage:
    """Usage and cost of one request."""

    def count(key: str, source: dict = usage) -> int:
        value = source.get(key)
        return value if isinstance(value, int) else 0

    creation = usage.get("cache_creation")
    if isinstance(creation, dict):
        write_5m, write_1h = count("ephemeral_5m_input_tokens", creation), count("ephemeral_1h_input_tokens", creation)
    else:
        write_5m, write_1h = count("cache_creation_input_tokens"), 0
    u = Usage(1, count("input_tokens"), count("output_tokens"), count("cache_read_input_tokens"), write_5m, write_1h)
    long_prompt = is_long_prompt(model, u.input + u.cache_read + u.write_5m + u.write_1h)
    prices = tier_prices(model, long_prompt)
    if prices is None:
        u.unpriced = 1
        return u
    u.over_100k = int(long_prompt)
    p_in, p_5m, p_1h, p_read, p_out = prices
    usd = u.input * p_in + u.write_5m * p_5m + u.write_1h * p_1h + u.cache_read * p_read + u.output * p_out
    u.usd = usd / 1e6 * geo_factor(usage)
    return u


def parse_time(value: str | None) -> datetime | None:
    return datetime.fromisoformat(value.replace("Z", "+00:00")) if value else None


def thinking_estimate(steps: list[tuple[int, int, bool]]) -> tuple[list[int], float]:
    """Estimated hidden output tokens of each request, and the characters-per-token ratio used.

    `steps` is each request in order as (context, chars_added, has_thinking): the context is input plus
    cache reads plus cache writes; chars_added is the request's visible text and tool input plus the tool
    results that follow it. A request's growth is the next request's context less its own. The ratio is
    sum(chars_added) / sum(growth) over the steps without thinking whose growth is positive, when there
    are at least MIN_RATIO_STEPS of them and it lies within MIN_CHARS_PER_TOKEN..MAX_CHARS_PER_TOKEN,
    else DEFAULT_CHARS_PER_TOKEN. A step with thinking gets its
    growth less chars_added / ratio, never below 0; every other step, and the last, gets 0.
    """
    growth = [steps[i + 1][0] - steps[i][0] for i in range(len(steps) - 1)]
    sample = [i for i, g in enumerate(growth) if g > 0 and not steps[i][2]]
    ratio = DEFAULT_CHARS_PER_TOKEN
    if len(sample) >= MIN_RATIO_STEPS:
        measured = sum(steps[i][1] for i in sample) / sum(growth[i] for i in sample)
        if MIN_CHARS_PER_TOKEN <= measured <= MAX_CHARS_PER_TOKEN:
            ratio = measured
    estimates = [0] * len(steps)
    for i, g in enumerate(growth):
        if steps[i][2]:
            estimates[i] = max(0, round(g - steps[i][1] / ratio))
    return estimates, ratio


def has_thinking(content: object) -> bool:
    """Whether one logged content list holds a thinking block (its text is empty; only the signature is kept)."""
    return isinstance(content, list) and any(
        isinstance(block, dict) and block.get("type") in ("thinking", "redacted_thinking") for block in content
    )


def result_chars(content: object) -> int:
    """Characters of the tool_result blocks in one logged user content list (a string or a list of text parts)."""
    if not isinstance(content, list):
        return 0
    chars = 0
    for block in content:
        if not isinstance(block, dict) or block.get("type") != "tool_result":
            continue
        body = block.get("content")
        if isinstance(body, str):
            chars += len(body)
        elif isinstance(body, list):
            chars += sum(len(part.get("text") or "") for part in body
                         if isinstance(part, dict) and part.get("type") == "text")
    return chars


def user_chars(content: object) -> int:
    """Characters one logged user record adds to the context: a string, or its text and tool_result blocks.

    Hand-back reports and notices of finished subagents arrive as string records (isMeta among them), so
    they count like any other user text.
    """
    if isinstance(content, str):
        return len(content)
    if not isinstance(content, list):
        return 0
    text = sum(len(block.get("text") or "") for block in content
               if isinstance(block, dict) and block.get("type") == "text")
    return text + result_chars(content)


def _text_chars(node: object, counting: bool) -> int:
    """Characters of the strings under an attachment node, counted once inside an ATTACHMENT_TEXT_KEYS key."""
    if isinstance(node, str):
        return len(node) if counting else 0
    if isinstance(node, list):
        return sum(_text_chars(item, counting) for item in node)
    if isinstance(node, dict):
        return sum(_text_chars(value, counting or key in ATTACHMENT_TEXT_KEYS) for key, value in node.items())
    return 0


def attachment_chars(attachment: object) -> int:
    """Characters one logged attachment adds to the context.

    A `*_delta` attachment (session-start listings of MCP instructions, agents and deferred tools, 5-25K
    characters each) is counted whole, less its name lists, which only repeat its lines. Any other
    attachment counts the text it carries under ATTACHMENT_TEXT_KEYS: a queued_command's prompt (a finished
    subagent's notice), a skill_listing's or hook_additional_context's content, an invoked skill's body,
    a file's content, a loaded CLAUDE.md's files, an edit's snippet and the small reminders. A hook_success
    counts too: its SessionStart brief is read as context. prompt_snapshot and deferred_tools_record are
    skipped by type, since their schema text is not conversation, and environment carries no text.
    """
    if not isinstance(attachment, dict) or attachment.get("type") in ATTACHMENT_SKIPPED_TYPES:
        return 0
    if str(attachment.get("type") or "").endswith("_delta"):
        kept = {k: v for k, v in attachment.items() if not k.endswith(("Names", "Types"))}
        return len(json.dumps(kept))
    return _text_chars(attachment, False)


def transcript_usage(path: Path, since: datetime | None, until: datetime | None) -> tuple[Usage, set[str], float]:
    """Sum one transcript's requests, each message id once at its largest values; also the ratio used.

    Each request's hidden thinking is estimated by thinking_estimate from the context growth to the next
    request, and priced at the model's output rate into usd_thinking. A logged final output count already
    holds the request's thinking, so the part of it above the visible text is taken off the estimate.
    """
    messages: dict[str, tuple[str, dict]] = {}
    visible: dict[str, int] = {}
    results: dict[str, int] = {}  # tool-result characters that followed each message id
    thinking: set[str] = set()
    last: str | None = None  # the request the next tool results belong to
    for line in path.read_text(errors="replace").splitlines():
        try:
            record = json.loads(line)
        except ValueError:
            continue
        if not isinstance(record, dict):
            continue
        message = record.get("message")
        when = parse_time(record.get("timestamp"))
        outside = bool(when and ((since and when < since) or (until and when > until)))
        if not isinstance(message, dict):
            if record.get("type") == "attachment" and last is not None and not outside:
                results[last] = results.get(last, 0) + attachment_chars(record.get("attachment"))
            continue
        if message.get("role") == "user":
            if last is not None and not outside:
                results[last] = results.get(last, 0) + user_chars(message.get("content"))
            continue
        if message.get("role") != "assistant":
            continue
        usage, model = message.get("usage"), message.get("model") or "?"
        if not isinstance(usage, dict) or model == "<synthetic>" or outside:
            last = None
            continue
        key = message.get("id") or record.get("uuid") or str(len(messages))
        last = key
        visible[key] = visible.get(key, 0) + visible_chars(message.get("content"))
        if has_thinking(message.get("content")):
            thinking.add(key)
        if key in messages:
            kept = messages[key][1]
            for k, v in usage.items():
                if isinstance(v, int) and v > kept.get(k, 0):
                    kept[k] = v
        else:
            messages[key] = (model, dict(usage))
    total, models = Usage(), set()
    requests: list[tuple[Usage, dict, str, int]] = []
    steps: list[tuple[int, int, bool]] = []
    for key, (model, usage) in messages.items():
        floor = -(-visible.get(key, 0) // CHARS_PER_TOKEN)
        logged = usage.get("output_tokens", 0)
        estimated = floor > logged
        if estimated:
            usage["output_tokens"] = floor
        request = request_usage(model, usage)
        request.output_estimated = int(estimated)
        requests.append((request, usage, model, max(0, logged - floor)))
        context = request.input + request.cache_read + request.write_5m + request.write_1h
        steps.append((context, visible.get(key, 0) + results.get(key, 0), key in thinking))
        models.add(model)
    estimates, ratio = thinking_estimate(steps)
    for (request, usage, model, hidden), estimate in zip(requests, estimates):
        request.thinking = max(0, estimate - hidden)  # the logged count above the visible floor is already in output
        prices = tier_prices(model, bool(request.over_100k))
        if prices is not None:
            request.usd_thinking = request.thinking * prices[4] / 1e6 * geo_factor(usage)
        total.add(request)
    return total, models, ratio


def transcript_effort(path: Path, since: datetime | None, until: datetime | None) -> str:
    """The effort a transcript's requests ran at, as Claude Code logs it on each assistant record: one level,
    or several in the order they first appear; "" when the transcript logs none. A retro reads it beside the
    agent's pin: `--agent` once ran headless agents at the account's effort, whatever their definition said."""
    seen: list[str] = []
    for line in path.read_text(errors="replace").splitlines():
        try:
            record = json.loads(line)
        except ValueError:
            continue
        if not isinstance(record, dict) or not isinstance(record.get("message"), dict):
            continue
        message, effort = record["message"], record.get("effort")
        if message.get("role") != "assistant" or message.get("model") == "<synthetic>" or not isinstance(effort, str):
            continue
        when = parse_time(record.get("timestamp"))
        if when and ((since and when < since) or (until and when > until)):
            continue
        if effort not in seen:
            seen.append(effort)
    return ", ".join(seen)


def visible_chars(content: object) -> int:
    """Characters of text, visible thinking and tool input in one logged content list."""
    if not isinstance(content, list):
        return 0
    chars = 0
    for block in content:
        if not isinstance(block, dict):
            continue
        if block.get("type") == "text":
            chars += len(block.get("text") or "")
        elif block.get("type") == "thinking":
            chars += len(block.get("thinking") or "")
        elif block.get("type") == "tool_use":
            chars += len(json.dumps(block.get("input") or {}))
    return chars


def projects_root() -> Path:
    return Path(os.environ.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude") / "projects"


def find_session(arg: str | None) -> Path:
    """The main transcript (<session>.jsonl) a SESSION argument names."""
    if arg:
        path = Path(arg).expanduser()
        if path.is_file() and path.suffix == ".jsonl":
            return path
        if path.is_dir() and path.with_suffix(".jsonl").is_file():
            return path.with_suffix(".jsonl")
        matches = sorted(projects_root().glob(f"*/{arg}.jsonl"))
        if len(matches) == 1:
            return matches[0]
        sys.exit(f"agent_costs: {len(matches)} session transcripts match {arg!r}")
    top = subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True).stdout.strip()
    project = projects_root() / re.sub(r"[^A-Za-z0-9]", "-", top or os.getcwd())
    sessions = sorted(project.glob("*.jsonl"), key=lambda p: p.stat().st_mtime)
    if not sessions:
        sys.exit(f"agent_costs: no session transcripts under {project}")
    return sessions[-1]


def run_inputs(run: str, hints: dict[str, str] | None = None) -> tuple[dict, list, list]:
    """A loom flow run's state and its transcripts, from <state>/runs/RUN.json and the agent records it names:
    each manager session as (label, session id, its entries: its own transcript as `main`, then its subagents'),
    and each agent run as (name, role, transcript or None, status). An agent run whose record has no session yet
    takes its session from hints (name: session id). What can't be read is said on stderr, as run_report says it."""
    import flowconf  # noqa: PLC0415  (flow_data is imported above; the rest only here)

    hints = hints or {}
    top = flowconf.toplevel(Path.cwd())
    state = flowconf.state_dir(top, flowconf.optional_profile(top))
    try:
        st = json.loads((state / "runs" / f"{run}.json").read_text())
    except (OSError, ValueError) as err:
        sys.exit(f"agent_costs: the run {run}: {err}")
    managers: list[tuple[str, str, list[tuple[str, str, Path, bool]]]] = []
    for sid in st.get("managers") or []:
        path = locate_session(sid)
        if path is None:
            print(f"agent_costs: no transcript for manager session {sid}", file=sys.stderr)
            continue
        label = f"manager {sid[:8]}"
        managers.append((label, sid, [("main", label, path, True), *subagent_entries(path)]))
    agents: list[tuple[str, str, Path | None, str]] = []
    for name in st.get("agents") or []:
        try:
            rec = json.loads((state / "agents" / f"{name}.json").read_text())
        except (OSError, ValueError) as err:
            print(f"agent_costs: the agent run {name}: {err}", file=sys.stderr)
            agents.append((name, "?", None, ""))
            continue
        path = locate_session(rec.get("session") or hints.get(name, ""))
        if path is None:
            print(f"agent_costs: no transcript for agent run {name}", file=sys.stderr)
        agents.append((name, rec.get("role", "?"), path, rec.get("status", "")))
    return st, managers, agents


def run_report(run: str) -> str:
    """A loom flow run's agents (flow in loom part 3b): each manager session and its subagents as `main`,
    and each headless agent run by its role, over the run's window, from <state>/runs/RUN.json and the
    agent records it names. The comment carries the run and each manager session's models."""
    st, managers, agents = run_inputs(run)
    since, until = parse_time(st.get("start")), parse_time(st.get("end"))
    entries: list[tuple[str, str, Path, bool]] = []
    for _, _, own in managers:
        entries += own
    entries += [(role, name, path, True) for name, role, path, _ in agents if path is not None]
    models_of: dict[str, set[str]] = {}
    report = agents_report(f"Run `{run}`" + (f", {since.isoformat()} to {until.isoformat() if until else 'end'}" if since else ""),
                           entries, since, until, {"session": run, "run": run}, models_of)
    # The managers' models go in a comment of their own: the totals comment's shape stays the one records reads.
    return report + "\n" + f"<!-- agent-costs-managers {json.dumps({sid: sorted(models_of.get(label, ())) for label, sid, _ in managers}, sort_keys=True)} -->"


def usd_cells(usage: Usage) -> dict:
    return {"usd": usage.usd, "usd_incl_est": usage.usd + usage.usd_thinking}


def run_json(run: str, hints: dict[str, str]) -> str:
    """run_report's figures as one JSON object (flow visualisation spec §3): a row per agent run, in dispatch
    order, priced to now while the run has no end and marked running while its record says so, with usd null
    for one no transcript was found for; a row per manager session, its subagents summed into it; the total."""
    st, managers, agents = run_inputs(run, hints)
    since, until = parse_time(st.get("start")), parse_time(st.get("end"))
    total = Usage()
    mans = []
    for label, sid, own in managers:
        summed, models = Usage(), set()
        for _, _, path, _ in own:
            usage, used, _ = transcript_usage(path, since, until)
            summed.add(usage)
            models |= used
        total.add(summed)
        mans.append({"name": label, "role": "manager", "session": sid, "model": ", ".join(sorted(models)), **usd_cells(summed)})
    rows = []
    for name, role, path, status in agents:
        row = {"name": name, "role": role, "model": "", "usd": None, "usd_incl_est": None, "running": status == "running"}
        if path is not None:
            usage, models, _ = transcript_usage(path, since, until)
            total.add(usage)
            row.update(model=", ".join(sorted(models)), **usd_cells(usage))
        rows.append(row)
    return json.dumps({"run": run, "since": st.get("start"), "until": st.get("end"), "agents": rows,
                       "managers": mans, "total": usd_cells(total)}, sort_keys=True)


# The tools whose input names a file the session wrote.
WRITE_TOOLS = ("Write", "Edit", "MultiEdit", "NotebookEdit")


def wrote(path: Path, target: Path) -> bool:
    """Whether transcript path holds a Write or Edit of target."""
    try:
        text = path.read_text(errors="replace")
    except OSError:
        return False
    for line in text.splitlines():
        if '"tool_use"' not in line:
            continue
        try:
            content = (json.loads(line).get("message") or {}).get("content") or []
        except ValueError:
            continue
        for block in content if isinstance(content, list) else []:
            if not isinstance(block, dict) or block.get("type") != "tool_use" or block.get("name") not in WRITE_TOOLS:
                continue
            named = (block.get("input") or {}).get("file_path") or (block.get("input") or {}).get("notebook_path")
            if isinstance(named, str) and named and Path(named).expanduser().resolve() == target:
                return True
    return False


def design_json(spec: str, plan: str | None) -> dict:
    """The design session's cost (flow visualisation spec §3): the transcripts in the worktree's project dir that
    wrote the spec, each priced from its first request up to the commit that added the plan (now while it isn't
    committed)."""
    top = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True).stdout.strip() or os.getcwd())
    target = (top / spec).resolve()
    until = None
    if plan:
        added = subprocess.run(["git", "-C", str(top), "log", "-1", "--diff-filter=A", "--format=%cI", "--", plan],
                               capture_output=True, text=True).stdout.strip()
        until = parse_time(added) if added else None
    # The worktree's spellings: Claude names a project dir after the cwd it ran in, which may be a symlinked path.
    spellings = {str(top), os.getcwd(), os.path.realpath(top), os.path.realpath(os.getcwd())}
    projects = sorted({projects_root() / re.sub(r"[^A-Za-z0-9]", "-", s) for s in spellings})
    sessions, total = [], Usage()
    for project in projects:
        for path in sorted(project.glob("*.jsonl")):
            if not wrote(path, target):
                continue
            usage, _, _ = transcript_usage(path, None, until)
            total.add(usage)
            sessions.append({"session": path.stem, **usd_cells(usage)})
    return {"spec": spec, "until": until.isoformat() if until else None, "sessions": sessions, "total": usd_cells(total)}


def locate_session(sid: str) -> Path | None:
    """The transcript of session sid under the projects root, or None."""
    if not sid:
        return None
    matches = sorted(projects_root().glob(f"*/{sid}.jsonl"))
    return matches[0] if len(matches) == 1 else None


def fmt(n: float, usd: bool = False) -> str:
    if usd:
        return f"${n:,.4f}" if n < 1 else f"${n:,.2f}"
    return f"{n:,}"


def usage_cells(u: Usage, estimated: bool = True) -> list[str]:
    """A table row's cells; the two estimate cells read n/a when no run behind the row carries an estimate."""
    estimate = [fmt(u.thinking), fmt(u.usd + u.usd_thinking, usd=True)] if estimated else ["n/a", "n/a"]
    return [fmt(u.requests), fmt(u.input), fmt(u.output), fmt(u.cache_read), fmt(u.write_5m),
            fmt(u.write_1h), fmt(u.over_100k), fmt(u.usd, usd=True), *estimate]


USAGE_HEADER = ("Requests | Input | Output | Cache read | Write 5m | Write 1h | Over 100K | USD"
                " | Est. thinking | USD incl. est.")
USAGE_COLUMNS = len(USAGE_HEADER.split(" | "))


def subagent_entries(main: Path) -> list[tuple[str, str, Path, bool]]:
    """A main transcript's subagents, as agents_report takes them: type, label, transcript, kept when idle."""
    entries = []
    for path in sorted((main.with_suffix("") / "subagents").glob("agent-*.jsonl")):
        meta_path = path.with_name(path.stem + ".meta.json")
        meta = json.loads(meta_path.read_text()) if meta_path.is_file() else {}
        label = f"{path.stem.removeprefix('agent-')[:7]} {meta.get('description', '')}".strip()
        entries.append((meta.get("agentType", "?"), label, path, False))
    return entries


def session_report(main: Path, since: datetime | None, until: datetime | None) -> str:
    entries = [("main", "main session", main, True), *subagent_entries(main)]
    title = f"Session `{main.stem}`" + (f", {since.isoformat()} to {until.isoformat() if until else 'end'}" if since else "")
    return agents_report(title, entries, since, until, {"session": main.stem})


def agents_report(title: str, entries: list[tuple[str, str, Path, bool]], since: datetime | None,
                  until: datetime | None, extra: dict, models_of: dict[str, set[str]] | None = None) -> str:
    """The tables and the agent-costs comment over entries (type, label, transcript, kept when idle); extra
    goes into the comment's data, and models_of, when given, gets each entry's models by label."""
    agents: list[tuple[str, str, Usage, set[str], str]] = []
    ratios: dict[str, float] = {}
    for kind, label, path, keep in entries:
        usage, models, ratio = transcript_usage(path, since, until)
        if models_of is not None:
            models_of[label] = models
        if keep or usage.requests:
            agents.append((kind, label, usage, models, transcript_effort(path, since, until)))
            ratios[label] = ratio
    by_type: dict[str, tuple[int, Usage]] = {}
    total = Usage()
    for kind, _, usage, _, _ in agents:
        count, summed = by_type.get(kind, (0, Usage()))
        summed.add(usage)
        by_type[kind] = (count + 1, summed)
        total.add(usage)
    lines = [title, "",
             "### Cost by agent", "", f"| Agent | Type | Model | Effort | {USAGE_HEADER} |",
             "|---|---|---|---|" + "---:|" * USAGE_COLUMNS]
    for kind, label, usage, models, effort in agents:
        lines.append(f"| {label} | {kind} | {', '.join(sorted(models))} | {effort or '—'} | "
                     + " | ".join(usage_cells(usage)) + " |")
    lines += ["", "### Cost by type", "", f"| Type | Agents | {USAGE_HEADER} |", "|---|---:|" + "---:|" * USAGE_COLUMNS]
    for kind, (count, usage) in sorted(by_type.items(), key=lambda kv: -kv[1][1].usd):
        lines.append(f"| {kind} | {count} | " + " | ".join(usage_cells(usage)) + " |")
    lines.append(f"| **Total** | {len(agents)} | " + " | ".join(usage_cells(total)) + " |")
    if total.unpriced:
        lines += ["", f"{total.unpriced} requests used a model with no price in prices.toml; their cost is missing."]
    if total.output_estimated:
        lines += ["", f"Output for {total.output_estimated} requests is estimated from their visible text at "
                  f"{CHARS_PER_TOKEN} characters a token: their transcripts log output only as the reply starts. "
                  "Hidden thinking isn't in the Output column, so that output is a lower bound."]
    lines += ["", "Est. thinking is estimated from each request's context growth to the next request, less the "
              "characters it added at the agent's measured characters per token (measured on its steps without "
              f"thinking when there are {MIN_RATIO_STEPS} or more and it lies within {MIN_CHARS_PER_TOKEN}-"
              f"{MAX_CHARS_PER_TOKEN}, else {DEFAULT_CHARS_PER_TOKEN}), and priced at the model's output rate. "
              "Where a transcript logs a request's final output count, the thinking already in it is taken off. "
              "It is not billing data. USD incl. est. is USD plus that estimate."]
    data = {**extra, "since": since.isoformat() if since else None,
            "until": until.isoformat() if until else None,
            "by_type": {kind: {"agents": count, **asdict(usage)} for kind, (count, usage) in by_type.items()},
            # chars_per_token is recorded for diagnosis only; no report or `records` reads it back.
            "total": asdict(total), "chars_per_token": ratios}
    lines += ["", f"<!-- agent-costs {json.dumps(data, sort_keys=True)} -->"]
    return "\n".join(lines)


def add_counts(into: dict, row: dict) -> None:
    for key, value in row.items():
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            into[key] = into.get(key, 0) + value


def merge_sessions(comments: list[dict]) -> dict:
    """One run's totals from its record's comments, one per manager session."""
    by_type: dict[str, dict] = {}
    total: dict = {}
    for data in comments:
        for kind, row in data["by_type"].items():
            add_counts(by_type.setdefault(kind, {}), row)
        add_counts(total, data["total"])
    return {"by_type": by_type, "total": total, "sessions": len(comments)}


def records_report(paths: list[str]) -> tuple[str, int]:
    """Totals across the records' runs: one table when the records share a directory (a repo's key),
    else one per directory and then the pooled one."""
    runs: list[tuple[str, dict]] = []
    for arg in paths:
        comments = [json.loads(m.group(1)) for m in COMMENT_RE.finditer(Path(arg).read_text())]
        if comments:
            runs.append((arg, merge_sessions(comments)))
        else:
            print(f"WARN {arg}: no agent-costs comment", file=sys.stderr)
    if not runs:
        return "agent_costs: no run records with an agent-costs comment", 1
    groups: dict[Path, list[tuple[str, dict]]] = {}
    for path, data in runs:
        groups.setdefault(Path(path).resolve().parent, []).append((path, data))
    if len(groups) == 1:
        return runs_table(runs), 0
    names = [directory.name for directory in groups]
    sections = [
        f"## {directory.name if names.count(directory.name) == 1 else directory}\n\n{runs_table(group)}"
        for directory, group in sorted(groups.items(), key=lambda item: (item[0].name, str(item[0])))
    ]
    sections.append(f"## Pooled\n\n{runs_table(runs)}")
    return "\n\n".join(sections), 0


def runs_table(runs: list[tuple[str, dict]]) -> str:
    """The by-type table and the per-run list for these runs."""
    by_type: dict[str, tuple[int, int, Usage]] = {}
    estimated: set[str] = set()  # types with at least one run whose comment carries the estimate
    for _, data in runs:
        for kind, row in data["by_type"].items():
            run_count, agents, summed = by_type.get(kind, (0, 0, Usage()))
            summed.add(Usage(**{f.name: row.get(f.name, 0) for f in fields(Usage)}))
            by_type[kind] = (run_count + 1, agents + row.get("agents", 0), summed)
            if "thinking" in row:
                estimated.add(kind)
    lines = [f"Across {len(runs)} run{'' if len(runs) == 1 else 's'}", "",
             f"| Type | Runs | Agents | {USAGE_HEADER} | USD per run |",
             "|---|---:|---:|" + "---:|" * (USAGE_COLUMNS + 1)]
    for kind, (run_count, agents, usage) in sorted(by_type.items(), key=lambda kv: -kv[1][2].usd):
        lines.append(f"| {kind} | {run_count} | {agents} | " + " | ".join(usage_cells(usage, kind in estimated))
                     + f" | {fmt(usage.usd / run_count, usd=True)} |")
    lines += ["", "Est. thinking sums only the runs whose comment carries an estimate, and USD incl. est. adds that "
              "to the USD of every run; n/a where no run does."]
    lines += ["", "| Run | Sessions | USD |", "|---|---:|---:|"]
    lines += [f"| {path} | {data['sessions']} | {fmt(data['total']['usd'], usd=True)} |" for path, data in runs]
    return "\n".join(lines)


def home_records() -> list[str]:
    """Every run record loom keeps, $FLOW_DATA/records/<key>/*.md, the retros left out."""
    root = flow_data() / "records"
    return sorted(str(path) for path in root.glob("*/*.md") if path.parent.name != "retros")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Token usage and API-price cost of a session's agents.")
    sub = parser.add_subparsers(dest="command", required=True)
    session = sub.add_parser("session", help="price one session's agents")
    session.add_argument("session", nargs="?", help="session id, transcript (.jsonl) or its directory")
    session.add_argument("--since", help="ISO time: skip requests before it")
    session.add_argument("--until", help="ISO time: skip requests after it")
    records = sub.add_parser("records", help="total run records' costs (default: every record in the home store)")
    records.add_argument("files", nargs="*")
    run = sub.add_parser("run", help="price a loom flow run's manager sessions and agent runs")
    run.add_argument("run", help="the run's ID, as loom flow run list prints it")
    run.add_argument("--json", action="store_true", help="one JSON object: a row per agent run and manager session")
    run.add_argument("--session", action="append", default=[], metavar="NAME=ID",
                     help="the session of an agent run whose record has none yet (repeatable)")
    design = sub.add_parser("design", help="price the design session: the transcripts that wrote the spec")
    design.add_argument("spec", help="the spec's path from the top level")
    design.add_argument("--plan", help="the plan, whose adding commit ends the design")
    design.add_argument("--json", action="store_true", help="one JSON object")
    args = parser.parse_args(argv)
    if args.command == "session":
        print(session_report(find_session(args.session), parse_time(args.since), parse_time(args.until)))
        return 0
    if args.command == "run":
        if not args.json:
            print(run_report(args.run))
            return 0
        hints = {}
        for hint in args.session:
            name, sep, sid = hint.partition("=")
            if not sep or not name or not sid:
                parser.error(f"--session {hint!r}: give NAME=ID")
            hints[name] = sid
        print(run_json(args.run, hints))
        return 0
    if args.command == "design":
        data = design_json(args.spec, args.plan)
        if args.json:
            print(json.dumps(data, sort_keys=True))
        else:
            print(f"design {args.spec}: {len(data['sessions'])} sessions, ${data['total']['usd']:.2f} logged, "
                  f"${data['total']['usd_incl_est']:.2f} with estimated thinking")
        return 0
    text, code = records_report(args.files or home_records())
    print(text, file=sys.stderr if code else sys.stdout)
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
