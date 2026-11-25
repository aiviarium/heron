package resolve

import "testing"

func TestGitImportResolvesFromPackagesDir(t *testing.T) {
	g, err := ParseGitImport("git+https://git@github.com/acme/specs.git//heron@v1")
	if err != nil || g.URL != "https://git@github.com/acme/specs.git" || g.Path != "heron" || g.Ref != "" || g.Major != 1 ||
		g.Name != "github.com/acme/specs/heron" {
		t.Fatalf("parse: %+v %v", g, err)
	}
	if g, err := ParseGitImport("git+file:///tmp/specs@v2.1.0"); err != nil || g.Ref != "v2.1.0" || g.Major != 2 || g.Path != "" || g.Name != "tmp/specs" {
		t.Fatalf("parse a tag: %+v %v", g, err)
	}
	for _, bad := range []string{"git+https://h/r//p", "git+https://h/r@main", "git+h/r@v1"} {
		if _, err := ParseGitImport(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
	const imp = `import = ["git+https://github.com/acme/specs//heron@v1"]
`
	dir := writeTree(t, map[string]string{
		"index.heron":    imp + "widget_count: Int = shop_limit\n",
		"sub/more.heron": imp,
		"other/x.heron":  `import = ["git+https://github.com/acme/specs//heron@v2"]` + "\n",
		".heron/packages/github.com/acme/specs/heron/package.heron": `shop: package(version = "1.3.0") {
  shop_limit<export>: Int = 4
}
`,
	})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if len(tr.GitImports) != 1 || tr.GitImports[0].Name != "github.com/acme/specs/heron" {
		t.Fatalf("git imports: %+v", tr.GitImports)
	}
	var p *Package
	for _, q := range tr.Imports {
		if q.Git != nil {
			p = q
		}
	}
	if p == nil || p.Version != "1.3.0" || p.Major != 1 {
		t.Fatalf("the git package isn't imported: %+v", p)
	}
	// A file in a folder below finds the install of the root above it.
	wantNoDiags(t, resolveDir(t, dir, "sub/more.heron"))
	wantDiag(t, resolveDir(t, dir, "other/x.heron"), "the package is version 1.3.0")
	empty := writeTree(t, map[string]string{"index.heron": imp})
	wantDiag(t, resolveDir(t, empty, "index.heron"), "packages from git are installed by `heron run setup`, and this one isn't installed yet")
}
