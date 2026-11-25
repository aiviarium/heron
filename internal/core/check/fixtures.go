package check

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/fixtures"
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// TB is the part of testing.TB the fixture runner uses, so tests of other
// packages, such as lint's, can run the fixtures without an import cycle.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	TempDir() string
}

// UnitOneTabs are the tabs whose code blocks unit 1's done criterion
// covers: its own design tabs and the Example tab.
var UnitOneTabs = map[string]bool{
	"core-format": true, "schemas": true, "markup": true, "data": true, "apis": true,
	"specifications": true, "interop": true, "scripting": true, "example": true,
}

// FixtureRun is one checked Heron block of unit 1's tabs.
type FixtureRun struct {
	Block   fixtures.Block
	Session *Session
	// Result is the check whose tree holds the block's file, and File
	// that file.
	Result *Result
	File   resolve.FileID
	// Errs are the block's errors.
	Errs diag.List
	// Pending names the node of spec/ that keeps the
	// block from checking clean, as fixtures/pending.txt lists it, or is "".
	Pending string
}

// CheckFixtures checks every Heron block of unit 1's tabs under the
// repository root, each in its tab's context (fixtures/context/), and
// reports a pending list that names unknown rules or blocks.
func CheckFixtures(t TB, root string) []FixtureRun {
	t.Helper()
	blocks, err := fixtures.Load(filepath.Join(root, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	pend := readPending(t, root)
	rules := specRules(t, root)
	for k, q := range pend {
		if !rules[q] {
			t.Errorf("pending.txt: %s names %s, which is no rule in spec/", k, q)
		}
	}
	f := &fixtureRunner{t: t, root: root, sessions: map[string]*Session{}}
	repos := map[string]map[string]FixtureRun{}
	var out []FixtureRun
	seen := map[string]bool{}
	for _, b := range blocks {
		if !UnitOneTabs[b.Tab] || (b.Mode != fixtures.ModeHeron && b.Mode != fixtures.ModeProse) {
			continue
		}
		var run FixtureRun
		if layout := filepath.Join(root, "fixtures", "context", "files", b.Tab, "layout.txt"); exists(layout) {
			if repos[b.Tab] == nil {
				repos[b.Tab] = f.checkRepo(blocks, b.Tab, layout)
			}
			run = repos[b.Tab][b.Key()]
		} else {
			run = f.checkFixture(b)
		}
		run.Block, run.Pending = b, pend[b.Key()]
		seen[b.Key()] = true
		out = append(out, run)
	}
	for k := range pend {
		if !seen[k] {
			t.Errorf("pending.txt lists %s, which is no Heron block of unit 1's tabs", k)
		}
	}
	return out
}

// readPending reads fixtures/pending.txt: blocks that can't check clean
// yet, each tied to the node of spec/ that says why.
func readPending(t TB, root string) map[string]string {
	f, err := os.Open(filepath.Join(root, "fixtures", "pending.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fs := strings.Fields(line)
		if len(fs) == 0 {
			continue
		}
		if len(fs) != 2 {
			t.Fatalf("pending.txt: %q: want a block and a rule", sc.Text())
		}
		out[fs[0]] = fs[1]
	}
	return out
}

// specRules names the rules of spec/. A planned rule keeps a block pending
// until it's built, such as the blocks that wait for units 3 and 4; a rule
// stating an erratum of the design until the design is corrected.
func specRules(t TB, root string) map[string]bool {
	re := regexp.MustCompile(`(?m)^\s*(\w+): rule(?:<\w+>)?\(`)
	out := map[string]bool{}
	ms, _ := fixtures.SpecFiles(filepath.Join(root, "spec"))
	for _, m := range ms {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range re.FindAllStringSubmatch(string(b), -1) {
			out[g[1]] = true
		}
	}
	return out
}

type fixtureRunner struct {
	t        TB
	root     string
	sessions map[string]*Session
}

func (f *fixtureRunner) newSession() *Session {
	s := NewSession()
	s.Resolver.PackageDirs = []string{filepath.Join(f.root, "fixtures", "context", "packages")}
	return s
}

// checkFixture checks one fixture against its tab's context, if the tab
// has one: fixtures/context/<tab>.heron, whose names are the outermost
// scope of the fixture's tree.
func (f *fixtureRunner) checkFixture(b fixtures.Block) FixtureRun {
	t := f.t
	s := f.sessions[b.Tab]
	if s == nil {
		s = f.newSession()
		ctx := filepath.Join(f.root, "fixtures", "context", b.Tab+".heron")
		if _, err := os.Stat(ctx); err == nil {
			id, _ := resolve.Canonical(ctx)
			cr := s.Check(id)
			if cr.Diags.HasErrors() {
				for _, d := range cr.Diags {
					t.Errorf("context %s: %s", b.Tab, diag.Format(d, s.Pos))
				}
			}
			s.Resolver.Context = cr.Tree.FileScope(id)
		}
		f.sessions[b.Tab] = s
	}
	id, _ := resolve.Canonical(filepath.Join(f.root, "fixtures", b.File))
	if dir := filepath.Join(f.root, "fixtures", "context", "files", b.Tab, b.Key()[len(b.Tab)+1:]); exists(dir) {
		id = f.placeFixture(dir, b)
	}
	r := s.Check(id)
	return FixtureRun{Session: s, Result: r, File: id, Errs: errorsOf(r.Diags)}
}

func errorsOf(ds diag.List) diag.List {
	var out diag.List
	for _, d := range ds {
		if d.Severity == diag.Error {
			out = append(out, d)
		}
	}
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// placeFixture puts a block at the path its tab gives it, next to the
// files it includes or imports that the tab doesn't show: the directory
// fixtures/context/files/<tab>/<NN>/ holds those files, and its fixture.txt
// names the path of the block itself.
func (f *fixtureRunner) placeFixture(dir string, b fixtures.Block) resolve.FileID {
	t := f.t
	tmp := t.TempDir()
	rel, err := os.ReadFile(filepath.Join(dir, "fixture.txt"))
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "fixture.txt" {
			return err
		}
		r, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(tmp, r)
		os.MkdirAll(filepath.Dir(out), 0o755)
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(f.root, "fixtures", b.File))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, strings.TrimSpace(string(rel)))
	os.MkdirAll(filepath.Dir(out), 0o755)
	if err := os.WriteFile(out, src, 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := resolve.Canonical(out)
	return id
}

// checkRepo writes a tab's blocks at the paths its layout gives them and
// checks the tree from its entry point, then every file the tree doesn't
// reach on its own. It returns each block's run.
func (f *fixtureRunner) checkRepo(blocks []fixtures.Block, tab, layout string) map[string]FixtureRun {
	t := f.t
	data, err := os.ReadFile(layout)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	byFile := map[resolve.FileID]string{}
	var entry resolve.FileID
	var order []resolve.FileID
	for _, line := range strings.Split(string(data), "\n") {
		fs := strings.Fields(line)
		if len(fs) != 2 || strings.HasPrefix(fs[0], "#") {
			continue
		}
		out := filepath.Join(tmp, fs[1])
		os.MkdirAll(filepath.Dir(out), 0o755)
		if fs[0] == "entry" {
			entry, _ = resolve.Canonical(out)
			continue
		}
		key := tab + "/" + fs[0]
		for _, b := range blocks {
			if b.Key() == key {
				src, err := os.ReadFile(filepath.Join(f.root, "fixtures", b.File))
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(out, src, 0o644)
			}
		}
		id, _ := resolve.Canonical(out)
		byFile[id] = key
		order = append(order, id)
	}
	s := f.newSession()
	f.sessions[tab] = s
	out := map[string]FixtureRun{}
	for id, key := range byFile {
		out[key] = FixtureRun{Session: s, File: id}
	}
	reached := map[resolve.FileID]bool{}
	collect := func(r *Result) {
		for _, fl := range r.Tree.Files {
			reached[fl.ID] = true
			if key, ok := byFile[fl.ID]; ok && out[key].Result == nil {
				run := out[key]
				run.Result = r
				out[key] = run
			}
		}
		for _, d := range errorsOf(r.Diags) {
			key, ok := byFile[resolve.FileID(d.File)]
			if !ok {
				key = byFile[r.Tree.Entry.ID]
			}
			run := out[key]
			run.Errs = append(run.Errs, d)
			out[key] = run
		}
	}
	collect(s.Check(entry))
	for _, id := range order {
		if !reached[id] {
			collect(s.Check(id))
		}
	}
	return out
}
