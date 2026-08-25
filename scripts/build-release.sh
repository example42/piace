#!/usr/bin/env bash
#
# Build PIACE release artifacts and the SHA-256 checksum manifest.
#
# See docs/release.md for the full procedure, including detached
# signature generation and the verification steps a consumer runs.
#
# Usage: scripts/build-release.sh VERSION [OUTPUT_DIR]

set -euo pipefail

version="${1:?usage: scripts/build-release.sh VERSION [OUTPUT_DIR]}"
outdir="${2:-dist}"

# The supported OS/architecture matrix is release metadata (design.md
# section 11), edited here deliberately rather than derived at run time.
platforms=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

mkdir -p "$outdir"
rm -f "$outdir/SHA256SUMS"

for platform in "${platforms[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"
  artifact="piace-${version}-${os}-${arch}"

  # CGO_ENABLED=0 is requirements.md 12.1's "CGO-free Go binary";
  # -trimpath removes local filesystem paths so two builds of the same
  # commit on different machines produce the same bytes.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.toolVersion=${version}" \
    -o "$outdir/$artifact" ./cmd/piace

  echo "built $artifact"
done

# One manifest covering every artifact, in a stable order.
(
  cd "$outdir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum piace-"${version}"-* | sort -k2 > SHA256SUMS
  else
    # macOS ships shasum rather than sha256sum; -a 256 is the same digest
    # and the same output format.
    shasum -a 256 piace-"${version}"-* | sort -k2 > SHA256SUMS
  fi
)

echo
echo "wrote $outdir/SHA256SUMS:"
cat "$outdir/SHA256SUMS"
echo
echo "Next: sign the manifest and publish both files. See docs/release.md."
