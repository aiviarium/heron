package specmodel

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// CommandParam is one input of a command, read from its parameter: a
// positional argument, or an option where `@option` marks it.
type CommandParam struct {
	Name string // the parameter's name, as dry_run
	// Display is how the usage line shows an argument: its @display, or
	// its name with each _ read as a space.
	Display  string
	Type     string // the type as written, as Bool
	Default  string // the default as written, or ""
	Optional bool   // `?` or a default
	Variadic bool
	Option   bool
	// Flag is the option's name, as --dry-run; Short, Env and Value are
	// its @option's short form, environment variable and value word.
	Flag, Short, Env, Value string
	Param                   ast.Param
	Site                    resolve.Site
}

// CommandParams reads a command's parameters as its inputs, in the order
// written.
func CommandParams(c *types.Checker, n *resolve.Node) []CommandParam {
	var out []CommandParam
	for _, p := range c.Params(n) {
		ap := ast.Param{N: p.N}
		cp := CommandParam{Name: p.Name, Optional: p.Optional, Variadic: p.Variadic, Param: ap,
			Site: resolve.Site{File: n.Decl.File, N: p.N, Scope: n.Decl.Scope}}
		cp.Display = strings.ReplaceAll(p.Name, "_", " ")
		if t := ap.Type(); t != nil && t.Syntax() != nil {
			cp.Type = strings.TrimSpace(t.Syntax().Text())
		}
		if d := ap.Default(); d != nil && d.Syntax() != nil {
			cp.Default = strings.TrimSpace(d.Syntax().Text())
		}
		for _, a := range ap.Annotations() {
			segs := resolve.PathSegs(a.Path().N)
			switch {
			case project.AnnotationNamed(segs, "display"):
				if es := project.ArgExprs(a.Args()); len(es) > 0 {
					if v, ok := resolve.StringValue(es[0].Syntax()); ok {
						cp.Display = v
					}
				}
			case project.AnnotationNamed(segs, "option"):
				cp.Option = true
				cp.Flag = "--" + strings.ReplaceAll(p.Name, "_", "-")
				cp.Short, _ = project.ArgString(a.Args(), -1, "short")
				cp.Env, _ = project.ArgString(a.Args(), -1, "env")
				cp.Value, _ = project.ArgString(a.Args(), -1, "value")
			}
		}
		out = append(out, cp)
	}
	return out
}

// CommandName is a command's name as it's typed, as `heron check`.
func CommandName(n *resolve.Node) string {
	if v, ok := project.ArgString(project.KindArgs(n), 0, "name"); ok {
		return v
	}
	return n.Name
}

// CommandUsage is the usage line a command's parameters derive: its name,
// then each option, `[--json]` for a Bool and `--entry file` for any
// other, in brackets where it's optional, then each argument, `<task>`, or
// `[paths…]` where it's optional and variadic.
func CommandUsage(c *types.Checker, n *resolve.Node) string {
	ps := CommandParams(c, n)
	parts := []string{CommandName(n)}
	for _, p := range ps {
		if !p.Option {
			continue
		}
		s := p.Flag
		if p.Type != "Bool" {
			v := p.Value
			if v == "" {
				v = p.Display
			}
			s += " " + v
		}
		if p.Variadic {
			s += "…"
		}
		if p.Optional {
			s = "[" + s + "]"
		}
		parts = append(parts, s)
	}
	for _, p := range ps {
		if p.Option {
			continue
		}
		s := p.Display
		if p.Variadic {
			s += "…"
		}
		if p.Optional {
			s = "[" + s + "]"
		} else {
			s = "<" + s + ">"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}
