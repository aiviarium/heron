#!/usr/bin/env bash
# Runs `heron serve` on a temporary copy of examples/petstore and prints its
# URL. The copy is a git repository, so the status bar shows a branch, and
# it's removed when the server stops. --build rebuilds the frontend first.
#   scripts/demo.sh [--build] [--addr host:port]
set -euo pipefail
cd "$(dirname "$0")/.."
addr=127.0.0.1:0
build=0
while [ $# -gt 0 ]; do
  case "$1" in
    --build) build=1 ;;
    --addr) addr=$2; shift ;;
    *) echo "usage: scripts/demo.sh [--build] [--addr host:port]" >&2; exit 2 ;;
  esac
  shift
done
if [ "$build" = 1 ] || [ ! -f internal/tools/server/frontend/dist/index.html ]; then
  (cd web && pnpm install --frozen-lockfile --silent && pnpm --filter @heron/shell build >/dev/null)
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/heron" ./cmd/heron
cp -r examples/petstore "$tmp/petstore"
rm -rf "$tmp/petstore/node_modules"
(cd "$tmp/petstore" && git init -q -b main && git add -A && git -c user.name=demo -c user.email=demo@localhost commit -qm demo)
cd "$tmp/petstore"
"$tmp/heron" serve --addr "$addr"
