#!/usr/bin/env bash
# Local CI: the whole check, run before every push. No hosted CI is used.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$PATH:$(go env GOPATH)/bin"

step() { printf '\n== %s\n' "$*"; }

step gofmt
bad=$(gofmt -l $(git ls-files '*.go'; git ls-files --others --exclude-standard '*.go') 2>/dev/null || true)
if [ -n "$bad" ]; then echo "not gofmt'ed:"; echo "$bad"; exit 1; fi

step "generated code is current"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
go run ./internal/core/ast/internal/astgen -grammar grammar/heron.ungram \
  -syntax "$tmp/kinds_gen.go" -ast "$tmp/nodes_gen.go"
diff -q "$tmp/kinds_gen.go" internal/core/syntax/kinds_gen.go
diff -q "$tmp/nodes_gen.go" internal/core/ast/nodes_gen.go
go run ./internal/core/extension/protocol/internal/protogen \
  -in internal/core/extension/protocol/protocol.heron \
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

step "fixtures"
go test -count=1 -run 'TestNoDrift' ./internal/core/fixtures/
go test -count=1 -run 'TestFixturesCheckClean' -v ./internal/core/check/ | grep 'check clean'
go run ./cmd/heron fmt --check fixtures spec std examples internal/core/extension/protocol internal/tools/server/api internal/tools/server/internal/sample web/samples/inventory/package.heron web/samples/inventory/example web/samples/docs-site

step "tree-sitter grammar: generated parser current, corpus, the same fixtures, queries"
(cd grammar/tree-sitter-heron && npm ci --silent --no-audit --no-fund \
  && cp src/parser.c "$tmp/parser.c" && npx --no-install tree-sitter generate \
  && diff -q "$tmp/parser.c" src/parser.c \
  && npx --no-install tree-sitter test 2>/dev/null | tail -1 \
  && ./test-fixtures.sh)

step "spec/ and examples/ check and lint clean; spec examples are the fixtures"
go build -o "$tmp/heron" ./cmd/heron
"$tmp/heron" check spec examples/petstore web/samples/docs-site internal/tools/server/api/server.heron
"$tmp/heron" lint spec examples/petstore web/samples/docs-site internal/tools/server/api/server.heron
go test -count=1 -run 'TestSpecExamples' ./internal/core/fixtures/
go test -count=1 -run 'TestSpecExamplesCheck' -v ./internal/core/check/ | grep 'spec examples:'

step "prose view: a sample provider through the CLI"
scripts/e2e-prose.sh

step "end to end: heron run setup"
./scripts/e2e-setup.sh

step "end to end: heron run"
./scripts/e2e-tasks.sh

step "the protocol description checks and lints clean"
"$tmp/heron" check internal/core/extension/protocol/protocol.heron
"$tmp/heron" lint internal/core/extension/protocol/protocol.heron

step "interop end to end: the sample TS package's parts"
scripts/e2e-interop.sh

step "end to end: a sealed spec and the project that implements it: sealed and project-mode errors, conformance, versions"
scripts/e2e-project.sh

step "unit 1 alone: a binary built with -tags unit1 checks spec/ and runs unit 1's end-to-end scripts"
# Unit 1's Go packages never import a later unit's, so their tests above are
# already unit 1's alone.
if go list -test -deps ./internal/core/... ./std/... | grep -q '/internal/tools'; then echo "unit 1 imports unit 2"; exit 1; fi
go build -tags unit1 -o "$tmp/heron1" ./cmd/heron
if "$tmp/heron1" lsp </dev/null >/dev/null 2>&1; then echo "the unit-1 binary has unit 2's lsp"; exit 1; fi
"$tmp/heron1" fmt --check fixtures spec std examples
"$tmp/heron1" check spec examples/petstore
"$tmp/heron1" lint spec examples/petstore
HERON_TAGS=unit1 scripts/e2e-setup.sh >/dev/null
HERON_TAGS=unit1 scripts/e2e-tasks.sh >/dev/null
HERON_TAGS=unit1 scripts/e2e-interop.sh >/dev/null
HERON_TAGS=unit1 scripts/e2e-project.sh >/dev/null

step "the editor's acceptance tests: Playwright against heron serve on the sample"
scripts/e2e-app.sh

step "language server end to end: heron lsp over stdio, Vale and the spec-aware lints"
scripts/e2e-lsp.sh
step "docs end to end: petstore and spec/ sites, every link and island"
scripts/e2e-docs.sh

step ok
