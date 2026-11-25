package lsp_test

import (
	"context"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/lsp"
	"github.com/aiviarium/heron/internal/tools/lsp/lsptest"
	"github.com/aiviarium/heron/internal/tools/prose"
)

// start runs a session in process and returns a client connected to it.
func start(t *testing.T, root string) *lsptest.Client {
	t.Helper()
	r := registry.New()
	cli.Register(r)
	prose.Register(r)
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- lsp.Serve(ctx, struct {
			io.Reader
			io.Writer
		}{sr, sw}, lsp.Options{Registry: r, Debounce: time.Millisecond})
	}()
	c := lsptest.New(cr, cw)
	t.Cleanup(func() {
		c.Shutdown()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the session didn't end after exit")
		}
		cancel()
		sw.Close()
		cw.Close()
	})
	if err := c.Initialize(root); err != nil {
		t.Fatal(err)
	}
	return c
}

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// heronFiles lists the .heron and .heron.md files under a directory.
func heronFiles(t *testing.T, dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".heron") || strings.HasSuffix(p, ".heron.md")) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noDiagnostics(t *testing.T, c *lsptest.Client, when string) {
	t.Helper()
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	for u, ds := range c.AllDiagnostics() {
		t.Errorf("%s: %s: %v", when, u, ds)
	}
}

// spellings returns URIs that all name the same file: canonical, through
// a symlinked folder, percent-escaped, with a localhost host, and with `.`
// and `..` segments.
func spellings(t *testing.T, path, link, linkTarget string) []string {
	canon := lsptest.FileURI(path)
	out := []string{canon}
	if link != "" {
		if rel, err := filepath.Rel(linkTarget, path); err == nil && !strings.HasPrefix(rel, "..") {
			out = append(out, lsptest.FileURI(filepath.Join(link, rel)))
		}
	}
	u, _ := url.Parse(canon)
	esc := strings.ReplaceAll(u.Path, "e", "%65")
	out = append(out, "file://"+esc)
	out = append(out, "file://localhost"+u.Path)
	dir, base := filepath.Split(path)
	out = append(out, lsptest.FileURI(dir)+"/./../"+filepath.Base(dir)+"/"+base)
	return out
}

// Every file of the sample repository and of spec/ opens with zero
// diagnostics, whatever URI spelling and order reach it, through an entry
// that imports or includes it, and after edits, closes and reopens.
func TestOpenEveryFileWithoutFalseDiagnostics(t *testing.T) {
	root := repoRoot(t)
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{"examples/petstore", "spec"} {
		dir := filepath.Join(root, set)
		files := heronFiles(t, dir)
		if len(files) == 0 {
			t.Fatalf("no files in %s", set)
		}
		for order := 0; order < 3; order++ {
			t.Run(set+"/order"+string(rune('0'+order)), func(t *testing.T) {
				c := start(t, root)
				fs := append([]string(nil), files...)
				switch order {
				case 1:
					sort.Sort(sort.Reverse(sort.StringSlice(fs)))
				case 2:
					// The entry first, then the rest.
					sort.SliceStable(fs, func(i, j int) bool { return strings.HasSuffix(fs[i], "/"+set+"/index.heron") })
				}
				var opened []string
				for i, f := range fs {
					sp := spellings(t, f, link, root)
					uri := sp[(i+order)%len(sp)]
					if err := c.Open(uri, read(t, f)); err != nil {
						t.Fatal(err)
					}
					opened = append(opened, uri)
				}
				noDiagnostics(t, c, "after opening")
				// A second spelling of an open file is the same document.
				extra := spellings(t, fs[0], link, root)[1]
				c.Open(extra, read(t, fs[0]))
				noDiagnostics(t, c, "after a second spelling")
				c.Close(extra)
				// Edit every file without changing what it means, then put
				// it back.
				for i, f := range fs {
					c.Replace(opened[i], read(t, f)+"\n// edited\n")
				}
				noDiagnostics(t, c, "after edits")
				for i := range fs {
					c.Close(opened[i])
				}
				noDiagnostics(t, c, "after closing")
				for i := len(fs) - 1; i >= 0; i-- {
					c.Open(opened[i], read(t, fs[i]))
				}
				noDiagnostics(t, c, "after reopening")
			})
		}
	}
}

func write(t *testing.T, p, src string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A real error appears where it is and clears when it's fixed; a break in
// an imported file shows in its importer, whether the import is open or
// changes on disk.
func TestErrorsAppearClearAndReachImporters(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib", "index.heron")
	app := filepath.Join(dir, "app", "index.heron")
	libSrc := "import = [\"std/doc@1\"]\n\n/// The thing the app links to.\nthing: section {{ A thing. }}\n"
	appSrc := "import = [\"std/doc@1\", \"../lib/\"]\n\nusage: section {{ See [the thing](#lib.thing). }}\n"
	write(t, lib, libSrc)
	write(t, app, appSrc)
	c := start(t, dir)
	appURI, libURI := lsptest.FileURI(app), lsptest.FileURI(lib)
	c.Open(appURI, appSrc)
	noDiagnostics(t, c, "app alone")

	c.Open(libURI, libSrc)
	noDiagnostics(t, c, "app and lib")

	// A real error in the open document.
	c.Replace(appURI, appSrc+"broken: Nope\n")
	c.Sync()
	ds := c.Diagnostics(appURI)
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "Nope") || ds[0].Source != "heron check" || ds[0].Range.Start.Line != 3 {
		t.Fatalf("want one error about Nope on line 4 from heron check, got %v", ds)
	}
	c.Replace(appURI, appSrc)
	noDiagnostics(t, c, "after the fix")

	// Renaming the node in the open import breaks the importer's link.
	c.Replace(libURI, strings.Replace(libSrc, "thing:", "other:", 1))
	c.Sync()
	if ds := c.Diagnostics(appURI); len(ds) != 1 || !strings.Contains(ds[0].Message, "thing") {
		t.Fatalf("the importer should show the broken link, got %v", ds)
	}
	c.Replace(libURI, libSrc)
	noDiagnostics(t, c, "after the import is fixed")

	// Closed, the import is read from disk; a change on disk reaches the
	// importer through didChangeWatchedFiles.
	c.Close(libURI)
	noDiagnostics(t, c, "after closing the import")
	write(t, lib, strings.Replace(libSrc, "thing:", "other:", 1))
	c.FilesChanged(libURI)
	c.Sync()
	if ds := c.Diagnostics(appURI); len(ds) != 1 {
		t.Fatalf("a change on disk should reach the importer, got %v", c.AllDiagnostics())
	}
	write(t, lib, libSrc)
	c.FilesChanged(libURI)
	noDiagnostics(t, c, "after the disk is fixed")
}
