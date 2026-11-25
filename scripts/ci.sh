#!/usr/bin/env bash
# The whole check, run before every push and by .github/workflows/ci.yml.
#
# The spec package comes first: scripts/spec-setup.sh runs `heron run setup`,
# which installs the version heron.lock pins from aiviarium/heron-specs, or,
# with HERON_SPECS_DIR set to a checkout such as ../heron-specs/heron, links
# that checkout in its place.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$PATH:$(go env GOPATH)/bin"

step() { printf '\n== %s\n' "$*"; }

step "the spec package: heron run setup, or the HERON_SPECS_DIR override"
scripts/spec-setup.sh
spec=$(go run ./internal/core/fixtures/specdir)
echo "spec: $spec"

step gofmt
bad=$(gofmt -l $(git ls-files '*.go'; git ls-files --others --exclude-standard '*.go') 2>/dev/null || true)
if [ -n "$bad" ]; then echo "not gofmt'ed:"; echo "$bad"; exit 1; fi

step "generated code is current"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
go run ./internal/core/ast/internal/astgen -grammar grammar/heron.ungram \
  -syntax "$tmp/kinds_gen.go" -ast "$tmp/nodes_gen.go"
diff -q "$tmp/kinds_gen.go" internal/core/syntax/kinds_gen.go
diff -q "$tmp/nodes_gen.go" internal/core/ast/nodes_gen.go
# The extension protocol, from the spec package's protocol_messages.heron.
go run ./internal/core/extension/protocol/internal/protogen \
  -go "$tmp/protocol_gen.go" -ts "$tmp/protocol.gen.ts"
diff -q "$tmp/protocol_gen.go" internal/core/extension/protocol/protocol_gen.go
diff -q "$tmp/protocol.gen.ts" web/runtime/src/protocol.gen.ts
go run ./internal/tools/server/apigen -base internal/tools/server/api/server.heron \
  -go "$tmp/api_gen.go" -pkg api -ts "$tmp/api.gen.ts"
diff -q "$tmp/api_gen.go" internal/tools/server/api/api_gen.go
diff -q "$tmp/api.gen.ts" web/shell/src/api.gen.ts
go run ./internal/tools/server/apigen -base internal/tools/server/api/server.heron \
  -ext internal/tools/server/internal/sample/sample.heron \
  -go "$tmp/sample_gen.go" -pkg sample -ts "$tmp/sample.gen.ts"
diff -q "$tmp/sample_gen.go" internal/tools/server/internal/sample/sample_gen.go
diff -q "$tmp/sample.gen.ts" internal/tools/server/internal/sample/sample.gen.ts
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
      if [ "$imp" = "$forbidden" ]; then echo "$pkg imports $imp"; exit 1; fi
    done
  done
done

step "web: install, build and test @heron/runtime; generate the sample's types"
(cd web && pnpm install --frozen-lockfile --silent && pnpm --filter @heron/runtime build && pnpm --filter @heron/runtime test)

step "web: typecheck, test and build the shell, the editor and the components"
(cd web/shell && npx tsc -p . && npx vitest run --silent && npx vite build --logLevel warn)

step "web: the docs theme and islands type-check, and the embedded build is current"
(cd web && pnpm --filter @heron/docs check && pnpm --filter @heron/docs build >/dev/null)
git diff --exit-code --stat -- internal/tools/docs/assets || { echo "internal/tools/docs/assets is stale: commit the build of web/docs"; exit 1; }
go build -o "$tmp/heron" ./cmd/heron
(cd web/samples/inventory && "$tmp/heron" generate --entry example ts >/dev/null)

step "Vale, pinned, from the Go proxy (GitHub release downloads are blocked here)"
command -v vale >/dev/null || go install github.com/vale-cli/vale/v3/cmd/vale@v3.28.1
export HERON_REQUIRE_VALE=1

step "go test"
go test ./...

step "fuzz the parser briefly"
go test -run XXX -fuzz FuzzParse -fuzztime 10s ./internal/core/syntax/ >/dev/null

step "fixtures: the spec package's examples parse, format to themselves and check clean, but the pending ones"
go test -count=1 -run 'TestFixturesAreTheSpecExamples' ./internal/core/fixtures/
go test -count=1 -run 'TestFixturesCheckClean' -v ./internal/core/check/ | grep 'check clean'
go run ./cmd/heron fmt --check index.heron project fixtures std examples internal/tools/server/api internal/tools/server/internal/sample web/samples/inventory/package.heron web/samples/inventory/example web/samples/docs-site

step "tree-sitter grammar: generated parser current, corpus, the same fixtures, queries"
(cd grammar/tree-sitter-heron && npm ci --silent --no-audit --no-fund \
  && cp src/parser.c "$tmp/parser.c" && npx --no-install tree-sitter generate \
  && diff -q "$tmp/parser.c" src/parser.c \
  && npx --no-install tree-sitter test 2>/dev/null | tail -1 \
  && ./test-fixtures.sh)

step "the repository's tree (the spec package and the project tree) and examples/ check and lint clean"
go build -o "$tmp/heron" ./cmd/heron
"$tmp/heron" check . examples/petstore web/samples/docs-site internal/tools/server/api/server.heron
"$tmp/heron" lint . examples/petstore web/samples/docs-site internal/tools/server/api/server.heron

step "conformance: every requirement of the spec is proven, planned or untested, and the project's docs build"
if grep -rqs --include='*.heron' -E '^[a-z_]+: implements\(' project; then
  "$tmp/heron" generate conformance >/dev/null
  head -3 .heron/conformance/conformance.md | tail -1
  missing=$(node -e 'console.log(require(process.argv[1]).counts.missing)' "$PWD/.heron/conformance/conformance.json")
  if [ "$missing" != 0 ]; then
    grep '| missing |' .heron/conformance/conformance.md
    echo "conformance: $missing requirements have no proof and no status"; exit 1
  fi
  rm -rf .heron/docs
  "$tmp/heron" generate docs >/dev/null
  test -f .heron/docs/_conformance.html
else
  echo "the project tree declares no implements yet: nothing to report"
fi

step "prose view: a sample provider through the CLI"
scripts/e2e-prose.sh

step "end to end: heron run setup"
./scripts/e2e-setup.sh

step "end to end: heron run"
./scripts/e2e-tasks.sh

step "the spec package checks on its own, as it's released"
"$tmp/heron" check "$spec"

step "interop end to end: the sample TS package's parts"
scripts/e2e-interop.sh

step "end to end: a sealed spec and the project that implements it: sealed and project-mode errors, conformance, versions"
scripts/e2e-project.sh

step "unit 1 alone: a binary built with -tags unit1 checks the tree and runs unit 1's end-to-end scripts"
# Unit 1's Go packages never import a later unit's, so their tests above are
# already unit 1's alone.
if go list -test -deps ./internal/core/... ./std/... | grep -q '/internal/tools'; then echo "unit 1 imports unit 2"; exit 1; fi
go build -tags unit1 -o "$tmp/heron1" ./cmd/heron
if "$tmp/heron1" lsp </dev/null >/dev/null 2>&1; then echo "the unit-1 binary has unit 2's lsp"; exit 1; fi
"$tmp/heron1" fmt --check index.heron project fixtures std examples
"$tmp/heron1" check . examples/petstore
"$tmp/heron1" lint . examples/petstore
HERON_TAGS=unit1 scripts/e2e-setup.sh >/dev/null
HERON_TAGS=unit1 scripts/e2e-tasks.sh >/dev/null
HERON_TAGS=unit1 scripts/e2e-interop.sh >/dev/null
HERON_TAGS=unit1 scripts/e2e-project.sh >/dev/null

step "the editor's acceptance tests: Playwright against heron serve on the sample"
scripts/e2e-app.sh

step "language server end to end: heron lsp over stdio, Vale and the spec-aware lints"
scripts/e2e-lsp.sh
step "docs end to end: petstore and spec package sites, every link and island"
scripts/e2e-docs.sh

step ok
