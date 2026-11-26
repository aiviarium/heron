package docs

import (
	"encoding/json"
	"github.com/aiviarium/heron/internal/core/jsonnames"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// AsyncAPI writes an AsyncAPI 3.0 document for each api of the tree, by
// its path: each op a channel at its full path, a send operation with its
// request and a receive operation with its response and the errors it
// throws, and every type the ops name a JSON Schema in the components.
// It's an extra for outside tooling; the docs stay the reference.
func AsyncAPI(tree *resolve.Tree, c *types.Checker) map[string][]byte {
	s := New(tree, c, Options{})
	out := map[string][]byte{}
	tree.Walk(func(n *resolve.Node) {
		if n.Name == "" || !own(n) || s.kindPath(n) != "types.api" {
			return
		}
		b, _ := json.MarshalIndent(s.asyncAPI(n), "", "  ")
		out[n.Path()+".json"] = append(b, '\n')
	})
	return out
}

func (s *Site) asyncAPI(api *resolve.Node) *omap {
	doc := &omap{}
	doc.set("asyncapi", "3.0.0")
	info := &omap{}
	info.set("title", displayName(s, api))
	info.set("version", "0.0.0")
	if d := plainText(s.proseHTML(api)); d != "" {
		info.set("description", d)
	}
	doc.set("info", info)
	channels, operations := &omap{}, &omap{}
	messages, schemas := &omap{}, &omap{}
	var named []*resolve.Node
	seen := map[*resolve.Node]bool{}
	ref := func(n *resolve.Node) {
		if !seen[n] {
			seen[n] = true
			named = append(named, n)
		}
	}
	for _, op := range api.Children {
		if s.kindPath(op) != "types.op" {
			continue
		}
		o := s.readOp(op)
		address := o.base + o.path
		if o.ws != "" && o.method == "" {
			address = o.ws
		}
		ch := &omap{}
		ch.set("address", address)
		if d := firstSentence(plainText(s.proseHTML(op))); d != "" {
			ch.set("description", d)
		}
		chMsgs := &omap{}
		reqName, resName := op.Name+".request", op.Name+".response"
		// The request: the body, or the parameters as one object.
		var req any
		props, required := &omap{}, []string{}
		for _, p := range o.params {
			t := p.typ
			if t.name == "Stream" && len(t.args) == 1 {
				t = t.args[0]
			}
			if p.loc == "body" || p.loc == "" && o.ws != "" {
				req = s.schemaOf(t, ref)
				continue
			}
			props.set(p.name, s.schemaOf(t, ref))
			if !p.optional {
				required = append(required, p.name)
			}
		}
		if req == nil {
			obj := &omap{}
			obj.set("type", "object")
			obj.set("properties", props)
			if len(required) > 0 {
				obj.set("required", required)
			}
			req = obj
		}
		messages.set(reqName, message(req, ""))
		chMsgs.set("request", refTo("#/components/messages/"+reqName))
		var receive []any
		if o.returns.text != "" {
			t := o.returns
			if t.name == "Stream" && len(t.args) == 1 {
				t = t.args[0]
			}
			messages.set(resName, message(s.schemaOf(t, ref), ""))
			chMsgs.set("response", refTo("#/components/messages/"+resName))
			receive = append(receive, refTo("#/channels/"+op.Name+"/messages/response"))
		}
		for _, e := range o.errors {
			key := "error." + e.typ.Name
			var payload any
			if e.code != "" {
				obj := &omap{}
				obj.set("type", "object")
				p := &omap{}
				p.set("error", map[string]any{"type": "string"})
				p.set("code", map[string]any{"const": e.code})
				obj.set("properties", p)
				obj.set("required", []string{"error", "code"})
				payload = obj
			} else {
				ref(e.typ)
				payload = refTo("#/components/schemas/" + e.typ.Name)
			}
			messages.set(key, message(payload, e.status))
			chMsgs.set(key, refTo("#/components/messages/"+key))
			receive = append(receive, refTo("#/channels/"+op.Name+"/messages/"+key))
		}
		ch.set("messages", chMsgs)
		channels.set(op.Name, ch)
		send := &omap{}
		send.set("action", "send")
		send.set("channel", refTo("#/channels/"+op.Name))
		send.set("messages", []any{refTo("#/channels/" + op.Name + "/messages/request")})
		operations.set(op.Name+".send", send)
		if len(receive) > 0 {
			rc := &omap{}
			rc.set("action", "receive")
			rc.set("channel", refTo("#/channels/"+op.Name))
			rc.set("messages", receive)
			operations.set(op.Name+".receive", rc)
		}
	}
	for i := 0; i < len(named); i++ {
		t := named[i]
		schemas.set(t.Name, s.typeSchema(t, ref))
	}
	doc.set("channels", channels)
	doc.set("operations", operations)
	comp := &omap{}
	comp.set("messages", messages)
	comp.set("schemas", schemas)
	doc.set("components", comp)
	return doc
}

func message(payload any, status string) *omap {
	m := &omap{}
	m.set("contentType", "application/json")
	m.set("payload", payload)
	if status != "" {
		m.set("description", "HTTP status "+status)
	}
	return m
}

func refTo(p string) map[string]any { return map[string]any{"$ref": p} }

// schemaOf is the JSON Schema of a type as written; a declared type is a
// reference to its component, which ref records.
func (s *Site) schemaOf(x texpr, ref func(*resolve.Node)) any {
	switch x.name {
	case "String":
		return map[string]any{"type": "string"}
	case "Int":
		return map[string]any{"type": "integer"}
	case "Float", "Number":
		return map[string]any{"type": "number"}
	case "Bool":
		return map[string]any{"type": "boolean"}
	case "Timestamp":
		return map[string]any{"type": "string", "format": "date-time"}
	case "Duration":
		return map[string]any{"type": "string"}
	case "Any":
		return map[string]any{}
	case "List", "Set", "Stream":
		if len(x.args) == 1 {
			return map[string]any{"type": "array", "items": s.schemaOf(x.args[0], ref)}
		}
	case "Map":
		if len(x.args) == 2 {
			return map[string]any{"type": "object", "additionalProperties": s.schemaOf(x.args[1], ref)}
		}
	}
	if len(x.union) > 0 {
		var one []any
		for _, u := range x.union {
			one = append(one, s.schemaOf(u, ref))
		}
		return map[string]any{"oneOf": one}
	}
	if t := x.decl; t != nil && own(t) && s.isTypeDecl(t) {
		ref(t)
		return refTo("#/components/schemas/" + t.Name)
	}
	return map[string]any{}
}

// typeSchema is a declared type's component: an enum's values, or a
// record's fields with their docs, its optional fields not required.
func (s *Site) typeSchema(t *resolve.Node, ref func(*resolve.Node)) *omap {
	out := &omap{}
	if d := plainText(s.proseHTML(t)); d != "" {
		out.set("description", d)
	}
	if s.kindPath(t) == "types.enum" {
		var vals []string
		for _, v := range t.Children {
			if v.Name != "" {
				vals = append(vals, v.Name)
			}
		}
		out.set("type", "string")
		out.set("enum", vals)
		return out
	}
	out.set("type", "object")
	props := &omap{}
	var required []string
	for _, m := range s.members(t, nil) {
		sc := s.schemaOf(m.typ, ref)
		if m.node != nil {
			if d := plainText(s.proseHTML(m.node)); d != "" {
				if mm, ok := sc.(map[string]any); ok {
					c := map[string]any{"description": d}
					for k, v := range mm {
						c[k] = v
					}
					sc = c
				}
			}
		}
		props.set(jsonnames.Member(m.node, t, s.jsonCase), sc)
		if !m.optional && !m.typ.optional && m.def == nil {
			required = append(required, m.name)
		}
	}
	out.set("properties", props)
	if len(required) > 0 {
		out.set("required", required)
	}
	return out
}

// plainText is rendered HTML as text, its tags dropped and its entities
// read.
func plainText(h string) string {
	var b strings.Builder
	depth := 0
	for _, r := range h {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`)
	return strings.Join(strings.Fields(r.Replace(b.String())), " ")
}
