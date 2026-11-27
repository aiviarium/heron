#!/usr/bin/env bash
# Makes Sparrow RPC available where heron's build finds it, until it's
# published: a checkout of aiviarium/sparrow beside this repository, at
# ../sparrow, which go.mod's replace (../sparrow/go) and web/runtime's
# link: dependency (../sparrow/ts) both name. It builds the TS package's
# dist/, which web/runtime imports.
#
#   scripts/sparrow-setup.sh               checks ../sparrow and builds its TS package
#   SPARROW_DIR=~/src/sparrow scripts/sparrow-setup.sh
#                                          links that checkout in at ../sparrow first
set -euo pipefail
cd "$(dirname "$0")/.."
dir=../sparrow
if [ -n "${SPARROW_DIR:-}" ]; then
  src=$(cd "$SPARROW_DIR" && pwd)
  if [ "$src" != "$(cd .. && pwd)/sparrow" ]; then
    [ -e "$dir" ] && [ ! -L "$dir" ] && { echo "sparrow-setup: $dir exists and isn't a link; move it or unset SPARROW_DIR" >&2; exit 1; }
    ln -sfn "$src" "$dir"
  fi
fi
if [ ! -f "$dir/go/go.mod" ] || [ ! -f "$dir/ts/package.json" ]; then
  echo "sparrow-setup: no Sparrow checkout at $dir: clone aiviarium/sparrow beside heron, or set SPARROW_DIR" >&2
  exit 1
fi
if [ "${1:-}" = "--go-only" ]; then
  exit 0
fi
command -v npm >/dev/null || { echo "sparrow-setup: npm isn't installed: Node.js 20 or newer builds Sparrow's TS package" >&2; exit 1; }
(cd "$dir/ts" && { [ -d node_modules ] || npm ci --silent; } && npm run --silent build)
echo "sparrow-setup: $dir ($(git -C "$dir" rev-parse --short HEAD 2>/dev/null || echo unknown)), TS package built"
