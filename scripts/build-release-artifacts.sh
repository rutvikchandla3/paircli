#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 VERSION" >&2
  exit 2
fi

VERSION="$1"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
  echo "VERSION must be a semantic version such as 0.2.0" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_ROOT="${PAIRCLI_RELEASE_OUTPUT_DIR:-$ROOT/dist/paircli}"
PUBLIC_BASE_URL="${PAIRCLI_PUBLIC_BASE_URL:-https://downloads.pair.sh/paircli}"
MIN_PLUGIN_VERSION="${PAIRCLI_MIN_PLUGIN_VERSION:-0.1.0}"
MINIMUM_VERSION="${PAIRCLI_MINIMUM_VERSION:-$VERSION}"
RELEASE_DIR="$OUTPUT_ROOT/$VERSION"

mkdir -p "$RELEASE_DIR"

build_target() {
  local target="$1"
  local goos="$2"
  local goarch="$3"
  local output="$RELEASE_DIR/paircli-$target"

  echo "building $target" >&2
  (
    cd "$ROOT"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$output" ./cmd/paircli
  )
  shasum -a 256 "$output" | awk '{print $1}'
}

DARWIN_ARM64_SHA="$(build_target darwin-arm64 darwin arm64)"
DARWIN_AMD64_SHA="$(build_target darwin-amd64 darwin amd64)"
LINUX_AMD64_SHA="$(build_target linux-amd64 linux amd64)"
LINUX_ARM64_SHA="$(build_target linux-arm64 linux arm64)"

cat >"$RELEASE_DIR/SHA256SUMS" <<EOF
$DARWIN_ARM64_SHA  paircli-darwin-arm64
$DARWIN_AMD64_SHA  paircli-darwin-amd64
$LINUX_AMD64_SHA  paircli-linux-amd64
$LINUX_ARM64_SHA  paircli-linux-arm64
EOF

cat >"$OUTPUT_ROOT/latest.json" <<EOF
{
  "schema": "paircli-release/v1",
  "version": "$VERSION",
  "minimum_version": "$MINIMUM_VERSION",
  "min_plugin_version": "$MIN_PLUGIN_VERSION",
  "revoked": [],
  "artifacts": {
    "darwin-arm64": {
      "url": "$PUBLIC_BASE_URL/$VERSION/paircli-darwin-arm64",
      "sha256": "$DARWIN_ARM64_SHA"
    },
    "darwin-amd64": {
      "url": "$PUBLIC_BASE_URL/$VERSION/paircli-darwin-amd64",
      "sha256": "$DARWIN_AMD64_SHA"
    },
    "linux-amd64": {
      "url": "$PUBLIC_BASE_URL/$VERSION/paircli-linux-amd64",
      "sha256": "$LINUX_AMD64_SHA"
    },
    "linux-arm64": {
      "url": "$PUBLIC_BASE_URL/$VERSION/paircli-linux-arm64",
      "sha256": "$LINUX_ARM64_SHA"
    }
  }
}
EOF

echo "release artifacts written to $OUTPUT_ROOT"
