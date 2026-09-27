#!/bin/sh
# Fake `claude` binary for TestClaudeCLI_Error: simulates the CLI exiting 0
# but reporting is_error: true in its JSON result.
cat >/dev/null
cat <<'EOF'
{"result":"the model declined to answer","is_error":true,"usage":{"input_tokens":5,"output_tokens":0}}
EOF
