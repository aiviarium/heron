package fixtures

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SpecExample is a fenced code block in the `|` lines of an example node.
type SpecExample struct {
	File, Name string
	Line       int
	Content    string
}

var (
	exampleOpenRE = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_]*): example \{\s*$`)
	proseFenceRE  = regexp.MustCompile("^(`{3,}|~{3,})[^`\\s]*$")
)

// SpecExamples returns the fenced code blocks held in the `|` lines of the
// `name: example { … }` nodes of a Heron file, with the `|` prefix removed.
func SpecExamples(file, src string) []SpecExample {
	var out []SpecExample
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := exampleOpenRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		indent, name := m[1], m[2]
		var fence string
		var body strings.Builder
		start := 0
		for i++; i < len(lines) && lines[i] != indent+"}"; i++ {
			t := strings.TrimLeft(lines[i], " \t")
			if !strings.HasPrefix(t, "|") {
				continue
			}
			text := strings.TrimPrefix(strings.TrimPrefix(t, "|"), " ")
			switch {
			case fence == "" && proseFenceRE.MatchString(text):
				fence = proseFenceRE.FindStringSubmatch(text)[1]
				body.Reset()
				start = i + 1
			case fence != "" && text == fence:
				out = append(out, SpecExample{File: file, Name: name, Line: start, Content: body.String()})
				fence = ""
			case fence != "":
				body.WriteString(text)
				body.WriteString("\n")
			}
		}
	}
	return out
}

// SpecFiles lists the Heron files of a spec tree, in every folder below
// its root, sorted.
func SpecFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".heron") || strings.HasSuffix(p, ".heron.md")) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
