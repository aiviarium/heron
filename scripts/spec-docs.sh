#!/usr/bin/env bash
# Heron's own spec as its documentation site: `heron generate docs` on the
# imported spec package ($HERON_SPECS_DIR, or the one scripts/spec-setup.sh
# installed), written to .heron/spec-docs/ at the repository root, then
# served with the URL printed.
#
#   scripts/spec-docs.sh                 generate and serve the static site
#   scripts/spec-docs.sh --live          serve the spec live in `heron serve` instead
#   scripts/spec-docs.sh --no-serve      generate only, and print the site's folder
#   scripts/spec-docs.sh --addr H:P      listen on H:P (default 127.0.0.1:7341;
#                                        port 0 picks a free one)
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD

mode=static
addr=127.0.0.1:7341
while [ $# -gt 0 ]; do
  case "$1" in
    --live) mode=live ;;
    --no-serve) mode=none ;;
    --addr) addr=$2; shift ;;
    --addr=*) addr=${1#--addr=} ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "spec-docs: unknown option $1" >&2; exit 2 ;;
  esac
  shift
done

spec=$(go run ./internal/core/fixtures/specdir)
bin=$root/.heron/bin
mkdir -p "$bin"
go build -o "$bin/heron" ./cmd/heron

if [ "$mode" = live ]; then
  # The spec browser renders the tree on disk as it changes; its docs are
  # under /docs/.
  "$bin/heron" serve --addr "$addr" "$spec" | sed -u "s#/\$#/docs/#"
  exit
fi

# The generator writes .heron/docs under the directory it runs in, so it
# runs in a scratch directory whose site then becomes .heron/spec-docs.
site=$root/.heron/spec-docs
work=$(mktemp -d "$root/.heron/spec-docs.XXXXXX")
trap 'rm -rf "$work"' EXIT
(cd "$work" && "$bin/heron" generate --entry "$spec" docs >/dev/null)
rm -rf "$site"
mv "$work/.heron/docs" "$site"
rm -rf "$work"
test -f "$site/index.html"
pages=$(find "$site" -name '*.html' | wc -l | tr -d ' ')

if [ "$mode" = none ]; then
  echo "$site ($pages pages)"
  exit 0
fi

host=${addr%:*}
port=${addr##*:}
if [ "$port" = 0 ]; then
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
fi
echo "$pages pages in $site"
echo "http://$host:$port/index.html"
exec python3 -m http.server --bind "$host" --directory "$site" "$port" >/dev/null 2>&1
