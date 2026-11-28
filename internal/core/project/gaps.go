package project

import (
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
	"github.com/aiviarium/heron/internal/core/usecase"
)

// Unrealized is the gap report's list of the behaviour no contract entry
// implements and no error's prose links, among the tree's own rules whose
// subject is a part or the system and the use case steps a part takes, by
// path, in tree order. It's for reading and fails nothing.
func Unrealized(t *resolve.Tree, c *types.Checker) []string {
	realized := map[*resolve.Node]bool{}
	t.Walk(func(n *resolve.Node) {
		for _, x := range Carried(c, n) {
			realized[x] = true
		}
		// An error's prose links the rules that say when it's reported.
		if KindPath(c, n) == "types.error" {
			for _, x := range ErrorRules(c, n) {
				realized[x] = true
			}
		}
	})
	isPart := func(n *resolve.Node) bool { return n != nil && KindPath(c, n) == "spec.part" }
	var out []string
	Own(t, func(n *resolve.Node) bool {
		switch KindPath(c, n) {
		case "spec.rule":
			if s := Subject(c, n); s != nil && !isPart(s) {
				return true
			}
			if !realized[n] {
				out = append(out, n.Path())
			}
		case "spec.usecase":
			for _, ps := range n.Prose {
				if ps.N == nil || ps.File == nil {
					continue
				}
				leads := map[int]*resolve.Node{}
				ps.N.Walk(func(x *syntax.Node) bool {
					if x.Kind == syntax.ShortLink {
						st, _ := x.Span()
						x.Walk(func(y *syntax.Node) bool {
							if y.Kind == syntax.Path {
								leads[st] = c.Refs[y]
								return false
							}
							return true
						})
						return false
					}
					return true
				})
				st, en := ps.N.Span()
				uc := usecase.Parse(ps.File.Src[st:en], st)
				for _, step := range uc.Steps {
					if isPart(leads[step.Start]) && !realized[n.Child(step.Num)] && !realized[n] {
						out = append(out, n.Path()+"."+step.Num)
					}
				}
			}
		}
		return true
	})
	return out
}
