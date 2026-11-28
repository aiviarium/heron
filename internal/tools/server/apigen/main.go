// Command apigen reads the server's API, declared in Heron as `spec_server`,
// through the same check every Heron tree goes through, and writes the Go
// handlers of its ops.
//
//	go run ./internal/tools/server/apigen \
//	    -go internal/tools/server/api/api_gen.go -pkg api
//
// The API is the spec package's: its tools/server_api folder, checked in
// the package's tree, which -base names ($HERON_SPECS_DIR's index.heron,
// or the installed package's, unless given).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/tools/server/apigen/spec"
)

func main() {
	base := flag.String("base", "", "the entry of the tree that declares spec_server (default: the spec package's index.heron)")
	root := flag.String("root", ".", "the repository root, whose spec package -base defaults to")
	goOut := flag.String("go", "", "the Go file to write")
	pkg := flag.String("pkg", "api", "the Go package name")
	flag.Parse()
	if *base == "" {
		dir, err := fixtures.SpecDir(*root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "apigen:", err)
			os.Exit(1)
		}
		*base = filepath.Join(dir, "index.heron")
	}
	a, err := spec.Load(*base)
	if err == nil && *goOut != "" {
		var src []byte
		if src, err = spec.Go(a, *pkg); err == nil {
			err = write(*goOut, src)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "apigen:", err)
		os.Exit(1)
	}
}

func write(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
