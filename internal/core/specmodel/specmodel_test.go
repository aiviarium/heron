package specmodel_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/project"
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

const head = `import = ["std/doc@1", "std/types@1", "std/grammar@1", "std/apis@1", "std/http@1"]

self_service: goal {{ Customers fix a wrong charge themselves. }}

customer: actor {{ Someone who paid. }}

desk: part {{ The support console. }}

window: term {{ The 30 days after capture. }}
`

// Each core kind has its layer, from its own `layer` or the kind it
// extends; a kind with neither has none.
func TestLayers(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(desk) {
  must {{ refund }}
}

big_rule: kind(extends = rule)

g: grammar {
  p: production {{ x }}
}

e: error(422, id = "x") {
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
r: rule(desk, self_service) {
  must {{ refund inside the [window], as [customer] asks; see [e] }}
}

e: error(422, id = "x") {
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

// References name the kinds their kind's references list allows, a term
// gets its own message, a rule binds at most one subject and links no
// rule, the reference graph has no cycles and a revision is a whole number
// from 1.
func TestReferences(t *testing.T) {
	res := checkSrc(t, head+`
a: rule(desk, b, window) {
  must {{ x }}
}

@rev(0)
b: rule {
  must {{ y }}
}

two: rule(desk, customer) {
  must {{ z }}
}

linking: rule {
  must {{ do what [b](#b) says }}
}

asked: goal(desk) {{ A goal a part asked for. }}

p1: part {{ Works with [p2](#p2). }}

p2: part {{ Works with [p1](#p1). }}

c: command("x", window)
`)
	wantAll(t, res,
		"reference: b is a rule, and a rule references only goal, quality, actor, part, assumption or feature",
		"reference: window is a term, which is vocabulary: a rule doesn't reference it; mention it in the prose",
		"reference: window is a term, which is vocabulary: a command doesn't reference it; mention it in the prose",
		"subject: rule two binds desk and customer; a rule references at most one actor or part, its subject",
		"rule-link: rule linking links the rule b",
		"reference: desk is a part, and a goal references only actor or persona",
		"cycle: p1 references itself through p1 → p2 → p1",
		"type: a revision is a whole number from 1, not 0")
	wantNone(t, res, "rule-link: rule a")
}

// The constructor's references come after the kind's main positional
// parameters, and an op's first positional argument is its result type
// only when it names a type: each kind takes them, and the checker reports
// one that names a kind it doesn't take.
func TestConstructorReferences(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(desk) {
  must {{ refund }}
}

refund: usecase(customer) {
  | 1. The customer asks for a refund.
  | 2. The desk refunds it.
}

Refund: type {
  amount: Int
}

payments: api(r) {
  create(amount: Int): op(Refund, r, refund.2)
  ping(): op(r)
  named(): op(returns = Refund, r)
  wrong(): op(Refund, self_service)
}

c(amount: Int, @option quiet?: Bool): command("refund", r)

s: screen(route = "/refunds", r) {
  list: region(refund.2) {}
}

bare: screen(r)

g: grammar(r, notation = "ungrammar") {
  P: production(r) {{ x }}
  T: token(r) {{ "x" }}
}

uc: usecase(customer, self_service) {
  | 1. The customer asks.
}

ex_case: scenario(r) {
  given {{ a payment }}
  when {{ a refund is asked }}
  then {{ it's refunded }}
}
`)
	wantAll(t, res, "reference: self_service is a goal, and a op references only a rule, a use case or its step, a part, a lifecycle or a transition")
	wantNone(t, res, "type:", "too-many-arguments", "Refund is a", "r is a rule", "refund.2", "unresolved", "missing-argument")
	c := res.Checker
	for path, want := range map[string]int{"payments.create": 2, "payments.ping": 1, "payments.named": 1, "payments": 1, "c": 1, "s": 1, "s.list": 1, "bare": 1, "g": 1, "g.P": 1, "g.T": 1, "uc": 2, "ex_case": 1} {
		if got := len(project.References(c, res.Tree.Find(path))); got != want {
			t.Errorf("%s has %d references, want %d", path, got, want)
		}
	}
	if e := project.KindArg(c, res.Tree.Find("payments.ping"), "returns"); e != nil {
		t.Errorf("ping returns %s; a reference is no result type", e.Syntax().Text())
	}
	if e := project.KindArg(c, res.Tree.Find("payments.create"), "returns"); e == nil || e.Syntax().Text() != "Refund" {
		t.Errorf("create's result type isn't Refund")
	}
	if e := project.KindArg(c, res.Tree.Find("bare"), "route"); e != nil {
		t.Errorf("bare's route is %s; a reference is no route", e.Syntax().Text())
	}
}

// @touch writes hard references as an annotation, on any kind: it takes
// what the kind's constructor takes, a type's the kinds in spec.touchable,
// and a reference written in both is listed twice. @see, the soft
// reference, stands on any kind, names no term and points up as any
// reference does.
func TestTouchAndSee(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(desk) {
  must {{ refund }}
}

other: rule {
  must {{ log }}
}

@touch(self_service)
split: rule(desk) {
  must {{ x }}
}

@touch(other)
touching_rule: rule {
  must {{ y }}
}

@touch(desk)
twice: rule(desk) {
  must {{ z }}
}

@touch(r)
Refund: type {
  amount: Int
}

@touch(self_service, window)
Wrong: enum {
  A: Self
}

@see(customer, self_service)
seeing: rule {
  must {{ w }}
}

@see(other)
sees_rule: rule {
  must {{ v }}
}

@see(window)
sees_term: part {{ The ledger. }}

@see(e)
sees_down: rule {
  must {{ u }}
}

e: error(422, id = "x") {
  title = "bad"
}

payments: api {
  @touch(r)
  pay(amount: Int): op(Refund)
  @touch(r)
  again(amount: Int): op(Refund, r)
  @see(self_service)
  look(): op(Refund)
}
`)
	wantAll(t, res,
		"reference: other is a rule, and a rule references only goal, quality, actor, part, assumption or feature",
		"reference: desk is listed twice, in the constructor and in @touch; list it once",
		"reference: r is listed twice, in the constructor and in @touch; list it once",
		"reference: self_service is a goal, and @touch on a enum takes only a rule, a use case or its step, a part, a lifecycle or a transition",
		"reference: window is a term, which is vocabulary: a enum doesn't reference it; mention it in the prose",
		"reference: window is a term, which is vocabulary: @see doesn't reference it; mention it in the prose",
		"rule-link: rule sees_rule links the rule other",
		"layer: e is a error in the contract layer, and a specification node can't reference a lower layer")
	wantNone(t, res, "rule split", "split binds", "Refund", "pay", "look", "seeing", "customer")
	c := res.Checker
	if got := project.Subject(c, res.Tree.Find("split")); got == nil || got.Name != "desk" {
		t.Errorf("split's subject is %v, want desk", got)
	}
	if got := project.Served(c, res.Tree.Find("split")); len(got) != 1 || got[0].Name != "self_service" {
		t.Errorf("split serves %v, want self_service through @touch", got)
	}
	if got := project.HardTargets(c, res.Tree.Find("payments.pay")); len(got) != 1 || got[0].Name != "r" {
		t.Errorf("pay's hard references are %v, want r", got)
	}
	if got := project.HardTargets(c, res.Tree.Find("seeing")); len(got) != 0 {
		t.Errorf("@see counted as a hard reference: %v", got)
	}
	if got := project.SeeAlso(c, res.Tree.Find("seeing")); len(got) != 2 {
		t.Errorf("seeing sees %v, want customer and self_service", got)
	}
}

// A custom kind declares the kinds its references may name, or takes the
// list of the kind it extends.
func TestCustomReferenceKinds(t *testing.T) {
	res := checkSrc(t, `import = ["std/schema@1", "std/doc@1"]

desk: part {{ The support console. }}

b: rule {
  must {{ y }}
}

team_rule: kind(extends = rule, references = [actor])

plain_rule: kind(extends = rule)

t1: team_rule(desk) {
  must {{ v }}
}

t2: plain_rule(b) {
  must {{ u }}
}
`)
	wantAll(t, res,
		"reference: desk is a part, and a team_rule references only actor",
		"reference: b is a rule, and a plain_rule references only goal, quality, actor, part, assumption or feature")
	// A kind that extends another takes its parameters and its body.
	wantNone(t, res, "too-many-arguments", "accept:")
}

// A rule references no term; a persona
// plays an actor; a scenario has one form, an outcome scenario references
// one rule or path, a quality scenario has a measure and references the
// quality it measures; at most one trigger.
func TestShapes(t *testing.T) {
	res := checkSrc(t, head+`
r: rule(window) {
  when {{ a }}
  if {{ b }}
  must {{ x }}
}

p: persona(desk) {{ Someone. }}

both: scenario(r) {
  given {{ g }}
  measure =| fast
}

none: scenario

lost: scenario {
  given {{ g }}
}

q: quality {{ Quick. }}

unmeasured: scenario(q) {
  stimulus =| a
}

pointed: scenario(q, r) {
  stimulus =| a
  measure =| fast
}

unlinked: scenario {
  stimulus =| a
  measure =| fast
}

measured: scenario(q) {
  stimulus =| a
  measure =| fast
}
`)
	wantAll(t, res,
		"reference: window is a term, which is vocabulary: a rule doesn't reference it; mention it in the prose",
		"accept: rule accepts at most 1 trigger, when or if",
		"reference: desk is a part, and a persona references only actor",
		"scenario: scenario both mixes outcome clauses and quality fields",
		"scenario: scenario none has neither outcome clauses nor quality fields",
		"scenario: scenario lost references no rule or path of a use case",
		"scenario: scenario unmeasured has no measure",
		"scenario: scenario pointed references something other than the quality it measures",
		"scenario: scenario unlinked references no quality it measures")
	wantNone(t, res, "scenario measured")
}

// A use case's body is its main path and its extensions; steps open with
// an actor or a part, an include is at a lower level, extensions branch
// off steps that exist and end with one outcome; steps resolve as paths.
func TestUseCases(t *testing.T) {
	res := checkSrc(t, head+`
confirm: usecase<subfunction>(customer) {{
  1. [customer] enters the code.
}}

big: usecase<subfunction>(customer) {{
  1. [confirm]
}}

nobody: usecase(self_service) {{
  1. [desk] does it.
}}

buy: usecase(customer) {{
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

late: scenario(buy.2a) {
  given {{ an old payment }}
  when {{ the customer asks }}
  then {{ credit is offered }}
}

wrong: scenario(buy.7) {
  given {{ a }}
}
`)
	wantAll(t, res,
		"usecase: step 2 opens with window, a term; a step's first link names an actor or a part",
		"usecase: step 1 includes confirm, which is at level subfunction",
		"usecase: use case nobody references no actor; its first actor reference is its primary actor",
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
e: error(422, id = "x") {
  title = "a {b} and {c}"
  b: String
  d: String
}
`)
	wantAll(t, res,
		"placeholder: {c} in the title of e is no member of it; declare c with its type, as c: String",
		"placeholder: member d of e appears in neither its title nor its related message")
}

// A story lists spec items; it states no behaviour of its own.
func TestStoryStatesNoBehaviour(t *testing.T) {
	res := checkSrc(t, `import = ["std/doc@1", "std/project@1"]

s: story {
  given {{ a cart }}
}
`)
	wantAll(t, res, "story: story s holds a given; a story lists the spec items it adds or changes")
}

// A use case's actors are the actors it references: the first its primary
// actor, any later one a secondary actor. Its level is its level flag, a
// user goal without one, and its guarantees are its success and minimal
// children.
func TestUseCaseActors(t *testing.T) {
	res := checkSrc(t, head+`
bank: actor<system> {{ The card network. }}

pay: usecase<summary>(self_service, customer, bank) {
  | 1. [customer] pays.
  | 2. [bank] settles.

  success {{ The payment is settled. }}
  minimal {{ No money moves. }}
}

plain: usecase(customer) {{
  1. [customer] looks.
}}
`)
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	c := res.Checker
	var names []string
	for _, a := range specmodel.UseCaseActors(c, res.Tree.Find("pay")) {
		names = append(names, a.Name)
	}
	if strings.Join(names, " ") != "customer bank" {
		t.Errorf("pay's actors %v, want its primary actor customer, then bank", names)
	}
	if l := c.GroupFlag(res.Tree.Find("pay"), "level"); l != "summary" {
		t.Errorf("pay's level %q", l)
	}
	if l := c.GroupFlag(res.Tree.Find("plain"), "level"); l != "" {
		t.Errorf("plain carries no level flag, got %q", l)
	}
	if r := c.GroupFlag(res.Tree.Find("bank"), "role"); r != "system" {
		t.Errorf("bank's role %q", r)
	}
	pay := res.Tree.Find("pay")
	for _, g := range []string{"success", "minimal"} {
		found := false
		for _, ch := range pay.Children {
			found = found || specmodel.Is(c, ch, "spec."+g)
		}
		if !found {
			t.Errorf("pay has no %s guarantee", g)
		}
	}
}
