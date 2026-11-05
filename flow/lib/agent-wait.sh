#!/usr/bin/env bash
# lib/agent-wait.sh — run a long command for a subagent while the agent stays
# in its turn.
#
# Claude Code counts an agent that has ended its turn to wait for a
# background command as not running, and an agent whose turn ends with none
# of its agents running must hand back. On 2026-10-14 a verifier started a
# 7-minute gate with run_in_background and ended its turn; its supervisor,
# with no other agent out, was made to hand back mid-run. An agent that waits
# with this script stays in its turn, so whoever dispatched it keeps a running
# agent.
#
#   agent-wait.sh start <name> -- <command> [args...]
#       Start <command> detached from the Bash call, its stdout and stderr in
#       <state>/agent-wait/<name>.log. Refuses a <name> still running.
#   agent-wait.sh wait <name> [seconds]
#       Wait up to [seconds] (default 170) for it to end, then print one of:
#         done <name> exit <status>   it ended; exits with its status
#         running <name>              still running; exits 75, wait again
#         lost <name> ...             it ended without a status; exits 70
#
# Make each wait a foreground Bash call with a 600000 ms timeout, so the wait
# always ends inside the call. The default stays under 180 s: Claude Code
# 2.1.316 moves a background subagent's foreground Bash call to the
# background once it has run 180 s (a server-side cap), and a moved wait
# leaves a process that later wakes its agent with a notification.
# <state> is the state directory of the current directory's repo
# (`flowconf.py state`: one per worktree); AGENT_WAIT_DIR overrides the
# whole log directory (tests).
#
# Inside a loom session (LOOM_LAUNCH set, `loom` on PATH) the command runs as a
# job in loom's work queue: `loom job run` waits until the job's claim fits,
# streams its output, adds `loom: job N ...` progress lines, and exits with
# the job's exit code (125 when loom could not run it; the log says why). A
# started step can therefore sit `running` while it waits its turn. Started
# with `setsid` outside the queue, a step ran on cores and memory the queue
# counted as free (2026-11-05). A command that already is `loom ...` runs as
# given, so a step its agent queued itself is not queued twice.

set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dir=""

usage() {
  sed -n '13,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

check_name() {
  [[ "$1" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "agent-wait: bad name '$1'" >&2; exit 2; }
}

resolve_dir() {
  if [[ -n "${AGENT_WAIT_DIR:-}" ]]; then
    dir="$AGENT_WAIT_DIR"
    return
  fi
  local state
  state="$(python3 "$here/flowconf.py" state)" || { echo "agent-wait: no state directory (flowconf.py state failed)" >&2; exit 2; }
  dir="$state/agent-wait"
}

start() {
  (( $# >= 3 )) && [[ "$2" == "--" ]] || usage
  local name=$1 base
  check_name "$name"
  shift 2
  resolve_dir
  base="$dir/$name"
  mkdir -p "$dir"
  if [[ -f "$base.pid" ]] && kill -0 "$(cat "$base.pid")" 2>/dev/null; then
    echo "agent-wait: $name is still running (pid $(cat "$base.pid"))" >&2
    exit 2
  fi
  rm -f "$base.pid" "$base.exit" "$base.log"
  local -a queue=()
  if [[ -n "${LOOM_LAUNCH:-}" && "${1##*/}" != loom ]] && command -v loom > /dev/null 2>&1; then
    local desc="$name: $*"
    desc=${desc//[$'\n\t']/ }
    queue=(loom job run -d "${desc:0:60}" --)
  fi
  # setsid puts the command in its own session, so it outlives the Bash call;
  # the inner shell records its pid first and its exit status last.
  setsid bash -c 'base=$1; shift
    echo $$ > "$base.pid"
    "$@" > "$base.log" 2>&1
    echo $? > "$base.exit.tmp" && mv "$base.exit.tmp" "$base.exit"' \
    agent-wait "$base" "${queue[@]}" "$@" < /dev/null > /dev/null 2>&1 &
  local tries=0
  until [[ -s "$base.pid" ]] || (( ++tries > 50 )); do sleep 0.1; done
  [[ -s "$base.pid" ]] || { echo "agent-wait: $name did not start" >&2; exit 1; }
  echo "started $name; log $base.log"
}

wait_for() {
  (( $# >= 1 && $# <= 2 )) || usage
  local name=$1 secs=${2:-170} base pid status
  check_name "$name"
  [[ "$secs" =~ ^[0-9]+$ ]] || usage
  resolve_dir
  base="$dir/$name"
  [[ -s "$base.pid" ]] || { echo "agent-wait: nothing started as $name" >&2; exit 2; }
  pid=$(cat "$base.pid")
  if kill -0 "$pid" 2>/dev/null; then
    timeout "$secs" tail --pid="$pid" -f /dev/null
  fi
  if [[ -f "$base.exit" ]]; then
    status=$(cat "$base.exit")
    echo "done $name exit $status"
    exit "$status"
  fi
  if kill -0 "$pid" 2>/dev/null; then
    echo "running $name"
    exit 75
  fi
  echo "lost $name: it ended without recording an exit status"
  exit 70
}

case "${1:-}" in
  start) shift; start "$@" ;;
  wait) shift; wait_for "$@" ;;
  *) usage ;;
esac
