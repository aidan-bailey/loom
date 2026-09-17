#!/usr/bin/env bash
# clean.sh (including its loom-pane guard) plus `git worktree prune`.
bash "$(dirname "$0")/clean.sh" || exit 1
git worktree prune
