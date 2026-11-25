#!/usr/bin/env bash
# Done means #1 (the language server, Go side) and #3 (prose) of unit 2:
# a scripted LSP client drives `heron lsp` over stdio on a fresh copy of the
# sample repository: diagnostics, completion, hover, definition and rename;
# Vale alerts on the right words with fixes and vocabulary actions; the
# spec-aware lints flagging an unlinked term.
set -euo pipefail
cd "$(dirname "$0")/.."

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/heron" ./cmd/heron
go build -o "$tmp/e2e" ./internal/tools/lsp/internal/e2e
mkdir "$tmp/petstore"
git ls-files -z examples/petstore | xargs -0 -I{} cp --parents {} "$tmp/petstore/"
"$tmp/e2e" "$tmp/heron" "$tmp/petstore/examples/petstore"
