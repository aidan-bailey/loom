#!/bin/sh
# hook_ok.sh [mode] <point>: a well-behaved hook. The runner appends the point
# as the last argument; a mode, when a test passes one, comes first. When
# $HOOK_CAPTURE_DIR is set, the script records there what the runner gave it.
# Run by the tests with sh.
mode=
if [ "$#" -gt 1 ]; then mode=$1; fi
for point; do :; done
if [ -n "$HOOK_CAPTURE_DIR" ]; then
  cat > "$HOOK_CAPTURE_DIR/stdin.json"
  printf '%s\n' "$point" > "$HOOK_CAPTURE_DIR/point"
  printf '%s\n' "$#" > "$HOOK_CAPTURE_DIR/argc"
  printf '%s\n' "$PWD" > "$HOOK_CAPTURE_DIR/pwd"
  pwd -P > "$HOOK_CAPTURE_DIR/cwd"
  printf '%s\n' "$LOOM_WATCH_WORKSPACE" > "$HOOK_CAPTURE_DIR/workspace"
  printf '%s\n' "$LOOM_WATCH_REPO" > "$HOOK_CAPTURE_DIR/repo"
  printf '%s\n' "$LOOM_HOOK_TEST_INHERITED" > "$HOOK_CAPTURE_DIR/inherited"
else
  cat > /dev/null
fi
case "$point:$mode" in
  start:refuse)
    printf '%s' '{"start": false, "reason": "no free row", "vars": {"spec": "dropped"}}'
    ;;
  start:*)
    printf '%s' '{"start": true, "vars": {"spec": "kalshi-ws", "row": "R33"}}'
    ;;
  nudges:*)
    printf '%s' '{"nudges": [{"kind": "redrift", "key": "k1", "text": "rerun the drift check"}]}'
    ;;
  *)
    echo "hook_ok.sh: unknown point '$point'" >&2
    exit 2
    ;;
esac
