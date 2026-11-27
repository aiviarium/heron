#!/usr/bin/env bash
# The docs end to end: `heron generate docs` builds a working site for the
# sample repository and for the imported spec package, and every link and island works in
# Chromium; moving between pages in place, without a flash, is checked in
# Firefox and WebKit too where Playwright has them installed. With
# --screenshots, also writes web/screenshots/docs-*.png.
#
#   scripts/e2e-docs.sh [--screenshots] [SITE...]   those sites (all by default)
#   scripts/e2e-docs.sh --except SITE...            every site but those
#
# The sites: petstore parcels spec lenses shop sample. Their checks run at
# once, HERON_E2E_JOBS at a time (half as many as there are CPUs by default),
# each one's output shown whole.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
all=(petstore parcels spec lenses shop sample)
shots=0
except=0
sites=()
for a in "$@"; do
  case "$a" in
  --screenshots) shots=1 ;;
  --except) except=1 ;;
  *) case " ${all[*]} " in *" $a "*) sites+=("$a") ;; *) echo "e2e-docs: no site $a (${all[*]})" >&2; exit 2 ;; esac ;;
  esac
done
if [ $except = 1 ]; then
  keep=()
  for s in "${all[@]}"; do case " ${sites[*]-} " in *" $s "*) ;; *) keep+=("$s") ;; esac; done
  sites=("${keep[@]}")
elif [ ${#sites[@]} -eq 0 ] || [ $shots = 1 ]; then
  sites=("${all[@]}")
fi
want() { case " ${sites[*]} " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

tmp=$(mktemp -d)
# The sample site is generated in a copy beside it, whose node_modules links
# still resolve, so the checkout stays as it is while other checks read it.
site=$(mktemp -d "$root/web/samples/.e2e-docs-site.XXXXXX")
trap 'rm -rf "$tmp" "$site"' EXIT
go build -o "$tmp/heron" ./cmd/heron
checks=()

# The petstore, generated in a copy so the checkout stays clean.
if want petstore; then
  cp -r examples/petstore "$tmp/petstore"
  rm -rf "$tmp/petstore/.heron"
  (cd "$tmp/petstore" && "$tmp/heron" generate docs >/dev/null)
  test -f "$tmp/petstore/.heron/docs/store.place_order.html"
  test -f "$tmp/petstore/.heron/docs/versions.json"
  checks+=("e2e.cjs $tmp/petstore/.heron/docs")
fi

# The RPC example: a service over HTTP, gRPC, Connect and JSON-RPC.
if want parcels; then
  cp -r examples/parcels "$tmp/parcels"
  rm -rf "$tmp/parcels/.heron"
  (cd "$tmp/parcels" && "$tmp/heron" generate docs proto openrpc >/dev/null)
  test -f "$tmp/parcels/.heron/docs/parcels.navigate.html"
  test -f "$tmp/parcels/.heron/proto/acme/parcels/v1/parcels.proto"
  test -f "$tmp/parcels/.heron/openrpc/parcels.json"
  checks+=("e2e.cjs $tmp/parcels/.heron/docs")
  # The binding switch and Try it over Connect and JSON-RPC, against a mock
  # of the parcels service.
  checks+=("e2e-rpc.cjs $tmp/parcels/.heron/docs")
fi

# Heron's own spec, the package this repository imports, as the language's
# documentation site.
if want spec; then
  spec=$(go run ./internal/core/fixtures/specdir)
  mkdir -p "$tmp/spec"
  (cd "$tmp/spec" && "$tmp/heron" generate --entry "$spec" docs >/dev/null)
  test -f "$tmp/spec/.heron/docs/index.html"
  checks=("e2e.cjs $tmp/spec/.heron/docs" "${checks[@]+"${checks[@]}"}") # the biggest, first
fi

# A tree with every lens and the authored kinds: the user lens at the
# root, dev/ and explore/ beside it.
if want lenses; then
  cp -r internal/tools/docs/testdata/lenses "$tmp/lenses"
  rm -rf "$tmp/lenses/.heron"
  (cd "$tmp/lenses" && "$tmp/heron" generate docs >/dev/null)
  test -f "$tmp/lenses/.heron/docs/dev/kite.store_api.get_note.html"
  test -f "$tmp/lenses/.heron/docs/explore/data.js"
  checks+=("e2e.cjs $tmp/lenses/.heron/docs")
fi

# A project that implements a sealed spec: its conformance and status
# pages, with status badges.
if want shop; then
  cp -r examples/shop-spec examples/shop-spec-2.1.2 examples/shop "$tmp/"
  rm -rf "$tmp/shop/.heron"
  (cd "$tmp/shop" && "$tmp/heron" generate docs >/dev/null)
  grep -q 'class="badge status-missing"' "$tmp/shop/.heron/docs/_conformance.html"
  checks+=("e2e.cjs $tmp/shop/.heron/docs")
fi

# The sample site: a theme package from node_modules, the landing and
# changelog layouts, slots, and components and a markup tag rendered by TS
# through the JS host. It's generated where the workspace's node_modules
# are, in the copy (scripts/ci.sh installs and builds web/ first).
d=$site/.heron/docs
if want sample; then
  cp -a web/samples/docs-site/. "$site/"
  (cd "$site" && npx tsc -p tsconfig.json --noEmit)
  (cd "$site" && rm -rf .heron && "$tmp/heron" generate docs >/dev/null)
  test -f "$d/_assets/theme/theme.css"
  test -f "$d/_assets/theme/logo.svg"
  test -f "$d/_assets/theme/fonts/fraunces-latin-600-normal.woff2"
  grep -q '<ol class="steps" id="guide.setup.install" start="1">' "$d/guide.setup.html"
  grep -q '<span class="status-badge" data-level="2">ready</span>' "$d/guide.setup.html"
  grep -q 'data-layout="landing"' "$d/guide.html"
  grep -q '<article class="entry" id="changes.v1_1">' "$d/changes.html"
  checks+=("e2e.cjs $d")
fi

export NODE_PATH="${NODE_PATH:-/opt/node-tools/node_modules}"
cpus=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)
max=${HERON_E2E_JOBS:-$(( cpus > 3 ? cpus / 2 : 1 ))}
pids=()
for i in "${!checks[@]}"; do
  while [ "$(jobs -rp | wc -l)" -ge "$max" ]; do sleep 0.1; done
  read -r script dir <<<"${checks[$i]}"
  node "web/docs/scripts/$script" "$dir" >"$tmp/check$i.log" 2>&1 &
  pids[$i]=$!
done
fail=0
for i in "${!checks[@]}"; do
  wait "${pids[$i]}" || { fail=1; echo "FAIL: ${checks[$i]}"; }
  cat "$tmp/check$i.log"
done
[ $fail = 0 ]

if [ $shots = 1 ]; then
  mkdir -p web/screenshots
  node web/docs/scripts/screenshots.cjs "$tmp/petstore/.heron/docs" "$tmp/spec/.heron/docs" web/screenshots "$d"
  node web/docs/scripts/e2e-rpc.cjs "$tmp/parcels/.heron/docs" web/screenshots
fi
