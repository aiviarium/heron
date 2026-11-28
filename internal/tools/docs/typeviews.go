package docs

import (
	"strings"
	"unicode"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// A type page shows its fields three ways, as tabs: Fields, the field
// table with nested types opening in place; Flat, every field the type
// holds by its dotted path; and Example, the declaration beside a value of
// it with every field filled in.

// capture runs f on a context that writes into a buffer of its own, and
// returns what it wrote.
func (c *Ctx) capture(f func(d *Ctx)) string {
	d := *c
	d.out = &strings.Builder{}
	f(&d)
	return d.out.String()
}

// typeViews writes a type's fields in their three tabs.
func (c *Ctx) typeViews(t *resolve.Node, ms []member, signature string) {
	base := "views-" + strings.NewReplacer(".", "-", "#", "-").Replace(Anchor(t))
	fields := c.capture(func(d *Ctx) {
		d.nest = true
		d.fieldsTable(t, ms)
	})
	flat := c.capture(func(d *Ctx) { d.flatTable(t) })
	example := c.capture(func(d *Ctx) { d.typeExample(t, signature) })
	c.tabs(label("docs.views.label"), "type-views", [][3]string{
		{base + "-fields", label("docs.views.fields"), fields},
		{base + "-flat", label("docs.views.flat"), flat},
		{base + "-example", label("docs.views.example"), example},
	})
}

// innerType is a type with its List, Set and Stream taken off, and the
// suffix its path takes for each.
func innerType(x texpr) (texpr, string) {
	suffix := ""
	for (x.name == "List" || x.name == "Set" || x.name == "Stream") && len(x.args) == 1 {
		x = x.args[0]
		suffix += "[]"
	}
	return x, suffix
}

// nestable reports whether a field's type is a type of the tree with
// fields of its own that the path hasn't met yet.
func (s *Site) nestable(x texpr, stack []*resolve.Node) bool {
	t := x.decl
	if t == nil || !own(t) || !s.isTypeDecl(t) || s.kindPath(t) == "types.enum" || s.kindPath(t) == "types.union" {
		return false
	}
	for _, st := range stack {
		if st == t {
			return false
		}
	}
	return len(s.members(t, s.envFor(x))) > 0
}

// nestedFields opens a field whose type has fields of its own in place.
func (c *Ctx) nestedFields(owner *resolve.Node, m member) string {
	s := c.Site
	inner, _ := innerType(m.typ)
	if !s.nestable(inner, []*resolve.Node{owner}) {
		return ""
	}
	tree := c.capture(func(d *Ctx) { d.schemaTree(m.typ, []*resolve.Node{owner}, 1) })
	if tree == "" {
		return ""
	}
	return `<details class="nested-fields"><summary>` + esc(label("docs.views.fields")) + `</summary>` + tree + `</details>`
}

// flatRow is one field of a type by its dotted path.
type flatRow struct {
	path string
	m    member
}

// flatFields are every field a type holds, the fields of its fields
// included, by their dotted paths.
func (s *Site) flatFields(t *resolve.Node) []flatRow {
	var out []flatRow
	var walk func(t *resolve.Node, env map[string]texpr, prefix string, stack []*resolve.Node)
	walk = func(t *resolve.Node, env map[string]texpr, prefix string, stack []*resolve.Node) {
		if len(stack) > 8 {
			return
		}
		stack = append(stack, t)
		for _, m := range s.members(t, env) {
			p := prefix + m.name
			out = append(out, flatRow{path: p, m: m})
			inner, suffix := innerType(m.typ)
			if s.nestable(inner, stack) {
				walk(inner.decl, s.envFor(inner), p+suffix+".", stack)
			}
		}
	}
	walk(t, nil, "", nil)
	return out
}

// flatAnchor is the anchor of a field's row in a type's Flat tab.
func flatAnchor(t *resolve.Node, path string) string { return Anchor(t) + ":" + path }

// enumValues lists an enum type's values in code.
func (s *Site) enumValues(x texpr) string {
	inner, _ := innerType(x)
	if inner.decl == nil || s.kindPath(inner.decl) != "types.enum" {
		return ""
	}
	var vs []string
	for _, v := range inner.decl.Children {
		if v.Name != "" {
			vs = append(vs, `<code>`+esc(v.Name)+`</code>`)
		}
	}
	return `<div class="mark values">` + strings.Join(vs, " ") + `</div>`
}

func (c *Ctx) flatTable(t *resolve.Node) {
	s := c.Site
	rows := s.flatFields(t)
	hasDef, hasDoc := false, false
	for _, r := range rows {
		if r.m.def != nil {
			hasDef = true
		}
		if s.proseHTML(r.m.node) != "" {
			hasDoc = true
		}
	}
	c.W(`<div class="table-wrap"><table class="fields flat"><thead><tr><th>`, esc(label("docs.views.path")), `</th><th>`, esc(label("docs.col.type")), `</th>`)
	if hasDef {
		c.W(`<th>`, esc(label("docs.col.default")), `</th>`)
	}
	if hasDoc {
		c.W(`<th>`, esc(label("docs.col.description")), `</th>`)
	}
	c.W(`</tr></thead><tbody>`)
	for _, r := range rows {
		id := flatAnchor(t, r.path)
		c.W(`<tr id="`, attr(id), `"><td><a class="anchor flat-path" href="#`, attr(id), `"><code class="fname">`, esc(r.path), `</code></a>`)
		if !r.m.optional && r.m.def == nil {
			c.W(` <span class="badge req">`, esc(label("docs.badge.required")), `</span>`)
		}
		c.W(`</td><td><code>`, r.m.typ.html, `</code>`, s.enumValues(r.m.typ), `</td>`)
		if hasDef {
			c.W(`<td>`)
			if r.m.def != nil {
				c.W(`<code>`, s.exprHTML(r.m.def, r.m.site), `</code>`)
			}
			c.W(`</td>`)
		}
		if hasDoc {
			c.W(`<td>`, s.proseHTML(r.m.node), `</td>`)
		}
		c.W(`</tr>`)
	}
	c.W(`</tbody></table></div>`)
}

// flatSearch are the search entries of the fields a type's Flat tab adds:
// the fields of its fields, each by its type's name and dotted path.
func (s *Site) flatSearch() []entry {
	var out []entry
	for _, t := range s.types {
		p := s.pageOf[t]
		if p == nil || s.kindPath(t) == "types.enum" || s.kindPath(t) == "types.union" {
			continue
		}
		for _, r := range s.flatFields(t) {
			if !strings.Contains(r.path, ".") {
				// A field of the type's own, which the index has already.
				continue
			}
			text := strings.Join(strings.Fields(stripTags(s.proseHTML(r.m.node))), " ")
			if len(text) > 240 {
				text = text[:240]
			}
			out = append(out, entry{Title: t.Name + "." + r.path, Kind: "field", Path: t.Path() + "." + r.path,
				URL: s.href(p.File) + "#" + strings.ReplaceAll(flatAnchor(t, r.path), "#", "%23"), Page: p.Title, Text: text})
		}
	}
	return out
}

// snakeName is a type's name as a node's, as `server_config` for
// ServerConfig.
func snakeName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// heronValue is a value of a type written in Heron with every field
// filled in, or "" where the type has no value to write.
func (s *Site) heronValue(x texpr, stack []*resolve.Node, indent string) string {
	switch x.name {
	case "String":
		return `"string"`
	case "Int":
		return "0"
	case "Float", "Number":
		return "0.0"
	case "Bool":
		return "false"
	case "Timestamp":
		return `"2026-01-01T00:00:00Z"`
	case "Duration":
		return `"30s"`
	case "List", "Set", "Stream":
		if len(x.args) == 1 {
			if v := s.heronValue(x.args[0], stack, indent); v != "" {
				return "[" + v + "]"
			}
		}
		return "[]"
	case "Map":
		if len(x.args) == 2 {
			if v := s.heronValue(x.args[1], stack, indent); v != "" {
				return "{ key = " + v + " }"
			}
		}
		return "{}"
	}
	if len(x.union) > 0 {
		return s.heronValue(x.union[0], stack, indent)
	}
	t := x.decl
	if t == nil || len(stack) > 4 {
		return ""
	}
	for _, st := range stack {
		if st == t {
			return ""
		}
	}
	switch s.kindPath(t) {
	case "types.enum":
		for _, v := range t.Children {
			if v.Name != "" {
				return t.Name + "." + v.Name
			}
		}
		return ""
	case "types.union":
		for _, m := range s.members(t, s.envFor(x)) {
			return s.heronValue(m.typ, append(stack, t), indent)
		}
		return ""
	}
	if !s.isTypeDecl(t) {
		return ""
	}
	var fs []string
	for _, m := range s.members(t, s.envFor(x)) {
		if v := s.heronValue(m.typ, append(stack, t), indent+"  "); v != "" {
			fs = append(fs, m.name+" = "+v)
		}
	}
	one := t.Name + "(" + strings.Join(fs, ", ") + ")"
	if len(fs) <= 2 && len(one) <= 40 && !strings.Contains(one, "\n") {
		return one
	}
	in := indent + "  "
	return t.Name + "(\n" + in + strings.Join(fs, ",\n"+in) + ",\n" + indent + ")"
}

// typeValue is a node of a type with every field filled in, as a Heron
// file writes it.
func (s *Site) typeValue(t *resolve.Node) string {
	var b strings.Builder
	b.WriteString(snakeName(t.Name) + ": " + t.Name + " {\n")
	for _, m := range s.members(t, nil) {
		if v := s.heronValue(m.typ, []*resolve.Node{t}, "  "); v != "" {
			b.WriteString("  " + m.name + " = " + v + "\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

func (c *Ctx) typeExample(t *resolve.Node, signature string) {
	s := c.Site
	v := s.typeValue(t)
	c.W(`<div class="type-example">`, signature, s.codeFrame("heron", label("docs.views.value"), s.highlightSource(v), v), `</div>`)
}
