#!/usr/bin/env bash
# The editor's acceptance tests: Playwright drives Chromium against the real
# `heron serve` on fresh copies of examples/petstore. The frontend must be
# built (ci.sh does it first). Pass test names to run only those.
#   scripts/e2e-app.sh [first-run switching typing …]
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/heron" ./cmd/heron
export HERON="$tmp/heron"
tests=("$@")
if [ ${#tests[@]} -eq 0 ]; then
  tests=(first-run switching typing indent lsp preview visible-text prose-tokens)
fi
fail=0
for t in "${tests[@]}"; do
  node "web/shell/e2e/$t.cjs" || fail=1
done
exit $fail
