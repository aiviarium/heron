package docs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// opInfo is an op read for its reference page.
type opInfo struct {
	node, api        *resolve.Node
	method, path     string
	base, ws         string
	params           []opParam
	returns          texpr
	errors           []opError
	performs         []*resolve.Node
	realizes         []*resolve.Node
	examples         []opExample
	crossCutting     []crossAnn
	streamIn, stream bool
	server           string // the api's first @server URL
}

type opParam struct {
	name, loc, header string
	typ               texpr
	optional          bool
	def               ast.Expr
	doc               *syntax.Node
	syn               ast.Param
	site              resolve.Site
}

type opError struct {
	typ    *resolve.Node
	status string
	code   string          // an error node's id, the body's code
	causes []*resolve.Node // rules and examples that lead to it
}

type opExample struct {
	node    *resolve.Node
	values  map[string]any // parameter values
	returns any
	hasRet  bool
	throws  *resolve.Node
	thrown  any
}

type crossAnn struct {
	name string
	a    ast.Annotation
	site resolve.Site
	on   *resolve.Node
}

var ownAnnotations = map[string]bool{"http": true, "throws": true, "performs": true, "display": true, "doc": true, "lint": true, "ws": true, "realizes": true, "proto": true, "jsonrpc": true, "server": true}

func (s *Site) readOp(op *resolve.Node) *opInfo {
	o := &opInfo{node: op, api: op.Parent}
	o.method, o.path = httpRoute(s, op)
	if o.api != nil {
		o.base = basePath(s, o.api)
		o.ws = wsRoute(o.api)
		if a := annotation(s, o.api, "server"); a != nil {
			o.server, _ = argString(a.Args(), 0, "url")
		}
	}
	if w := wsRoute(op); w != "" {
		o.ws = w
	}
	if op.Decl.N != nil && op.Decl.N.Kind == syntax.Declare {
		d := ast.Declare{N: op.Decl.N}
		site := resolve.Site{File: op.Decl.File, N: op.Decl.N, Scope: op.Decl.Scope}
		for _, p := range d.Params().Params() {
			if p.Ident() == nil {
				continue
			}
			pp := opParam{name: p.Ident().Text, syn: p, site: site, optional: p.Question() != nil || p.Default() != nil, def: p.Default()}
			pp.typ = s.readType(p.Type(), site, nil)
			if doc := p.Doc(); doc.N != nil {
				pp.doc = doc.N
			}
			for _, a := range p.Annotations() {
				switch n := annName(a); n {
				case "path", "query", "body":
					pp.loc = n
				case "header":
					pp.loc = "header"
					pp.header, _ = argString(a.Args(), 0, "name")
					if pp.header == "" {
						pp.header = pp.name
					}
				}
			}
			if pp.typ.name == "Stream" {
				o.streamIn = true
			}
			o.params = append(o.params, pp)
		}
		if t, ok := d.Type().(ast.NamedType); ok {
			args := t.Args()
			if es := argExprs(args); len(es) > 0 {
				o.returns = s.readTypeExpr(es[0], site, nil)
			} else if e := namedArg(args, "returns"); e != nil {
				o.returns = s.readTypeExpr(e, site, nil)
			}
		}
	}
	if o.returns.name == "Stream" {
		o.stream = true
	}
	// Errors: the op's own @throws, then its api's.
	var thrown []*resolve.Node
	for _, holder := range []*resolve.Node{op, o.api} {
		if holder == nil {
			continue
		}
		for _, a := range annotationsNamed(holder, "throws") {
			for _, e := range argExprs(a.Args()) {
				if t := s.refTarget(e.Syntax(), holder.Decl); t != nil {
					thrown = append(thrown, t)
				}
			}
		}
	}
	seen := map[*resolve.Node]bool{}
	var addErr func(t *resolve.Node)
	addErr = func(t *resolve.Node) {
		if seen[t] {
			return
		}
		seen[t] = true
		if s.kindPath(t) == "types.interface" && len(s.implementors[t]) > 0 {
			for _, i := range s.implementors[t] {
				addErr(i)
			}
			return
		}
		e := opError{typ: t}
		if s.kindPath(t) == "spec.error" {
			// An error node: its HTTP status and id are its projection.
			e.code, _ = argString(kindArgs(t), 0, "id")
			if st := namedArg(kindArgs(t), "status"); st != nil {
				e.status = strings.TrimSpace(st.Syntax().Text())
			}
		} else if a := annotation(s, t, "status"); a != nil {
			if es := argExprs(a.Args()); len(es) > 0 {
				e.status = strings.TrimSpace(es[0].Syntax().Text())
			}
		}
		o.errors = append(o.errors, e)
	}
	for _, t := range thrown {
		addErr(t)
	}
	for _, a := range op.Annotations {
		an := ast.Annotation{N: a.N}
		if an.N == nil || an.Path().N == nil || annName(an) != "realizes" {
			continue
		}
		for _, e := range project.ArgExprs(an.Args()) {
			if t := project.RefTarget(s.Checker, e.Syntax(), a); t != nil {
				o.realizes = append(o.realizes, t)
			}
		}
	}
	for _, a := range annotationsNamed(op, "performs") {
		for _, e := range argExprs(a.Args()) {
			if t := s.refTarget(e.Syntax(), op.Decl); t != nil {
				o.performs = append(o.performs, t)
			}
		}
	}
	// Cross-cutting annotations, from the op and then its api.
	for _, holder := range []*resolve.Node{op, o.api} {
		if holder == nil {
			continue
		}
		for _, a := range annotations(holder) {
			if ownAnnotations[annName(a)] {
				continue
			}
			o.crossCutting = append(o.crossCutting, crossAnn{name: annName(a), a: a, site: holder.Decl, on: holder})
		}
	}
	// Examples.
	for _, ch := range op.Children {
		if s.kindPath(ch) != "types.example" {
			continue
		}
		ex := opExample{node: ch, values: map[string]any{}}
		for _, f := range ch.Children {
			if f.Value == nil {
				continue
			}
			v := s.valueOf(f.Value, f.ValueSite, 0)
			switch f.Name {
			case "returns":
				ex.returns, ex.hasRet = v, true
			case "throws":
				ex.throws = s.refTarget(f.Value.Syntax(), f.ValueSite)
				ex.thrown = v
			default:
				ex.values[f.Name] = v
			}
		}
		o.examples = append(o.examples, ex)
	}
	// What causes each error: rules on the op that name it, and examples
	// that throw it.
	var rules []*resolve.Node
	s.Tree.Walk(func(x *resolve.Node) {
		if own(x) && s.kindPath(x) == "spec.rule" {
			if s.entryRealizes(op, x) {
				rules = append(rules, x)
				return
			}
			for _, t := range s.targets(x) {
				if t == op {
					rules = append(rules, x)
				}
			}
		}
	})
	for i := range o.errors {
		e := &o.errors[i]
		for _, r := range rules {
			if strings.Contains(s.ruleText(r), e.typ.Name) {
				e.causes = append(e.causes, r)
			}
		}
		for _, ex := range o.examples {
			if ex.throws == e.typ {
				e.causes = append(e.causes, ex.node)
			}
		}
	}
	return o
}

// ruleText is a rule's text with its children's, for matching names.
func (s *Site) ruleText(r *resolve.Node) string {
	var b strings.Builder
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, site := range append(append([]resolve.Site(nil), n.Prose...), n.Docs...) {
			if site.N != nil {
				b.WriteString(site.N.Text())
				b.WriteString(" ")
			}
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(r)
	return b.String()
}

// valueOf turns an example's value into plain data: a type's constructor
// into an object, an enum variant into its name, an error type into its
// defaults.
func (s *Site) valueOf(e ast.Expr, site resolve.Site, depth int) any {
	if e == nil || depth > 8 {
		return nil
	}
	switch e := e.(type) {
	case ast.Literal:
		switch {
		case e.KwTrue() != nil:
			return true
		case e.KwFalse() != nil:
			return false
		case e.Number() != nil:
			return json.Number(e.Number().Text)
		}
		return strings.TrimSpace(e.N.Text())
	case ast.StringLit:
		v, _ := resolve.StringValue(e.N)
		return v
	case ast.ArrayExpr:
		out := []any{}
		for _, x := range e.Exprs() {
			out = append(out, s.valueOf(x, site, depth+1))
		}
		return out
	case ast.MapExpr:
		m := &omap{}
		for _, en := range e.MapEntries() {
			m.set(resolve.PathText(en.Path().N), s.valueOf(en.Expr(), site, depth+1))
		}
		return m
	case ast.CallExpr:
		m := &omap{}
		t := s.refTarget(e.Callee().Syntax(), site)
		var names []string
		if t != nil {
			for _, mem := range s.members(t, nil) {
				names = append(names, mem.name)
			}
		}
		pos := 0
		for _, a := range e.Args().Args() {
			switch a := a.(type) {
			case ast.NamedArg:
				if a.Ident() != nil {
					m.set(a.Ident().Text, s.valueOf(a.Expr(), site, depth+1))
				}
			case ast.PositionalArg:
				if pos < len(names) {
					m.set(names[pos], s.valueOf(a.Expr(), site, depth+1))
				}
				pos++
			}
		}
		return m
	case ast.PathExpr:
		t := s.refTarget(e.Path().N, site)
		switch {
		case t == nil:
			return strings.TrimSpace(e.N.Text())
		case t.Parent != nil && s.kindPath(t.Parent) == "types.enum":
			return t.Name
		case s.isTypeDecl(t):
			m := &omap{}
			for _, mem := range s.members(t, nil) {
				if mem.def != nil {
					m.set(mem.name, s.valueOf(mem.def, mem.site, depth+1))
				}
			}
			return m
		case t.Value != nil:
			return s.valueOf(t.Value, t.ValueSite, depth+1)
		}
		return t.Name
	case ast.ParenExpr:
		return s.valueOf(e.Expr(), site, depth+1)
	default:
		// Other expressions show as written.
	}
	return strings.TrimSpace(e.Syntax().Text())
}

// omap is an object that keeps its keys in order.
type omap struct {
	keys []string
	vals map[string]any
}

func (m *omap) set(k string, v any) {
	if m.vals == nil {
		m.vals = map[string]any{}
	}
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

func (m *omap) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	for i, k := range m.keys {
		if i > 0 {
			b.WriteString(",")
		}
		kj, _ := json.Marshal(k)
		vj, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kj)
		b.WriteString(":")
		b.Write(vj)
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

func pretty(v any) string {
	j, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(j)
}

func compact(v any) string {
	j, _ := json.Marshal(v)
	return string(j)
}

func scalar(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return compact(v)
}

// request is an example's request, built the same way for every form.
type request struct {
	method, url, path string
	server            string
	headers           [][2]string
	body              any
	hasBody           bool
}

func (o *opInfo) request(ex *opExample) request {
	r := request{method: o.method, path: o.path, server: o.server}
	if r.method == "" {
		r.method = "POST"
	}
	path := o.base + o.path
	var query []string
	for _, p := range o.params {
		var v any
		has := false
		if ex != nil {
			v, has = ex.values[p.name]
		}
		switch p.loc {
		case "path":
			if has {
				path = strings.ReplaceAll(path, ":"+p.name, scalar(v))
			}
		case "query":
			if !has {
				continue
			}
			if l, ok := v.([]any); ok {
				for _, x := range l {
					query = append(query, p.name+"="+scalar(x))
				}
			} else {
				query = append(query, p.name+"="+scalar(v))
			}
		case "header":
			if has {
				r.headers = append(r.headers, [2]string{p.header, scalar(v)})
			}
		default:
			if has {
				r.body, r.hasBody = v, true
			}
		}
	}
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	r.url = path
	if r.hasBody {
		r.headers = append(r.headers, [2]string{"Content-Type", "application/json"})
	}
	return r
}

func (r request) curl() string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -X %s \"%s%s\"", r.method, orBase(r.server), r.url)
	for _, h := range r.headers {
		fmt.Fprintf(&b, " \\\n  -H \"%s: %s\"", h[0], h[1])
	}
	if r.hasBody {
		fmt.Fprintf(&b, " \\\n  -d '%s'", compact(r.body))
	}
	return b.String()
}

func (r request) http() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\n", r.method, r.url)
	for _, h := range r.headers {
		fmt.Fprintf(&b, "%s: %s\n", h[0], h[1])
	}
	if r.hasBody {
		b.WriteString("\n" + pretty(r.body))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ts is the call through the generated client: the op called by its
// parameters, so it's the same request over any binding.
func (o *opInfo) ts(ex *opExample) string {
	args := &omap{}
	for _, p := range o.params {
		if ex != nil {
			if v, ok := ex.values[p.name]; ok {
				args.set(p.name, v)
			}
		}
	}
	api := "api"
	if o.api != nil {
		api = o.api.Name
	}
	call := api + "." + o.node.Name + "(" + tsValue(args, "") + ")"
	return "const result = await " + call + ";"
}

func tsValue(v any, indent string) string {
	switch v := v.(type) {
	case *omap:
		if len(v.keys) == 0 {
			return "{}"
		}
		var parts []string
		for _, k := range v.keys {
			parts = append(parts, indent+"  "+k+": "+tsValue(v.vals[k], indent+"  "))
		}
		return "{\n" + strings.Join(parts, ",\n") + ",\n" + indent + "}"
	case []any:
		var parts []string
		for _, x := range v {
			parts = append(parts, tsValue(x, indent))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return compact(v)
}

// response is the status and body an example answers with.
func (o *opInfo) response(ex *opExample) (status string, body any, ok bool) {
	switch {
	case ex == nil:
		return "", nil, false
	case ex.throws != nil:
		st := ""
		if a := annotation(nil, ex.throws, "status"); a != nil {
			if es := argExprs(a.Args()); len(es) > 0 {
				st = strings.TrimSpace(es[0].Syntax().Text())
			}
		}
		return st, ex.thrown, true
	case ex.hasRet:
		return "200", ex.returns, true
	}
	return "", nil, false
}

func renderAPI(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="node api" id="`, attr(Anchor(n)), `">`)
	c.title("")
	c.Prose(n)
	var facts []string
	if b := basePath(s, n); b != "" {
		facts = append(facts, `<dt>`+esc(label("docs.label.base_path"))+`</dt><dd><code>`+esc(b)+`</code></dd>`)
	}
	if w := wsRoute(n); w != "" {
		facts = append(facts, `<dt>`+esc(label("docs.label.socket"))+`</dt><dd><code>`+esc(w)+`</code></dd>`)
	}
	bs := s.apiBindings(n)
	rpcOnly := len(bs) > 0 && bs[0] != bindHTTP
	if !rpcOnly {
		facts = append(facts, `<dt>`+esc(label("docs.label.wire"))+`</dt><dd>`+esc(label("docs.label.wire_json"))+`</dd>`)
	}
	svcFacts := s.serviceFacts(n)
	if len(svcFacts) > 0 {
		c.Island("copy")
	}
	for _, a := range annotations(n) {
		if ownAnnotations[annName(a)] && annName(a) != "throws" {
			continue
		}
		facts = append(facts, `<dt>`+esc(sentenceCase(annName(a)))+`</dt><dd><code>`+s.highlightDecl(resolve.Site{File: n.Decl.File, N: a.Args().N, Scope: n.Decl.Scope}, false)+`</code></dd>`)
	}
	if len(facts) > 0 {
		c.W(`<dl class="facts">`, strings.Join(facts, ""), `</dl>`)
	}
	if len(svcFacts) > 0 {
		c.W(`<dl class="facts rpc-facts">`, strings.Join(svcFacts, ""), `</dl>`)
	}
	// An api bound to RPC: each binding's index, under a switch when it
	// has more than one.
	if len(bs) > 1 {
		c.bindingSwitch(bs)
	}
	for _, b := range bs {
		if b == bindHTTP {
			continue
		}
		c.bindOpen(bs, b)
		c.rpcIndex(n, b)
		c.bindClose(bs)
	}
	if len(bs) == 0 || !rpcOnly {
		c.bindOpen(bs, bindHTTP)
		c.httpIndex(n)
		c.bindClose(bs)
	}
	c.apiTypesList(n)
	c.W(`</section>`)
	return nil
}

// httpIndex writes an api's ops by resource, with their methods and
// paths.
func (c *Ctx) httpIndex(n *resolve.Node) {
	s := c.Site
	// The index: ops by resource, with their methods and paths.
	groups := map[string][]*resolve.Node{}
	var order []string
	rpcBound := len(s.apiBindings(n)) > 0
	for _, op := range n.Children {
		if s.kindPath(op) != "types.op" {
			continue
		}
		if rpcBound && !httpOr(s, op) {
			continue // carried by its RPC bindings alone
		}
		_, p := httpRoute(s, op)
		if p == "" {
			p = wsRoute(op)
		}
		res := resource(p)
		if _, ok := groups[res]; !ok {
			order = append(order, res)
		}
		groups[res] = append(groups[res], op)
	}
	// One table, a row heading each group, so the columns line up.
	c.W(`<div class="table-wrap"><table class="ops"><colgroup><col class="method-col"><col class="path-col"><col></colgroup><tbody>`)
	for _, res := range order {
		if res != "" && len(order) > 1 {
			id := Anchor(n) + "." + res
			*c.outline = append(*c.outline, Heading{ID: id, Title: res, Level: 2})
			c.W(`<tr class="ops-group" id="`, attr(id), `"><th colspan="3">`, esc(res), `</th></tr>`)
		}
		for _, op := range groups[res] {
			m, p := httpRoute(s, op)
			if m == "" {
				if w := wsRoute(op); w != "" {
					m, p = "WS", w
				}
			}
			c.W(`<tr><td class="method-col">`, methodHTML(m), `</td><td><code class="path">`, esc(p), `</code></td><td class="op-cell">`)
			c.Link(op, displayName(s, op))
			c.W(deprecatedBadge(op))
			if t := firstSentence(inlineParagraph(s.proseHTML(op))); t != "" {
				c.W(`<span class="op-line">`, t, `</span>`)
			}
			c.W(`</td></tr>`)
		}
	}
	c.W(`</tbody></table></div>`)
}

// apiTypesList writes the types and errors an api's ops name.
func (c *Ctx) apiTypesList(n *resolve.Node) {
	s := c.Site
	// The types and errors the ops name, directly or through fields.
	named := s.apiTypes(n)
	if len(named) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".types", label("docs.label.types"), "")
		c.W(`<ul class="refs api-types">`)
		for _, t := range named {
			c.W(`<li>`, s.linkHTML(t, `<code class="t">`+esc(displayName(s, t))+`</code>`, ""))
			if d := firstSentence(inlineParagraph(s.proseHTML(t))); d != "" {
				c.W(`<span class="op-line">`, d, `</span>`)
			}
			c.W(`</li>`)
		}
		c.W(`</ul>`)
	}
}

// resource is the first segment of a path, which groups ops in the index.
func resource(p string) string {
	for _, seg := range strings.Split(p, "/") {
		if seg != "" && !strings.HasPrefix(seg, ":") {
			return seg
		}
	}
	return ""
}

func methodHTML(m string) string {
	if m == "" {
		return ""
	}
	return `<span class="method m-` + strings.ToLower(attr(m)) + `">` + esc(m) + `</span>`
}

func renderOp(c *Ctx) error {
	n := c.Node
	s := c.Site
	o := s.readOp(n)
	c.W(`<section class="node op-page" id="`, attr(Anchor(n)), `">`)
	c.title("")
	// An op bound to RPC too: one contract, a switch for the parts each
	// binding shows differently.
	bs := s.opBindings(n)
	httpShown := len(bs) == 0 || bs[0] == bindHTTP
	if len(bs) > 1 {
		c.bindingSwitch(bs)
	}
	for _, b := range bs {
		if b == bindHTTP {
			continue
		}
		c.bindOpen(bs, b)
		c.rpcHeader(n, b)
		c.bindClose(bs)
	}
	if httpShown {
		c.bindOpen(bs, bindHTTP)
	}
	if !httpShown {
		// No HTTP route or socket: the HTTP header is left out.
	} else if o.method != "" || o.path != "" {
		full := o.base + o.path
		c.W(`<div class="endpoint">`, methodHTML(o.method), `<code class="path">`)
		if o.base != "" {
			c.W(`<span class="base">`, esc(o.base), `</span>`)
		}
		c.W(esc(o.path), `</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`, attr(full), `" aria-label="`, attr(label("docs.action.copy")), `">`, iconCopy, `</button></div>`)
		c.Island("copy")
	} else if o.ws != "" {
		c.W(`<div class="endpoint"><span class="method m-ws">WS</span><code class="path">`, esc(o.ws), `</code></div>`)
	}
	if httpShown {
		c.bindClose(bs)
	}
	if b := deprecatedBadge(n); b != "" {
		c.W(`<p class="deprecation">`, strings.TrimSpace(b), `</p>`)
	}
	c.Prose(n)

	// Facts: the transitions it performs, then cross-cutting behaviour.
	var facts []string
	if len(o.performs) > 0 {
		var ls []string
		for _, t := range o.performs {
			text := t.Name
			if t.Parent != nil {
				text = t.Parent.Name + "." + t.Name
			}
			ls = append(ls, s.linkHTML(t, `<code>`+esc(text)+`</code>`, ""))
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.performs"))+`</dt><dd>`+strings.Join(ls, ", ")+`</dd>`)
	}
	if len(o.realizes) > 0 {
		var ls []string
		for _, t := range o.realizes {
			ls = append(ls, s.linkHTML(t, esc(displayName(s, t)), ""))
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.realizes"))+`</dt><dd>`+strings.Join(ls, ", ")+`</dd>`)
	}
	for _, a := range o.crossCutting {
		v := ""
		if a.a.Args().N != nil {
			v = `<code>` + s.highlightDecl(resolve.Site{File: a.site.File, N: a.a.Args().N, Scope: a.site.Scope}, false) + `</code>`
		}
		if fl := flagsOf(a.a.Flags()); len(fl) > 0 {
			v = `<code>` + esc(strings.Join(fl, ", ")) + `</code> ` + v
		}
		if v == "" {
			v = `<code>@` + esc(a.name) + `</code>`
		}
		facts = append(facts, `<dt>`+esc(sentenceCase(a.name))+`</dt><dd>`+v+`</dd>`)
	}
	facts = append(facts, s.rpcFacts(n)...)
	if len(facts) > 0 {
		c.W(`<dl class="facts">`, strings.Join(facts, ""), `</dl>`)
	}

	// Each RPC binding's messages and errors.
	for _, b := range bs {
		if b == bindHTTP {
			continue
		}
		c.bindOpen(bs, b)
		c.rpcBody(n, b, b == bs[0])
		c.bindClose(bs)
	}
	if httpShown {
		c.bindOpen(bs, bindHTTP)
		c.httpBody(o)
		c.bindClose(bs)
	}

	c.opRules(n)
	c.usedBy(n)
	c.W(`</section>`)

	if c.isPage() {
		if httpShown {
			c.opAside(o, bs)
		}
		for _, b := range bs {
			if b != bindHTTP {
				c.Island("examples")
				c.Island("tryit")
				c.Aside(c.rpcAside(o, b, bs))
			}
		}
	}
	return nil
}

// httpBody writes an op's parameters, messages and responses as HTTP
// carries them.
func (c *Ctx) httpBody(o *opInfo) {
	s := c.Site
	n := o.node
	// Parameters, grouped by where they go.
	locs := []struct{ key, title string }{
		{"path", label("docs.heading.path_params")},
		{"query", label("docs.heading.query_params")},
		{"header", label("docs.heading.header_params")},
		{"", label("docs.heading.params")},
	}
	for _, l := range locs {
		var ps []opParam
		for _, p := range o.params {
			if p.loc == l.key && !(l.key == "" && o.method != "" && !p.typ.isScalar(s)) {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 {
			continue
		}
		c.Heading(c.Level+1, Anchor(n)+".params."+orDefault(l.key, "args"), l.title, "")
		c.paramsTable(ps)
	}
	for _, p := range o.params {
		if p.loc == "body" || p.loc == "" && o.method != "" && !p.typ.isScalar(s) {
			c.Heading(c.Level+1, Anchor(n)+".body", label("docs.heading.body"), ` <code class="t">`+p.typ.html+`</code>`)
			if p.doc != nil {
				c.W(`<div class="prose">`, s.renderText([]resolve.Site{{File: p.site.File, N: p.doc, Scope: p.site.Scope}}), `</div>`)
			}
			c.schemaTree(p.typ, nil, 0)
		}
	}

	// Messages of streams and sockets.
	if o.stream || o.streamIn || o.ws != "" {
		c.Heading(c.Level+1, Anchor(n)+".messages", label("docs.heading.messages"), "")
		c.W(`<div class="table-wrap"><table><thead><tr><th>`, esc(label("docs.col.direction")), `</th><th>`, esc(label("docs.col.type")), `</th><th>`, esc(label("docs.col.framing")), `</th></tr></thead><tbody>`)
		framing := label("docs.framing.sse")
		if o.ws != "" {
			framing = label("docs.framing.ws")
		}
		for _, p := range o.params {
			if p.typ.name == "Stream" && len(p.typ.args) > 0 {
				c.W(`<tr><td>`, esc(label("docs.direction.client")), `</td><td><code>`, p.typ.args[0].html, `</code></td><td>`, esc(framing), `</td></tr>`)
			}
		}
		if o.stream && len(o.returns.args) > 0 {
			c.W(`<tr><td>`, esc(label("docs.direction.server")), `</td><td><code>`, o.returns.args[0].html, `</code></td><td>`, esc(framing), `</td></tr>`)
		}
		c.W(`</tbody></table></div>`)
	}

	// Responses by outcome: the success type, then each error.
	c.Heading(c.Level+1, Anchor(n)+".responses", label("docs.heading.responses"), "")
	c.W(`<div class="responses">`)
	if o.returns.text != "" {
		st := ""
		if o.method != "" {
			st = "200"
		}
		c.W(`<div class="response"><div class="response-head">`)
		if st != "" {
			c.W(`<code class="status ok">`, st, `</code>`)
		}
		c.W(`<code class="t">`, o.returns.html, `</code></div>`)
		c.schemaTree(o.returns, nil, 0)
		c.W(`</div>`)
	}
	for _, e := range o.errors {
		c.W(`<div class="response" id="`, attr(Anchor(n)+".error."+e.typ.Name), `"><div class="response-head">`)
		if e.status != "" {
			c.W(`<code class="status err">`, esc(e.status), `</code>`)
		}
		if e.code != "" {
			c.W(s.linkHTML(e.typ, esc(displayName(s, e.typ)), ""), ` <code class="err-code">`, esc(e.code), `</code></div>`)
			if title := fieldString(e.typ, "title"); title != "" {
				c.W(`<p class="err-title">`, messageHTML(title), `</p>`)
			}
			var fs []string
			for _, f := range e.typ.Children {
				if s.kindPath(f) == "spec.field" {
					t := ""
					if es := argExprs(kindArgs(f)); len(es) > 0 {
						t = strings.TrimSpace(es[0].Syntax().Text())
					}
					fs = append(fs, `<code>`+esc(f.Name)+`</code> <code class="t">`+esc(t)+`</code>`)
				}
			}
			if len(fs) > 0 {
				c.W(`<p class="err-fields"><span class="label">`, esc(label("docs.label.fields")), `</span> `, strings.Join(fs, ", "), `</p>`)
			}
		} else {
			c.W(s.linkHTML(e.typ, `<code class="t">`+esc(e.typ.Name)+`</code>`, ""), `</div>`)
		}
		if d := s.proseHTML(e.typ); d != "" {
			c.W(`<div class="prose">`, d, `</div>`)
		}
		for _, cause := range e.causes {
			if s.kindPath(cause) == "spec.rule" {
				c.W(`<p class="cause sentence">`, s.sentence(cause), ` `, s.linkHTML(cause, "#", "rule-link"), `</p>`)
			} else {
				c.W(`<div class="cause"><span class="label">`, esc(label("docs.label.example")), `</span> `, s.linkHTML(cause, esc(displayName(s, cause)), ""), `</div>`)
			}
		}
		c.W(`</div>`)
	}
	c.W(`</div>`)
}

// opRules writes the rules on an op.
func (c *Ctx) opRules(n *resolve.Node) error {
	s := c.Site
	var rules []*resolve.Node
	s.Tree.Walk(func(x *resolve.Node) {
		if own(x) && s.kept(x) && s.kindPath(x) == "spec.rule" {
			if s.entryRealizes(n, x) {
				rules = append(rules, x)
				return
			}
			for _, t := range s.targets(x) {
				if t == n {
					rules = append(rules, x)
				}
			}
		}
	})
	if len(rules) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".rules", label("docs.heading.rules"), "")
		for _, r := range rules {
			d := c.at(r, c.Level+2)
			d.anchorSuffix = "--" + Anchor(n)
			if s.kindPath(r) == "spec.quality" {
				d.W(`<div class="rule"><p class="sentence">`, s.fullSentence(r), ` `, s.linkHTML(r, "#", "rule-link"), `</p></div>`)
				continue
			}
			if err := d.renderRef(); err != nil {
				return err
			}
		}
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// isScalar reports whether a type is a scalar, which an op without a
// location annotation takes as an argument rather than a body.
func (x texpr) isScalar(s *Site) bool {
	switch x.name {
	case "Int", "Float", "String", "Bool", "Decimal", "Timestamp", "Duration", "List":
		return true
	}
	return x.decl != nil && s.kindPath(x.decl) == "types.enum"
}

func (c *Ctx) paramsTable(ps []opParam) {
	s := c.Site
	hasDef, hasDoc := false, false
	for _, p := range ps {
		if p.def != nil {
			hasDef = true
		}
		if p.doc != nil {
			hasDoc = true
		}
	}
	if len(ps) < 3 {
		// A small table: each parameter as one line.
		c.W(`<ul class="lines">`)
		for _, p := range ps {
			name := p.name
			if p.loc == "header" {
				name = p.header
			}
			c.W(`<li><p class="line-head"><code class="fname">`, esc(name), `</code> <code>`, p.typ.html, `</code>`)
			if !p.optional {
				c.W(` <span class="badge req">`, esc(label("docs.badge.required")), `</span>`)
			}
			if p.def != nil {
				c.W(`<span class="sep">, </span>`, esc(label("docs.label.default")), ` <code>`, s.exprHTML(p.def, p.site), `</code>`)
			}
			c.W(`</p>`)
			if p.doc != nil {
				c.W(lineDoc(s.renderText([]resolve.Site{{File: p.site.File, N: p.doc, Scope: p.site.Scope}})))
			}
			c.W(`</li>`)
		}
		c.W(`</ul>`)
		return
	}
	c.W(`<div class="table-wrap"><table class="fields"><thead><tr><th>`, esc(label("docs.col.name")), `</th><th>`, esc(label("docs.col.type")), `</th>`)
	if hasDef {
		c.W(`<th>`, esc(label("docs.col.default")), `</th>`)
	}
	if hasDoc {
		c.W(`<th>`, esc(label("docs.col.description")), `</th>`)
	}
	c.W(`</tr></thead><tbody>`)
	for _, p := range ps {
		name := p.name
		if p.loc == "header" {
			name = p.header
		}
		c.W(`<tr><td><code class="fname">`, esc(name), `</code>`)
		if !p.optional {
			c.W(` <span class="badge req">`, esc(label("docs.badge.required")), `</span>`)
		}
		c.W(`</td><td><code>`, p.typ.html, `</code>`)
		if t := p.typ; len(t.args) == 1 && t.name == "List" {
			t = t.args[0]
			if t.decl != nil && s.kindPath(t.decl) == "types.enum" {
				c.W(`<div class="mark values">`)
				for _, v := range t.decl.Children {
					c.W(`<code>`, esc(v.Name), `</code> `)
				}
				c.W(`</div>`)
			}
		} else if t.decl != nil && s.kindPath(t.decl) == "types.enum" {
			c.W(`<div class="mark values">`)
			for _, v := range t.decl.Children {
				c.W(`<code>`, esc(v.Name), `</code> `)
			}
			c.W(`</div>`)
		}
		c.W(`</td>`)
		if hasDef {
			c.W(`<td>`)
			if p.def != nil {
				c.W(`<code>`, s.exprHTML(p.def, p.site), `</code>`)
			}
			c.W(`</td>`)
		}
		if hasDoc {
			c.W(`<td>`)
			if p.doc != nil {
				c.W(s.renderText([]resolve.Site{{File: p.site.File, N: p.doc, Scope: p.site.Scope}}))
			}
			c.W(`</td>`)
		}
		c.W(`</tr>`)
	}
	c.W(`</tbody></table></div>`)
}

// opAside writes the right column of an op page: the request example with
// its language tabs, example picker, copy and Try it, then the response.
func (c *Ctx) opAside(o *opInfo, bs []string) {
	s := c.Site
	c.Island("examples")
	c.Island("tryit")
	var b strings.Builder
	binding := ""
	if len(bs) > 1 {
		binding = ` data-binding="http"`
	}
	exs := o.examples
	var list []*opExample
	for i := range exs {
		list = append(list, &exs[i])
	}
	if len(list) == 0 {
		// No example: the messages as JSON built from their types.
		list = []*opExample{o.skeletonExample(s)}
	}
	b.WriteString(`<div class="examples" data-island="examples"` + binding + `>`)
	b.WriteString(`<div class="panel"><div class="panel-head"><div class="seg" role="tablist" aria-label="` + attr(label("docs.label.language")) + `">`)
	for i, lang := range []struct{ k, t string }{{"curl", label("docs.lang.curl")}, {"ts", label("docs.lang.ts")}, {"http", label("docs.lang.http")}} {
		sel := "false"
		if i == 0 {
			sel = "true"
		}
		b.WriteString(`<button type="button" role="tab" aria-selected="` + sel + `" data-lang="` + lang.k + `">` + esc(lang.t) + `</button>`)
	}
	b.WriteString(`</div>`)
	if len(exs) > 1 {
		b.WriteString(`<select class="picker" aria-label="` + attr(label("docs.label.example")) + `">`)
		for i, ex := range list {
			b.WriteString(`<option value="` + fmt.Sprint(i) + `">` + esc(displayName(s, ex.node)) + `</option>`)
		}
		b.WriteString(`</select>`)
	} else if len(exs) == 1 {
		b.WriteString(`<span class="picker-one">` + esc(displayName(s, exs[0].node)) + `</span>`)
	}
	b.WriteString(`</div>`)
	for i, ex := range list {
		hidden := ""
		if i > 0 {
			hidden = " hidden"
		}
		r := o.request(ex)
		id := ""
		if ex != nil && ex.node != nil {
			id = ` id="` + attr(Anchor(ex.node)) + `"`
		}
		b.WriteString(`<div class="ex"` + id + ` data-example="` + fmt.Sprint(i) + `"` + hidden + `>`)
		for j, form := range []struct{ k, code string }{{"curl", r.curl()}, {"ts", o.ts(ex)}, {"http", r.http()}} {
			h := ""
			if j > 0 {
				h = " hidden"
			}
			b.WriteString(`<div class="lang" data-lang="` + form.k + `"` + h + `><pre><code>` + highlightSnippet(form.k, form.code) + `</code></pre></div>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div class="panel-foot"><button type="button" class="icon-btn copy" data-island="copy" aria-label="` + attr(label("docs.action.copy")) + `">` + iconCopy + `</button>`)
	served := o.api != nil && s.Opts.ServedOps[o.api.Name+"."+o.node.Name]
	// The server's own API is tried only where that server serves the page.
	if o.method != "" && (served || o.api == nil || o.api.Name != ServerAPI) {
		b.WriteString(`<button type="button" class="btn primary" data-tryit>` + esc(label("docs.action.try")) + `</button>`)
	}
	b.WriteString(`</div></div>`)
	// Responses for the selected example.
	hasResp := false
	for _, ex := range list {
		if _, _, ok := o.response(ex); ok {
			hasResp = true
		}
	}
	if hasResp {
		b.WriteString(`<div class="panel response-panel">`)
		for i, ex := range list {
			st, body, ok := o.response(ex)
			hidden := ""
			if i > 0 {
				hidden = " hidden"
			}
			b.WriteString(`<div class="ex" data-example="` + fmt.Sprint(i) + `"` + hidden + `>`)
			if ok {
				cls := "ok"
				if ex.throws != nil {
					cls = "err"
				}
				b.WriteString(`<div class="panel-head"><span class="label">` + esc(label("docs.label.response")) + `</span><code class="status ` + cls + `">` + esc(st) + `</code></div>`)
				b.WriteString(`<pre><code>` + highlightSnippet("json", pretty(body)) + `</code></pre>`)
			}
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
	}
	// What Try it needs, as data.
	data := map[string]any{"method": o.method, "path": o.base + o.path}
	if o.server != "" {
		data["server"] = o.server
	}
	if served {
		// Sent through the generated client, as the frontend sends it.
		data["client"] = clientName(o.node.Name)
		data["stream"] = o.stream
	}
	var params []map[string]any
	for _, p := range o.params {
		params = append(params, map[string]any{"name": p.name, "in": p.loc, "header": p.header, "required": !p.optional})
	}
	data["params"] = params
	var exData []any
	for _, ex := range list {
		if ex == nil || ex.node == nil {
			exData = append(exData, map[string]any{})
			continue
		}
		exData = append(exData, ex.values)
	}
	data["examples"] = exData
	j, _ := json.Marshal(data)
	b.WriteString(`<script type="application/json" class="tryit-data">` + strings.ReplaceAll(string(j), "</", `<\/`) + `</script>`)
	b.WriteString(`</div>`)
	c.Aside(b.String())
}

// highlightSnippet colours a generated example by its language.
func highlightSnippet(lang, code string) string {
	switch lang {
	case "json":
		return highlightJSON(code)
	}
	var out []string
	for _, line := range strings.Split(code, "\n") {
		l := esc(line)
		switch lang {
		case "curl":
			if strings.HasPrefix(line, "curl") {
				l = `<span class="tk-kw">curl</span>` + esc(line[4:])
			}
		case "http":
			for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				if strings.HasPrefix(line, m+" ") {
					l = `<span class="tk-kw">` + m + `</span>` + esc(line[len(m):])
				}
			}
			if i := strings.Index(line, ": "); i > 0 && !strings.ContainsAny(line[:i], " {\"") {
				l = `<span class="tk-name">` + esc(line[:i]) + `</span>` + esc(line[i:])
			}
		case "ts":
			l = strings.Replace(l, "const ", `<span class="tk-kw">const</span> `, 1)
			l = strings.Replace(l, "await ", `<span class="tk-kw">await</span> `, 1)
		}
		if strings.Contains(l, "{") && lang != "ts" || lang == "http" && strings.HasPrefix(strings.TrimSpace(line), "\"") {
			l = highlightJSON(line)
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func highlightJSON(code string) string {
	var b strings.Builder
	in := false
	start := 0
	for i := 0; i < len(code); i++ {
		ch := code[i]
		if in {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				str := code[start : i+1]
				cls := "tk-str"
				rest := strings.TrimLeft(code[i+1:], " ")
				if strings.HasPrefix(rest, ":") {
					cls = "tk-name"
				}
				b.WriteString(`<span class="` + cls + `">` + esc(str) + `</span>`)
				in = false
			}
			continue
		}
		switch {
		case ch == '"':
			in, start = true, i
		case ch >= '0' && ch <= '9' || ch == '-':
			j := i
			for j < len(code) && strings.IndexByte("0123456789.-eE+", code[j]) >= 0 {
				j++
			}
			b.WriteString(`<span class="tk-num">` + esc(code[i:j]) + `</span>`)
			i = j - 1
		case strings.HasPrefix(code[i:], "true") || strings.HasPrefix(code[i:], "false") || strings.HasPrefix(code[i:], "null"):
			w := 4
			if code[i] == 'f' {
				w = 5
			}
			b.WriteString(`<span class="tk-kw">` + code[i:i+w] + `</span>`)
			i += w - 1
		case strings.IndexByte("{}[]:,", ch) >= 0:
			b.WriteString(`<span class="tk-punct">` + string(ch) + `</span>`)
		default:
			b.WriteString(esc(string(ch)))
		}
	}
	if in {
		b.WriteString(esc(code[start:]))
	}
	return b.String()
}

var _ = sort.Strings

// skeletonExample is the example an op without one shows: each required
// parameter and the response with a value of its type.
func (o *opInfo) skeletonExample(s *Site) *opExample {
	ex := &opExample{values: map[string]any{}}
	for _, p := range o.params {
		if p.optional || p.typ.name == "Stream" {
			continue
		}
		ex.values[p.name] = s.skeleton(p.typ, nil)
	}
	if o.returns.text != "" {
		t := o.returns
		if t.name == "Stream" && len(t.args) == 1 {
			t = t.args[0]
		}
		ex.returns, ex.hasRet = s.skeleton(t, nil), true
	}
	return ex
}

// skeleton is a value of a type as it travels on the wire: a record's
// fields, optional ones included, a list with one element, an enum's
// first variant.
func (s *Site) skeleton(x texpr, stack []*resolve.Node) any {
	switch x.name {
	case "String":
		return "string"
	case "Int":
		return json.Number("0")
	case "Float", "Number":
		return json.Number("0.0")
	case "Bool":
		return false
	case "Timestamp":
		return "2026-01-01T00:00:00Z"
	case "Duration":
		return "30s"
	case "Any":
		return &omap{}
	case "List", "Set", "Stream":
		if len(x.args) == 1 {
			return []any{s.skeleton(x.args[0], stack)}
		}
		return []any{}
	case "Map":
		m := &omap{}
		if len(x.args) == 2 {
			m.set("key", s.skeleton(x.args[1], stack))
		}
		return m
	}
	if len(x.union) > 0 {
		return s.skeleton(x.union[0], stack)
	}
	t := x.decl
	if t == nil || len(stack) > 4 {
		return nil
	}
	for _, st := range stack {
		if st == t {
			return nil
		}
	}
	if s.kindPath(t) == "types.enum" {
		for _, v := range t.Children {
			if v.Name != "" {
				return v.Name
			}
		}
		return ""
	}
	m := &omap{}
	for _, mem := range s.members(t, s.envFor(x)) {
		m.set(mem.name, s.skeleton(mem.typ, append(stack, t)))
	}
	return m
}

// deprecatedBadge marks a node its kind flags as deprecated.
func deprecatedBadge(n *resolve.Node) string {
	for _, f := range kindFlags(n) {
		if f == "deprecated" {
			return ` <span class="badge badge-deprecated">` + esc(label("docs.label.deprecated")) + `</span>`
		}
	}
	return ""
}

// apiTypes are the types and errors an api's ops name, in the params,
// results and throws, and through those types' fields, in the order met.
func (s *Site) apiTypes(api *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	var add func(x texpr, depth int)
	add = func(x texpr, depth int) {
		for _, a := range x.args {
			add(a, depth)
		}
		for _, u := range x.union {
			add(u, depth)
		}
		t := x.decl
		if t == nil || seen[t] || !own(t) || !s.isTypeDecl(t) || depth > 6 {
			return
		}
		seen[t] = true
		out = append(out, t)
		for _, m := range s.members(t, s.envFor(x)) {
			add(m.typ, depth+1)
		}
	}
	for _, op := range api.Children {
		if s.kindPath(op) != "types.op" {
			continue
		}
		o := s.readOp(op)
		for _, p := range o.params {
			add(p.typ, 0)
		}
		add(o.returns, 0)
		for _, e := range o.errors {
			if !seen[e.typ] {
				seen[e.typ] = true
				out = append(out, e.typ)
			}
		}
	}
	return out
}

// clientName is an op's method on the generated TS client: its name in
// lower camel case, as get_record is getRecord.
func clientName(op string) string {
	parts := strings.Split(op, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
