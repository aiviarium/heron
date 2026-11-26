package docs

import (
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
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
		name := m[i : j+len("›")]
		if strings.HasSuffix(m[:i], "<") && strings.HasPrefix(m[j+len("›"):], ">") {
			// A placeholder inside literal angle brackets, as `<‹kind›>`
			// for a tag, reads as `<kind>`: the brackets already mark it.
			name = m[i+len("‹") : j]
		}
		b.WriteString(esc(m[:i]))
		b.WriteString(`<span class="ph">` + esc(name) + `</span>`)
		m = m[j+len("›"):]
	}
}

// diagnosticBadges are a diagnostic's code and severity, as text.
func diagnosticBadges(n *resolve.Node) string {
	code, _ := argString(kindArgs(n), 0, "code")
	sev := diagnosticSeverity(n)
	return `<code class="diag-code">` + esc(code) + `</code> <span class="sev sev-` + attr(sev) + `">` + esc(sev) + `</span>`
}

// renderDiagnostic writes a diagnostic as one entry of a compact list: its
// code, severity and message on one line, what it means below, and who
// reports it with the other formal parts on hover, or under it in explore.
func renderDiagnostic(c *Ctx) error {
	n := c.Node
	s := c.Site
	if s.Opts.Lens != nil && s.Opts.Lens.Name == "user" {
		// The error a person meets: code and message, and the fix.
		code, _ := argString(kindArgs(n), 0, "code")
		c.W(`<div class="diag" id="`, attr(Anchor(n)), `"><p class="diag-line"><code class="diag-code">`, esc(code), `</code>`)
		if m := fieldString(n, "message"); m != "" {
			c.W(` <span class="diag-message">`, messageHTML(m), `</span>`)
		}
		c.W(`</p>`)
		if fix := firstSentence(inlineParagraph(s.proseHTML(n))); fix != "" {
			c.W(`<p class="diag-fix">`, fix, `</p>`)
		}
		c.W(`</div>`)
		return nil
	}
	c.W(`<div class="diag" id="`, attr(Anchor(n)), `"><p class="diag-line">`, diagnosticBadges(n), statusBadge(n))
	if m := fieldString(n, "message"); m != "" {
		c.W(` <span class="diag-message">`, messageHTML(m), `</span>`)
	}
	if n.Name != "" {
		c.W(` <a class="rule-link" href="#`, attr(Anchor(n)), `" aria-label="`, attr(displayName(s, n)), `">#</a>`)
	}
	c.W(`</p>`)
	var meta []string
	if n.Name != "" {
		meta = append(meta, `<code class="meta-name">`+esc(relPathOf(n))+`</code>`)
	}
	if k := s.Checker.KindOf(n); k != nil {
		meta = append(meta, `<code class="meta-kind">`+esc(k.Name)+`</code>`)
	}
	if r := fieldString(n, "related"); r != "" {
		meta = append(meta, esc(label("docs.label.related_site"))+` <span class="diag-related">`+messageHTML(r)+`</span>`)
	}
	if by := s.ruleBy(n); by != nil {
		meta = append(meta, esc(label("docs.label.reported_by"))+` `+s.linkHTML(by, `<code>`+esc(c.refText(by))+`</code>`, ""))
	}
	if len(meta) > 0 {
		c.W(`<span class="rule-meta">`, strings.Join(meta, `<span class="dot"> · </span>`), `</span>`)
	}
	if h := s.proseHTML(n); h != "" {
		c.W(`<div class="diag-doc">`, h, `</div>`)
	}
	if exs := s.childrenOfKind(n, "spec.example"); len(exs) == 1 {
		c.W(`<p class="rule-example">`, esc(label("docs.label.for_example")), `: `, s.inlineHTML(exs[0]), `</p>`)
	} else if len(exs) > 1 {
		c.W(`<div class="rule-example"><p>`, esc(label("docs.label.for_example")), `:</p><ul>`)
		for _, e := range exs {
			c.W(`<li>`, s.inlineHTML(e), `</li>`)
		}
		c.W(`</ul></div>`)
	}
	if v := c.verifyList(n); v != "" {
		c.W(v)
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
	// One entry per code: a code with one diagnostic on one line with its
	// message, a code with more as the code once and each message under
	// it, each message with its fix.
	var groups [][]*resolve.Node
	for _, d := range ds {
		if n := len(groups); n > 0 && code(groups[n-1][0]) == code(d) {
			groups[n-1] = append(groups[n-1], d)
			continue
		}
		groups = append(groups, []*resolve.Node{d})
	}
	message := func(d *resolve.Node) string {
		m := messageHTML(fieldString(d, "message"))
		if s.anchorOf[d] == c.Page {
			// The entry is the diagnostic's place.
			return `<span class="diag-message">` + m + `</span>`
		}
		return s.linkHTML(d, m, "diag-message")
	}
	fix := func(d *resolve.Node) {
		if f := firstSentence(inlineParagraph(s.proseHTML(d))); f != "" {
			c.W(`<p class="diag-fix">`, f, `</p>`)
		}
	}
	id := func(d *resolve.Node) string {
		if s.anchorOf[d] == c.Page {
			return ` id="` + attr(Anchor(d)) + `"`
		}
		return ""
	}
	c.W(`<ul class="diag-list">`)
	for _, g := range groups {
		cd := code(g[0])
		*c.outline = append(*c.outline, Heading{ID: "code-" + cd, Title: cd, Level: 2})
		c.W(`<li id="code-`, attr(cd), `">`)
		if len(g) == 1 {
			c.W(`<div`, id(g[0]), `><p class="diag-line"><code class="diag-code">`, esc(cd), `</code> `, message(g[0]), `</p>`)
			fix(g[0])
			c.W(`</div></li>`)
			continue
		}
		c.W(`<p class="diag-line"><code class="diag-code">`, esc(cd), `</code></p><ul class="diag-group">`)
		for _, d := range g {
			c.W(`<li`, id(d), `><p class="diag-line">`, message(d), `</p>`)
			fix(d)
			c.W(`</li>`)
		}
		c.W(`</ul></li>`)
	}
	c.W(`</ul>`)
	return nil
}
