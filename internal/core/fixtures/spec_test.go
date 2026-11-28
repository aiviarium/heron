package fixtures

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func root() string { return filepath.Join("..", "..", "..") }

func TestSpecExamplesParse(t *testing.T) {
	src := "x: example {\n  | ```heron\n  | a = 1\n  |\n  |   b {{ c }}\n  | ```\n}\n"
	got := SpecExamples("x.heron", src)
	if len(got) != 1 || got[0].Content != "a = 1\n\n  b {{ c }}\n" || got[0].Name != "x" || got[0].Lang != "heron" || got[0].NameLine != 1 || got[0].Line != 3 {
		t.Fatalf("got %#v", got)
	}
}

// TestFixturesAreTheSpecExamples: every example node of the spec package
// holds at least one code block, each is found by its node's path, and
// the keys are unique.
func TestFixturesAreTheSpecExamples(t *testing.T) {
	r := root()
	blocks, err := Load(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("the spec package has no examples")
	}
	seen := map[string]bool{}
	for _, b := range blocks {
		if seen[b.Key()] {
			t.Errorf("%s: two blocks have this key", b.Key())
		}
		seen[b.Key()] = true
		if !strings.HasPrefix(b.Path, "language.") && !strings.HasPrefix(b.Path, "tools.") && !strings.HasPrefix(b.Path, "cli.") && !strings.HasPrefix(b.Path, "product.") && !strings.HasPrefix(b.Path, "learn.") && !strings.HasPrefix(b.Path, "guide.") {
			t.Errorf("%s (%s): not a path inside the spec package", b.Key(), b.File)
		}
	}
	dir, _ := SpecDir(r)
	files, _ := SpecFiles(dir)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		held := map[int]bool{}
		for _, ex := range SpecExamples(f, string(src)) {
			held[ex.NameLine] = true
		}
		fenced := make([]bool, strings.Count(string(src), "\n")+1)
		if strings.HasSuffix(f, ".heron.md") {
			fenced = InFence(string(src))
		}
		for i, line := range strings.Split(string(src), "\n") {
			m := exampleOpenRE.FindStringSubmatch(line)
			if m == nil && strings.HasSuffix(f, ".heron.md") {
				if em := exampleElementRE.FindStringSubmatch(line); em != nil {
					m = []string{"", "", em[1]}
				}
			}
			if m != nil && !held[i+1] && !fenced[i] && !opExample(lines(string(src)), i) {
				rel, _ := filepath.Rel(dir, f)
				t.Errorf("%s:%d: example %s holds no code block", rel, i+1, m[2])
			}
		}
	}
	t.Logf("%d code blocks of examples", len(blocks))
}

func lines(src string) []string { return strings.Split(src, "\n") }

// opExample reports whether the example opening at line i is an op's
// example, which sets the op's parameters and its outcome as fields
// rather than holding a code block.
func opExample(ls []string, i int) bool {
	for _, l := range ls[i+1:] {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "|") {
			continue
		}
		return regexp.MustCompile(`^[a-z_][a-z0-9_]* = `).MatchString(t)
	}
	return false
}
