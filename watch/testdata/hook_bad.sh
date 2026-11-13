#!/bin/sh
# hook_bad.sh <mode> <point>: a hook that misbehaves as <mode> says. The
# runner appends the point as the last argument. Run by the tests with sh.
mode=$1
cat > /dev/null
case "$mode" in
  invalid-json)
    printf '%s' 'this is not json'
    ;;
  non-zero)
    # A well-formed answer does not excuse a failing exit status.
    echo 'boom: the drift script crashed' >&2
    printf '%s' '{"start": true}'
    exit 3
    ;;
  flood)
    # A valid answer, then 2 MiB of blanks: valid JSON, but over the cap.
    printf '%s' '{"start": true}'
    head -c 2097152 /dev/zero | tr '\0' ' '
    ;;
  noisy)
    i=0
    while [ "$i" -lt 600 ]; do
      echo "line $i padding padding padding" >&2
      i=$((i + 1))
    done
    exit 1
    ;;
  bad-var)
    printf '%s' '{"start": true, "vars": {"Spec": "kalshi"}}'
    ;;
  reserved-kind)
    printf '%s' '{"nudges": [{"kind": "ci", "key": "k", "text": "t"}]}'
    ;;
  *)
    echo "hook_bad.sh: unknown mode '$mode'" >&2
    exit 2
    ;;
esac
