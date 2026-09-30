#!/bin/sh
# Fake `pi` binary for TestPi_Error/nonzero exit: reports the failure the way
# the real one does — a reason on stderr, exit 1, nothing usable on stdout.
set -eu
cat >/dev/null
echo "Codex error: The usage limit has been reached" >&2
exit 1
