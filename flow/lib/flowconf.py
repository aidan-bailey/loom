#!/usr/bin/env python3
"""The flow's view of a repo: its key, its profile, and what each role's brief needs.

A repo is keyed by its main checkout's resolved path, with every character
outside [A-Za-z0-9] replaced by `-` (the way auto memory names its
directories), so every worktree of a repo shares one key. Its profile is
`.claude/flow.toml` at the worktree's top level.
Validation is strict: an unknown key, a wrong type or another schema is
refused by name, so a typo can't silently drop a landmine.

    flowconf.py key                the repo key
    flowconf.py status             the key and the profile it would use, or "none"
    flowconf.py show --for ROLE    one role's values for its briefs; supervisor
                                   prints its own and every other role's block
                                   but the drafter's, which is /flow:draft's
    flowconf.py services           the profile's services, one per line, as written
    flowconf.py state              the state directory, made if missing, with
                                   its agent-reports/ subdirectory
    flowconf.py drafts STEM        the drafts directory of the plan whose file
                                   name is STEM.md, made if missing and kept
                                   out of git (part 2 spec §6.1)
    flowconf.py vacuous FILE       the lines of FILE (`-` is stdin) that match a
                                   gates.vacuous pattern, matched with Python
                                   `re`; exit 0 when any does, 1 when none, 2 on
                                   an error
    flowconf.py roster             the roster ID loom gave this run
                                   (FLOW_ROSTER, `loom flow roster <12 hex>`)
    flowconf.py gates --story COMMIT [COMMIT ...]
                                   the story gates, `{packages}` expanded for
                                   the files those commits changed
    flowconf.py gates --final      the final gates
    flowconf.py doctor             check that a run can start here (doctor.py)

The role blocks (spec §5.4): the developer gets the prefix, the services (start
each, as written, before the tests that need them) and the landmine notes (for
a deep story); the verifier the services and `vacuous`; the reviewer the prefix,
the landmine notes and the idioms. The drafter (part 2 spec §6.3) gets the
prefix, the story gates and the `{packages}` rule, the generated globs, the
landmine markers and notes, the idioms and the design registries.

Every command but `roster` works on the repo of the current directory. Exit 0
on success, 1 when there is no usable profile, git fails or a gate is FAIL, 2 on a usage error. `vacuous` and `roster` exit 2 on any
error, as their callers read 1 as an answer or a verdict.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

if sys.version_info < (3, 11):
    sys.exit(f"flowconf: the flow needs Python 3.11 or later (for tomllib); this is {sys.version.split()[0]}")

import tomllib  # noqa: E402

SCHEMA = 1
ROLES = ("supervisor", "developer", "verifier", "reviewer")
# /flow:draft's role, not the supervisor's: `show --for supervisor` prints every role in ROLES and leaves it out.
DRAFTER = "drafter"
# Every key a schema-1 profile may hold, by table ("" is the top level), with its type.
# A list holds strings only. TEXT is a string, or a list of strings read as one line each.
TEXT = "text"
KEYS: dict[str, dict[str, object]] = {
    "": {"schema": int, "prefix": str, "services": list, "generated": list},
    "gates": {"story": list, "final": list, "vacuous": list},
    "build": {"exclusive": list},
    "landmines": {"markers": list, "notes": TEXT},
    "review": {"idioms": TEXT},
    "paths": {"state": str, "records": str, "drafts": str},
    "design": {"registries": TEXT},
}
# Where /flow:draft keeps each plan's drafts when paths.drafts says nothing, under the worktree's top level
# (part 2 spec §6.1): inside the worktree, where a drafter's Write and Edit need no permission prompt.
DEFAULT_DRAFTS = ".flow/drafts"


class FlowError(Exception):
    """A lookup that failed, or a profile the flow can't use; the message says which and why."""


@dataclass(frozen=True)
class Profile:
    """A validated profile. `digest` is the first 12 hex digits of the file's sha256."""

    path: Path
    digest: str
    prefix: str = ""
    services: tuple[str, ...] = ()
    generated: tuple[str, ...] = ()
    story_gates: tuple[str, ...] = ()
    final_gates: tuple[str, ...] = ()
    vacuous: tuple[str, ...] = ()
    exclusive: tuple[str, ...] = ()
    markers: tuple[str, ...] = ()
    notes: str = ""
    idioms: str = ""
    state: str = ""
    records: str = ""
    drafts: str = ""
    registries: str = ""


def flow_root() -> Path:
    """The flow root loom runs this copy from: $FLOW_ROOT, else the directory above lib/."""
    return Path(os.environ.get("FLOW_ROOT") or Path(__file__).resolve().parent.parent)


def flow_data() -> Path:
    """Where run records and retros live: $FLOW_DATA, else ~/.loom/flow."""
    return Path(os.environ.get("FLOW_DATA") or Path.home() / ".loom" / "flow")


def git(cwd: Path, *args: str) -> str:
    """git's stdout, stripped; FlowError with git's stderr, on one line, when it fails."""
    result = subprocess.run(["git", "-C", str(cwd), *args], capture_output=True, text=True)
    if result.returncode != 0:
        # git's stderr can span lines; one line keeps a `FAIL [profile]` message on one line.
        raise FlowError(f"git {' '.join(args)} failed in {cwd}: {' '.join(result.stderr.split())}")
    return result.stdout.strip()


def toplevel(cwd: Path) -> Path:
    """The top level of the worktree holding `cwd`."""
    return Path(git(cwd, "rev-parse", "--show-toplevel"))


def main_checkout(cwd: Path) -> Path:
    """The main checkout every worktree of the repo shares, symlinks resolved."""
    common = Path(git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"))
    # A checkout's common directory is its .git; a bare repository's is the repository itself.
    return Path(os.path.realpath(common.parent if common.name == ".git" else common))


def repo_key(cwd: Path) -> str:
    """Spec §4.3: the main checkout's resolved path with every character outside [A-Za-z0-9] made `-`."""
    return re.sub(r"[^A-Za-z0-9]", "-", str(main_checkout(cwd)))


def find_profile(top: Path) -> Path:
    """The profile a worktree uses: its own .claude/flow.toml."""
    repo_file = top / ".claude" / "flow.toml"
    if repo_file.is_file():
        return repo_file
    raise FlowError(f"no flow profile: {repo_file} does not exist. Run /flow:init to draft one.")


def validate(data: dict, path: Path, digest: str) -> Profile:
    """A Profile from parsed TOML, refusing another schema, unknown keys and wrong types by name."""
    schema = data.get("schema")
    if type(schema) is not int:
        raise FlowError(f"{path}: schema: missing or not an integer; this flow reads schema {SCHEMA}")
    if schema != SCHEMA:
        raise FlowError(f"{path}: schema: {schema}; this flow reads schema {SCHEMA}")
    values: dict[str, object] = {}
    for key, value in data.items():
        if key and key in KEYS:
            if not isinstance(value, dict):
                raise FlowError(f"{path}: {key}: must be a table")
            entries = [(key, name, item) for name, item in value.items()]
        else:
            entries = [("", key, value)]
        for table, name, item in entries:
            dotted = f"{table}.{name}" if table else name
            kind = KEYS[table].get(name)
            if kind is None:
                raise FlowError(f"{path}: {dotted}: unknown key")
            if kind is list:
                if not isinstance(item, list) or not all(isinstance(entry, str) for entry in item):
                    raise FlowError(f"{path}: {dotted}: must be a list of strings")
                values[dotted] = tuple(item)
            elif kind == TEXT:
                if isinstance(item, list) and all(isinstance(entry, str) for entry in item):
                    item = "\n".join(item)
                if not isinstance(item, str):
                    raise FlowError(f"{path}: {dotted}: must be a string or a list of strings")
                values[dotted] = item
            elif kind is str:
                if not isinstance(item, str):
                    raise FlowError(f"{path}: {dotted}: must be a string")
                values[dotted] = item
    for pattern in values.get("gates.vacuous", ()):
        try:
            re.compile(pattern)
        except re.error as err:
            raise FlowError(f"{path}: gates.vacuous: {pattern!r} is not a regular expression ({err})") from None
    drafts = values.get("paths.drafts", "")
    parts = Path(drafts).parts
    # The drafts must stay inside the worktree (and out of .git), and the exclude line names the path's first directory.
    if drafts and (not parts or drafts.startswith("~") or Path(drafts).is_absolute() or ".." in parts or parts[0] == ".git"):
        raise FlowError(f"{path}: paths.drafts: {drafts!r} must be a directory below the worktree's top level")
    return Profile(
        path=path,
        digest=digest,
        prefix=values.get("prefix", ""),
        services=values.get("services", ()),
        generated=values.get("generated", ()),
        story_gates=values.get("gates.story", ()),
        final_gates=values.get("gates.final", ()),
        vacuous=values.get("gates.vacuous", ()),
        exclusive=values.get("build.exclusive", ()),
        markers=values.get("landmines.markers", ()),
        notes=values.get("landmines.notes", ""),
        idioms=values.get("review.idioms", ""),
        state=values.get("paths.state", ""),
        records=values.get("paths.records", ""),
        drafts=drafts,
        registries=values.get("design.registries", ""),
    )


def load_profile(top: Path) -> Profile:
    """The worktree's profile, validated; FlowError when there is none or it can't be used."""
    path = find_profile(top)
    raw = path.read_bytes()
    try:
        data = tomllib.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, tomllib.TOMLDecodeError) as err:
        raise FlowError(f"{path}: not valid TOML: {err}") from None
    return validate(data, path, hashlib.sha256(raw).hexdigest()[:12])


def optional_profile(top: Path) -> Profile | None:
    """The worktree's profile, or None when it has none; one that can't be used still raises."""
    try:
        find_profile(top)
    except FlowError:
        return None
    return load_profile(top)


def state_dir(top: Path, profile: Profile | None) -> Path:
    """Where a run keeps its state: the profile's paths.state, else <git-dir>/flow, one per worktree."""
    if profile is not None and profile.state:
        path = Path(profile.state).expanduser()
        return path if path.is_absolute() else top / path
    return Path(git(top, "rev-parse", "--path-format=absolute", "--git-dir")) / "flow"


def drafts_dir(top: Path, profile: Profile | None) -> Path:
    """Where /flow:draft keeps every plan's drafts: the profile's paths.drafts, else DEFAULT_DRAFTS, under `top`."""
    return top / (profile.drafts if profile is not None and profile.drafts else DEFAULT_DRAFTS)


def ignored(top: Path, directory: Path) -> bool:
    """Whether git's ignore rules cover `directory`, under the worktree at `top`, whether or not it exists yet."""
    # The trailing slash makes git read a missing path as a directory, which a rule such as `drafts/` needs.
    relative = directory.relative_to(top).as_posix() + "/"
    result = subprocess.run(["git", "-C", str(top), "check-ignore", "-q", "--", relative], capture_output=True, text=True)
    if result.returncode not in (0, 1):
        raise FlowError(f"git check-ignore failed in {top}: {' '.join(result.stderr.split())}")
    return result.returncode == 0


def exclude_line(top: Path, directory: Path) -> str | None:
    """The info/exclude line that keeps `directory` out of git, or None when git ignores it already.

    The line names the directory's first component below `top`, `/<first>/`. FlowError when git tracks
    files in that first directory: ignoring it would hide every new file beside them."""
    if ignored(top, directory):
        return None
    first = directory.relative_to(top).parts[0]
    if git(top, "ls-files", "--", first):
        raise FlowError(
            f"{directory} is not ignored, and {first}/ holds tracked files, so ignoring {first}/ would hide new "
            "files there: ignore the drafts directory in the repo's .gitignore, or point paths.drafts at a new "
            "top-level directory"
        )
    return f"/{first}/"


def make_drafts(top: Path, profile: Profile | None, stem: str) -> Path:
    """Part 2 spec §6.1: the drafts directory of the plan whose file stem is `stem`, made and kept out of git.

    When git doesn't ignore it, its exclude_line goes once into the file `git rev-parse --git-path
    info/exclude` names, which in a linked worktree is the common directory's: one line covers every
    worktree, and no tracked file changes. No directory is made when it can't be kept out of git,
    though the exclude line may stay."""
    # A path or a whole file name would make a directory plan_join never reads: it takes the plan's stem.
    if not stem or "/" in stem or stem in (".", "..") or stem.endswith(".md"):
        raise FlowError(f"drafts: {stem!r} is not a plan file stem (the plan's file name without .md)")
    path = drafts_dir(top, profile) / stem
    line = exclude_line(top, path)
    if line is not None:
        exclude = Path(git(top, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude"))
        text = exclude.read_text() if exclude.is_file() else ""
        if line not in text.splitlines():
            exclude.parent.mkdir(parents=True, exist_ok=True)
            exclude.write_text(text + ("" if not text or text.endswith("\n") else "\n") + line + "\n")
        if not ignored(top, path):
            raise FlowError(f"{path} is still not ignored with {line} in {exclude}: a rule in the repo un-ignores it")
    path.mkdir(parents=True, exist_ok=True)
    return path


def roster() -> str:
    """The roster ID loom gave this run, `loom flow roster <12 hex>` (FLOW_ROSTER)."""
    value = os.environ.get("FLOW_ROSTER", "").strip()
    if not value:
        raise FlowError("FLOW_ROSTER is not set: run flow's tools through `loom flow`")
    return value


def vacuous_lines(text: str, patterns: tuple[str, ...]) -> list[str]:
    """The lines of `text` that any of `patterns` matches (Python `re.search`)."""
    compiled = [re.compile(pattern) for pattern in patterns]
    return [line for line in text.splitlines() if any(rx.search(line) for rx in compiled)]


def _listed(items: tuple[str, ...]) -> str:
    return ", ".join(f"`{item}`" for item in items) if items else "none"


def _indented(text: str) -> list[str]:
    return ["    " + line if line.strip() else "" for line in (text.strip() or "none").splitlines()]


def role_block(profile: Profile, role: str, top: Path) -> list[str]:
    """One role's lines: what the supervisor, or /flow:draft for the drafter, pastes into that role's briefs."""
    prefix = f"`{profile.prefix}`" if profile.prefix else "none"
    if role == DRAFTER:
        packages = [
            "`{packages}` in a gate stands for the Cargo packages a story's commits change and the packages "
            "that depend on them; a task's own commands name their packages instead."
        ]
        return [
            f"Prefix for every build and test command a task gives: {prefix}",
            f"Story gates, run in this order after each story: {_listed(profile.story_gates)}",
            *(packages if any("{packages}" in gate for gate in profile.story_gates) else []),
            f"Generated files (the build regenerates them; no task writes them by hand): {_listed(profile.generated)}",
            f"Landmine markers (a story whose code holds one says so in its paragraph): {_listed(profile.markers)}",
            "Landmine notes:",
            *_indented(profile.notes),
            "Idioms reviewers check beyond CLAUDE.md:",
            *_indented(profile.idioms),
            "Design registries (a story that adds what one lists edits it too):",
            *_indented(profile.registries),
        ]
    if role == "developer":
        return [
            f"Prefix for every build and test command: {prefix}",
            f"Services (start each, as written, before the tests that need them): {_listed(profile.services)}",
            "Landmine notes (deep stories only):",
            *_indented(profile.notes),
        ]
    if role == "verifier":
        return [
            f"Services, each run once as written before the first gate: {_listed(profile.services)}",
            f"Vacuous output (a gate whose output matches one is VACUOUS, never PASS): {_listed(profile.vacuous)}",
        ]
    if role == "reviewer":
        return [
            f"Prefix for your check-only commands, such as the formatter check: {prefix}",
            "Landmine notes:",
            *_indented(profile.notes),
            "Idioms to check beyond CLAUDE.md:",
            *_indented(profile.idioms),
        ]
    return [
        f"Profile: {profile.path} sha256:{profile.digest}",
        f"Prefix: {prefix}",
        f"Exclusive builds (two stories whose Verify holds one never run at once): {_listed(profile.exclusive)}",
        f"Landmine markers: {_listed(profile.markers)}",
        f"State directory: {state_dir(top, profile)}",
        f"Record copy in the repo: {profile.records or 'none'}",
        f"Gates: {len(profile.story_gates)} per story, {len(profile.final_gates)} final; `flowconf.py gates` expands them",
    ]


def show(profile: Profile, role: str, top: Path) -> str:
    """`role`'s block; the supervisor's is followed by every other role's."""
    roles = ROLES if role == "supervisor" else (role,)
    return "\n\n".join(f"[{name}]\n" + "\n".join(role_block(profile, name, top)) for name in roles)


def status(cwd: Path) -> str:
    top = toplevel(cwd)
    key = repo_key(top)
    try:
        return f"key {key}; profile {find_profile(top)}"
    except FlowError:
        return f"key {key}; profile none"


def _vacuous_command(file: str, cwd: Path) -> int:
    """0 when a line of `file` matches the profile's gates.vacuous, 1 when none does, 2 on an error."""
    try:
        patterns = load_profile(toplevel(cwd)).vacuous
        try:
            text = sys.stdin.read() if file == "-" else Path(file).read_text(errors="replace")
        except OSError as err:
            raise FlowError(f"cannot read {file}: {err.strerror or err}") from None
    except FlowError as err:
        print(f"flowconf: {err}", file=sys.stderr)
        return 2
    matches = vacuous_lines(text, patterns)
    for line in matches:
        print(line)
    return 0 if matches else 1


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="flowconf.py", description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("key", help="the repo key")
    sub.add_parser("status", help="the key and the profile it would use")
    show_parser = sub.add_parser("show", help="one role's values, for its briefs")
    show_parser.add_argument("--for", dest="role", choices=(*ROLES, DRAFTER), required=True)
    sub.add_parser("state", help="the state directory, made if missing")
    sub.add_parser("services", help="the profile's services, one per line, as written")
    drafts_parser = sub.add_parser("drafts", help="a plan's drafts directory, made if missing and kept out of git")
    drafts_parser.add_argument("stem", metavar="STEM", help="the plan's file name without .md")
    vacuous_parser = sub.add_parser("vacuous", help="the lines of a gate's output that match gates.vacuous")
    vacuous_parser.add_argument("file", metavar="FILE", help="the output to scan; - is stdin")
    sub.add_parser("roster", help="the flow repo's commit and roster hash")
    gates_parser = sub.add_parser("gates", help="the gates to run, expanded")
    kind = gates_parser.add_mutually_exclusive_group(required=True)
    kind.add_argument("--story", nargs="+", metavar="COMMIT", help="the story's own commits")
    kind.add_argument("--final", action="store_true", help="the final gates")
    sub.add_parser("doctor", help="check that a run can start here")
    try:
        args = parser.parse_args(argv)
    except SystemExit as err:
        return 2 if err.code else 0
    cwd = Path.cwd()
    try:
        if args.command == "key":
            print(repo_key(cwd))
        elif args.command == "status":
            print(status(cwd))
        elif args.command == "show":
            top = toplevel(cwd)
            print(show(load_profile(top), args.role, top))
        elif args.command == "services":
            # Raw, for loom flow run's engine, which runs each before a gate run (flow in loom part 3b).
            for service in load_profile(toplevel(cwd)).services:
                print(service)
        elif args.command == "state":
            top = toplevel(cwd)
            path = state_dir(top, optional_profile(top))
            (path / "agent-reports").mkdir(parents=True, exist_ok=True)
            print(path)
        elif args.command == "drafts":
            top = toplevel(cwd)
            print(make_drafts(top, optional_profile(top), args.stem))
        elif args.command == "vacuous":
            return _vacuous_command(args.file, cwd)
        elif args.command == "roster":
            try:
                print(roster())
            except FlowError as err:
                print(f"flowconf: {err}", file=sys.stderr)
                return 2
        elif args.command == "gates":
            import packages  # imported here: packages imports this module

            return packages.gates_command(args.story, args.final, cwd)
        elif args.command == "doctor":
            import doctor  # imported here: doctor imports this module

            return doctor.main(cwd)
    except FlowError as err:
        print(f"flowconf: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    # Run as the module `flowconf`, so FlowError is one class here and in every module that imports it.
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    import flowconf

    sys.exit(flowconf.main(sys.argv[1:]))
