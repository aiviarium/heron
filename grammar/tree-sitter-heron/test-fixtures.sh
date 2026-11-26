#!/usr/bin/env bash
# Parses the Go parser's fixtures with the tree-sitter grammar: every Heron
# example of the spec package of mode "heron" (extracted to a temporary
# folder), the fixtures' context files, the spec package itself and the
# repository's own Heron trees (std/, examples/, project/) must parse without an
# ERROR or MISSING node. A prose file (.heron.md, mode heron-md) is Markdown
# to editors, and a sketch (heron-sketch) elides parts on purpose, so both
# are left out, as the "other" blocks are.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
cd "$here"
ts=(npx --no-install tree-sitter)

fx=$(mktemp -d); trap 'rm -rf "$fx"' EXIT
(cd "$root" && go run ./internal/core/fixtures/extract -out "$fx" >/dev/null)
spec=$(cd "$root" && go run ./internal/core/fixtures/specdir)

files=()
while IFS= read -r f; do files+=("$fx/$f"); done < <(
  node -e 'for (const e of require(process.argv[1])) if (e.mode === "heron") console.log(e.out)' "$fx/manifest.json")
while IFS= read -r f; do files+=("$f"); done < <(
  find "$root/fixtures/context" "$root/std" "$spec" "$root/examples" "$root/project" "$root/index.heron" \
    -name '*.heron' -not -path '*/node_modules/*' -not -path '*/.heron/*' | sort)

bad=0
for f in "${files[@]}"; do
  out=$("${ts[@]}" parse --quiet "$f" 2>/dev/null || true)
  if [ -n "$out" ] && grep -qE 'ERROR|MISSING' <<<"$out"; then
    echo "$out"
    bad=$((bad + 1))
  fi
done
echo "tree-sitter: ${#files[@]} files, $bad with ERROR or MISSING nodes"
[ "$bad" = 0 ]

# The queries compile against the grammar and match on a fixture.
for q in queries/*.scm; do
  if ! out=$("${ts[@]}" query "$q" "$fx/language.apis.apis_and_ops.apis_example.heron" 2>&1); then
    echo "$q doesn't compile:"; grep -v -i 'warning\|config\|grammars\|^$' <<<"$out" | head -5; exit 1
  fi
  grep -q 'capture:' <<<"$out" || { echo "$q captures nothing"; exit 1; }
done
echo "tree-sitter: queries compile"

# Prose is authored text, not code: a prose line's words get no keyword,
# variable, type or number capture, only Markdown's marks, elements, links
# and code spans do.
pf="$fx/prose-highlight.heron"
printf '%s\n' 'x: section {' \
  '  | Refunds are asked for if the order is paid and then in time.' \
  '  | - a list item with *emphasis* and <step id="a" @lens="user">for in</step>.' \
  '}' > "$pf"
caps=$("${ts[@]}" query queries/highlights.scm "$pf" 2>/dev/null | grep 'capture:' | grep -E 'start: \((1|2),' || true)
if grep -E 'capture: [0-9]+ - (keyword|variable|type|number|constant|function)' <<<"$caps" |
  grep -q .; then
  echo "tree-sitter: a prose line has code captures:"; echo "$caps"; exit 1
fi
echo "tree-sitter: prose lines have no code captures"
