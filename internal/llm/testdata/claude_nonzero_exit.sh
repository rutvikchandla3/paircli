#!/bin/sh
# Fake `claude` binary for TestClaudeCLI_Error: simulates a CLI failure
# (non-zero exit with a stderr message).
cat >/dev/null
echo "boom: something went wrong talking to the model" >&2
exit 1
