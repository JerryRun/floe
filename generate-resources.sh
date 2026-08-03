#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

mkdir -p .tools cmd/floe/assets internal/app/web/assets
GOPATH="${GOPATH:-$ROOT/.cache/gopath}"
GOCACHE="${GOCACHE:-$ROOT/.cache/go-build}"
export GOPATH GOCACHE

go run ./tools/makeicon \
  assets/floe-source.png \
  cmd/floe/assets/floe.ico \
  internal/app/web/assets/floe-64.png

if [[ ! -x .tools/rsrc ]]; then
  GOBIN="$ROOT/.tools" go install github.com/akavel/rsrc@v0.10.2
fi

.tools/rsrc \
  -arch amd64 \
  -ico cmd/floe/assets/floe.ico \
  -manifest cmd/floe/floe.exe.manifest \
  -o cmd/floe/rsrc_windows_amd64.syso

echo "Generated Floe Windows resources"
