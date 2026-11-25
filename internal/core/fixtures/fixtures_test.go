package fixtures

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func root(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..")
}

// TestNoDrift fails when fixtures/ differs from what docs/design produces.
func TestNoDrift(t *testing.T) {
	r := root(t)
	dir := filepath.Join(r, "fixtures")
	files, _, err := Generate(filepath.Join(r, "docs", "design"), filepath.Join(dir, "modes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v (run go run ./internal/core/fixtures/extract)", rel, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s drifted from docs/design (run go run ./internal/core/fixtures/extract)", rel)
		}
	}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if _, ok := files[rel]; !ok && rel != "modes.txt" && rel != "pending.txt" && !strings.HasPrefix(rel, "context/") {
			t.Errorf("%s is not produced by docs/design", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExtractIndentedAndLongFences(t *testing.T) {
	src := "a\n\n````text\nx\n```\ny\n````\n\n- item\n\n  ```\n  b: kind\n  ```\n"
	bs := Extract("t", src)
	if len(bs) != 2 {
		t.Fatalf("got %d blocks", len(bs))
	}
	if bs[0].Content != "x\n```\ny\n" || bs[0].Lang != "text" {
		t.Errorf("block 1: %q %q", bs[0].Content, bs[0].Lang)
	}
	if bs[1].Content != "b: kind\n" {
		t.Errorf("block 2: %q", bs[1].Content)
	}
}
