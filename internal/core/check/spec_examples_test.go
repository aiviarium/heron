package check

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aiviarium/heron/internal/core/fixtures"
)

// The Heron inside spec/'s example nodes is checked as its fixture is: each
// example of a unit-1 tab's Heron block links to that block's run, which
// checks clean or is pending on an open question. Examples of other tabs'
// blocks, and of blocks that aren't Heron, are checked by the units they
// belong to.
func TestSpecExamplesCheck(t *testing.T) {
	root := repoRoot()
	runs := map[string]FixtureRun{}
	for _, r := range CheckFixtures(t, root) {
		runs[r.Block.Content] = r
	}
	blocks, err := fixtures.Load(filepath.Join(root, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	byContent := map[string][]fixtures.Block{}
	for _, b := range blocks {
		byContent[b.Content] = append(byContent[b.Content], b)
	}
	files, _ := fixtures.SpecFiles(filepath.Join(root, "spec"))
	sort.Strings(files)
	clean, pending, other := 0, 0, map[string]int{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, f)
		for _, ex := range fixtures.SpecExamples(rel, string(src)) {
			checked := false
			for _, b := range byContent[ex.Content] {
				if !UnitOneTabs[b.Tab] || (b.Mode != fixtures.ModeHeron && b.Mode != fixtures.ModeProse) {
					continue
				}
				r, ok := runs[ex.Content]
				if !ok {
					t.Errorf("%s:%d: example %s is fixture %s, which wasn't checked", ex.File, ex.Line, ex.Name, b.Key())
					continue
				}
				checked = true
				switch {
				case len(r.Errs) == 0:
					clean++
				case r.Pending != "":
					pending++
				default:
					t.Errorf("%s:%d: example %s doesn't check: %s", ex.File, ex.Line, ex.Name, r.Errs[0].Message)
				}
				break
			}
			if !checked {
				key := "unmatched"
				if bs := byContent[ex.Content]; len(bs) > 0 {
					key = bs[0].Tab + "/" + bs[0].Mode
				}
				other[key]++
			}
		}
	}
	if clean == 0 {
		t.Fatal("no spec example was checked")
	}
	t.Logf("spec examples: %d check clean, %d pending, not unit 1's Heron: %v", clean, pending, other)
}
