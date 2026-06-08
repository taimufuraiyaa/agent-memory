#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$ROOT_DIR/../../.." && pwd)"
BUILD_DIR="$ROOT_DIR/.build/release"
DIST_DIR="$ROOT_DIR/dist"
WORK_DIR="$ROOT_DIR/.build/menubar-package"
APP_NAME="${APP_NAME:-AgentMemoryMenuBar}"
APP_DISPLAY_NAME="${APP_DISPLAY_NAME:-agent-memory}"
BUNDLE_ID="${BUNDLE_ID:-com.timebooks.agent-memory.menubar}"
APP_VERSION="${APP_VERSION:-0.1.0}"
APP_BUILD="${APP_BUILD:-$(date -u +%Y%m%d%H%M%S)}"
EXECUTABLE_NAME="${EXECUTABLE_NAME:-agent-memory-menubar}"
BACKEND_EXECUTABLE_NAME="${BACKEND_EXECUTABLE_NAME:-agent-memory}"
APP_BUNDLE_PATH="$DIST_DIR/$APP_NAME.app"
PLIST_TEMPLATE="$ROOT_DIR/resources/Info.plist.template"
ICON_PATH="$ROOT_DIR/resources/AppIcon.icns"
SIGN_IDENTITY="${SIGN_IDENTITY:--}"
NOTARY_PROFILE="${NOTARY_PROFILE:-}"
NOTARY_KEYCHAIN_PROFILE="${NOTARY_KEYCHAIN_PROFILE:-$NOTARY_PROFILE}"
ZIP_PATH="$DIST_DIR/$APP_NAME.zip"

if [[ ! -f "$PLIST_TEMPLATE" ]]; then
  echo "missing Info.plist template: $PLIST_TEMPLATE" >&2
  exit 1
fi

cd "$ROOT_DIR"
mkdir -p "$WORK_DIR"
bash "$ROOT_DIR/scripts/generate_icon.sh" >/dev/null
swift build -c release
(cd "$REPO_ROOT" && go build -o "$WORK_DIR/$BACKEND_EXECUTABLE_NAME" ./cmd/agent-memory)

if [[ ! -x "$BUILD_DIR/$EXECUTABLE_NAME" ]]; then
  echo "built executable not found: $BUILD_DIR/$EXECUTABLE_NAME" >&2
  exit 1
fi
if [[ ! -x "$WORK_DIR/$BACKEND_EXECUTABLE_NAME" ]]; then
  echo "built backend executable not found: $WORK_DIR/$BACKEND_EXECUTABLE_NAME" >&2
  exit 1
fi

rm -rf "$APP_BUNDLE_PATH"
mkdir -p "$APP_BUNDLE_PATH/Contents/MacOS" "$APP_BUNDLE_PATH/Contents/Resources/bin"

cp "$BUILD_DIR/$EXECUTABLE_NAME" "$APP_BUNDLE_PATH/Contents/MacOS/$EXECUTABLE_NAME"
chmod +x "$APP_BUNDLE_PATH/Contents/MacOS/$EXECUTABLE_NAME"
cp "$WORK_DIR/$BACKEND_EXECUTABLE_NAME" "$APP_BUNDLE_PATH/Contents/Resources/bin/$BACKEND_EXECUTABLE_NAME"
chmod +x "$APP_BUNDLE_PATH/Contents/Resources/bin/$BACKEND_EXECUTABLE_NAME"
if [[ -f "$ICON_PATH" ]]; then
  cp "$ICON_PATH" "$APP_BUNDLE_PATH/Contents/Resources/AppIcon.icns"
fi

sed \
  -e "s#__APP_DISPLAY_NAME__#$APP_DISPLAY_NAME#g" \
  -e "s#__APP_EXECUTABLE__#$EXECUTABLE_NAME#g" \
  -e "s#__APP_BUNDLE_ID__#$BUNDLE_ID#g" \
  -e "s#__APP_NAME__#$APP_NAME#g" \
  -e "s#__APP_VERSION__#$APP_VERSION#g" \
  -e "s#__APP_BUILD__#$APP_BUILD#g" \
  "$PLIST_TEMPLATE" > "$APP_BUNDLE_PATH/Contents/Info.plist"

if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign "$SIGN_IDENTITY" "$APP_BUNDLE_PATH" >/dev/null
fi

if [[ -n "$NOTARY_KEYCHAIN_PROFILE" ]] && command -v xcrun >/dev/null 2>&1; then
  rm -f "$ZIP_PATH"
  ditto -c -k --keepParent "$APP_BUNDLE_PATH" "$ZIP_PATH"
  xcrun notarytool submit "$ZIP_PATH" --keychain-profile "$NOTARY_KEYCHAIN_PROFILE" --wait
  xcrun stapler staple "$APP_BUNDLE_PATH"
fi

test -f "$APP_BUNDLE_PATH/Contents/Info.plist"
test -x "$APP_BUNDLE_PATH/Contents/MacOS/$EXECUTABLE_NAME"
test -x "$APP_BUNDLE_PATH/Contents/Resources/bin/$BACKEND_EXECUTABLE_NAME"
if [[ -f "$ICON_PATH" ]]; then
  test -f "$APP_BUNDLE_PATH/Contents/Resources/AppIcon.icns"
fi

echo "$APP_BUNDLE_PATH"
