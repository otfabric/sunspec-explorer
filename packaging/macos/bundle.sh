#!/bin/sh
# Wraps the desktop binary in a macOS application bundle and signs it ad hoc.
#
#   packaging/macos/bundle.sh <binary> <version> <output directory>
#
# The result is "<output directory>/SunSpec Explorer.app". There is no
# Developer ID: the ad-hoc signature is what lets the binary run on Apple
# silicon at all; Gatekeeper still asks the user to approve a downloaded copy
# (see the README).
set -eu

if [ $# -ne 3 ]; then
	echo "usage: $0 <binary> <version> <output directory>" >&2
	exit 2
fi
binary=$1
version=$2
out=$3
here=$(cd "$(dirname "$0")" && pwd)
icon="$here/../../cmd/sunspec-explorer/appicon.png"
app="$out/SunSpec Explorer.app"

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
install -m 0755 "$binary" "$app/Contents/MacOS/sunspec-explorer"
sed "s/@VERSION@/$version/g" "$here/Info.plist" > "$app/Contents/Info.plist"

# The icon: every size macOS asks for, from the one 1024 px image.
iconset=$(mktemp -d)/appicon.iconset
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$icon" --out "$iconset/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z "$double" "$double" "$icon" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/appicon.icns"
rm -rf "$(dirname "$iconset")"

codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"
echo "$app"
