package specmodel

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
	"github.com/aiviarium/heron/internal/core/usecase"
)

// Parsed is a use case's body with the links of its text resolved.
type Parsed struct {
	usecase.UseCase
	Site  resolve.Site
	links map[int]*resolve.Node // a short link's start → its target
}

// Target is the node the short link starting at an offset names.
func (p Parsed) Target(at int) *resolve.Node { return p.links[at] }

// Links are the targets of the short links between two offsets.
func (p Parsed) Links(start, end int) []*resolve.Node {
	var out []*resolve.Node
	for at, t := range p.links {
		if at >= start && at < end && t != nil {
			out = append(out, t)
		}
	}
	return out
}

// ParseUseCase reads a use case's body.
func ParseUseCase(c *types.Checker, n *resolve.Node) (Parsed, bool) {
	for _, p := range n.Prose {
		if p.N == nil || p.File == nil {
			continue
		}
		s, e := p.N.Span()
		out := Parsed{UseCase: usecase.Parse(p.File.Src[s:e], s), Site: p, links: map[int]*resolve.Node{}}
		p.N.Walk(func(x *syntax.Node) bool {
			if x.Kind == syntax.ShortLink {
				st, _ := x.Span()
				x.Walk(func(y *syntax.Node) bool {
					if y.Kind == syntax.Path {
						out.links[st] = c.Refs[y]
						return false
					}
					return true
				})
				return false
			}
			return true
		})
		return out, true
	}
	return Parsed{}, false
}

// level is a use case's goal level, from its level flag: 0 for summary, 1
// for user, the level of one without a flag, and 2 for subfunction.
func level(c *types.Checker, n *resolve.Node) int {
	switch c.GroupFlag(n, "level") {
	case "summary":
		return 0
	case "subfunction":
		return 2
	}
	return 1
}

// UseCaseActors are the actors a use case references, in the order
// written: the first is its primary actor, any later one a secondary
// actor.
func UseCaseActors(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, r := range nodeRefs(c, n) {
		if r.Target != nil && Is(c, r.Target, "spec.actor") {
			out = append(out, r.Target)
		}
	}
	return out
}

// UseCaseChecks reports what a use case's notation breaks.
func UseCaseChecks(t *resolve.Tree, c *types.Checker, n *resolve.Node, ds *diag.List) {
	p, ok := ParseUseCase(c, n)
	if !ok {
		return
	}
	step := func(st usecase.Step) {
		lead := strings.Index(p.Site.File.Src[st.Start:st.End], "[")
		if lead != 0 {
			return
		}
		target := p.Target(st.Start)
		if target == nil {
			return
		}
		if inc := usecase.Include(st.Text); inc != "" && Is(c, target, "spec.usecase") {
			if level(c, target) <= level(c, n) || reaches(c, target, n, map[*resolve.Node]bool{}) {
				ds.Errorf(string(p.Site.File.ID), st.Start, st.End, "usecase", "step %s includes %s, which is at level %s; an include is at a lower level, with no cycle", st.Num, target.Name, levelName(level(c, target)))
			}
			return
		}
		if !Is(c, target, "spec.actor", "spec.part", "spec.persona") {
			ds.Errorf(string(p.Site.File.ID), st.Start, st.End, "usecase", "step %s opens with %s, a %s; a step's first link names an actor or a part", st.Num, target.Name, KindName(c, target))
		}
	}
	for _, st := range p.Steps {
		step(st)
	}
	for _, x := range p.Exts {
		if x.At < 1 || x.At > len(p.Steps) {
			ds.Errorf(string(p.Site.File.ID), x.Start, x.End, "usecase", "use case %s has no step %d", n.Name, x.At)
		}
		for _, st := range x.Steps {
			step(st)
		}
		switch {
		case x.Outcome == usecase.NoOutcome:
			o, e := x.Start, x.End
			if x.OutcomeAt >= 0 {
				o = x.OutcomeAt
			}
			ds.Errorf(string(p.Site.File.ID), o, e, "usecase", "extension %s ends with %s; it ends with exactly one of \"Fails.\", \"Back to N.\" or \"Succeeds.\"", x.Num, strconv.Quote(x.Last))
		case x.Outcome == usecase.BackTo && (x.BackTo < 1 || x.BackTo > len(p.Steps)):
			ds.Errorf(string(p.Site.File.ID), x.OutcomeAt, x.End, "usecase", "use case %s has no step %d", n.Name, x.BackTo)
		}
	}
}

func levelName(l int) string { return []string{"summary", "user", "subfunction"}[l] }

// reaches reports whether use case from includes target, directly or not.
func reaches(c *types.Checker, from, target *resolve.Node, seen map[*resolve.Node]bool) bool {
	if seen[from] {
		return false
	}
	seen[from] = true
	p, ok := ParseUseCase(c, from)
	if !ok {
		return false
	}
	for _, st := range p.Steps {
		if usecase.Include(st.Text) == "" {
			continue
		}
		t := p.Target(st.Start)
		if t == target || (t != nil && Is(c, t, "spec.usecase") && reaches(c, t, target, seen)) {
			return true
		}
	}
	return false
}
