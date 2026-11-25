#!/usr/bin/env bash
# End to end: `heron run` with typed params, a missing param asked for on a
# pseudo-terminal and exit 2 without one, @needs in order, @undo after a
# failure, and the run record written.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
go build -tags "${HERON_TAGS:-}" -o "$tmp/heron" ./cmd/heron
heron() { "$tmp/heron" "$@"; }
fail() { echo "e2e-tasks: $*" >&2; exit 1; }

repo="$tmp/repo"
mkdir -p "$repo"
cat >"$repo/index.heron" <<'EOF'
import = ["std/types@1", "std/files@1", "std/process@1"]

Size: enum {
  SMALL: Self
  LARGE: Self
}

note(name: String): func do {
  run("sh", args = ["-c", "echo \"$1\" >> order.txt", "note", name])
}

fetch: task do {
  note("fetch")
}

@needs(fetch)
compile: task do {
  note("compile")
}

/// Builds a number of pets.
@needs(compile, fetch)
build(count: Int, size: Size, tags: List(String) = []): task(String) do {
  note("build")
  return "built $count $size ${tags.join("+")}"
}

migrate: task do {
  note("migrate")
}

unmigrate: task do {
  note("unmigrate")
}

@needs(migrate)
@undo(unmigrate)
seed: task do {
  note("seed")
}

@needs(seed)
verify: task do {
  run("sh", args = ["-c", "exit 3"])
}
EOF
cd "$repo"
heron check . || fail "the sample tree doesn't check"
order() { tr '\n' ' ' <order.txt; rm -f order.txt; }

echo "-- typed params, and @needs once each in dependency order"
out=$(heron run build --count 3 --size LARGE --tags a,b --tags c) || fail "build failed"
[ "$out" = "built 3 LARGE a+b+c" ] || fail "build printed $out"
[ "$(order)" = "fetch compile build " ] || fail "@needs order is wrong"
code=0; heron run build --count three --size LARGE </dev/null 2>"$tmp/err" || code=$?
[ $code -eq 2 ] && grep -q -- "--count takes an Int" "$tmp/err" || fail "a bad Int gave $code: $(cat "$tmp/err")"
code=0; heron run build --count 1 --size HUGE 2>"$tmp/err" </dev/null || code=$?
[ $code -eq 2 ] && grep -q "SMALL, LARGE" "$tmp/err" || fail "a bad variant gave $code: $(cat "$tmp/err")"
rm -f order.txt

echo "-- a missing param without a terminal is exit 2"
code=0; printf '' | heron run build --size SMALL 2>"$tmp/err" || code=$?
[ $code -eq 2 ] && grep -q "build needs --count (Int)" "$tmp/err" || fail "missing param gave $code: $(cat "$tmp/err")"
[ ! -f order.txt ] || fail "build ran without its param"

echo "-- a missing param is asked for on a pseudo-terminal"
python3 -I - "$tmp/heron" <<'EOF' || fail "the prompt on a pseudo-terminal failed"
import os, pty, sys, time

pid, fd = pty.fork()
if pid == 0:
    os.execv(sys.argv[1], [sys.argv[1], "run", "build", "--size", "SMALL"])
out = b""
deadline = time.time() + 30
while b"count (Int)" not in out:
    if time.time() > deadline:
        sys.exit("no prompt for count: %r" % out)
    out += os.read(fd, 1024)
os.write(fd, b"seven\n")
while out.count(b"count (Int)") < 2:
    if time.time() > deadline:
        sys.exit("a bad answer wasn't asked again: %r" % out)
    out += os.read(fd, 1024)
os.write(fd, b"7\n")
while True:
    try:
        chunk = os.read(fd, 1024)
    except OSError:
        break
    if not chunk:
        break
    out += chunk
_, status = os.waitpid(pid, 0)
if os.waitstatus_to_exitcode(status) != 0 or b"built 7 SMALL" not in out:
    sys.exit("exit %d, output %r" % (os.waitstatus_to_exitcode(status), out))
EOF
[ "$(order)" = "fetch compile build " ] || fail "the prompted run didn't run its needs"

echo "-- a failure undoes finished tasks only when asked"
code=0; heron run verify 2>"$tmp/err" || code=$?
[ $code -eq 1 ] || fail "verify gave $code"
[ "$(order)" = "migrate seed " ] || fail "without --undo-on-failure something was undone"
code=0; heron run --undo-on-failure verify 2>"$tmp/err" || code=$?
[ $code -eq 1 ] && grep -q "undoing seed with unmigrate" "$tmp/err" || fail "undo gave $code: $(cat "$tmp/err")"
[ "$(order)" = "migrate seed unmigrate " ] || fail "@undo didn't run after the failure"

echo "-- every run that started is recorded"
python3 -I - "$repo/.heron/audit/run" <<'EOF' || fail "the run records are wrong"
import json, os, sys

d = sys.argv[1]
recs = [json.load(open(os.path.join(d, f))) for f in sorted(os.listdir(d))]
runs = [(r["data"]["task"], r["data"]["status"]) for r in recs]
want = [("build", "ok"), ("build", "ok"), ("verify", "failed"), ("verify", "failed")]
if runs != want:
    sys.exit("runs: %r" % runs)
first, prompted, last = recs[0]["data"], recs[1]["data"], recs[3]["data"]
assert recs[0]["kind"] == "run" and recs[0]["version"] == 1, recs[0]
assert first["args"] == {"count": 3, "size": "LARGE", "tags": ["a", "b", "c"]}, first["args"]
assert first["result"] == "built 3 LARGE a+b+c", first["result"]
assert prompted["args"]["count"] == 7, prompted["args"]
assert [t["task"] for t in first["tasks"]] == ["fetch", "compile", "build"], first["tasks"]
assert all(t["end_ms"] >= t["start_ms"] for t in first["tasks"]), first["tasks"]
assert first["duration_ms"] >= 0 and first["started"] <= first["finished"]
assert len(first["processes"]) == 3 and first["processes"][0]["name"] == "sh", first["processes"]
roles = [(t["task"], t["role"], t["status"]) for t in last["tasks"]]
assert roles == [("migrate", "needed", "ok"), ("seed", "needed", "ok"), ("verify", "task", "failed"),
                 ("unmigrate", "undo", "ok")], roles
assert last["exit"] == 1 and "status 3" in last["error"], last["error"]
EOF

echo "e2e-tasks: ok"
