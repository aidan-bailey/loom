"""Expand a profile's gates for one story, or for the end of the run.

`{packages}` in a gate becomes the Cargo packages a story changed: the
workspace package owning each file its commits changed (the nearest directory
above it holding a package's Cargo.toml), and every workspace package that
depends on one of those, transitively, read from
`<prefix> cargo metadata --format-version 1 --no-deps`; `-p <name>` each,
sorted. A change to a workspace-wide file expands it to `--workspace`. A story
that changed no Cargo package skips the gates that hold it, and a metadata
call that fails fails them. Final gates expand it to `--workspace`. Every
command is printed with the profile's prefix applied, one line per gate:

    RUN <command>
    SKIPPED <gate> (<why>)
    FAIL <gate> (<why>)

Version 1 knows Cargo only; other ecosystems' gates run over the whole repo.
"""

from __future__ import annotations

import json
import os
import subprocess
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from pathlib import Path

from flowconf import FlowError, git, load_profile, toplevel

PLACEHOLDER = "{packages}"
# Files whose change can affect every package of the workspace, as paths from the top level.
WORKSPACE_WIDE = frozenset(
    {"Cargo.toml", "Cargo.lock", ".cargo/config.toml", "rust-toolchain.toml", "clippy.toml", "rustfmt.toml"}
)


@dataclass
class Graph:
    """The workspace's packages: each one's directory, and the packages that depend on each directly."""

    by_dir: dict[Path, str] = field(default_factory=dict)
    dependents: dict[str, set[str]] = field(default_factory=dict)


def parse_metadata(metadata: dict) -> Graph:
    """The workspace graph from `cargo metadata --no-deps` output: path dependencies of every kind."""
    graph = Graph()
    for package in metadata["packages"]:
        directory = Path(os.path.realpath(Path(package["manifest_path"]).parent))
        graph.by_dir[directory] = package["name"]
        graph.dependents.setdefault(package["name"], set())
    for package in metadata["packages"]:
        for dependency in package.get("dependencies", []):
            if dependency.get("path") and dependency["name"] in graph.dependents:
                graph.dependents[dependency["name"]].add(package["name"])
    return graph


def load_metadata(top: Path, prefix: str) -> dict:
    """`cargo metadata` for the workspace at `top`, run with the profile's prefix."""
    command = f"{prefix} cargo metadata --format-version 1 --no-deps".strip()
    result = subprocess.run(["bash", "-c", command], cwd=top, capture_output=True, text=True)
    if result.returncode != 0:
        reason = " ".join(result.stderr.split())[-400:] or f"exited {result.returncode}"
        raise FlowError(f"`{command}` failed: {reason}")
    try:
        return json.loads(result.stdout)
    except ValueError as err:
        raise FlowError(f"`{command}` printed no JSON: {err}") from None


def owner(graph: Graph, path: Path) -> str | None:
    """The workspace package whose directory is nearest above `path`."""
    for directory in path.parents:
        if directory in graph.by_dir:
            return graph.by_dir[directory]
    return None


def package_args(changed: Sequence[str], top: Path, graph: Graph) -> list[str] | None:
    """`-p` arguments for the packages `changed` touches and their dependents, `--workspace` for a
    workspace-wide file, or None when no changed file belongs to a package."""
    if any(path in WORKSPACE_WIDE for path in changed):
        return ["--workspace"]
    root = Path(os.path.realpath(top))
    touched = {name for path in changed if (name := owner(graph, root / path)) is not None}
    if not touched:
        return None
    closure, frontier = set(touched), list(touched)
    while frontier:
        for dependent in graph.dependents.get(frontier.pop(), ()):
            if dependent not in closure:
                closure.add(dependent)
                frontier.append(dependent)
    return [arg for name in sorted(closure) for arg in ("-p", name)]


def with_prefix(prefix: str, command: str) -> str:
    return f"{prefix} {command}" if prefix else command


def expand(gates: Sequence[str], prefix: str, resolve: Callable[[], list[str] | None]) -> list[str]:
    """One line per gate. `resolve` gives the `{packages}` arguments; it runs once, and only when a
    gate holds the placeholder."""
    lines: list[str] = []
    args: list[str] | None = None
    error: str | None = None
    resolved = False
    for gate in gates:
        command = gate
        if PLACEHOLDER in gate:
            if not resolved:
                resolved = True
                try:
                    args = resolve()
                except FlowError as err:
                    error = " ".join(str(err).split())
            if error is not None:
                lines.append(f"FAIL {gate} (cannot expand {PLACEHOLDER}: {error})")
                continue
            if args is None:
                lines.append(f"SKIPPED {gate} (the story changed no Cargo package)")
                continue
            command = gate.replace(PLACEHOLDER, " ".join(args))
        lines.append(f"RUN {with_prefix(prefix, command)}")
    return lines


def changed_files(top: Path, commits: Sequence[str]) -> list[str]:
    """Every path the commits changed, deleted and renamed ones included, from the top level."""
    paths: set[str] = set()
    for commit in commits:
        # -m --first-parent: a merge commit lists what it brought in; -z: a non-ASCII path is not C-quoted.
        out = git(top, "show", "-m", "--first-parent", "--name-only", "-z", "--no-renames", "--format=", commit)
        paths.update(path for path in out.split("\0") if path)
    return sorted(paths)


def gates_command(story: Sequence[str] | None, final: bool, cwd: Path) -> int:
    """`flowconf.py gates`: print the expanded gates; exit 1 when any gate is FAIL."""
    top = toplevel(cwd)
    profile = load_profile(top)
    if final:
        lines = expand(profile.final_gates, profile.prefix, lambda: ["--workspace"])
    else:
        changed = changed_files(top, story or [])
        lines = expand(
            profile.story_gates,
            profile.prefix,
            lambda: package_args(changed, top, parse_metadata(load_metadata(top, profile.prefix))),
        )
    print("\n".join(lines) if lines else "none: the profile lists no gates of this kind")
    return 1 if any(line.startswith("FAIL ") for line in lines) else 0
