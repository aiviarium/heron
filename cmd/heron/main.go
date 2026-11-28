// Command heron is the single binary of the Heron toolchain. It only wires
// the units' registrations together; every command lives in a unit.
//
// Each unit after the first installs itself from its own file, so a prefix
// of the units builds by leaving the later files out: `go build -tags unit1`
// gives the language and runtime alone.
package main

import (
	"os"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
)

// units holds each installed unit's registrations, in unit order. Unit 1 is
// always present; later units append theirs in init.
var units = []func(*registry.Registry){
	cli.Register, // 1. language and runtime
	gen.Register, //    generation targets and `heron generate`
}

func main() {
	r := registry.New()
	for _, register := range units {
		register(r)
	}
	os.Exit(cli.Main(r, os.Args[1:], cli.OSEnv()))
}
