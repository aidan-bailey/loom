#!/bin/sh
# hook_slow_child.sh <point>: like hook_slow.sh, but sleep runs as a child of
# the shell (no exec), so the runner's kill lands on the shell while sleep
# keeps the pipes open until WaitDelay closes them. Run by the tests with sh.
cat > /dev/null
sleep 20
