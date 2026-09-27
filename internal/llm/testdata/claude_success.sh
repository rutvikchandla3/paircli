#!/bin/sh
# Fake `claude` binary for TestClaudeCLI_Success. Verifies the flags
# internal/llm/claudecli.go sends, then prints canned JSON on stdout.
set -eu

have_p=0
have_output_json=0
model=""
append_system=""

while [ $# -gt 0 ]; do
  case "$1" in
    -p)
      have_p=1
      shift
      ;;
    --output-format)
      shift
      if [ "${1:-}" != "json" ]; then
        echo "unexpected --output-format value: ${1:-}" >&2
        exit 1
      fi
      have_output_json=1
      shift
      ;;
    --model)
      shift
      model="${1:-}"
      shift
      ;;
    --append-system-prompt)
      shift
      append_system="${1:-}"
      shift
      ;;
    *)
      echo "unexpected arg: $1" >&2
      exit 1
      ;;
  esac
done

if [ "$have_p" -ne 1 ] || [ "$have_output_json" -ne 1 ]; then
  echo "missing required -p/--output-format json flags" >&2
  exit 1
fi
if [ "$model" != "claude-test-model" ]; then
  echo "unexpected --model value: $model" >&2
  exit 1
fi
if [ "$append_system" != "be terse" ]; then
  echo "unexpected --append-system-prompt value: $append_system" >&2
  exit 1
fi

prompt="$(cat)"
if [ -z "$prompt" ]; then
  echo "expected a prompt on stdin" >&2
  exit 1
fi
if [ "$prompt" != "say hi" ]; then
  echo "unexpected prompt on stdin: $prompt" >&2
  exit 1
fi

cat <<'EOF'
{"result":"hello from claude cli","is_error":false,"usage":{"input_tokens":11,"output_tokens":22}}
EOF
