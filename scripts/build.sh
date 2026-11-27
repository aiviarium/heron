#!/usr/bin/env bash
# Builds bin/heron from a checkout, a fresh clone included: checks the
# prerequisites, installs the web dependencies, builds the editor's
# frontend into internal/tools/server/frontend/dist, which the binary
# embeds, and builds the binary with its commit and build time stamped.
#
#   scripts/build.sh              the frontend and bin/heron
#   scripts/build.sh --no-web     bin/heron alone, with the frontend already
#                                 built (or none: heron serve then serves
#                                 the docs and the API but no editor)
#   scripts/build.sh -o <path>    the binary at <path> instead of bin/heron
#
# Needs Go (the version go.mod names, or newer), and for the frontend
# Node.js 20 or newer with corepack, which runs the pnpm version
# web/package.json pins.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
out=bin/heron
web=1
while [ $# -gt 0 ]; do
  case "$1" in
    --no-web) web=0 ;;
    -o) out=$2; shift ;;
    *) echo "usage: scripts/build.sh [--no-web] [-o path]" >&2; exit 2 ;;
  esac
  shift
done

fail() { echo "build: $*" >&2; exit 1; }
step() { printf '== %s\n' "$*"; }

# Go: at least the version go.mod's go line names.
command -v go >/dev/null || fail "Go isn't installed: get it from https://go.dev/dl/"
want_go=$(sed -n 's/^go \([0-9.]*\)$/\1/p' go.mod)
have_go=$(go env GOVERSION | sed 's/^go//')
older() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" != "$2" ]; }
if older "$have_go" "$want_go"; then fail "Go $have_go is older than the $want_go go.mod needs"; fi
echo "go $have_go"

if [ "$web" = 1 ]; then
  command -v node >/dev/null || fail "Node.js isn't installed: get version 20 or newer from https://nodejs.org/, or build with --no-web"
  node_major=$(node -p 'process.versions.node.split(".")[0]')
  [ "$node_major" -ge 20 ] || fail "Node.js $(node --version) is older than 20"
  echo "node $(node --version)"
  # pnpm: the version web/package.json's packageManager pins, through
  # corepack, which ships with Node.js; a pnpm on the PATH of that version
  # does as well.
  want_pnpm=$(node -p 'require("./web/package.json").packageManager.split("@")[1]')
  if command -v pnpm >/dev/null && [ "$(pnpm --version 2>/dev/null)" = "$want_pnpm" ]; then
    pnpm=(pnpm)
  elif command -v corepack >/dev/null; then
    export COREPACK_ENABLE_DOWNLOAD_PROMPT=0
    pnpm=(corepack pnpm)
  else
    fail "neither pnpm $want_pnpm nor corepack is installed: run 'npm install -g corepack', or build with --no-web"
  fi
  echo "pnpm $("${pnpm[@]}" --version)"

  # Sparrow's TS package comes from the web/sparrow submodule, at the
  # commit go.mod's Sparrow module names; web/runtime's install builds it.
  step "sparrow: the web/sparrow submodule"
  if [ ! -f web/sparrow/ts/package.json ]; then
    git rev-parse --git-dir >/dev/null 2>&1 || fail "web/sparrow is empty and this isn't a git checkout: clone heron with git"
    git submodule update --init web/sparrow
  fi
  step "web: install the dependencies"
  (cd web && "${pnpm[@]}" install --frozen-lockfile --silent)
  step "web: build the runtime and the editor into internal/tools/server/frontend/dist"
  (cd web && "${pnpm[@]}" --filter @heron/runtime build >/dev/null && "${pnpm[@]}" --filter @heron/shell build --logLevel warn)
  test -f internal/tools/server/frontend/dist/index.html || fail "the frontend build wrote no index.html"
fi

step "go: build $out"
pkg=github.com/aiviarium/heron/internal/core/version
commit=unknown
if git rev-parse --git-dir >/dev/null 2>&1; then
  commit=$(git rev-parse HEAD)
  if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then commit="$commit-dirty"; fi
fi
built=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mkdir -p "$(dirname "$out")"
go build -trimpath -ldflags "-X $pkg.Commit=$commit -X $pkg.Built=$built" -o "$out" ./cmd/heron
"$out" version
if [ ! -f internal/tools/server/frontend/dist/index.html ]; then
  echo "build: no frontend was built, so heron serve serves the docs and the API but no editor" >&2
fi
echo "built $out"
