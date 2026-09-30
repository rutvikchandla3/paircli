#!/bin/sh
# Fake `pi` binary for TestPi_Error/stream error: exits 0 but ends the turn
# with stopReason "error", which is how a refused turn can still look clean.
set -eu
cat >/dev/null
cat <<'JSON'
{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"provider refused the request","usage":{"input":0,"output":0}}}
JSON
exit 0
