#!/usr/bin/env bash
# Makes the spec package available at .heron/packages/, where the tree and
# the tests read it, before anything is checked.
#
#   scripts/spec-setup.sh            heron run setup: installs the package
#                                    heron.lock pins, from git
#   HERON_SPECS_DIR=../heron-specs/heron scripts/spec-setup.sh
#                                    links that checkout of the package in
#                                    its place instead, for local work and CI
#                                    against an unreleased spec
#
# With HERON_SPECS_DIR set, the Go tests read the package from there too.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
pkg=.heron/packages/github.com/aiviarium/heron-specs/heron

if [ -n "${HERON_SPECS_DIR:-}" ]; then
  dir=$(cd "$HERON_SPECS_DIR" && pwd)
  [ -f "$dir/index.heron" ] || { echo "spec-setup: $dir has no index.heron" >&2; exit 1; }
  mkdir -p "$(dirname "$pkg")"
  rm -rf "$pkg"
  ln -s "$dir" "$pkg"
  echo "spec-setup: $pkg -> $dir"
  exit 0
fi

# An earlier override's link is replaced by the pinned install.
[ -L "$pkg" ] && rm "$pkg"
bin=$root/.heron/bin
mkdir -p "$bin"
go build -o "$bin/heron" ./cmd/heron
"$bin/heron" run setup "$@"
