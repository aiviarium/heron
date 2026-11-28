package project

import (
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// Contract coverage: of the rules about the boundary, those whose subject
// is a part or the system, how many a contract entry carries out. It's a
// figure for reading and fails nothing.

// CarriedOut is the set of nodes a contract entry carries out: those an
// entry implements, and the rules and steps an error's prose links.
func CarriedOut(t *resolve.Tree, c *types.Checker) map[*resolve.Node]bool {
	out := map[*resolve.Node]bool{}
	t.Walk(func(n *resolve.Node) {
		for _, x := range Carried(c, n) {
			out[x] = true
		}
		// An error's prose links the rules that say when it's reported.
		if KindPath(c, n) == "types.error" {
			for _, x := range ErrorRules(c, n) {
				out[x] = true
			}
		}
	})
	return out
}

// AboutBoundary reports whether a node is a rule contract coverage
// counts: one whose subject is a part, or the system when it names no
// actor, persona or part.
func AboutBoundary(c *types.Checker, n *resolve.Node) bool {
	if KindPath(c, n) != "spec.rule" {
		return false
	}
	s := Subject(c, n)
	return s == nil || KindPath(c, s) == "spec.part"
}

// ContractCoverage counts, among the nodes given, the rules about the
// boundary, all, and those a contract entry carries out, carried, by the
// set CarriedOut returns.
func ContractCoverage(c *types.Checker, nodes []*resolve.Node, carried map[*resolve.Node]bool) (done, all int) {
	for _, n := range nodes {
		if !AboutBoundary(c, n) {
			continue
		}
		all++
		if carried[n] {
			done++
		}
	}
	return done, all
}
