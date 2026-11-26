package syntax

import (
	"strings"
	"testing"
)

// The cases below prove the rules of spec/language/grammar, one test per
// section: each source parses to the shape given, with exactly the
// diagnostics listed, and every tree is lossless.

type grammarCase struct {
	src, shape string
	diags      []string // messages, in order
}

func runGrammarCases(t *testing.T, cases []grammarCase) {
	t.Helper()
	for _, c := range cases {
		name := "t.heron"
		src := c.src
		if strings.HasPrefix(src, "md:") {
			name, src = "t.heron.md", src[3:]
		}
		tr := Parse(name, src)
		if got := shape(tr.Root); got != c.shape {
			t.Errorf("%q\n got %s\nwant %s", src, got, c.shape)
		}
		var msgs []string
		for _, d := range tr.Diags {
			msgs = append(msgs, d.Message)
		}
		if strings.Join(msgs, "\n") != strings.Join(c.diags, "\n") {
			t.Errorf("%q: diagnostics %q, want %q", src, msgs, c.diags)
		}
		if tr.Root.Text() != src {
			t.Errorf("%q: not lossless", src)
		}
	}
}

func TestLexical(t *testing.T) {
	runGrammarCases(t, []grammarCase{
		// line_kinds, comment_members
		{"// c\na = 1", "File(CommentLine Set(Path Literal))", nil},
		{"/// d\na: b", "File(Declare(Doc(DocLine) Path NamedType(Path)))", nil},
		{"s: section {\n  | text\n  // c\n}", "File(Declare(Path NamedType(Path) StructBody(ProseLines(ProseLine) CommentLine)))", nil},
		{"a = 1 // trailing", "File(Set(Path Literal))", nil},
		// no_comments_in_text
		{"s: section {\n  | see http://example.com\n}", "File(Declare(Path NamedType(Path) StructBody(ProseLines(ProseLine))))", nil},
		{"x = \"a // b\"", "File(Set(Path StringLit))", nil},
		// line_ends
		{"a = 1\r\nb = 2", "File(Set(Path Literal) Set(Path Literal))", nil},
		// layout_whitespace
		{"a:\tb\n    c = 1", "File(Declare(Path NamedType(Path)) Set(Path Literal))", nil},
		// identifiers
		{"größe: Int", "File(Declare(Path NamedType(Path)))", nil},
		{"_draft = v2", "File(Set(Path PathExpr(Path)))", nil},
		{"x = max-items", "File(Set(Path BinaryExpr(PathExpr(Path) PathExpr(Path))))", nil},
		// numbers
		{"x = 1_000.5e-3", "File(Set(Path Literal))", nil},
		{"x = -1", "File(Set(Path UnaryExpr(Literal)))", nil},
		{"x = 1.", "File(Set(Path Literal) ErrorNode)", []string{"expected the end of the line"}},
		// words_in_structure, meaning_is_local
		{"if {{ late }}", "File(TextNode(Path ProseBlock))", nil},
		{"do: rule", "File(Declare(Path NamedType(Path)))", nil},
		{"let: Int = 1", "File(Value(Path NamedType(Path) Literal))", nil},
		{"for = 1", "File(Set(Path Literal))", nil},
		// word_boundaries
		{"x = a or b", "File(Set(Path BinaryExpr(PathExpr(Path) PathExpr(Path))))", nil},
		{"x = order", "File(Set(Path PathExpr(Path)))", nil},
		{"x = origin", "File(Set(Path PathExpr(Path)))", nil},
	})
}

func TestStructure(t *testing.T) {
	runGrammarCases(t, []grammarCase{
		// body_by_opener
		{"a: b {\n  c = 1\n}", "File(Declare(Path NamedType(Path) StructBody(Set(Path Literal))))", nil},
		{"a: b {{ text }}", "File(Declare(Path NamedType(Path) ProseBlock))", nil},
		{"a: b do {\n  f()\n}", "File(Declare(Path NamedType(Path) DoBlock(Code(ExprStmt(CallExpr(PathExpr(Path) Args))))))", nil},
		// prose_after_eq
		{"m =| No pet.", "File(Set(Path ProseLines(ProseLine)))", nil},
		{"m = {{ No pet. }}", "File(Set(Path ProseBlock))", nil},
		{"x =\n  | one\n  | two", "File(Set(Path ProseLines(ProseLine ProseLine)))", nil},
		// code_only_in_do
		{"do {\n  let a = 1\n}", "File(Action(DoBlock(Code(LetStmt(Literal)))))", nil},
		// declaration_shape, optional_marker
		{"a: b", "File(Declare(Path NamedType(Path)))", nil},
		{"a: Int = 1", "File(Value(Path NamedType(Path) Literal))", nil},
		{"note?: String", "File(Declare(Path NamedType(Path)))", nil},
		{"g<x>(n: Int)?: k", "File(Declare(Path Flags Params(Param(NamedType(Path))) NamedType(Path)))", nil},
		// params_or_args, named_or_positional
		{"f(x: Int): func", "File(Declare(Path Params(Param(NamedType(Path))) NamedType(Path)))", nil},
		{"f(lang?: String): k", "File(Declare(Path Params(Param(NamedType(Path))) NamedType(Path)))", nil},
		{"f(1): k", "File(Declare(Path Params(PositionalArg(Literal)) NamedType(Path)))", nil},
		{"case(days_ago = 31)", "File(Invoke(Path Args(NamedArg(Literal))))", nil},
		{"code(\"sh\", a == b)", "File(Invoke(Path Args(PositionalArg(StringLit) PositionalArg(BinaryExpr(PathExpr(Path) PathExpr(Path))))))", nil},
		// invoke_shape
		{"verification<generate>", "File(Invoke(Path Flags))", nil},
		{"requires.add(checks.e2e)", "File(Invoke(Path Args(PositionalArg(PathExpr(Path)))))", nil},
		{"get(a) =| text", "File(Invoke(Path Args(PositionalArg(PathExpr(Path))) ProseLines(ProseLine)))", nil},
		{"marker", "File(Invoke(Path))", nil},
		// enter_shape
		{"a.{\n  b = 1\n}", "File(Scope(Path StructBody(Set(Path Literal))))", nil},
		{".{\n  b = 1\n}", "File(Scope(StructBody(Set(Path Literal))))", nil},
		{"a.b = 1", "File(Set(Path Literal))", nil},
		{"a {{ text }}", "File(TextNode(Path ProseBlock))", nil},
		{"a {\n}", "File(Enter(Path StructBody))", nil},
		{"t do {\n}", "File(Enter(Path DoBlock(Code)))", nil},
		// let_shape
		{"let base = 1", "File(Let(Literal))", nil},
		{"let n: Int = 1", "File(Let(NamedType(Path) Literal))", nil},
		// paths
		{"packages.\"std/ci\" = 1", "File(Set(Path(StringLit) Literal))", nil},
		// flag_lists
		{"t: task<confirm, slow>", "File(Declare(Path NamedType(Path Flags)))", nil},
		{"x = a<b", "File(Set(Path BinaryExpr(PathExpr(Path) PathExpr(Path))))", nil},
		// newlines_end_members
		{"x =\n  1", "File(Set(Path Literal))", nil},
		{"f(\n  a = 1,\n  b,\n)", "File(Invoke(Path Args(NamedArg(Literal) PositionalArg(PathExpr(Path)))))", nil},
		{"a { b = 1 }", "File(Enter(Path StructBody(Set(Path Literal))))", nil},
		// doc_lines_attach
		{"/// one\n/// two\na: b", "File(Declare(Doc(DocLine DocLine) Path NamedType(Path)))", nil},
		{"/// d\n\na: b", "File(Doc(DocLine) Declare(Path NamedType(Path)))", nil},
		// annotation_lines
		{"@a\n// c\n@b(1)\nx: y", "File(Declare(Annotation(Path) Annotation(Path Args(PositionalArg(Literal))) Path NamedType(Path)))", nil},
		{"@a x: y", "File(Declare(Annotation(Path) Path NamedType(Path)))", nil},
	})
}

func TestCode(t *testing.T) {
	task := func(body string) string { return "t: task do {\n" + body + "\n}" }
	wrap := func(stmts string) string { return "File(Declare(Path NamedType(Path) DoBlock(Code(" + stmts + "))))" }
	runGrammarCases(t, []grammarCase{
		// statement_shapes, assignment_targets
		{task("  let a = 1\n  a.b = 2\n  return\n  // c\n  f(a)"), wrap("LetStmt(Literal) AssignStmt(Path Literal) ReturnStmt CommentLine ExprStmt(CallExpr(PathExpr(Path) Args(PositionalArg(PathExpr(Path)))))"), nil},
		{task("  return a == b"), wrap("ReturnStmt(BinaryExpr(PathExpr(Path) PathExpr(Path)))"), nil},
		// trailing_blocks
		{task("  check(\"x\") {\n    expect(a)\n  }"), wrap("ExprStmt(CallExpr(PathExpr(Path) Args(PositionalArg(StringLit))) Code(ExprStmt(CallExpr(PathExpr(Path) Args(PositionalArg(PathExpr(Path)))))))"), nil},
		// else_placement
		{task("  if a {\n  } else {\n  }"), wrap("ExprStmt(IfExpr(PathExpr(Path) Code Code))"), nil},
		{task("  if a {\n  }\n  else {\n  }"), wrap("ExprStmt(IfExpr(PathExpr(Path) Code Code))"), nil},
		{"x = if a { 1 } else if b { 2 } else { 3 }", "File(Set(Path IfExpr(PathExpr(Path) Code(ExprStmt(Literal)) IfExpr(PathExpr(Path) Code(ExprStmt(Literal)) Code(ExprStmt(Literal))))))", nil},
		// for_loops
		{task("  for x in xs {\n  }"), wrap("ExprStmt(ForExpr(PathExpr(Path) Code))"), nil},
		// operator_precedence
		{"x = a or b and c", "File(Set(Path BinaryExpr(PathExpr(Path) BinaryExpr(PathExpr(Path) PathExpr(Path)))))", nil},
		{"x = 1 + 2 * 3", "File(Set(Path BinaryExpr(Literal BinaryExpr(Literal Literal))))", nil},
		{"x = a - b - c", "File(Set(Path BinaryExpr(BinaryExpr(PathExpr(Path) PathExpr(Path)) PathExpr(Path))))", nil},
		{"x = a in b and c is D", "File(Set(Path BinaryExpr(BinaryExpr(PathExpr(Path) PathExpr(Path)) BinaryExpr(PathExpr(Path) PathExpr(Path)))))", nil},
		// unary_operators
		{"x = not a == b", "File(Set(Path UnaryExpr(BinaryExpr(PathExpr(Path) PathExpr(Path)))))", nil},
		{"x = -a.b", "File(Set(Path UnaryExpr(PathExpr(Path))))", nil},
		{"x = -f(a).b", "File(Set(Path UnaryExpr(FieldExpr(CallExpr(PathExpr(Path) Args(PositionalArg(PathExpr(Path))))))))", nil},
		// postfix_binds_tightest
		{"x = f(a).b", "File(Set(Path FieldExpr(CallExpr(PathExpr(Path) Args(PositionalArg(PathExpr(Path)))))))", nil},
		{"x = f (a)", "File(Set(Path PathExpr(Path)) ErrorNode)", []string{"expected the end of the line"}},
		// operator_continues_line
		{"x = a +\n  b", "File(Set(Path BinaryExpr(PathExpr(Path) PathExpr(Path))))", nil},
		// unspaced_operators
		{"x = f<pure>(1)", "File(Set(Path CallExpr(PathExpr(Path) Flags Args(PositionalArg(Literal)))))", nil},
		{"x = a<=b", "File(Set(Path BinaryExpr(PathExpr(Path) PathExpr(Path))))", nil},
		// cast_lookahead
		{"x = (u8)12", "File(Set(Path CastExpr(NamedType(Path) Literal)))", nil},
		{"x = (Set)[1]", "File(Set(Path CastExpr(NamedType(Path) ArrayExpr(Literal))))", nil},
		{"x = (a) + 1", "File(Set(Path BinaryExpr(ParenExpr(PathExpr(Path)) Literal)))", nil},
		{"x = (a)", "File(Set(Path ParenExpr(PathExpr(Path))))", nil},
		// collection_literals, map_after_eq
		{"x = [\n  1,\n  2,\n]", "File(Set(Path ArrayExpr(Literal Literal)))", nil},
		{"x = { a = 1, b = [2]\n  c = \"d\" }", "File(Set(Path MapExpr(MapEntry(Path Literal) MapEntry(Path ArrayExpr(Literal)) MapEntry(Path StringLit))))", nil},
		{"x = {{ text }}", "File(Set(Path ProseBlock))", nil},
		// type_operators, type_arguments
		{"x: A | B & C", "File(Declare(Path IntersectionType(UnionType(NamedType(Path) NamedType(Path)) NamedType(Path))))", nil},
		{"x: String? = null", "File(Value(Path OptionalType(NamedType(Path)) PathExpr(Path)))", nil},
		{"x: List(Payment)", "File(Declare(Path NamedType(Path Args(PositionalArg(PathExpr(Path))))))", nil},
	})
}

func TestStrings(t *testing.T) {
	runGrammarCases(t, []grammarCase{
		// quote_runs
		{`x = ""`, "File(Set(Path StringLit))", nil},
		{`x = """a "b" c"""`, "File(Set(Path StringLit))", nil},
		{`x = """"a """ b""""`, "File(Set(Path StringLit))", nil},
		{`x = "a\"b"`, "File(Set(Path StringLit))", nil},
		// one_line_strings
		{"x = \"abc\ny = 1", "File(Set(Path StringLit) Set(Path Literal))", []string{"unterminated string"}},
		{"x = \"\"\"\n  a\n  \"\"\"", "File(Set(Path StringLit))", nil},
		// raw_runs, one_line_raw
		{"x = `a$b`", "File(Set(Path Literal))", nil},
		{"x = ``", "File(Set(Path Literal))", nil},
		{"x = ```a`b``c```", "File(Set(Path Literal))", nil},
		{"x = `abc\ny = 1", "File(Set(Path Literal) Set(Path Literal))", []string{"unterminated raw string"}},
		{"x = ```\na\n```", "File(Set(Path Literal))", nil},
		// insert_forms, inserts_switch_back
		{`x = "${a + "}"}"`, "File(Set(Path StringLit(Insert(BinaryExpr(PathExpr(Path) StringLit)))))", nil},
		{`x = "$a.b."`, "File(Set(Path StringLit(Insert(Path))))", nil},
		{`x = "$ 5 US$"`, "File(Set(Path StringLit))", nil},
		// insert_calls, nothing_follows_a_call
		{`x = "$f(1)"`, "File(Set(Path StringLit(Insert(Path Args(PositionalArg(Literal))))))", nil},
		{`x = "get_$name(@path id: Int)"`, "File(Set(Path StringLit(Insert(Path))))", nil},
		{`x = "$f(1).name"`, "File(Set(Path StringLit(Insert(Path Args(PositionalArg(Literal))))))", nil},
		// embedded_is_cast
		{`x = (sh)"ls $dir"`, "File(Set(Path CastExpr(NamedType(Path) StringLit(Insert(Path)))))", nil},
		{"x = (sql)```select```", "File(Set(Path CastExpr(NamedType(Path) Literal)))", nil},
	})
}

func TestProse(t *testing.T) {
	sec := func(lines string) string { return "s: section {\n" + lines + "\n}" }
	wrap := func(inner string) string {
		return "File(Declare(Path NamedType(Path) StructBody(" + inner + ")))"
	}
	runGrammarCases(t, []grammarCase{
		// prose_line_runs
		{sec("  | one\n  |\n  | two\n  a = 1"), wrap("ProseLines(ProseLine ProseLine ProseLine) Set(Path Literal)"), nil},
		// inline_on_one_line
		{sec("  | <details>\n  |   <title>T</title>\n  | </details>"), wrap("ProseLines(ProseLine(Tag(Path Tag(Path TagClose(Path)) TagClose(Path))))"), nil},
		// block_closes
		{"x {{ `}}` <a b={c}>d</a> ${e} \\}} }}", "File(TextNode(Path ProseBlock(Tag(Path TagAttr(PathExpr(Path)) TagClose(Path)) Insert(PathExpr(Path)))))", nil},
		// block_close_ends_fence
		{"x {{\n  ```\n  a\n}}", "File(TextNode(Path ProseBlock(Fence)))", nil},
		// prose_escapes
		{sec("  | \\$x \\[a\\](#b) \\<c> \\q"), wrap("ProseLines(ProseLine)"), nil},
		// code_spans
		{sec("  | `$x [a](#b)` and `` a ` b ``"), wrap("ProseLines(ProseLine)"), nil},
		{sec("  | an open ` $x"), wrap("ProseLines(ProseLine(Insert(Path)))"), nil},
		// links
		{sec("  | [a](#b.c) [d](https://x.y/$p)"), wrap("ProseLines(ProseLine(Link(Path) Link(Insert(Path))))"), nil},
		{sec("  | [a b] c [d](e f)"), wrap("ProseLines(ProseLine)"), nil},
		// short_links: a name or a dotted path in brackets, in backticks
		// or not, is a short link; other bracketed text, an escaped
		// bracket and a bracket in code are text.
		{sec("  | [a] [`b.c`] [d.e_f]"), wrap("ProseLines(ProseLine(ShortLink(Path) ShortLink(Path) ShortLink(Path)))"), nil},
		{sec("  | [entry points…] [ ] [1] [a b] \\[a] `[a]` [``a``]"), wrap("ProseLines(ProseLine)"), nil},
		{"x {{ see [y] }}", "File(TextNode(Path ProseBlock(ShortLink(Path))))", nil},
		{"md:See [y].\n", "ProseFile(ShortLink(Path))", nil},
		// tags
		{sec("  | <note/> <n kind=\"tip\" open>x</n>"), wrap("ProseLines(ProseLine(Tag(Path) Tag(Path TagAttr(StringLit) TagAttr TagClose(Path))))"), nil},
		{sec("  | a < b and <- c"), wrap("ProseLines(ProseLine)"), nil},
		// an @name attribute is an annotation on the element; an
		// unquoted value is an error that keeps the element
		{sec("  | <step id=\"a\" @lens=\"user\" @wip>x</step>"), wrap("ProseLines(ProseLine(Tag(Path TagAttr(StringLit) TagAttr(StringLit) TagAttr TagClose(Path))))"), nil},
		{sec("  | <toc depth=2 />"), wrap("ProseLines(ProseLine(Tag(Path TagAttr)))"), []string{"the value of depth needs quotes or braces"}},
		// fences
		{sec("  | ~~~json\n  | {\"$a\": [1]}\n  | ~~~"), wrap("ProseLines(ProseLine(Fence))"), nil},
		{"x {{\n  ````\n  ```\n  ````\n}}", "File(TextNode(Path ProseBlock(Fence)))", nil},
		// code_spans: a run of backticks that doesn't close on its `///`
		// line is text, so the next line's is too, and `}` is text in a line.
		{"/// with `versioning.{ scheme =\n/// \"SM\" }`, a patch.\nx = 1", "File(Set(Doc(DocLine DocLine) Path Literal))", nil},
		// verbatim_text: a `{{ }}` body of a kind that takes Verbatim text
		// has no tags, inserts, links or code spans, by the `:` slot's or
		// the callee's name; another kind's body has them.
		{"s: tech.surface {{\n  load(): Promise<Result> $x [a](#b) `c\n  <T>x</T> \\}} }}", "File(Declare(Path NamedType(Path) ProseBlock))", nil},
		{"surface(title = \"a.ts\") {{ <Result> ${x} }}", "File(Invoke(Path Args(NamedArg(StringLit)) ProseBlock))", nil},
		{"production {{ \"<\" Path TagAttr* \">\" }}", "File(TextNode(Path ProseBlock))", nil},
		{"e: code(lang = \"ts\") {{ Map<K> }}", "File(Declare(Path NamedType(Path Args(NamedArg(StringLit))) ProseBlock))", nil},
		{"n: note {{ <b>x</b> }}", "File(Declare(Path NamedType(Path) ProseBlock(Tag(Path TagClose(Path)))))", nil},
		// headings, heron_fences
		{"md:# T {#t}\n\nText $a.\n\n```heron\nx: y\n```\n", "ProseFile(Heading Insert(Path) HeronFence(Declare(Path NamedType(Path))))", nil},
		{"md:####### seven\n#no\n", "ProseFile", nil},
		// heron_fences: inside an element a heron fence is code
		{"md:<x id=\"a\">\n```heron\ny: z\n```\n</x>\n", "ProseFile(Tag(Path TagAttr(StringLit) Fence TagClose(Path)))", nil},
		{"md:```heron\nx: y\n", "ProseFile(HeronFence(Declare(Path NamedType(Path))))", []string{"expected the closing fence"}},
	})
}

func TestRecovery(t *testing.T) {
	runGrammarCases(t, []grammarCase{
		// rest_of_line, union_not_prose
		{"a = 1 b\nc = 2", "File(Set(Path Literal) ErrorNode Set(Path Literal))", []string{"expected the end of the line"}},
		{"x = a | b", "File(Set(Path PathExpr(Path)) ErrorNode)", []string{"expected the end of the line"}},
		// no_member
		{"= 1\na = 2", "File(ErrorNode(ErrorNode) Set(Path Literal))", []string{"expected a member"}},
		{"}", "File(ErrorNode(ErrorNode))", []string{"expected a member"}},
		// missing_parts
		{"a: b {\n", "File(Declare(Path NamedType(Path) StructBody))", []string{"expected `}`"}},
		{"x {{ abc", "File(TextNode(Path ProseBlock))", []string{"expected `}}`"}},
		{"a =", "File(Set(Path ErrorNode))", []string{"expected a value after `=`"}},
		{"let x", "File(Let)", []string{"expected `=`"}},
		{"x = [1, =]", "File(Set(Path ArrayExpr(Literal ErrorNode ErrorNode)))", []string{"expected a value", "expected `,` or `]`"}},
		{"x: ", "File(Declare(Path ErrorNode))", []string{"expected a type"}},
		{"f(@a): k", "File(Declare(Path Params(Param(Annotation(Path))) NamedType(Path)))", []string{"expected a parameter name", "expected `:` and a type"}},
		{"x = (1", "File(Set(Path ParenExpr(Literal)))", []string{"expected `)`"}},
		{"t: task do {\n  for x y {\n  }\n}", "File(Declare(Path NamedType(Path) DoBlock(Code(ExprStmt(ForExpr(PathExpr(Path) Code))))))", []string{"expected `in`"}},
		{"t: task do {\n  if a\n}", "File(Declare(Path NamedType(Path) DoBlock(Code(ExprStmt(IfExpr(PathExpr(Path) Code))))))", []string{"expected `{`"}},
		{`x = "${a"`, "File(Set(Path StringLit(Insert(PathExpr(Path)))))", []string{"expected `}`"}},
		// list_entries
		{"f(a b)", "File(Invoke(Path Args(PositionalArg(PathExpr(Path)) ErrorNode)))", []string{"expected `,` or `)`"}},
		{"x = [1 2]", "File(Set(Path ArrayExpr(Literal ErrorNode)))", []string{"expected `,` or `]`"}},
		// map_entries
		{"x = { 1 }", "File(Set(Path MapExpr(MapEntry(ErrorNode))))", []string{"expected a map entry"}},
		{"x = { a 1 }", "File(Set(Path MapExpr(MapEntry(Path) ErrorNode)))", []string{"expected `=` in a map entry", "expected `,`, a new line or `}`"}},
		// tag_closing
		{"s: section {\n  | <a>x</b>\n}", "File(Declare(Path NamedType(Path) StructBody(ProseLines(ProseLine(Tag(Path TagClose(Path)))))))", []string{"`</b>` closes `<a>`"}},
		{"s: section {\n  | <a>x\n}", "File(Declare(Path NamedType(Path) StructBody(ProseLines(ProseLine(Tag(Path))))))", []string{"`<a>` is never closed"}},
	})
}

// nesting_limit, syntax_errors: a hostile depth is cut off with one
// diagnostic at the offset where it starts, and the tree stays lossless.
func TestRecoveryDepth(t *testing.T) {
	src := "x = " + strings.Repeat("[", 300)
	tr := Parse("t.heron", src)
	if tr.Root.Text() != src {
		t.Fatal("not lossless")
	}
	var deep int
	for _, d := range tr.Diags {
		if d.Message == "nesting is too deep" {
			deep++
		}
	}
	if deep != 1 {
		t.Fatalf("got %d nesting diagnostics, want 1: %v", deep, tr.Diags)
	}
}

// A fence's lines read as written: in a prose file, where an empty line
// and a line's indentation have no `|` before them, as in `|` lines,
// where the space after each `|` is the renderer's to remove.
func TestFenceLines(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"t.heron.md", "<x id=\"a\">\n```heron\na {\n\n  b\n}\n```\n</x>\n", "```heron|a {||  b|}|```"},
		{"t.heron", "s: section {\n  | ```heron\n  | a {\n  |\n  |   b\n  | }\n  | ```\n}\n", " ```heron| a {||   b| }| ```"},
	} {
		var got []string
		Parse(c.name, c.src).Root.Walk(func(n *Node) bool {
			if n.Kind == Fence {
				got = FenceLines(n)
			}
			return true
		})
		if strings.Join(got, "|") != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}
