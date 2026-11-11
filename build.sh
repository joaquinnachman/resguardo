#!/usr/bin/env bash
# Compila Resguardo para Windows y Mac (Apple Silicon + Intel en una sola app).
set -euo pipefail
cd "$(dirname "$0")"
VERSION=${RESGUARDO_VERSION:-$(grep 'AppVersion =' model.go | cut -d'"' -f2)}
X="-X main.AppVersion=${VERSION}"
export CGO_ENABLED=0
DIST=${DIST:-dist}
rm -rf "$DIST" && mkdir -p "$DIST/tmp"

echo "Windows…"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui ${X}" -o "$DIST"/tmp/Resguardo.exe .

echo "Mac…"
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w ${X}" -o "$DIST"/tmp/mac-arm64 .
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w ${X}" -o "$DIST"/tmp/mac-amd64 .
APP="$DIST"/tmp/Resguardo.app
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
python3 scripts/lipo.py "$APP/Contents/MacOS/Resguardo" "$DIST"/tmp/mac-arm64 "$DIST"/tmp/mac-amd64
chmod 755 "$APP/Contents/MacOS/Resguardo"
cp icons/app.icns "$APP/Contents/Resources/app.icns"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Resguardo</string>
  <key>CFBundleDisplayName</key><string>Resguardo</string>
  <key>CFBundleIdentifier</key><string>ar.claraboya.resguardo</string>
  <key>CFBundleExecutable</key><string>Resguardo</string>
  <key>CFBundleIconFile</key><string>app</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHumanReadableCopyright</key><string>Joaquín Nachman · Claraboya Estudio</string>
</dict>
</plist>
PLIST

cp LEEME.txt "$DIST"/tmp/LEEME.txt
(cd "$DIST/tmp" && zip -qr -X "../Resguardo-${VERSION}-Windows.zip" Resguardo.exe LEEME.txt)
(cd "$DIST/tmp" && zip -qry -X "../Resguardo-${VERSION}-Mac.zip" Resguardo.app LEEME.txt)
cp "$DIST"/tmp/Resguardo.exe "$DIST"/
rm -rf "$DIST/tmp"
ls -la "$DIST"
