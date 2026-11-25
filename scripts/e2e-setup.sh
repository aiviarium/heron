#!/usr/bin/env bash
# End to end: `heron run setup` on a fresh git copy of examples/petstore
# installs the toolchain check, the pinned sources and the workspace, writes
# heron.lock and the managed ignore block, and a second run changes nothing.
set -euo pipefail
cd "$(dirname "$0")/.."
here=$(pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
export GIT_AUTHOR_NAME=e2e GIT_AUTHOR_EMAIL=e2e@example.com
export GIT_COMMITTER_NAME=e2e GIT_COMMITTER_EMAIL=e2e@example.com
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

go build -tags "${HERON_TAGS:-}" -o "$tmp/heron" ./cmd/heron
heron() { "$tmp/heron" "$@"; }
fail() { echo "e2e-setup: $*" >&2; exit 1; }

# The external project the sample pins, as a local git repository with the
# tag the sample names, so the test needs no network.
git init -q "$tmp/mailer"
echo "mailer" >"$tmp/mailer/README"
git -C "$tmp/mailer" add -A
git -C "$tmp/mailer" commit -qm "mailer 1.8.0"
git -C "$tmp/mailer" tag -a v1.8.0 -m v1.8.0
echo "later" >>"$tmp/mailer/README"
git -C "$tmp/mailer" commit -qam "after 1.8.0"
tagged=$(git -C "$tmp/mailer" rev-parse 'v1.8.0^{commit}')

repo="$tmp/petstore"
cp -r "$here/examples/petstore" "$repo"
sed -i "s#https://github.com/petstore/mailer#file://$tmp/mailer#" "$repo/projects.heron"
cd "$repo"
git init -q
git add -A
git commit -qm "a fresh checkout"

echo "-- first run installs and writes"
heron run setup >"$tmp/out1" 2>&1 || { cat "$tmp/out1"; fail "the first setup failed"; }
[ -f heron.lock ] || fail "no heron.lock"
grep -q '"std/spec" = { version = "1.0", checksum = "sha256:' heron.lock || fail "std/spec isn't pinned"
grep -q "mailer = { url = \"file://$tmp/mailer\", ref = \"v1.8.0\", commit = \"$tagged\" }" heron.lock ||
  { cat heron.lock; fail "mailer isn't pinned at v1.8.0"; }
# Unit 2's setup step pins Vale, for the sample's .vale.ini, and syncs it;
# a unit-1-only binary (HERON_TAGS=unit1) has no such step.
case " ${HERON_TAGS:-} " in
*" unit1 "*) ! grep -q 'vale = {' heron.lock || fail "a unit-1 binary pinned Vale" ;;
*) grep -q 'vale = { version = "v3.28.1", checksum = "sha256:' heron.lock || { cat heron.lock; fail "Vale isn't pinned"; } ;;
esac
heron fmt --check heron.lock || fail "heron.lock isn't formatted"
heron check heron.lock || fail "heron.lock doesn't check"
[ "$(git -C vendor/mailer rev-parse HEAD)" = "$tagged" ] || fail "vendor/mailer isn't at v1.8.0"
grep -qx '# heron:begin (managed by heron; edits inside are replaced)' .gitignore || fail "no managed block"
grep -qx '# from heron run setup' .gitignore || fail "the block has no section for setup"
grep -qx '/.heron/' .gitignore || fail "the block doesn't ignore .heron/"
grep -qx '/vendor/mailer/' .gitignore || fail "the block doesn't ignore the checkout"
grep -qx 'node_modules/' .gitignore || fail "the repository's own entry is lost"
[ -d node_modules ] || fail "pnpm install didn't run"
ls .heron/audit/run/*.json >/dev/null 2>&1 || fail "setup wasn't recorded"
[ -f .heron/types.ts ] || fail "setup didn't run the generation targets"
[ -z "$(git status --porcelain --untracked-files=all -- vendor .heron node_modules)" ] ||
  fail "generated paths aren't ignored"

git add -A
git commit -qm "after setup"
stamp() { stat -c '%n %Y %s' heron.lock .gitignore; git -C vendor/mailer rev-parse HEAD; }
before=$(stamp)

echo "-- second run changes nothing"
sleep 1
heron run setup >"$tmp/out2" 2>&1 || { cat "$tmp/out2"; fail "the second setup failed"; }
[ -z "$(git status --porcelain --untracked-files=all)" ] || { git status --short; fail "the second run changed files"; }
[ "$(stamp)" = "$before" ] || fail "the second run rewrote heron.lock or .gitignore"
[ "$(ls .heron/audit/run/*.json | wc -l)" -eq 2 ] || fail "each setup run isn't recorded"

echo "-- a pin that doesn't match fails, and --update moves it"
sed -i 's/"std\/spec" = { version = "1.0", checksum = "sha256:./"std\/spec" = { version = "1.0", checksum = "sha256:X/' heron.lock
if heron run setup >"$tmp/out3" 2>&1; then fail "a wrong pin was accepted"; fi
grep -q "heron.lock pins std/spec" "$tmp/out3" || { cat "$tmp/out3"; fail "the wrong pin isn't named"; }
heron run setup --update >/dev/null 2>&1 || fail "--update failed"
[ -z "$(git status --porcelain)" ] || fail "--update didn't restore the pin"

echo "-- a toolchain that doesn't match fails, naming the opt-in install"
sed -i 's/pnpm = "10"/pnpm = "1"/' index.heron
if heron run setup >"$tmp/out4" 2>&1; then fail "a wrong toolchain was accepted"; fi
grep -q "wants pnpm 1, but found" "$tmp/out4" && grep -q -- "--install_toolchain" "$tmp/out4" ||
  { cat "$tmp/out4"; fail "the toolchain failure isn't explained"; }
git checkout -q index.heron

echo "-- a package from git installs from heron.lock before the tree is checked"
# A spec repository with its package in heron/, tagged, as a local git
# repository so the test needs no network.
specs="$tmp/specs"
mkdir -p "$specs/heron"
git init -q "$specs"
spec_pkg() {
  printf 'shop: package(version = "%s") {\n  /// The most widgets in a cart.\n  max_widgets<export>: Int = %s\n}\n' "$1" "$2" >"$specs/heron/package.heron"
  git -C "$specs" add -A
  git -C "$specs" commit -qm "shop $1"
  git -C "$specs" tag -a "v$1" -m "v$1"
}
echo "specs" >"$specs/README"
spec_pkg 1.0.0 3
spec_pkg 1.1.0 4
v110=$(git -C "$specs" rev-parse 'v1.1.0^{commit}')
impl="$tmp/impl"
mkdir -p "$impl"
cat >"$impl/index.heron" <<EOF
import = ["std/repo@1", "git+file://$specs//heron@v1"]

cart_limit: Int = max_widgets

repository {
  setup(update: Bool = false): task do {
    install_packages(update = update)
  }
}
EOF
cd "$impl"
git init -q
git add -A
git commit -qm "an implementation that imports the spec"
if heron check . >"$tmp/out5" 2>&1; then fail "the tree checked before its package was installed"; fi
grep -q "isn't installed yet" "$tmp/out5" || { cat "$tmp/out5"; fail "the missing package isn't explained"; }
heron run setup >"$tmp/out6" 2>&1 || { cat "$tmp/out6"; fail "setup with a git package failed"; }
pkg="${specs#/}/heron"
grep -q "\"$pkg\" = { version = \"1.1.0\", checksum = \"sha256:[0-9a-f]*\", url = \"file://$specs\", path = \"heron\", ref = \"v1.1.0\", commit = \"$v110\" }" heron.lock ||
  { cat heron.lock; fail "the git package isn't pinned at the newest v1 tag"; }
[ -f ".heron/packages/$pkg/package.heron" ] || fail "the package isn't installed under .heron/packages"
heron check . >/dev/null || fail "the tree doesn't check once its package is installed"
heron fmt --check heron.lock || fail "heron.lock isn't formatted"
git add heron.lock
git commit -qm "pin the spec"
# A fresh clone has only the lock: setup installs from it, at the pinned
# commit, though the spec has a newer v1 tag by now.
spec_pkg 1.2.0 5
rm -rf .heron
heron run setup >"$tmp/out7" 2>&1 || { cat "$tmp/out7"; fail "setup from the lock failed"; }
grep -q "installed $pkg from heron.lock" "$tmp/out7" || { cat "$tmp/out7"; fail "the package wasn't installed from the lock"; }
grep -q "max_widgets<export>: Int = 4" ".heron/packages/$pkg/package.heron" || fail "the install isn't the pinned commit"
[ -z "$(git status --porcelain -- heron.lock)" ] || { git diff; fail "setup from the lock moved the pin"; }
heron run setup --update >"$tmp/out8" 2>&1 || { cat "$tmp/out8"; fail "setup --update failed"; }
grep -q 'ref = "v1.2.0"' heron.lock || { cat heron.lock; fail "--update didn't move the pin to v1.2.0"; }
grep -q "max_widgets<export>: Int = 5" ".heron/packages/$pkg/package.heron" || fail "--update didn't install v1.2.0"
# An install that no longer matches its pin is installed again.
echo "edited" >>".heron/packages/$pkg/package.heron"
heron run setup >/dev/null 2>&1 || fail "setup didn't repair an edited install"
grep -q "edited" ".heron/packages/$pkg/package.heron" && fail "the edited install was kept"

echo "e2e-setup: ok"
