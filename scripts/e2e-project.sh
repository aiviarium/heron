#!/usr/bin/env bash
# A detached spec and the project that implements it, through the CLI: the
# sample pair checks clean; amending the sealed spec and declaring spec
# kinds in the project are errors; `heron generate conformance` reports each
# requirement and the spec's changes; `heron release` computes the next
# version and checks the project version against the implemented spec.
set -euo pipefail
cd "$(dirname "$0")/.."

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
go build ${HERON_TAGS:+-tags "$HERON_TAGS"} -o "$tmp/heron" ./cmd/heron
heron="$tmp/heron"
fail() { echo "e2e-project: $*"; exit 1; }

# A fresh copy of the sample pair for each case.
pair() {
  rm -rf "$tmp/x"; mkdir -p "$tmp/x"
  cp -r examples/shop-spec examples/shop-spec-2.1.2 examples/shop "$tmp/x/"
}
# expect_error <code> <message part> [<check args>…]: heron check fails
# with an error of the code whose message contains the part.
expect_error() {
  local code=$1 part=$2; shift 2
  set +e
  out=$("$heron" check "$@" 2>&1); status=$?
  set -e
  [ "$status" = 1 ] || fail "check $* exited $status, want 1: $out"
  grep -F "[$code]" <<<"$out" | grep -qF -- "$part" || fail "check $*: no $code error with \"$part\": $out"
}

# The sample pair checks and lints clean.
"$heron" check examples/shop-spec examples/shop-spec-2.1.2 examples/shop
"$heron" lint examples/shop-spec examples/shop

# Sealed: the project can't amend, scope or set into the spec's nodes.
pair
printf '\nshop.checkout.{\n  extra: section {}\n}\n' >> "$tmp/x/shop/index.heron"
expect_error sealed "shop.checkout is in the sealed package shop 2.1.3" "$tmp/x/shop"
pair
printf '\nshop.checkout.pay_once.owner = "us"\n' >> "$tmp/x/shop/index.heron"
expect_error sealed "sealed package shop 2.1.3" "$tmp/x/shop"

# Project mode: the project declares no spec kinds.
pair
printf '\nextra: rule {\n  must {{ ship fast }}\n}\n' >> "$tmp/x/shop/index.heron"
expect_error project-mode "extra is a rule, which is spec content: this tree implements shop" "$tmp/x/shop"
pair
printf '\nnotes: section {\n  word: term {{ A word. }}\n}\n' >> "$tmp/x/shop/index.heron"
expect_error project-mode "notes.word is a term" "$tmp/x/shop"

# Conformance: each requirement's standing, and the changes since 2.1.2.
pair
(cd "$tmp/x/shop" && "$heron" generate conformance >/dev/null)
md="$tmp/x/shop/.heron/conformance/conformance.md"
for want in \
  '# Conformance of store to shop 2.1.3' \
  '5 requirements: 3 proven, 1 planned, 0 untested, 1 missing.' \
  '| `checkout.pay_once` | rule | proven | `internal/payments/charge_test.go` `TestChargeOnce`, `scripts/e2e-checkout.sh` `pay twice` |' \
  '| `checkout.refund_window` | rule | planned |  |' \
  '| `checkout.empty_cart` | rule | missing |  |' \
  '## Changes from shop 2.1.2 to 2.1.3' \
  '- Added: `checkout.empty_cart`' \
  '- Removed: `checkout.pay_later`' \
  '- Changed: `checkout.receipt`'; do
  grep -qF -- "$want" "$md" || fail "conformance.md lacks: $want"
done
grep -q '"status": "missing"' "$tmp/x/shop/.heron/conformance/conformance.json" || fail "conformance.json lacks the missing requirement"

# Versions: the next release, and the project version against the spec.
next=$("$heron" release next --entry "$tmp/x/shop")
[ "$next" = "2.1.3+build3" ] || fail "release next: $next, want 2.1.3+build3"
next=$("$heron" release next --entry "$tmp/x/shop" --tag rc)
[ "$next" = "2.1.3+rc3" ] || fail "release next --tag rc: $next, want 2.1.3+rc3"
"$heron" release check --entry "$tmp/x/shop" | grep -qF "store implements shop at 2.1.3, version 2.1.3+build2" || fail "release check"
sed -i 's|version = "2.1.3+build2"|version = "2.1.2+build2"|' "$tmp/x/shop/index.heron"
set +e
out=$("$heron" release check --entry "$tmp/x/shop" 2>&1); status=$?
set -e
[ "$status" = 1 ] || fail "release check of a version off the spec exited $status: $out"
grep -qF "the project version 2.1.2+build2 starts with 2.1.2, but the project implements shop at 2.1.3" <<<"$out" || fail "release check: $out"
expect_error version "starts with 2.1.2" "$tmp/x/shop"
# The 4-part scheme, opted into: IP bumps.
pair
sed -i 's|  version = "2.1.3+build2"|  version = "2.1.3.1"\n  versioning.{ scheme = "SM.Sm.Sp.IP" }|; s|release("2.1.3+build1"|release("2.1.3.0"|; s|release("2.1.3+build2"|release("2.1.3.1"|' "$tmp/x/shop/index.heron"
"$heron" check "$tmp/x/shop"
next=$("$heron" release next --entry "$tmp/x/shop")
[ "$next" = "2.1.3.2" ] || fail "release next in the 4-part scheme: $next, want 2.1.3.2"
# Release tags: every release has its git tag v<version>.
(cd "$tmp/x/shop" && git init -q && git -c user.email=e2e@example.invalid -c user.name=e2e commit -q --allow-empty -m init && git tag v2.1.3.0)
set +e
out=$("$heron" release check --git --entry "$tmp/x/shop" 2>&1); status=$?
set -e
[ "$status" = 1 ] && grep -qF "release 2.1.3.1 has no git tag v2.1.3.1" <<<"$out" || fail "release check --git: $status $out"
(cd "$tmp/x/shop" && git tag v2.1.3.1)
"$heron" release check --git --entry "$tmp/x/shop" >/dev/null || fail "release check --git with every tag"

echo "e2e-project: ok"
