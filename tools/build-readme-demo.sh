#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SOURCE="${1:-/mnt/c/Windows/Temp/floe-readme-demo}"
FRAME_PATTERN="$SOURCE/frame-%03d.png"
HERO_SOURCE="$SOURCE/floe-server-to-server.png"
OUTPUT_DIR="$ROOT/docs/demo"
SCREENSHOT_DIR="$ROOT/docs/screenshots"

if [[ ! -f "$SOURCE/frame-000.png" ]]; then
  echo "Missing captured frames under $SOURCE" >&2
  exit 1
fi
if [[ ! -f "$HERO_SOURCE" ]]; then
  echo "Missing hero screenshot: $HERO_SOURCE" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR" "$SCREENSHOT_DIR"
cp "$HERO_SOURCE" "$SCREENSHOT_DIR/floe-server-to-server.png"

PALETTE="$(mktemp --suffix=.png)"
trap 'rm -f "$PALETTE"' EXIT

ffmpeg -hide_banner -loglevel error -y \
  -framerate 2 -i "$FRAME_PATTERN" \
  -vf "fps=2,scale=1200:-1:flags=lanczos,palettegen=max_colors=128:stats_mode=diff" \
  "$PALETTE"

ffmpeg -hide_banner -loglevel error -y \
  -framerate 2 -i "$FRAME_PATTERN" -i "$PALETTE" \
  -lavfi "fps=2,scale=1200:-1:flags=lanczos[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=3:diff_mode=rectangle" \
  -loop 0 "$OUTPUT_DIR/floe-server-to-server.gif"

chmod 644 \
  "$SCREENSHOT_DIR/floe-server-to-server.png" \
  "$OUTPUT_DIR/floe-server-to-server.gif"

echo "Created:"
echo "  $SCREENSHOT_DIR/floe-server-to-server.png"
echo "  $OUTPUT_DIR/floe-server-to-server.gif"
