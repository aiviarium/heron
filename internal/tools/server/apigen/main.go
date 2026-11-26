// Command apigen reads the server's API, declared in Heron as `spec_server`,
// through the same check every Heron tree goes through, and writes the Go
// handlers and the TS client of its ops. A later unit runs it on its own
// files, with the base API beside them, and gets the code for its own ops
// only, which the server shell mounts like the base.
//
//	go run ./internal/tools/server/apigen \
//	    -go internal/tools/server/api/api_gen.go -pkg api \
//	    -ts web/shell/src/api.gen.ts
//
// The API is the spec package's: its tools/server_api folder, checked in
// the package's tree, which -base names ($HERON_SPECS_DIR's index.heron,
// or the installed package's, unless given). With -ext, the named files
// join the tree and only what they declare is written.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/tools/server/apigen/spec"
)

func main() {
	base := flag.String("base", "", "the entry of the tree that declares spec_server (default: the spec package's index.heron)")
	root := flag.String("root", ".", "the repository root, whose spec package -base defaults to")
	ext := flag.String("ext", "", "comma-separated files that add ops to spec_server; only they are written")
	goOut := flag.String("go", "", "the Go file to write")
	pkg := flag.String("pkg", "api", "the Go package name")
	tsOut := flag.String("ts", "", "the TS file to write")
	flag.Parse()
	if *base == "" {
		dir, err := fixtures.SpecDir(*root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "apigen:", err)
			os.Exit(1)
		}
		*base = filepath.Join(dir, "index.heron")
	}
	var exts []string
	if *ext != "" {
		exts = strings.Split(*ext, ",")
	}
	a, err := spec.Load(*base, exts)
	if err == nil && *goOut != "" {
		var src []byte
		if src, err = spec.Go(a, *pkg); err == nil {
			err = write(*goOut, src)
		}
	}
	if err == nil && *tsOut != "" {
		err = write(*tsOut, spec.TS(a))
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
