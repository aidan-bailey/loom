"""The languages the plan scripts know: file extensions and item-definition patterns.

plan_premises reads a backticked token as a path when its extension is listed
here, and the items an added block defines through DEF_RES. Add a language here
when a repo needs one; nothing else in the scripts names a language.
"""

import re

# Extensions a plan's backticked path may end in.
EXTENSIONS = frozenset("rs ts tsx js mjs cjs py go mod sum lua toml lock md json jsonl yml yaml sh sql html scss css nix txt".split())
# Item definitions at a block's top level: Rust, TS/JS, Python, Go. Group 1 is the item's name.
DEF_RES = (
    re.compile(r"^(?:pub(?:\([^)]*\))?\s+)?(?:(?:async|const|unsafe|extern)\s+)*fn\s+(\w+)"),
    re.compile(r"^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|type|mod|const|static)\s+(\w+)"),
    re.compile(r"^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:async\s+)?(?:function|class|interface|enum|type)\s+([\w$]+)"),
    re.compile(r"^(?:export\s+)?const\s+([\w$]+)\s*[:=]"),
    re.compile(r"^(?:async\s+)?(?:def|class)\s+(\w+)"),
    # Go's top-level funcs, generics included. A method (`func (r T) Name`) never matches: its
    # name repeats across receivers in one file. Go's `type` and `const` match the Rust pattern,
    # and a grouped `var (`, `type (` or `const (` block matches nothing.
    re.compile(r"^func\s+(\w+)\s*[\[(]"),
    re.compile(r"^var\s+(\w+)"),
)
# Names too common to mean a collision: every impl has a `new`, every file a `mod tests`, and a Go
# file may hold several `func init`.
COMMON_NAMES = frozenset({"new", "default", "fmt", "from", "drop", "clone", "eq", "hash", "init", "main", "tests", "test"})
