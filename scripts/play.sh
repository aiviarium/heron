#!/usr/bin/env bash
# Plays with heron on a copy of an example: copies examples/<name> into a
# scratch folder, makes it a git repository, checks it, writes its docs and
# agent export, and runs `heron serve` on it, printing where the live docs
# and the agent export are and how to open the copy in VS Code with the
# Heron extension. Builds bin/heron first when it's missing.
#
#   scripts/play.sh [petstore|parcels|shop] [--addr host:port] [--dir folder]
#
# The copy is removed when the server stops, unless --dir names where to
# put it.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
example=petstore
addr=127.0.0.1:0
dir=""
while [ $# -gt 0 ]; do
  case "$1" in
    --addr) addr=$2; shift ;;
    --dir) dir=$2; shift ;;
    -*) echo "usage: scripts/play.sh [example] [--addr host:port] [--dir folder]" >&2; exit 2 ;;
    *) example=$1 ;;
  esac
  shift
done
[ -f "examples/$example/index.heron" ] || { echo "play: no example $example; the examples are: $(cd examples && ls -d */ | tr -d / | tr '\n' ' ')" >&2; exit 2; }

heron=${HERON_BIN:-$root/bin/heron}
[ -x "$heron" ] || scripts/build.sh

if [ -z "$dir" ]; then
  dir=$(mktemp -d "${TMPDIR:-/tmp}/heron-play.XXXXXX")
  trap 'rm -rf "$dir"' EXIT
fi
mkdir -p "$dir"
work="$dir/$example"
rm -rf "$work"
cp -r "examples/$example" "$work"
rm -rf "$work/node_modules" "$work/.heron"
(cd "$work" && git init -q -b main && git add -A && git -c user.name=play -c user.email=play@localhost commit -qm "$example")

cd "$work"
echo "== $work"
"$heron" check . && echo "heron check: clean"
"$heron" generate docs agents >/dev/null && echo "heron generate docs agents: .heron/docs/ and .heron/agents/"

log="$dir/.serve.log"
"$heron" serve --addr "$addr" >"$log" &
pid=$!
trap 'kill $pid 2>/dev/null || true; rm -rf "${dir:?}"/.serve.log' INT TERM
for _ in $(seq 100); do
  url=$(head -1 "$log" 2>/dev/null || true)
  [ -n "$url" ] && break
  kill -0 "$pid" 2>/dev/null || { echo "play: heron serve stopped" >&2; exit 1; }
  sleep 0.1
done
vsix=$root/editors/vscode/heron.vsix
cat <<MSG

heron serve is running on a copy of examples/$example:
  docs          ${url}docs/  (live: reload a page after an edit)
  agent export  ${url}agents/  (and ${url}agents/llms.txt)

To edit it in VS Code with the Heron extension:
MSG
if [ -f "$vsix" ]; then
  echo "  code --install-extension $vsix"
else
  echo "  (cd $root/editors/vscode && npm ci && npm run package)"
  echo "  code --install-extension $vsix"
fi
cat <<MSG
  code $work
with $(dirname "$heron") on PATH, or heron.path set to $heron. Heron: Open
Preview to the Side shows the node under the cursor as the docs do.
Press Ctrl-C to stop the server.
MSG
wait "$pid"
