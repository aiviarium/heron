#!/usr/bin/env bash
# The editor's acceptance tests: Playwright drives Chromium against the real
# `heron serve` on fresh copies of examples/petstore. The frontend must be
# built (ci.sh does it first). Pass test names to run only those. Each test
# has its own server, sample and browser, so they run at once
# (HERON_E2E_JOBS of them, 4 by default), each one's output shown whole.
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
max=${HERON_E2E_JOBS:-4}
pids=()
for i in "${!tests[@]}"; do
  while [ "$(jobs -rp | wc -l)" -ge "$max" ]; do sleep 0.1; done
  node "web/shell/e2e/${tests[$i]}.cjs" >"$tmp/$i.log" 2>&1 &
  pids[$i]=$!
done
fail=0
for i in "${!tests[@]}"; do
  wait "${pids[$i]}" || fail=1
  cat "$tmp/$i.log"
done
exit $fail
