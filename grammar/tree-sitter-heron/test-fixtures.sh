#!/usr/bin/env bash
# Parses the Go parser's fixtures with the tree-sitter grammar: every Heron
# fixture of mode "heron", the fixtures' context files, and the repository's
# own Heron trees (std/, spec/, examples/) must parse without an
# ERROR or MISSING node. A prose file (.heron.md, mode heron-md) is Markdown
# to editors, and a sketch (heron-sketch) elides parts on purpose, so both
# are left out, as the "other" blocks are.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
cd "$here"
ts=(npx --no-install tree-sitter)

files=()
while IFS= read -r f; do files+=("$root/fixtures/$f"); done < <(
  node -e 'for (const e of require(process.argv[1])) if (e.mode === "heron") console.log(e.file)' "$root/fixtures/manifest.json")
while IFS= read -r f; do files+=("$f"); done < <(
  find "$root/fixtures/context" "$root/std" "$root/spec" "$root/examples" \
    "$root/internal/core/extension/protocol" -name '*.heron' -not -path '*/node_modules/*' | sort)

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
  if ! out=$("${ts[@]}" query "$q" "$root/fixtures/apis/01.heron" 2>&1); then
    echo "$q doesn't compile:"; grep -v -i 'warning\|config\|grammars\|^$' <<<"$out" | head -5; exit 1
  fi
  grep -q 'capture:' <<<"$out" || { echo "$q captures nothing"; exit 1; }
done
echo "tree-sitter: queries compile"
