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

if [ -n "${FLOE_VERSION:-}" ]; then
  VERSION="${FLOE_VERSION#v}"
  RELEASE_TAG="v$VERSION"
else
  RELEASE_TAG="$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)"
  REVISION="$(git rev-parse --short HEAD 2>/dev/null || true)"
  VERSION="${RELEASE_TAG#v}"
  if [ -z "$VERSION" ]; then
    VERSION="${REVISION:+dev+$REVISION}"
    VERSION="${VERSION:-dev}"
  elif [ "$(git rev-list -n 1 "$RELEASE_TAG")" != "$(git rev-parse HEAD)" ] ||
       [ -n "$(git status --porcelain)" ]; then
    VERSION="$VERSION-dev+$REVISION"
  fi
fi

go build -buildvcs=false -trimpath -ldflags="-s -w -H=windowsgui -X floe/internal/app.Version=$VERSION -X main.releaseTag=$RELEASE_TAG" -o "${FLOE_BUILD_OUTPUT:-dist/Floe.exe}" ./cmd/floe
echo "Built ${FLOE_BUILD_OUTPUT:-$ROOT/dist/Floe.exe} ($VERSION)"
