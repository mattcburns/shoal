#!/usr/bin/env bash
# Build CGO-free multi-platform shoal binaries and a license notice bundle.
# Output: dist/shoal_<os>_<arch> and dist/checksums.txt
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-}"
if [[ -z "$VERSION" ]]; then
  if git describe --tags --always --dirty 2>/dev/null | grep -q .; then
    VERSION="$(git describe --tags --always --dirty)"
  else
    VERSION="dev"
  fi
fi

DIST="${DIST:-$ROOT/dist}"
mkdir -p "$DIST"

LDFLAGS="-s -w -X github.com/mattcburns/shoal/internal/cli.Version=${VERSION}"
MODULE_MAIN="./cmd/shoal"

# GOOS/GOARCH pairs for Phase 6c
TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

echo "Building shoal version=${VERSION} into ${DIST}"

for target in "${TARGETS[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  out="${DIST}/shoal_${goos}_${goarch}"
  echo "  -> ${out}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" "$MODULE_MAIN"
done

# License / attribution bundle for redistribution
cp -f LICENSE NOTICE docs/third-party-licenses.md "$DIST/"
# Also name-stable copies for archives
cp -f docs/third-party-licenses.md "$DIST/THIRD_PARTY_LICENSES.md"

(
  cd "$DIST"
  # Portable checksums (sha256sum on Linux; shasum on macOS)
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum shoal_* > checksums.txt
  else
    shasum -a 256 shoal_* > checksums.txt
  fi
)

echo "Done. Artifacts:"
ls -la "$DIST"
