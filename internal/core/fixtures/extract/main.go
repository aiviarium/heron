// Command extract regenerates fixtures/ from docs/design/*.md.
//
//	go run ./internal/core/fixtures/extract [-root DIR]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/internal/core/fixtures"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	out := filepath.Join(*root, "fixtures")
	files, blocks, err := fixtures.Generate(filepath.Join(*root, "docs", "design"), filepath.Join(out, "modes.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Remove generated files that no longer exist; keep modes.txt.
	_ = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(out, p)
		rel = filepath.ToSlash(rel)
		if _, ok := files[rel]; !ok && rel != "modes.txt" && !strings.HasPrefix(rel, ".") {
			os.Remove(p)
		}
		return nil
	})
	for rel, data := range files {
		p := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("%d fixtures\n", len(blocks))
}
