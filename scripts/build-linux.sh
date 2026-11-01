#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${AND_VERSION:-$(cat "$ROOT/VERSION")}"
cd "$ROOT"
mkdir -p dist
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o dist/and ./cmd/and
echo "Binary: $ROOT/dist/and"
