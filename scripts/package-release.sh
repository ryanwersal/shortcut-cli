#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="$(python3 scripts/release.py validate --tag "${1:?release tag required}")"
for arch in amd64 arm64; do
  mkdir -p "dist/$arch"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/ryanwersal/shortcut-cli/internal/cli.Version=$version" \
    -o "dist/$arch/sc" ./cmd/sc
  codesign --force --sign - --timestamp=none --identifier dev.ryanwersal.shortcut-cli "dist/$arch/sc"
  codesign --verify "dist/$arch/sc"
  cp LICENSE README.md "dist/$arch/"
done
case "$(uname -m)" in
  arm64) native=arm64 ;;
  x86_64) native=amd64 ;;
  *) echo "Packaging smoke tests require a macOS runner" >&2; exit 1 ;;
esac
[[ "$(dist/$native/sc --version)" == "sc version $version" ]]
dist/$native/sc agent schema > dist/schema.json
python3 - "$version" <<'PY'
import json, sys
with open("dist/schema.json") as f:
    assert json.load(f)["data"]["version"] == sys.argv[1]
PY
for arch in amd64 arm64; do
  dist/$native/sc completions bash > "dist/$arch/sc.bash"
  dist/$native/sc completions zsh > "dist/$arch/_sc"
  dist/$native/sc completions fish > "dist/$arch/sc.fish"
done
file dist/amd64/sc | grep -q x86_64
file dist/arm64/sc | grep -q arm64
python3 scripts/release.py package --tag "$1"
(cd dist && shasum -a 256 -c checksums.txt)
ruby -c dist/shortcut-cli.rb
