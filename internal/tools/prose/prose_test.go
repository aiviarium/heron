package prose_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/tools/prose"
)

func write(t *testing.T, p, src string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func needVale(t *testing.T) {
	if prose.Binary() == "" {
		if os.Getenv("HERON_REQUIRE_VALE") != "" {
			t.Fatal("Vale isn't installed")
		}
		t.Skip("Vale isn't installed")
	}
}

// valeRepo writes a repository with a .vale.ini, a substitution style and
// a vocabulary.
func valeRepo(t *testing.T, index string) string {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".vale.ini"), "StylesPath = styles\nMinAlertLevel = suggestion\nVocab = Team\n\n[*]\nBasedOnStyles = Vale, Team\n")
	write(t, filepath.Join(dir, "styles/Team/Simple.yml"), "extends: substitution\nmessage: \"Use '%s' instead of '%s'.\"\nlevel: warning\nignorecase: true\naction:\n  name: replace\nswap:\n  utilize: use\n")
	write(t, filepath.Join(dir, "styles/config/vocabularies/Team/accept.txt"), "Heron\n")
	write(t, filepath.Join(dir, "index.heron"), index)
	return dir
}

type finding struct {
	line, col  int
	code, text string
	fixes      []string
}

func lintDir(t *testing.T, dir string, cache *lint.ProseCache) ([]finding, diag.List) {
	t.Helper()
	r := registry.New()
	cli.Register(r)
	prose.Register(r)
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	s := check.NewSession()
	res := s.Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("check: %v", res.Diags)
	}
	ds := lint.RunWith(r, res, cache)
	var out []finding
	for _, d := range ds {
		src, _ := os.ReadFile(d.File)
		l, c := syntax.LineCol(string(src), d.Start)
		f := finding{line: l, col: c, code: d.Code, text: string(src[d.Start:d.End])}
		for _, fx := range d.Fixes {
			f.fixes = append(f.fixes, fx.Edits[0].New)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].line*1000+out[i].col < out[j].line*1000+out[j].col })
	return out, ds
}

const valeIndex = `import = ["std/doc@1"]

/// We utilize this guide.
guide: section {
  | We utilize the thign, not ` + "`utilize`" + ` or [utilize](#guide) or $guide.
  notes: section {{
    Héllo wörld, we utilize Heron.
  }}
}
`

// Vale alerts land on the right words of the source, never inside code,
// links or inserts, with Vale's fixes.
func TestValeAlertsMapToWords(t *testing.T) {
	needVale(t)
	dir := valeRepo(t, valeIndex)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	want := []finding{
		{3, 8, "Vale.Team.Simple", "utilize", []string{"use"}},
		{5, 8, "Vale.Team.Simple", "utilize", []string{"use"}},
		{5, 20, "Vale.Vale.Spelling", "thign", nil},
		{7, 23, "Vale.Team.Simple", "utilize", []string{"use"}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		g := got[i]
		if g.line != w.line || g.col != w.col || g.code != w.code || g.text != w.text {
			t.Errorf("finding %d: got %+v, want %+v", i, g, w)
		}
		if w.fixes != nil && (len(g.fixes) == 0 || g.fixes[0] != w.fixes[0]) {
			t.Errorf("finding %d fixes: %v", i, g.fixes)
		}
	}
	// Spelling suggestions come from `vale fix`.
	if len(got[2].fixes) == 0 {
		t.Errorf("no spelling suggestions for thign")
	}
	// A substitution with one replacement is deterministic; a spelling
	// suggestion is a guess that `heron lint -fix` must never apply, since
	// one turned "subtree's" into "sub tree's".
	_, ds := lintDir(t, dir, lint.NewProseCache())
	for _, d := range ds {
		for _, fx := range d.Fixes {
			if fx.Suggestion != (d.Code == "Vale.Vale.Spelling") {
				t.Errorf("%s: fix %q has suggestion %v", d.Code, fx.Title, fx.Suggestion)
			}
		}
	}
}

// The `lint { vale { … } }` configuration: the config path, the lowest
// level and the skipped scopes.
func TestValeConfiguration(t *testing.T) {
	needVale(t)
	src := strings.Replace(valeIndex, `import = ["std/doc@1"]`, `import = ["std/doc@1", "std/lint@1"]

lint {
  vale {
    config = "./.vale.ini"
    min_level = "error"
    skip = ["code", "heading"]
  }
}`, 1)
	dir := valeRepo(t, src)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	if len(got) != 1 || got[0].code != "Vale.Vale.Spelling" {
		t.Fatalf("with min_level error, only the spelling error: %+v", got)
	}
	// A config elsewhere, relative to the file that names it.
	os.MkdirAll(filepath.Join(dir, "conf"), 0o755)
	os.Rename(filepath.Join(dir, ".vale.ini"), filepath.Join(dir, "conf/.vale.ini"))
	os.Rename(filepath.Join(dir, "styles"), filepath.Join(dir, "conf/styles"))
	write(t, filepath.Join(dir, "index.heron"), strings.Replace(src, `"./.vale.ini"`, `"./conf/.vale.ini"`, 1))
	if got, _ := lintDir(t, dir, lint.NewProseCache()); len(got) != 1 {
		t.Fatalf("a config in another folder: %+v", got)
	}
}

// Accepting a word in the vocabulary clears its alert; unchanged regions
// keep their cached alerts, and only changed ones go to Vale again.
func TestVocabularyActionsAndCaching(t *testing.T) {
	needVale(t)
	dir := valeRepo(t, valeIndex)
	cache := lint.NewProseCache()
	_, ds := lintDir(t, dir, cache)
	var spell diag.Diagnostic
	for _, d := range ds {
		if d.Code == "Vale.Vale.Spelling" {
			spell = d
		}
	}
	acts := prose.Actions(spell, "thign")
	if len(acts) != 2 || !strings.Contains(acts[0].Title, `Accept "thign" in vocabulary Team`) {
		t.Fatalf("actions: %+v", acts)
	}
	linted := cache.Linted
	lintDir(t, dir, cache)
	if cache.Linted != linted {
		t.Errorf("an unchanged tree went to Vale again: %d regions", cache.Linted-linted)
	}
	if err := prose.Run(acts[0].Command, acts[0].Args); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "styles/config/vocabularies/Team/accept.txt"))
	if string(b) != "Heron\nthign\n" {
		t.Fatalf("accept.txt: %q", b)
	}
	got, _ := lintDir(t, dir, cache)
	for _, f := range got {
		if f.code == "Vale.Vale.Spelling" {
			t.Fatalf("accepted word still flagged: %+v", got)
		}
	}
	// Rejecting moves it to reject.txt.
	if err := prose.Run(acts[1].Command, acts[1].Args); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "styles/config/vocabularies/Team/accept.txt"))
	r, _ := os.ReadFile(filepath.Join(dir, "styles/config/vocabularies/Team/reject.txt"))
	if string(b) != "Heron\n" || string(r) != "thign\n" {
		t.Fatalf("after reject: accept %q, reject %q", b, r)
	}
	// Arguments that don't name the configured vocabulary are refused.
	if err := prose.Run(acts[0].Command, []any{acts[0].Args[0], "../../etc", "x"}); err == nil {
		t.Fatal("an unknown vocabulary was accepted")
	}
	lintDir(t, dir, cache) // the vocabulary changed: every region again
	// An edit to one region sends that region alone.
	write(t, filepath.Join(dir, "index.heron"), strings.Replace(valeIndex, "Héllo wörld", "Hello world", 1))
	linted = cache.Linted
	lintDir(t, dir, cache)
	if n := cache.Linted - linted; n != 1 {
		t.Errorf("an edit to one region sent %d regions to Vale", n)
	}
}

// In a prose file, a node's element holds its text: a term's own
// definition doesn't ask to link the term, and a rule's `<must>` is a must.
func TestSpecLintsInProseFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), `import = ["std/doc@1", "std/lint@1"]

include = ["./refunds.heron.md"]
`)
	write(t, filepath.Join(dir, "refunds.heron.md"), `# Refunds {#refunds}

<goal id="refunds_work">Customers get their money back.</goal>

<term id="capture_date">
The day the payment is captured, which is the capture date.
</term>

<rule id="late" @targets="refunds">
<must>the system should reject a refund after the capture date</must>
</rule>
`)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	var codes []string
	for _, f := range got {
		codes = append(codes, f.code+" "+f.text)
	}
	sort.Strings(codes)
	want := []string{"Spec.MustSaysShould should", "Spec.UnlinkedTerm capture date"}
	if strings.Join(codes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(codes, "\n"), strings.Join(want, "\n"))
	}
}

const specIndex = `import = ["std/doc@1", "std/lint@1"]

refunds_work: goal {{ Customers get their money back. }}

capture_date: term(aliases = ["captured at"]) {{
  The day the payment is captured.
}}

window: term {{ The span of time. }}
refund_window: term(aliases = ["window"]) {{ Thirty days. }}
type_kind: term {{ A record. }}
entry_point: term(aliases = ["entry point"]) {{ Where a tree starts. }}

refunds: section {
  | Refunds count from the capture date, not when captured at, see the
  | [capture date](#capture_date), place_order and the [guide](./missing.heron.md).

  late: rule {
    must {{ the system should reject a late refund }}
    when {{ the request is fast }}
  }

  quoting: rule {
    must {{ reject the words "fast" and "should" when they're used }}
  }

  @serves(speed)
  timed: rule {
    must {{ answer quickly }}
  }

  speed: quality(measure = "Within a second.") {{ Answers feel instant. }}

  place_order: section {{ Placing. }}

  // Words of a phrase are separated by spaces only.
  listed: section {{ Every type, kind and binding. }}
}
`

func TestSpecLints(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), specIndex)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	var codes []string
	for _, f := range got {
		codes = append(codes, f.code+" "+f.text)
	}
	want := []string{
		"Spec.TwoSenses window",
		"Spec.TwoSenses refund_window",
		"Spec.UnlinkedTerm capture date",
		"Spec.Synonym captured at",
		"Spec.UnlinkedNode place_order",
		"Spec.DeadLink [guide](./missing.heron.md)",
		"Spec.MustSaysShould should",
		"Spec.VagueWord fast",
	}
	sort.Strings(codes)
	sort.Strings(want)
	if strings.Join(codes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(codes, "\n"), strings.Join(want, "\n"))
	}
	for _, f := range got {
		switch f.code {
		case "Spec.UnlinkedTerm":
			if len(f.fixes) != 1 || f.fixes[0] != "[capture date](#capture_date)" {
				t.Errorf("unlinked term fix: %v", f.fixes)
			}
		case "Spec.Synonym":
			if len(f.fixes) != 1 || f.fixes[0] != "[capture date](#capture_date)" {
				t.Errorf("synonym fix: %v", f.fixes)
			}
		case "Spec.UnlinkedNode":
			// The name names one node: the fix wraps it as a short link.
			if len(f.fixes) != 1 || f.fixes[0] != "[place_order]" {
				t.Errorf("unlinked node fix: %v", f.fixes)
			}
		}
	}
	// Without std/lint, none of them run.
	write(t, filepath.Join(dir, "index.heron"), strings.Replace(specIndex, `, "std/lint@1"`, "", 1))
	if got, _ := lintDir(t, dir, lint.NewProseCache()); len(got) != 0 {
		t.Fatalf("without std/lint: %+v", got)
	}
}
