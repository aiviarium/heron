// Command specdir prints the folder of the spec package the repository
// reads: $HERON_SPECS_DIR, or the one installed under .heron/packages/.
//
//	go run ./internal/core/fixtures/specdir [-root DIR]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aiviarium/heron/internal/core/fixtures"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	d, err := fixtures.SpecDir(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(d)
}
