#!/usr/bin/env bash
# Makes the spec package, and the authoring guide's package beside it, which
# the spec imports, available at .heron/packages/, where the tree and the
# tests read them, before anything is checked.
#
#   scripts/spec-setup.sh            heron run setup: installs the packages
#                                    heron.lock pins, from git
#   HERON_SPECS_DIR=../heron-specs/heron scripts/spec-setup.sh
#                                    links that checkout of the package, and
#                                    the authoring folder beside it, in their
#                                    place instead, for local work and CI
#                                    against an unreleased spec
#
# With HERON_SPECS_DIR set, the Go tests read the package from there too.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
pkgs=.heron/packages/github.com/aiviarium/heron-specs

if [ -n "${HERON_SPECS_DIR:-}" ]; then
  dir=$(cd "$HERON_SPECS_DIR" && pwd)
  [ -f "$dir/index.heron" ] || { echo "spec-setup: $dir has no index.heron" >&2; exit 1; }
  [ -f "$dir/../authoring/index.heron" ] || { echo "spec-setup: $(dirname "$dir") has no authoring/index.heron" >&2; exit 1; }
  mkdir -p "$pkgs"
  for p in heron authoring; do
    src=$dir
    [ $p = heron ] || src=$(cd "$dir/../$p" && pwd)
    rm -rf "${pkgs:?}/$p"
    ln -s "$src" "$pkgs/$p"
    echo "spec-setup: $pkgs/$p -> $src"
  done
  exit 0
fi

# An earlier override's links are replaced by the pinned install.
for p in heron authoring; do
  if [ -L "$pkgs/$p" ]; then rm "$pkgs/$p"; fi
done
bin=$root/.heron/bin
mkdir -p "$bin"
go build -o "$bin/heron" ./cmd/heron
"$bin/heron" run setup "$@"
