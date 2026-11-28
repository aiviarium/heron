package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// The agent export's schema files: one per package of the schema
// reference, each kind as its signature and the rows of its page's
// summary as `key: value` lines, then each annotation and flag.

// schemaMDFile is the file of a package of the schema reference.
func schemaMDFile(p *schemaPkg) string { return schemaPagePrefix + "." + p.id + ".md" }

// withSchema adds the schema files to an export and lists them in its
// index and its llms.txt.
func (e *agentExport) withSchema(files []AgentFile) []AgentFile {
	s := e.s
	if s.schema == nil {
		return files
	}
	var add []AgentFile
	for _, p := range s.schema.pkgs {
		b := e.schemaMD(p)
		sum := ""
		if p.node != nil {
			sum = strings.Join(strings.Fields(mdText(s.schemaSentence(p.node))), " ")
		}
		add = append(add, AgentFile{Path: schemaMDFile(p), Title: p.name, ID: p.id, Summary: sum, Content: []byte(b)})
	}
	for i, f := range files {
		switch f.Path {
		case AgentIndex:
			var ls []string
			for _, a := range add {
				ls = append(ls, "["+mdLinkText(a.Title)+"]("+a.Path+")")
			}
			files[i].Content = append(f.Content, []byte("\n## Schema\n\nEach package's kinds, annotations and flags: "+strings.Join(ls, ", ")+".\n")...)
		case LLMsTxt:
			var b strings.Builder
			b.WriteString("\n## Schema\n\n")
			for _, a := range add {
				b.WriteString("- [" + a.Title + "](" + a.Path + ")\n")
			}
			files[i].Content = append(f.Content, []byte(b.String())...)
		}
	}
	return append(files, add...)
}

// signatureOf is a declaration as it's written without its kind slot and
// body: its name and parameters, as `rule(refs<variadic>?: Ref(Any))`.
func signatureOf(n *resolve.Node) string {
	sig := n.Name
	if n.Decl.N != nil && n.Decl.N.Kind == syntax.Declare {
		if ps := (ast.Declare{N: n.Decl.N}).Params(); ps.N != nil {
			sig += strings.Join(strings.Fields(ps.N.Text()), " ")
		}
	}
	return sig
}

// mdNames writes declarations by their names in code, as `@rev`.
func (s *Site) mdNames(ns []*resolve.Node) string {
	var out []string
	for _, n := range ns {
		name := n.Name
		switch {
		case s.Checker.IsAnnotation(n):
			name = "@" + name
		case s.Checker.IsFlag(n):
			name = "<" + name + ">"
		}
		out = append(out, "`"+name+"`")
	}
	return strings.Join(out, ", ")
}

func (e *agentExport) schemaMD(p *schemaPkg) string {
	s := e.s
	var b strings.Builder
	b.WriteString("# Schema of " + p.name + " (`" + p.id + "`)\n\n")
	b.WriteString("Agent export of " + e.siteTitle() + ": start at [" + AgentIndex + "](" + AgentIndex + ").\n")
	doc := func(n *resolve.Node) {
		if t := strings.Join(strings.Fields(mdText(s.schemaSentence(n))), " "); t != "" {
			b.WriteString("\n" + t + "\n")
		}
	}
	if p.node != nil {
		doc(p.node)
	}
	rows := func(rs [][2]string) {
		if len(rs) == 0 {
			return
		}
		b.WriteString("\n")
		for _, r := range rs {
			b.WriteString(r[0] + ": " + r[1] + "\n")
		}
	}
	if len(p.kinds) > 0 {
		b.WriteString("\n## Kinds\n")
		for _, k := range p.kinds {
			b.WriteString("\n### " + signatureOf(k) + "\n")
			doc(k)
			rows(s.kindRowsMD(k))
		}
	}
	if len(p.annotations) > 0 {
		b.WriteString("\n## Annotations\n")
		for _, a := range p.annotations {
			b.WriteString("\n### @" + signatureOf(a) + "\n")
			doc(a)
			on := "any node"
			if l := s.Checker.On(a); len(l) > 0 {
				on = s.mdNames(l)
			}
			rep := "no"
			if types.HasKindFlag(a, "repeatable") {
				rep = "yes"
			}
			rows([][2]string{{"attaches to", on}, {"repeatable", rep}})
		}
	}
	if len(p.flags) > 0 {
		b.WriteString("\n## Flags\n")
		for _, f := range flagsByGroup(p.flags) {
			b.WriteString("\n### <" + f.Name + ">\n")
			doc(f)
			var rs [][2]string
			if g := flagGroup(f); g != "" {
				rs = append(rs, [2]string{"group", strconv.Quote(g)})
			}
			if flagDefault(f) {
				rs = append(rs, [2]string{"default", "yes"})
			}
			on := "any node"
			if l := s.Checker.On(f); len(l) > 0 {
				on = s.mdNames(l)
			}
			rs = append(rs, [2]string{"on", on})
			rows(rs)
		}
	}
	if len(p.categories) > 0 {
		b.WriteString("\n## Categories\n")
		for _, c := range p.categories {
			b.WriteString("\n### " + c.Name + "\n")
			doc(c)
			if m := s.schema.members[c]; len(m) > 0 {
				rows([][2]string{{"kinds", s.mdNames(m)}})
			}
		}
	}
	return b.String()
}

// kindRowsMD are the rows of a kind page's summary, as plain text.
func (s *Site) kindRowsMD(k *resolve.Node) [][2]string {
	var rs [][2]string
	if l := s.layerOf(k); l != "" {
		rs = append(rs, [2]string{"layer", strings.ToLower(l)})
	}
	if cats := s.Checker.Categories(k); len(cats) > 0 {
		rs = append(rs, [2]string{"belongs to", s.mdNames(cats)})
	}
	if kinds, cats := s.holdersOf(k); len(kinds)+len(cats) > 0 {
		var items []string
		if len(kinds) > 0 {
			items = append(items, s.mdNames(kinds))
		}
		for _, c := range cats {
			items = append(items, "any kind that accepts `"+c.Name+"`")
		}
		rs = append(rs, [2]string{"goes inside", strings.Join(items, "; ")})
	}
	if groups, modes := s.holdsOf(k); len(groups)+len(modes) > 0 {
		var parts []string
		for _, g := range groups {
			name := "other"
			if g.cat != nil {
				name = "`" + g.cat.Name + "`"
				if g.catMax > 0 {
					name += " at most " + strconv.Itoa(g.catMax)
				}
			}
			var ks []string
			for _, m := range g.kinds {
				l := "`" + m.Name + "`"
				if n := g.max[m]; n > 0 {
					l += " (at most " + strconv.Itoa(n) + ")"
				}
				ks = append(ks, l)
			}
			parts = append(parts, name+" ("+strings.Join(ks, ", ")+")")
		}
		parts = append(parts, modes...)
		rs = append(rs, [2]string{"holds", strings.Join(parts, "; ")})
	}
	if refs, ok := s.Checker.ReferenceKinds(k); ok && len(refs) > 0 {
		rs = append(rs, [2]string{"references", s.mdNames(refs)})
	}
	if named, _ := s.annotationsOn(k); len(named) > 0 {
		rs = append(rs, [2]string{"annotations", s.mdNames(named)})
	}
	if fs := s.flagsOn(k); len(fs) > 0 {
		var parts []string
		for _, f := range flagsByGroup(fs) {
			l := "`<" + f.Name + ">`"
			if g := flagGroup(f); g != "" {
				l += " (group " + strconv.Quote(g)
				if flagDefault(f) {
					l += ", default"
				}
				l += ")"
			}
			parts = append(parts, l)
		}
		rs = append(rs, [2]string{"flags", strings.Join(parts, ", ")})
	}
	return rs
}

// mdText is inline HTML as Markdown text: its code in backticks, its tags
// dropped.
func mdText(h string) string {
	h = strings.NewReplacer("<code>", "`", "</code>", "`").Replace(h)
	return htmlUnescape(stripTags(h))
}
