// Command heronwords is the heron binary with the sample Words prose
// provider registered, the way a later unit registers its own. The words
// come from HERON_WORDS, such as "simply=,utilize=use". scripts/e2e-prose.sh
// drives it.
package main

import (
	"os"

	"github.com/helightdev/specs-v0/internal/core/cli"
	"github.com/helightdev/specs-v0/internal/core/lint/prose/words"
	"github.com/helightdev/specs-v0/internal/core/registry"
)

func main() {
	r := registry.New()
	cli.Register(r)
	r.MustAddProseProvider(words.Provider(words.Parse(os.Getenv("HERON_WORDS"))))
	os.Exit(cli.Main(r, os.Args[1:], cli.OSEnv()))
}
