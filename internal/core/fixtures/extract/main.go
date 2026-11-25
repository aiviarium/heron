// Command extract writes the fixtures, the code blocks of the spec
// package's example nodes, into a folder, one file per block named after
// its key, with a manifest.json that lists them. Nothing in the repository
// keeps the copy; tools that want the fixtures as files, such as the
// tree-sitter grammar's test, write one to a temporary folder.
//
//	go run ./internal/core/fixtures/extract [-root DIR] -out DIR
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiviarium/heron/internal/core/fixtures"
)

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "", "folder to write the fixtures to")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "extract: -out is required")
		os.Exit(2)
	}
	blocks, err := fixtures.Load(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	type entry struct {
		fixtures.Block
		Out string `json:"out"`
	}
	var manifest []entry
	for _, b := range blocks {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(*out, b.Name()), []byte(b.Content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		manifest = append(manifest, entry{b, b.Name()})
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%d fixtures\n", len(blocks))
}
