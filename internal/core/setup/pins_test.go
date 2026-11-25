package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// A package from git at major version 0, as `…//heron@v0`, is pinned like
// any other git package; only a file import, whose Major is also 0, isn't.
func TestPackagePinsKeepGitAtV0(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte("heron: package(version = \"0.1.0\") {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := &resolve.File{ID: resolve.FileID(filepath.Join(dir, "index.heron"))}
	g := &resolve.GitImport{URL: "https://github.com/aiviarium/heron-specs", Path: "heron", Name: "github.com/aiviarium/heron-specs/heron"}
	tree := &resolve.Tree{Imports: []*resolve.Package{
		{Name: g.Name, Major: 0, Version: "0.1.0", Git: g, Tree: &resolve.Tree{Entry: entry}},
		{Name: "../local/", Major: 0, Tree: &resolve.Tree{Entry: entry}},
	}}
	pins := packagePins(tree)
	if len(pins) != 1 {
		t.Fatalf("pins %v, want the git package alone", pins)
	}
	p, ok := pins[g.Name]
	if !ok || p.URL != g.URL || p.Path != "heron" || p.Version != "0.1.0" || p.Checksum == "" {
		t.Fatalf("pin %+v", p)
	}
}
