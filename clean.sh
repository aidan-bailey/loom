#!/usr/bin/env bash
# Wipes ALL loom state: every tmux session on the server and ~/.loom.
# Refuses inside a loom-managed tmux session, where that would destroy the
# loom instance hosting this shell. For dev sandboxes use
# `go run ./tools/loomdev down` instead.
if [ -n "${TMUX:-}" ]; then
  current="$(tmux display-message -p ${TMUX_PANE:+-t "$TMUX_PANE"} '#S' 2>/dev/null || true)"
  case "$current" in
    loom_*|claudesquad_*)
      echo "clean.sh: refusing to run inside loom-managed tmux session '$current'" >&2
      exit 1
      ;;
  esac
fi

tmux kill-server
rm -rf worktree*
rm -rf ~/.loom
rm -rf ~/.claude-squad
