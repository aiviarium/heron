#!/usr/bin/env bash
# Done means #5 of unit 1, through the CLI: a sample prose lint provider
# flags words in `{{ }}`, `|` and `///` text, never inside code, inserts or
# links, and its fixes apply to the exact source range.
set -euo pipefail
cd "$(dirname "$0")/.."

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/heron" ./internal/core/lint/prose/words/heronwords
export HERON_WORDS="simply=,utilize=use"
mkdir "$tmp/p"; cd "$tmp/p"

cat > index.heron <<'EOF'
import = ["std/doc@1"]
let simply = "x"

/// Simply put, the guide.
guide: section {
  | We utilize the console. Not `simply`, $simply, ${simply}
  | or [simply](#guide).
  notes: section {{
    Refunds are simply issued.
  }}
}

@lint(allow = ["Words.Flagged"])
quiet: section {
  | Simply allowed.
}
EOF

fail() { echo "e2e-prose: $*"; exit 1; }

set +e
out=$("$tmp/heron" lint); code=$?
set -e
[ "$code" = 1 ] || fail "lint exited $code, want 1: $out"
want='index.heron:4:5: warning: avoid "Simply" [Words.Flagged]
index.heron:6:8: warning: avoid "utilize" [Words.Flagged]
index.heron:9:17: warning: avoid "simply" [Words.Flagged]'
[ "$out" = "$want" ] || fail "lint printed
$out
want
$want"

json=$("$tmp/heron" lint --json || true)
grep -q '"code": "Words.Flagged"' <<<"$json" || fail "--json lacks the findings"
grep -q '"new": "use"' <<<"$json" || fail "--json lacks the fixes"

set +e
"$tmp/heron" lint --fix 2>/dev/null; code=$?
set -e
[ "$code" = 0 ] || fail "lint --fix left findings (exit $code)"
expected=$(sed -e 's|^/// Simply put|/// put|' -e 's|We utilize|We use|' -e 's|are simply issued|are issued|' <<'EOF'
import = ["std/doc@1"]
let simply = "x"

/// Simply put, the guide.
guide: section {
  | We utilize the console. Not `simply`, $simply, ${simply}
  | or [simply](#guide).
  notes: section {{
    Refunds are simply issued.
  }}
}

@lint(allow = ["Words.Flagged"])
quiet: section {
  | Simply allowed.
}
EOF
)
[ "$(cat index.heron)" = "$expected" ] || fail "fixed file differs:
$(diff <(echo "$expected") index.heron)"
"$tmp/heron" check >/dev/null || fail "the fixed file doesn't check"
echo "e2e-prose: ok"
