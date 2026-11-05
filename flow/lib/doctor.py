"""`flowconf.py doctor`: check that a supervised run can start in this repo.

FAIL, and exit 1, when this is not a flow root loom wrote (FLOW_ROOT: plugin/,
lib/ and ROSTER); when the git guard (lib/git_guard.py, which loom's
`loom flow guard` runs for the guarded agents) doesn't block `git stash` or
doesn't let `git status` through; when `tail --pid` is missing (agent-wait.sh
needs it); when the profile is missing or can't be used; when it lists no story
gates; when the prefix fails; when a program a gate runs isn't on PATH inside the prefix, or the
gate can't be parsed (doctor checks every command of a chain, a `for` loop and a `bash -c` script,
a cargo plugin as `cargo-<sub>`, and `rustup` for `cargo +<toolchain>`); when the state
directory isn't writable; or when the drafts directory (part 2 spec §6.1) isn't ignored and
`flowconf.py drafts` would refuse to ignore it, because its first directory holds tracked
files. Doctor makes no drafts directory and writes no exclude line.
WARN, without failing, for each
backticked path a design registry names that isn't in the worktree.
The Python check is flowconf's own: it exits before anything runs on a Python older than 3.11.
"""

from __future__ import annotations

import json
import re
import shlex
import subprocess
import sys
from pathlib import Path

from flowconf import FlowError, Profile, drafts_dir, exclude_line, flow_root, load_profile, state_dir, toplevel

# Cargo's own subcommands. Any other `cargo <sub>` runs the program `cargo-<sub>`.
CARGO_BUILTINS = frozenset(
    "add b bench build c check clean config d doc fetch fix generate-lockfile help info init install "
    "locate-project login logout metadata new owner package pkgid publish r remove report run rustc "
    "rustdoc search t test tree uninstall update vendor verify-project version yank".split()
)


SEPARATORS = frozenset({"&&", "||", ";", "|", "&", "(", ")", ";;", "|&"})
REDIRECTS = frozenset({">", ">>", "<", "<<", "<<<", ">&", "<&", "&>", "&>>", ">|"})
RESERVED = frozenset("if then else elif do while until ! { } fi done esac time".split())
# Builtins no program on PATH runs.
BUILTINS = frozenset(
    "cd exit continue break return [ [[ test echo printf true false : export set shift read local source . eval "
    "exec trap wait pushd popd unset command".split()
)
ASSIGNMENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]*=.*", re.DOTALL)
PUNCTUATION = frozenset("();<>|&")


def _is_separator(word: str) -> bool:
    """A punctuation run that ends a command. A run the lexer glued together (`);`) counts too."""
    return word in SEPARATORS or (bool(word) and set(word) <= PUNCTUATION and not any(c in word for c in "<>"))


def _is_redirect(word: str) -> bool:
    return word in REDIRECTS or (bool(word) and set(word) <= PUNCTUATION and any(c in word for c in "<>"))


def programs_of(gate: str) -> list[str] | None:
    """Every program a gate runs, in first-seen order and each once; None when it cannot be told.

    Reads chains, pipes, `for` loops and `bash -c` scripts; a cargo plugin is `cargo-<sub>` and
    `cargo +<toolchain>` also needs `rustup`. A program named by an expansion, a `case`, or an
    unbalanced quote is None."""
    text = gate.replace("{packages}", "").replace("\n", " ; ")
    lexer = shlex.shlex(text, posix=True, punctuation_chars=True)
    lexer.whitespace_split = True
    try:
        words = list(lexer)
    except ValueError:
        return None
    found: list[str] = []

    def add(program: str) -> None:
        if program not in found:
            found.append(program)

    at = 0
    start = True
    while at < len(words):
        word = words[at]
        if _is_separator(word):
            start = True
            at += 1
            continue
        if _is_redirect(word):
            at += 2
            continue
        if not start:
            at += 1
            continue
        if ASSIGNMENT.fullmatch(word):
            at += 1
            continue
        if word in ("for", "select"):
            if "do" not in words[at + 1 :]:
                return None
            at = words.index("do", at + 1) + 1
            continue
        if word == "case":
            return None
        if word in RESERVED:
            at += 1
            continue
        if word.startswith("$") or "`" in word or "$(" in word:
            return None
        start = False
        if word in BUILTINS:
            at += 1
        elif word in ("bash", "sh") and words[at + 1 : at + 2] == ["-c"]:
            if at + 2 >= len(words):
                return None
            inner = programs_of(words[at + 2])
            if inner is None:
                return None
            add(word)
            for program in inner:
                add(program)
            at += 3
        elif word == "cargo":
            end = at + 1
            while end < len(words) and not _is_separator(words[end]) and not _is_redirect(words[end]):
                end += 1
            args = words[at + 1 : end]
            sub = next((i for i, arg in enumerate(args) if not arg.startswith(("-", "+"))), len(args))
            if any(arg.startswith("+") for arg in args[:sub]):
                add("rustup")
            if sub < len(args) and args[sub] not in CARGO_BUILTINS:
                add(f"cargo-{args[sub]}")
            else:
                add("cargo")
            at = end
        else:
            add(word)
            at += 1
    return found


def missing_programs(prefix: str, programs: list[str]) -> list[str] | None:
    """The programs not found inside the prefix, in one call; None when the prefix itself fails or can't start."""
    script = 'for p in "$@"; do command -v "$p" >/dev/null 2>&1 || echo "$p"; done'
    command = ["bash", "-c", script, "doctor", *programs]
    try:
        if prefix:
            command = [*shlex.split(prefix), *command]
        result = subprocess.run(command, capture_output=True, text=True)
    except (OSError, ValueError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout.split()


def prefix_works(prefix: str) -> str | None:
    """None when `<prefix> true` succeeds (or there is no prefix), else what went wrong."""
    if not prefix:
        return None
    try:
        result = subprocess.run([*shlex.split(prefix), "true"], capture_output=True, text=True)
    except (OSError, ValueError) as err:
        return f"`{prefix} true` could not run: {err}"
    if result.returncode == 0:
        return None
    return result.stderr.strip()[-300:] or f"`{prefix} true` exited {result.returncode}"


def guard_problem() -> str | None:
    """None when the git guard blocks `git stash` with its message and lets `git status` through,
    else what is wrong. A block alone could be the fail-closed path."""

    def run(command: str) -> subprocess.CompletedProcess:
        payload = {"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": "/", "tool_input": {"command": command}}
        guard = flow_root() / "lib" / "git_guard.py"
        return subprocess.run([sys.executable, "-I", str(guard)], input=json.dumps(payload), capture_output=True, text=True)

    allowed, blocked = run("git status"), run("git stash")
    if allowed.returncode != 0:
        return f"git_guard.py blocked `git status`: {allowed.stderr.strip()[-200:]}"
    if blocked.returncode != 2 or "developer-git-guard:" not in blocked.stderr:
        return f"git_guard.py did not block `git stash` with the guard's message (exit {blocked.returncode})"
    return None


def root_problem() -> str | None:
    """None when FLOW_ROOT is a flow root loom wrote (plugin/, lib/ and ROSTER), else why not."""
    root = flow_root()
    missing = [part for part in ("plugin", "lib", "ROSTER") if not (root / part).exists()]
    if missing:
        return f"{root} is not a flow root (no {', '.join(missing)}): run flow's tools through `loom flow`"
    return None


def tail_pid_problem() -> str | None:
    result = subprocess.run(["tail", "--help"], capture_output=True, text=True)
    return None if "--pid" in result.stdout else "`tail --pid` is missing; agent-wait.sh needs GNU tail"


def state_problem(top: Path, profile: Profile | None) -> str | None:
    try:
        path = state_dir(top, profile)
        path.mkdir(parents=True, exist_ok=True)
        probe = path / ".doctor-probe"
        probe.write_text("x")
        probe.unlink()
    except (OSError, FlowError) as err:
        return str(err)
    return None


def drafts_problem(top: Path, profile: Profile) -> str | None:
    """None when /flow:draft can keep the drafts directory out of git, else why it can't. Doctor makes nothing.

    It probes what make_drafts probes, a plan's directory below the drafts root (a rule such as `docs/drafts/*/`
    covers that and not the root), under a stem no plan has."""
    try:
        exclude_line(top, drafts_dir(top, profile) / "doctor-probe")
    except FlowError as err:
        return str(err)
    return None


def registry_paths_missing(top: Path, profile: Profile) -> list[str]:
    """Each backticked path the design registries name that isn't in the worktree, once.

    A path is a token that holds a `/` and no wildcard, space or URL scheme; a trailing `:<line>` or
    `:<start>-<end>` and a `#anchor` are left off. Absolute and `~` paths, placeholders (`<...>`, `{...}`)
    and URLs are not worktree paths and are skipped. Each line is scanned alone, so one stray backtick
    can't shift the pairing in the lines after it."""
    missing: list[str] = []
    for line in profile.registries.splitlines():
        for token in re.findall(r"`([^`]+)`", line):
            path = re.sub(r":\d+(?:-\d+)?$", "", re.sub(r"#.*$", "", token))
            if path.startswith(("/", "~")) or any(char in path for char in "<>{}"):
                continue
            if "/" in path and not re.search(r"[\s*?\[]|://", path) and not (top / path).exists() and path not in missing:
                missing.append(path)
    return missing


def doctor(cwd: Path) -> tuple[list[str], int]:
    """The PASS, FAIL and WARN lines for this repo, and the number of FAILs."""
    lines: list[str] = []
    fails = 0

    def report(check: str, problem: str | None) -> None:
        nonlocal fails
        if problem:
            fails += 1
            lines.append(f"FAIL {check}: {problem}")
        else:
            lines.append(f"PASS {check}")

    report("flow root", root_problem())
    report("guard", guard_problem())
    report("tail --pid", tail_pid_problem())
    lines.append(f"PASS python {sys.version.split()[0]}")
    try:
        top = toplevel(cwd)
    except FlowError as err:
        report("repo", str(err))
        return lines, fails
    profile: Profile | None
    try:
        profile = load_profile(top)
        report("profile", None)
    except FlowError as err:
        profile = None
        report("profile", str(err))
    if profile is not None:
        report("story gates", None if profile.story_gates else "the profile lists none, so no story would be verified")
        problem = prefix_works(profile.prefix)
        report("prefix", problem)
        if problem is None:
            gates = profile.story_gates + profile.final_gates
            unparsed = [gate for gate in gates if programs_of(gate) is None]
            programs = sorted({p for gate in gates for p in programs_of(gate) or []})
            missing = missing_programs(profile.prefix, programs)
            problems = [f"cannot tell which program this gate runs: {gate}" for gate in unparsed]
            if missing is None:
                problems.append("the prefix failed while looking them up")
            elif missing:
                problems.append(f"not found: {', '.join(missing)}")
            report("gate programs", "; ".join(problems) or None)
        report("drafts", drafts_problem(top, profile))
        stale = registry_paths_missing(top, profile)
        for path in stale:
            lines.append(f"WARN design registries: `{path}` names nothing in this worktree")
        if not stale:
            lines.append("PASS design registries")
    report("state", state_problem(top, profile))
    return lines, fails


def main(cwd: Path) -> int:
    lines, fails = doctor(cwd)
    warns = sum(line.startswith("WARN ") for line in lines)
    print("\n".join(lines))
    print(f"doctor: {fails} FAIL, {warns} WARN")
    return 1 if fails else 0
