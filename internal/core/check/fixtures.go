package check

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/resolve"
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

// FixtureRun is one checked Heron example of the spec package.
type FixtureRun struct {
	Block   fixtures.Block
	Session *Session
	// Result is the check whose tree holds the block's file, and File
	// that file.
	Result *Result
	File   resolve.FileID
	// Errs are the block's errors.
	Errs diag.List
	// Pending is the reason fixtures/pending.txt gives for the block not
	// checking clean yet, or "".
	Pending string
}

// CheckFixtures checks every Heron code block of the spec package's
// examples, each in the context fixtures/contexts.txt gives it, and
// reports a pending list that names a key that's no checked block.
func CheckFixtures(t TB, root string) []FixtureRun {
	t.Helper()
	blocks, err := fixtures.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	pend := readList(t, root, "pending.txt")
	contexts := readList(t, root, "contexts.txt")
	f := &fixtureRunner{t: t, root: root, sessions: map[string]*Session{}, blocks: blocks}
	var out []FixtureRun
	seen := map[string]bool{}
	for _, b := range blocks {
		if b.Mode != fixtures.ModeHeron && b.Mode != fixtures.ModeProse {
			continue
		}
		run := f.checkFixture(b, contexts[b.Key()])
		run.Block, run.Pending = b, pend[b.Key()]
		seen[b.Key()] = true
		out = append(out, run)
	}
	for _, list := range []struct {
		name string
		m    map[string]string
	}{{"pending.txt", pend}, {"contexts.txt", contexts}} {
		for k := range list.m {
			if !seen[k] {
				t.Errorf("fixtures/%s lists %s, which is no Heron example of the spec", list.name, k)
			}
		}
	}
	return out
}

// readList reads a list of fixtures/: one example key per line, then the
// rest of the line, such as a context's name or a reason. Lines that start
// with # are comments.
func readList(t TB, root, name string) map[string]string {
	f, err := os.Open(filepath.Join(root, "fixtures", name))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}
		}
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		if rest = strings.TrimSpace(rest); rest == "" {
			t.Fatalf("fixtures/%s: %q: want an example's key and then text", name, sc.Text())
		}
		out[key] = rest
	}
	return out
}

type fixtureRunner struct {
	t        TB
	root     string
	sessions map[string]*Session
	blocks   []fixtures.Block
	// contextDir is a copy of fixtures/context/ with every example of the
	// spec beside it as examples/<name>, so a context can include an
	// example it builds on, as "./examples/language.data.types.data_types.heron".
	contextDir string
}

// contexts returns the folder the context files are read from.
func (f *fixtureRunner) contexts() string {
	if f.contextDir != "" {
		return f.contextDir
	}
	t := f.t
	dir := t.TempDir()
	src := filepath.Join(f.root, "fixtures", "context")
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(dir, "examples"), 0o755)
	for _, b := range f.blocks {
		if err := os.WriteFile(filepath.Join(dir, "examples", b.Name()), []byte(b.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.contextDir = dir
	return dir
}

func (f *fixtureRunner) newSession() *Session {
	s := NewSession()
	s.Resolver.PackageDirs = []string{filepath.Join(f.root, "fixtures", "context", "packages")}
	return s
}

// checkFixture checks one fixture against its context, if it has one:
// fixtures/context/<context>.heron, whose names are the outermost scope of
// the fixture's tree, and which may include the spec's examples it builds
// on from ./examples/. Blocks with one context share a session.
func (f *fixtureRunner) checkFixture(b fixtures.Block, context string) FixtureRun {
	t := f.t
	s := f.sessions[context]
	if s == nil {
		s = f.newSession()
		if context != "" {
			ctx := filepath.Join(f.contexts(), context+".heron")
			if _, err := os.Stat(ctx); err != nil {
				t.Fatalf("fixtures/contexts.txt: %s names %s, which isn't in fixtures/context/", b.Key(), context)
			}
			id, _ := resolve.Canonical(ctx)
			cr := s.Check(id)
			if cr.Diags.HasErrors() {
				for _, d := range cr.Diags {
					t.Errorf("context %s: %s", context, diag.Format(d, s.Pos))
				}
			}
			s.Resolver.Context = cr.Tree.FileScope(id)
		}
		f.sessions[context] = s
	}
	var id resolve.FileID
	if dir := filepath.Join(f.root, "fixtures", "context", "files", b.Key()); exists(dir) {
		id = f.placeFixture(dir, b)
	} else {
		out := filepath.Join(t.TempDir(), b.Name())
		if err := os.WriteFile(out, []byte(b.Content), 0o644); err != nil {
			t.Fatal(err)
		}
		id, _ = resolve.Canonical(out)
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
// fixtures/context/files/<example key>/ holds those files, and its
// fixture.txt names the path of the block itself.
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
	src := []byte(b.Content)
	out := filepath.Join(tmp, strings.TrimSpace(string(rel)))
	os.MkdirAll(filepath.Dir(out), 0o755)
	if err := os.WriteFile(out, src, 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := resolve.Canonical(out)
	return id
}
