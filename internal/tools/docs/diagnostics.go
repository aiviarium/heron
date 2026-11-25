package docs

import (
	"sort"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// The view of std/tech's diagnostic kind: a card on its section's page,
// with its code, severity, messages and the component that reports it, and
// one generated index of every diagnostic in the tree.

// fieldString is the string a field of a node is set to, or "".
func fieldString(n *resolve.Node, name string) string {
	f := n.Child(name)
	if f == nil || f.Value == nil {
		return ""
	}
	v, _ := resolve.StringValue(f.Value.Syntax())
	return v
}

// diagnosticSeverity is a diagnostic's severity as written, or "error".
func diagnosticSeverity(n *resolve.Node) string {
	if e := namedArg(kindArgs(n), "severity"); e != nil {
		t := strings.TrimSpace(e.Syntax().Text())
		t = strings.Trim(t[strings.LastIndex(t, ".")+1:], `"`)
		return strings.ToLower(t)
	}
	if v, ok := argString(kindArgs(n), 1, "severity"); ok {
		return strings.ToLower(v)
	}
	return "error"
}

// messageHTML is a message template with each ‹placeholder› marked and each
// backticked part as code.
func messageHTML(m string) string {
	var b strings.Builder
	for i, part := range strings.Split(m, "`") {
		if i%2 == 1 {
			b.WriteString(`<code>` + placeholders(part) + `</code>`)
			continue
		}
		b.WriteString(placeholders(part))
	}
	return b.String()
}

// placeholders escapes text and marks each ‹placeholder› in it.
func placeholders(m string) string {
	var b strings.Builder
	for {
		i := strings.Index(m, "‹")
		j := strings.Index(m, "›")
		if i < 0 || j < i {
			b.WriteString(esc(m))
			return b.String()
		}
		b.WriteString(esc(m[:i]))
		b.WriteString(`<span class="ph">` + esc(m[i:j+len("›")]) + `</span>`)
		m = m[j+len("›"):]
	}
}

// diagnosticBadges are a diagnostic's code and severity as badges.
func diagnosticBadges(n *resolve.Node) string {
	code, _ := argString(kindArgs(n), 0, "code")
	sev := diagnosticSeverity(n)
	return `<code class="diag-code">` + esc(code) + `</code><span class="badge sev sev-` + attr(sev) + `">` + esc(sev) + `</span>`
}

func renderDiagnostic(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="rule diag" id="`, attr(Anchor(n)), `">`)
	c.W(`<div class="rule-head">`)
	if n.Name != "" {
		c.W(`<a class="anchor rule-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`)
	}
	c.W(diagnosticBadges(n), statusBadge(n))
	c.W(`</div>`)
	if m := fieldString(n, "message"); m != "" {
		c.W(`<p class="diag-message">`, messageHTML(m), `</p>`)
	}
	c.Prose(n)
	var rows []string
	if r := fieldString(n, "related"); r != "" {
		rows = append(rows, `<dt>`+esc(label("docs.label.related_site"))+`</dt><dd><span class="diag-related">`+messageHTML(r)+`</span></dd>`)
	}
	if by := s.ruleBy(n); by != nil {
		rows = append(rows, `<dt>`+esc(label("docs.label.reported_by"))+`</dt><dd>`+s.linkHTML(by, `<code>`+esc(c.refText(by))+`</code>`, "")+`</dd>`)
	}
	if exs := s.childrenOfKind(n, "spec.example"); len(exs) > 0 {
		var li strings.Builder
		for _, e := range exs {
			li.WriteString(`<li>` + s.inlineHTML(e) + `</li>`)
		}
		rows = append(rows, `<dt>`+esc(label("docs.label.examples"))+`</dt><dd><ul>`+li.String()+`</ul></dd>`)
	}
	if v := c.verifyList(n); v != "" {
		rows = append(rows, `<dt>`+esc(label("docs.label.verified_by"))+`</dt><dd>`+v+`</dd>`)
	}
	if len(rows) > 0 {
		c.W(`<dl class="rule-more">`, strings.Join(rows, ""), `</dl>`)
	}
	c.W(`</div>`)
	return nil
}

// diagnosticsPage is the index of every diagnostic, by code and message,
// each linked to the section that specifies it.
func (s *Site) diagnosticsPage(c *Ctx) error {
	ds := append([]*resolve.Node(nil), s.diagnostics...)
	code := func(n *resolve.Node) string { v, _ := argString(kindArgs(n), 0, "code"); return v }
	sort.SliceStable(ds, func(i, j int) bool {
		if a, b := code(ds[i]), code(ds[j]); a != b {
			return a < b
		}
		return fieldString(ds[i], "message") < fieldString(ds[j], "message")
	})
	c.W(`<div class="table-wrap"><table class="diag-index"><thead><tr>`)
	for _, k := range []string{"code", "severity", "message", "reported_by", "specified_in"} {
		c.W(`<th>`, esc(label("docs.col."+k)), `</th>`)
	}
	c.W(`</tr></thead><tbody>`)
	last := ""
	for _, d := range ds {
		cd := code(d)
		id := ""
		if cd != last {
			id = ` id="code-` + attr(cd) + `"`
			*c.outline = append(*c.outline, Heading{ID: "code-" + cd, Title: cd, Level: 2})
			last = cd
		}
		cls := ` class="diag-repeat"`
		if id != "" {
			cls = ""
		}
		c.W(`<tr`, id, cls, `><td><code class="diag-code">`, esc(cd), `</code></td>`)
		sev := diagnosticSeverity(d)
		c.W(`<td><span class="badge sev sev-`, attr(sev), `">`, esc(sev), `</span></td>`)
		c.W(`<td>`, s.linkHTML(d, messageHTML(fieldString(d, "message")), "diag-message"), `</td><td>`)
		if by := s.ruleBy(d); by != nil {
			c.W(s.linkHTML(by, `<code>`+esc(by.Path())+`</code>`, ""))
		}
		c.W(`</td><td>`)
		if p := s.anchorOf[d]; p != nil {
			c.W(`<a href="`, attr(s.href(p.File)), `">`, esc(p.Title), `</a>`)
		}
		c.W(`</td></tr>`)
	}
	c.W(`</tbody></table></div>`)
	return nil
}
