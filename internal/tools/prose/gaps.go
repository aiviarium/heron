package prose

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint"
	coreprose "github.com/aiviarium/heron/internal/core/lint/prose"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/specmodel"
	"github.com/aiviarium/heron/internal/core/usecase"
)

// The words the layer lints look for, outside code, links and quotes.
var (
	designWords   = []string{"database", "table", "class", "microservice", "endpoint", "column", "schema migration"}
	deliveryWords = []string{"sprint", "milestone", "milestones", "increment", "increments", "story points", "backlog", "deadline"}
	scriptWords   = []string{"click", "clicks", "press", "presses", "type", "types", "tap", "taps", "field", "button"}
	clauseEnds    = regexp.MustCompile(`[,;]|\s(and|or)\s`)
	negations     = regexp.MustCompile(`(?i)\b(not|no|never|none|nothing|nobody|neither|cannot)\b|n't\b`)
)

// layerWords are the prose lints that read a region by the layer and kind
// of the node it belongs to: design and delivery words, scripted
// scenarios, negated conditions, compound rules and bare negations.
func (f *specFacts) layerWords(i int, r *registry.ProseRegion, chain []*resolve.Node, ws []word) []registry.ProseFinding {
	var out []registry.ProseFinding
	add := func(s, e int, rule, msg string) {
		out = append(out, registry.ProseFinding{Region: i, Start: s, End: e, Rule: rule, Severity: diag.Warning, Message: msg})
	}
	if r.Block == "code" || r.Block == "heading" {
		return nil
	}
	layer := specmodel.None
	var owner *resolve.Node
	for _, n := range chain {
		if l := specmodel.LayerOf(f.c, n); l != specmodel.None {
			layer, owner = l, n
			break
		}
	}
	if layer == specmodel.None {
		return nil
	}
	used := func(w word) bool { return !coreprose.Masked(r, w.s, w.e) && !quoted(r.Text, w.s, w.e) }
	phrase := func(list []string, rule, format string) {
		lower := strings.ToLower(r.Text)
		for _, p := range list {
			for _, m := range findPhrase(lower, ws, p) {
				if coreprose.Masked(r, m[0], m[1]) || quoted(r.Text, m[0], m[1]) {
					continue
				}
				add(m[0], m[1], rule, fmt.Sprintf(format, r.Text[m[0]:m[1]], strings.ToLower(specmodel.LayerNames[layer])))
			}
		}
	}
	if layer != specmodel.Contract && !specmodel.Is(f.c, owner, "spec.constraint") {
		// A word the tree declares as a term is its domain's, not design.
		var words []string
		for _, w := range designWords {
			domain := false
			for _, t := range f.terms {
				for _, p := range t.phrases {
					domain = domain || p == w || p == w+"s"
				}
			}
			if !domain {
				words = append(words, w, w+"s")
			}
		}
		phrase(words, "DesignWord", "%q is a design word in the %s layer; say what's observable, or write a constraint with its reason")
	}
	phrase(deliveryWords, "DeliveryWord", "%q is delivery vocabulary%.0s; plan work in the project, not the spec")
	kindOf := func(n *resolve.Node) string { return project.KindPath(f.c, n) }
	first := chain[0]
	if specmodel.Is(f.c, owner, "spec.scenario") && first != owner {
		phrase(scriptWords, "ScriptedScenario", "%q scripts a screen%.0s; say what happens, not how it's driven")
	}
	// Conditions: no "not the case that" and no double negation.
	switch kindOf(first) {
	case "spec.while", "spec.where", "spec.when", "spec.if":
		if i := strings.Index(strings.ToLower(r.Text), "not the case that"); i >= 0 && !coreprose.Masked(r, i, i+17) && !quoted(r.Text, i, i+17) {
			add(i, i+len("not the case that"), "NegatedCondition", fmt.Sprintf("%q negates the condition; say the state the rule applies in, or write the exception as a rule of its own", r.Text[i:i+len("not the case that")]))
		} else {
			// Two negations in one clause: a clause ends at a comma, a
			// semicolon, or "and" or "or" between words.
			start := 0
			for _, end := range append(clauseEnds.FindAllStringIndex(r.Text, -1), []int{len(r.Text), len(r.Text)}) {
				var ms [][]int
				for _, m := range negations.FindAllStringIndex(r.Text[start:end[0]], -1) {
					a, b := start+m[0], start+m[1]
					if !coreprose.Masked(r, a, b) && !quoted(r.Text, a, b) {
						ms = append(ms, []int{a, b})
					}
				}
				if len(ms) >= 2 {
					a, b := ms[0][0], ms[len(ms)-1][1]
					add(a, b, "NegatedCondition", fmt.Sprintf("%q negates the condition twice; say the state the rule applies in", r.Text[a:b]))
					break
				}
				start = end[1]
			}
		}
	case "spec.must", "spec.should", "spec.may", "spec.mustNot", "spec.shouldNot":
		lower := strings.ToLower(r.Text)
		if i := strings.Index(lower, "and/or"); i >= 0 {
			add(i, i+len("and/or"), "CompoundRule", `this rule joins two behaviours with "and/or"; split it into two rules`)
		}
		if k := kindOf(first); (k == "spec.mustNot" || k == "spec.shouldNot") && owner != nil && specmodel.Is(f.c, owner, "spec.rule") {
			cond := false
			for _, ch := range owner.Children {
				switch kindOf(ch) {
				case "spec.while", "spec.where", "spec.when", "spec.if", "spec.instead":
					cond = true
				}
			}
			if !cond {
				for _, w := range ws {
					if lw := strings.ToLower(w.text); (lw == "never" || lw == "not") && used(w) {
						add(w.s, w.e, "BareNegation", fmt.Sprintf("%q forbids without saying when; give a condition or what to do instead", w.text))
						break
					}
				}
			}
		}
	}
	return out
}

// reportGaps reports the spec's own gaps: goals and qualities nothing
// serves, requirements that serve no goal, entries and errors that realize
// nothing, failing extensions no error realizes, qualities with no
// measure, and long scenarios.
func reportGaps(li *lint.Input) {
	t, c := li.Tree, li.Checker
	is := func(n *resolve.Node, kinds ...string) bool { return specmodel.Is(c, n, kinds...) }
	refs := func(n *resolve.Node, name string) []*resolve.Node { return project.AnnotationRefs(c, n, name) }
	// The goals a section declares, for the requirements below it.
	areaGoals := func(n *resolve.Node) []*resolve.Node {
		for a := n.Parent; a != nil; a = a.Parent {
			var gs []*resolve.Node
			for _, ch := range a.Children {
				if is(ch, "spec.goal") {
					gs = append(gs, ch)
				}
			}
			if len(gs) > 0 {
				return gs
			}
		}
		return nil
	}
	served := map[*resolve.Node]bool{}
	realized := map[*resolve.Node]bool{}
	var goals, reqs, entries, usecases, qualities, scenarios []*resolve.Node
	project.Own(t, func(n *resolve.Node) bool {
		switch {
		case is(n, "spec.goal"):
			goals = append(goals, n)
		case is(n, "spec.quality"):
			goals = append(goals, n)
			qualities = append(qualities, n)
		case is(n, "spec.rule", "spec.constraint", "spec.usecase"):
			reqs = append(reqs, n)
			if is(n, "spec.usecase") {
				usecases = append(usecases, n)
			}
		case is(n, "spec.scenario"):
			scenarios = append(scenarios, n)
		case is(n, "spec.error", "spec.command", "spec.screen") || is(n, "types.op"):
			entries = append(entries, n)
		}
		for _, a := range n.Annotations {
			an := ast.Annotation{N: a.N}
			if an.N == nil || an.Path().N == nil {
				continue
			}
			if segs := resolve.PathSegs(an.Path().N); len(segs) == 0 || segs[len(segs)-1] != "realizes" {
				continue
			}
			for _, e := range project.ArgExprs(an.Args()) {
				if x := project.RefTarget(c, e.Syntax(), a); x != nil {
					realized[x] = true
					realized[n] = true // n realizes something
					if x.Parent != nil && x.Shape == resolve.ShapeStep {
						realized[x.Parent] = true
					}
				}
			}
		}
		return true
	})
	motivated := map[*resolve.Node]bool{}
	for _, r := range append(append([]*resolve.Node(nil), reqs...), scenarios...) {
		s := refs(r, "serves")
		if len(s) == 0 && !is(r, "spec.scenario") {
			s = areaGoals(r)
		}
		for _, g := range s {
			served[g] = true
		}
		if len(s) > 0 {
			motivated[r] = true
		}
	}
	// A rule derived from a motivated rule is motivated.
	for changed := true; changed; {
		changed = false
		for _, r := range reqs {
			if motivated[r] {
				continue
			}
			for _, d := range refs(r, "derives") {
				if motivated[d] {
					motivated[r], changed = true, true
				}
			}
		}
	}
	for _, g := range goals {
		if !served[g] {
			li.As(SpecName+".UnservedGoal").Report(g, g.Decl, diag.Warning, fmt.Sprintf("nothing serves %s; serve it from a rule, a use case or a scenario, or drop it", g.Name))
		}
	}
	for _, r := range reqs {
		if !motivated[r] && !is(r, "spec.constraint") {
			li.As(SpecName+".Unmotivated").Report(r, r.Decl, diag.Warning, fmt.Sprintf("%s serves no goal; give it @serves or @derives, or make it a constraint with its reason", r.Name))
		}
	}
	for _, e := range entries {
		if realized[e] {
			continue
		}
		// Its surface: the outermost contract node around it, and every
		// entry in that surface.
		surface := e
		for a := e.Parent; a != nil && specmodel.LayerOf(c, a) == specmodel.Contract; a = a.Parent {
			surface = a
		}
		surfaceRealizes := false
		var walk func(n *resolve.Node)
		walk = func(n *resolve.Node) {
			surfaceRealizes = surfaceRealizes || realized[n]
			for _, ch := range n.Children {
				walk(ch)
			}
		}
		if surface != e {
			walk(surface)
		}
		for _, ch := range e.Children {
			surfaceRealizes = surfaceRealizes || realized[ch]
		}
		if !surfaceRealizes {
			li.As(SpecName+".OrphanEntry").Report(e, e.Decl, diag.Warning, fmt.Sprintf("%s realizes nothing; name the rules or steps it exists for with @realizes", e.Name))
		}
	}
	for _, u := range usecases {
		p, ok := specmodel.ParseUseCase(c, u)
		if !ok {
			continue
		}
		for _, x := range p.Exts {
			if x.Outcome != usecase.Fails {
				continue
			}
			if st := u.Child(x.Num); (st != nil && realized[st]) || realized[u] {
				continue
			}
			li.As(SpecName+".UnhandledFailure").ReportAt(u, p.Site.File, x.Start, x.End, diag.Warning, fmt.Sprintf("extension %s fails, and no error realizes it", x.Num))
		}
	}
	for _, q := range qualities {
		measured := project.NamedArg(project.KindArgs(q), "measure") != nil
		for _, sc := range scenarios {
			for _, g := range refs(sc, "serves") {
				measured = measured || g == q
			}
		}
		if !measured {
			li.As(SpecName+".MeasureMissing").Report(q, q.Decl, diag.Warning, fmt.Sprintf("quality %s has no measure and no quality scenario", q.Name))
		}
	}
	for _, sc := range scenarios {
		n := 0
		for _, ch := range sc.Children {
			if is(ch, "spec.given", "spec.when", "spec.then") {
				n++
			}
		}
		if n > 5 {
			li.As(SpecName+".LongScenario").Report(sc, sc.Decl, diag.Warning, fmt.Sprintf("scenario %s has %d clauses; keep it to about five, or split the rule", sc.Name, n))
		}
	}
}
