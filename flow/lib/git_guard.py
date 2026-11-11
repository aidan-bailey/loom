#!/usr/bin/env python3
"""PreToolUse guard for the developer subagents' Bash calls.

junior-dev, intermediate-dev and senior-dev share the controller's worktree and
its one git index, so the flow:habits skill forbids the git commands that
sweep, rewrite or discard shared state. This hook makes the riskiest of those
rules mechanical. Each developer's frontmatter runs it before every Bash call;
it reads the PreToolUse JSON on stdin and exits 2 — which blocks the call and
hands Claude the stderr line as the reason — when any command in the line is
one of them:

- `git reset`, `rebase`, `stash`, `clean`, `checkout`, `switch`, `restore`,
  `push`;
- `git commit --amend`, `git commit -a`, or a commit with no `-- <paths>`;
- `git add -A`/`--all`/`-u`/`.`/`:/`, or `git add` of a directory.

A command behind a wrapper is checked too: `env` and `VAR=value`, `rtk` and the
rtk subcommands that run a command (`proxy`, `err`, `test`, `summary`, and
`run`'s string), `direnv exec <dir>`, and a shell's `-c` string. Heredoc
bodies (commit messages) are skipped. A command line it can't tokenise falls
back to a plain search for the blocked subcommands. Anything else, and any
input it can't read, is allowed through.

With `--drafter` it guards the drafter, which writes plan text and never
changes the tree. Its frontmatter runs the hook on Bash, Write and Edit. Only
the read-only git subcommands in DRAFTER_GIT get through, and Write and Edit
only under the drafts directory flowconf names for the worktree holding
`$CLAUDE_PROJECT_DIR` (else the call's cwd): the profile's `paths.drafts`,
`.flow/drafts` by default, symlinks resolved. A write whose directory can't be
told, outside a worktree or under a profile flowconf refuses, is blocked. A
Bash redirect can still write anywhere; the drafter's definition forbids that
in prose.
"""

from __future__ import annotations

import json
import os
import re
import shlex
import sys
from pathlib import Path

BLOCKED_SUBCOMMANDS = {
    "reset": "it rewrites history and the shared index",
    "rebase": "it rewrites history",
    "stash": "the stash stack is shared with every worktree",
    "clean": "it deletes untracked files other agents may own",
    "checkout": "it switches commits or restores files over other agents' uncommitted work",
    "switch": "it moves the shared worktree, and every agent working in it, to another branch",
    "restore": "it restores files over other agents' uncommitted work",
    "push": "pushing is the owner's call",
}
DRAFTER_GIT = {
    "annotate", "blame", "cat-file", "check-ignore", "describe", "diff", "diff-files",
    "diff-index", "diff-tree", "for-each-ref", "grep", "help", "log", "ls-files", "ls-tree",
    "merge-base", "name-rev", "rev-list", "rev-parse", "shortlog", "show", "show-ref",
    "status", "version",
}
PATHSPEC = 'commit by pathspec: `git commit -m "…" -- <your files>`'
WRAPPERS = {"env", "command", "rtk", "nice", "time"}
# rtk subcommands that run the rest of the line as a command, raw or filtered (`rtk run` takes one sh -c string).
RTK_RUNNERS = {"proxy", "err", "test", "summary"}
# Shells whose `-c <string>` is a command line of its own.
SHELLS = {"sh", "bash", "zsh", "dash"}
# How deep a -c string inside a -c string is still read.
MAX_NESTING = 3
GIT_OPTIONS_WITH_VALUE = {"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"}
ASSIGNMENT_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*=")
SHORT_CLUSTER_RE = re.compile(r"-[A-Za-z]+")
HEREDOC_RE = re.compile(r"(?<!<)<<(?!<)-?\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\1")
OPERATORS = set("();<>|&\n")
FALLBACK_RE = re.compile(r"\bgit\s+(?:-[Cc]\s+\S+\s+)*(" + "|".join(BLOCKED_SUBCOMMANDS) + r")\b")
DRAFTER_FALLBACK_RE = re.compile(r"\bgit\s+(?:-[Cc]\s+\S+\s+|--?[a-z-]+\s+)*([a-z][a-z-]*)")


def strip_heredocs(command: str) -> str:
    """Drop heredoc bodies, so a commit message that mentions `git reset` is not a command."""
    lines = command.split("\n")
    kept: list[str] = []
    i = 0
    while i < len(lines):
        kept.append(lines[i])
        delimiters = [m.group(2) for m in HEREDOC_RE.finditer(lines[i])]
        i += 1
        for delimiter in delimiters:
            while i < len(lines) and lines[i].strip() != delimiter:
                i += 1
            i += 1
    return "\n".join(kept)


def segments(command: str) -> list[list[str]]:
    """Tokenise a command line and split it at shell operators (`;`, `&&`, `|`, newlines…)."""
    lexer = shlex.shlex(command, posix=True, punctuation_chars="();<>|&\n")
    lexer.whitespace = " \t\r"
    lexer.whitespace_split = True
    result: list[list[str]] = [[]]
    for token in lexer:
        if token and set(token) <= OPERATORS:
            result.append([])
        else:
            result[-1].append(token)
    return [segment for segment in result if segment]


def unwrap(segment: list[str]) -> list[str]:
    """Skip leading `VAR=value` assignments and wrappers such as `/usr/bin/env`, `rtk`, `rtk proxy` or
    `direnv exec <dir>`; `rtk run <string>` comes back as `sh -c <string>`, which `shell_string` reads."""
    i = 0
    while i < len(segment):
        token, base = segment[i], os.path.basename(segment[i])
        following = segment[i + 1] if i + 1 < len(segment) else ""
        if base == "rtk" and following == "run":
            return ["sh", "-c", *segment[i + 2:]]
        if base == "rtk" and following in RTK_RUNNERS:
            i += 2
        elif base == "direnv" and following == "exec" and i + 2 < len(segment):
            i += 3
        elif ASSIGNMENT_RE.match(token) or base in WRAPPERS:
            i += 1
        elif token.startswith("-") and i > 0 and os.path.basename(segment[i - 1]) == "env":
            i += 1
        else:
            break
    return segment[i:]


def commit_violation(args: list[str]) -> str | None:
    options = args[: args.index("--")] if "--" in args else args
    for token in options:
        if token == "--amend":
            return "`git commit --amend` rewrites a commit other agents may point at; to fix a commit of yours, make a new one"
        if token == "--all" or (SHORT_CLUSTER_RE.fullmatch(token) and "a" in token[1:]):
            return f"`git commit {token}` commits every tracked change in the shared worktree, other agents' included; {PATHSPEC}"
    if "--" not in args or args.index("--") == len(args) - 1:
        return f"a commit without a pathspec takes whatever anyone has staged; {PATHSPEC}"
    return None


def add_violation(args: list[str], cwd: str) -> str | None:
    paths: list[str] = []
    after_dashdash = False
    for token in args:
        if after_dashdash:
            paths.append(token)
        elif token == "--":
            after_dashdash = True
        elif token in ("--all", "--update", "--no-ignore-removal") or (
            SHORT_CLUSTER_RE.fullmatch(token) and ("A" in token or "u" in token)
        ):
            return f"`git add {token}` stages every change in the shared worktree, other agents' included; stage your files by explicit path"
        elif not token.startswith("-"):
            paths.append(token)
    for path in paths:
        if path in (".", "./", ":/", ":/."):
            return f"`git add {path}` stages the whole tree; stage your files by explicit path"
        if os.path.isdir(path if os.path.isabs(path) else os.path.join(cwd, path)):
            return f"`git add {path}` stages a whole directory, which is how ghost entries reach a commit; stage your files by explicit path"
    return None


def drafter_git_violation(subcommand: str) -> str | None:
    if subcommand in DRAFTER_GIT:
        return None
    return f"`git {subcommand}` is blocked for drafters: a drafter only reads git, and never stages, commits or changes the tree"


def git_violation(args: list[str], cwd: str, drafter: bool = False) -> str | None:
    i = 0
    while i < len(args) and args[i].startswith("-"):
        i += 2 if args[i] in GIT_OPTIONS_WITH_VALUE else 1
    if i >= len(args):
        return None
    subcommand, rest = args[i], args[i + 1 :]
    if drafter:
        return drafter_git_violation(subcommand)
    if subcommand in BLOCKED_SUBCOMMANDS:
        return f"`git {subcommand}` is blocked for developers: {BLOCKED_SUBCOMMANDS[subcommand]}"
    if subcommand == "commit":
        return commit_violation(rest)
    if subcommand == "add":
        return add_violation(rest, cwd)
    return None


def shell_string(args: list[str]) -> str | None:
    """The command string of `sh -c <string>` (bash, zsh and dash too; `-c` alone or in a flag group such as
    `-lc`, after options such as `-o pipefail`), else None."""
    if not args or os.path.basename(args[0]) not in SHELLS:
        return None
    k = 1
    while k < len(args):
        token = args[k]
        if re.fullmatch(r"-[A-Za-z]*c[A-Za-z]*", token):
            return args[k + 1] if k + 1 < len(args) else None
        if token in ("-o", "+o", "-O", "+O"):
            k += 2
        elif token.startswith(("-", "+")):
            k += 1
        else:
            return None  # a script file, not a -c string
    return None


def violation(command: str, cwd: str, drafter: bool = False, depth: int = 0) -> str | None:
    """The reason to block `command`, or None to let it through. A shell's -c string is read as a command line
    of its own, up to MAX_NESTING deep."""
    text = strip_heredocs(command)
    try:
        parsed = segments(text)
    except ValueError:
        if drafter:
            for match in DRAFTER_FALLBACK_RE.finditer(text):
                reason = drafter_git_violation(match.group(1))
                if reason:
                    return reason
            return None
        match = FALLBACK_RE.search(text)
        if match:
            return f"`git {match.group(1)}` is blocked for developers: {BLOCKED_SUBCOMMANDS[match.group(1)]}"
        return None
    for segment in parsed:
        args = unwrap(segment)
        inner = shell_string(args)
        if inner is not None and depth < MAX_NESTING:
            reason = violation(inner, cwd, drafter, depth + 1)
            if reason:
                return reason
            continue
        if args and os.path.basename(args[0]) == "git":
            reason = git_violation(args[1:], cwd, drafter)
            if reason:
                return reason
    return None


def drafts_root(project: str) -> tuple[str, str]:
    """The top level of the worktree holding `project`, and its drafts directory as flowconf names it (symlinks resolved)."""
    # The script's own directory, not an implicit sys.path[0]: PYTHONSAFEPATH=1 leaves it off the path.
    sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))
    import flowconf  # imported here: only a drafter's Write and Edit need the profile, so a developer's call never loads it

    top = flowconf.toplevel(Path(project))
    return str(top), os.path.realpath(flowconf.drafts_dir(top, flowconf.optional_profile(top)))


def write_violation(path: str, root: str) -> str | None:
    """The reason to block a drafter's Write or Edit of `path`, or None when it is under the drafts directory.

    A relative `path` is read against the worktree's top level, where the drafts directory is anchored."""
    try:
        top, drafts = drafts_root(root)
    except (Exception, SystemExit) as err:
        # Fail closed. flowconf raises FlowError outside a worktree and for a profile it refuses, and exits on a
        # Python older than 3.11. Any error is caught here because an uncaught one exits 1, which Claude Code treats
        # as non-blocking (the call goes through): a write the guard can't place is a write it blocks.
        return f"a drafter writes only under its drafts directory, which can't be told here: {err}"
    target = os.path.realpath(os.path.join(top, path))
    if target.startswith(drafts + os.sep):
        return None
    return f"a drafter writes only under {drafts}{os.sep}, and `{path}` is outside it"


def main() -> int:
    drafter = "--drafter" in sys.argv[1:]
    try:
        data = json.load(sys.stdin)
    except ValueError:
        return 0
    if not isinstance(data, dict):
        return 0
    tool, tool_input = data.get("tool_name"), data.get("tool_input") or {}
    cwd = data.get("cwd") or os.getcwd()
    if tool == "Bash":
        reason = violation(tool_input.get("command") or "", cwd, drafter)
    elif drafter and tool in ("Write", "Edit"):
        reason = write_violation(tool_input.get("file_path") or "", os.environ.get("CLAUDE_PROJECT_DIR") or cwd)
    else:
        return 0
    if reason:
        if drafter:
            print(f"drafter-guard: {reason}. The drafter definition forbids it.", file=sys.stderr)
        else:
            print(f"developer-git-guard: {reason}. The flow:habits skill forbids it.", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
