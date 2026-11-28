#!/usr/bin/env bash
# The published site, as .github/workflows/pages.yml deploys it to GitHub
# Pages: the spec package's site with every lens its docs block builds
# (the user lens at the root, then contributor/ and explore/) and its agent export
# under agents/ with llms.txt at the root.
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

rm -rf "$out"/*

# The generator writes .heron/docs under the directory it runs in.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
(cd "$work" && "$bin/heron" generate --entry "$spec" docs >/dev/null)
cp -r "$work/.heron/docs/." "$out/"

test -f "$out/index.html"
for d in contributor explore; do
  if grep -q "lenses" "$spec/index.heron"; then test -f "$out/$d/index.html"; fi
done
test -f "$out/agents/INDEX.md"
grep -q '](agents/INDEX.md)' "$out/llms.txt"
echo "$out: $(find "$out" -maxdepth 1 -name '*.html' | wc -l | tr -d ' ') pages in the default lens, $(find "$out" -mindepth 2 -name '*.html' | wc -l | tr -d ' ') in the others, $(find "$out/agents" -name '*.md' | wc -l | tr -d ' ') agent files"
