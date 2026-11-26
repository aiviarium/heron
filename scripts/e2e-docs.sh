#!/usr/bin/env bash
# The docs end to end: `heron generate docs` builds a working site for the
# sample repository and for the imported spec package, and every link and island works in
# Chromium. With --screenshots, also writes web/screenshots/docs-*.png.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
tmp=$(mktemp -d)
site=web/samples/docs-site
trap 'rm -rf "$tmp" "$site/.heron"' EXIT
go build -o "$tmp/heron" ./cmd/heron

# The petstore, generated in a copy so the checkout stays clean.
cp -r examples/petstore "$tmp/petstore"
rm -rf "$tmp/petstore/.heron"
(cd "$tmp/petstore" && "$tmp/heron" generate docs >/dev/null)
test -f "$tmp/petstore/.heron/docs/store.place_order.html"
test -f "$tmp/petstore/.heron/docs/versions.json"

# The RPC example: a service over HTTP, gRPC, Connect and JSON-RPC.
cp -r examples/parcels "$tmp/parcels"
rm -rf "$tmp/parcels/.heron"
(cd "$tmp/parcels" && "$tmp/heron" generate docs proto openrpc >/dev/null)
test -f "$tmp/parcels/.heron/docs/parcels.navigate.html"
test -f "$tmp/parcels/.heron/proto/acme/parcels/v1/parcels.proto"
test -f "$tmp/parcels/.heron/openrpc/parcels.json"

# Heron's own spec, the package this repository imports, as the language's
# documentation site.
spec=$(go run ./internal/core/fixtures/specdir)
mkdir -p "$tmp/spec"
(cd "$tmp/spec" && "$tmp/heron" generate --entry "$spec" docs >/dev/null)
test -f "$tmp/spec/.heron/docs/index.html"

# A tree with every lens and the authored kinds: the user lens at the
# root, dev/ and explore/ beside it.
cp -r internal/tools/docs/testdata/lenses "$tmp/lenses"
rm -rf "$tmp/lenses/.heron"
(cd "$tmp/lenses" && "$tmp/heron" generate docs >/dev/null)
test -f "$tmp/lenses/.heron/docs/dev/kite.store_api.get_note.html"
test -f "$tmp/lenses/.heron/docs/explore/data.js"

# A project that implements a sealed spec: its conformance and status
# pages, with status badges.
cp -r examples/shop-spec examples/shop-spec-2.1.2 examples/shop "$tmp/"
rm -rf "$tmp/shop/.heron"
(cd "$tmp/shop" && "$tmp/heron" generate docs >/dev/null)
grep -q 'class="badge status-missing"' "$tmp/shop/.heron/docs/_conformance.html"

# The sample site: a theme package from node_modules, the landing and
# changelog layouts, slots, and components and a markup tag rendered by TS
# through the JS host. It's generated in place, where the workspace's
# node_modules are (scripts/ci.sh installs and builds web/ first).
(cd "$site" && npx tsc -p tsconfig.json --noEmit)
(cd "$site" && rm -rf .heron && "$tmp/heron" generate docs >/dev/null)
d=$site/.heron/docs
test -f "$d/_assets/theme/theme.css"
test -f "$d/_assets/theme/logo.svg"
test -f "$d/_assets/theme/fonts/fraunces-latin-600-normal.woff2"
grep -q '<ol class="steps" id="guide.setup.install" start="1">' "$d/guide.setup.html"
grep -q '<span class="status-badge" data-level="2">ready</span>' "$d/guide.setup.html"
grep -q 'data-layout="landing"' "$d/guide.html"
grep -q '<article class="entry" id="changes.v1_1">' "$d/changes.html"

export NODE_PATH="${NODE_PATH:-/opt/node-tools/node_modules}"
node web/docs/scripts/e2e.cjs "$tmp/petstore/.heron/docs"
node web/docs/scripts/e2e.cjs "$tmp/parcels/.heron/docs"
# The binding switch and Try it over Connect and JSON-RPC, against a mock
# of the parcels service.
node web/docs/scripts/e2e-rpc.cjs "$tmp/parcels/.heron/docs"
node web/docs/scripts/e2e.cjs "$tmp/spec/.heron/docs"
node web/docs/scripts/e2e.cjs "$tmp/shop/.heron/docs"
node web/docs/scripts/e2e.cjs "$tmp/lenses/.heron/docs"
node web/docs/scripts/e2e.cjs "$d"

if [ "${1:-}" = "--screenshots" ]; then
  mkdir -p web/screenshots
  node web/docs/scripts/screenshots.cjs "$tmp/petstore/.heron/docs" "$tmp/spec/.heron/docs" web/screenshots "$d"
  node web/docs/scripts/e2e-rpc.cjs "$tmp/parcels/.heron/docs" web/screenshots
fi
