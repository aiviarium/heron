package fixtures

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// portedTabs are the tabs Heron's own spec under spec/ holds so far. Every
// code block of these tabs must appear in spec/ as an example node.
var portedTabs = []string{
	"core-format",
	"schemas",
	"markup",
	"data",
	"apis",
	"specifications",
	"interop",
	"scripting",
	"implementation",
	"unit-1-language-and-runtime",
	"unit-2-development-tools",
}

// TestSpecExamplesAreFixtures fails when an example in spec/ differs from
// every fixture, or when a code block of a ported tab has no example.
func TestSpecExamplesAreFixtures(t *testing.T) {
	r := root(t)
	blocks, err := Load(filepath.Join(r, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	byContent := map[string][]string{}
	for _, b := range blocks {
		byContent[b.Content] = append(byContent[b.Content], b.Key())
	}
	files, err := SpecFiles(filepath.Join(r, "spec"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(r, f)
		for _, ex := range SpecExamples(rel, string(src)) {
			keys, ok := byContent[ex.Content]
			if !ok {
				t.Errorf("%s:%d: example %s matches no fixture", ex.File, ex.Line, ex.Name)
			}
			for _, k := range keys {
				seen[k] = true
			}
		}
	}
	ported := map[string]bool{}
	for _, tab := range portedTabs {
		ported[tab] = true
	}
	for _, b := range blocks {
		if ported[b.Tab] && !seen[b.Key()] {
			t.Errorf("fixture %s (%s) has no example node in spec/", b.Key(), b.File)
		}
	}
}

func TestSpecExamplesParse(t *testing.T) {
	src := "x: example {\n  | ```heron\n  | a = 1\n  |\n  |   b {{ c }}\n  | ```\n}\n"
	got := SpecExamples("x.heron", src)
	if len(got) != 1 || got[0].Content != "a = 1\n\n  b {{ c }}\n" || got[0].Name != "x" {
		t.Fatalf("got %#v", got)
	}
}

// verifyRE finds the file a `verify(…)` node in spec/ names.
var verifyRE = regexp.MustCompile(`verify\("([^"]+)"`)

// TestSpecVerifyPathsExist fails when a verify link in spec/ names a test
// or script that isn't in the repository.
func TestSpecVerifyPathsExist(t *testing.T) {
	r := root(t)
	files, err := SpecFiles(filepath.Join(r, "spec"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range verifyRE.FindAllStringSubmatch(string(src), -1) {
			n++
			if _, err := os.Stat(filepath.Join(r, m[1])); err != nil {
				rel, _ := filepath.Rel(r, f)
				t.Errorf("%s: verify names %s, which doesn't exist", rel, m[1])
			}
		}
	}
	if n == 0 {
		t.Error("spec/ has no verify links")
	}
}
