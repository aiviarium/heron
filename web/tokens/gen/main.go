// Command gen writes tokens.css and src/index.ts from tokens.json. Run it
// from web/tokens, as `go generate` does.
package main

import (
	"log"
	"os"

	"github.com/helightdev/specs-v0/web/tokens"
)

func main() {
	src, err := os.ReadFile("tokens.json")
	if err != nil {
		log.Fatal(err)
	}
	s, err := tokens.Parse(src)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("tokens.css", []byte(s.GenerateCSS()), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("src/index.ts", []byte(s.GenerateTS()), 0o644); err != nil {
		log.Fatal(err)
	}
}
