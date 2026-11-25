#!/usr/bin/env bash
# The published site, as .github/workflows/pages.yml deploys it to GitHub
# Pages: the project site (architecture, conformance, releases) at the root
# and the spec package's own site under spec/, where the project's
# `implements { docs = ... }` links spec nodes to.
#
#   scripts/pages.sh <out dir>
#
# The spec package must be set up first (scripts/spec-setup.sh, with or
# without HERON_SPECS_DIR).
set -euo pipefail
[ $# -eq 1 ] || { echo "usage: scripts/pages.sh <out dir>" >&2; exit 2; }
mkdir -p "$1"
out=$(cd "$1" && pwd)
cd "$(dirname "$0")/.."
root=$PWD

spec=$(go run ./internal/core/fixtures/specdir)
bin=$root/.heron/bin
mkdir -p "$bin"
go build -o "$bin/heron" ./cmd/heron

rm -rf "$out"/* .heron/docs
"$bin/heron" generate docs >/dev/null
cp -r .heron/docs/. "$out/"

# The generator writes .heron/docs under the directory it runs in.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
(cd "$work" && "$bin/heron" generate --entry "$spec" docs >/dev/null)
cp -r "$work/.heron/docs" "$out/spec"

test -f "$out/index.html"
test -f "$out/_conformance.html"
test -f "$out/spec/index.html"
echo "$out: $(find "$out" -maxdepth 1 -name '*.html' | wc -l | tr -d ' ') project pages, $(find "$out/spec" -maxdepth 1 -name '*.html' | wc -l | tr -d ' ') spec pages"
