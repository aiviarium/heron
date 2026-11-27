package cli

import (
	"fmt"

	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/version"
)

// versionCommand is `heron version`, which `heron --version` answers too.
func versionCommand() registry.Command {
	return registry.Command{
		Name:    "version",
		Summary: "Prints the product version, the spec version it implements and the build's commit, time and platform",
		Usage:   "",
		Run: func(env *registry.Env, args []string) int {
			if len(args) > 0 {
				fmt.Fprintf(env.Stderr, "heron version: takes no arguments or options, not %q\n", args[0])
				return 2
			}
			fmt.Fprint(env.Stdout, version.Read().Lines())
			return 0
		},
	}
}
