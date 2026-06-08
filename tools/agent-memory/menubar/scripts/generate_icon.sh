#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RES_DIR="$ROOT_DIR/resources"
WORK_DIR="$ROOT_DIR/.build/icon"
ICONSET_DIR="$WORK_DIR/AppIcon.iconset"
BASE_PNG="$WORK_DIR/AppIcon-1024.png"
OUT_ICNS="$RES_DIR/AppIcon.icns"

mkdir -p "$WORK_DIR" "$ICONSET_DIR" "$RES_DIR"
swift "$ROOT_DIR/scripts/render_icon.swift" "$BASE_PNG"

for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$BASE_PNG" --out "$ICONSET_DIR/icon_${size}x${size}.png" >/dev/null
  retina=$((size * 2))
  sips -z "$retina" "$retina" "$BASE_PNG" --out "$ICONSET_DIR/icon_${size}x${size}@2x.png" >/dev/null
done

iconutil -c icns "$ICONSET_DIR" -o "$OUT_ICNS"
echo "$OUT_ICNS"
