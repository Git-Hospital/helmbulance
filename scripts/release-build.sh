#!/usr/bin/env bash
# Builds the release archives for a tag: helmbulance_<tag>_<os>_<arch>.tar.gz for Linux and macOS
# on amd64 and arm64, each holding the binary, LICENSE and README.md, and SHA256SUMS over them.
# Used by .github/workflows/release.yml; runs the same way locally, on a clean checkout of the tag.
# The output directory must be outside the checkout: Go stamps the version from git, and any
# untracked file makes it v0.1.0+dirty.
#
#   scripts/release-build.sh v0.1.0 "$RUNNER_TEMP/dist"
set -euo pipefail

tag=${1:?usage: release-build.sh <tag> <out-dir>}
out=${2:?usage: release-build.sh <tag> <out-dir>}

if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
  echo "release-build.sh: $tag is not a version tag like v0.1.0" >&2
  exit 2
fi
if [[ -e $out ]]; then
  echo "release-build.sh: $out already exists; give an empty path" >&2
  exit 2
fi
if [[ -n $(git status --porcelain) ]]; then
  echo "release-build.sh: the checkout has changes; a release is built from the tag as committed" >&2
  exit 2
fi
mkdir -p "$out"
out=$(cd "$out" && pwd -P)
top=$(git rev-parse --show-toplevel)
top=$(cd "$top" && pwd -P)
if [[ $out/ == "$top"/* ]]; then
  rmdir "$out"
  echo "release-build.sh: $out is inside the checkout; give a path outside it" >&2
  exit 2
fi

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${target%/*}
  arch=${target#*/}
  name="helmbulance_${tag}_${os}_${arch}"
  mkdir "$out/$name"
  # -trimpath keeps the builder's paths out of the binary. Go stamps the module version from the
  # tag the checkout is on, which the go install of the same tag also reports.
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -o "$out/$name/helmbulance" ./cmd/helmbulance
  stamped=$(go version -m "$out/$name/helmbulance" | awk '$1 == "mod" { print $3 }')
  if [[ $stamped != "$tag" ]]; then
    echo "release-build.sh: $name reports version $stamped, not $tag; is the checkout on the tag?" >&2
    exit 1
  fi
  cp LICENSE README.md "$out/$name/"
  tar -C "$out" -czf "$out/$name.tar.gz" "$name"
  rm -r "${out:?}/$name"
done

(cd "$out" && sha256sum helmbulance_*.tar.gz >SHA256SUMS)
