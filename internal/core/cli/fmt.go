package cli

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/internal/core/format"
	"github.com/aiviarium/heron/internal/core/registry"
)

func fmtCommand() registry.Command {
	return registry.Command{
		Name:    "fmt",
		Summary: "Formats Heron files from the lossless syntax tree",
		Usage:   "[--check] [--stdout] [paths…]",
		Run:     runFmt,
	}
}

// isHeronFile reports whether name is a Heron source or prose file.
func isHeronFile(name string) bool {
	return strings.HasSuffix(name, ".heron") || strings.HasSuffix(name, ".heron.md")
}

// heronFiles expands paths into Heron files; directories are walked, and
// directories starting with "." or named node_modules are skipped.
func heronFiles(dir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var out []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != p && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && isHeronFile(d.Name()) {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func runFmt(env *registry.Env, args []string) int {
	fl := flag.NewFlagSet("fmt", flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	check := fl.Bool("check", false, "list files that aren't formatted and exit 1, changing nothing")
	stdout := fl.Bool("stdout", false, "print the formatted files instead of rewriting them")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	files, err := heronFiles(env.Dir, fl.Args())
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron fmt:", err)
		return 1
	}
	status := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(env.Stderr, "heron fmt:", err)
			status = 1
			continue
		}
		out := format.Source(f, string(src))
		rel, _ := filepath.Rel(env.Dir, f)
		switch {
		case *stdout:
			fmt.Fprint(env.Stdout, out)
		case out == string(src):
		case *check:
			fmt.Fprintln(env.Stdout, rel)
			status = 1
		default:
			if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
				fmt.Fprintln(env.Stderr, "heron fmt:", err)
				status = 1
			}
		}
	}
	return status
}
