#!/bin/sh
# hook_slow.sh <point>: a hook that does not answer within any timeout a test
# sets. exec replaces the shell with sleep, so the runner's kill lands on the
# process that holds the pipes. Run by the tests with sh.
cat > /dev/null
exec sleep 30
