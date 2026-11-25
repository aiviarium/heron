package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
)

func checkDir(t *testing.T, dir string) *check.Result {
	t.Helper()
	abs, _ := filepath.Abs(filepath.Join(dir, "index.heron"))
	id, err := resolve.Canonical(abs)
	if err != nil {
		t.Fatal(err)
	}
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("%s doesn't check: %v", dir, res.Diags)
	}
	return res
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDiscoverTheSamplePackage(t *testing.T) {
	res := checkDir(t, "../../../web/samples/inventory/example")
	d := Discover(res.Tree, res.Checker)
	if len(d.Specs) != 1 || d.Specs[0].Name != JSHost {
		t.Fatalf("specs: %+v", d.Specs)
	}
	var got []string
	for _, dc := range d.Specs[0].Decls {
		got = append(got, string(dc.Point)+" "+dc.Path+" "+filepath.Base(dc.Module)+"#"+dc.Export)
	}
	want := []string{
		"generator inventory.catalog index.ts#catalog",
		"func inventory.count index.ts#count",
		"func inventory.describe index.ts#describe",
		"transport inventory.memory transport.ts#default",
		"lint inventory.no_todo index.ts#noTodo",
		"func inventory.slugify index.ts#slugify",
		"resource inventory.store index.ts#store",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for _, dc := range d.Specs[0].Decls {
		switch dc.Path {
		case "inventory.slugify":
			if dc.Pure == nil || !*dc.Pure || dc.Returns == nil || dc.Returns.Name != "String" || len(dc.Params) != 1 || dc.Params[0].Name != "text" {
				t.Fatalf("slugify: %+v", dc.Declaration)
			}
		case "inventory.catalog":
			if dc.Target != "catalog" {
				t.Fatalf("catalog: %+v", dc.Declaration)
			}
		case "inventory.store":
			if dc.Returns == nil || dc.Returns.Name != "Store" {
				t.Fatalf("store: %+v", dc.Declaration)
			}
		}
	}
}

func TestDiscoverExternDeclarations(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": `import = ["std/schema@1", "std/types@1"]

compliance: extension(command = ["acme-compliance", "serve"], files = ["./out"], network = ["api.acme.test"])

@extern(compliance)
redact(text: String): func(String)

@extern(compliance)
no_plain_secrets: lint
`})
	res := checkDir(t, dir)
	d := Discover(res.Tree, res.Checker)
	if len(d.Specs) != 1 {
		t.Fatalf("specs: %+v", d.Specs)
	}
	s := d.Specs[0]
	if s.Name != "compliance" || strings.Join(s.Command, " ") != "acme-compliance serve" || s.Files[0] != "./out" || s.Network[0] != "api.acme.test" {
		t.Fatalf("spec: %+v", s)
	}
	if len(s.Decls) != 2 || s.Decls[0].Path != "no_plain_secrets" || s.Decls[1].Path != "redact" {
		t.Fatalf("decls: %+v", s.Decls)
	}
}
