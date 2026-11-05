#!/usr/bin/env bash
# lib/test.sh — loom's gate for flow's tools: every unit test in lib/, then the
# agent lint. CI runs it (.github/workflows/flow.yml); run it after changing
# flow/.

set -uo pipefail
# No __pycache__ in the worktree: loom's diff probe would give it index entries.
export PYTHONDONTWRITEBYTECODE=1

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here" || exit 2
shopt -s nullglob
tests=(*_test.py)
if (( ${#tests[@]} == 0 )); then
  echo "FAIL [tests] no *_test.py in $here" >&2
  exit 1
fi
status=0
python3 -m unittest -q "${tests[@]}" || status=1
bash "$here/check-agents.sh" || status=1
exit "$status"
