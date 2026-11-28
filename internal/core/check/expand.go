package check

import (
	"context"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// expandMacros expands the invocations among invs that call a macro, for
// the resolver to add to the bodies that invoke them. Expansion is
// evaluation: the machine has no processes, files, environment or clock,
// so nothing a macro does reaches outside the tree.
func expandMacros(t *resolve.Tree, invs []*resolve.Node) []resolve.Expanded {
	c := types.New(t)
	var m *eval.Machine
	var out []resolve.Expanded
	for _, inv := range invs {
		macro := c.Macro(inv)
		if macro == nil {
			continue
		}
		if m == nil {
			m = eval.New(t, c, nil, eval.Caps{})
		}
		src, err := m.ExpandAt(context.Background(), macro, inv.Decl, ast.Invoke{N: inv.Decl.N}.Args())
		out = append(out, resolve.Expanded{Inv: inv, Macro: macro, Src: src, Err: err})
	}
	return out
}
