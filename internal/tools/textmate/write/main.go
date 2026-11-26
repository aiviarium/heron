// Command write writes the published TextMate grammars under
// grammar/textmate from the repository root.
//
//	go run ./internal/tools/textmate/write
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiviarium/heron/internal/tools/textmate"
)

func main() {
	sc, err := textmate.Scopes(".")
	if err == nil {
		var gs map[string][]byte
		if gs, err = textmate.Grammars(sc); err == nil {
			for name, b := range gs {
				if err = os.WriteFile(filepath.Join(textmate.Dir, name), b, 0o644); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmate:", err)
		os.Exit(1)
	}
}
