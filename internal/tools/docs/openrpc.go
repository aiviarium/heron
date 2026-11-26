package docs

import (
	"encoding/json"
	"github.com/aiviarium/heron/internal/core/jsonnames"
	"strconv"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// OpenRPC writes an OpenRPC 1.3 document for each api with JSON-RPC
// methods, by its path: each method by its wire name, with its params,
// its result unless it's a notification, its errors and its examples, and
// every type the methods name a JSON Schema in the components. It's an
// extra for outside tooling; the docs stay the reference.
func OpenRPC(tree *resolve.Tree, c *types.Checker) map[string][]byte {
	s := New(tree, c, Options{})
	out := map[string][]byte{}
	for _, j := range s.rpc().JSONAPIs {
		b, _ := json.MarshalIndent(s.openRPC(j.API), "", "  ")
		out[j.API.Path()+".json"] = append(b, '\n')
	}
	return out
}

func (s *Site) openRPC(api *resolve.Node) *omap {
	j := s.rpc().JSONAPIOf(api)
	s.jsonCase = j.Case
	defer func() { s.jsonCase = "" }()
	doc := &omap{}
	doc.set("openrpc", "1.3.2")
	info := &omap{}
	info.set("title", displayName(s, api))
	info.set("version", "0.0.0")
	if d := plainText(s.proseHTML(api)); d != "" {
		info.set("description", d)
	}
	doc.set("info", info)
	if len(j.Servers) > 0 {
		var servers []any
		for i, u := range j.Servers {
			sv := &omap{}
			sv.set("name", "server"+strconv.Itoa(i+1))
			sv.set("url", u)
			servers = append(servers, sv)
		}
		doc.set("servers", servers)
	}
	var named []*resolve.Node
	seen := map[*resolve.Node]bool{}
	ref := func(n *resolve.Node) {
		if !seen[n] {
			seen[n] = true
			named = append(named, n)
		}
	}
	var methods []any
	for _, jm := range j.Methods {
		o := s.readOp(jm.Op)
		me := &omap{}
		me.set("name", jm.Name)
		if d := firstSentence(plainText(s.proseHTML(jm.Op))); d != "" {
			me.set("summary", d)
		}
		if d := plainText(s.proseHTML(jm.Op)); d != "" {
			me.set("description", d)
		}
		if jm.ByPosition {
			me.set("paramStructure", "by-position")
		} else {
			me.set("paramStructure", "by-name")
		}
		params := []any{}
		if jm.Whole != nil {
			// The record's fields are the params, by name.
			for _, p := range o.params {
				if p.name != jm.Whole.Name || p.typ.decl == nil {
					continue
				}
				for _, mem := range s.members(p.typ.decl, s.envFor(p.typ)) {
					cd := &omap{}
					cd.set("name", jsonnames.Member(mem.node, p.typ.decl, j.Case))
					cd.set("required", !mem.optional && !mem.typ.optional && mem.def == nil)
					cd.set("schema", s.schemaOf(mem.typ, ref))
					params = append(params, cd)
				}
			}
		} else {
			for i, p := range o.params {
				cd := &omap{}
				cd.set("name", jm.Params[i].Wire)
				cd.set("required", !p.optional)
				if p.doc != nil {
					cd.set("description", plainText(s.renderText([]resolve.Site{{File: p.site.File, N: p.doc, Scope: p.site.Scope}})))
				}
				cd.set("schema", s.schemaOf(p.typ, ref))
				params = append(params, cd)
			}
		}
		me.set("params", params)
		if !jm.Notification {
			res := &omap{}
			res.set("name", "result")
			if jm.NullResult {
				res.set("schema", map[string]any{"type": "null"})
			} else {
				res.set("schema", s.schemaOf(o.returns, ref))
			}
			me.set("result", res)
		}
		var errs []any
		for _, e := range jm.Errors {
			code, err := strconv.Atoi(e.JSONRPC)
			if err != nil {
				continue
			}
			eo := &omap{}
			eo.set("code", code)
			msg := e.Message
			if msg == "" {
				msg = e.Node.Name
			}
			eo.set("message", msg)
			errs = append(errs, eo)
		}
		if len(errs) > 0 {
			me.set("errors", errs)
		}
		var exs []any
		for i := range o.examples {
			ex := &o.examples[i]
			eo := &omap{}
			eo.set("name", ex.node.Name)
			var ps []any
			for _, p := range o.params {
				v, ok := ex.values[p.name]
				if !ok {
					continue
				}
				v = s.wireValue(p.typ, v, j.Case)
				if jm.Whole != nil {
					if mm, ok := v.(*omap); ok {
						for _, k := range mm.keys {
							pp := &omap{}
							pp.set("name", k)
							pp.set("value", mm.vals[k])
							ps = append(ps, pp)
						}
					}
					continue
				}
				pp := &omap{}
				pp.set("name", jsonnames.Apply(p.name, j.Case))
				pp.set("value", v)
				ps = append(ps, pp)
			}
			if ps == nil {
				ps = []any{}
			}
			eo.set("params", ps)
			if ex.hasRet {
				r := &omap{}
				r.set("name", "result")
				r.set("value", s.wireValue(o.returns, ex.returns, j.Case))
				eo.set("result", r)
			} else if ex.throws != nil {
				continue // OpenRPC pairs a result, not an error
			}
			exs = append(exs, eo)
		}
		if len(exs) > 0 {
			me.set("examples", exs)
		}
		methods = append(methods, me)
	}
	doc.set("methods", methods)
	schemas := &omap{}
	for i := 0; i < len(named); i++ {
		t := named[i]
		schemas.set(t.Name, s.typeSchema(t, ref))
	}
	comp := &omap{}
	comp.set("schemas", schemas)
	doc.set("components", comp)
	return doc
}
