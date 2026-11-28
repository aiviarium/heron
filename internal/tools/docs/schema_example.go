package docs

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// A kind's example on its page is written from its declaration alone: a
// node of the kind with a value for each required parameter and field and
// a child of each kind it accepts, held in the first kind that accepts it
// where it stands at no file's top. It's checked as a tree of its own; a
// child the checker reports is left out, and the example where it still
// doesn't check clean. The rendered tab is the docs renderer's own view of
// the example.

// exNode is one node of an example.
type exNode struct {
	kind   *resolve.Node
	name   string // "" for an unnamed child, as `must {{ … }}`
	args   []string
	attrs  [][2]string
	fields [][2]string
	text   string
	kids   []*exNode
}

// kindExample is what a kind's example tab shows, once worked out.
type kindExample struct {
	heron, md, rendered string
}

var (
	exampleCheckMu sync.Mutex
	exampleSession *check.Session
	exampleChecks  = map[[32]byte]*exampleCheck{}
)

// exampleCheck is one checked example source.
type exampleCheck struct {
	diags    diag.List
	rendered string
}

// plainSentence is the first sentence of a declaration's docs as plain
// text that can stand in a `{{ … }}` body.
func (s *Site) plainSentence(n *resolve.Node) string {
	t := strings.TrimSpace(htmlUnescape(stripTags(s.schemaSentence(n))))
	t = strings.ReplaceAll(t, "`", "")
	if t == "" || strings.ContainsAny(t, "{}|<>\\[]") {
		return ""
	}
	return t
}

// exampleText is the text a node of a kind holds in its example.
func (s *Site) exampleText(k *resolve.Node) string {
	if t := s.plainSentence(k); t != "" {
		return t
	}
	for _, c := range s.Checker.Categories(k) {
		if t := s.plainSentence(c); t != "" {
			return t
		}
	}
	return sentenceCase(k.Name) + "."
}

// exampleValue is a value of a type written as an example, or "".
func (s *Site) exampleValue(x texpr, name string) string {
	switch x.name {
	case "String":
		return strconv.Quote(name)
	case "Int":
		return "1"
	case "Float", "Number":
		return "1.0"
	case "Bool":
		return "true"
	}
	if x.decl != nil && s.Checker.IsEnum(x.decl) {
		for _, v := range x.decl.Children {
			if v.Name != "" {
				return x.decl.Name + "." + v.Name
			}
		}
	}
	return ""
}

// acceptsText reports whether a kind's body reads text.
func (s *Site) acceptsText(k *resolve.Node) bool {
	for _, a := range s.Checker.AcceptEntries(k) {
		if a.Mode == "prose" {
			return true
		}
	}
	return false
}

// exampleNode is a node of kind k with a value for each required
// parameter and field, and, with kids, a child of each kind it accepts.
func (s *Site) exampleNode(k *resolve.Node, kids, child bool) *exNode {
	e := &exNode{kind: k, name: k.Name + "_example"}
	for _, p := range s.paramsOf(k) {
		if p.optional || strings.HasSuffix(p.name, "…") {
			continue
		}
		v := s.exampleValue(p.typ, p.name)
		if v == "" {
			return nil
		}
		e.args = append(e.args, v)
		e.attrs = append(e.attrs, [2]string{p.name, strings.Trim(v, `"`)})
	}
	for _, m := range s.members(k, nil) {
		if m.optional || m.def != nil {
			continue
		}
		v := s.exampleValue(m.typ, m.name)
		if v == "" {
			return nil
		}
		e.fields = append(e.fields, [2]string{m.name, v})
	}
	if s.acceptsText(k) {
		e.text = s.exampleText(k)
	}
	if child && e.text != "" {
		// A child's text reads as part of its parent's sentence.
		e.text = strings.TrimSuffix(e.text, ".")
		if r := []rune(e.text); len(r) > 1 && unicode.IsUpper(r[0]) && !unicode.IsUpper(r[1]) {
			e.text = string(unicode.ToLower(r[0])) + string(r[1:])
		}
	}
	if child && len(e.args) == 0 && len(e.fields) == 0 && e.text != "" && len(s.accepted(k)) == 0 {
		e.name = ""
	}
	if !kids {
		return e
	}
	seen := map[*resolve.Node]bool{k: true}
	for _, x := range s.accepted(k) {
		kk := x
		if s.Checker.IsCategory(x) {
			kk = nil
			for _, m := range s.schema.members[x] {
				if !seen[m] {
					kk = m
					break
				}
			}
		}
		if kk == nil || seen[kk] {
			continue
		}
		seen[kk] = true
		if c := s.exampleNode(kk, false, true); c != nil {
			e.kids = append(e.kids, c)
		}
	}
	return e
}

// heronText writes an example node as a Heron file writes it, recording
// where each child starts and ends.
func (e *exNode) heronText(b *strings.Builder, indent string, spans map[*exNode][2]int) {
	start := b.Len()
	defer func() { spans[e] = [2]int{start, b.Len()} }()
	head := e.kind.Name
	if len(e.args) > 0 {
		head += "(" + strings.Join(e.args, ", ") + ")"
	}
	if e.name == "" {
		b.WriteString(indent + head + " {{ " + e.text + " }}\n")
		return
	}
	head = e.name + ": " + head
	if len(e.fields) == 0 && len(e.kids) == 0 {
		if e.text != "" {
			b.WriteString(indent + head + " {{ " + e.text + " }}\n")
		} else {
			b.WriteString(indent + head + " {}\n")
		}
		return
	}
	b.WriteString(indent + head + " {\n")
	in := indent + "  "
	if e.text != "" {
		b.WriteString(in + "| " + e.text + "\n")
	}
	for _, f := range e.fields {
		b.WriteString(in + f[0] + " = " + f[1] + "\n")
	}
	if len(e.kids) > 0 && (e.text != "" || len(e.fields) > 0) {
		b.WriteString("\n")
	}
	for _, k := range e.kids {
		k.heronText(b, in, spans)
	}
	b.WriteString(indent + "}\n")
}

// mdText writes an example node as an element of a prose file.
func (e *exNode) mdText(b *strings.Builder) {
	b.WriteString("<" + e.kind.Name)
	if e.name != "" {
		b.WriteString(` id="` + e.name + `"`)
	}
	for _, a := range append(append([][2]string(nil), e.attrs...), e.fields...) {
		b.WriteString(" " + a[0] + `="` + strings.Trim(a[1], `"`) + `"`)
	}
	if e.text == "" && len(e.kids) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	if len(e.kids) == 0 {
		b.WriteString(e.text + "</" + e.kind.Name + ">")
		return
	}
	b.WriteString("\n")
	if e.text != "" {
		b.WriteString(e.text + "\n\n")
	}
	for _, k := range e.kids {
		k.mdText(b)
		b.WriteString("\n")
	}
	b.WriteString("</" + e.kind.Name + ">")
}

// standsAtTop reports whether a kind stands at a file's top: no kind
// holds it, or it joins a category that stands there.
func (s *Site) standsAtTop(k *resolve.Node, top map[*resolve.Node]bool) bool {
	if hs, cs := s.holdersOf(k); len(hs)+len(cs) == 0 {
		return true
	}
	for _, c := range s.Checker.Categories(k) {
		if top[c] {
			return true
		}
	}
	return false
}

// exampleImports are the imports an example needs: each standard package
// its kinds are declared in beyond the core, std/doc, and the tree itself
// for the tree's own kinds.
func (s *Site) exampleImports(e *exNode, dir string) []string {
	set := map[string]bool{"std/doc@1": true}
	var walk func(e *exNode)
	walk = func(e *exNode) {
		if p := s.schema.pkgOf[e.kind]; p != nil {
			if p.node == nil || own(e.kind) {
				if s.Tree.Entry != nil {
					if rel, err := filepath.Rel(dir, filepath.Dir(string(s.Tree.Entry.ID))); err == nil {
						set[filepath.ToSlash(rel)+"/"] = true
					}
				}
			} else if strings.HasPrefix(p.name, "std/") && p.name != resolve.PreludeName && p.name != resolve.SpecName {
				set[p.name+"@1"] = true
			}
		}
		for _, k := range e.kids {
			walk(k)
		}
	}
	walk(e)
	var out []string
	for k := range set {
		out = append(out, strconv.Quote(k))
	}
	sortStrings(out)
	return out
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// exampleDir is the folder the examples are checked in.
var exampleDir = sync.OnceValue(func() string {
	d, err := os.MkdirTemp("", "heron-schema-example-")
	if err != nil {
		return ""
	}
	return d
})

// checkExample checks a tree of the example's own and renders its node
// named top, memoized by the sources.
func (s *Site) checkExample(index, md, top string, render bool) *exampleCheck {
	key := sha256.Sum256([]byte(index + "\x00" + md + "\x00" + top + "\x00" + strconv.FormatBool(render)))
	exampleCheckMu.Lock()
	defer exampleCheckMu.Unlock()
	if r, ok := exampleChecks[key]; ok {
		return r
	}
	r := &exampleCheck{}
	exampleChecks[key] = r
	dir := exampleDir()
	if dir == "" {
		r.diags.Errorf("", 0, 0, "example", "no folder")
		return r
	}
	sub, err := os.MkdirTemp(dir, "x")
	if err != nil {
		r.diags.Errorf("", 0, 0, "example", "no folder")
		return r
	}
	defer os.RemoveAll(sub)
	if err := os.WriteFile(filepath.Join(sub, "index.heron"), []byte(index), 0o644); err != nil {
		r.diags.Errorf("", 0, 0, "example", "no file")
		return r
	}
	if md != "" {
		if err := os.WriteFile(filepath.Join(sub, "example.heron.md"), []byte(md), 0o644); err != nil {
			r.diags.Errorf("", 0, 0, "example", "no file")
			return r
		}
	}
	id, err := resolve.Canonical(filepath.Join(sub, "index.heron"))
	if err != nil {
		r.diags.Errorf("", 0, 0, "example", "no file")
		return r
	}
	if exampleSession == nil {
		exampleSession = check.NewSession()
	}
	res := exampleSession.Check(id)
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			r.diags = append(r.diags, d)
		}
	}
	if len(r.diags) == 0 && render {
		res.Complete()
		if n := res.Tree.Root.Child(top); n != nil {
			r.rendered = s.renderExample(res, n)
		}
	}
	return r
}

// renderExample is the docs renderer's view of an example's node, inert:
// without ids, links or islands, so it sits inside another page.
func (s *Site) renderExample(res *check.Result, n *resolve.Node) (out string) {
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()
	site := New(res.Tree, res.Checker, Options{Registry: s.Opts.Registry})
	c := site.newCtx(n, nil, 3)
	if err := c.render(); err != nil {
		return ""
	}
	return inert(c.out.String())
}

var (
	inertAttrRe = regexp.MustCompile(` (?:id|data-card|data-island|data-key|aria-controls|aria-labelledby|aria-describedby|href)="[^"]*"`)
	inertLinkRe = regexp.MustCompile(`<a\b([^>]*)>`)
	inertTabsRe = regexp.MustCompile(`<div class="seg" role="tablist"[^>]*>.*?</div>`)
)

// inert takes the ids, links and islands out of rendered HTML.
func inert(h string) string {
	h = inertTabsRe.ReplaceAllString(h, "")
	h = strings.ReplaceAll(h, ` role="tabpanel"`, "")
	h = strings.ReplaceAll(h, ` tabindex="0"`, "")
	h = inertAttrRe.ReplaceAllString(h, "")
	h = inertLinkRe.ReplaceAllString(h, "<span$1>")
	h = strings.ReplaceAll(h, "</a>", "</span>")
	h = strings.ReplaceAll(h, `<button type="button" class="icon-btn copy"`, `<button type="button" hidden class="icon-btn copy"`)
	return h
}

// example works a kind's example out: its two forms and its rendering,
// each "" where it doesn't check clean.
func (s *Site) example(k *resolve.Node) *kindExample {
	main := s.exampleNode(k, true, false)
	if main == nil {
		return nil
	}
	top := main
	roots := map[*resolve.Node]bool{}
	cats, _ := s.treeTop()
	for _, c := range cats {
		roots[c] = true
	}
	if !s.standsAtTop(k, roots) {
		var holder *resolve.Node
		if hs, cats := s.holdersOf(k); len(hs) > 0 {
			holder = hs[0]
		} else if len(cats) > 0 {
			holder = s.schema.holders[cats[0]][0]
		}
		if holder == nil {
			return nil
		}
		top = s.exampleNode(holder, false, false)
		if top == nil {
			return nil
		}
		top.text = s.exampleText(holder)
		main.name = ""
		if len(main.args) > 0 || len(main.fields) > 0 || len(main.kids) > 0 || main.text == "" {
			main.name = k.Name + "_example"
		}
		top.name = holder.Name + "_example"
		top.kids = []*exNode{main}
	}
	dir := filepath.Join(exampleDir(), "x")
	imports := "import = [" + strings.Join(s.exampleImports(top, dir), ", ") + "]\n\n"
	var heron string
	var res *exampleCheck
	for try := 0; try < 12; try++ {
		var b strings.Builder
		spans := map[*exNode][2]int{}
		top.heronText(&b, "", spans)
		heron = b.String()
		res = s.checkExample(imports+heron, "", top.name, true)
		if len(res.diags) == 0 {
			break
		}
		// Leave out the children the checker reports.
		dropped := false
		for _, d := range res.diags {
			at := d.Start - len(imports)
			for _, holder := range []*exNode{main, top} {
				for i, kid := range holder.kids {
					sp := spans[kid]
					if kid != main && at >= sp[0] && at < sp[1] {
						holder.kids = append(holder.kids[:i:i], holder.kids[i+1:]...)
						dropped = true
						break
					}
				}
			}
			if dropped {
				break
			}
		}
		if !dropped {
			return nil
		}
	}
	if len(res.diags) > 0 {
		return nil
	}
	out := &kindExample{heron: strings.TrimRight(heron, "\n"), rendered: res.rendered}
	if !s.Checker.StructureOnly(k) && !s.Checker.StructureOnly(top.kind) {
		var b strings.Builder
		top.mdText(&b)
		md := b.String() + "\n"
		if r := s.checkExample(imports+"include = [\"./example.heron.md\"]\n", md, top.name, false); len(r.diags) == 0 {
			out.md = strings.TrimRight(md, "\n")
		}
	}
	return out
}

// kindExample writes a kind's example in its tabs: `.heron`, `.heron.md`
// and Rendered, each where it checks clean.
func (c *Ctx) kindExample(k *resolve.Node) {
	s := c.Site
	ex := s.example(k)
	if ex == nil {
		return
	}
	type tab struct{ id, title, body string }
	var tabs []tab
	base := "example-" + strings.ReplaceAll(Anchor(k), ".", "-")
	tabs = append(tabs, tab{base + "-heron", label("docs.schema.tab_heron"), s.codeFrame("heron", "index.heron", s.highlightSource(ex.heron), ex.heron)})
	if ex.md != "" {
		tabs = append(tabs, tab{base + "-md", label("docs.schema.tab_md"), s.codeFrame("heron", "example.heron.md", esc(ex.md), ex.md)})
	}
	if ex.rendered != "" {
		tabs = append(tabs, tab{base + "-rendered", label("docs.schema.tab_rendered"), `<div class="kind-rendered">` + ex.rendered + `</div>`})
	}
	c.Heading(2, Anchor(k)+".example", label("docs.schema.example"), "")
	c.tabs(label("docs.schema.example_tabs"), "kind-example", tabs2(tabs, func(t tab) [3]string { return [3]string{t.id, t.title, t.body} }))
}

func tabs2[T any](xs []T, f func(T) [3]string) [][3]string {
	var out [][3]string
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

// tabs writes a tab list and its panels: each tab a button with its id,
// controlling its panel, the first selected; the tabs island moves between
// them with the arrow keys, Home and End.
func (c *Ctx) tabs(name, class string, tabs [][3]string) {
	c.Island("tabs")
	c.W(`<div class="`, attr(class), ` view-tabs" data-island="tabs"><div class="seg" role="tablist" aria-label="`, attr(name), `">`)
	for i, t := range tabs {
		sel := "false"
		if i == 0 {
			sel = "true"
		}
		c.W(`<button type="button" role="tab" id="`, attr(t[0]), `-tab" aria-controls="`, attr(t[0]), `" aria-selected="`, sel, `">`, esc(t[1]), `</button>`)
	}
	c.W(`</div>`)
	for i, t := range tabs {
		hidden := ""
		if i > 0 {
			hidden = " hidden"
		}
		c.W(`<div role="tabpanel" id="`, attr(t[0]), `" aria-labelledby="`, attr(t[0]), `-tab" tabindex="0"`, hidden, `>`, t[2], `</div>`)
	}
	c.W(`</div>`)
}
