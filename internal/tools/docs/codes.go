package docs

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/rpc"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// surfaceCode is an error's effective code on one surface that reports
// it: the one its @status derives, or the one an override sets.
type surfaceCode struct {
	Surface  string // "HTTP", "gRPC", "JSON-RPC", "Sparrow" or "exit"
	Code     string
	Explicit bool
}

// errorCodes are an error's status and its effective code on each surface
// that reports it: its HTTP status with its text always, then gRPC,
// JSON-RPC and Sparrow where a method of the tree's bindings answers with
// it, and its exit status where a command holds it or throws it.
func (s *Site) errorCodes(n *resolve.Node) []surfaceCode {
	var out []surfaceCode
	status := annText(n, "status")
	if status != "" {
		text := ""
		if a := annotation(s, n, "status"); a != nil {
			text, _ = argString(a.Args(), 1, "text")
		}
		if text == "" {
			text, _ = argString(kindArgs(n), 0, "id")
		}
		code := status
		if text != "" {
			code += " " + text
		}
		out = append(out, surfaceCode{Surface: "HTTP", Code: code, Explicit: true})
	}
	m := s.rpc()
	var grpc, jsonrpc, sparrow *rpc.Error
	for _, svc := range m.Services {
		for _, meth := range svc.Methods {
			for i := range meth.Errors {
				if x := &meth.Errors[i]; x.Node == n && grpc == nil && x.GRPC != "" {
					grpc = x
				}
			}
		}
	}
	for _, api := range m.JSONAPIs {
		for _, meth := range api.Methods {
			for i := range meth.Errors {
				if x := &meth.Errors[i]; x.Node == n && jsonrpc == nil && x.JSONRPC != "" {
					jsonrpc = x
				}
			}
		}
	}
	for _, f := range m.Facets {
		for _, svc := range f.Services {
			for _, meth := range svc.Methods {
				for i := range meth.Errors {
					if x := &meth.Errors[i]; x.Node == n && sparrow == nil {
						sparrow = x
					}
				}
			}
		}
	}
	if grpc != nil {
		out = append(out, surfaceCode{Surface: "gRPC", Code: grpc.GRPC, Explicit: grpc.GRPCExplicit})
	}
	if jsonrpc != nil {
		out = append(out, surfaceCode{Surface: "JSON-RPC", Code: jsonrpc.JSONRPC, Explicit: jsonrpc.JSONRPCExplicit})
	}
	if sparrow != nil {
		if c := sparrowCode(sparrow); c != "" {
			out = append(out, surfaceCode{Surface: "Sparrow", Code: c, Explicit: sparrow.SparrowExplicit || sparrow.SparrowSigil != nil})
		}
	}
	if s.onCommand(n) {
		if code, explicit := exitCode(n); code != "" {
			out = append(out, surfaceCode{Surface: "exit", Code: code, Explicit: explicit})
		}
	}
	return out
}

// onCommand reports whether a command reports an error: it holds it, or
// names it with @throws.
func (s *Site) onCommand(n *resolve.Node) bool {
	for a := n.Parent; a != nil; a = a.Parent {
		if s.kindPath(a) == "spec.command" {
			return true
		}
	}
	if s.commandThrows == nil {
		s.commandThrows = map[*resolve.Node]bool{}
		s.Tree.Walk(func(x *resolve.Node) {
			if s.kindPath(x) == "spec.command" {
				for _, t := range s.annRefs(x, "throws") {
					s.commandThrows[t] = true
				}
			}
		})
	}
	return s.commandThrows[n] || annText(n, "exit") != ""
}

// codeMark is how a page marks an effective code: derived from @status,
// or explicit, set by an override.
func codeMark(c surfaceCode) string {
	if c.Explicit {
		return label("docs.label.code_explicit")
	}
	return label("docs.label.code_derived")
}

// codeFacts are an error's codes as the facts of its entry, each with its
// mark, the HTTP status with its text alone.
func (s *Site) codeFacts(n *resolve.Node) []string {
	var out []string
	for _, c := range s.errorCodes(n) {
		f := esc(c.Surface) + ` <code>` + esc(c.Code) + `</code>`
		if c.Surface != "HTTP" {
			f += ` <span class="mark">` + esc(codeMark(c)) + `</span>`
		}
		out = append(out, f)
	}
	return out
}

// memberType is a member's type as written, as String.
func memberType(n *resolve.Node) string {
	if n.Decl.N == nil {
		return ""
	}
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t := (ast.Declare{N: n.Decl.N}).Type(); t != nil {
			return strings.TrimSpace(t.Syntax().Text())
		}
	case syntax.Value:
		if t := (ast.Value{N: n.Decl.N}).Type(); t != nil {
			return strings.TrimSpace(t.Syntax().Text())
		}
	}
	return ""
}

// codeMarkHTML marks an effective code on an op's page: derived from its
// error's @status, or explicit; nothing for no code.
func codeMarkHTML(code string, explicit bool) string {
	if code == "" {
		return ""
	}
	return ` <span class="mark">` + esc(codeMark(surfaceCode{Explicit: explicit})) + `</span>`
}
