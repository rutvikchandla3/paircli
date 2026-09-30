#!/bin/sh
# Fake `pi` binary for TestPi_Success. Verifies the flags internal/llm/pi.go
# sends, then prints a canned --mode json event stream on stdout.
set -eu

have_p=0
have_mode_json=0
have_no_session=0
have_no_tools=0
model=""
append_system=""

while [ $# -gt 0 ]; do
  case "$1" in
    -p)
      have_p=1
      shift
      ;;
    --mode)
      shift
      if [ "${1:-}" != "json" ]; then
        echo "unexpected --mode value: ${1:-}" >&2
        exit 1
      fi
      have_mode_json=1
      shift
      ;;
    --no-session)
      have_no_session=1
      shift
      ;;
    --no-tools)
      have_no_tools=1
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

if [ "$have_p" -ne 1 ] || [ "$have_mode_json" -ne 1 ]; then
  echo "missing required -p/--mode json flags" >&2
  exit 1
fi
if [ "$have_no_session" -ne 1 ] || [ "$have_no_tools" -ne 1 ]; then
  echo "missing --no-session/--no-tools" >&2
  exit 1
fi
if [ "$model" != "pi-test-model" ]; then
  echo "unexpected --model value: $model" >&2
  exit 1
fi
if [ "$append_system" != "be terse" ]; then
  echo "unexpected --append-system-prompt value: $append_system" >&2
  exit 1
fi

prompt="$(cat)"
if [ "$prompt" != "say hi" ]; then
  echo "unexpected prompt on stdin: $prompt" >&2
  exit 1
fi

cat <<'EOF'
{"type":"session","version":3,"id":"01a0f1ef-66bb-7403-85ae-89f56a07ba70"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"say hi"}]}}
{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"say hi"}]}}
{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"stop","usage":{"input":0,"output":0}}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello from "},{"type":"text","text":"pi"}],"stopReason":"stop","usage":{"input":7,"output":9}}}
{"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":"hello from pi"}],"stopReason":"stop","usage":{"input":7,"output":9}},"toolResults":[]}
{"type":"agent_end","messages":[]}
{"type":"agent_settled"}
EOF
