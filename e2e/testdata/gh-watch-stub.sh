#!/bin/sh
# A stand-in for `gh` for the issue watcher's e2e test (e2e/watch_test.go).
# The test puts a copy named `gh` first on PATH before the daemon starts and
# keeps the answers in $WATCH_STUB_DIR: issues.json is what
# `gh issue list --label ...` prints and prs.json what
# `gh pr list --author ...` prints, both in gh's own --json shape. The
# existing GitHub poll's `pr list` (no --author) and any `issue list` without
# --label get an empty list; `auth status` succeeds, as the existing poll's
# gh check needs. `gh issue view` (the existing poll's backfill, and the
# watcher's view of a closed issue a PR names, which this test never needs:
# both PR-named issues are in the open list) is unhandled and fails, and
# each caller drops that one read.
# Every call is appended to $WATCH_STUB_DIR/calls.log, so the test can prove
# the watcher reached this stub rather than a real gh. A call it does not
# know fails, as gh does with no network.
dir=${WATCH_STUB_DIR:?WATCH_STUB_DIR is not set}
printf '%s\n' "$*" >> "$dir/calls.log"
case "$1 $2" in
  "api user")
    echo watcher-bot
    ;;
  "pr list")
    case " $* " in
      *" --author "*) cat "$dir/prs.json" ;;
      *) echo '[]' ;;
    esac
    ;;
  "issue list")
    case " $* " in
      *" --label "*) cat "$dir/issues.json" ;;
      *) echo '[]' ;;
    esac
    ;;
  "auth status")
    echo "Logged in to github.com as watcher-bot (watch stub)"
    ;;
  *)
    echo "gh-watch-stub: unhandled: $*" >&2
    exit 1
    ;;
esac
