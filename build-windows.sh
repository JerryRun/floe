#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

mkdir -p dist .cache/go-build .cache/gopath
export GOCACHE="${FLOE_GOCACHE:-$ROOT/.cache/go-build}"
export GOPATH="${FLOE_GOPATH:-$ROOT/.cache/gopath}"
export CGO_ENABLED=0
export GOOS=windows
export GOARCH=amd64

VERSION="${FLOE_VERSION:-0.1.0}"
go build -buildvcs=false -trimpath -ldflags="-s -w -H=windowsgui -X floe/internal/app.Version=$VERSION" -o dist/Floe.exe ./cmd/floe
echo "Built $ROOT/dist/Floe.exe"
