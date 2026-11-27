// Package cli dispatches the heron binary's subcommands and registers the
// commands of the language and runtime unit.
package cli

import (
	"fmt"
	"os"

	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/records"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/setup"
)

// Register installs unit 1's commands and core lints.
func Register(r *registry.Registry) {
	r.MustAddCommand(fmtCommand())
	r.MustAddCommand(checkCommand())
	r.MustAddCommand(lintCommand(r))
	r.MustAddCommand(runCommand(r))
	r.MustAddCommand(versionCommand())
	lint.Register(r)
	records.Register(r)
	setup.Register(r, setup.Options{})
}

// Main runs the subcommand named by args[0] and returns the exit code.
func Main(r *registry.Registry, args []string, env *registry.Env) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 {
			if c := r.Command(args[1]); c != nil {
				fmt.Fprintf(env.Stdout, "usage: heron %s %s\n\n%s\n", c.Name, c.Usage, c.Summary)
				return 0
			}
		}
		usage(r, env)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if args[0] == "--version" {
		args[0] = "version"
	}
	c := r.Command(args[0])
	if c == nil {
		fmt.Fprintf(env.Stderr, "heron: unknown command %q\n", args[0])
		usage(r, env)
		return 2
	}
	return c.Run(env, args[1:])
}

func usage(r *registry.Registry, env *registry.Env) {
	fmt.Fprintln(env.Stderr, "usage: heron <command> [arguments]")
	fmt.Fprintln(env.Stderr)
	for _, c := range r.Commands() {
		fmt.Fprintf(env.Stderr, "  %-10s %s\n", c.Name, c.Summary)
	}
}

// OSEnv is the environment of the running process.
func OSEnv() *registry.Env {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	return &registry.Env{Dir: dir, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}
