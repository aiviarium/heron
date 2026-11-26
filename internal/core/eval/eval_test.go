package eval_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// fakeProcs records commands and answers them from a table by program.
type fakeProcs struct {
	runs    []eval.Command
	answers map[string]eval.Exit
}

func (p *fakeProcs) Run(_ context.Context, c eval.Command) (eval.Exit, error) {
	p.runs = append(p.runs, c)
	if e, ok := p.answers[c.Path]; ok {
		return e, nil
	}
	return eval.Exit{}, nil
}

// fakeFiles is an in-memory file system.
type fakeFiles struct {
	files   map[string]string
	temps   int
	removed []string
}

func (f *fakeFiles) Read(p string) ([]byte, error) {
	s, ok := f.files[p]
	if !ok {
		return nil, errors.New("no such file " + p)
	}
	return []byte(s), nil
}
func (f *fakeFiles) Write(p string, data []byte) error { f.files[p] = string(data); return nil }
func (f *fakeFiles) TempDir() (string, error)          { f.temps++; return "/tmp/run", nil }
func (f *fakeFiles) RemoveAll(p string) error          { f.removed = append(f.removed, p); return nil }

type fakeEnv []string

func (e fakeEnv) Environ() []string { return e }

type harness struct {
	t      *testing.T
	m      *eval.Machine
	procs  *fakeProcs
	files  *fakeFiles
	events []eval.Event
	logs   []string
}

// load checks a source file and returns a machine for it with fake
// capabilities; the file must check without errors.
func load(t *testing.T, src string, reg *registry.Registry) *harness {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := resolve.Canonical(p)
	s := check.NewSession()
	res := s.Check(id)
	for _, d := range res.Diags {
		if d.Severity.String() == "error" {
			t.Fatalf("check: %s: %s", d.Code, d.Message)
		}
	}
	h := &harness{t: t, procs: &fakeProcs{answers: map[string]eval.Exit{}}, files: &fakeFiles{files: map[string]string{}}}
	caps := eval.Caps{
		Processes: h.procs, Files: h.files, Env: fakeEnv{"PATH=/bin"},
		Clock: eval.FixedClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		Log:   func(l string) { h.logs = append(h.logs, l) },
	}
	h.m = eval.New(res.Tree, res.Checker, reg, caps)
	h.m.Dir = "/repo"
	h.m.OnEvent = func(e eval.Event) { h.events = append(h.events, e) }
	return h
}

func (h *harness) node(path string) *resolve.Node {
	h.t.Helper()
	n := h.m.Tree.Find(path)
	if n == nil {
		h.t.Fatalf("no node %s", path)
	}
	return n
}

func (h *harness) call(path string, named map[string]eval.Value, pos ...eval.Value) (eval.Value, error) {
	return h.m.Call(context.Background(), h.node(path), eval.Args{Positional: pos, Named: named})
}

func (h *harness) mustCall(path string, named map[string]eval.Value, pos ...eval.Value) eval.Value {
	h.t.Helper()
	v, err := h.call(path, named, pos...)
	if err != nil {
		h.t.Fatalf("%s: %v", path, err)
	}
	return v
}

func want(t *testing.T, got, exp eval.Value) {
	t.Helper()
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("got %#v, want %#v", got, exp)
	}
}

func TestBindingsAndControlFlow(t *testing.T) {
	h := load(t, `label(refunds: List(Int)): func(String) do {
  let label = if refunds.size == 1 { "refund" } else if refunds.size == 0 { "none" } else { "refunds" }
  return label
}

doubled(amounts: List(Int)): func(List(Int)) do {
  let out = for amount in amounts { amount * 2 }
  return out
}

first_big(amounts: List(Int), limit: Int = 10): func(Int) do {
  for a in amounts {
    if a > limit {
      return a
    }
  }
  return -1
}

refunded_within(paid: Int, refunded: Int): func(Bool) = refunded <= paid and not (refunded < 0)

greet(name: String): func(String) = "Hello, $name! ${name.size} letters"
`, nil)
	want(t, h.mustCall("label", nil, []eval.Value{int64(4)}), "refund")
	want(t, h.mustCall("label", nil, []eval.Value{}), "none")
	want(t, h.mustCall("label", map[string]eval.Value{"refunds": []eval.Value{int64(1), int64(2)}}), "refunds")
	want(t, h.mustCall("doubled", nil, []eval.Value{int64(1), int64(5)}), []eval.Value{int64(2), int64(10)})
	want(t, h.mustCall("first_big", nil, []eval.Value{int64(3), int64(12), int64(40)}), int64(12))
	want(t, h.mustCall("first_big", map[string]eval.Value{"limit": int64(100)}, []eval.Value{int64(3)}), int64(-1))
	want(t, h.mustCall("refunded_within", map[string]eval.Value{"paid": int64(10), "refunded": int64(4)}), true)
	want(t, h.mustCall("refunded_within", map[string]eval.Value{"paid": int64(1), "refunded": int64(4)}), false)
	want(t, h.mustCall("greet", map[string]eval.Value{"name": "Ada"}), "Hello, Ada! 3 letters")
	if _, err := h.call("label", nil); err == nil || !strings.Contains(err.Error(), "needs the argument refunds") {
		t.Errorf("missing argument: %v", err)
	}
}

func TestOperators(t *testing.T) {
	h := load(t, `ops: func(List(Any)) do {
  return [1 + 2 * 3, 7 / 2, 7 % 3, 1.5 + 1, "a" + "b", 2 in [1, 2], "x" in "xyz", 3 != 4, "a" < "b", [1] + [2]]
}
`, nil)
	want(t, h.mustCall("ops", nil), []eval.Value{int64(7), int64(3), int64(1), 2.5, "ab", true, true, true, true, []eval.Value{int64(1), int64(2)}})
}

// The Processes and files section's example.
const seedSrc = `import = ["std/types@1", "std/process@1", "std/files@1", "std/json@1"]

PetStatus: enum {
  AVAILABLE: Self
  SOLD: Self
}

Pet: type {
  id: Int
  status: PetStatus
}

seed_pets(count: Int, status: PetStatus): func(List(Pet)) do {
  let out = run(
    "./scripts/seed-pets",
    args = ["--count", count, "--status", status],
  )
  return json.decode(List(Pet), out.stdout)
}

export_orders(pet_id: Int): func(String) do {
  let path = temp("orders.csv")
  run(
    "./scripts/export-orders",
    env = { PET_ID = pet_id },
    stdout = path,
  )
  return path
}

safe_seed: func(Bool) do {
  let result = attempt(run("./scripts/fail"))
  if result.error is ProcessError {
    log("seeding failed with ${result.error.exit}")
    return false
  }
  return true
}

shout(name: String): func(String) do {
  let out = run((sh)"echo $name | tr a-z A-Z")
  return out.stdout
}

write_report(n: Int): func(String) do {
  write("report.json", (json)"""
    {
      "count": $n
    }
    """)
  return read("report.json")
}
`

func TestProcessesAndFiles(t *testing.T) {
	h := load(t, seedSrc, nil)
	h.procs.answers["./scripts/seed-pets"] = eval.Exit{Stdout: []byte(`[{"id": 1, "status": "AVAILABLE"}]`)}
	got := h.mustCall("seed_pets", map[string]eval.Value{"count": int64(2), "status": "AVAILABLE"})
	want(t, got, []eval.Value{&eval.Record{Type: "Pet", Fields: map[string]eval.Value{"id": int64(1), "status": "AVAILABLE"}}})
	run := h.procs.runs[0]
	want(t, run.Path, "./scripts/seed-pets")
	want(t, run.Args, []string{"--count", "2", "--status", "AVAILABLE"})
	want(t, run.Dir, "/repo")

	h.procs.answers["./scripts/export-orders"] = eval.Exit{Stdout: []byte("id,amount\n1,40\n")}
	want(t, h.mustCall("export_orders", map[string]eval.Value{"pet_id": int64(7)}), "/tmp/run/orders.csv")
	exp := h.procs.runs[1]
	want(t, exp.Env, []string{"PATH=/bin", "PET_ID=7"})
	want(t, h.files.files["/tmp/run/orders.csv"], "id,amount\n1,40\n")
	if err := h.m.Close(); err != nil || len(h.files.removed) != 1 {
		t.Errorf("temp files not removed: %v %v", err, h.files.removed)
	}

	h.procs.answers["./scripts/fail"] = eval.Exit{Code: 3, Stderr: []byte("boom")}
	want(t, h.mustCall("safe_seed", nil), false)
	want(t, h.logs, []string{"seeding failed with 3"})
	if _, err := h.call("seed_pets", map[string]eval.Value{"count": int64(1), "status": "SOLD"}); err != nil {
		t.Fatal(err)
	}

	h.procs.answers["sh"] = eval.Exit{Stdout: []byte("A B; RM -RF\n")}
	h.mustCall("shout", map[string]eval.Value{"name": "a b; rm -rf"})
	sh := h.procs.runs[len(h.procs.runs)-1]
	want(t, sh.Path, "sh")
	want(t, sh.Args, []string{"-c", "echo 'a b; rm -rf' | tr a-z A-Z"})

	want(t, h.mustCall("write_report", map[string]eval.Value{"n": int64(3)}), "{\n  \"count\": 3\n}")
}

func TestUncaughtThrowFails(t *testing.T) {
	h := load(t, seedSrc, nil)
	h.procs.answers["./scripts/seed-pets"] = eval.Exit{Code: 1, Stderr: []byte("no db")}
	_, err := h.call("seed_pets", map[string]eval.Value{"count": int64(1), "status": "SOLD"})
	var th *eval.Throw
	if !errors.As(err, &th) || th.Value.Type != "ProcessError" || th.Value.Fields["exit"] != int64(1) {
		t.Fatalf("want a ProcessError, got %v", err)
	}
	if !strings.Contains(err.Error(), "no db") {
		t.Errorf("message: %v", err)
	}
}

// The Tasks section's example: a task calls another like a func.
func TestTasks(t *testing.T) {
	h := load(t, `import = ["std/process@1"]

/// Seeds a local store with available pets.
seed_local(count: Int = 20): task(Int) do {
  run("./scripts/seed-pets", args = ["--count", count])
  return count
}

@needs(seed_local)
demo: task<confirm> do {
  let n = seed_local(count = 5)
  log("seeded $n")
  run("pnpm", args = ["dev"])
}
`, nil)
	h.mustCall("demo", nil)
	want(t, h.logs, []string{"seeded 5"})
	want(t, len(h.procs.runs), 2)
	want(t, h.mustCall("seed_local", nil), int64(20))
	var calls []string
	for _, e := range h.events {
		if e.Kind == eval.EventCall {
			calls = append(calls, e.Name)
		}
	}
	want(t, calls, []string{"process.run", "seed_local", "core.log", "process.run", "demo", "process.run", "seed_local"})
}

func TestMacros(t *testing.T) {
	h := load(t, `import = ["std/types@1", "std/http@1"]

resource_ops(name: String, path: String): macro = (heron)"""
  @http<get>("$path")
  list_${name}s(@query cursor?: String): op(Page(${name.pascal}))
  """

crud(names: List(String)): macro do {
  for n in names {
    emit("${n}_count: Int")
  }
}
`, nil)
	src, err := h.m.Expand(context.Background(), h.node("resource_ops"), eval.Args{Named: map[string]eval.Value{"name": "pet_category", "path": "/categories"}})
	if err != nil {
		t.Fatal(err)
	}
	want(t, src, "@http<get>(\"/categories\")\nlist_pet_categorys(@query cursor?: String): op(Page(PetCategory))")
	src, err = h.m.Expand(context.Background(), h.node("crud"), eval.Args{Positional: []eval.Value{[]eval.Value{"a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	want(t, src, "a_count: Int\nb_count: Int")
}

func TestNodeFieldsAndHooks(t *testing.T) {
	h := load(t, `import = ["std/schema@1"]

resource(ready?: Bool): kind {
  accept(code)
}

db: resource {
  ready = false
  do {
    db.ready = true
    log("ready: ${db.ready}")
  }
}

when: func(String) = clock.now
`, nil)
	if _, err := h.m.RunCode(context.Background(), h.node("db"), nil); err != nil {
		t.Fatal(err)
	}
	want(t, h.logs, []string{"ready: true"})
	want(t, h.m.State[h.node("db")]["ready"], true)
	want(t, h.mustCall("when", nil), "2026-01-02T03:04:05.000Z")
}

func TestCodeErrors(t *testing.T) {
	cases := []struct{ src, fn, want string }{
		{"f: func(Int) do {\n  let a = 1\n  let a = 2\n  return a\n}\n", "f", "locals never change"},
		{"f: func(Int) do {\n  return f()\n}\n", "f", "calls itself"},
		{"f: func(Int) do {\n  return 1 + \"x\"\n}\n", "f", "Int + String isn't defined"},
		{"f: func(Int) do {\n  if 1 { return 1 }\n  return 0\n}\n", "f", "if needs a Bool"},
		{"f: func(Int) do {\n  return 1 / 0\n}\n", "f", "division by zero"},
		// Int arithmetic that leaves the range of Int fails, located,
		// rather than wrap around.
		{"f: func(Int) do {\n  let big = 9223372036854775807\n  return big + 1\n}\n", "f", "index.heron:3:10: 9223372036854775807 + 1 overflows Int, a 64-bit integer"},
		{"f: func(Int) do {\n  let small = -9223372036854775807\n  return small - 2\n}\n", "f", "-9223372036854775807 - 2 overflows Int"},
		{"f: func(Int) do {\n  let big = 4294967296\n  return big * big\n}\n", "f", "4294967296 * 4294967296 overflows Int"},
		{"f: func(Int) do {\n  let min = -9223372036854775807 - 1\n  return min / -1\n}\n", "f", "-9223372036854775808 / -1 overflows Int"},
		{"f: func(Int) do {\n  let min = -9223372036854775807 - 1\n  return -min\n}\n", "f", "-(-9223372036854775808) overflows Int"},
		{"f: func(Int) do {\n  return 9223372036854775808\n}\n", "f", "9223372036854775808 overflows Int"},
		{"f: func(Int) do {\n  let a = 1\n  a.x = 2\n  return a\n}\n", "f", "locals never change"},
		{"g(x: Int): func\nf: func do {\n  g(1)\n}\n", "f", "no implementation is registered for g"},
	}
	for _, c := range cases {
		h := load(t, c.src, nil)
		_, err := h.call(c.fn, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: want %q, got %v", c.src, c.want, err)
		}
	}
	h := load(t, "f: func(Int) do {\n  return 1 + \"x\"\n}\n", nil)
	_, err := h.call("f", nil)
	if !strings.Contains(err.Error(), "index.heron:2:10:") {
		t.Errorf("want a position, got %v", err)
	}
}

// Foreign funcs reach their host through the registry, by module kind,
// and funcs declared without a body through their path.
func TestForeignFuncs(t *testing.T) {
	reg := registry.New()
	var bound []string
	reg.MustAddForeign(registry.Foreign{Kind: "js", Bind: func(m registry.Module, export string) (registry.FuncImpl, error) {
		bound = append(bound, m.Kind+" "+m.Spec+" "+m.Path+" "+export)
		return func(_ context.Context, call *registry.FuncCall) (any, error) {
			if call.Args["pet_id"] == int64(0) {
				return nil, &registry.Thrown{Type: "NotFound", Fields: map[string]any{"message": "no pet"}}
			}
			return map[string]any{"path": "/out/" + call.Func, "pet": call.Args["pet_id"]}, nil
		}, nil
	}})
	reg.MustAddFunc(registry.Func{Name: "checksum", Call: func(_ context.Context, call *registry.FuncCall) (any, error) {
		return float64(len(call.Args["text"].(string))), nil
	}})
	h := load(t, `import = ["std/js@1"]

tools: module = js("./tools/orders.ts")

/// Writes every order of a pet to a CSV file and returns its path.
export_orders(pet_id: Int): func(Any) = tools.exportOrders

checksum(text: String): func(Int)

export_pet_orders(pet_id: Int): task do {
  let out = export_orders(pet_id = pet_id)
  log("Wrote ${out.path} for ${out.pet}, sum ${checksum(out.path)}")
  let missing = attempt(export_orders(pet_id = 0))
  log(missing.error.message)
}
`, reg)
	h.mustCall("export_pet_orders", map[string]eval.Value{"pet_id": int64(4)})
	want(t, bound, []string{"js ./tools/orders.ts tools exportOrders"})
	want(t, h.logs, []string{"Wrote /out/export_orders for 4, sum 18", "no pet"})
}

func TestStepsAreRecorded(t *testing.T) {
	h := load(t, `check(name: String): func

f: func do {
  check("rejected") {
    log("inside")
  }
}
`, nil)
	h.mustCall("f", nil)
	var steps []string
	for _, e := range h.events {
		if e.Kind == eval.EventStep {
			steps = append(steps, e.Name)
		}
	}
	want(t, steps, []string{"rejected"})
	want(t, h.logs, []string{"inside"})
}

// Escapes and the trimming of multi-line strings, as the grammar's strings
// section states them.
func TestStringValues(t *testing.T) {
	src := `strs: func(List(Any)) do {
  return ["a\"b", "c\$d", "t\tu", "x\qy", """
    one
      two
    """, ` + "`raw $x \\n`" + `]
}
`
	h := load(t, src, nil)
	want(t, h.mustCall("strs", nil), []eval.Value{"a\"b", "c$d", "t\tu", "xqy", "one\n  two", "raw $x \\n"})
}

// json.encode writes one line of JSON, with <, > and & as themselves, and
// json.decode reads it back.
func TestJSONEncode(t *testing.T) {
	src := `import = ["std/json@1"]
enc: func(String) do {
  return json.encode({ name = "<a&b>", n = 2, ok = true, tags = ["x"] })
}
`
	h := load(t, src, nil)
	want(t, h.mustCall("enc", nil), `{"n":2,"name":"<a&b>","ok":true,"tags":["x"]}`)
}

// json.encode and json.decode name a record's fields as std/json's
// @json says: a member's own name, or its type's case.
func TestJSONNames(t *testing.T) {
	src := `import = ["std/types@1", "std/json@1"]

@keys(camel)
Position: type {
  line_number: Int
  @key("char")
  character_offset: Int
}

enc: func(String) do {
  return json.encode(Position(line_number = 2, character_offset = 5))
}

dec: func(Int) do {
  let p = json.decode(Position, "{\"lineNumber\": 7, \"char\": 1}")
  return p.line_number
}
`
	h := load(t, src, nil)
	want(t, h.mustCall("enc", nil), `{"char":5,"lineNumber":2}`)
	want(t, h.mustCall("dec", nil), int64(7))
}

// The edges of values, members, loops, calls and failures that the
// Scripting section's code rules name.
func TestCodeEdges(t *testing.T) {
	reg := registry.New()
	var stepped int
	reg.MustAddFunc(registry.Func{Name: "twice", Call: func(ctx context.Context, call *registry.FuncCall) (any, error) {
		for i := 0; i < 2; i++ {
			if err := call.Step(ctx); err != nil {
				return nil, err
			}
			stepped++
		}
		return nil, nil
	}})
	h := load(t, `import = ["std/types@1", "std/process@1"]

Pet: type {
  name: String
  tag?: String
}

total(items: List(Int)): func(Int) do {
  let sum = for i in items { i }
  return sum.size
}

twice(name: String): func

check(name: String): func

edges: func(List(Any)) do {
  let pet = Pet(name = "Rex")
  let keys = for k in { b = 1, a = 2 } { k }
  let none = for x in null { x }
  twice("loop") {
    log("again")
  }
  return [Int("3"), String(4), attempt(Int("three")).error.message, pet.tag, [].first, { b = 1, a = 2 }.keys, [1, 2, 3].total(), keys, none, "pet_category".pascal, "PetCategory".snake]
}

early: func(Int) do {
  check("early") {
    return 5
  }
  return 7
}

bad_attempt: func(Any) do {
  return attempt(1 + "x")
}

two_attempt: func(Any) do {
  return attempt(1, 2)
}

null_member: func(Any) do {
  let p = null
  return p.name
}

no_method: func(Any) do {
  let n = 3
  return n.frobnicate()
}

bad_for: func(Any) do {
  return for x in 3 { x }
}

in_arg: func(Any) do {
  return String(if true { return 1 })
}

limit: Int = 3

call_field: func(Any) do {
  return limit()
}

proc: func(Any) do {
  return run("ls")
}

when: func(Any) = clock.now
`, reg)
	want(t, h.mustCall("edges", nil), []eval.Value{
		int64(3), "4", `"three" is no Int`, nil, nil, nil, int64(3),
		[]eval.Value{"a", "b"}, []eval.Value{}, "PetCategory", "pet_category",
	})
	want(t, h.mustCall("early", nil), int64(7))
	want(t, stepped, 2)
	want(t, h.logs, []string{"again", "again"})
	cases := map[string]string{
		"bad_attempt": "Int + String isn't defined",
		"two_attempt": "attempt takes one call",
		"null_member": "null has no member name",
		"no_method":   "Int has no method frobnicate",
		"bad_for":     "for iterates a list, a set or a map, not Int",
		"in_arg":      "return can't stand inside an expression",
		"call_field":  "limit can't be called: it is no func, task or type",
	}
	for fn, msg := range cases {
		if _, err := h.call(fn, nil); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: want %q, got %v", fn, msg, err)
		}
	}
	h.m.Caps = eval.Caps{}
	if _, err := h.call("proc", nil); err == nil || !strings.Contains(err.Error(), "no processes can run here") {
		t.Errorf("no processes: %v", err)
	}
	if _, err := h.call("when", nil); err == nil || !strings.Contains(err.Error(), "no clock is available") {
		t.Errorf("no clock: %v", err)
	}
}

// missingProcs fails to start every program, as when it isn't on PATH.
type missingProcs struct{}

func (missingProcs) Run(_ context.Context, c eval.Command) (eval.Exit, error) {
	return eval.Exit{}, errors.New(`exec: "` + c.Path + `": executable file not found in $PATH`)
}

// The edges of processes and files: a program that can't start, a script
// with arguments, inserts that opt out of quoting, a file that isn't there
// and a temp path asked for twice.
// The error types std/files, std/json and std/core declare check where code
// names them, and a thrown error is its declared type and an Error.
func TestDeclaredErrorTypes(t *testing.T) {
	reg := registry.New()
	reg.MustAddFunc(registry.Func{Name: "broken", Call: func(context.Context, *registry.FuncCall) (any, error) {
		return nil, &registry.Thrown{Type: "ForeignError", Fields: map[string]any{"message": "host gone", "code": int64(-32000)}}
	}})
	h := load(t, `import = ["std/types@1", "std/files@1", "std/json@1"]

file_error: func(List(Bool)) do {
  let r = attempt(read("nope.txt"))
  return [r.error is FileError, r.error is Error, r.error is DecodeError]
}

decode_error: func(List(Bool)) do {
  let r = attempt(json.decode(List(Int), "{"))
  return [r.error is DecodeError, r.error is Error]
}

parse_error: func(List(Bool)) do {
  let r = attempt(Int("three"))
  return [r.error is ParseError, r.error is Error, r.error is FileError]
}

broken: func(Any)

foreign_error: func(List(Any)) do {
  let r = attempt(broken())
  return [r.error is ForeignError, r.error is Error, r.error.code]
}
`, reg)
	want(t, h.mustCall("file_error", nil), []eval.Value{true, true, false})
	want(t, h.mustCall("decode_error", nil), []eval.Value{true, true})
	want(t, h.mustCall("parse_error", nil), []eval.Value{true, true, false})
	want(t, h.mustCall("foreign_error", nil), []eval.Value{true, true, int64(-32000)})
}

func TestProcessAndFileEdges(t *testing.T) {
	h := load(t, `import = ["std/schema@1", "std/process@1", "std/files@1"]

@insert<raw>
raw_script(cmd: String): func(Any) do {
  return run((sh)"$cmd")
}

script_args: func(Any) do {
  return run((sh)"echo $1", args = ["a b"])
}

missing_file: func(Any) do {
  let r = attempt(read("nope.txt"))
  return [r.error.path, r.error.message.size > 0]
}

temps: func(Bool) do {
  return temp("x.json") == temp("dir/x.json")
}

missing_program: func(Any) do {
  let r = attempt(run("nope"))
  return [r.error.exit, r.error.message]
}
`, nil)
	h.mustCall("raw_script", map[string]eval.Value{"cmd": "ls -la; pwd"})
	want(t, h.procs.runs[0].Args, []string{"-c", "ls -la; pwd"})
	h.mustCall("script_args", nil)
	want(t, h.procs.runs[1].Args, []string{"-c", "echo $1", "sh", "a b"})
	want(t, h.mustCall("missing_file", nil), []eval.Value{"nope.txt", true})
	want(t, h.mustCall("temps", nil), true)
	h.m.Caps.Processes = missingProcs{}
	want(t, h.mustCall("missing_program", nil), []eval.Value{int64(-1), `exec: "nope": executable file not found in $PATH`})
}
