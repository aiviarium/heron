package docs

import (
	"encoding/json"
	"fmt"
	"github.com/aiviarium/heron/internal/core/jsonnames"
	"net/url"
	"strconv"
	"strings"

	"github.com/aiviarium/sparrow/go/schema"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/rpc"
)

// The RPC views: an op's gRPC, Connect, JSON-RPC and Sparrow bindings
// beside its HTTP one, a service's index, and a message's protobuf and JSON forms.
// The projection itself is internal/core/rpc's, the one the checker and
// the generators read.

// Binding keys, in the order the switch shows them.
const (
	bindHTTP    = "http"
	bindGRPC    = "grpc"
	bindConnect = "connect"
	bindJSONRPC = "jsonrpc"
	bindSparrow = "sparrow"
)

// opBindings lists the bindings an op has, in the switch's order. An op
// with no RPC binding has none listed, and reads as it always has.
func (s *Site) opBindings(op *resolve.Node) []string {
	m := s.rpc()
	me, jm, sm := m.Method(op), m.JSONMethod(op), m.SparrowMethod(op)
	if me == nil && jm == nil && sm == nil {
		return nil
	}
	var out []string
	if rpc.HTTPBound(s.Checker, op) {
		out = append(out, bindHTTP)
	}
	if me != nil && (me.Service.Has("grpc") || me.Service.Has("grpc_web")) {
		out = append(out, bindGRPC)
	}
	if me != nil && me.Service.Has("connect") {
		out = append(out, bindConnect)
	}
	if jm != nil {
		out = append(out, bindJSONRPC)
	}
	if sm != nil {
		out = append(out, bindSparrow)
	}
	return out
}

// apiBindings lists the bindings of an api's ops, in the switch's order.
func (s *Site) apiBindings(api *resolve.Node) []string {
	seen := map[string]bool{}
	for _, op := range api.Children {
		if s.kindPath(op) != "types.op" {
			continue
		}
		bs := s.opBindings(op)
		if bs == nil && (httpOr(s, op)) {
			seen[bindHTTP] = true
		}
		for _, b := range bs {
			seen[b] = true
		}
	}
	var out []string
	for _, b := range []string{bindHTTP, bindGRPC, bindConnect, bindJSONRPC, bindSparrow} {
		if seen[b] {
			out = append(out, b)
		}
	}
	return out
}

func httpOr(s *Site, op *resolve.Node) bool {
	m, _ := httpRoute(s, op)
	return m != "" || wsRoute(op) != ""
}

func bindingTitle(b string) string {
	return label("docs.binding." + b)
}

// bindingSwitch writes the switch between an op's or an api's bindings.
func (c *Ctx) bindingSwitch(bs []string) {
	c.Island("bindings")
	c.W(`<div class="binding-switch" data-island="bindings"><span class="label">`, esc(label("docs.label.binding")), `</span><div class="seg" role="tablist" aria-label="`, attr(label("docs.label.binding")), `">`)
	for i, b := range bs {
		c.W(`<button type="button" role="tab" aria-selected="`, strconv.FormatBool(i == 0), `" data-binding-tab="`, b, `">`, esc(bindingTitle(b)), `</button>`)
	}
	c.W(`</div></div>`)
}

// bindOpen opens the part of a page one binding shows, when there's a
// switch; bindClose closes it.
func (c *Ctx) bindOpen(bs []string, b string) {
	if len(bs) < 2 {
		return
	}
	hidden := ""
	if bs[0] != b {
		hidden = " hidden"
	}
	c.binding, c.bindingHidden = b, bs[0] != b
	c.W(`<div class="binding" data-binding="`, b, `"`, hidden, `>`)
}

func (c *Ctx) bindClose(bs []string) {
	if len(bs) >= 2 {
		c.binding, c.bindingHidden = "", false
		c.W(`</div>`)
	}
}

// subHeading writes a heading of a binding's part; the outline lists it
// under that binding, shown while the binding is.
func (c *Ctx) subHeading(id, title, extra string, _ bool) {
	c.Heading(c.Level+1, id, title, extra)
}

// kindBadge writes a method's streaming kind.
func kindBadge(k rpc.Kind) string {
	return `<span class="rpc-kind kind-` + k.Key() + `" title="` + attr(kindTitle(k)) + `">` + esc(kindTitle(k)) + `</span>`
}

func kindTitle(k rpc.Kind) string { return label("docs.kind." + k.Key()) }

// kindShort is a streaming kind as the navigation's fixed-width column
// shows it.
func kindShort(k rpc.Kind) string {
	return label("docs.kind_short." + k.Key())
}

// navMethod is what the navigation shows beside an op bound by RPC alone.
func (s *Site) navMethod(op *resolve.Node) string {
	m := s.rpc()
	if me := m.Method(op); me != nil {
		return kindShort(me.Kind)
	}
	if jm := m.JSONMethod(op); jm != nil {
		if jm.Notification {
			return label("docs.kind_short.notification")
		}
		return label("docs.kind_short.unary")
	}
	if sm := m.SparrowMethod(op); sm != nil {
		return sparrowShort(sm)
	}
	return ""
}

// sparrowShort is a Sparrow method's shape as the navigation shows it.
func sparrowShort(sm *rpc.SparrowMethod) string {
	if sm.Notification {
		return label("docs.kind_short.notification")
	}
	return kindShort(sm.Kind)
}

// bindMethods is what the navigation shows beside an op for each of its
// bindings, the HTTP method for HTTP.
func (s *Site) bindMethods(op *resolve.Node, httpMethod string) [][2]string {
	bs := s.opBindings(op)
	if len(bs) < 2 {
		return nil
	}
	var out [][2]string
	m := s.rpc()
	for _, b := range bs {
		switch b {
		case bindHTTP:
			out = append(out, [2]string{b, httpMethod})
		case bindGRPC, bindConnect:
			out = append(out, [2]string{b, kindShort(m.Method(op).Kind)})
		case bindJSONRPC:
			v := label("docs.kind_short.unary")
			if m.JSONMethod(op).Notification {
				v = label("docs.kind_short.notification")
			}
			out = append(out, [2]string{b, v})
		case bindSparrow:
			out = append(out, [2]string{b, sparrowShort(m.SparrowMethod(op))})
		}
	}
	return out
}

// hasBindings reports whether any op of the tree has an RPC binding.
func (s *Site) hasBindings() bool {
	m := s.rpc()
	return len(m.Services) > 0 || len(m.JSONAPIs) > 0 || len(m.Facets) > 0
}

// bindingScript applies the binding the reader picked last, kept in
// localStorage as heron-binding, before the page paints: the switch's
// tabs, the parts of the page and its outline, and the navigation's
// methods. docs.js keeps them in step when the reader picks another.
const bindingScript = `<script>(function(){var b;try{b=localStorage.getItem("heron-binding")}catch(e){}if(!b)return;` +
	`var tabs=document.querySelectorAll("[data-binding-tab]"),ok=false;` +
	`tabs.forEach(function(t){if(t.dataset.bindingTab===b)ok=true});` +
	`if(ok){tabs.forEach(function(t){var on=t.dataset.bindingTab===b;t.setAttribute("aria-selected",on?"true":"false");t.tabIndex=on?0:-1});` +
	`document.querySelectorAll("[data-binding]").forEach(function(p){p.hidden=p.dataset.binding!==b})}` +
	`document.querySelectorAll(".nav .m[data-m-"+b+"]").forEach(function(m){var v=m.getAttribute("data-m-"+b);m.textContent=v;m.className="m m-"+v.toLowerCase()})})()</script>`

// rpcHeader writes the head of a binding's part: the method's path or wire
// name with a copy button, and for gRPC and Connect its signature.
func (c *Ctx) rpcHeader(op *resolve.Node, b string) {
	s := c.Site
	m := s.rpc()
	switch b {
	case bindGRPC, bindConnect:
		me := m.Method(op)
		verb := "RPC"
		if b == bindConnect {
			verb = "POST"
		}
		c.W(`<div class="endpoint rpc-endpoint">`, methodHTML(verb), `<code class="path">`, esc(me.Path()), `</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`, attr(me.Path()), `" aria-label="`, attr(label("docs.action.copy")), `">`, iconCopy, `</button>`, kindBadge(me.Kind), `</div>`)
		c.Island("copy")
		c.W(`<pre class="rpc-sig"><code>`, c.signatureHTML(me, b), `</code></pre>`)
	case bindJSONRPC:
		jm := m.JSONMethod(op)
		c.W(`<div class="endpoint rpc-endpoint">`, methodHTML("JSON-RPC"), `<code class="path">`, esc(jm.Name), `</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`, attr(jm.Name), `" aria-label="`, attr(label("docs.action.copy")), `">`, iconCopy, `</button>`)
		if jm.Notification {
			c.W(`<span class="rpc-kind kind-notification">`, esc(label("docs.kind.notification")), `</span>`)
		}
		c.W(`</div>`)
		c.Island("copy")
	case bindSparrow:
		sm := m.SparrowMethod(op)
		path := sm.Service.Facet.Name + "/" + sm.Name
		c.W(`<div class="endpoint rpc-endpoint">`, methodHTML("Sparrow"), `<code class="path">`, esc(path), `</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`, attr(path), `" aria-label="`, attr(label("docs.action.copy")), `">`, iconCopy, `</button>`)
		if sm.Notification {
			c.W(`<span class="rpc-kind kind-notification">`, esc(label("docs.kind.notification")), `</span>`)
		} else {
			c.W(kindBadge(sm.Kind))
		}
		c.W(`</div>`)
		c.Island("copy")
		c.W(`<pre class="rpc-sig"><code>`, c.sparrowSignatureHTML(sm), `</code></pre>`)
	}
}

// sparrowSignatureHTML is a Sparrow method as getParcel(GetParcelInput)
// returns (stream ParcelEvent), with its facet's method id.
func (c *Ctx) sparrowSignatureHTML(sm *rpc.SparrowMethod) string {
	half := func(r rpc.SparrowRef, part string) string {
		kw := ""
		if r.Kind == schema.RefStream {
			kw = `<span class="tk-kw">stream</span> `
			r = *r.Elem
		}
		return kw + `<a href="#` + attr(Anchor(sm.Op)+".sparrow."+part) + `" class="code-ref"><span class="tk-type">` + esc(r.String()) + `</span></a>`
	}
	return `<span class="tk-num">` + strconv.FormatUint(sm.ID, 10) + `</span> <span class="tk-name">` + esc(sm.Name) + `</span><span class="tk-punct">(</span>` + half(sm.Input, "input") + `<span class="tk-punct">)</span> <span class="tk-kw">returns</span> <span class="tk-punct">(</span>` + half(sm.Output, "output") + `<span class="tk-punct">)</span>`
}

// sparrowFacts are the facts of a Sparrow method: its facet and method id,
// and its flags.
func (s *Site) sparrowFacts(sm *rpc.SparrowMethod) []string {
	f := sm.Service.Facet
	facts := []string{
		`<dt>` + esc(label("docs.label.facet")) + `</dt><dd><code>` + esc(f.Name) + `</code> <span class="mark">` + esc(label("docs.label.facet_id")) + ` <code>` + strconv.FormatUint(f.ID, 10) + `</code></span></dd>`,
		`<dt>` + esc(label("docs.label.method_id")) + `</dt><dd><code>` + strconv.FormatUint(sm.ID, 10) + `</code></dd>`,
	}
	if fl := sm.FlagNames(); len(fl) > 0 {
		var cs []string
		for _, x := range fl {
			cs = append(cs, `<code>`+esc(x)+`</code>`)
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.flags"))+`</dt><dd>`+strings.Join(cs, " ")+`</dd>`)
	}
	return facts
}

// sparrowRefHTML is a reference, a type of the facet linked to its page
// and marked with its TypeId.
func (s *Site) sparrowRefHTML(r rpc.SparrowRef) string {
	t := `<span class="tk-type">` + esc(r.String()) + `</span>`
	if n := r.Named(); n != nil && n.Node != nil && own(n.Node) {
		t = s.linkHTML(n.Node, t, "code-ref")
	}
	return t
}

// sparrowFields writes a struct's fields or a union's variants: tag, name,
// type, whether it's optional, docs.
func (c *Ctx) sparrowFields(t *rpc.SparrowType) {
	c.W(`<ul class="lines rpc-fields">`)
	for _, f := range t.Fields {
		c.W(`<li><p class="line-head"><span class="fnum" title="`, attr(label("docs.label.tag")), `">`, strconv.FormatUint(f.Tag, 10), `</span> <code class="fname">`, esc(f.Name), `</code> <code class="t">`, c.Site.sparrowRefHTML(f.Type), `</code>`)
		if f.Optional {
			c.W(` <span class="mark">optional</span>`)
		}
		if n := f.Type.Named(); n != nil {
			c.W(` <span class="mark">`, esc(label("docs.label.type_id")), ` <code>`, strconv.FormatUint(n.ID, 10), `</code></span>`)
		}
		c.W(`</p>`)
		if f.Doc != "" {
			c.W(`<div class="line-doc">`, esc(f.Doc), `</div>`)
		}
		c.W(`</li>`)
	}
	c.W(`</ul>`)
}

// sparrowHalf writes a method's input or output: the type with its
// TypeId, and a struct's fields.
func (c *Ctx) sparrowHalf(r rpc.SparrowRef, fields *rpc.SparrowType) {
	if r.Kind == schema.RefNamed && r.Type == nil && r.Builtin == schema.TypeUnit && fields == nil {
		c.W(`<p class="note">`, esc(label("docs.note.no_input")), `</p>`)
		return
	}
	c.W(`<p><code class="t">`, c.Site.sparrowRefHTML(r), `</code>`)
	if n := r.Named(); n != nil {
		c.W(` <span class="mark">`, esc(label("docs.label.type_id")), ` <code>`, strconv.FormatUint(n.ID, 10), `</code></span>`)
	}
	c.W(`</p>`)
	if fields != nil && len(fields.Fields) > 0 {
		c.sparrowFields(fields)
	}
}

// signatureHTML is a method as rpc Method(Request) returns (stream
// Response), each message linked to its part of the page.
func (c *Ctx) signatureHTML(me *rpc.Method, b string) string {
	id := Anchor(me.Op) + "." + b
	in := `<a href="#` + attr(id+".request") + `" class="code-ref"><span class="tk-type">` + esc(me.Request.Name) + `</span></a>`
	out := `<a href="#` + attr(id+".response") + `" class="code-ref"><span class="tk-type">` + esc(me.Response.Name) + `</span></a>`
	if me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
		in = `<span class="tk-kw">stream</span> ` + in
	}
	if me.Kind == rpc.ServerStreaming || me.Kind == rpc.BidiStreaming {
		out = `<span class="tk-kw">stream</span> ` + out
	}
	return `<span class="tk-kw">rpc</span> <span class="tk-name">` + esc(me.Name) + `</span><span class="tk-punct">(</span>` + in + `<span class="tk-punct">)</span> <span class="tk-kw">returns</span> <span class="tk-punct">(</span>` + out + `<span class="tk-punct">)</span>`
}

// rpcFacts are the facts a gRPC method adds to its op's.
func (s *Site) rpcFacts(op *resolve.Node) []string {
	me := s.rpc().Method(op)
	if me == nil {
		return nil
	}
	var facts []string
	if me.Idempotency != "" {
		facts = append(facts, `<dt>`+esc(label("docs.label.idempotency"))+`</dt><dd><code>`+esc(me.Idempotency)+`</code></dd>`)
	}
	if me.Timeout != "" {
		facts = append(facts, `<dt>`+esc(label("docs.label.timeout"))+`</dt><dd><code>`+esc(me.Timeout)+`</code></dd>`)
	}
	return facts
}

// rpcBody writes a binding's messages and errors.
func (c *Ctx) rpcBody(op *resolve.Node, b string, first bool) {
	s := c.Site
	m := s.rpc()
	id := Anchor(op) + "." + b
	switch b {
	case bindGRPC, bindConnect:
		me := m.Method(op)
		if facts := s.rpcFacts(op); len(facts) > 0 {
			c.W(`<dl class="facts">`, strings.Join(facts, ""), `</dl>`)
		}
		c.subHeading(id+".request", label("docs.heading.request"), ` <code class="t">`+esc(me.Request.Name)+`</code>`, first)
		c.messageFields(me.Request, b)
		if len(me.Metadata) > 0 {
			c.subHeading(id+".metadata", label("docs.heading.metadata"), "", first)
			c.W(`<ul class="lines rpc-fields">`)
			for _, md := range me.Metadata {
				name := md.Name
				if b == bindConnect {
					name = headerCase(md.Name)
				}
				c.W(`<li><p class="line-head"><code class="fname">`, esc(name), `</code> <code class="t">`, esc(md.Type), `</code>`)
				if md.Binary {
					c.W(` <span class="mark">`, esc(label("docs.label.base64")), `</span>`)
				}
				c.W(`</p>`)
				if md.Doc != "" {
					c.W(`<div class="line-doc">`, esc(md.Doc), `</div>`)
				}
				c.W(`</li>`)
			}
			c.W(`</ul>`)
		}
		c.subHeading(id+".response", label("docs.heading.response"), ` <code class="t">`+esc(me.Response.Name)+`</code>`, first)
		c.messageFields(me.Response, b)
		if me.Kind != rpc.Unary {
			c.subHeading(id+".messages", label("docs.heading.messages"), "", first)
			c.W(`<ul class="lines rpc-directions">`)
			if me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
				c.W(`<li><span class="label">`, esc(label("docs.direction.client")), `</span> <code>stream `, esc(me.Request.Name), `</code></li>`)
			}
			if me.Kind == rpc.ServerStreaming || me.Kind == rpc.BidiStreaming {
				c.W(`<li><span class="label">`, esc(label("docs.direction.server")), `</span> <code>stream `, esc(me.Response.Name), `</code></li>`)
			}
			c.W(`</ul>`)
			if b == bindConnect && (me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming) {
				c.W(`<p class="note">`, esc(label("docs.note.browser_streams")), `</p>`)
			}
		}
		c.rpcErrors(op, me.Errors, b, first)
	case bindJSONRPC:
		jm := m.JSONMethod(op)
		s.jsonCase = jm.API.Case
		defer func() { s.jsonCase = "" }()
		c.subHeading(id+".params", label("docs.heading.params"), "", first)
		c.jsonParams(jm)
		if !jm.Notification {
			c.subHeading(id+".result", label("docs.heading.result"), "", first)
			if jm.NullResult {
				c.W(`<p><code class="t">null</code></p>`)
			} else {
				o := s.readOp(op)
				c.W(`<p><code class="t">`, o.returns.html, `</code></p>`)
				c.schemaTree(o.returns, nil, 0)
			}
		} else {
			c.W(`<p class="note">`, esc(label("docs.note.notification")), `</p>`)
		}
		c.rpcErrors(op, jm.Errors, b, first)
	case bindSparrow:
		sm := m.SparrowMethod(op)
		c.W(`<dl class="facts">`, strings.Join(s.sparrowFacts(sm), ""), `</dl>`)
		c.subHeading(id+".input", label("docs.heading.input"), "", first)
		c.sparrowHalf(sm.Input, sm.InputType)
		c.subHeading(id+".output", label("docs.heading.output"), "", first)
		if sm.Notification {
			c.W(`<p class="note">`, esc(label("docs.note.sparrow_notification")), `</p>`)
		} else {
			c.sparrowHalf(sm.Output, nil)
		}
		c.rpcErrors(op, sm.Errors, b, first)
	}
}

// headerCase writes a metadata name as an HTTP header, as tenant is
// Tenant.
func headerCase(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}

// messageFields writes a message's fields as a list: number, name, type,
// JSON name, whether it's optional, docs.
func (c *Ctx) messageFields(msg *rpc.Message, b string) {
	if len(msg.Fields) == 0 {
		c.W(`<p class="note">`, esc(label("docs.note.empty_message")), `</p>`)
		return
	}
	c.W(`<ul class="lines rpc-fields">`)
	for _, f := range msg.Fields {
		c.W(`<li><p class="line-head"><span class="fnum" title="`, attr(label("docs.label.field_number")), `">`, strconv.Itoa(f.Number), `</span> `)
		if b == bindConnect {
			c.W(`<code class="fname">`, esc(f.JSON), `</code>`)
		} else {
			c.W(`<code class="fname">`, esc(f.Name), `</code>`)
		}
		c.W(` <code class="t">`, c.Site.protoTypeHTML(f), `</code>`)
		if f.Label == "optional" {
			c.W(` <span class="mark">optional</span>`)
		}
		if b == bindGRPC && f.JSON != f.Name {
			c.W(` <span class="mark json-name">`, esc(label("docs.label.json")), ` <code>`, esc(f.JSON), `</code></span>`)
		}
		if b == bindConnect && f.JSON != f.Name {
			c.W(` <span class="mark json-name">`, esc(label("docs.label.proto")), ` <code>`, esc(f.Name), `</code></span>`)
		}
		if f.Enum != nil {
			var vs []string
			for _, v := range f.Enum.Values[1:] {
				vs = append(vs, `<code>`+esc(v.Name)+`</code>`)
			}
			c.W(` <span class="mark values">`, strings.Join(vs, " "), `</span>`)
		}
		c.W(`</p>`)
		if f.Doc != "" {
			c.W(`<div class="line-doc">`, esc(f.Doc), `</div>`)
		}
		c.W(`</li>`)
	}
	c.W(`</ul>`)
}

// protoTypeHTML is a field's protobuf type, a message or enum with a page
// linked to it.
func (s *Site) protoTypeHTML(f *rpc.Field) string {
	t := f.Type
	label := ""
	if f.Label == "repeated" {
		label = `<span class="tk-kw">repeated</span> `
	}
	var n *resolve.Node
	switch {
	case f.Message != nil:
		n = f.Message.Node
	case f.Enum != nil:
		n = f.Enum.Node
	case f.MapValue != nil && f.MapValue.Message != nil:
		n = f.MapValue.Message.Node
	}
	if n != nil && own(n) {
		return label + s.linkHTML(n, `<span class="tk-type">`+esc(t)+`</span>`, "code-ref")
	}
	return label + `<span class="tk-type">` + esc(t) + `</span>`
}

// jsonParams writes a JSON-RPC method's params.
func (c *Ctx) jsonParams(jm *rpc.JSONMethod) {
	s := c.Site
	o := s.readOp(jm.Op)
	if len(o.params) == 0 {
		c.W(`<p class="note">`, esc(label("docs.note.no_params")), `</p>`)
		return
	}
	structure := label("docs.label.by_name")
	if jm.ByPosition {
		structure = label("docs.label.by_position")
	}
	if jm.Whole != nil {
		structure = label("docs.label.whole_params")
	}
	c.W(`<p class="rpc-structure"><span class="label">`, esc(label("docs.label.params_structure")), `</span> `, esc(structure), `</p>`)
	if jm.Whole != nil {
		for _, p := range o.params {
			c.W(`<p><code class="t">`, p.typ.html, `</code></p>`)
			c.schemaTree(p.typ, nil, 0)
		}
		return
	}
	c.W(`<ul class="lines rpc-fields">`)
	for i, p := range o.params {
		c.W(`<li><p class="line-head">`)
		if jm.ByPosition {
			c.W(`<span class="fnum">`, strconv.Itoa(i), `</span> `)
		}
		c.W(`<code class="fname">`, esc(jm.Params[i].Wire), `</code> <code class="t">`, p.typ.html, `</code>`)
		if !p.optional {
			c.W(` <span class="badge req">`, esc(label("docs.badge.required")), `</span>`)
		}
		c.W(`</p>`)
		if p.doc != nil {
			c.W(lineDoc(s.renderText([]resolve.Site{{File: p.site.File, N: p.doc, Scope: p.site.Scope}})))
		}
		c.W(`</li>`)
	}
	c.W(`</ul>`)
}

// rpcErrors writes the errors keyed by the binding's code, as a list.
func (c *Ctx) rpcErrors(op *resolve.Node, errs []rpc.Error, b string, first bool) {
	s := c.Site
	if len(errs) == 0 {
		return
	}
	c.subHeading(Anchor(op)+"."+b+".errors", label("docs.heading.errors"), "", first)
	c.W(`<div class="responses rpc-errors">`)
	for _, e := range errs {
		c.W(`<div class="response"><div class="response-head">`)
		switch b {
		case bindGRPC:
			code := e.GRPC
			if code == "" {
				code = "UNKNOWN"
			}
			c.W(`<code class="status err">`, esc(code), `</code>`)
		case bindConnect:
			code := e.GRPC
			if code == "" {
				code = "UNKNOWN"
			}
			c.W(`<code class="status err">`, esc(rpc.ConnectCode(code)), `</code> <code class="status-code">`, strconv.Itoa(rpc.ConnectStatus(code)), `</code>`)
		case bindJSONRPC:
			if e.JSONRPC != "" {
				c.W(`<code class="status err">`, esc(e.JSONRPC), `</code>`)
			}
		case bindSparrow:
			if e.Sparrow != "" {
				n, _ := rpc.SparrowCode(e.Sparrow)
				c.W(`<code class="status err" title="`, attr(label("docs.label.core_code")), `">`, esc(e.Sparrow), `</code> <code class="status-code">`, strconv.FormatUint(n, 10), `</code>`)
			} else if e.SparrowSigil != nil {
				c.W(`<code class="status err" title="`, attr(label("docs.label.sigil")), `">`, esc(e.SparrowSigil.Name), `</code> <code class="status-code">`, strconv.FormatUint(e.SparrowSigil.ID, 10), `</code>`)
			}
		}
		if e.IsNode {
			c.W(s.linkHTML(e.Node, esc(displayName(s, e.Node)), ""), ` <code class="err-code">`, esc(e.ID), `</code></div>`)
			if title := fieldString(e.Node, "title"); title != "" {
				c.W(`<p class="err-title">`, messageHTML(title), `</p>`)
			}
		} else {
			c.W(s.linkHTML(e.Node, `<code class="t">`+esc(e.Node.Name)+`</code>`, ""), `</div>`)
			if e.Message != "" {
				c.W(`<p class="err-title">`, esc(e.Message), `</p>`)
			}
		}
		if len(e.Fields) > 0 {
			var fs []string
			for _, f := range e.Fields {
				fs = append(fs, `<code>`+esc(f.Name)+`</code> <code class="t">`+esc(f.Type)+`</code>`)
			}
			where := label("docs.label.fields")
			switch {
			case b == bindJSONRPC:
				where = label("docs.label.error_data")
			case b == bindSparrow:
				where = label("docs.label.error_detail")
			case e.IsNode:
				where = label("docs.label.error_info")
			default:
				where = label("docs.label.error_detail")
			}
			c.W(`<p class="err-fields"><span class="label">`, esc(where), `</span> `, strings.Join(fs, ", "), `</p>`)
		}
		c.W(`</div>`)
	}
	c.W(`</div>`)
}

// --- examples ---

// rpcExample is one example as a binding's messages.
type rpcExample struct {
	name     string
	node     *resolve.Node
	requests []any // the request messages, in order
	replies  []any // the response messages, in order
	err      any   // the error, as the binding shows it
	status   string
	hasReply bool
}

// protoValue writes a Heron value as protobuf's JSON form of a field: by
// JSON name, 64-bit integers as strings, enum values by their protobuf
// names.
func protoValue(f *rpc.Field, v any) any {
	if f == nil {
		return v
	}
	if l, ok := v.([]any); ok && f.Label == "repeated" {
		out := make([]any, 0, len(l))
		for _, x := range l {
			out = append(out, protoScalar(f, x))
		}
		return out
	}
	if f.MapValue != nil {
		if mm, ok := v.(*omap); ok {
			out := &omap{}
			for _, k := range mm.keys {
				out.set(k, protoScalar(f.MapValue, mm.vals[k]))
			}
			return out
		}
	}
	return protoScalar(f, v)
}

func protoScalar(f *rpc.Field, v any) any {
	switch {
	case f.Message != nil:
		return protoMessage(f.Message, v)
	case f.Enum != nil:
		if s, ok := v.(string); ok {
			for _, ev := range f.Enum.Values {
				if ev.Variant == s {
					return ev.Name
				}
			}
		}
		return v
	}
	switch f.Scalar {
	case "int64", "uint64", "sint64", "fixed64", "sfixed64":
		if n, ok := v.(json.Number); ok {
			return string(n)
		}
	}
	return v
}

// protoMessage writes a value of a message's type by its fields.
func protoMessage(msg *rpc.Message, v any) any {
	mm, ok := v.(*omap)
	if !ok {
		return v
	}
	out := &omap{}
	for _, f := range msg.Fields {
		if x, ok := mm.vals[f.Name]; ok {
			if msg.Oneof != "" {
				out.set(f.JSON, protoValue(f, x))
				continue
			}
			out.set(f.JSON, protoValue(f, x))
		}
	}
	return out
}

// skeletonMessage is a message with a value of each field's type.
func skeletonMessage(msg *rpc.Message, depth int) *omap {
	out := &omap{}
	if depth > 4 {
		return out
	}
	for i, f := range msg.Fields {
		if msg.Oneof != "" && i > 0 {
			break
		}
		out.set(f.JSON, skeletonField(f, depth))
	}
	return out
}

func skeletonField(f *rpc.Field, depth int) any {
	one := func() any {
		switch {
		case f.Message != nil:
			return skeletonMessage(f.Message, depth+1)
		case f.Enum != nil:
			if len(f.Enum.Values) > 1 {
				return f.Enum.Values[1].Name
			}
			return f.Enum.Values[0].Name
		}
		switch f.Scalar {
		case "string":
			return "string"
		case "bool":
			return false
		case "bytes":
			return "YmFzZTY0"
		case "int64", "uint64", "sint64", "fixed64", "sfixed64":
			return "0"
		case "int32", "uint32", "sint32", "fixed32", "sfixed32":
			return json.Number("0")
		case "double", "float":
			return json.Number("0.0")
		case "google.protobuf.Timestamp":
			return "2026-01-01T00:00:00Z"
		case "google.protobuf.Duration":
			return "30s"
		case "google.protobuf.Struct":
			return &omap{}
		}
		return nil
	}
	if f.MapValue != nil {
		m := &omap{}
		m.set("key", skeletonField(f.MapValue, depth))
		return m
	}
	if f.Label == "repeated" {
		return []any{one()}
	}
	return one()
}

// grpcExamples are an op's examples as its method's messages.
func (s *Site) grpcExamples(o *opInfo, me *rpc.Method) []rpcExample {
	var out []rpcExample
	list := o.examples
	if len(list) == 0 {
		ex := rpcExample{requests: []any{skeletonMessage(me.Request, 0)}, replies: []any{skeletonMessage(me.Response, 0)}, hasReply: true}
		return []rpcExample{ex}
	}
	for i := range list {
		ex := &list[i]
		re := rpcExample{name: displayName(s, ex.node), node: ex.node}
		// The requests: one message, or one per element of a stream.
		streamed := ""
		for _, f := range me.Request.Fields {
			if f.Stream {
				streamed = f.Name
			}
		}
		if streamed != "" {
			if l, ok := ex.values[streamed].([]any); ok {
				var f *rpc.Field
				for _, x := range me.Request.Fields {
					if x.Name == streamed {
						f = x
					}
				}
				for _, el := range l {
					m := &omap{}
					m.set(f.JSON, protoScalar(f, el))
					re.requests = append(re.requests, m)
				}
			} else if v, ok := ex.values[streamed]; ok {
				m := &omap{}
				m.set(rpc.Camel(streamed), v)
				re.requests = append(re.requests, m)
			}
		} else {
			m := &omap{}
			for _, f := range me.Request.Fields {
				if v, ok := ex.values[f.Name]; ok {
					m.set(f.JSON, protoValue(f, v))
				}
			}
			re.requests = append(re.requests, m)
		}
		switch {
		case ex.throws != nil:
			re.err = ex.thrown
		case ex.hasRet:
			re.hasReply = true
			if len(me.Response.Fields) == 0 {
				re.replies = append(re.replies, &omap{})
				break
			}
			f := me.Response.Fields[0]
			if l, ok := ex.returns.([]any); ok && f.Stream {
				for _, el := range l {
					m := &omap{}
					m.set(f.JSON, protoScalar(f, el))
					re.replies = append(re.replies, m)
				}
			} else {
				m := &omap{}
				m.set(f.JSON, protoValue(f, ex.returns))
				re.replies = append(re.replies, m)
			}
		}
		out = append(out, re)
	}
	return out
}

// errorFor finds the projection of the error an example throws.
func errorFor(errs []rpc.Error, n *resolve.Node) *rpc.Error {
	for i := range errs {
		if errs[i].Node == n {
			return &errs[i]
		}
	}
	return nil
}

// errorFields is an example's thrown error's fields, without its message.
func errorFields(v any) *omap {
	out := &omap{}
	if mm, ok := v.(*omap); ok {
		for _, k := range mm.keys {
			if k != "message" {
				out.set(k, mm.vals[k])
			}
		}
	}
	return out
}

func errorMessage(e *rpc.Error, v any) string {
	if mm, ok := v.(*omap); ok {
		if m, ok := mm.vals["message"].(string); ok {
			return m
		}
	}
	if e != nil {
		return e.Message
	}
	return ""
}

// serverOf is the first server URL of an op's api.
func (s *Site) serverOf(op *resolve.Node) string {
	m := s.rpc()
	if me := m.Method(op); me != nil && len(me.Service.Servers) > 0 {
		return me.Service.Servers[0]
	}
	if jm := m.JSONMethod(op); jm != nil && len(jm.API.Servers) > 0 {
		return jm.API.Servers[0]
	}
	return ""
}

// hostPort is a server URL as grpcurl dials it.
func hostPort(server string) string {
	if server == "" {
		return "$HOST:443"
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return server
	}
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "http" {
		return u.Host + ":80"
	}
	return u.Host + ":443"
}

func orBase(server string) string {
	if server == "" {
		return "$BASE_URL"
	}
	return strings.TrimRight(server, "/")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// grpcurl is an example's call with grpcurl.
func grpcurl(me *rpc.Method, ex rpcExample, server string, md [][2]string) string {
	var b strings.Builder
	b.WriteString("grpcurl")
	if strings.HasPrefix(server, "http://") {
		b.WriteString(" -plaintext")
	}
	for _, h := range md {
		fmt.Fprintf(&b, " \\\n  -H %s", shellQuote(h[0]+": "+h[1]))
	}
	if len(ex.requests) > 1 || me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
		fmt.Fprintf(&b, " \\\n  -d @ %s \\\n  %s/%s <<'EOF'\n", hostPort(server), me.Service.FullName(), me.Name)
		for _, r := range ex.requests {
			b.WriteString(compact(r) + "\n")
		}
		b.WriteString("EOF")
		return b.String()
	}
	req := "{}"
	if len(ex.requests) == 1 {
		req = compact(ex.requests[0])
	}
	fmt.Fprintf(&b, " \\\n  -d %s \\\n  %s %s/%s", shellQuote(req), hostPort(server), me.Service.FullName(), me.Name)
	return b.String()
}

// bufCurl is an example's call with buf curl, against the generated
// .proto files.
func bufCurl(me *rpc.Method, ex rpcExample, server string, md [][2]string, protocol string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "buf curl --protocol %s --schema .heron/proto", protocol)
	if protocol == "grpc" {
		b.WriteString(" --http2-prior-knowledge")
	}
	for _, h := range md {
		fmt.Fprintf(&b, " \\\n  -H %s", shellQuote(h[0]+": "+h[1]))
	}
	if len(ex.requests) > 1 || me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
		fmt.Fprintf(&b, " \\\n  -d @- \\\n  %s%s <<'EOF'\n", orBase(server), me.Path())
		for _, r := range ex.requests {
			b.WriteString(compact(r) + "\n")
		}
		b.WriteString("EOF")
		return b.String()
	}
	req := "{}"
	if len(ex.requests) == 1 {
		req = compact(ex.requests[0])
	}
	fmt.Fprintf(&b, " \\\n  -d %s \\\n  %s%s", shellQuote(req), orBase(server), me.Path())
	return b.String()
}

// envelope is a Connect stream message's 5-byte prefix, as printf writes
// it: flags 0 and the big-endian length.
func envelope(body string) string {
	n := len(body)
	return fmt.Sprintf(`\x00\x%02x\x%02x\x%02x\x%02x`, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// connectCurl is an example's call over Connect with curl: a unary call as
// JSON, a no-side-effects one as GET, a server stream enveloped.
func connectCurl(me *rpc.Method, ex rpcExample, server string, md [][2]string, get bool) string {
	var b strings.Builder
	req := "{}"
	if len(ex.requests) > 0 {
		req = compact(ex.requests[0])
	}
	u := orBase(server) + me.Path()
	if get {
		fmt.Fprintf(&b, "curl --get %q", u)
		for _, h := range md {
			fmt.Fprintf(&b, " \\\n  -H %q", headerCase(h[0])+": "+h[1])
		}
		fmt.Fprintf(&b, " \\\n  --data-urlencode 'encoding=json' \\\n  --data-urlencode %s \\\n  --data-urlencode 'connect=v1'", shellQuote("message="+req))
		return b.String()
	}
	fmt.Fprintf(&b, "curl -X POST %q", u)
	for _, h := range md {
		fmt.Fprintf(&b, " \\\n  -H %q", headerCase(h[0])+": "+h[1])
	}
	if me.Kind == rpc.Unary {
		fmt.Fprintf(&b, " \\\n  -H \"Content-Type: application/json\" \\\n  -d %s", shellQuote(req))
		return b.String()
	}
	if me.Timeout != "" {
		fmt.Fprintf(&b, " \\\n  -H \"Connect-Timeout-Ms: %d\"", durationMs(me.Timeout))
	}
	fmt.Fprintf(&b, " \\\n  -H \"Content-Type: application/connect+json\" \\\n  --data-binary @<(printf '%s%%s' %s)", envelope(req), shellQuote(req))
	return b.String()
}

// durationMs reads a duration such as "5s" or "200ms" as milliseconds.
func durationMs(d string) int {
	n, unit := 0, ""
	for i, ch := range d {
		if ch < '0' || ch > '9' {
			unit = d[i:]
			break
		}
		n = n*10 + int(ch-'0')
	}
	switch unit {
	case "ms":
		return n
	case "m":
		return n * 60000
	case "h":
		return n * 3600000
	}
	return n * 1000
}

// connectTS is an example's call through a Connect client in TS.
func connectTS(me *rpc.Method, ex rpcExample, server string) string {
	svc := me.Service.Name
	file := strings.TrimSuffix(rpc.FilePath(me.Service.Package), ".proto") + "_pb"
	method := clientName(rpc.Snake(me.Name))
	base := orBase(server)
	var b strings.Builder
	b.WriteString(`import { createClient } from "@connectrpc/connect";` + "\n")
	if me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
		b.WriteString(`import { createGrpcTransport } from "@connectrpc/connect-node";` + "\n")
	} else {
		b.WriteString(`import { createConnectTransport } from "@connectrpc/connect-web";` + "\n")
	}
	fmt.Fprintf(&b, "import { %s } from \"./gen/%s\";\n\n", svc, file)
	if me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
		fmt.Fprintf(&b, "const client = createClient(%s, createGrpcTransport({ baseUrl: %q }));\n", svc, base)
	} else {
		fmt.Fprintf(&b, "const client = createClient(%s, createConnectTransport({ baseUrl: %q }));\n", svc, base)
	}
	req := func(v any) string { return tsValue(v, "") }
	first := any(&omap{})
	if len(ex.requests) > 0 {
		first = ex.requests[0]
	}
	switch me.Kind {
	case rpc.Unary:
		fmt.Fprintf(&b, "const res = await client.%s(%s);", method, req(first))
	case rpc.ServerStreaming:
		fmt.Fprintf(&b, "for await (const res of client.%s(%s)) {\n  console.log(res);\n}", method, req(first))
	default:
		b.WriteString("async function* requests() {\n")
		for _, r := range ex.requests {
			fmt.Fprintf(&b, "  yield %s;\n", tsValue(r, "  "))
		}
		b.WriteString("}\n")
		if me.Kind == rpc.ClientStreaming {
			fmt.Fprintf(&b, "const res = await client.%s(requests());", method)
		} else {
			fmt.Fprintf(&b, "for await (const res of client.%s(requests())) {\n  console.log(res);\n}", method)
		}
	}
	return b.String()
}

// jsonEnvelope is a JSON-RPC request for an example's params.
func jsonEnvelope(jm *rpc.JSONMethod, params any) *omap {
	env := &omap{}
	if jm.API.Encoding != "cbor" {
		env.set("jsonrpc", "2.0")
	}
	if !jm.Notification {
		env.set("id", json.Number("1"))
	}
	env.set("method", jm.Name)
	if params != nil {
		env.set("params", params)
	}
	return env
}

// wireValue writes an example's value with each record's fields by their
// names in JSON, as std/json's @key and @keys and the api's case say.
func (s *Site) wireValue(x texpr, v any, apiCase string) any {
	switch val := v.(type) {
	case []any:
		el := x
		if len(x.args) == 1 {
			el = x.args[0]
		}
		out := make([]any, len(val))
		for i, e := range val {
			out[i] = s.wireValue(el, e, apiCase)
		}
		return out
	case *omap:
		t := x.decl
		if t == nil || !s.isTypeDecl(t) {
			return v
		}
		ms := s.members(t, s.envFor(x))
		out := &omap{}
		for _, k := range val.keys {
			name, ft := k, texpr{}
			for _, m := range ms {
				if m.name == k {
					name, ft = jsonnames.Member(m.node, t, apiCase), m.typ
				}
			}
			out.set(name, s.wireValue(ft, val.vals[k], apiCase))
		}
		return out
	}
	return v
}

// jsonParamsOf is an example's params as the method sends them.
func (s *Site) jsonParamsOf(jm *rpc.JSONMethod, o *opInfo, ex *opExample) any {
	if jm.Whole != nil {
		if ex != nil {
			for _, p := range o.params {
				if v, ok := ex.values[p.name]; ok && p.name == jm.Whole.Name {
					return s.wireValue(p.typ, v, jm.API.Case)
				}
			}
		}
		return &omap{}
	}
	if len(o.params) == 0 {
		return nil
	}
	if jm.ByPosition {
		var out []any
		for _, p := range o.params {
			v, ok := any(nil), false
			if ex != nil {
				v, ok = ex.values[p.name]
			}
			if !ok {
				break
			}
			out = append(out, s.wireValue(p.typ, v, jm.API.Case))
		}
		if out == nil {
			out = []any{}
		}
		return out
	}
	m := &omap{}
	for i, p := range o.params {
		if ex == nil {
			continue
		}
		if v, ok := ex.values[p.name]; ok {
			m.set(jm.Params[i].Wire, s.wireValue(p.typ, v, jm.API.Case))
		}
	}
	return m
}

// framed writes a message as its api's framing carries it.
func framed(j *rpc.JSONAPI, body string, server string) string {
	switch j.Framing {
	case "content_length":
		return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	case "length_prefixed":
		return fmt.Sprintf("[length %d]%s", len(body), body)
	case "websocket":
		return body
	}
	path := "/"
	if u, err := url.Parse(server); err == nil && u.Path != "" {
		path = u.Path
	}
	return fmt.Sprintf("POST %s HTTP/1.1\nContent-Type: application/json\n\n%s", path, body)
}

// rpcAside writes the right column of a binding's part: each example's
// call in the binding's forms, its messages and its response, and Try it
// where a browser can make the call.
func (c *Ctx) rpcAside(o *opInfo, b string, bs []string) string {
	if b == bindSparrow {
		// Sparrow's examples and Try it are left to its own tools.
		return ""
	}
	s := c.Site
	m := s.rpc()
	op := o.node
	server := s.serverOf(op)
	hidden := ""
	if len(bs) > 1 && bs[0] != b {
		hidden = " hidden"
	}
	type form struct{ k, t string }
	var forms []form
	var exs []rpcExample
	var me *rpc.Method
	var jm *rpc.JSONMethod
	var md [][2]string
	get := false
	switch b {
	case bindGRPC:
		me = m.Method(op)
		forms = []form{{"grpcurl", "grpcurl"}, {"bufcurl", "buf curl"}}
		exs = s.grpcExamples(o, me)
	case bindConnect:
		me = m.Method(op)
		forms = []form{{"curl", label("docs.lang.curl")}, {"ts", label("docs.lang.ts")}}
		get = me.Idempotency == "NO_SIDE_EFFECTS" && me.Kind == rpc.Unary
		if get {
			forms = append(forms, form{"get", label("docs.lang.get")})
		}
		forms = append(forms, form{"bufcurl", "buf curl"})
		exs = s.grpcExamples(o, me)
	case bindJSONRPC:
		jm = m.JSONMethod(op)
		if jm.API.Framing == "http" {
			forms = append(forms, form{"curl", label("docs.lang.curl")})
		}
		forms = append(forms, form{"raw", label("docs.lang.raw")})
	}
	if me != nil {
		for _, x := range me.Metadata {
			if len(o.examples) > 0 {
				for _, ex := range o.examples {
					if v, ok := ex.values[x.Param]; ok {
						md = append(md, [2]string{x.Name, scalar(v)})
						break
					}
				}
			}
		}
	}
	// JSON-RPC examples are the op's own, as envelopes.
	type jsonEx struct {
		name   string
		node   *resolve.Node
		req    *omap
		resp   *omap
		status string
	}
	var jexs []jsonEx
	if jm != nil {
		list := []*opExample{}
		for i := range o.examples {
			list = append(list, &o.examples[i])
		}
		if len(list) == 0 {
			list = []*opExample{o.skeletonExample(s)}
		}
		for _, ex := range list {
			je := jsonEx{req: jsonEnvelope(jm, s.jsonParamsOf(jm, o, ex))}
			if ex.node != nil {
				je.name, je.node = displayName(s, ex.node), ex.node
			}
			if !jm.Notification {
				r := &omap{}
				if jm.API.Encoding != "cbor" {
					r.set("jsonrpc", "2.0")
				}
				r.set("id", json.Number("1"))
				switch {
				case ex.throws != nil:
					e := errorFor(jm.Errors, ex.throws)
					eo := &omap{}
					code := any(json.Number("-32000"))
					if e != nil && e.JSONRPC != "" {
						code = json.Number(e.JSONRPC)
					}
					eo.set("code", code)
					eo.set("message", errorMessage(e, ex.thrown))
					data := errorFields(ex.thrown)
					if e != nil && e.IsNode {
						d := &omap{}
						d.set("id", e.ID)
						for _, k := range data.keys {
							d.set(k, data.vals[k])
						}
						data = d
					}
					if len(data.keys) > 0 {
						eo.set("data", data)
					}
					r.set("error", eo)
					je.status = "error"
				case ex.hasRet:
					r.set("result", s.wireValue(o.returns, ex.returns, jm.API.Case))
				case jm.NullResult:
					r.set("result", nil)
				}
				je.resp = r
			}
			jexs = append(jexs, je)
		}
	}
	n := len(exs)
	if jm != nil {
		n = len(jexs)
	}
	var w strings.Builder
	w.WriteString(`<div class="examples rpc-examples" data-island="examples" data-binding="` + b + `"` + hidden + `>`)
	w.WriteString(`<div class="panel"><div class="panel-head"><div class="seg" role="tablist" aria-label="` + attr(label("docs.label.language")) + `">`)
	for i, f := range forms {
		w.WriteString(`<button type="button" role="tab" aria-selected="` + strconv.FormatBool(i == 0) + `" data-lang="` + f.k + `">` + esc(f.t) + `</button>`)
	}
	w.WriteString(`</div>`)
	names := func(i int) (string, *resolve.Node) {
		if jm != nil {
			return jexs[i].name, jexs[i].node
		}
		return exs[i].name, exs[i].node
	}
	if n > 1 {
		w.WriteString(`<select class="picker" aria-label="` + attr(label("docs.label.example")) + `">`)
		for i := 0; i < n; i++ {
			nm, _ := names(i)
			w.WriteString(`<option value="` + strconv.Itoa(i) + `">` + esc(nm) + `</option>`)
		}
		w.WriteString(`</select>`)
	} else if n == 1 {
		if nm, _ := names(0); nm != "" {
			w.WriteString(`<span class="picker-one">` + esc(nm) + `</span>`)
		}
	}
	w.WriteString(`</div>`)
	for i := 0; i < n; i++ {
		hid := ""
		if i > 0 {
			hid = " hidden"
		}
		id := ""
		if _, node := names(i); node != nil && bs[0] == b && b != bindHTTP {
			id = ` id="` + attr(Anchor(node)) + `"`
		}
		w.WriteString(`<div class="ex"` + id + ` data-example="` + strconv.Itoa(i) + `"` + hid + `>`)
		for j, f := range forms {
			code, lang := "", "json"
			switch {
			case me != nil && f.k == "grpcurl":
				code, lang = grpcurl(me, exs[i], server, md), "curl"
			case me != nil && f.k == "bufcurl":
				proto := "grpc"
				if b == bindConnect {
					proto = "connect"
				}
				code, lang = bufCurl(me, exs[i], server, md, proto), "curl"
			case me != nil && f.k == "curl":
				code, lang = connectCurl(me, exs[i], server, md, false), "curl"
			case me != nil && f.k == "get":
				code, lang = connectCurl(me, exs[i], server, md, true), "curl"
			case me != nil && f.k == "ts":
				code, lang = connectTS(me, exs[i], server), "ts"
			case jm != nil && f.k == "curl":
				code = fmt.Sprintf("curl -X POST %q \\\n  -H \"Content-Type: application/json\" \\\n  -d %s", orBase(server), shellQuote(compact(jexs[i].req)))
				lang = "curl"
			case jm != nil && f.k == "raw":
				code, lang = framed(jm.API, pretty(jexs[i].req), server), "http"
			}
			h := ""
			if j > 0 {
				h = " hidden"
			}
			hl := highlightSnippet(lang, code)
			if lang == "curl" {
				hl = highlightShell(code)
			}
			w.WriteString(`<div class="lang" data-lang="` + f.k + `"` + h + `><pre><code>` + hl + `</code></pre></div>`)
		}
		// A stream's requests, in order.
		if me != nil && len(exs[i].requests) > 1 {
			w.WriteString(`<div class="rpc-seq"><span class="label">` + esc(label("docs.direction.client")) + `</span><ol>`)
			for _, r := range exs[i].requests {
				w.WriteString(`<li><pre><code>` + highlightJSON(compact(r)) + `</code></pre></li>`)
			}
			w.WriteString(`</ol></div>`)
		}
		w.WriteString(`</div>`)
	}
	w.WriteString(`<div class="panel-foot"><button type="button" class="icon-btn copy" data-island="copy" aria-label="` + attr(label("docs.action.copy")) + `">` + iconCopy + `</button>`)
	tryable := server != "" && (b == bindConnect && (me.Kind == rpc.Unary || me.Kind == rpc.ServerStreaming) || b == bindJSONRPC && jm.API.Framing == "http" && jm.API.Encoding == "json")
	if tryable {
		w.WriteString(`<button type="button" class="btn primary" data-tryit>` + esc(label("docs.action.try")) + `</button>`)
	}
	w.WriteString(`</div></div>`)
	// The responses.
	w.WriteString(`<div class="panel response-panel">`)
	for i := 0; i < n; i++ {
		hid := ""
		if i > 0 {
			hid = " hidden"
		}
		w.WriteString(`<div class="ex" data-example="` + strconv.Itoa(i) + `"` + hid + `>`)
		switch {
		case jm != nil:
			if jexs[i].resp == nil {
				w.WriteString(`<div class="panel-head"><span class="label">` + esc(label("docs.label.response")) + `</span><span class="mark">` + esc(label("docs.kind.notification")) + `</span></div>`)
				w.WriteString(`<p class="note">` + esc(label("docs.note.notification")) + `</p>`)
				break
			}
			cls := "ok"
			st := "result"
			if jexs[i].status != "" {
				cls, st = "err", "error"
			}
			w.WriteString(`<div class="panel-head"><span class="label">` + esc(label("docs.label.response")) + `</span><code class="status ` + cls + `">` + st + `</code></div>`)
			w.WriteString(`<pre><code>` + highlightJSON(pretty(jexs[i].resp)) + `</code></pre>`)
		case exs[i].err != nil:
			e := errorFor(me.Errors, s.thrownBy(o, exs[i].node))
			code := "UNKNOWN"
			if e != nil && e.GRPC != "" {
				code = e.GRPC
			}
			var body *omap
			if b == bindConnect {
				body = connectError(me, e, exs[i].err, code)
				w.WriteString(`<div class="panel-head"><span class="label">` + esc(label("docs.label.response")) + `</span><code class="status err">` + strconv.Itoa(rpc.ConnectStatus(code)) + `</code> <code class="status err">` + esc(rpc.ConnectCode(code)) + `</code></div>`)
			} else {
				body = grpcStatus(me, e, exs[i].err, code)
				w.WriteString(`<div class="panel-head"><span class="label">` + esc(label("docs.label.status")) + `</span><code class="status err">` + esc(code) + `</code></div>`)
			}
			w.WriteString(`<pre><code>` + highlightJSON(pretty(body)) + `</code></pre>`)
		case exs[i].hasReply:
			ok := "OK"
			if b == bindConnect {
				ok = "200"
			}
			w.WriteString(`<div class="panel-head"><span class="label">` + esc(label("docs.label.response")) + `</span><code class="status ok">` + ok + `</code>`)
			if len(exs[i].replies) > 1 || me.Kind == rpc.ServerStreaming || me.Kind == rpc.BidiStreaming {
				w.WriteString(`<span class="mark">` + esc(label("docs.direction.server")) + `</span></div><ol class="rpc-seq">`)
				for _, r := range exs[i].replies {
					w.WriteString(`<li><pre><code>` + highlightJSON(pretty(r)) + `</code></pre></li>`)
				}
				w.WriteString(`</ol>`)
			} else {
				w.WriteString(`</div>`)
				for _, r := range exs[i].replies {
					w.WriteString(`<pre><code>` + highlightJSON(pretty(r)) + `</code></pre>`)
				}
			}
		}
		w.WriteString(`</div>`)
	}
	w.WriteString(`</div>`)
	if tryable {
		data := map[string]any{"protocol": b, "server": server}
		var reqs []any
		if me != nil {
			data["path"] = me.Path()
			data["stream"] = me.Kind == rpc.ServerStreaming
			for _, ex := range exs {
				if len(ex.requests) > 0 {
					reqs = append(reqs, ex.requests[0])
				} else {
					reqs = append(reqs, &omap{})
				}
			}
		} else {
			data["method"] = jm.Name
			for _, ex := range jexs {
				reqs = append(reqs, ex.req)
			}
		}
		data["requests"] = reqs
		j, _ := json.Marshal(data)
		w.WriteString(`<script type="application/json" class="tryit-data">` + strings.ReplaceAll(string(j), "</", `<\/`) + `</script>`)
	}
	w.WriteString(`</div>`)
	return w.String()
}

// thrownBy is the error an op example throws.
func (s *Site) thrownBy(o *opInfo, ex *resolve.Node) *resolve.Node {
	for _, e := range o.examples {
		if e.node == ex {
			return e.throws
		}
	}
	return nil
}

// grpcStatus is an error as a gRPC status, its detail in protobuf's JSON.
func grpcStatus(me *rpc.Method, e *rpc.Error, thrown any, code string) *omap {
	st := &omap{}
	st.set("code", code)
	st.set("message", errorMessage(e, thrown))
	if d := errorDetail(me, e, thrown, true); d != nil {
		st.set("details", []any{d})
	}
	return st
}

// connectError is an error as Connect's JSON body.
func connectError(me *rpc.Method, e *rpc.Error, thrown any, code string) *omap {
	st := &omap{}
	st.set("code", rpc.ConnectCode(code))
	st.set("message", errorMessage(e, thrown))
	if d := errorDetail(me, e, thrown, false); d != nil {
		st.set("details", []any{d})
	}
	return st
}

// errorDetail is the one detail an error travels with: its own message,
// or a google.rpc.ErrorInfo for an error node.
func errorDetail(me *rpc.Method, e *rpc.Error, thrown any, grpc bool) *omap {
	if e == nil {
		return nil
	}
	fields := errorFields(thrown)
	typ := ""
	var value any = fields
	if e.IsNode {
		typ = "google.rpc.ErrorInfo"
		info := &omap{}
		info.set("reason", e.ID)
		info.set("domain", me.Service.Package)
		md := &omap{}
		for _, k := range fields.keys {
			md.set(k, scalar(fields.vals[k]))
		}
		info.set("metadata", md)
		value = info
	} else if e.Detail != nil {
		typ = e.Detail.FullName()
		value = protoMessage(e.Detail, fields)
	}
	d := &omap{}
	if grpc {
		d.set("@type", "type.googleapis.com/"+typ)
		if mm, ok := value.(*omap); ok {
			for _, k := range mm.keys {
				d.set(k, mm.vals[k])
			}
		}
		return d
	}
	d.set("type", typ)
	d.set("value", "…")
	d.set("debug", value)
	return d
}

// --- the service index ---

// rpcIndex writes a service's methods, grouped: a gRPC service's by
// streaming kind, a JSON-RPC api's by the namespace of their names.
func (c *Ctx) rpcIndex(api *resolve.Node, b string) {
	s := c.Site
	m := s.rpc()
	switch b {
	case bindGRPC, bindConnect:
		svc := m.ServiceOf(api)
		if svc == nil {
			return
		}
		for _, k := range []rpc.Kind{rpc.Unary, rpc.ServerStreaming, rpc.ClientStreaming, rpc.BidiStreaming} {
			var ms []*rpc.Method
			for _, me := range svc.Methods {
				if me.Kind == k {
					ms = append(ms, me)
				}
			}
			if len(ms) == 0 {
				continue
			}
			c.W(`<div class="rpc-group"><p class="rpc-group-head">`, esc(kindTitle(k)), `</p><ul class="rpc-methods">`)
			for _, me := range ms {
				c.W(`<li><code class="rpc-sig-line">`, esc(me.Name), `(`)
				if me.Kind == rpc.ClientStreaming || me.Kind == rpc.BidiStreaming {
					c.W(`<span class="tk-kw">stream</span> `)
				}
				c.W(esc(me.Request.Name), `) <span class="tk-kw">returns</span> (`)
				if me.Kind == rpc.ServerStreaming || me.Kind == rpc.BidiStreaming {
					c.W(`<span class="tk-kw">stream</span> `)
				}
				c.W(esc(me.Response.Name), `)</code> `)
				c.Link(me.Op, displayName(s, me.Op))
				c.W(deprecatedBadge(me.Op))
				if t := firstSentence(inlineParagraph(s.proseHTML(me.Op))); t != "" {
					c.W(`<span class="op-line">`, t, `</span>`)
				}
				c.W(`</li>`)
			}
			c.W(`</ul></div>`)
		}
	case bindSparrow:
		svc := m.SparrowServiceOf(api)
		if svc == nil {
			return
		}
		groups := map[string][]*rpc.SparrowMethod{}
		for _, sm := range svc.Methods {
			groups[sm.Shape()] = append(groups[sm.Shape()], sm)
		}
		for _, k := range []string{"unary", "server", "client", "bidi", "notification"} {
			if len(groups[k]) == 0 {
				continue
			}
			c.W(`<div class="rpc-group"><p class="rpc-group-head">`, esc(label("docs.kind."+k)), `</p><ul class="rpc-methods">`)
			for _, sm := range groups[k] {
				c.W(`<li><code class="rpc-sig-line">`, strconv.FormatUint(sm.ID, 10), ` `, esc(sm.Name), `(`, esc(sm.Input.String()), `) <span class="tk-kw">returns</span> (`, esc(sm.Output.String()), `)</code> `)
				c.Link(sm.Op, displayName(s, sm.Op))
				c.W(deprecatedBadge(sm.Op))
				if t := firstSentence(inlineParagraph(s.proseHTML(sm.Op))); t != "" {
					c.W(`<span class="op-line">`, t, `</span>`)
				}
				c.W(`</li>`)
			}
			c.W(`</ul></div>`)
		}
	case bindJSONRPC:
		j := m.JSONAPIOf(api)
		if j == nil {
			return
		}
		groups := map[string][]*rpc.JSONMethod{}
		var order []string
		for _, jm := range j.Methods {
			g := ""
			if jm.Notification {
				g = "\x00"
			} else if i := strings.IndexAny(jm.Name, "/."); i > 0 {
				g = jm.Name[:i]
			}
			if _, ok := groups[g]; !ok && g != "\x00" {
				order = append(order, g)
			}
			groups[g] = append(groups[g], jm)
		}
		if len(groups["\x00"]) > 0 {
			order = append(order, "\x00")
		}
		for _, g := range order {
			title := g
			switch g {
			case "\x00":
				title = label("docs.heading.notifications")
			case "":
				title = label("docs.heading.methods")
			}
			c.W(`<div class="rpc-group"><p class="rpc-group-head">`, esc(title), `</p><ul class="rpc-methods">`)
			for _, jm := range groups[g] {
				c.W(`<li><code class="rpc-sig-line">`, esc(jm.Name), `</code> `)
				c.Link(jm.Op, displayName(s, jm.Op))
				if t := firstSentence(inlineParagraph(s.proseHTML(jm.Op))); t != "" {
					c.W(`<span class="op-line">`, t, `</span>`)
				}
				c.W(`</li>`)
			}
			c.W(`</ul></div>`)
		}
	}
}

// bindingFacts are the facts of one RPC binding of an api: the service
// and its protocols for gRPC and Connect, the framing, the encoding and
// batches for JSON-RPC.
func (s *Site) bindingFacts(api *resolve.Node, b string) []string {
	m := s.rpc()
	var facts []string
	switch b {
	case bindGRPC, bindConnect:
		svc := m.ServiceOf(api)
		if svc == nil {
			return nil
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.service"))+`</dt><dd><code class="rpc-service">`+esc(svc.FullName())+`</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`+attr(svc.FullName())+`" aria-label="`+attr(label("docs.action.copy"))+`">`+iconCopy+`</button></dd>`)
		var ps []string
		for _, p := range svc.Protocols {
			ps = append(ps, `<span class="rpc-protocol">`+esc(label("docs.protocol."+p))+`</span>`)
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.protocols"))+`</dt><dd>`+strings.Join(ps, " ")+`</dd>`)
	case bindJSONRPC:
		j := m.JSONAPIOf(api)
		if j == nil {
			return nil
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.framing"))+`</dt><dd>`+esc(label("docs.framing."+j.Framing))+`</dd>`)
		facts = append(facts, `<dt>`+esc(label("docs.label.encoding"))+`</dt><dd>`+esc(label("docs.encoding."+j.Encoding))+`</dd>`)
		if j.Batch {
			facts = append(facts, `<dt>`+esc(label("docs.label.batch"))+`</dt><dd>`+esc(label("docs.label.batches"))+`</dd>`)
		}
	case bindSparrow:
		svc := m.SparrowServiceOf(api)
		if svc == nil {
			return nil
		}
		f := svc.Facet
		facts = append(facts, `<dt>`+esc(label("docs.label.facet"))+`</dt><dd><code class="rpc-service">`+esc(f.Name)+`</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`+attr(f.Name)+`" aria-label="`+attr(label("docs.action.copy"))+`">`+iconCopy+`</button> <span class="mark">`+esc(label("docs.label.facet_id"))+` <code>`+strconv.FormatUint(f.ID, 10)+`</code></span></dd>`)
		facts = append(facts, `<dt>`+esc(label("docs.label.version"))+`</dt><dd><code>`+esc(f.Version)+`</code></dd>`)
		facts = append(facts, `<dt>`+esc(label("docs.label.service"))+`</dt><dd><code>`+esc(svc.Name)+`</code> <span class="mark">`+esc(label("docs.label.service_id"))+` <code>`+strconv.FormatUint(svc.ID, 10)+`</code></span></dd>`)
		if len(f.Retired) > 0 {
			var rs []string
			for _, r := range f.Retired {
				rs = append(rs, `<code>`+esc(r.Space.String())+` `+strconv.FormatUint(r.ID, 10)+`</code>`)
			}
			facts = append(facts, `<dt>`+esc(label("docs.label.retired"))+`</dt><dd>`+strings.Join(rs, " ")+`</dd>`)
		}
		if h := f.HashHex(); h != "" {
			facts = append(facts, `<dt>`+esc(label("docs.label.facet_hash"))+`</dt><dd><code class="rpc-hash" title="`+attr(h)+`">`+esc(h[:18])+`…</code><button type="button" class="icon-btn copy" data-island="copy" data-copy="`+attr(h)+`" aria-label="`+attr(label("docs.action.copy"))+`">`+iconCopy+`</button></dd>`)
		}
	}
	return facts
}

// serverFacts are an api's server URLs, the same for every binding.
func (s *Site) serverFacts(api *resolve.Node) []string {
	var servers []string
	for _, a := range annotationsNamed(api, "server") {
		if u, ok := argString(a.Args(), 0, "url"); ok {
			servers = append(servers, `<code>`+esc(u)+`</code>`)
		}
	}
	if len(servers) == 0 {
		return nil
	}
	return []string{`<dt>` + esc(label("docs.label.servers")) + `</dt><dd>` + strings.Join(servers, " ") + `</dd>`}
}

// --- the type page ---

// protoTabs writes a type's declaration beside its .proto definition and
// its JSON form, as tabs, when the type is a message or an enum of a
// service; heron is the declaration's frame.
func (c *Ctx) protoTabs(n *resolve.Node, heron string) bool {
	s := c.Site
	m := s.rpc()
	msg, enum := m.MessageOf(n), m.EnumOf(n)
	if msg == nil && enum == nil {
		return false
	}
	var proto, js string
	if msg != nil {
		proto = msg.Proto()
		js = pretty(skeletonMessage(msg, 0))
	} else {
		proto = enum.Proto()
		var vs []any
		for _, v := range enum.Values[1:] {
			vs = append(vs, v.Name)
		}
		js = pretty(vs)
	}
	pkg := ""
	if msg != nil {
		pkg = msg.Package
	} else {
		pkg = enum.Package
	}
	c.Island("tabs")
	c.W(`<div class="type-forms" data-island="tabs"><div class="seg" role="tablist" aria-label="`, attr(label("docs.label.view")), `">`)
	for i, t := range []string{"Heron", "Protobuf", "JSON"} {
		c.W(`<button type="button" role="tab" aria-selected="`, strconv.FormatBool(i == 0), `">`, t, `</button>`)
	}
	c.W(`</div><div role="tabpanel">`, heron, `</div>`)
	c.W(`<div role="tabpanel" hidden>`, s.codeFrame("proto", rpc.FilePath(pkg), highlightProto(proto), proto), `</div>`)
	c.W(`<div role="tabpanel" hidden>`, s.codeFrame("json", label("docs.label.json_form"), highlightJSON(js), js), `</div>`)
	c.W(`</div>`)
	return true
}

// highlightShell colours a command: the program's name, and the JSON of a
// single-quoted argument or of a heredoc's lines.
func highlightShell(code string) string {
	var out []string
	for i, line := range strings.Split(code, "\n") {
		l := line
		if i == 0 {
			if sp := strings.IndexByte(l, ' '); sp > 0 {
				out = append(out, `<span class="tk-kw">`+esc(l[:sp])+`</span>`+shellRest(l[sp:]))
				continue
			}
		}
		if strings.HasPrefix(strings.TrimSpace(l), "{") {
			out = append(out, highlightJSON(l))
			continue
		}
		out = append(out, shellRest(l))
	}
	return strings.Join(out, "\n")
}

// shellRest colours the JSON inside a line's '{…}' argument.
func shellRest(l string) string {
	i := strings.Index(l, "'{")
	j := strings.LastIndex(l, "}'")
	if i < 0 || j < i {
		return esc(l)
	}
	return esc(l[:i+1]) + highlightJSON(l[i+1:j+1]) + esc(l[j+1:])
}

// highlightProto colours a .proto excerpt by its keywords, types, names
// and numbers.
func highlightProto(src string) string {
	kws := map[string]bool{"syntax": true, "package": true, "import": true, "service": true, "rpc": true, "returns": true, "stream": true, "message": true, "enum": true, "oneof": true, "repeated": true, "optional": true, "map": true, "reserved": true, "option": true}
	scalars := map[string]bool{"string": true, "bool": true, "bytes": true, "int32": true, "int64": true, "uint32": true, "uint64": true, "sint32": true, "sint64": true, "fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true, "double": true, "float": true}
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
			out = append(out, `<span class="tk-comment">`+esc(line)+`</span>`)
			continue
		}
		var b strings.Builder
		word := ""
		flush := func() {
			if word == "" {
				return
			}
			switch {
			case kws[word]:
				b.WriteString(`<span class="tk-kw">` + esc(word) + `</span>`)
			case scalars[word] || word[0] >= 'A' && word[0] <= 'Z' || strings.HasPrefix(word, "google."):
				b.WriteString(`<span class="tk-type">` + esc(word) + `</span>`)
			case word[0] >= '0' && word[0] <= '9':
				b.WriteString(`<span class="tk-num">` + esc(word) + `</span>`)
			default:
				b.WriteString(esc(word))
			}
			word = ""
		}
		for _, r := range line {
			if r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				word += string(r)
				continue
			}
			flush()
			if strings.ContainsRune("{}()<>=;,", r) {
				b.WriteString(`<span class="tk-punct">` + esc(string(r)) + `</span>`)
			} else {
				b.WriteString(esc(string(r)))
			}
		}
		flush()
		out = append(out, b.String())
	}
	return strings.Join(out, "\n")
}
