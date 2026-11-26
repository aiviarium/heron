package specmodel_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/specmodel"
)

func checkSrc(t *testing.T, src string) *check.Result {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := resolve.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	return check.NewSession().Check(id)
}

func messages(res *check.Result) string {
	var out []string
	for _, d := range res.Diags {
		out = append(out, d.Code+": "+d.Message)
	}
	return strings.Join(out, "\n")
}

func wantAll(t *testing.T, res *check.Result, wants ...string) {
	t.Helper()
	got := messages(res)
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("no %q in\n%s", w, got)
		}
	}
}

func wantNone(t *testing.T, res *check.Result, nots ...string) {
	t.Helper()
	got := messages(res)
	for _, w := range nots {
		if strings.Contains(got, w) {
			t.Errorf("%q in\n%s", w, got)
		}
	}
}

const head = `import = ["std/doc@1", "std/types@1", "std/spec@1", "std/grammar@1", "std/apis@1", "std/http@1"]

self_service: goal {{ Customers fix a wrong charge themselves. }}

customer: actor {{ Someone who paid. }}

desk: part {{ The support console. }}

window: term {{ The 30 days after capture. }}
`

// Each core kind has its layer, from its own `layer` or the kind it
// extends; a kind with neither has none.
func TestLayers(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(by = desk) {
  must {{ refund }}
}

big_rule: kind(extends = rule)

g: grammar {
  p: production {{ x }}
}

e: error("x", by = desk) {
  title = "bad"
}
`)
	c := res.Checker
	for path, want := range map[string]int{"self_service": specmodel.Intent, "customer": specmodel.Intent, "desk": specmodel.Specification,
		"window": specmodel.Specification, "r": specmodel.Specification, "g": specmodel.Contract, "g.p": specmodel.Contract, "e": specmodel.Contract} {
		if got := specmodel.LayerOf(c, res.Tree.Find(path)); got != want {
			t.Errorf("%s is in layer %d, want %d", path, got, want)
		}
	}
	if got := specmodel.KindLayer(c, res.Tree.Find("big_rule")); got != specmodel.Specification {
		t.Errorf("a kind that extends rule is in layer %d", got)
	}
	if got := specmodel.KindLayer(c, res.Tree.Find("self_service").Parent); got != specmodel.None {
		t.Errorf("the root is in layer %d", got)
	}
}

// A reference from a higher layer to a lower one is an error at it; one
// within a layer, up, or from or to a node with no layer is fine.
func TestReferencesPointUp(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(by = desk, serves = [self_service]) {
  must {{ refund inside the [window], as [customer] asks; see [e] }}
}

e: error("x", by = desk) {
  title = "bad"
}

loud: goal {{ Fix it, as [r] says. }}

notes: section {
  | See [e] and [r].
}
`)
	wantAll(t, res,
		"layer: e is a error in the contract layer, and a specification node can't reference a lower layer",
		"layer: r is a rule in the specification layer, and an intent node can't reference a lower layer")
	wantNone(t, res, "window is a term", "customer is a actor", "self_service is a goal", "notes")
}

// Links name the kinds their table allows; derives has no cycles; a
// revision is a whole number from 1; @realizes names rules, use cases,
// steps or parts.
func TestLinks(t *testing.T) {
	res := checkSrc(t, head+`
a: rule(serves = [desk], derives = [b]) {
  must {{ x }}
}

b: rule(derives = [a], rev = 0) {
  must {{ y }}
}

@realizes(window)
e: error("x") {
  title = "bad"
}
`)
	wantAll(t, res,
		"link: serves takes a goal or a quality, and desk is a part",
		"cycle: a derives from itself",
		"type: a revision is a whole number from 1, not 0",
		"link: @realizes takes rules, use cases, steps, extensions or parts, and window is a term")
}

// A rule's by names an actor or a part; a constraint needs its reason; a
// persona plays an actor; a scenario has one form, an outcome scenario
// illustrates one rule or path, a quality scenario has a measure and
// serves a quality; at most one trigger.
func TestShapes(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(by = window) {
  when {{ a }}
  if {{ b }}
  must {{ x }}
}

c: constraint {
  must {{ y }}
}

p: persona(plays = desk) {{ Someone. }}

both: scenario(illustrates = r) {
  given {{ g }}
  measure =| fast
}

none: scenario

lost: scenario {
  given {{ g }}
}

q: quality {{ Quick. }}

unmeasured: scenario(serves = [q]) {
  stimulus =| a
}
`)
	wantAll(t, res,
		"type: window is a term; a rule's by names an actor or a part",
		"accept: rule accepts at most 1 trigger, when or if",
		"missing-argument: constraint c needs a reason from outside the solution",
		"type: desk is a part, not an actor",
		"scenario: scenario both mixes outcome clauses and quality fields",
		"scenario: scenario none has neither outcome clauses nor quality fields",
		"scenario: scenario lost illustrates nothing",
		"scenario: scenario unmeasured has no measure")
}

// A use case's body is its main path and its extensions; steps open with
// an actor or a part, an include is at a lower level, extensions branch
// off steps that exist and end with one outcome; steps resolve as paths.
func TestUseCases(t *testing.T) {
	res := checkSrc(t, head+`
confirm: usecase(actor = customer, level = UseCaseLevel.SUBFUNCTION) {{
  1. [customer] enters the code.
}}

big: usecase(actor = customer, level = UseCaseLevel.SUBFUNCTION) {{
  1. [confirm]
}}

buy: usecase(actor = customer) {{
  A customer asks for a refund.

  1. [customer] picks the payment.
  2. [window] checks it.
  3. [confirm]

  - 2a. Late:
    [desk] offers credit. Fails.
  - 5a. Nope:
    [desk] does it. Back to 9.
  - 3a. Odd:
    [desk] does it.
}}

late: scenario(illustrates = buy.2a) {
  given {{ an old payment }}
  when {{ the customer asks }}
  then {{ credit is offered }}
}

wrong: scenario(illustrates = buy.7) {
  given {{ a }}
}
`)
	wantAll(t, res,
		"usecase: step 2 opens with window, a term; a step's first link names an actor or a part",
		"usecase: step 1 includes confirm, which is at level SUBFUNCTION",
		"usecase: use case buy has no step 5",
		"usecase: use case buy has no step 9",
		`usecase: extension 3a ends with "[desk] does it."`,
		"usecase: use case buy has no step 7")
	wantNone(t, res, "late", "step 3 includes")
	if st := res.Tree.Find("buy.2a"); st == nil || st.Shape != resolve.ShapeStep {
		t.Errorf("buy.2a is %v", st)
	}
	// A step's site is its own range of the body, so the outline and go
	// to definition land on it.
	if st := res.Tree.Find("buy.2"); st != nil {
		s, e := st.Decl.Span()
		if txt := st.Decl.File.Src[s:e]; !strings.Contains(txt, "checks it") || strings.Contains(txt, "picks") {
			t.Errorf("step 2's site is %q", txt)
		}
	}
}

// Every {placeholder} of an error's title is a field, and every field
// appears in the title or the related message.
func TestErrorPlaceholders(t *testing.T) {
	res := checkSrc(t, head+`
e: error("x") {
  title = "a {b} and {c}"
  b: field(String)
  d: field(String)
}
`)
	wantAll(t, res,
		"placeholder: {c} in the title of e is no field of it; declare c: field(…)",
		"placeholder: field d of e appears in neither its title nor its related message")
}

// A retired kind or package is an error naming what to write instead,
// with the fix where the change is mechanical.
func TestRetired(t *testing.T) {
	res := checkSrc(t, `import = ["std/doc@1", "std/spec@1", "std/tech@1"]

old: invariant {{ x }}
c: criterion {
  given {{ g }}
}
r: rule {
  must {{ x }}
  unless {{ y }}
}
`)
	wantAll(t, res,
		"retired: std/tech is merged into std/spec",
		"retired: invariant is retired; write a rule with no condition",
		"retired: criterion is retired; write a scenario",
		"retired: unless is retired; write while")
	fixes := map[string]bool{}
	for _, d := range res.Diags {
		for _, f := range d.Fixes {
			fixes[f.Title] = true
		}
	}
	for _, w := range []string{"Write rule instead of invariant", "Write scenario instead of criterion", "Import std/spec@1"} {
		if !fixes[w] {
			t.Errorf("no fix %q in %v", w, fixes)
		}
	}
}

// A story lists spec items; it states no behaviour of its own.
func TestStoryStatesNoBehaviour(t *testing.T) {
	res := checkSrc(t, `import = ["std/doc@1", "std/spec@1", "std/project@1"]

s: story {
  given {{ a cart }}
}
`)
	wantAll(t, res, "story: story s holds a given; a story lists the spec items it adds or changes")
}
