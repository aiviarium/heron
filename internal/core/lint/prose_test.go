package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint/prose"
	"github.com/aiviarium/heron/internal/core/lint/prose/words"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

const proseSrc = "import = [\"std/doc@1\"]\n" +
	"let simply = \"x\"\n" +
	"/// Simply documented, not `simply` in code.\n" +
	"guide: section {\n" +
	"  | One line, simply\n" +
	"  | joined. See [simply](#guide) and $simply and ${simply}.\n" +
	"  |\n" +
	"  | <abbr title=\"simply\">Simply a tip</abbr>\n" +
	"  more: section {{\n" +
	"    A paragraph that simply\n" +
	"    runs on.\n" +
	"\n" +
	"    - first simply\n" +
	"    - second\n" +
	"  }}\n" +
	"}\n" +
	"tagline: Prose = \"Simply prose, `simply` not.\"\n"

func setup(t *testing.T, files map[string]string) (string, *check.Session, *registry.Registry) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644)
	}
	r := registry.New()
	r.MustAddProseProvider(words.Provider(map[string]string{"simply": "", "joined": "linked"}))
	return dir, check.NewSession(), r
}

func checkDir(t *testing.T, s *check.Session, dir string) *check.Result {
	t.Helper()
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	res := s.Check(id)
	if len(res.Diags) > 0 {
		t.Fatalf("check: %v", res.Diags)
	}
	return res
}

// The sample provider flags words in `{{ }}`, `|`, `///` and markup-typed
// text, never inside code, inserts, links or tag attributes, and its fixes
// apply to the exact source range.
func TestProseProvider(t *testing.T) {
	dir, s, r := setup(t, map[string]string{"index.heron": proseSrc})
	res := checkDir(t, s, dir)
	ds := RunWith(r, res, NewProseCache())
	src := proseSrc
	var got []string
	for _, d := range ds {
		got = append(got, d.Code+" "+src[d.Start:d.End])
	}
	want := []string{
		"Words.Flagged Simply", // ///
		"Words.Flagged simply", // | line
		"Words.Flagged joined", // across the line break
		"Words.Flagged Simply", // tag content
		"Words.Flagged simply", // {{ }} paragraph
		"Words.Flagged simply", // {{ }} item
		"Words.Flagged Simply", // Prose string
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		for _, rg := range prose.Project(res.Tree, res.Checker) {
			t.Logf("%s %s %s %q %v", rg.Node, rg.Source, rg.Block, rg.Text, rg.Masks)
		}
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	fixed := applyFixes(src, ds)
	wantFixed := strings.NewReplacer(
		"/// Simply documented", "/// documented",
		"One line, simply\n", "One line,\n",
		"  | joined.", "  | linked.",
		"\">Simply a tip", "\">a tip",
		"that simply\n", "that\n",
		"first simply", "first",
		"\"Simply prose", "\"prose",
	).Replace(src)
	if fixed != wantFixed {
		t.Fatalf("fixed:\n%s\nwant:\n%s", fixed, wantFixed)
	}
}

func applyFixes(src string, ds diag.List) string {
	var edits []diag.Edit
	for _, d := range ds {
		if len(d.Fixes) > 0 {
			edits = append(edits, d.Fixes[0].Edits...)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	for _, e := range edits {
		src = src[:e.Start] + e.New + src[e.End:]
	}
	return src
}

func TestProseAllow(t *testing.T) {
	src := "import = [\"std/doc@1\"]\n" +
		"@lint(allow = [\"Words.Flagged\"])\na: section {\n  | simply\n}\n" +
		"@lint(allow = [\"Words\"])\nb: section {\n  | simply\n}\n" +
		"c: section {\n  | simply\n}\n"
	dir, s, r := setup(t, map[string]string{"index.heron": src})
	ds := RunWith(r, checkDir(t, s, dir), NewProseCache())
	if len(ds) != 1 || !strings.HasPrefix(src[ds[0].Start:], "simply\n}\n") || ds[0].Start < strings.Index(src, "c: section") {
		t.Fatalf("got %v", ds)
	}
}

// An edit re-lints only the paragraph it changed.
func TestProseCache(t *testing.T) {
	dir, s, r := setup(t, map[string]string{"index.heron": proseSrc})
	cache := NewProseCache()
	res := checkDir(t, s, dir)
	n := len(prose.Project(res.Tree, res.Checker))
	RunWith(r, res, cache)
	if cache.Linted != n {
		t.Fatalf("first run linted %d of %d regions", cache.Linted, n)
	}
	RunWith(r, res, cache)
	if cache.Linted != n {
		t.Fatalf("an unchanged tree re-linted %d regions", cache.Linted-n)
	}
	edited := strings.Replace(proseSrc, "runs on.", "runs on and on.", 1)
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte(edited), 0o644)
	res = checkDir(t, check.NewSession(), dir)
	ds := RunWith(r, res, cache)
	if cache.Linted != n+1 {
		t.Fatalf("an edit to one paragraph re-linted %d regions", cache.Linted-n)
	}
	// Findings of unchanged regions still land where their text now is.
	for _, d := range ds {
		if got := strings.ToLower(edited[d.Start:d.End]); got != "simply" && got != "joined" {
			t.Fatalf("finding at %q", edited[d.Start:d.End])
		}
	}
}

// The HTML rendering keeps structure and maps back to region text.
func TestProseHTML(t *testing.T) {
	dir, s, _ := setup(t, map[string]string{"index.heron": proseSrc})
	res := checkDir(t, s, dir)
	regions := prose.Project(res.Tree, res.Checker)
	h := prose.RenderHTML(regions)
	for _, want := range []string{"<ul>\n<li>first simply</li>\n<li>second</li>\n</ul>", "<code>`simply`</code>", "<code>&lt;abbr title=&#34;simply&#34;&gt;</code>Simply a tip"} {
		if !strings.Contains(h.Text, want) {
			t.Fatalf("html lacks %q:\n%s", want, h.Text)
		}
	}
	at := strings.Index(h.Text, "runs on.")
	ri, pos, ok := h.Locate(at)
	if !ok || !strings.HasPrefix(regions[ri].Text[pos:], "runs on.") {
		t.Fatalf("locate: %v %d %d", ok, ri, pos)
	}
	for _, rg := range regions {
		if rg.Node == "" || rg.Hash == "" {
			t.Fatalf("region without context: %+v", rg)
		}
	}
}

// Over every fixture that checks, each exact segment of the source map
// holds the same text as the projection, and the map covers every
// character in order.
func TestProseFixtureSourceMaps(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	seen := map[*check.Result]bool{}
	regions, files := 0, map[string]bool{}
	for _, run := range check.CheckFixtures(t, root) {
		if run.Result == nil || seen[run.Result] {
			continue
		}
		seen[run.Result] = true
		srcs := map[string]string{}
		for _, f := range run.Result.Tree.Files {
			srcs[string(f.ID)] = f.Src
		}
		for _, rg := range prose.Project(run.Result.Tree, run.Result.Checker) {
			regions++
			files[rg.File] = true
			if os.Getenv("HERON_PROSE") != "" { // logs the projection
				t.Logf("%s %s(%s) %s %s%d %q %v", filepath.Base(rg.File), rg.Node, rg.Kind, rg.Source, rg.Block, rg.Level, rg.Text, rg.Masks)
			}
			at := 0
			for _, sg := range rg.Map {
				if sg.At != at {
					t.Fatalf("%s %s: map gap at %d in %q", rg.File, rg.Node, at, rg.Text)
				}
				at += sg.Len
				if sg.Exact && srcs[rg.File][sg.Src:sg.Src+sg.SrcLen] != rg.Text[sg.At:sg.At+sg.Len] {
					t.Fatalf("%s %s: segment %+v maps %q to %q", rg.File, rg.Node, sg, rg.Text[sg.At:sg.At+sg.Len], srcs[rg.File][sg.Src:sg.Src+sg.SrcLen])
				}
			}
			if at != len(rg.Text) {
				t.Fatalf("%s %s: map covers %d of %d", rg.File, rg.Node, at, len(rg.Text))
			}
		}
	}
	if regions < 100 {
		t.Fatalf("only %d regions in %d files", regions, len(files))
	}
	t.Logf("%d regions in %d files", regions, len(files))
}
