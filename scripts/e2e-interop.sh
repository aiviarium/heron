#!/usr/bin/env bash
# End to end, unit 1's done criterion 4: the sample package in
# web/samples/inventory provides a foreign func, a transport, a lint, a
# resource driver and a generation adapter in TS. Each is type-checked
# against its Heron declaration and used like a built-in part: the
# transport carries an api's ops in heron run, bound by an annotation of
# its package, and TS calls back into Heron through ctx.call.
# Expects web/ to be installed and built (scripts/ci.sh does that first).
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
tmp=$(mktemp -d)
sample=$root/web/samples/inventory
restore() {
  [ -f "$tmp/package.heron" ] && cp "$tmp/package.heron" "$sample/package.heron"
  [ -f "$tmp/index.heron" ] && cp "$tmp/index.heron" "$sample/example/index.heron"
  rm -f "$sample/catalog.md"
  rm -rf "$tmp"
}
trap restore EXIT
go build -tags "${HERON_TAGS:-}" -o "$tmp/heron" ./cmd/heron
heron() { "$tmp/heron" "$@"; }

fail() { echo "e2e-interop: $*" >&2; exit 1; }
expect() { # expect <what> <want> <got>
  [ "$2" = "$3" ] || fail "$1: want $(printf %q "$2"), got $(printf %q "$3")"
}

cd "$sample"

echo "-- types from Heron, and tsc checks the implementation against them"
heron generate --entry example ts >/dev/null
npx tsc -p tsconfig.json --noEmit

echo "-- a signature that drifts from its implementation is a compile error"
cp package.heron "$tmp/package.heron"
sed -i 's/count(sku: String): func(Int)/count(sku: String): func(String)/' package.heron
heron generate --entry example ts >/dev/null
if npx tsc -p tsconfig.json --noEmit >"$tmp/tsc.out" 2>&1; then
  fail "tsc accepted count returning a number where Heron says String"
fi
grep -q "src/index.ts" "$tmp/tsc.out" || fail "tsc failed elsewhere: $(cat "$tmp/tsc.out")"
cp "$tmp/package.heron" package.heron
heron generate --entry example ts >/dev/null

echo "-- the foreign func and the resource driver, called from tasks by heron run"
expect "heron run slug" "hello-big-world" "$(heron run --entry example slug --name 'Hello Big World' 2>&1)"
expect "heron run stock" "kib-1: 12" "$(heron run --entry example stock --sku kib-1 2>&1)"
expect "heron run stock (missing)" "nope: 0" "$(heron run --entry example stock --sku nope 2>&1)"

echo "-- the lint, shown by heron lint"
set +e
out=$(heron lint example 2>&1)
code=$?
set -e
expect "heron lint exit" 1 "$code"
expect "heron lint" "example/index.heron:10:1: warning: Bin still says TODO [inventory.no_todo]" "$out"
heron lint --json example >"$tmp/lint.json" || true
grep -q '"code": "inventory.no_todo"' "$tmp/lint.json" || fail "heron lint --json lacks the finding"

echo "-- the lint's fix, applied by heron lint --fix"
cp example/index.heron "$tmp/index.heron"
heron lint --fix example >/dev/null 2>&1 || true
grep -q '^/// say what a bin holds\.$' example/index.heron || fail "the fix wasn't applied: $(grep -n bin example/index.heron)"
heron lint example >/dev/null 2>&1 || fail "findings remain after --fix"
cp "$tmp/index.heron" example/index.heron

echo "-- the generation adapter written in TS, through heron generate"
heron generate --entry example catalog >/dev/null
expect "catalog.md" "$(printf '# Types\n\n- Shelf\n- Bin')" "$(cat catalog.md)"

echo "-- heron generate runs every target and the managed ignore block"
heron generate --entry example >/dev/null
grep -q '^/catalog.md$' .gitignore || fail ".gitignore lacks the generated catalog"
before=$(cat .gitignore)
heron generate --entry example >/dev/null
expect "a second heron generate" "$before" "$(cat .gitignore)"

echo "-- the transport carries the shelf api's ops in heron run, like a built-in"
expect "heron run find" "inventory.shelf.find: true" "$(heron run --entry example find --sku kib-1 2>&1)"
expect "heron run find (missing)" "inventory.shelf.find: false" "$(heron run --entry example find --sku nope 2>&1)"

echo "-- ctx.call reaches an op and a Heron func from TS"
expect "heron run describe" "Kibble (12)" "$(heron run --entry example describe --sku kib-1 2>&1)"
expect "heron run describe (missing)" "nope: not stocked" "$(heron run --entry example describe --sku nope 2>&1)"

echo "-- the run record holds the foreign calls and the resource's lifecycle"
record=$(ls -t example/.heron/audit/run/*.json | sed -n 1p)
node -e '
  const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).data;
  const fail = (m) => { console.error("e2e-interop: " + m); process.exit(1); };
  const f = d.foreign.map((c) => c.func).join(",");
  if (f !== "inventory.memory.send,inventory.describe") fail("foreign calls: " + f);
  const send = d.foreign[0];
  if (send.args.op !== "inventory.shelf.find" || send.args.request.sku !== "nope" || send.result.found !== false) fail("send: " + JSON.stringify(send));
  if (typeof send.duration_ms !== "number") fail("no duration");
  const r = d.resources.map((e) => e.phase + " " + e.resource).join(",");
  if (r !== "setup inventory.store,teardown inventory.store") fail("resources: " + r);
' "$record"

echo "-- a pure func computes a value while the tree is evaluated"
expect "heron run home" "pet-store-home" "$(heron run --entry example home 2>&1)"

echo "-- the host's crash handling, handshake and limits"
cd "$root"
go test -count=1 -run 'TestSample|TestRunScopes|TestCrash|TestHandshake|TestExtensionRunsWithItsLimits' ./internal/core/extension/ >/dev/null

echo "e2e-interop: ok"
