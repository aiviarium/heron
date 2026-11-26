package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes files under a temp dir and returns the dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func resolveDir(t *testing.T, dir, entry string) *Tree {
	t.Helper()
	id, err := Canonical(filepath.Join(dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	return New(NewLoader()).Resolve(id)
}

func codes(tr *Tree) []string {
	var out []string
	for _, d := range tr.Diags {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func wantNoDiags(t *testing.T, tr *Tree) {
	t.Helper()
	if len(tr.Diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", codes(tr))
	}
}

func wantDiag(t *testing.T, tr *Tree, sub string) {
	t.Helper()
	for _, c := range codes(tr) {
		if strings.Contains(c, sub) {
			return
		}
	}
	t.Fatalf("no diagnostic containing %q in %v", sub, codes(tr))
}

// One identity per file: every spelling of a path, including through a
// symlinked directory, reaches the same FileID, so a file included twice by
// different spellings enters the tree once.
func TestCanonicalIdentity(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"real/a.heron": "a_node = 1\n",
	})
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "real"), link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	spellings := []string{
		filepath.Join(dir, "real", "a.heron"),
		filepath.Join(dir, "real", ".", "a.heron"),
		filepath.Join(dir, "real", "..", "real", "a.heron"),
		filepath.Join(link, "a.heron"),
	}
	var first FileID
	for i, s := range spellings {
		id, err := Canonical(s)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		} else if id != first {
			t.Errorf("%s has identity %s, want %s", s, id, first)
		}
	}
	// A relative spelling from the working directory agrees too.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if id, _ := Canonical("link/a.heron"); id != first {
		t.Errorf("relative spelling has identity %s, want %s", id, first)
	}
	// A file that doesn't exist yet gets the identity it will have.
	want := FileID(filepath.Join(string(first.Dir()), "new.heron"))
	if id, _ := Canonical(filepath.Join(link, "new.heron")); id != want {
		t.Errorf("unsaved file has identity %s, want %s", id, want)
	}

	// Included through both spellings, the file joins the tree once.
	root := writeTree(t, map[string]string{})
	_ = root
	idx := filepath.Join(dir, "index.heron")
	os.WriteFile(idx, []byte(`include = ["./real/a.heron", "./link/a.heron", "./real/../real/a.heron"]`+"\n"), 0o644)
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if len(tr.Files) != 2 {
		t.Fatalf("tree has %d files, want 2", len(tr.Files))
	}
}

func TestIncludeGuardAndMount(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron": `include = ["./model.heron", "./refunds/"]
checks.{
  include = ["./model.heron"]
}
`,
		"model.heron":            "pet_kind = \"dog\"\n",
		"refunds/index.heron":    "include = [\"./*.heron\", \"!./draft.heron\"]\n",
		"refunds/window.heron":   "window_days = 30\n",
		"refunds/draft.heron":    "draft_only = 1\n",
		"refunds/sub/deep.heron": "deep = 1\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if tr.Find("pet_kind") == nil {
		t.Error("model.heron should join the root flat")
	}
	if tr.Find("checks.pet_kind") != nil {
		t.Error("a second include must not copy the file")
	}
	if tr.Find("refunds.window_days") == nil {
		t.Error("a folder include mounts its index under the folder's name")
	}
	if tr.Find("refunds.draft_only") != nil {
		t.Error("an excluded file must not be included")
	}
	if tr.Find("refunds.deep") != nil {
		t.Error("* must not cross directories")
	}
	// The guarded file now also sees the checks scope.
	sc := tr.FileScope(tr.Find("pet_kind").Decl.File.ID)
	if len(sc.Also) != 1 {
		t.Errorf("the second include should add one scope, got %d", len(sc.Also))
	}
}

func TestIncludeCycleIsFinite(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron": "include = [\"./a.heron\"]\n",
		"a.heron":     "include = [\"./b.heron\"]\na_val = 1\n",
		"b.heron":     "include = [\"./a.heron\"]\nb_val = 2\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if tr.Find("a_val") == nil || tr.Find("b_val") == nil {
		t.Error("both files of a cycle join the tree")
	}
}

func TestDeclareEnterConflicts(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron": `import = ["./kinds.heron"]
include = ["./a.heron", "./b.heron"]

// Entered before it's declared: resolution takes rounds.
gauge {
  owner.team = "platform"
}

gauge: widget {
  size = 3
}

gauge: widget
`,
		"kinds.heron": "widget: kind\n",
		"a.heron":     "shared = 1\n",
		"b.heron":     "shared = 2\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	wantDiag(t, tr, "gauge is declared twice")
	wantDiag(t, tr, "shared is set in two files")
	g := tr.Find("gauge")
	if g == nil || g.Shape != ShapeDeclared || tr.Find("gauge.owner.team") == nil || tr.Find("gauge.size") == nil {
		t.Fatalf("the enter and the declaration should make one node: %+v", g)
	}
	if b := tr.FileScope(tr.Entry.ID).Lookup("widget", nil, -1); b.Node == nil {
		t.Error("an imported file's nodes resolve in the importing scope")
	}
	if tr.Find("widget") != nil {
		t.Error("an import puts no nodes in the tree")
	}
}

func TestLaterAmendWinsWhenIncluded(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron":    "include = [\"./base.heron\"]\nretries = 3\nci.{\n  timeout = \"20m\"\n}\nci.timeout = \"30m\"\n",
		"base.heron":     "retries = 1\n",
		"settings.heron": "",
	})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if v := tr.Find("retries").Value.Syntax().Text(); v != "3" {
		t.Errorf("retries = %s, want the later amend 3", v)
	}
	if v := tr.Find("ci.timeout").Value.Syntax().Text(); v != `"30m"` {
		t.Errorf("ci.timeout = %s, want \"30m\"", v)
	}
}

func TestSetTwiceInOneBody(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": "a = 1\na = 2\n"})
	wantDiag(t, resolveDir(t, dir, "index.heron"), "a is set twice in one body")
}

func TestMapsMergeAndAssignReplaces(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": `ci.{
  retries = { main = 0, develop = 2 }
}
ci.retries.staging = 0
`})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	for _, k := range []string{"main", "develop", "staging"} {
		if tr.Find("ci.retries."+k) == nil {
			t.Errorf("ci.retries.%s missing", k)
		}
	}
}

func TestScopedImports(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron":         "include = [\"./ci.heron\", \"./docs.heron\"]\n",
		"ci.heron":            "import = [\"./helpers/\"]\nci_uses = 1\n",
		"docs.heron":          "docs_uses = 1\n",
		"helpers/index.heron": "helper: kind\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	ci := tr.Find("ci_uses").Decl.Scope
	docs := tr.Find("docs_uses").Decl.Scope
	if !ci.Lookup("helper", nil, -1).Found() {
		t.Error("an import is visible in its file")
	}
	if docs.Lookup("helper", nil, -1).Found() {
		t.Error("an import leaves with its scope: a sibling file must not see it")
	}
}

func TestImportCycleIsAnError(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron": "import = [\"./a.heron\"]\n",
		"a.heron":     "import = [\"./b.heron\"]\n",
		"b.heron":     "import = [\"./a.heron\"]\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	found := false
	for _, d := range tr.PackageDiags() {
		if strings.Contains(d.Message, "import cycle") {
			found = true
		}
	}
	if !found {
		t.Error("an import cycle must be reported")
	}
}

func TestExportsMountOnce(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pkgs/acme/ci/package.heron": `ci: package(version = "1.0") {
  Development: type
  development<export>: Development
}
`,
		"pkgs/acme/other/package.heron": "development<export>: Thing\n",
		"index.heron": `import = ["acme/ci@1"]
include = ["./more.heron"]
development {
  max_open = 3
}
`,
		"more.heron":  "import = [\"acme/ci@1\"]\n",
		"clash.heron": "import = [\"acme/ci@1\", \"acme/other@1\"]\n",
	})
	r := New(NewLoader())
	r.PackageDirs = []string{filepath.Join(dir, "pkgs")}
	id, _ := Canonical(filepath.Join(dir, "index.heron"))
	tr := r.Resolve(id)
	wantNoDiags(t, tr)
	d := tr.Find("development")
	if d == nil || d.Shape != ShapeMounted || tr.Find("development.max_open") == nil {
		t.Fatalf("development should be mounted once and entered: %+v", d)
	}
	id, _ = Canonical(filepath.Join(dir, "clash.heron"))
	wantDiag(t, r.Resolve(id), "two packages export development")
}

// A heading declares the section its {#name} names; a heading without
// one is reported, never named from its text.
func TestProseFileHeadings(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"guide.heron.md": "# Refund guide {#refund_guide}\n\nText.\n\n## Before you start {#before_you_start}\n\nMore.\n\n## Limits {#refund_limits}\n\n```heron\nlimit = 1\n```\n",
	})
	tr := resolveDir(t, dir, "guide.heron.md")
	wantNoDiags(t, tr)
	for _, p := range []string{"refund_guide", "refund_guide.before_you_start", "refund_guide.refund_limits.limit"} {
		if tr.Find(p) == nil {
			t.Errorf("%s missing", p)
		}
	}
	dir = writeTree(t, map[string]string{"guide.heron.md": "# Refund guide\n\nText.\n"})
	tr = resolveDir(t, dir, "guide.heron.md")
	if len(tr.Diags) != 1 || tr.Diags[0].Code != "unnamed" || tr.Find("refund_guide") != nil {
		t.Errorf("a heading without a name: %v", tr.Diags)
	}
}

// An element with an id declares a node of the text's node; inside a
// named element's body an element on lines of its own is a member, named
// or anonymous; an element inside a sentence is content; and an
// element's @name attributes are its annotations.
func TestElementsDeclareNodes(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron": "include = [\"./g.heron.md\"]\n",
		"g.heron.md":  "<howto id=\"h\" @display=\"How\">\nIntro with <em>stress</em>.\n\n<step id=\"a\">One.</step>\n\n<note>Aside.</note>\n</howto>\n\nText <kbd id=\"k\">K</kbd> and <kbd>L</kbd>.\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	h := tr.Find("h")
	if h == nil || h.Shape != ShapeElement || tr.Find("h.a") == nil || tr.Find("k") == nil {
		t.Fatalf("nodes: %v", tr.Root.Children)
	}
	if len(h.Children) != 2 || h.Children[1].Name != "" || h.Children[1].Shape != ShapeElement {
		t.Errorf("members of h: %v", h.Children)
	}
	if len(h.Annotations) != 1 {
		t.Errorf("annotations: %v", h.Annotations)
	}
	if n := len(tr.Elements); n != 4 {
		t.Errorf("%d elements declare nodes, want 4", n)
	}
}

func TestLocalsAreVisibleBelowOnly(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": "a = 1\nlet base = 2\nb = 3\n"})
	tr := resolveDir(t, dir, "index.heron")
	sc := tr.FileScope(tr.Entry.ID)
	aStart, _ := tr.Find("a").Decl.Span()
	bStart, _ := tr.Find("b").Decl.Span()
	if sc.Lookup("base", tr.Entry, aStart).Found() {
		t.Error("a local is invisible above its line")
	}
	if !sc.Lookup("base", tr.Entry, bStart).Found() {
		t.Error("a local is visible below its line")
	}
	if tr.Find("base") != nil {
		t.Error("a local is never part of the tree")
	}
}

func TestStdPackagesResolve(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": "import = [\"std/types@1\", \"std/schema@1\"]\n"})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if len(tr.PackageDiags()) > 0 {
		t.Fatalf("std packages: %v", tr.PackageDiags())
	}
	sc := tr.FileScope(tr.Entry.ID)
	for _, n := range []string{"type", "kind", "Ref", "String", "func", "Duration", "u8"} {
		if !sc.Lookup(n, nil, -1).Found() {
			t.Errorf("%s should resolve", n)
		}
	}
}

// The diagnostics of `import` and `include`, as spec/language/core_format
// states them: each names the entry and says what's wrong.
func TestImportAndIncludeDiagnostics(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron": `import = ["std/nosuch@1", "std/doc", "std/doc@x", "std/doc@2", "npm:foo@1", "./nofile.heron"]
include = ["./missing.heron", 5]
`,
	})
	tr := resolveDir(t, dir, "index.heron")
	all := append(codes(tr), func() (out []string) {
		for _, d := range tr.PackageDiags() {
			out = append(out, d.Code+": "+d.Message)
		}
		return
	}()...)
	for _, want := range []string{
		"unknown package std/nosuch",
		"std/doc: a package import names a major version, as in std/doc@1",
		"std/doc@x: the version after @ is a major version number",
		"std/doc@2: the package is version 1.0",
		"npm:foo@1: packages from npm and git are installed by `heron run setup`",
		"cannot import ./nofile.heron: no such file",
		"missing.heron: no such file",
		"include entries are plain strings",
	} {
		found := false
		for _, c := range all {
			if strings.Contains(c, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("want a diagnostic %q, got %v", want, all)
		}
	}
}

// A folder include mounts its index under the folder's name; a sibling
// reaches a mounted name through the prefix, never bare.
func TestMountedNamesArePrefixed(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.heron":        "include = [\"./orders/\"]\n",
		"orders/index.heron": "held = 1\n",
	})
	tr := resolveDir(t, dir, "index.heron")
	wantNoDiags(t, tr)
	if tr.Find("orders.held") == nil {
		t.Error("orders.held missing")
	}
	if tr.Find("held") != nil {
		t.Error("a mounted name must not be visible at the root")
	}
}
