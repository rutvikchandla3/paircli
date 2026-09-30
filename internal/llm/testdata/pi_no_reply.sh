#!/bin/sh
# Fake `pi` binary for TestPi_Error/no reply: a well-formed stream with no
# assistant message in it.
set -eu
cat >/dev/null
cat <<'JSON'
{"type":"session","version":3}
{"type":"agent_start"}
{"type":"agent_settled"}
JSON
exit 0
