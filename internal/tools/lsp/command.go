package lsp

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aiviarium/heron/internal/core/registry"
)

// Register installs `heron lsp`. The session lints with every lint and
// prose provider registered in r, whichever units registered them.
func Register(r *registry.Registry) {
	r.MustAddCommand(registry.Command{
		Name:    "lsp",
		Summary: "Serves the language server over stdio",
		Usage:   "[--debounce duration]",
		Run: func(env *registry.Env, args []string) int {
			fs := flag.NewFlagSet("lsp", flag.ContinueOnError)
			fs.SetOutput(env.Stderr)
			debounce := fs.Duration("debounce", 0, "wait after an edit before rechecking (default 150ms)")
			fs.Bool("stdio", true, "serve over stdin and stdout (the only transport)")
			if err := fs.Parse(args); err != nil {
				return 2
			}
			rw := struct {
				io.Reader
				io.Writer
			}{env.Stdin, env.Stdout}
			err := Serve(context.Background(), rw, Options{Registry: r, Dir: env.Dir, Debounce: *debounce, Log: env.Stderr})
			if err != nil {
				fmt.Fprintln(env.Stderr, "heron lsp:", err)
				return 1
			}
			return 0
		},
	})
}

var _ = os.Stdin
