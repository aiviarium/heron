// Package lint runs the lints registered in the registry over a checked
// tree: the core lints in this package, and those other units add. A
// finding on a node is dropped when the node or one around it says
// @lint(allow = ["name"]).
package lint

import (
	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Input is what a lint reads, and how it reports.
type Input struct {
	Tree    *resolve.Tree
	Checker *types.Checker
	name    string
	out     *diag.List
}

// Report adds a finding about a node, unless the node allows the lint.
func (in *Input) Report(n *resolve.Node, at resolve.Site, sev diag.Severity, msg string) {
	if allowed(in.Checker, n, in.name) || at.File == nil {
		return
	}
	s, e := at.Span()
	in.out.Add(diag.Diagnostic{File: string(at.File.ID), Start: s, End: e, Severity: sev, Code: in.name, Message: msg})
}

// ReportFix adds a finding at a byte range with fixes; `heron lint --fix` applies the first.
func (in *Input) ReportFix(n *resolve.Node, f *resolve.File, start, end int, sev diag.Severity, msg string, fixes []diag.Fix) {
	if allowed(in.Checker, n, in.name) || f == nil {
		return
	}
	in.out.Add(diag.Diagnostic{File: string(f.ID), Start: start, End: end, Severity: sev, Code: in.name, Message: msg, Fixes: fixes})
}

// As returns an input that reports under the code name, such as a prose
// provider's "Name.Rule" for a finding about the tree rather than a region.
func (in *Input) As(name string) *Input {
	cp := *in
	cp.name = name
	return &cp
}

// ReportAt adds a finding at a byte range, such as one flag of a list.
func (in *Input) ReportAt(n *resolve.Node, f *resolve.File, start, end int, sev diag.Severity, msg string) {
	if allowed(in.Checker, n, in.name) || f == nil {
		return
	}
	in.out.Add(diag.Diagnostic{File: string(f.ID), Start: start, End: end, Severity: sev, Code: in.name, Message: msg})
}

// Run runs every registered lint and prose provider over a checked tree,
// keeping prose results in a cache that lives as long as the process.
func Run(r *registry.Registry, res *check.Result) diag.List {
	return RunWith(r, res, defaultProse)
}

// allowed reports whether a node or an ancestor carries
// @lint(allow = [… name …]).
func allowed(c *types.Checker, n *resolve.Node, name string) bool {
	for x := n; x != nil; x = x.Parent {
		for _, a := range append(append([]resolve.Site(nil), x.Annotations...), x.GroupAnnotations...) {
			if c.Refs[childPath(a.N)] == nil || !types.IsStd(c.Refs[childPath(a.N)], "core", "core.lint") {
				continue
			}
			found := false
			a.N.Walk(func(m *syntax.Node) bool {
				if m.Kind == syntax.StringLit {
					if s, ok := resolve.StringValue(m); ok && s == name {
						found = true
					}
				}
				return true
			})
			if found {
				return true
			}
		}
	}
	return false
}

func childPath(n *syntax.Node) *syntax.Node {
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == syntax.Path {
			return c
		}
	}
	return nil
}
