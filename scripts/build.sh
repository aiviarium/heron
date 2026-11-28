#!/usr/bin/env bash
# Builds bin/heron from a checkout, a fresh clone included: checks the
# prerequisites and builds the binary with its commit and build time
# stamped. The docs' theme is committed, built, under
# internal/tools/docs/assets, so the build needs Go alone.
#
#   scripts/build.sh              bin/heron
#   scripts/build.sh -o <path>    the binary at <path> instead of bin/heron
#
# Needs Go (the version go.mod names, or newer). The VS Code extension
# builds apart, from editors/vscode (its README says how).
set -euo pipefail
cd "$(dirname "$0")/.."
out=bin/heron
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift ;;
    *) echo "usage: scripts/build.sh [-o path]" >&2; exit 2 ;;
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
echo "built $out"
