#!/usr/bin/env bash
# The whole check, run before every push and by .github/workflows/ci.yml.
#
#   scripts/ci.sh             every lane at once, each one's output in a log,
#                             and a table of where the time went
#   scripts/ci.sh --serial    every lane, one after another, output as it comes
#   scripts/ci.sh LANE...     only those lanes, after what they need (CI runs
#                             each lane as its own job this way)
#   scripts/ci.sh --list      the lanes
#
# Every way runs the same checks: a lane is a part of the check, never a
# lighter version of it. The lanes share nothing they write, so they can run
# at once; what they read is built first, once (the "needs" below).
#
# The spec package comes first: scripts/spec-setup.sh runs `heron run setup`,
# which installs the version heron.lock pins from aiviarium/heron-specs, or,
# with HERON_SPECS_DIR set to a checkout such as ../heron-specs/heron, links
# that checkout in its place.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
export PATH="$PATH:$(go env GOPATH)/bin"

# The lanes, in the order --serial runs them. .github/workflows/ci.yml runs
# each as a job; the lint lane checks that its matrix names them all.
lanes=(lint web gotest golsp gorace treesitter tree e2e unit1 editor docs docslens docsspec)

# What each lane reads that another step builds; built once, in this order,
# before any lane starts.
#   web     the web workspace installed and @heron/runtime built
#   shell   the editor's frontend built into internal/tools/server/frontend
#   docsweb the docs theme and islands built into internal/tools/docs/assets
#   heron   bin: the heron binary (with the frontend when shell is built)
#   types   the inventory sample's types generated from Heron
#   buf     buf on PATH, for the Go tests of the generated .proto files
#   vale    Vale on PATH, and the tests that use it never skip
needs_lint=""
needs_web="web shell docsweb"
needs_gotest="vale web heron types buf"
needs_golsp="vale"
needs_gorace=""
needs_treesitter=""
needs_tree="web heron"
needs_e2e="vale web heron types"
needs_unit1="web"
needs_editor="web shell"
needs_docs="web"
needs_docslens="web"
needs_docsspec="web"

usage() { sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; }

mode=parallel
selected=()
for a in "$@"; do
  case "$a" in
  --list) printf '%s\n' "${lanes[@]}"; exit 0 ;;
  --serial) mode=serial ;;
  -h | --help) usage; exit 0 ;;
  -*) echo "ci.sh: unknown flag $a" >&2; usage >&2; exit 2 ;;
  *)
    known=0
    for l in "${lanes[@]}"; do [ "$l" = "$a" ] && known=1; done
    [ $known = 1 ] || { echo "ci.sh: no lane $a (scripts/ci.sh --list)" >&2; exit 2; }
    selected+=("$a")
    ;;
  esac
done
if [ ${#selected[@]} -gt 0 ]; then mode=serial; else selected=("${lanes[@]}"); fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/logs"
times=$tmp/times # lane <tab> seconds <tab> step
cpu=$tmp/cpu     # lane <tab> CPU seconds
: >"$cpu"

now() { if [ -n "${EPOCHREALTIME:-}" ]; then echo "${EPOCHREALTIME/,/.}"; else date +%s; fi; }
lane=prep
step_name=""
step_start=""
# step <name>: starts a step, and records how long the one before took.
step() {
  step_end
  step_name="$*"
  step_start=$(now)
  printf '\n== %s\n' "$*"
}
step_end() {
  if [ -n "$step_name" ]; then
    printf '%s\t%s\t%s\n' "$lane" "$(awk -v a="$step_start" -v b="$(now)" 'BEGIN { printf "%.1f", b - a }')" "$step_name" >>"$times"
  fi
  step_name=""
}

# The Go tests' results come from the build cache when nothing they read
# changed; on CI (GitHub sets CI), whose cache is restored from other
# commits, every test runs.
gocount=()
[ -n "${CI:-}" ] && gocount=(-count=1)

# --- what the lanes read, built once ---------------------------------------

bin=$tmp/bin
mkdir -p "$bin"
built=" "
need() {
  local n=$1
  case "$built" in *" $n "*) return ;; esac
  case "$n" in
  web)
    step "web: install the workspace and build @heron/runtime"
    (cd web && pnpm install --frozen-lockfile --silent && pnpm --filter @heron/runtime build)
    ;;
  shell)
    step "web: build the shell, the editor and the components"
    (cd web/shell && npx vite build --logLevel warn)
    ;;
  docsweb)
    step "web: build the docs theme and islands into internal/tools/docs/assets"
    (cd web && pnpm --filter @heron/docs build >/dev/null)
    ;;
  heron)
    step "build heron"
    go build -o "$bin/heron" ./cmd/heron
    ;;
  types)
    step "generate the inventory sample's types"
    (cd web/samples/inventory && "$bin/heron" generate --entry example ts >/dev/null)
    ;;
  vale)
    step "Vale, pinned, from the Go proxy (CI puts its release binary on PATH)"
    command -v vale >/dev/null || go install github.com/vale-cli/vale/v3/cmd/vale@v3.28.1
    export HERON_REQUIRE_VALE=1
    ;;
  buf)
    step "buf, pinned, from npm: the generated .proto files are built and linted by the Go tests"
    if ! command -v buf >/dev/null; then
      (cd "$tmp" && npm install --silent --no-audit --no-fund @bufbuild/buf@1.47.2 >/dev/null)
      export PATH="$PATH:$tmp/node_modules/.bin"
    fi
    ;;
  esac
  built="$built$n "
}

# --- the lanes -------------------------------------------------------------

lane_lint() {
  step gofmt
  bad=$(gofmt -l $(git ls-files '*.go'; git ls-files --others --exclude-standard '*.go') 2>/dev/null || true)
  if [ -n "$bad" ]; then echo "not gofmt'ed:"; echo "$bad"; return 1; fi

  step "generated code is current"
  local g=$tmp/gen
  mkdir -p "$g"
  go run ./internal/core/ast/internal/astgen -grammar grammar/heron.ungram \
    -syntax "$g/kinds_gen.go" -ast "$g/nodes_gen.go"
  diff -q "$g/kinds_gen.go" internal/core/syntax/kinds_gen.go
  diff -q "$g/nodes_gen.go" internal/core/ast/nodes_gen.go
  # The extension protocol, from the spec package's protocol_messages.heron.
  go run ./internal/core/extension/protocol/internal/protogen \
    -go "$g/protocol_gen.go" -ts "$g/protocol.gen.ts"
  diff -q "$g/protocol_gen.go" internal/core/extension/protocol/protocol_gen.go
  diff -q "$g/protocol.gen.ts" web/runtime/src/protocol.gen.ts
  go run ./internal/tools/server/apigen \
    -go "$g/api_gen.go" -pkg api -ts "$g/api.gen.ts"
  diff -q "$g/api_gen.go" internal/tools/server/api/api_gen.go
  diff -q "$g/api.gen.ts" web/shell/src/api.gen.ts
  go run ./internal/tools/server/apigen \
    -ext internal/tools/server/internal/sample/sample.heron \
    -go "$g/sample_gen.go" -pkg sample -ts "$g/sample.gen.ts"
  diff -q "$g/sample_gen.go" internal/tools/server/internal/sample/sample_gen.go
  diff -q "$g/sample.gen.ts" internal/tools/server/internal/sample/sample.gen.ts
  go test -count=1 -run "TestGeneratedFilesAreCurrent|TestContrast" ./web/tokens/

  step "go vet"
  go vet ./...

  step go-check-sumtype
  go-check-sumtype ./...

  step "syntax and eval do no I/O"
  for pkg in ./internal/core/syntax ./internal/core/eval; do
    [ -d "$pkg" ] || continue
    imports=$(go list -f '{{join .Imports " "}}' "$pkg")
    for forbidden in os os/exec net net/http io/fs io/ioutil syscall path/filepath; do
      for imp in $imports; do
        if [ "$imp" = "$forbidden" ]; then echo "$pkg imports $imp"; return 1; fi
      done
    done
  done

  step "unit 1 never imports unit 2"
  # Unit 1's Go packages never import a later unit's, so their tests in the
  # gotest lane are already unit 1's alone.
  if go list -test -deps ./internal/core/... ./std/... | grep -q '/internal/tools'; then echo "unit 1 imports unit 2"; return 1; fi

  step "every lane runs on GitHub: the ci workflow's matrix names them all"
  local m
  m=$(sed -n 's/^ *lane: \[\(.*\)\]$/\1/p' .github/workflows/ci.yml | tr -d ' ' | tr ',' '\n' | sort)
  if [ "$m" != "$(printf '%s\n' "${lanes[@]}" | sort)" ]; then
    echo "the matrix in .github/workflows/ci.yml runs: $(echo $m)"
    echo "scripts/ci.sh has the lanes: ${lanes[*]}"
    return 1
  fi
}

lane_web() {
  step "web: test @heron/runtime"
  (cd web && pnpm --filter @heron/runtime test)

  step "web: typecheck and test the shell, the editor and the components"
  (cd web/shell && npx tsc -p . && npx vitest run --silent)

  step "web: the docs theme and islands type-check, and the embedded build is current"
  (cd web && pnpm --filter @heron/docs check)
  git diff --exit-code --stat -- internal/tools/docs/assets || { echo "internal/tools/docs/assets is stale: commit the build of web/docs"; return 1; }
}

# go test ./..., in two lanes: the language server's package, whose test
# opens the whole spec package in every order, and every other package.
lsp_pkg=github.com/aiviarium/heron/internal/tools/lsp
# The tests check the spec package over and over; collecting garbage less
# often (GOGC=400) takes a third off their CPU time, for memory they have.
gotest() { GOGC=${GOGC:-400} go test ${gocount[@]+"${gocount[@]}"} "$@"; }
lane_gotest() {
  step "go test, every package but the language server's (golsp)"
  local pkgs
  pkgs=$(go list ./... | grep -vx "$lsp_pkg")
  gotest $pkgs
}

lane_golsp() {
  step "go test the language server"
  gotest "$lsp_pkg"
}

lane_gorace() {
  step "race: checks sharing a package run at once"
  go test -race -count=1 -run 'TestConcurrentChecksSharePackage' ./internal/core/check/

  step "fuzz the parser briefly"
  go test -run XXX -fuzz FuzzParse -fuzztime 10s ./internal/core/syntax/ >/dev/null

  step "fixtures: the spec package's examples parse, format to themselves and check clean, but the pending ones"
  go test -count=1 -run 'TestFixturesAreTheSpecExamples' ./internal/core/fixtures/
  go test -count=1 -run 'TestFixturesCheckClean' -v ./internal/core/check/ | grep 'check clean'
}

lane_treesitter() {
  step "tree-sitter grammar: generated parser current, corpus, the same fixtures, queries"
  local g=$tmp/tree-sitter
  mkdir -p "$g"
  (cd grammar/tree-sitter-heron && npm ci --silent --no-audit --no-fund \
    && cp src/parser.c "$g/parser.c" && npx --no-install tree-sitter generate \
    && diff -q "$g/parser.c" src/parser.c \
    && npx --no-install tree-sitter test 2>/dev/null | tail -1 \
    && ./test-fixtures.sh)
}

lane_tree() {
  local heron=$bin/heron
  step "the trees and examples are formatted"
  "$heron" fmt --check index.heron project fixtures std examples internal/tools/server/api internal/tools/server/internal/sample web/samples/inventory/package.heron web/samples/inventory/example web/samples/docs-site

  step "the repository's tree (the spec package and the project tree) and examples/ check and lint clean"
  "$heron" check . examples/petstore examples/parcels web/samples/docs-site
  "$heron" lint . examples/petstore examples/parcels web/samples/docs-site

  step "the spec package checks on its own, as it's released"
  "$heron" check "$spec"

  step "conformance: every requirement of the spec is proven, planned or untested, and the project's docs build"
  if grep -rqs --include='*.heron' -E '^[a-z_]+: implements\(' project; then
    "$heron" generate conformance >/dev/null
    head -3 .heron/conformance/conformance.md | tail -1
    missing=$(node -e 'console.log(require(process.argv[1]).counts.missing)' "$PWD/.heron/conformance/conformance.json")
    if [ "$missing" != 0 ]; then
      grep '| missing |' .heron/conformance/conformance.md
      echo "conformance: $missing requirements have no proof and no status"; return 1
    fi
    rm -rf .heron/docs
    "$heron" generate docs >/dev/null
    test -f .heron/docs/_conformance.html
  else
    echo "the project tree declares no implements yet: nothing to report"
  fi
}

lane_e2e() {
  step "prose view: a sample provider through the CLI"
  scripts/e2e-prose.sh

  step "end to end: heron run setup"
  scripts/e2e-setup.sh

  step "end to end: heron run"
  scripts/e2e-tasks.sh

  step "interop end to end: the sample TS package's parts"
  scripts/e2e-interop.sh

  step "end to end: a sealed spec and the project that implements it: sealed and project-mode errors, conformance, versions"
  scripts/e2e-project.sh

  step "language server end to end: heron lsp over stdio, Vale and the spec-aware lints"
  scripts/e2e-lsp.sh
}

lane_unit1() {
  step "unit 1 alone: a binary built with -tags unit1 checks the tree and runs unit 1's end-to-end scripts"
  local heron1=$tmp/heron1
  go build -tags unit1 -o "$heron1" ./cmd/heron
  if "$heron1" lsp </dev/null >/dev/null 2>&1; then echo "the unit-1 binary has unit 2's lsp"; return 1; fi
  "$heron1" fmt --check index.heron project fixtures std examples
  "$heron1" check . examples/petstore examples/parcels
  "$heron1" lint . examples/petstore examples/parcels
  export HERON_TAGS=unit1
  scripts/e2e-setup.sh >/dev/null
  scripts/e2e-tasks.sh >/dev/null
  scripts/e2e-interop.sh >/dev/null
  scripts/e2e-project.sh >/dev/null
}

lane_editor() {
  step "the editor's acceptance tests: Playwright against heron serve on the sample"
  scripts/e2e-app.sh
}

# The docs end to end, in three lanes: the spec package's site, the
# biggest; the lenses and the sample site; and every other site.
lane_docs() {
  step "docs end to end: every site but the spec package's (docsspec) and the lenses and sample (docslens), every link and island"
  scripts/e2e-docs.sh --except spec lenses sample
}

lane_docslens() {
  step "docs end to end: the lenses and the sample site, every link and island"
  scripts/e2e-docs.sh lenses sample
}

lane_docsspec() {
  step "docs end to end: the spec package's site, every link and island"
  scripts/e2e-docs.sh spec
}

# --- running them ----------------------------------------------------------

# run_lane <lane>: the lane's steps, in a subshell, its time recorded.
run_lane() (
  lane=$1
  trap 'step_end; lane_cpu' EXIT # the time of the step that failed, too
  set -euo pipefail
  "lane_$1"
)

# lane_cpu: the CPU seconds the lane's commands used, from `times`.
lane_cpu() {
  times >"$tmp/times.$lane" # a builtin: in a pipe it would time the pipe's shell
  sed -n 2p "$tmp/times.$lane" | awk -v l="$lane" '{
    s = 0
    for (i = 1; i <= 2; i++) { split($i, p, "m"); s += p[1] * 60 + p[2] }
    printf "%s\t%.1f\n", l, s }' >>"$cpu"
}

summary() {
  [ -s "$times" ] || return 0

  printf '\n== where the time went (seconds of wall clock, and of CPU; the lanes ran %s)\n' "$1"
  awk -F'\t' 'FNR == NR { c[$1] += $2; next } { t[$1] += $2 } END { for (l in t) printf "%7.1f %7.1f  %s\n", t[l], c[l], l }' "$cpu" "$times" | sort -rn
  echo "-- the slowest steps"
  sort -t"$(printf '\t')" -k2,2 -rn "$times" | head -12 | awk -F'\t' '{ printf "%7.1f  %-10s %s\n", $2, $1, $3 }'
}

start=$(now)
step "the spec package: heron run setup, or the HERON_SPECS_DIR override"
scripts/spec-setup.sh
spec=$(go run ./internal/core/fixtures/specdir)
echo "spec: $spec"
# The needs of the lanes that run, in order.
order="vale web shell docsweb heron types buf"
for n in $order; do
  for l in "${selected[@]}"; do
    v=needs_$l
    case " ${!v} " in *" $n "*) need "$n"; break ;; esac
  done
done
# CI finishes here what it started in the background, such as installing
# browsers, while the needs were built.
if [ -n "${HERON_CI_BEFORE_LANES:-}" ]; then
  step "what CI started in the background is done (HERON_CI_BEFORE_LANES)"
  bash -c "$HERON_CI_BEFORE_LANES"
fi
step_end
lane_cpu

# A lane runs in the background even one at a time: errexit holds in a
# subshell of its own, never in one an `if` or `!` tests.
fail=()
if [ "$mode" = serial ]; then
  for l in "${selected[@]}"; do
    run_lane "$l" &
    if ! wait $!; then fail+=("$l"); break; fi
  done
  summary "one after another"
else
  echo
  # The longest lanes start first; at most HERON_CI_JOBS run at once (as
  # many as there are CPUs by default), as the e2e lanes time their pages.
  max=${HERON_CI_JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)}
  queue=()
  for l in docsspec gotest golsp e2e unit1 editor docs docslens web; do
    case " ${selected[*]} " in *" $l "*) queue+=("$l") ;; esac
  done
  for l in "${selected[@]}"; do
    case " ${queue[*]} " in *" $l "*) ;; *) queue+=("$l") ;; esac
  done
  echo "== ${#selected[@]} lanes, $max at once; each one's output is in its log, shown if it fails"
  running="" # lane:pid ...
  while [ ${#queue[@]} -gt 0 ] || [ -n "$running" ]; do
    while [ ${#queue[@]} -gt 0 ] && [ "$(echo $running | wc -w)" -lt "$max" ]; do
      l=${queue[0]}
      queue=("${queue[@]:1}")
      run_lane "$l" >"$tmp/logs/$l.log" 2>&1 &
      running="$running $l:$!"
    done
    still=""
    for lp in $running; do
      l=${lp%%:*} p=${lp##*:}
      if kill -0 "$p" 2>/dev/null; then still="$still $lp"; continue; fi
      if wait "$p"; then ok=ok; else ok=FAIL; fail+=("$l"); fi
      t=$(awk -F'\t' -v l="$l" '$1 == l { s += $2 } END { printf "%.0f", s }' "$times")
      printf '%-5s %-11s %4ss\n' "$ok" "$l" "$t"
      if [ $ok = FAIL ]; then
        echo "---- $l's log"
        cat "$tmp/logs/$l.log"
        echo "---- end of $l's log"
      fi
    done
    running=$still
    [ -z "$running" ] || sleep 0.2
  done
  summary "at once"
fi
printf '%7.1f  wall clock\n' "$(awk -v a="$start" -v b="$(now)" 'BEGIN { print b - a }')"

if [ ${#fail[@]} -gt 0 ]; then
  echo
  echo "FAIL: ${fail[*]}"
  exit 1
fi
echo
echo ok
