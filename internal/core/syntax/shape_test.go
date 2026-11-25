package syntax

import (
	"strings"
	"testing"
)

// shape renders the node kinds under n as a compact s-expression.
func shape(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(n *Node) {
		b.WriteString(n.Kind.String())
		var kids []*Node
		for _, c := range n.Children {
			if c, ok := c.(*Node); ok {
				kids = append(kids, c)
			}
		}
		if len(kids) == 0 {
			return
		}
		b.WriteString("(")
		for i, k := range kids {
			if i > 0 {
				b.WriteString(" ")
			}
			walk(k)
		}
		b.WriteString(")")
	}
	walk(n)
	return b.String()
}

func TestShapes(t *testing.T) {
	cases := []struct{ src, want string }{
		{"a: b", "File(Declare(Path NamedType(Path)))"},
		{"f(x: Int = 1): func<pure>(Bool) do {\n  return x > 0\n}",
			"File(Declare(Path Params(Param(NamedType(Path) Literal)) NamedType(Path Flags Args(PositionalArg(PathExpr(Path)))) DoBlock(Code(ReturnStmt(BinaryExpr(PathExpr(Path) Literal))))))"},
		{"label =| Printed under the gauge.", "File(Set(Path ProseLines(ProseLine)))"},
		{"x =\n  | one\n  | two", "File(Set(Path ProseLines(ProseLine ProseLine)))"},
		{"gauge {\n  owner.team = \"core\"\n}", "File(Enter(Path StructBody(Set(Path StringLit))))"},
		{"example {{ day 30: accepted }}", "File(TextNode(Path ProseBlock))"},
		{"refunds.{\n  include = [\"./a.heron\"]\n}", "File(Scope(Path StructBody(Set(Path ArrayExpr(StringLit)))))"},
		{"@profile(local) .{\n  a = 1\n}", "File(Scope(Annotation(Path Args(PositionalArg(PathExpr(Path)))) StructBody(Set(Path Literal))))"},
		{"case(days_ago = 31)", "File(Invoke(Path Args(NamedArg(Literal))))"},
		{"requires.add(checks.e2e)", "File(Invoke(Path Args(PositionalArg(PathExpr(Path)))))"},
		{"verification<generate>", "File(Invoke(Path Flags))"},
		{"let base = 1", "File(Let(Literal))"},
		{"/// Docs.\n@source(file = \"g.csv\")\ng: widget<large>(size = 3)", "File(Declare(Doc(DocLine) Annotation(Path Args(NamedArg(StringLit))) Path NamedType(Path Flags Args(NamedArg(Literal)))))"},
		{"limits = (Set)[(u8)10, (u8)20]", "File(Set(Path CastExpr(NamedType(Path) ArrayExpr(CastExpr(NamedType(Path) Literal) CastExpr(NamedType(Path) Literal)))))"},
		{"x = (a + b) * c", "File(Set(Path BinaryExpr(ParenExpr(BinaryExpr(PathExpr(Path) PathExpr(Path))) PathExpr(Path))))"},
		{"x = a < b", "File(Set(Path BinaryExpr(PathExpr(Path) PathExpr(Path))))"},
		{"note?: String? = null", "File(Value(Path OptionalType(NamedType(Path)) PathExpr(Path)))"},
		{"Sorted(T: Comparable & Listable): type", "File(Declare(Path Params(Param(IntersectionType(NamedType(Path) NamedType(Path)))) NamedType(Path)))"},
		{"t: task do {\n  if a {\n    b()\n  }\n  else {\n    c()\n  }\n}",
			"File(Declare(Path NamedType(Path) DoBlock(Code(ExprStmt(IfExpr(PathExpr(Path) Code(ExprStmt(CallExpr(PathExpr(Path) Args))) Code(ExprStmt(CallExpr(PathExpr(Path) Args)))))))))"},
		{"t: task do {\n  check(\"x\") {\n    expect(a)\n  }\n  node.field = 1\n}",
			"File(Declare(Path NamedType(Path) DoBlock(Code(ExprStmt(CallExpr(PathExpr(Path) Args(PositionalArg(StringLit))) Code(ExprStmt(CallExpr(PathExpr(Path) Args(PositionalArg(PathExpr(Path))))))) AssignStmt(Path Literal)))))"},
		{"m: macro = (heron)\"\"\"get_$name(@path id: Int)\"\"\"", "File(Value(Path NamedType(Path) CastExpr(NamedType(Path) StringLit(Insert(Path)))))"},
		{"s: section {\n  | <details>\n  |   <title>T</title>\n  | </details>\n}", "File(Declare(Path NamedType(Path) StructBody(ProseLines(ProseLine(Tag(Path Tag(Path TagClose(Path)) TagClose(Path)))))))"},
		{"s: section {\n  | ~~~json\n  | {}\n  | ~~~\n}", "File(Declare(Path NamedType(Path) StructBody(ProseLines(ProseLine(Fence)))))"},
		{"x = { a = 1, b = [2]\n  c = \"d\" }", "File(Set(Path MapExpr(MapEntry(Path Literal) MapEntry(Path ArrayExpr(Literal)) MapEntry(Path StringLit))))"},
		{"packages.{\n  \"std/ci\" = { version = \"1\" }\n}", "File(Scope(Path StructBody(Set(Path(StringLit) MapExpr(MapEntry(Path StringLit))))))"},
		{"c(@body r: R, @query q?: S): op(T) =| Doc.", "File(Value(Path Params(Param(Annotation(Path) NamedType(Path)) Param(Annotation(Path) NamedType(Path))) NamedType(Path Args(PositionalArg(PathExpr(Path)))) ProseLines(ProseLine)))"},
		{"a = [Link(x), \"$y.\"]", "File(Set(Path ArrayExpr(CallExpr(PathExpr(Path) Args(PositionalArg(PathExpr(Path)))) StringLit(Insert(Path)))))"},
	}
	for _, c := range cases {
		tr := Parse("t.heron", c.src)
		if got := shape(tr.Root); got != c.want {
			t.Errorf("%q\n got %s\nwant %s", c.src, got, c.want)
		}
		if len(tr.Diags) > 0 {
			t.Errorf("%q: %v", c.src, tr.Diags)
		}
	}
}
