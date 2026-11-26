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
	// NameLine is the line of `name: example {`, Line the block's first.
	NameLine, Line int
	Lang           string // the fence's info word, or ""
	Content        string
}

var (
	exampleOpenRE    = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_]*): example \{\s*$`)
	exampleElementRE = regexp.MustCompile(`^\s*<example\s+id="([A-Za-z_][A-Za-z0-9_]*)"`)
	proseFenceRE     = regexp.MustCompile("^(`{3,}|~{3,})([^`\\s]*)$")
)

// SpecExamples returns the fenced code blocks held in the `|` lines of the
// `name: example { … }` nodes of a Heron file, with the `|` prefix removed.
func SpecExamples(file, src string) []SpecExample {
	if strings.HasSuffix(file, ".heron.md") {
		return elementExamples(file, src)
	}
	var out []SpecExample
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := exampleOpenRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		indent, name, nameLine := m[1], m[2], i+1
		var fence, lang string
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
				fm := proseFenceRE.FindStringSubmatch(text)
				fence, lang = fm[1], fm[2]
				body.Reset()
				start = i + 2
			case fence != "" && text == fence:
				out = append(out, SpecExample{File: file, Name: name, NameLine: nameLine, Line: start, Lang: lang, Content: body.String()})
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
// its root, sorted. Folders whose names start with a dot, such as a
// package's rendered .heron/, are skipped.
func SpecFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".heron") || strings.HasSuffix(p, ".heron.md")) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// elementExamples returns the fenced code blocks of the `<example id="…">`
// elements of a prose file, as written, up to each one's `</example>`.
func elementExamples(file, src string) []SpecExample {
	var out []SpecExample
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := exampleElementRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name, nameLine := m[1], i+1
		var fence, lang string
		var body strings.Builder
		start := 0
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "</example>"; i++ {
			text := lines[i]
			switch {
			case fence == "" && proseFenceRE.MatchString(strings.TrimSpace(text)):
				fm := proseFenceRE.FindStringSubmatch(strings.TrimSpace(text))
				fence, lang = fm[1], fm[2]
				body.Reset()
				start = i + 2
			case fence != "" && strings.TrimSpace(text) == fence:
				out = append(out, SpecExample{File: file, Name: name, NameLine: nameLine, Line: start, Lang: lang, Content: body.String()})
				fence = ""
			case fence != "":
				body.WriteString(text)
				body.WriteString("\n")
			}
		}
	}
	return out
}

// InFence reports, for each line of a prose file, whether it's inside a
// fenced block, where a line that reads like a declaration is code.
func InFence(src string) []bool {
	lines := strings.Split(src, "\n")
	out := make([]bool, len(lines))
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case fence == "" && proseFenceRE.MatchString(t):
			fence = proseFenceRE.FindStringSubmatch(t)[1]
			out[i] = true
		case fence != "":
			out[i] = true
			if t == fence {
				fence = ""
			}
		}
	}
	return out
}
