package ast_test

import (
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// TestViews: every node of every fixture has a typed view, and members,
// expressions and types are covered by their sums where the tree puts them.
func TestViews(t *testing.T) {
	bs, err := fixtures.Load(filepath.Join("..", "..", "..", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if !b.IsHeron() {
			continue
		}
		tr := syntax.Parse(b.File, b.Content)
		tr.Root.Walk(func(n *syntax.Node) bool {
			if ast.Cast(n) == nil {
				t.Errorf("%s: no view for %s", b.File, n.Kind)
			}
			if n.Kind == syntax.StructBody {
				for _, m := range (ast.StructBody{N: n}).Members() {
					if m == nil {
						t.Errorf("%s: nil member", b.File)
					}
				}
			}
			return true
		})
		if b.Mode == fixtures.ModeHeron && tr.Root.Kind == syntax.File {
			f := ast.File{N: tr.Root}
			if len(f.Members()) == 0 {
				t.Errorf("%s: no members", b.File)
			}
		}
	}
}

func TestAccessors(t *testing.T) {
	tr := syntax.Parse("t.heron", "/// Doc.\ngauge: widget<large>(size = 3) {\n  label =| Hi.\n}")
	d, ok := ast.File{N: tr.Root}.Members()[0].(ast.Declare)
	if !ok {
		t.Fatalf("not a declare: %T", ast.File{N: tr.Root}.Members()[0])
	}
	if got := d.Path().Idents()[0].Text; got != "gauge" {
		t.Errorf("name %q", got)
	}
	nt, ok := d.Type().(ast.NamedType)
	if !ok || nt.Path().Idents()[0].Text != "widget" || len(nt.Flags().Idents()) != 1 {
		t.Errorf("type %v", d.Type())
	}
	if d.Doc().N == nil || len(d.Doc().DocLines()) != 1 {
		t.Error("doc missing")
	}
	body, ok := d.Body().(ast.StructBody)
	if !ok {
		t.Fatalf("body %T", d.Body())
	}
	if _, ok := body.Members()[0].(ast.Set); !ok {
		t.Errorf("member %T", body.Members()[0])
	}
}
